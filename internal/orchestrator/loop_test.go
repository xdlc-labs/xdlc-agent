package orchestrator

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/backlog"
)

type fakeDispatcher struct {
	fixCalls, revertCalls, promoteCalls []Signal
}

func (f *fakeDispatcher) Fix(_ context.Context, s Signal) error {
	f.fixCalls = append(f.fixCalls, s)
	return nil
}
func (f *fakeDispatcher) Revert(_ context.Context, s Signal) error {
	f.revertCalls = append(f.revertCalls, s)
	return nil
}
func (f *fakeDispatcher) Promote(_ context.Context, s Signal) error {
	f.promoteCalls = append(f.promoteCalls, s)
	return nil
}

func TestOrchestratorRunDispatchesAndAudits(t *testing.T) {
	bl, err := backlog.Open(filepath.Join(t.TempDir(), "BACKLOG.md"))
	if err != nil {
		t.Fatalf("backlog.Open: %v", err)
	}

	disp := &fakeDispatcher{}
	o := New(disp, bl, slog.New(slog.NewTextHandler(io.Discard, nil)))

	type audited struct {
		s      Signal
		action Action
	}
	// Channel, not shared slice: Audit runs on a per-repo worker
	// goroutine; this test reads on the main goroutine.
	auditCh := make(chan audited, 3)
	o.Audit = func(s Signal, action Action, _ error, _ time.Time) error {
		auditCh <- audited{s, action}
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- o.Run(ctx) }()

	o.Signals <- Signal{Source: SourceCI, Repo: "svc-a", Kind: KindFail}
	o.Signals <- Signal{Source: SourceDevGate, Repo: "svc-a", Kind: KindPass}
	o.Signals <- Signal{Source: SourceProdHealth, Repo: "svc-a", Kind: KindBreach}

	var audits []audited
	deadline := time.After(2 * time.Second)
	for len(audits) < 3 {
		select {
		case a := <-auditCh:
			audits = append(audits, a)
		case <-deadline:
			t.Fatalf("timed out waiting for 3 audits, got %d: %+v", len(audits), audits)
		}
	}

	cancel()
	<-done

	if len(disp.fixCalls) != 1 || disp.fixCalls[0].Repo != "svc-a" {
		t.Errorf("expected 1 Fix call for svc-a, got %+v", disp.fixCalls)
	}
	if len(disp.promoteCalls) != 1 {
		t.Errorf("expected 1 Promote call, got %+v", disp.promoteCalls)
	}
	if len(disp.revertCalls) != 1 {
		t.Errorf("expected 1 Revert call, got %+v", disp.revertCalls)
	}

	wantActions := map[Action]bool{ActionFix: true, ActionPromote: true, ActionRevert: true}
	for _, a := range audits {
		if !wantActions[a.action] {
			t.Errorf("unexpected audited action %v for signal %+v", a.action, a.s)
		}
	}
}

// blockingDispatcher lets tests hold Fix until released, proving another
// repo's Revert can still complete.
type blockingDispatcher struct {
	fixStarted chan struct{}
	fixRelease chan struct{}
	revertDone chan struct{}
}

func (d *blockingDispatcher) Fix(ctx context.Context, _ Signal) error {
	close(d.fixStarted)
	select {
	case <-d.fixRelease:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}
func (d *blockingDispatcher) Revert(_ context.Context, _ Signal) error {
	close(d.revertDone)
	return nil
}
func (d *blockingDispatcher) Promote(_ context.Context, _ Signal) error { return nil }

func TestSlowFixDoesNotBlockOtherRepoRevert(t *testing.T) {
	bl, err := backlog.Open(filepath.Join(t.TempDir(), "BACKLOG.md"))
	if err != nil {
		t.Fatalf("backlog.Open: %v", err)
	}

	disp := &blockingDispatcher{
		fixStarted: make(chan struct{}),
		fixRelease: make(chan struct{}),
		revertDone: make(chan struct{}),
	}
	o := New(disp, bl, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- o.Run(ctx) }()

	o.Signals <- Signal{Source: SourceCI, Repo: "repo-a", Kind: KindFail}

	select {
	case <-disp.fixStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Fix to start on repo-a")
	}

	o.Signals <- Signal{Source: SourceProdHealth, Repo: "repo-b", Kind: KindBreach}

	select {
	case <-disp.revertDone:
		// Revert finished while Fix still held — concurrency works.
	case <-time.After(2 * time.Second):
		t.Fatal("Revert on repo-b blocked by slow Fix on repo-a")
	}

	close(disp.fixRelease)
	cancel()
	<-done
}

// countingFixDispatcher counts Fix calls; first Fix blocks until release.
type countingFixDispatcher struct {
	mu         sync.Mutex
	n          int
	fixStarted chan struct{}
	fixRelease chan struct{}
}

func (d *countingFixDispatcher) Fix(ctx context.Context, _ Signal) error {
	d.mu.Lock()
	d.n++
	n := d.n
	d.mu.Unlock()
	if n == 1 {
		close(d.fixStarted)
		select {
		case <-d.fixRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (d *countingFixDispatcher) Revert(context.Context, Signal) error  { return nil }
func (d *countingFixDispatcher) Promote(context.Context, Signal) error { return nil }
func (d *countingFixDispatcher) fixes() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.n
}

func TestFixCoalesceLatestWins(t *testing.T) {
	bl, err := backlog.Open(filepath.Join(t.TempDir(), "BACKLOG.md"))
	if err != nil {
		t.Fatalf("backlog.Open: %v", err)
	}
	disp := &countingFixDispatcher{
		fixStarted: make(chan struct{}),
		fixRelease: make(chan struct{}),
	}
	o := New(disp, bl, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- o.Run(ctx) }()

	o.Signals <- Signal{Source: SourceCI, Repo: "noisy", Kind: KindFail, Evidence: map[string]any{"n": 0}}
	select {
	case <-disp.fixStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first Fix never started")
	}

	for i := 1; i <= 20; i++ {
		o.Signals <- Signal{Source: SourceCI, Repo: "noisy", Kind: KindFail, Evidence: map[string]any{"n": i}}
	}
	// Let Run drain Signals into the per-repo chan (coalesce while Fix holds).
	time.Sleep(50 * time.Millisecond)

	close(disp.fixRelease)
	deadline := time.After(2 * time.Second)
	for disp.fixes() < 2 {
		select {
		case <-deadline:
			t.Fatalf("timed out; fixes=%d", disp.fixes())
		case <-time.After(10 * time.Millisecond):
		}
	}
	time.Sleep(50 * time.Millisecond) // any stragglers
	cancel()
	<-done

	if n := disp.fixes(); n > 3 {
		t.Fatalf("20 CI fails coalesced to %d Fixes; want ≤3", n)
	}
}
