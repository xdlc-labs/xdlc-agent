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
	"sync"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
	"github.com/xdlc-labs/xdlc-agent/internal/orchestrator"
	"github.com/xdlc-labs/xdlc-agent/internal/promote"
	"github.com/xdlc-labs/xdlc-agent/internal/repos"
	"github.com/xdlc-labs/xdlc-agent/internal/session"
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

func TestFixReverifyFailDoesNotReportOK(t *testing.T) {
	_, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	runner := &fakeRunner{}
	d := New(mgr, runner, silentLogger())
	d.Reverify = func(_ context.Context, _ orchestrator.Signal) error {
		return errors.New("still red")
	}
	sig := orchestrator.Signal{
		Repo:     "svc",
		Source:   orchestrator.SourceCI,
		Kind:     orchestrator.KindFail,
		Evidence: map[string]any{},
	}
	err := d.Fix(context.Background(), sig)
	if err == nil {
		t.Fatal("expected reverify error")
	}
	if !strings.Contains(err.Error(), "reverify") {
		t.Fatalf("error = %v", err)
	}
	if sig.Evidence["escalate"] != "reverify_failed" {
		t.Fatalf("escalate = %v", sig.Evidence["escalate"])
	}
}

func TestFixReverifyPass(t *testing.T) {
	_, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	runner := &fakeRunner{}
	d := New(mgr, runner, silentLogger())
	d.Reverify = func(_ context.Context, _ orchestrator.Signal) error { return nil }
	sig := orchestrator.Signal{
		Repo:     "svc",
		Source:   orchestrator.SourceCI,
		Kind:     orchestrator.KindFail,
		Evidence: map[string]any{},
	}
	if err := d.Fix(context.Background(), sig); err != nil {
		t.Fatal(err)
	}
	if sig.Evidence["reverify"] != "pass" {
		t.Fatalf("reverify = %v", sig.Evidence["reverify"])
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

// multiBlockRunner maps successive Run calls onto blockingRunner slots.
type multiBlockRunner struct {
	mu     sync.Mutex
	starts []*blockingRunner
	n      int
}

func (m *multiBlockRunner) Run(ctx context.Context, dir, prompt string, env []string) (string, error) {
	m.mu.Lock()
	idx := m.n
	m.n++
	var br *blockingRunner
	if idx < len(m.starts) {
		br = m.starts[idx]
	} else {
		br = &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
		close(br.release) // unexpected extras finish immediately
	}
	m.mu.Unlock()
	return br.Run(ctx, dir, prompt, env)
}

func TestFairDrainMultiRepo(t *testing.T) {
	_, workA := setupOrigin(t)
	_, workB := setupOrigin(t)
	mgr := repos.NewManager("unused", []config.Repo{
		{Name: "a", GitHub: "org/a", Dir: workA, Branch: "develop"},
		{Name: "b", GitHub: "org/b", Dir: workB, Branch: "develop"},
	}, nil)

	brA := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	brB := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	runner := &multiBlockRunner{starts: []*blockingRunner{brA, brB}}
	d := New(mgr, runner, silentLogger())
	d.SetFixConcurrency(2)

	errA := make(chan error, 1)
	go func() {
		errA <- d.Fix(context.Background(), orchestrator.Signal{Repo: "a", Source: orchestrator.SourceCI, Kind: orchestrator.KindFail})
	}()
	select {
	case <-brA.started:
	case <-time.After(2 * time.Second):
		t.Fatal("repo A Fix never started")
	}

	// Second Fix on A must wait (per-repo cap 1) even though global has a free slot.
	ctxA2, cancelA2 := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancelA2()
	errA2 := make(chan error, 1)
	go func() {
		errA2 <- d.Fix(ctxA2, orchestrator.Signal{Repo: "a", Source: orchestrator.SourceCI, Kind: orchestrator.KindFail})
	}()

	// Repo B should acquire the other global slot while A holds one.
	errB := make(chan error, 1)
	go func() {
		errB <- d.Fix(context.Background(), orchestrator.Signal{Repo: "b", Source: orchestrator.SourceCI, Kind: orchestrator.KindFail})
	}()
	select {
	case <-brB.started:
	case <-time.After(2 * time.Second):
		t.Fatal("repo B Fix starved by repo A")
	}

	select {
	case err := <-errA2:
		if err == nil {
			t.Fatal("second Fix on A should have blocked / timed out")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Fix on A did not return after timeout")
	}

	w, in := d.FixQueueStats()
	if in < 1 {
		t.Fatalf("expected inflight ≥1, got waiting=%d inflight=%d", w, in)
	}

	close(brA.release)
	close(brB.release)
	if err := <-errA; err != nil {
		t.Fatalf("A: %v", err)
	}
	if err := <-errB; err != nil {
		t.Fatalf("B: %v", err)
	}
}

func TestFixBudgetTimeout(t *testing.T) {
	_, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	br := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	d := New(mgr, br, silentLogger())
	d.FixBudget = 50 * time.Millisecond

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Fix(context.Background(), orchestrator.Signal{Repo: "svc", Source: orchestrator.SourceCI, Kind: orchestrator.KindFail})
	}()
	<-br.started
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected budget timeout")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Fix did not return after budget")
	}
	close(br.release)
}

// planThenFixRunner: first call returns plan text (no git); second commits like fakeRunner.
type planThenFixRunner struct {
	calls   int
	prompts []string
}

func (f *planThenFixRunner) Run(ctx context.Context, dir, prompt string, _ []string) (string, error) {
	f.calls++
	f.prompts = append(f.prompts, prompt)
	if f.calls == 1 {
		return "1. change app.txt to fixed", nil
	}
	writeCommitT(ctx, dir, "app.txt", "fixed\n", "fix from subagent")
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "push", "origin", "develop").CombinedOutput()
	return string(out), err
}

func TestFixPlanOffSingleRunnerCall(t *testing.T) {
	_, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	runner := &planThenFixRunner{}
	d := New(mgr, runner, silentLogger())
	// FixPlan left false
	sig := orchestrator.Signal{
		Repo: "svc", Source: orchestrator.SourceCI, Kind: orchestrator.KindFail,
		Evidence: map[string]any{},
	}
	if err := d.Fix(context.Background(), sig); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 1 {
		t.Fatalf("calls = %d, want 1", runner.calls)
	}
	if strings.Contains(runner.prompts[0], "Do NOT edit files") {
		t.Fatal("one-shot must not use plan prompt")
	}
}

func TestFixPlanOnTwoRunnerCalls(t *testing.T) {
	_, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	runner := &planThenFixRunner{}
	d := New(mgr, runner, silentLogger())
	d.FixPlan = true
	sig := orchestrator.Signal{
		Repo: "svc", Source: orchestrator.SourceCI, Kind: orchestrator.KindFail,
		Evidence: map[string]any{},
	}
	if err := d.Fix(context.Background(), sig); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 2 {
		t.Fatalf("calls = %d, want 2", runner.calls)
	}
	if !strings.Contains(runner.prompts[0], "Do NOT edit files") {
		t.Fatalf("pass1 should be plan-only:\n%s", runner.prompts[0])
	}
	if !strings.Contains(runner.prompts[1], "1. change app.txt to fixed") {
		t.Fatalf("pass2 missing plan text:\n%s", runner.prompts[1])
	}
	if !strings.Contains(runner.prompts[1], "Implement the trusted plan") {
		t.Fatalf("pass2 missing implement instruction:\n%s", runner.prompts[1])
	}
	if sig.Evidence["fix_plan"] != "used" {
		t.Fatalf("fix_plan evidence = %v", sig.Evidence["fix_plan"])
	}
}

// TestFixRecordsSession covers the operator's after-the-fact question:
// what was the agent told, what did it say, what did it change.
func TestFixRecordsSession(t *testing.T) {
	_, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	store, err := session.Open(t.TempDir(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	d := New(mgr, &fakeRunner{}, silentLogger())
	d.Sessions = store
	d.DefaultProvider = "claude"

	sig := orchestrator.Signal{
		Repo:     "svc",
		Source:   orchestrator.SourceCI,
		Kind:     orchestrator.KindFail,
		Evidence: map[string]any{"run_url": "http://ci/123"},
	}
	if err := d.Fix(context.Background(), sig); err != nil {
		t.Fatalf("Fix: %v", err)
	}

	metas, err := store.List("svc", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 {
		t.Fatalf("want 1 recorded session, got %d", len(metas))
	}
	m := metas[0]
	if m.Status != "ok" || m.Provider != "claude" || m.Source != "ci" {
		t.Fatalf("unexpected meta: %+v", m)
	}
	if m.BaseSHA == "" || m.HeadSHA == "" || m.BaseSHA == m.HeadSHA {
		t.Fatalf("want a base..head range for the agent's commit: %+v", m)
	}
	if id, ok := sig.Evidence["session_id"].(string); !ok || id != m.ID {
		t.Fatalf("session id missing from evidence: %v", sig.Evidence["session_id"])
	}

	prompt, err := store.ReadFile(m.ID, session.FilePrompt)
	if err != nil || !strings.Contains(prompt, "run_url") {
		t.Fatalf("prompt not recorded: %q %v", prompt, err)
	}
	diff, err := store.ReadFile(m.ID, session.FileDiff)
	if err != nil || !strings.Contains(diff, "fixed") {
		t.Fatalf("diff not recorded: %q %v", diff, err)
	}
}

// TestFixRecordsFailedSession: a failed Fix is the one you most want to
// read afterwards, so the recording must survive the error path.
func TestFixRecordsFailedSession(t *testing.T) {
	_, workDir := setupOrigin(t)
	store, err := session.Open(t.TempDir(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	d := New(testManager(t, workDir), &errRunner{}, silentLogger())
	d.Sessions = store

	sig := orchestrator.Signal{
		Repo: "svc", Source: orchestrator.SourceCI, Kind: orchestrator.KindFail,
		Evidence: map[string]any{},
	}
	if err := d.Fix(context.Background(), sig); err == nil {
		t.Fatal("want the subagent error to surface")
	}
	metas, err := store.List("svc", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].Status != "error" || metas[0].Error == "" {
		t.Fatalf("failed run not recorded: %+v", metas)
	}
	out, err := store.ReadFile(metas[0].ID, session.FileOutput)
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Fatalf("errRunner prints nothing; got %q", out)
	}
}

// errRunner fails without touching the repo.
type errRunner struct{}

func (errRunner) Run(context.Context, string, string, []string) (string, error) {
	return "", errors.New("agent exploded")
}

func TestFixPromptCarriesOperatorInstructions(t *testing.T) {
	_, workDir := setupOrigin(t)
	runner := &fakeRunner{}
	d := New(testManager(t, workDir), runner, silentLogger())

	sig := orchestrator.Signal{
		Repo: "svc", Source: orchestrator.SourceCI, Kind: orchestrator.KindFail,
		Evidence:             map[string]any{"run_url": "http://ci/1"},
		OperatorInstructions: "the flake is in the seed data",
	}
	if err := d.Fix(context.Background(), sig); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !strings.Contains(runner.gotPrompt, "the flake is in the seed data") {
		t.Fatalf("operator instructions missing from prompt:\n%s", runner.gotPrompt)
	}
	// Operator text is trusted input: it must sit outside the untrusted
	// evidence block, like AGENTS.md rules do.
	trusted := strings.Index(runner.gotPrompt, "operator instructions for this run")
	evidence := strings.Index(runner.gotPrompt, "---BEGIN UNTRUSTED EVIDENCE---")
	if trusted < 0 || evidence < 0 || trusted > evidence {
		t.Fatalf("operator instructions landed in the untrusted block:\n%s", runner.gotPrompt)
	}
}

func TestFixUsesGlobalRulesFile(t *testing.T) {
	_, workDir := setupOrigin(t)
	rules := filepath.Join(t.TempDir(), "rules.md")
	if err := os.WriteFile(rules, []byte("never touch generated files"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	d := New(testManager(t, workDir), runner, silentLogger())
	d.RulesFile = rules

	sig := orchestrator.Signal{
		Repo: "svc", Source: orchestrator.SourceCI, Kind: orchestrator.KindFail,
		Evidence: map[string]any{},
	}
	if err := d.Fix(context.Background(), sig); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !strings.Contains(runner.gotPrompt, "never touch generated files") {
		t.Fatalf("global rules file missing from prompt:\n%s", runner.gotPrompt)
	}
}
