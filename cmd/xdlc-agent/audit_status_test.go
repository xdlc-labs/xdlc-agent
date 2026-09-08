package main

import (
	"errors"
	"testing"

	"github.com/xdlc-labs/xdlc-agent/internal/orchestrator"
	"github.com/xdlc-labs/xdlc-agent/internal/store"
)

// TestAuditStatusMarksAnUnrunnableGate: the audit row is the record an
// operator reads in `xdlc history` and the console Activity feed. A gate
// that could not run dispatches nothing, so it used to be indistinguishable
// from a clean noop there (issue #45).
func TestAuditStatusMarksAnUnrunnableGate(t *testing.T) {
	blocked := orchestrator.Blocked(orchestrator.SourceDevGate, "svc", "dev-smoke", "abc1234",
		errors.New(`gitops: argocd app get dev-typo: exit status 20: applications.argoproj.io "dev-typo" not found`))

	status, errMsg := auditStatus(blocked, nil)
	if status != store.StatusError {
		t.Fatalf("status = %q, want %q — a dead gate must not read ok", status, store.StatusError)
	}
	if errMsg == "" {
		t.Fatal("no reason recorded on the audit row")
	}
	// The row must read ok=false in `xdlc history` and the console.
	if (store.Record{Status: status}).Succeeded() {
		t.Fatal("blocked row counts as a successful dispatch")
	}

	// A real gate fail is a *successful* dispatch of a Fix, so it stays
	// ok=true — that is what keeps the two distinguishable.
	failed := orchestrator.Signal{Source: orchestrator.SourceDevGate, Repo: "svc", Kind: orchestrator.KindFail}
	if status, errMsg := auditStatus(failed, nil); status != store.StatusOK || errMsg != "" {
		t.Fatalf("gate fail = (%q, %q), want (%q, \"\")", status, errMsg, store.StatusOK)
	}

	// A dispatch error still wins, and reports its own message.
	if status, errMsg := auditStatus(failed, errors.New("git push rejected")); status != store.StatusError || errMsg != "git push rejected" {
		t.Fatalf("dispatch error = (%q, %q)", status, errMsg)
	}
	// Even on a blocked signal: the dispatch error is the more specific fact.
	if _, errMsg := auditStatus(blocked, errors.New("boom")); errMsg != "boom" {
		t.Fatalf("dispatch error lost on a blocked signal: %q", errMsg)
	}
}
