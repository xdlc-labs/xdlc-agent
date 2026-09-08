package poller

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/gate"
	"github.com/xdlc-labs/xdlc-agent/internal/orchestrator"
)

// fakeGate returns a fixed Result (or error) per repo, recording calls.
type fakeGate struct {
	results map[string]gate.Result
	errs    map[string]error
	mu      sync.Mutex
	calls   []string
}

func (f *fakeGate) Name() string              { return "fake" }
func (f *fakeGate) Trigger() gate.TriggerKind { return gate.Continuous }
func (f *fakeGate) Check(_ context.Context, repo string) (gate.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, repo)
	f.mu.Unlock()
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

	p.tick(context.Background(), 30*time.Second)
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
	// A Check that errored has no verdict. It used to emit nothing at
	// all, which left a typo'd argocd_app invisible outside the daemon
	// log (issue #45); it now emits KindBlocked, which is ActionNoop —
	// never the KindBreach/KindFail that would revert or Fix.
	if got["repo-err"] != orchestrator.KindBlocked {
		t.Errorf("repo-err kind = %v, want %v", got["repo-err"], orchestrator.KindBlocked)
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

	p.tick(context.Background(), 30*time.Second)
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

	p.tick(context.Background(), 30*time.Second) // first pass → emit
	p.tick(context.Background(), 30*time.Second) // same pass → suppress
	fg.results["svc"] = gate.Result{Status: gate.StatusFail}
	p.tick(context.Background(), 30*time.Second) // fail → emit
	p.tick(context.Background(), 30*time.Second) // same fail → suppress
	fg.results["svc"] = gate.Result{Status: gate.StatusPass}
	p.tick(context.Background(), 30*time.Second) // pass again → emit

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

	p.tick(context.Background(), 30*time.Second)
	if len(order) != 2 || order[0] != "sha" || order[1] != "check" {
		t.Fatalf("order = %v, want the sha read before the probe", order)
	}
	got := <-signals
	if got.SHA != shaA {
		t.Errorf("signal sha = %q, want %q", got.SHA, shaA)
	}

	// Same kind, same commit → still suppressed.
	p.tick(context.Background(), 30*time.Second)
	if len(signals) != 0 {
		t.Fatalf("re-emitted for an unchanged commit: %+v", <-signals)
	}

	// A *new* commit that also passes is a new thing to promote, even
	// though the Kind never changed — keying the edge on Kind alone
	// would leave it gated but never shipped.
	const shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	sha = shaB
	p.tick(context.Background(), 30*time.Second)
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

	p.tick(context.Background(), 30*time.Second)
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

// hangingGate blocks until its context is cancelled — a Prometheus (or
// ArgoCD) that accepts the connection and never answers.
type hangingGate struct {
	name  string
	mu    sync.Mutex
	calls int
}

func (h *hangingGate) Name() string              { return h.name }
func (h *hangingGate) Trigger() gate.TriggerKind { return gate.Continuous }
func (h *hangingGate) Check(ctx context.Context, _ string) (gate.Result, error) {
	h.mu.Lock()
	h.calls++
	h.mu.Unlock()
	<-ctx.Done()
	return gate.Result{}, ctx.Err()
}

func (h *hangingGate) callCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

func TestTickTimeout(t *testing.T) {
	cases := []struct {
		name     string
		timeout  time.Duration
		interval time.Duration
		want     time.Duration
	}{
		{"explicit timeout wins", 5 * time.Second, 30 * time.Second, 5 * time.Second},
		{"derived from interval", 0, 30 * time.Second, 24 * time.Second},
		{"derived from a short interval", 0, 3 * time.Second, 2400 * time.Millisecond},
		{"no interval falls back", 0, 0, defaultTickTimeout},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &Poller{Timeout: c.timeout}
			got := p.tickTimeout(c.interval)
			if got != c.want {
				t.Fatalf("tickTimeout(%v) = %v, want %v", c.interval, got, c.want)
			}
			// The derived default has to stay under the interval, or
			// ticks overrun each other.
			if c.timeout == 0 && c.interval > 0 && got >= c.interval {
				t.Fatalf("derived timeout %v is not under the interval %v", got, c.interval)
			}
		})
	}
}

// TestPollerTickBoundedByTimeout is the "hung Prometheus silently
// disables the gate" regression. Gate.Check used to inherit the daemon
// root context, and tick is called synchronously from the ticker loop,
// so one unanswered query parked the loop for the life of the process:
// exactly one query ever, no further ticks, and nothing logged (the
// "tick slow" warn sits after the wait).
func TestPollerTickBoundedByTimeout(t *testing.T) {
	hg := &hangingGate{name: "prod-health"}
	var logs bytes.Buffer
	p := &Poller{
		Gate:    hg,
		Repos:   []string{"svc"},
		Source:  orchestrator.SourceProdHealth,
		Signals: make(chan orchestrator.Signal, 1),
		Log:     slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Timeout: 50 * time.Millisecond,
	}

	start := time.Now()
	p.tick(context.Background(), time.Second)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("tick was not bounded by Timeout: took %v", elapsed)
	}
	if hg.callCount() != 1 {
		t.Fatalf("gate calls = %d, want 1", hg.callCount())
	}

	// The whole point: the operator can see it. A tick whose verdict is
	// "unknown" must not look like a healthy one.
	out := logs.String()
	if !strings.Contains(out, "poller tick timed out") {
		t.Errorf("no tick-timeout signal in the log:\n%s", out)
	}
	if !strings.Contains(out, "gate check failed") {
		t.Errorf("no per-repo check failure in the log:\n%s", out)
	}

	// And the next tick still runs, rather than the gate being dead.
	p.tick(context.Background(), time.Second)
	if hg.callCount() != 2 {
		t.Fatalf("gate calls after a second tick = %d, want 2", hg.callCount())
	}
}

// TestPollerRunKeepsTickingThroughAHang: end to end through Run, the
// loop a hung gate used to wedge.
func TestPollerRunKeepsTickingThroughAHang(t *testing.T) {
	hg := &hangingGate{name: "prod-health"}
	p := &Poller{
		Gate:     hg,
		Repos:    []string{"svc"},
		Interval: 20 * time.Millisecond,
		Source:   orchestrator.SourceProdHealth,
		Signals:  make(chan orchestrator.Signal, 1),
		Log:      silentLogger(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	p.Run(ctx)
	if got := hg.callCount(); got < 3 {
		t.Fatalf("gate calls in 300ms of 20ms ticks = %d, want several (the loop was wedged)", got)
	}
}

// TestPollerTickTimeoutIgnoredOnShutdown: a cancelled parent context is
// a shutdown, not a gate problem, and must not log a timeout.
func TestPollerTickTimeoutIgnoredOnShutdown(t *testing.T) {
	hg := &hangingGate{name: "prod-health"}
	var logs bytes.Buffer
	p := &Poller{
		Gate:    hg,
		Repos:   []string{"svc"},
		Source:  orchestrator.SourceProdHealth,
		Signals: make(chan orchestrator.Signal, 1),
		Log:     slog.New(slog.NewTextHandler(&logs, nil)),
		Timeout: 5 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	p.tick(ctx, time.Minute)
	if strings.Contains(logs.String(), "poller tick timed out") {
		t.Errorf("reported a tick timeout for a daemon shutdown:\n%s", logs.String())
	}
}
