package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/backlog"
)

// TestDecideNeverActsOnABlockedGate is the money assertion of issue
// #45's second half. A gate that could not run is not a verdict about
// the deployed software: dev-smoke fail routes to ActionFix, which pays
// for a coding-agent run, and a prod-health breach routes to
// ActionRevert, which pushes to prod. An unreachable ArgoCD or a typo'd
// argocd_app must produce neither.
func TestDecideNeverActsOnABlockedGate(t *testing.T) {
	for _, src := range []Source{SourceCI, SourceDevGate, SourceProdHealth, Source("something-new")} {
		s := Blocked(src, "svc", "dev-smoke", "abc1234", errors.New("gitops: argocd app get dev-typo: exit status 20"))
		if got := Decide(s); got != ActionNoop {
			t.Errorf("Decide(%s/blocked) = %s, want %s", src, got, ActionNoop)
		}
	}

	// And the contrast: the same source and repo with a real verdict
	// still acts, so "blocked" is not just a disabled gate.
	if got := Decide(Signal{Source: SourceDevGate, Repo: "svc", Kind: KindFail}); got != ActionFix {
		t.Errorf("dev-gate fail = %s, want %s", got, ActionFix)
	}
	if got := Decide(Signal{Source: SourceDevGate, Repo: "svc", Kind: KindPass}); got != ActionPromote {
		t.Errorf("dev-gate pass = %s, want %s", got, ActionPromote)
	}
	if got := Decide(Signal{Source: SourceProdHealth, Repo: "svc", Kind: KindBreach}); got != ActionRevert {
		t.Errorf("prod-health breach = %s, want %s", got, ActionRevert)
	}
}

func TestBlockedCarriesTheReason(t *testing.T) {
	s := Blocked(SourceDevGate, "svc", "dev-smoke", "abc1234",
		errors.New(`gitops: argocd app get dev-typo: exit status 20: applications.argoproj.io "dev-typo" not found`))

	if s.Kind != KindBlocked {
		t.Fatalf("kind = %s", s.Kind)
	}
	if s.Evidence["escalate"] != EscalateGateUnavailable {
		t.Fatalf("escalate = %v, want %s", s.Evidence["escalate"], EscalateGateUnavailable)
	}
	if s.Evidence["gate"] != "dev-smoke" {
		t.Fatalf("gate = %v", s.Evidence["gate"])
	}
	reason, blocked := BlockedReason(s)
	if !blocked {
		t.Fatal("BlockedReason did not recognise a blocked signal")
	}
	if !strings.Contains(reason, `"dev-typo" not found`) {
		t.Fatalf("reason lost the CLI's own diagnosis: %q", reason)
	}

	// A pass or a fail is never mistaken for one.
	for _, k := range []Kind{KindPass, KindFail, KindBreach} {
		if _, blocked := BlockedReason(Signal{Kind: k}); blocked {
			t.Errorf("BlockedReason(%s) = blocked", k)
		}
	}
	// Never empty, even if evidence was stripped.
	if reason, _ := BlockedReason(Signal{Kind: KindBlocked}); reason == "" {
		t.Fatal("blocked signal with no evidence produced an empty reason")
	}
}

// TestBlockedGateIsRecordedForAnOperator: the whole point of routing it
// through the orchestrator instead of returning an error. The record
// has to reach BACKLOG.md and the audit callback, tagged so an operator
// can tell it from both a pass and a fail — and no Dispatcher method
// may be called.
func TestBlockedGateIsRecordedForAnOperator(t *testing.T) {
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
		err    error
	}
	auditCh := make(chan audited, 1)
	o.Audit = func(s Signal, action Action, dispatchErr error, _ time.Time) error {
		auditCh <- audited{s, action, dispatchErr}
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = o.Run(ctx) }()

	o.Signals <- Blocked(SourceDevGate, "svc", "dev-smoke", "abc1234",
		errors.New(`gitops: argocd app get dev-typo: exit status 20: applications.argoproj.io "dev-typo" not found`))

	select {
	case a := <-auditCh:
		if a.action != ActionNoop {
			t.Fatalf("action = %s, want %s — a blocked gate must not act", a.action, ActionNoop)
		}
		if a.s.Kind != KindBlocked {
			t.Fatalf("audited kind = %s, want %s", a.s.Kind, KindBlocked)
		}
		if a.err != nil {
			t.Fatalf("dispatchErr = %v; nothing was dispatched", a.err)
		}
		// The daemon's Audit func turns this into store.StatusError.
		if _, blocked := BlockedReason(a.s); !blocked {
			t.Fatal("audited signal is not recognisable as blocked")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for the audit record")
	}

	if len(disp.fixCalls)+len(disp.revertCalls)+len(disp.promoteCalls) != 0 {
		t.Fatalf("dispatcher was called: fix=%d revert=%d promote=%d",
			len(disp.fixCalls), len(disp.revertCalls), len(disp.promoteCalls))
	}

	raw, err := os.ReadFile(blPath)
	if err != nil {
		t.Fatal(err)
	}
	line := string(raw)
	if !strings.Contains(line, "escalate="+EscalateGateUnavailable) {
		t.Fatalf("BACKLOG.md has no escalate=%s:\n%s", EscalateGateUnavailable, line)
	}
	if !strings.Contains(line, "action=noop") {
		t.Fatalf("BACKLOG.md action is not noop:\n%s", line)
	}
	if !strings.Contains(line, "dev-typo") {
		t.Fatalf("BACKLOG.md does not say why the gate could not run:\n%s", line)
	}
}

// TestBlockedGateDoesNotClearAProdBreach: a check that could not run
// knows nothing about prod, so it must not re-arm Revert (or the
// circuit breaker) by looking like a recovery.
func TestBlockedGateDoesNotClearAProdBreach(t *testing.T) {
	o := New(&fakeDispatcher{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	o.updateBreach(Signal{Source: SourceProdHealth, Repo: "svc", Kind: KindBreach})
	if o.breachCount() != 1 {
		t.Fatalf("breach not recorded")
	}
	o.updateBreach(Blocked(SourceProdHealth, "svc", "prod-health", "", errors.New("prometheus: connection refused")))
	if o.breachCount() != 1 {
		t.Fatal("a gate that could not run cleared the prod breach")
	}
	// A real pass still clears it.
	o.updateBreach(Signal{Source: SourceProdHealth, Repo: "svc", Kind: KindPass})
	if o.breachCount() != 0 {
		t.Fatal("a real pass did not clear the breach")
	}
}
