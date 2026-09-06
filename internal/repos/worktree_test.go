package repos

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
)

// worktreeFixture builds a bare origin with a develop branch and a clone
// of it, then returns a Manager rooted at a scratch dir.
func worktreeFixture(t *testing.T) (mgr *Manager, bareDir, workDir string) {
	t.Helper()
	root := t.TempDir()
	bareDir = filepath.Join(root, "origin.git")
	seedDir := filepath.Join(root, "seed")
	workDir = filepath.Join(root, "clone")

	gitCmdTest(t, root, "init", "--bare", bareDir)
	gitCmdTest(t, root, "clone", bareDir, seedDir)
	gitCmdTest(t, seedDir, "config", "user.email", "test@example.com")
	gitCmdTest(t, seedDir, "config", "user.name", "test")
	gitCmdTest(t, seedDir, "checkout", "-b", "develop")
	if err := os.WriteFile(filepath.Join(seedDir, "app.txt"), []byte("v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitCmdTest(t, seedDir, "add", ".")
	gitCmdTest(t, seedDir, "commit", "-m", "v1")
	gitCmdTest(t, seedDir, "push", "origin", "develop")
	gitCmdTest(t, bareDir, "symbolic-ref", "HEAD", "refs/heads/develop")

	gitCmdTest(t, root, "clone", "--branch", "develop", bareDir, workDir)
	gitCmdTest(t, workDir, "config", "user.email", "test@example.com")
	gitCmdTest(t, workDir, "config", "user.name", "test")

	mgr = NewManager(filepath.Join(root, "managed"), []config.Repo{
		{Name: "svc", GitHub: "org/svc", Dir: workDir, Branch: "develop"},
	}, nil)
	return mgr, bareDir, workDir
}

func commitInWorktree(t *testing.T, dir, content, msg string) {
	t.Helper()
	gitCmdTest(t, dir, "config", "user.email", "test@example.com")
	gitCmdTest(t, dir, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "app.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	gitCmdTest(t, dir, "add", ".")
	gitCmdTest(t, dir, "commit", "-m", msg)
}

func TestWorktreeIsolatesFixFromSharedClone(t *testing.T) {
	mgr, bareDir, workDir := worktreeFixture(t)
	ctx := context.Background()

	w, err := mgr.Worktree(ctx, "svc", "20260906T000000Z-svc")
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	if w.Branch != "xdlc/20260906T000000Z-svc" {
		t.Fatalf("branch = %q", w.Branch)
	}
	// The worktree must live outside the shared clone: nested inside it,
	// the clone would read as dirty and get hard-reset every pass.
	if strings.HasPrefix(w.Dir, workDir+string(filepath.Separator)) {
		t.Fatalf("worktree %s is inside the shared clone %s", w.Dir, workDir)
	}

	commitInWorktree(t, w.Dir, "fixed\n", "fix")

	// The shared clone must not see the agent's edit at all.
	shared, err := os.ReadFile(filepath.Join(workDir, "app.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(shared)) != "v1" {
		t.Fatalf("shared clone app.txt = %q, want untouched v1", shared)
	}
	if out := gitCmdTest(t, workDir, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("shared clone dirty after worktree Fix: %q", out)
	}

	if !mgr.HasCommits(ctx, w) {
		t.Fatal("HasCommits should be true after a commit")
	}
	if err := mgr.Push(ctx, w, ""); err != nil {
		t.Fatalf("Push: %v", err)
	}
	got := gitCmdTest(t, bareDir, "show", "develop:app.txt")
	if strings.TrimSpace(got) != "fixed" {
		t.Fatalf("origin develop app.txt = %q, want fixed", got)
	}

	if err := mgr.Remove(ctx, w); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(w.Dir); !os.IsNotExist(err) {
		t.Fatalf("worktree dir still present after Remove: %v", err)
	}
	if out := gitCmdTest(t, workDir, "worktree", "list"); strings.Contains(out, "xdlc") {
		t.Fatalf("worktree still registered: %s", out)
	}
	if out := gitCmdTest(t, workDir, "branch", "--list", "xdlc/*"); strings.TrimSpace(out) != "" {
		t.Fatalf("worktree branch left behind: %q", out)
	}
}

// The reason worktrees exist: two Fixes on one repo at once.
func TestTwoWorktreesForOneRepoAreIndependent(t *testing.T) {
	mgr, _, _ := worktreeFixture(t)
	ctx := context.Background()

	a, err := mgr.Worktree(ctx, "svc", "run-a")
	if err != nil {
		t.Fatalf("Worktree a: %v", err)
	}
	b, err := mgr.Worktree(ctx, "svc", "run-b")
	if err != nil {
		t.Fatalf("Worktree b: %v", err)
	}
	if a.Dir == b.Dir || a.Branch == b.Branch {
		t.Fatalf("worktrees collide: %+v %+v", a, b)
	}

	commitInWorktree(t, a.Dir, "from-a\n", "a")
	commitInWorktree(t, b.Dir, "from-b\n", "b")

	for _, c := range []struct {
		dir, want string
	}{{a.Dir, "from-a"}, {b.Dir, "from-b"}} {
		got, err := os.ReadFile(filepath.Join(c.dir, "app.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(got)) != c.want {
			t.Fatalf("%s app.txt = %q, want %q", c.dir, got, c.want)
		}
	}
	_ = mgr.Remove(ctx, a)
	_ = mgr.Remove(ctx, b)
}

// A second push to a branch that moved underneath must be refused rather
// than overwrite the commit that landed first.
func TestWorktreePushRefusesStaleTarget(t *testing.T) {
	mgr, _, workDir := worktreeFixture(t)
	ctx := context.Background()

	a, err := mgr.Worktree(ctx, "svc", "run-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := mgr.Worktree(ctx, "svc", "run-b")
	if err != nil {
		t.Fatal(err)
	}
	commitInWorktree(t, a.Dir, "from-a\n", "a")
	commitInWorktree(t, b.Dir, "from-b\n", "b")

	if err := mgr.Push(ctx, a, ""); err != nil {
		t.Fatalf("first push: %v", err)
	}
	if err := mgr.Push(ctx, b, ""); err == nil {
		t.Fatal("second push onto a moved branch must be refused, not forced")
	}
	_ = workDir
}

func TestHasCommitsFalseWhenAgentChangedNothing(t *testing.T) {
	mgr, _, _ := worktreeFixture(t)
	ctx := context.Background()
	w, err := mgr.Worktree(ctx, "svc", "empty-run")
	if err != nil {
		t.Fatal(err)
	}
	if mgr.HasCommits(ctx, w) {
		t.Fatal("a worktree with no commits must not report work to push")
	}
}

// A killed Fix leaves its worktree for inspection; the sweep collects it
// once it is past the keep window, and leaves fresh ones alone.
func TestPruneWorktreesRespectsKeepWindow(t *testing.T) {
	mgr, _, _ := worktreeFixture(t)
	ctx := context.Background()

	old, err := mgr.Worktree(ctx, "svc", "old-run")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := mgr.Worktree(ctx, "svc", "fresh-run")
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old.Dir, past, past); err != nil {
		t.Fatal(err)
	}
	// Both Fixes have ended; only age decides now.
	mgr.Done(old)
	mgr.Done(fresh)

	n, err := mgr.PruneWorktrees(ctx, "svc", 24*time.Hour)
	if err != nil {
		t.Fatalf("PruneWorktrees: %v", err)
	}
	if n != 1 {
		t.Fatalf("pruned %d, want 1", n)
	}
	if _, err := os.Stat(old.Dir); !os.IsNotExist(err) {
		t.Fatal("stale worktree survived the sweep")
	}
	if _, err := os.Stat(fresh.Dir); err != nil {
		t.Fatalf("fresh worktree was swept: %v", err)
	}
}

func TestPruneWorktreesNoDirectory(t *testing.T) {
	mgr, _, _ := worktreeFixture(t)
	n, err := mgr.PruneWorktrees(context.Background(), "svc", time.Hour)
	if err != nil || n != 0 {
		t.Fatalf("PruneWorktrees on a fresh manager = (%d, %v), want (0, nil)", n, err)
	}
}

// Reusing an id (a retried run, a restarted daemon) must not fail on the
// leftover directory.
func TestWorktreeReusesIDCleanly(t *testing.T) {
	mgr, _, _ := worktreeFixture(t)
	ctx := context.Background()
	first, err := mgr.Worktree(ctx, "svc", "same-id")
	if err != nil {
		t.Fatal(err)
	}
	commitInWorktree(t, first.Dir, "stale\n", "stale")

	second, err := mgr.Worktree(ctx, "svc", "same-id")
	if err != nil {
		t.Fatalf("re-creating a worktree with the same id: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(second.Dir, "app.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "v1" {
		t.Fatalf("reused worktree kept stale content %q", got)
	}
}

// A Fix still running owns its worktree however old the directory looks.
// Without this the sweep could delete a live checkout mid-edit whenever a
// run outlasts keep_failed.
func TestPruneSkipsWorktreeOfRunningFix(t *testing.T) {
	mgr, _, _ := worktreeFixture(t)
	ctx := context.Background()

	w, err := mgr.Worktree(ctx, "svc", "long-run")
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(w.Dir, past, past); err != nil {
		t.Fatal(err)
	}

	n, err := mgr.PruneWorktrees(ctx, "svc", time.Hour)
	if err != nil {
		t.Fatalf("PruneWorktrees: %v", err)
	}
	if n != 0 {
		t.Fatalf("pruned %d worktrees of a running Fix, want 0", n)
	}
	if _, err := os.Stat(w.Dir); err != nil {
		t.Fatalf("running Fix's worktree was swept: %v", err)
	}

	// Once the Fix reports done, the sweep may collect it.
	mgr.Done(w)
	n, err = mgr.PruneWorktrees(ctx, "svc", time.Hour)
	if err != nil {
		t.Fatalf("PruneWorktrees after Done: %v", err)
	}
	if n != 1 {
		t.Fatalf("pruned %d after Done, want 1", n)
	}
}

// Two Fixes for one repo now overlap, so the shared clone's git commands
// must not run concurrently: git collides on .git/index.lock and on
// remote-tracking ref locks.
func TestConcurrentEnsureClonedAndWorktreeAreSerialized(t *testing.T) {
	mgr, _, workDir := worktreeFixture(t)
	ctx := context.Background()

	// Make the clone stale and dirty so EnsureCloned really does
	// fetch + checkout + reset rather than short-circuiting.
	if err := os.WriteFile(filepath.Join(workDir, "app.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	const n = 6
	errs := make(chan error, n*2)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- mgr.EnsureCloned(ctx, "svc")
		}()
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w, err := mgr.Worktree(ctx, "svc", fmt.Sprintf("run-%d", i))
			if err != nil {
				errs <- err
				return
			}
			errs <- mgr.Remove(ctx, w)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent shared-clone operation failed: %v", err)
		}
	}
}
