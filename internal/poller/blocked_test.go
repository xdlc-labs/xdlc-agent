package poller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/gate"
	"github.com/xdlc-labs/xdlc-agent/internal/orchestrator"
)

// erroringGate returns whatever error is currently set, so a test can
// change the failure mode between ticks.
type erroringGate struct {
	mu    sync.Mutex
	err   error
	calls int
}

func (g *erroringGate) Name() string              { return "dev-smoke" }
func (g *erroringGate) Trigger() gate.TriggerKind { return gate.OnSync }
func (g *erroringGate) Check(context.Context, string) (gate.Result, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	if g.err != nil {
		return gate.Result{}, g.err
	}
	return gate.Result{Status: gate.StatusPass}, nil
}

func (g *erroringGate) set(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.err = err
}

func drain(ch chan orchestrator.Signal) []orchestrator.Signal {
	var out []orchestrator.Signal
	for {
		select {
		case s := <-ch:
			out = append(out, s)
		default:
			return out
		}
	}
}

// TestBlockedGateEmitsAnOperatorVisibleSignal: a dev-smoke gate whose
// `argocd app get` failed used to produce no Signal at all — no audit
// row, no BACKLOG.md line, nothing but a daemon log line — and Promote
// simply never fired again (issue #45).
func TestBlockedGateEmitsAnOperatorVisibleSignal(t *testing.T) {
	eg := &erroringGate{err: errors.New(
		`smoke gate: argocd health: gitops: argocd app get dev-typo: exit status 20: applications.argoproj.io "dev-typo" not found`)}
	ch := make(chan orchestrator.Signal, 8)
	var logs bytes.Buffer
	p := &Poller{
		Gate:    eg,
		Repos:   []string{"svc"},
		Source:  orchestrator.SourceDevGate,
		Signals: ch,
		Log:     slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		SHA:     func(context.Context, string) (string, error) { return "abc1234", nil },
	}

	p.tick(context.Background(), 30*time.Second)

	got := drain(ch)
	if len(got) != 1 {
		t.Fatalf("emitted %d signals, want 1: %+v", len(got), got)
	}
	sig := got[0]
	if sig.Kind != orchestrator.KindBlocked {
		t.Fatalf("kind = %s, want %s", sig.Kind, orchestrator.KindBlocked)
	}
	// Distinguishable from a fail, and specifically not the kind that
	// would spend a coding-agent run on a repo that is not broken.
	if orchestrator.Decide(sig) != orchestrator.ActionNoop {
		t.Fatalf("Decide = %s, want noop", orchestrator.Decide(sig))
	}
	if sig.Evidence["escalate"] != orchestrator.EscalateGateUnavailable {
		t.Fatalf("escalate = %v", sig.Evidence["escalate"])
	}
	if sig.Evidence["gate"] != "dev-smoke" {
		t.Fatalf("gate = %v", sig.Evidence["gate"])
	}
	reason, _ := sig.Evidence["gate_error"].(string)
	if !strings.Contains(reason, `"dev-typo" not found`) {
		t.Fatalf("evidence does not name the cause: %q", reason)
	}
	if sig.SHA != "abc1234" {
		t.Fatalf("sha = %q, want the commit the missing verdict applied to", sig.SHA)
	}
	if !strings.Contains(logs.String(), "gate check failed") {
		t.Fatalf("no operator-visible log line:\n%s", logs.String())
	}
}

// TestBlockedGateDoesNotRepeatEveryTick is the log/backlog-flood guard.
// The poller re-checks on every interval, so a permanently
// misconfigured argocd_app would otherwise write an ERROR line, a
// BACKLOG.md entry and an audit row per tick forever.
func TestBlockedGateDoesNotRepeatEveryTick(t *testing.T) {
	eg := &erroringGate{err: errors.New("gitops: argocd app get dev-typo: exit status 20")}
	ch := make(chan orchestrator.Signal, 32)
	var logs bytes.Buffer
	p := &Poller{
		Gate:    eg,
		Repos:   []string{"svc"},
		Source:  orchestrator.SourceDevGate,
		Signals: ch,
		Log:     slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	for i := 0; i < 10; i++ {
		p.tick(context.Background(), 30*time.Second)
	}
	if got := len(drain(ch)); got != 1 {
		t.Fatalf("10 ticks of the same failure emitted %d signals, want 1", got)
	}
	if n := strings.Count(logs.String(), "gate check failed"); n != 1 {
		t.Fatalf("10 ticks of the same failure logged %d error lines, want 1:\n%s", n, logs.String())
	}
	if eg.calls != 10 {
		t.Fatalf("gate was checked %d times, want 10 — suppression must not stop the polling", eg.calls)
	}

	// Recovery re-arms it, so a gate that breaks again later is
	// reported again rather than staying silent for the process's life.
	eg.set(nil)
	p.tick(context.Background(), 30*time.Second)
	if got := drain(ch); len(got) != 1 || got[0].Kind != orchestrator.KindPass {
		t.Fatalf("recovery emitted %+v, want one pass", got)
	}
	eg.set(errors.New("gitops: argocd app get dev-typo: exit status 20"))
	p.tick(context.Background(), 30*time.Second)
	if got := drain(ch); len(got) != 1 || got[0].Kind != orchestrator.KindBlocked {
		t.Fatalf("a gate that broke again emitted %+v, want one blocked", got)
	}
}

// TestBlockedGateWithATimestampedReasonDoesNotFlood is the flood that
// reason-equality alone does not stop, found against a stub `argocd`:
// the real CLI stamps its own timestamp into the fatal line it prints,
// so every tick produced a *different* reason string and wrote a record
// per tick after all. A changed reason is held to one report per
// BlockedRepeatWindow.
func TestBlockedGateWithATimestampedReasonDoesNotFlood(t *testing.T) {
	eg := &erroringGate{}
	ch := make(chan orchestrator.Signal, 64)
	var logs bytes.Buffer
	p := &Poller{
		Gate:    eg,
		Repos:   []string{"svc"},
		Source:  orchestrator.SourceDevGate,
		Signals: ch,
		Log:     slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	for i := 0; i < 20; i++ {
		// What the stub (and the real CLI) actually prints.
		eg.set(fmt.Errorf(
			`gitops: argocd app get dev-typo: exit status 20: time=%q level=fatal msg="applications.argoproj.io \"dev-typo\" not found"`,
			time.Now().Add(time.Duration(i)*time.Second).Format(time.RFC3339)))
		p.tick(context.Background(), 30*time.Second)
	}
	if got := len(drain(ch)); got != 1 {
		t.Fatalf("20 ticks of a timestamped reason emitted %d signals, want 1", got)
	}
	if n := strings.Count(logs.String(), "gate check failed"); n != 1 {
		t.Fatalf("20 ticks of a timestamped reason logged %d error lines, want 1", n)
	}
}

// TestBlockedGateReportsAGenuinelyNewFailureMode: the window must not
// swallow a different cause forever — the app name was fixed and now
// the session has expired is a new thing for an operator to see.
func TestBlockedGateReportsAGenuinelyNewFailureMode(t *testing.T) {
	eg := &erroringGate{err: errors.New("gitops: argocd app get dev-typo: exit status 20")}
	ch := make(chan orchestrator.Signal, 8)
	p := &Poller{
		Gate:    eg,
		Repos:   []string{"svc"},
		Source:  orchestrator.SourceDevGate,
		Signals: ch,
		Log:     silentLogger(),
		// The window has elapsed by the time the reason changes.
		BlockedRepeatWindow: time.Nanosecond,
	}

	p.tick(context.Background(), 30*time.Second)
	if got := len(drain(ch)); got != 1 {
		t.Fatalf("first failure emitted %d signals, want 1", got)
	}
	// Same reason: still suppressed, window or no window.
	p.tick(context.Background(), 30*time.Second)
	if got := len(drain(ch)); got != 0 {
		t.Fatalf("the identical reason re-emitted %d signals", got)
	}

	eg.set(errors.New("gitops: argocd app get dev-svc: exit status 20: token has expired"))
	p.tick(context.Background(), 30*time.Second)
	got := drain(ch)
	if len(got) != 1 {
		t.Fatalf("a new failure mode emitted %d signals, want 1", len(got))
	}
	if reason, _ := got[0].Evidence["gate_error"].(string); !strings.Contains(reason, "token has expired") {
		t.Fatalf("second episode carried the first reason: %q", reason)
	}
}

// TestBlockedGateDoesNotDisturbTheVerdictEdge: recording "blocked" in
// the verdict edge state would make the next real verdict look like a
// transition, so a sustained prod-health breach interrupted by one
// failed check would emit a second breach — and revert the revert.
func TestBlockedGateDoesNotDisturbTheVerdictEdge(t *testing.T) {
	fg := &fakeGate{results: map[string]gate.Result{"svc": {Status: gate.StatusFail}}}
	ch := make(chan orchestrator.Signal, 8)
	p := &Poller{
		Gate:    fg,
		Repos:   []string{"svc"},
		Source:  orchestrator.SourceProdHealth,
		Signals: ch,
		Log:     silentLogger(),
	}

	p.tick(context.Background(), 30*time.Second) // breach → one Revert
	if got := drain(ch); len(got) != 1 || got[0].Kind != orchestrator.KindBreach {
		t.Fatalf("first tick emitted %+v, want one breach", got)
	}

	fg.errs = map[string]error{"svc": errors.New("promclient: connection refused")}
	p.tick(context.Background(), 30*time.Second) // gate cannot run
	if got := drain(ch); len(got) != 1 || got[0].Kind != orchestrator.KindBlocked {
		t.Fatalf("blocked tick emitted %+v, want one blocked", got)
	}

	fg.errs = nil
	p.tick(context.Background(), 30*time.Second) // still breaching
	if got := drain(ch); len(got) != 0 {
		t.Fatalf("the same sustained breach re-emitted after a blocked tick: %+v — that reverts the revert", got)
	}
}

// TestCancelledCheckIsNotABlockedGate: a tick deadline or a daemon
// shutdown says nothing about the gate's configuration, and tick()
// already reports an overrun itself. Emitting a record per timed-out
// tick would be the flood in a different costume.
func TestCancelledCheckIsNotABlockedGate(t *testing.T) {
	hg := &hangingGate{name: "prod-health"}
	ch := make(chan orchestrator.Signal, 4)
	p := &Poller{
		Gate:    hg,
		Repos:   []string{"svc"},
		Source:  orchestrator.SourceProdHealth,
		Signals: ch,
		Log:     silentLogger(),
		Timeout: 20 * time.Millisecond,
	}
	p.tick(context.Background(), time.Second)
	if got := drain(ch); len(got) != 0 {
		t.Fatalf("a timed-out tick emitted %+v, want nothing", got)
	}
}
