package dispatch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/xdlc-labs/xdlc-agent/internal/orchestrator"
	"github.com/xdlc-labs/xdlc-agent/internal/subagent"
)

// Revert undoes a deploy. With no Promote on record the prod tip is a
// human's commit, and the daemon must refuse to touch it.
func TestRevertRefusesWithoutAPromoteOnRecord(t *testing.T) {
	bareDir, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	d := New(mgr, nil, silentLogger())
	before := strings.TrimSpace(runGit(t, bareDir, "rev-parse", "main"))

	sig := orchestrator.Signal{Repo: "svc", Source: orchestrator.SourceProdHealth, Kind: orchestrator.KindBreach, Evidence: map[string]any{}}
	err := d.Revert(context.Background(), sig)
	if err == nil || !strings.Contains(err.Error(), "no Promote on record") {
		t.Fatalf("want refusal, got %v", err)
	}
	if sig.Evidence["escalate"] != "nothing_to_revert" {
		t.Fatalf("evidence=%v", sig.Evidence)
	}
	if after := strings.TrimSpace(runGit(t, bareDir, "rev-parse", "main")); after != before {
		t.Fatalf("main moved from %s to %s", before, after)
	}
}

// A Promote is on record but someone pushed to prod since: that commit
// is theirs, not a deploy, and reverting it would not be a rollback.
func TestRevertRefusesWhenProdTipIsNotThePromotedCommit(t *testing.T) {
	bareDir, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	d := New(mgr, nil, silentLogger())
	d.LastPromote = func(string) (string, bool) { return "0123456789abcdef0123456789abcdef01234567", true }
	before := strings.TrimSpace(runGit(t, bareDir, "rev-parse", "main"))

	sig := orchestrator.Signal{Repo: "svc", Source: orchestrator.SourceProdHealth, Kind: orchestrator.KindBreach, Evidence: map[string]any{}}
	err := d.Revert(context.Background(), sig)
	if err == nil || !strings.Contains(err.Error(), "is not the last promoted commit") {
		t.Fatalf("want refusal, got %v", err)
	}
	if sig.Evidence["prod_tip"] != before || sig.Evidence["escalate"] != "nothing_to_revert" {
		t.Fatalf("evidence=%v", sig.Evidence)
	}
}

// The happy path: Promote, then Revert reverts exactly what was promoted
// and says so in the evidence.
func TestPromoteThenRevertTargetsThePromotedCommit(t *testing.T) {
	bareDir, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	d := New(mgr, nil, silentLogger())

	promoteSig := orchestrator.Signal{Repo: "svc", Source: orchestrator.SourceDevGate, Kind: orchestrator.KindPass, Evidence: map[string]any{}}
	if err := d.Promote(context.Background(), promoteSig); err != nil {
		t.Fatalf("Promote: %v", err)
	}
	promoted, _ := promoteSig.Evidence["promoted_sha"].(string)
	if promoted == "" || !strings.HasPrefix(strings.TrimSpace(runGit(t, bareDir, "rev-parse", "main")), promoted) {
		t.Fatalf("promoted_sha=%q, main=%s", promoted, runGit(t, bareDir, "rev-parse", "main"))
	}

	d.LastPromote = func(string) (string, bool) { return promoted, true }
	revertSig := orchestrator.Signal{Repo: "svc", Source: orchestrator.SourceProdHealth, Kind: orchestrator.KindBreach, Evidence: map[string]any{}}
	if err := d.Revert(context.Background(), revertSig); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if revertSig.Evidence["reverted_sha"] != promoted || revertSig.Evidence["new_prod_tip"] == "" {
		t.Fatalf("evidence=%v", revertSig.Evidence)
	}
	subject := runGit(t, bareDir, "log", "-1", "--format=%s", "main")
	if !strings.HasPrefix(subject, "Revert") {
		t.Fatalf("main tip is %q", subject)
	}
}

// A CLI that exits non-zero with no output did not run an agent. The
// Fix moves to the next provider; the dead one stays out of the rotation.
func TestFailOverOnSilentProviderFailure(t *testing.T) {
	d := &Dispatcher{Providers: []string{"claude", "cursor", "codex"}, DefaultProvider: "claude"}
	next, ok := d.failOver("codex", "", errors.New("exit status 1: refresh token was already used"), false)
	if !ok || next != "claude" {
		t.Fatalf("want fail-over to claude, got %q %v", next, ok)
	}
	if !d.isDown("codex") {
		t.Fatal("codex should be marked down")
	}
	if got := d.liveProviders(); strings.Join(got, ",") != "claude,cursor" {
		t.Fatalf("live=%v", got)
	}
	// Nothing to fail over to once every candidate is down.
	d.markDown("claude")
	d.markDown("cursor")
	if _, ok := d.failOver("claude", "", errors.New("exit status 1"), false); ok {
		t.Fatal("no live provider left; must not fail over")
	}
}

func TestFailOverDoesNotApplyToRealAgentFailures(t *testing.T) {
	d := &Dispatcher{Providers: []string{"claude", "cursor"}}
	if _, ok := d.failOver("claude", "the agent printed something", errors.New("exit status 1"), false); ok {
		t.Fatal("an agent that ran and failed is the Fix's failure, not the provider's")
	}
	if _, ok := d.failOver("claude", "", subagent.ErrStalled, false); ok {
		t.Fatal("a stall is a working agent gone quiet; do not fail over")
	}
	if _, ok := d.failOver("codex", "", errors.New("exit status 1"), true); ok {
		t.Fatal("an operator's explicit provider is honoured even when it fails")
	}
}
