package orchestrator

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/backlog"
)

func TestCIRerunGreenSkipsFix(t *testing.T) {
	bl, err := backlog.Open(filepath.Join(t.TempDir(), "BACKLOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	disp := &fakeDispatcher{}
	o := New(disp, bl, slog.New(slog.NewTextHandler(io.Discard, nil)))
	o.RerunCI = func(_ context.Context, _ Signal) (bool, error) {
		return true, nil
	}

	auditCh := make(chan Action, 1)
	o.Audit = func(_ Signal, action Action, _ error, _ time.Time) error {
		auditCh <- action
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = o.Run(ctx) }()

	o.Signals <- Signal{
		Source:   SourceCI,
		Repo:     "svc",
		Kind:     KindFail,
		Evidence: map[string]any{"run_url": "https://github.com/o/r/actions/runs/1"},
	}

	select {
	case a := <-auditCh:
		if a != ActionRerun {
			t.Fatalf("action = %v, want rerun", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	if len(disp.fixCalls) != 0 {
		t.Fatalf("Fix should not run on green rerun, got %d", len(disp.fixCalls))
	}
}

func TestCIRerunRedFallsThroughToFix(t *testing.T) {
	bl, err := backlog.Open(filepath.Join(t.TempDir(), "BACKLOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	disp := &fakeDispatcher{}
	o := New(disp, bl, slog.New(slog.NewTextHandler(io.Discard, nil)))
	o.RerunCI = func(_ context.Context, _ Signal) (bool, error) {
		return false, nil
	}

	auditCh := make(chan Action, 1)
	o.Audit = func(_ Signal, action Action, _ error, _ time.Time) error {
		auditCh <- action
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = o.Run(ctx) }()

	o.Signals <- Signal{
		Source:   SourceCI,
		Repo:     "svc",
		Kind:     KindFail,
		Evidence: map[string]any{"run_url": "https://github.com/o/r/actions/runs/99"},
	}

	select {
	case a := <-auditCh:
		if a != ActionFix {
			t.Fatalf("action = %v, want fix", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	if len(disp.fixCalls) != 1 {
		t.Fatalf("expected 1 Fix, got %d", len(disp.fixCalls))
	}
}
