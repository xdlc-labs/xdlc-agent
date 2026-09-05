package dispatch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
	"github.com/xdlc-labs/xdlc-agent/internal/orchestrator"
	"github.com/xdlc-labs/xdlc-agent/internal/promote"
	"github.com/xdlc-labs/xdlc-agent/internal/repos"
)

// runGit runs git in dir, failing the test on error. Used only to build
// fixtures — the code under test (repos.EnsureCloned, promote.FastForward,
// Dispatcher.Revert) makes its own git calls independently.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (in %s): %v: %s", args, dir, err, out)
	}
	return string(out)
}

func writeCommit(t *testing.T, dir, file, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", msg)
}

// setupOriginBranches builds a bare repo with prod@A and dev@A+1 (dev
// one commit ahead — the fast-forward-able state promote expects), and
// a working clone already checked out on dev.
func setupOriginBranches(t *testing.T, prod, dev string) (bareDir, workDir string) {
	t.Helper()
	root := t.TempDir()
	bareDir = filepath.Join(root, "origin.git")
	seedDir := filepath.Join(root, "seed")
	workDir = filepath.Join(root, "work")

	runGit(t, root, "init", "--bare", bareDir)
	runGit(t, root, "clone", bareDir, seedDir)
	runGit(t, seedDir, "config", "user.email", "test@example.com")
	runGit(t, seedDir, "config", "user.name", "test")

	runGit(t, seedDir, "checkout", "-b", prod)
	writeCommit(t, seedDir, "app.txt", "v1\n", "init on "+prod)
	runGit(t, seedDir, "push", "origin", prod)

	runGit(t, seedDir, "checkout", "-b", dev)
	writeCommit(t, seedDir, "app.txt", "v2\n", "work on "+dev)
	runGit(t, seedDir, "push", "origin", dev)

	runGit(t, bareDir, "symbolic-ref", "HEAD", "refs/heads/"+dev)

	runGit(t, root, "clone", "--branch", dev, bareDir, workDir)
	runGit(t, workDir, "config", "user.email", "test@example.com")
	runGit(t, workDir, "config", "user.name", "test")

	return bareDir, workDir
}

func setupOrigin(t *testing.T) (bareDir, workDir string) {
	return setupOriginBranches(t, "main", "develop")
}

func testManager(t *testing.T, workDir string) *repos.Manager {
	t.Helper()
	return repos.NewManager("unused-root", []config.Repo{
		{Name: "svc", GitHub: "org/svc", Dir: workDir, Branch: "develop"},
	}, nil /* no auth needed for local file:// remotes */)
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestPromoteFastForwardsRealRepo(t *testing.T) {
	bareDir, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	d := New(mgr, nil, silentLogger())

	if err := d.Promote(context.Background(), orchestrator.Signal{Repo: "svc"}); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	mainRev := strings.TrimSpace(runGit(t, bareDir, "rev-parse", "main"))
	developRev := strings.TrimSpace(runGit(t, bareDir, "rev-parse", "develop"))
	if mainRev != developRev {
		t.Errorf("origin main (%s) != develop (%s) after promote", mainRev, developRev)
	}
}

func TestRevertPushesUndoCommit(t *testing.T) {
	bareDir, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	d := New(mgr, nil, silentLogger())

	// Prod revert targets main after a promote (main == develop tip).
	if err := d.Promote(context.Background(), orchestrator.Signal{Repo: "svc"}); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	if err := d.Revert(context.Background(), orchestrator.Signal{Repo: "svc"}); err != nil {
		t.Fatalf("Revert: %v", err)
	}

	mainApp := runGit(t, bareDir, "show", "main:app.txt")
	if mainApp != "v1\n" {
		t.Errorf("main:app.txt after revert = %q, want %q", mainApp, "v1\n")
	}
	developApp := runGit(t, bareDir, "show", "develop:app.txt")
	if developApp != "v1\n" {
		t.Errorf("develop:app.txt after align = %q, want %q", developApp, "v1\n")
	}

	log := runGit(t, bareDir, "log", "main", "--oneline", "-3")
	if !strings.Contains(log, "Revert") {
		t.Errorf("expected a Revert commit on main, got:\n%s", log)
	}
}

// TestPromotePinnedToGatedSHA is the S3 regression at the dispatch
// level: Promote must ship the commit the gate passed on, and must
// refuse when develop has moved on since — otherwise a commit that
// landed after the smoke probe reaches prod untested.
func TestPromotePinnedToGatedSHA(t *testing.T) {
	t.Run("unmoved develop promotes the gated sha", func(t *testing.T) {
		bareDir, workDir := setupOrigin(t)
		d := New(testManager(t, workDir), nil, silentLogger())
		gated := strings.TrimSpace(runGit(t, bareDir, "rev-parse", "develop"))

		sig := orchestrator.Signal{Repo: "svc", Source: orchestrator.SourceDevGate, Kind: orchestrator.KindPass, SHA: gated}
		if err := d.Promote(context.Background(), sig); err != nil {
			t.Fatalf("Promote: %v", err)
		}
		if got := strings.TrimSpace(runGit(t, bareDir, "rev-parse", "main")); got != gated {
			t.Errorf("origin main = %s, want the gated sha %s", got, gated)
		}
	})

	t.Run("moved develop fails the promote", func(t *testing.T) {
		bareDir, workDir := setupOrigin(t)
		d := New(testManager(t, workDir), nil, silentLogger())
		gated := strings.TrimSpace(runGit(t, bareDir, "rev-parse", "develop"))
		mainBefore := strings.TrimSpace(runGit(t, bareDir, "rev-parse", "main"))

		// A commit lands on develop between the smoke pass and here. It
		// was never probed, so it must not reach prod.
		other := t.TempDir()
		runGit(t, other, "clone", "--branch", "develop", bareDir, "clone")
		side := filepath.Join(other, "clone")
		runGit(t, side, "config", "user.email", "test@example.com")
		runGit(t, side, "config", "user.name", "test")
		writeCommit(t, side, "app.txt", "ungated\n", "landed after the gate passed")
		runGit(t, side, "push", "origin", "develop")

		sig := orchestrator.Signal{Repo: "svc", Source: orchestrator.SourceDevGate, Kind: orchestrator.KindPass, SHA: gated}
		err := d.Promote(context.Background(), sig)
		if err == nil {
			t.Fatal("Promote succeeded against a moved develop — an ungated commit reached prod")
		}
		if !errors.Is(err, promote.ErrMoved) {
			t.Errorf("error = %v, want promote.ErrMoved", err)
		}
		if got := strings.TrimSpace(runGit(t, bareDir, "rev-parse", "main")); got != mainBefore {
			t.Errorf("main moved to %s despite the failed promote", got)
		}
	})

	t.Run("unpinned signal still promotes the branch tip", func(t *testing.T) {
		// Operator-initiated promotes (POST /api/actions, the CLI) carry
		// no gate result and no SHA; a human is the authorization.
		bareDir, workDir := setupOrigin(t)
		d := New(testManager(t, workDir), nil, silentLogger())
		if err := d.Promote(context.Background(), orchestrator.Signal{Repo: "svc"}); err != nil {
			t.Fatalf("Promote: %v", err)
		}
		if runGit(t, bareDir, "rev-parse", "main") != runGit(t, bareDir, "rev-parse", "develop") {
			t.Error("unpinned promote did not fast-forward main")
		}
	})
}

// TestPromoteRepinsAcrossTagCarry: the gitops tag carry commits to
// develop itself, which moves the tip off the gated SHA. That commit is
// ours and a direct child of the verified commit, so the promote stays
// pinned to it rather than silently falling back to a branch push.
func TestPromoteRepinsAcrossTagCarry(t *testing.T) {
	bareDir, workDir := setupOrigin(t)
	for _, env := range []string{"dev", "prod"} {
		dir := filepath.Join(workDir, "gitops", "values", env)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		tag := "sha-newtag"
		if env == "prod" {
			tag = "sha-oldtag"
		}
		if err := os.WriteFile(filepath.Join(dir, "svc.yaml"),
			[]byte("image:\n  repository: ghcr.io/org/svc\n  tag: \""+tag+"\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, workDir, "add", ".")
	runGit(t, workDir, "commit", "-m", "add gitops values")
	runGit(t, workDir, "push", "origin", "develop")

	d := New(testManager(t, workDir), nil, silentLogger())
	gated := strings.TrimSpace(runGit(t, bareDir, "rev-parse", "develop"))
	sig := orchestrator.Signal{Repo: "svc", Source: orchestrator.SourceDevGate, Kind: orchestrator.KindPass, SHA: gated}
	if err := d.Promote(context.Background(), sig); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	// main is the carry commit, whose parent is the gated commit.
	mainRev := strings.TrimSpace(runGit(t, bareDir, "rev-parse", "main"))
	if mainRev == gated {
		t.Fatal("tag carry did not commit")
	}
	if parent := strings.TrimSpace(runGit(t, bareDir, "rev-parse", "main^")); parent != gated {
		t.Errorf("main^ = %s, want the gated sha %s", parent, gated)
	}
	if got := runGit(t, bareDir, "show", "main:gitops/values/prod/svc.yaml"); !strings.Contains(got, "sha-newtag") {
		t.Errorf("prod values not carried: %s", got)
	}
}

func TestPromoteRevertCustomBranches(t *testing.T) {
	const dev, prod = "release", "production"
	bareDir, workDir := setupOriginBranches(t, prod, dev)
	mgr := repos.NewManager("unused-root", []config.Repo{
		{Name: "svc", GitHub: "org/svc", Dir: workDir, Branch: dev, ProdBranch: prod},
	}, nil)
	d := New(mgr, nil, silentLogger())

	if err := d.Promote(context.Background(), orchestrator.Signal{Repo: "svc"}); err != nil {
		t.Fatalf("Promote: %v", err)
	}
	prodRev := strings.TrimSpace(runGit(t, bareDir, "rev-parse", prod))
	devRev := strings.TrimSpace(runGit(t, bareDir, "rev-parse", dev))
	if prodRev != devRev {
		t.Errorf("origin %s (%s) != %s (%s) after promote", prod, prodRev, dev, devRev)
	}

	if err := d.Revert(context.Background(), orchestrator.Signal{Repo: "svc"}); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if got := runGit(t, bareDir, "show", prod+":app.txt"); got != "v1\n" {
		t.Errorf("%s:app.txt after revert = %q, want v1", prod, got)
	}
	if got := runGit(t, bareDir, "show", dev+":app.txt"); got != "v1\n" {
		t.Errorf("%s:app.txt after align = %q, want v1", dev, got)
	}
	if log := runGit(t, bareDir, "log", prod, "--oneline", "-3"); !strings.Contains(log, "Revert") {
		t.Errorf("expected Revert on %s, got:\n%s", prod, log)
	}
}

// fakeRunner simulates a subagent that "fixes" the repo by committing
// and pushing a change — proving Fix hands it a clean, correctly-synced
// working directory (see the EnsureCloned fetch+checkout+reset fix).
type fakeRunner struct {
	gotDir, gotPrompt string
}

func (f *fakeRunner) Run(ctx context.Context, dir, prompt string, _ []string) (string, error) {
	f.gotDir = dir
	f.gotPrompt = prompt
	writeCommitT(ctx, dir, "app.txt", "fixed\n", "fix from subagent")
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "push", "origin", "develop").CombinedOutput()
	return string(out), err
}

func writeCommitT(ctx context.Context, dir, file, content, msg string) {
	_ = os.WriteFile(filepath.Join(dir, file), []byte(content), 0o600)
	_ = exec.CommandContext(ctx, "git", "-C", dir, "add", ".").Run()
	_ = exec.CommandContext(ctx, "git", "-C", dir, "commit", "-m", msg).Run()
}

func TestFixRunsSubagentAgainstSyncedRepo(t *testing.T) {
	bareDir, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	runner := &fakeRunner{}
	d := New(mgr, runner, silentLogger())

	sig := orchestrator.Signal{
		Repo:     "svc",
		Source:   orchestrator.SourceCI,
		Kind:     orchestrator.KindFail,
		Evidence: map[string]any{"run_url": "http://ci/123"},
	}
	if err := d.Fix(context.Background(), sig); err != nil {
		t.Fatalf("Fix: %v", err)
	}

	if runner.gotDir != workDir {
		t.Errorf("subagent got dir %q, want %q", runner.gotDir, workDir)
	}
	if !strings.Contains(runner.gotPrompt, "run_url") || !strings.Contains(runner.gotPrompt, "svc") {
		t.Errorf("subagent prompt missing evidence/repo: %q", runner.gotPrompt)
	}

	developRev := strings.TrimSpace(runGit(t, bareDir, "rev-parse", "develop"))
	workRev := strings.TrimSpace(runGit(t, workDir, "rev-parse", "HEAD"))
	if developRev != workRev {
		t.Errorf("origin develop (%s) != local HEAD (%s) — subagent's push didn't land", developRev, workRev)
	}
}

func TestFixPromptUsesFixMode(t *testing.T) {
	_, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	runner := &fakeRunner{}
	d := New(mgr, runner, silentLogger())
	d.FixMode = "pr"

	sig := orchestrator.Signal{
		Repo:   "svc",
		Source: orchestrator.SourceCI,
		Kind:   orchestrator.KindFail,
	}
	if err := d.Fix(context.Background(), sig); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !strings.Contains(runner.gotPrompt, "open a PR") {
		t.Fatalf("expected pr-mode prompt, got:\n%s", runner.gotPrompt)
	}
	if strings.Contains(runner.gotPrompt, "commit to the current branch, and push") {
		t.Fatal("pr mode should not use direct-push wording")
	}
}

func TestFixModePRRecordsPRIntoEvidence(t *testing.T) {
	_, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	runner := &fakeRunner{}
	d := New(mgr, runner, silentLogger())
	d.FixMode = "pr"

	var gotOwnerRepo, gotBranch string
	d.FindPR = func(_ context.Context, ownerRepo, branch string) (*PRRef, error) {
		gotOwnerRepo, gotBranch = ownerRepo, branch
		return &PRRef{Number: 42, URL: "https://github.com/org/svc/pull/42", State: "open"}, nil
	}

	sig := orchestrator.Signal{
		Repo:     "svc",
		Source:   orchestrator.SourceCI,
		Kind:     orchestrator.KindFail,
		Evidence: map[string]any{},
	}
	if err := d.Fix(context.Background(), sig); err != nil {
		t.Fatalf("Fix: %v", err)
	}

	if gotOwnerRepo != "org/svc" {
		t.Errorf("FindPR ownerRepo = %q, want org/svc", gotOwnerRepo)
	}
	if !strings.HasPrefix(gotBranch, "xdlc-fix-") {
		t.Errorf("FindPR branch = %q, want xdlc-fix-<generated> prefix", gotBranch)
	}
	if !strings.Contains(runner.gotPrompt, gotBranch) {
		t.Errorf("prompt should instruct the exact branch FindPR was called with:\n%s", runner.gotPrompt)
	}
	if sig.Evidence["pr_number"] != 42 || sig.Evidence["pr_url"] != "https://github.com/org/svc/pull/42" || sig.Evidence["pr_state"] != "open" {
		t.Fatalf("pr evidence = %+v", sig.Evidence)
	}
	if sig.Evidence["pr_branch"] != gotBranch {
		t.Errorf("pr_branch evidence = %v, want %q", sig.Evidence["pr_branch"], gotBranch)
	}
}

func TestFixModePRNoMatchDoesNotFailDispatch(t *testing.T) {
	_, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	d := New(mgr, &fakeRunner{}, silentLogger())
	d.FixMode = "pr"
	d.FindPR = func(context.Context, string, string) (*PRRef, error) {
		return nil, nil // subagent didn't actually open a PR — not a lookup error
	}

	sig := orchestrator.Signal{Repo: "svc", Source: orchestrator.SourceCI, Kind: orchestrator.KindFail, Evidence: map[string]any{}}
	if err := d.Fix(context.Background(), sig); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if _, ok := sig.Evidence["pr_number"]; ok {
		t.Errorf("no PR found should not add pr_* evidence, got %+v", sig.Evidence)
	}
}

func TestFixModeDirectNeverCallsFindPR(t *testing.T) {
	_, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	d := New(mgr, &fakeRunner{}, silentLogger())
	// FixMode left "" (direct).
	called := false
	d.FindPR = func(context.Context, string, string) (*PRRef, error) {
		called = true
		return nil, nil
	}

	sig := orchestrator.Signal{Repo: "svc", Source: orchestrator.SourceCI, Kind: orchestrator.KindFail, Evidence: map[string]any{}}
	if err := d.Fix(context.Background(), sig); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if called {
		t.Error("direct mode must not call FindPR")
	}
}

// blockingRunner holds the first Run until release is closed.
type blockingRunner struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingRunner) Run(ctx context.Context, dir, prompt string, _ []string) (string, error) {
	close(b.started)
	select {
	case <-b.release:
		return "ok", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// costJSONRunner returns Claude-style cost JSON (and optional err).
type costJSONRunner struct {
	out string
	err error
}

func (c *costJSONRunner) Run(context.Context, string, string, []string) (string, error) {
	return c.out, c.err
}

func TestFixMergesCostIntoEvidence(t *testing.T) {
	_, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	const out = `{"total_cost_usd":0.02,"duration_ms":1500,"usage":{"input_tokens":11,"output_tokens":7}}`
	d := New(mgr, &costJSONRunner{out: out}, silentLogger())

	sig := orchestrator.Signal{
		Repo:     "svc",
		Source:   orchestrator.SourceCI,
		Kind:     orchestrator.KindFail,
		Evidence: map[string]any{"run_url": "http://ci/1"},
	}
	if err := d.Fix(context.Background(), sig); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if sig.Evidence["total_cost_usd"] != 0.02 {
		t.Fatalf("cost = %v", sig.Evidence["total_cost_usd"])
	}
	if sig.Evidence["duration_ms"] != int64(1500) {
		t.Fatalf("duration_ms = %v", sig.Evidence["duration_ms"])
	}
	if sig.Evidence["input_tokens"] != int64(11) || sig.Evidence["output_tokens"] != int64(7) {
		t.Fatalf("tokens = %v / %v", sig.Evidence["input_tokens"], sig.Evidence["output_tokens"])
	}
}

func TestFixMergesCostOnSubagentError(t *testing.T) {
	_, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	d := New(mgr, &costJSONRunner{
		out: `{"total_cost_usd":0.01,"duration_ms":100}`,
		err: context.Canceled,
	}, silentLogger())

	sig := orchestrator.Signal{
		Repo:     "svc",
		Evidence: map[string]any{},
	}
	if err := d.Fix(context.Background(), sig); err == nil {
		t.Fatal("expected error")
	}
	if sig.Evidence["total_cost_usd"] != 0.01 {
		t.Fatalf("cost on error = %v", sig.Evidence["total_cost_usd"])
	}
}

func TestFixConcurrencyCap(t *testing.T) {
	_, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	br := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	d := New(mgr, br, silentLogger())
	d.SetFixConcurrency(1)

	sig := orchestrator.Signal{Repo: "svc", Source: orchestrator.SourceCI, Kind: orchestrator.KindFail}
	errCh := make(chan error, 2)
	go func() { errCh <- d.Fix(context.Background(), sig) }()
	<-br.started

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := d.Fix(ctx, sig); err == nil {
		t.Fatal("second Fix should block until slot free / ctx cancel")
	}

	close(br.release)
	if err := <-errCh; err != nil {
		t.Fatalf("first Fix: %v", err)
	}
}
