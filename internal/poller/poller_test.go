package poller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/xdlc-labs/xdlc-agent/internal/gate"
	"github.com/xdlc-labs/xdlc-agent/internal/orchestrator"
)

// fakeGate returns a fixed Result (or error) per repo, recording calls.
type fakeGate struct {
	results map[string]gate.Result
	errs    map[string]error
	calls   []string
}

func (f *fakeGate) Name() string              { return "fake" }
func (f *fakeGate) Trigger() gate.TriggerKind { return gate.Continuous }
func (f *fakeGate) Check(_ context.Context, repo string) (gate.Result, error) {
	f.calls = append(f.calls, repo)
	if err, ok := f.errs[repo]; ok {
		return gate.Result{}, err
	}
	return f.results[repo], nil
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestPollerTick(t *testing.T) {
	fg := &fakeGate{
		results: map[string]gate.Result{
			"repo-pass": {Status: gate.StatusPass},
			"repo-fail": {Status: gate.StatusFail},
		},
		errs: map[string]error{
			"repo-err": errors.New("boom"),
		},
	}

	signals := make(chan orchestrator.Signal, 10)
	p := &Poller{
		Gate:    fg,
		Repos:   []string{"repo-pass", "repo-fail", "repo-err"},
		Source:  orchestrator.SourceProdHealth,
		Signals: signals,
		Log:     silentLogger(),
	}

	p.tick(context.Background())
	close(signals)

	got := map[string]orchestrator.Kind{}
	for s := range signals {
		got[s.Repo] = s.Kind
	}

	if got["repo-pass"] != orchestrator.KindPass {
		t.Errorf("repo-pass kind = %v, want %v", got["repo-pass"], orchestrator.KindPass)
	}
	// prod-health fail maps to "breach", not "fail"
	if got["repo-fail"] != orchestrator.KindBreach {
		t.Errorf("repo-fail kind = %v, want %v", got["repo-fail"], orchestrator.KindBreach)
	}
	if _, ok := got["repo-err"]; ok {
		t.Errorf("repo-err should not emit a signal (Check errored), got %v", got["repo-err"])
	}
	if len(fg.calls) != 3 {
		t.Errorf("expected 3 Check calls, got %d: %v", len(fg.calls), fg.calls)
	}
}

func TestPollerTickNonProdHealthFailMapsToFail(t *testing.T) {
	fg := &fakeGate{
		results: map[string]gate.Result{"repo-fail": {Status: gate.StatusFail}},
	}
	signals := make(chan orchestrator.Signal, 1)
	p := &Poller{
		Gate:    fg,
		Repos:   []string{"repo-fail"},
		Source:  orchestrator.SourceDevGate,
		Signals: signals,
		Log:     silentLogger(),
	}

	p.tick(context.Background())
	close(signals)

	s := <-signals
	if s.Kind != orchestrator.KindFail {
		t.Errorf("dev-gate fail kind = %v, want %v", s.Kind, orchestrator.KindFail)
	}
}

func TestPollerTickEdgeTriggered(t *testing.T) {
	fg := &fakeGate{
		results: map[string]gate.Result{
			"svc": {Status: gate.StatusPass},
		},
	}
	signals := make(chan orchestrator.Signal, 10)
	p := &Poller{
		Gate:    fg,
		Repos:   []string{"svc"},
		Source:  orchestrator.SourceDevGate,
		Signals: signals,
		Log:     silentLogger(),
	}

	p.tick(context.Background()) // first pass → emit
	p.tick(context.Background()) // same pass → suppress
	fg.results["svc"] = gate.Result{Status: gate.StatusFail}
	p.tick(context.Background()) // fail → emit
	p.tick(context.Background()) // same fail → suppress
	fg.results["svc"] = gate.Result{Status: gate.StatusPass}
	p.tick(context.Background()) // pass again → emit

	close(signals)
	var kinds []orchestrator.Kind
	for s := range signals {
		kinds = append(kinds, s.Kind)
	}
	want := []orchestrator.Kind{
		orchestrator.KindPass,
		orchestrator.KindFail,
		orchestrator.KindPass,
	}
	if len(kinds) != len(want) {
		t.Fatalf("got %d signals %v, want %v", len(kinds), kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Errorf("signal[%d] = %v, want %v", i, kinds[i], want[i])
		}
	}
}

// TestPollerPinsGatedSHA: a dev-smoke pass authorizes a promote, so the
// Signal has to name the commit that was probed (S3). The SHA is read
// before Check, so a commit landing mid-probe can't inherit its verdict.
func TestPollerPinsGatedSHA(t *testing.T) {
	const shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sha := shaA
	var order []string

	fg := &fakeGate{results: map[string]gate.Result{"svc": {Status: gate.StatusPass}}}
	// Wrap Check so the call order is observable.
	signals := make(chan orchestrator.Signal, 4)
	p := &Poller{
		Gate:    recordingGate{fg, func() { order = append(order, "check") }},
		Repos:   []string{"svc"},
		Source:  orchestrator.SourceDevGate,
		Signals: signals,
		Log:     silentLogger(),
		SHA: func(context.Context, string) (string, error) {
			order = append(order, "sha")
			return sha, nil
		},
	}

	p.tick(context.Background())
	if len(order) != 2 || order[0] != "sha" || order[1] != "check" {
		t.Fatalf("order = %v, want the sha read before the probe", order)
	}
	got := <-signals
	if got.SHA != shaA {
		t.Errorf("signal sha = %q, want %q", got.SHA, shaA)
	}

	// Same kind, same commit → still suppressed.
	p.tick(context.Background())
	if len(signals) != 0 {
		t.Fatalf("re-emitted for an unchanged commit: %+v", <-signals)
	}

	// A *new* commit that also passes is a new thing to promote, even
	// though the Kind never changed — keying the edge on Kind alone
	// would leave it gated but never shipped.
	const shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	sha = shaB
	p.tick(context.Background())
	got = <-signals
	if got.SHA != shaB {
		t.Errorf("signal sha = %q, want %q for the new commit", got.SHA, shaB)
	}
}

// TestPollerSkipsRepoWhenSHAUnresolvable: an unattributable pass would
// promote whatever the branch tip happens to be, so skip the tick.
func TestPollerSkipsRepoWhenSHAUnresolvable(t *testing.T) {
	fg := &fakeGate{results: map[string]gate.Result{"svc": {Status: gate.StatusPass}}}
	signals := make(chan orchestrator.Signal, 1)
	p := &Poller{
		Gate:    fg,
		Repos:   []string{"svc"},
		Source:  orchestrator.SourceDevGate,
		Signals: signals,
		Log:     silentLogger(),
		SHA:     func(context.Context, string) (string, error) { return "", errors.New("ls-remote failed") },
	}

	p.tick(context.Background())
	if len(signals) != 0 {
		t.Fatalf("emitted an unpinned dev-gate signal: %+v", <-signals)
	}
	if len(fg.calls) != 0 {
		t.Errorf("ran the gate anyway: %v", fg.calls)
	}
}

// recordingGate notes each Check call before delegating.
type recordingGate struct {
	gate.Gate
	onCheck func()
}

func (g recordingGate) Check(ctx context.Context, repo string) (gate.Result, error) {
	g.onCheck()
	return g.Gate.Check(ctx, repo)
}
