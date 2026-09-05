package orchestrator

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/backlog"
)

func TestStructuralMatch(t *testing.T) {
	if got := structuralMatch(Signal{Evidence: map[string]any{"log": "cross-service API break"}}); got != "cross-service" {
		t.Fatalf("got %q", got)
	}
	if got := structuralMatch(Signal{Evidence: map[string]any{"log": "normal test fail"}}); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
	// Manual Fix bypasses.
	if got := structuralMatch(Signal{Evidence: map[string]any{
		"manual": true,
		"log":    "needs complete rewrite",
	}}); got != "" {
		t.Fatalf("manual should bypass, got %q", got)
	}
}

func TestStructuralSuppressesFix(t *testing.T) {
	blPath := filepath.Join(t.TempDir(), "BACKLOG.md")
	bl, err := backlog.Open(blPath)
	if err != nil {
		t.Fatal(err)
	}
	disp := &fakeDispatcher{}
	o := New(disp, bl, slog.New(slog.NewTextHandler(io.Discard, nil)))

	type audited struct {
		s      Signal
		action Action
	}
	auditCh := make(chan audited, 1)
	o.Audit = func(s Signal, action Action, _ error, _ time.Time) error {
		auditCh <- audited{s, action}
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = o.Run(ctx) }()

	o.Signals <- Signal{
		Source: SourceCI,
		Repo:   "svc",
		Kind:   KindFail,
		Evidence: map[string]any{
			"run_url": "https://github.com/o/r/actions/runs/1",
			"log":     "failure spans cross-service contract; needs architecture redesign",
		},
	}

	select {
	case a := <-auditCh:
		if a.action != ActionNoop {
			t.Fatalf("action = %v, want noop", a.action)
		}
		if a.s.Evidence["escalate"] != "structural" {
			t.Fatalf("escalate = %v", a.s.Evidence["escalate"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	if len(disp.fixCalls) != 0 {
		t.Fatalf("Runner invoked %d times", len(disp.fixCalls))
	}

	raw, err := os.ReadFile(blPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "escalate=structural") {
		t.Fatalf("backlog missing escalate=structural:\n%s", raw)
	}
}

func TestStructuralManualStillFixes(t *testing.T) {
	bl, err := backlog.Open(filepath.Join(t.TempDir(), "BACKLOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	disp := &fakeDispatcher{}
	o := New(disp, bl, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Avoid flake ladder swallowing the Fix.
	o.RerunCI = nil

	auditCh := make(chan Action, 1)
	o.Audit = func(_ Signal, action Action, _ error, _ time.Time) error {
		auditCh <- action
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = o.Run(ctx) }()

	o.Signals <- Signal{
		Source: SourceCI,
		Repo:   "svc",
		Kind:   KindFail,
		Evidence: map[string]any{
			"manual": true,
			"via":    "api",
			"log":    "complete rewrite needed",
		},
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
		t.Fatalf("want 1 Fix, got %d", len(disp.fixCalls))
	}
}
