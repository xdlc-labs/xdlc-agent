package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/backlog"
)

// pushingDispatcher reports that its Fix pushed a commit, the way the
// real dispatcher does in worktree mode.
type pushingDispatcher struct {
	fakeDispatcher
	pushed string
}

func (p *pushingDispatcher) Fix(ctx context.Context, s Signal) (FixResult, error) {
	p.fixCalls = append(p.fixCalls, s)
	return FixResult{Delivered: true, PushedSHAs: []string{p.pushed}}, nil
}

func newOrch(t *testing.T, d Dispatcher) (*Orchestrator, chan Action) {
	t.Helper()
	bl, err := backlog.Open(filepath.Join(t.TempDir(), "BACKLOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	o := New(d, bl, slog.New(slog.NewTextHandler(io.Discard, nil)))
	actions := make(chan Action, 8)
	o.Audit = func(_ Signal, a Action, _ error, _ time.Time) error {
		actions <- a
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = o.Run(ctx) }()
	return o, actions
}

func next(t *testing.T, ch chan Action) Action {
	t.Helper()
	select {
	case a := <-ch:
		return a
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for an action")
		return ""
	}
}

// The workflow_run for a commit the daemon itself pushed is the
// reverify's subject. In the field it was picked up as a fresh failure
// and started a third Fix on a branch that had just gone green.
func TestFailForOwnPushIsDropped(t *testing.T) {
	d := &pushingDispatcher{pushed: "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111"}
	o, actions := newOrch(t, d)

	o.Signals <- Signal{Source: SourceCI, Repo: "svc", Kind: KindFail, SHA: "0000000000000000000000000000000000000000",
		Evidence: map[string]any{"run_url": "https://github.com/o/r/actions/runs/1"}}
	if a := next(t, actions); a != ActionFix {
		t.Fatalf("first signal: %v", a)
	}
	o.Signals <- Signal{Source: SourceCI, Repo: "svc", Kind: KindFail, SHA: d.pushed,
		Evidence: map[string]any{"run_url": "https://github.com/o/r/actions/runs/2"}}
	if a := next(t, actions); a != ActionNoop {
		t.Fatalf("fail for own push: want noop, got %v", a)
	}
	if len(d.fixCalls) != 1 {
		t.Fatalf("Fix ran %d times, want 1", len(d.fixCalls))
	}
}

// The completion GitHub delivers for a rerun the ladder requested is the
// answer the ladder already read. It carries run_attempt+1.
func TestRerunEchoIsDropped(t *testing.T) {
	d := &fakeDispatcher{}
	o, actions := newOrch(t, d)
	o.RerunCI = func(context.Context, Signal) (bool, error) { return false, nil } // still red

	sha := "bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222"
	o.Signals <- Signal{Source: SourceCI, Repo: "svc", Kind: KindFail, SHA: sha,
		Evidence: map[string]any{"run_url": "https://github.com/o/r/actions/runs/7", "run_attempt": 1}}
	if a := next(t, actions); a != ActionFix {
		t.Fatalf("first: %v", a)
	}
	// The echo: same run, attempt 2, arriving after the Fix.
	o.Signals <- Signal{Source: SourceCI, Repo: "svc", Kind: KindFail, SHA: sha,
		Evidence: map[string]any{"run_url": "https://github.com/o/r/actions/runs/7", "run_attempt": 2}}
	if a := next(t, actions); a != ActionNoop {
		t.Fatalf("rerun echo: want noop, got %v", a)
	}
	if len(d.fixCalls) != 1 {
		t.Fatalf("Fix ran %d times, want 1", len(d.fixCalls))
	}
}

// A run for a commit that is no longer the branch tip is stale: not
// reran, not fixed on a checkout that does not contain it.
func TestSupersededSHAIsDropped(t *testing.T) {
	d := &fakeDispatcher{}
	o, actions := newOrch(t, d)
	reruns := 0
	o.RerunCI = func(context.Context, Signal) (bool, error) { reruns++; return false, nil }
	o.TipSHA = func(context.Context, string) (string, error) { return "cccc3333cccc3333cccc3333cccc3333cccc3333", nil }

	o.Signals <- Signal{Source: SourceCI, Repo: "svc", Kind: KindFail, SHA: "dddd4444dddd4444dddd4444dddd4444dddd4444",
		Evidence: map[string]any{"run_url": "https://github.com/o/r/actions/runs/9"}}
	if a := next(t, actions); a != ActionNoop {
		t.Fatalf("superseded: want noop, got %v", a)
	}
	if reruns != 0 || len(d.fixCalls) != 0 {
		t.Fatalf("stale run was reran (%d) or fixed (%d)", reruns, len(d.fixCalls))
	}
	// The current tip is still fixed.
	o.Signals <- Signal{Source: SourceCI, Repo: "svc", Kind: KindFail, SHA: "cccc3333cccc3333cccc3333cccc3333cccc3333",
		Evidence: map[string]any{"run_url": "https://github.com/o/r/actions/runs/10"}}
	if a := next(t, actions); a != ActionFix {
		t.Fatalf("current tip: want fix, got %v", a)
	}
}

// A tip lookup that fails must not block Fixes: the signal is treated as
// current, since that is the pre-1.0.1 behaviour and the safe side here.
func TestTipLookupErrorDoesNotBlockFix(t *testing.T) {
	d := &fakeDispatcher{}
	o, actions := newOrch(t, d)
	o.TipSHA = func(context.Context, string) (string, error) { return "", errors.New("ls-remote: network") }
	o.Signals <- Signal{Source: SourceCI, Repo: "svc", Kind: KindFail, SHA: "eeee5555eeee5555eeee5555eeee5555eeee5555"}
	if a := next(t, actions); a != ActionFix {
		t.Fatalf("want fix, got %v", a)
	}
}

// Manual Fixes carry no SHA and are never second-guessed.
func TestManualFixIsNeverDropped(t *testing.T) {
	d := &fakeDispatcher{}
	o, actions := newOrch(t, d)
	o.TipSHA = func(context.Context, string) (string, error) { return "ffff", nil }
	o.Signals <- Signal{Source: SourceCI, Repo: "svc", Kind: KindFail, Evidence: map[string]any{"manual": true}}
	if a := next(t, actions); a != ActionFix {
		t.Fatalf("want fix, got %v", a)
	}
}

// A failed dispatch leaves its reason in the evidence, so BACKLOG.md can
// say why the row is red.
func TestDispatchErrorReachesEvidence(t *testing.T) {
	d := &failingPromote{}
	bl, err := backlog.Open(filepath.Join(t.TempDir(), "BACKLOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	o := New(d, bl, slog.New(slog.NewTextHandler(io.Discard, nil)))
	got := make(chan Signal, 1)
	o.Audit = func(s Signal, _ Action, _ error, _ time.Time) error { got <- s; return nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = o.Run(ctx) }()
	o.Signals <- Signal{Source: SourceDevGate, Repo: "svc", Kind: KindPass}
	select {
	case s := <-got:
		if s.Evidence["error"] != "promote: push develop->main not fast-forwardable" {
			t.Fatalf("evidence=%v", s.Evidence)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

type failingPromote struct{ fakeDispatcher }

func (f *failingPromote) Promote(context.Context, Signal) error {
	return errors.New("promote: push develop->main not fast-forwardable")
}
