package repos

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
)

func gitCmdTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (in %s): %v: %s", args, dir, err, out)
	}
	return string(out)
}

func TestResolve(t *testing.T) {
	mgr := NewManager("repos", []config.Repo{
		{Name: "example-service", GitHub: "your-org/example-service"},
		{Name: "api", GitHub: "your-org/api"},
	}, nil)

	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"example-service", "example-service", true},
		{"your-org/example-service", "example-service", true},
		{"your-org/api", "api", true},
		{"unknown/repo", "", false},
	}
	for _, c := range cases {
		got, ok := mgr.Resolve(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("Resolve(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestBranchDefaults(t *testing.T) {
	mgr := NewManager("repos", []config.Repo{
		{Name: "defaults"},
		{Name: "custom", Branch: "release", ProdBranch: "production"},
	}, nil)
	cases := []struct {
		repo, wantDev, wantProd string
	}{
		{"defaults", "develop", "main"},
		{"custom", "release", "production"},
		{"unknown", "develop", "main"},
	}
	for _, c := range cases {
		if got := mgr.Branch(c.repo); got != c.wantDev {
			t.Errorf("Branch(%q) = %q, want %q", c.repo, got, c.wantDev)
		}
		if got := mgr.ProdBranch(c.repo); got != c.wantProd {
			t.Errorf("ProdBranch(%q) = %q, want %q", c.repo, got, c.wantProd)
		}
	}
}

// TestBranchByGitHub: the CI gate and the CI webhook know a repo by its
// "owner/name", so the per-repo branch has to be resolvable that way
// too — otherwise a repo on `branch: main` gets no CI signals (C2).
func TestBranchByGitHub(t *testing.T) {
	mgr := NewManager("repos", []config.Repo{
		{Name: "trunk", GitHub: "org/trunk", Branch: "main"},
		{Name: "defaults", GitHub: "org/defaults"},
	}, nil)
	cases := []struct{ in, want string }{
		{"org/trunk", "main"},
		{"org/defaults", DefaultBranch},
		{"org/unknown", DefaultBranch},
		{"", DefaultBranch},
	}
	for _, c := range cases {
		if got := mgr.BranchByGitHub(c.in); got != c.want {
			t.Errorf("BranchByGitHub(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRemoteSHA is the input to the promote pin (S3): the commit a gate
// result is attributed to has to come from origin, not from a local
// clone that may be behind.
func TestRemoteSHA(t *testing.T) {
	root := t.TempDir()
	bareDir := filepath.Join(root, "origin.git")
	seedDir := filepath.Join(root, "seed")
	workDir := filepath.Join(root, "work")

	gitCmdTest(t, root, "init", "--bare", bareDir)
	gitCmdTest(t, root, "clone", bareDir, seedDir)
	gitCmdTest(t, seedDir, "config", "user.email", "test@example.com")
	gitCmdTest(t, seedDir, "config", "user.name", "test")
	gitCmdTest(t, seedDir, "checkout", "-b", "release")
	if err := os.WriteFile(filepath.Join(seedDir, "app.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmdTest(t, seedDir, "add", ".")
	gitCmdTest(t, seedDir, "commit", "-m", "v1")
	gitCmdTest(t, seedDir, "push", "origin", "release")
	gitCmdTest(t, root, "clone", "--branch", "release", bareDir, workDir)

	mgr := NewManager("unused-root", []config.Repo{
		{Name: "svc", GitHub: "org/svc", Dir: workDir, Branch: "release"},
	}, nil)

	want := strings.TrimSpace(gitCmdTest(t, bareDir, "rev-parse", "release"))
	got, err := mgr.RemoteSHA(context.Background(), "svc")
	if err != nil {
		t.Fatalf("RemoteSHA: %v", err)
	}
	if got != want {
		t.Errorf("RemoteSHA = %q, want %q", got, want)
	}

	// A commit landing on origin is visible without touching the clone.
	if err := os.WriteFile(filepath.Join(seedDir, "app.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmdTest(t, seedDir, "add", ".")
	gitCmdTest(t, seedDir, "commit", "-m", "v2")
	gitCmdTest(t, seedDir, "push", "origin", "release")

	moved := strings.TrimSpace(gitCmdTest(t, bareDir, "rev-parse", "release"))
	got, err = mgr.RemoteSHA(context.Background(), "svc")
	if err != nil {
		t.Fatalf("RemoteSHA after push: %v", err)
	}
	if got != moved {
		t.Errorf("RemoteSHA = %q, want the new origin tip %q (stale local clone)", got, moved)
	}

	if _, err := mgr.RemoteSHA(context.Background(), "unknown"); err == nil {
		t.Error("expected an error for an unconfigured repo")
	}
}

func TestGitHub(t *testing.T) {
	mgr := NewManager("repos", []config.Repo{
		{Name: "svc", GitHub: "org/svc"},
	}, nil)
	if got := mgr.GitHub("svc"); got != "org/svc" {
		t.Errorf("GitHub(svc) = %q, want org/svc", got)
	}
	if got := mgr.GitHub("unknown"); got != "" {
		t.Errorf("GitHub(unknown) = %q, want empty", got)
	}
}

func TestEnsureClonedResetsStaleWorkingTree(t *testing.T) {
	root := t.TempDir()
	bareDir := filepath.Join(root, "origin.git")
	seedDir := filepath.Join(root, "seed")
	workDir := filepath.Join(root, "work")

	gitCmdTest(t, root, "init", "--bare", bareDir)
	gitCmdTest(t, root, "clone", bareDir, seedDir)
	gitCmdTest(t, seedDir, "config", "user.email", "test@example.com")
	gitCmdTest(t, seedDir, "config", "user.name", "test")
	gitCmdTest(t, seedDir, "checkout", "-b", "develop")
	if err := os.WriteFile(filepath.Join(seedDir, "app.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmdTest(t, seedDir, "add", ".")
	gitCmdTest(t, seedDir, "commit", "-m", "v1")
	gitCmdTest(t, seedDir, "push", "origin", "develop")
	gitCmdTest(t, bareDir, "symbolic-ref", "HEAD", "refs/heads/develop")

	gitCmdTest(t, root, "clone", "--branch", "develop", bareDir, workDir)
	gitCmdTest(t, workDir, "config", "user.email", "test@example.com")
	gitCmdTest(t, workDir, "config", "user.name", "test")

	// Stale the local clone two ways: an uncommitted local edit, and a
	// second commit lands on origin that the local clone doesn't have.
	if err := os.WriteFile(filepath.Join(workDir, "app.txt"), []byte("uncommitted local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seedDir, "app.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmdTest(t, seedDir, "add", ".")
	gitCmdTest(t, seedDir, "commit", "-m", "v2")
	gitCmdTest(t, seedDir, "push", "origin", "develop")

	mgr := NewManager("unused-root", []config.Repo{
		{Name: "svc", GitHub: "org/svc", Dir: workDir, Branch: "develop"},
	}, nil)

	if err := mgr.EnsureCloned(context.Background(), "svc"); err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(workDir, "app.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "v2" {
		t.Errorf("app.txt = %q after EnsureCloned, want %q (origin's latest, uncommitted local edit discarded)", got, "v2")
	}

	localRev := strings.TrimSpace(gitCmdTest(t, workDir, "rev-parse", "HEAD"))
	originRev := strings.TrimSpace(gitCmdTest(t, bareDir, "rev-parse", "develop"))
	if localRev != originRev {
		t.Errorf("local HEAD (%s) != origin develop (%s) after EnsureCloned", localRev, originRev)
	}
}

func TestEnsureClonedSkipsFetchWhenSynced(t *testing.T) {
	root := t.TempDir()
	bareDir := filepath.Join(root, "origin.git")
	seedDir := filepath.Join(root, "seed")
	workDir := filepath.Join(root, "work")

	gitCmdTest(t, root, "init", "--bare", bareDir)
	gitCmdTest(t, root, "clone", bareDir, seedDir)
	gitCmdTest(t, seedDir, "config", "user.email", "test@example.com")
	gitCmdTest(t, seedDir, "config", "user.name", "test")
	gitCmdTest(t, seedDir, "checkout", "-b", "develop")
	if err := os.WriteFile(filepath.Join(seedDir, "app.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmdTest(t, seedDir, "add", ".")
	gitCmdTest(t, seedDir, "commit", "-m", "v1")
	gitCmdTest(t, seedDir, "push", "origin", "develop")
	gitCmdTest(t, bareDir, "symbolic-ref", "HEAD", "refs/heads/develop")
	gitCmdTest(t, root, "clone", "--branch", "develop", bareDir, workDir)

	mgr := NewManager("unused-root", []config.Repo{
		{Name: "svc", GitHub: "org/svc", Dir: workDir, Branch: "develop"},
	}, nil)

	// Prime FETCH_HEAD, then a synced EnsureCloned must not touch it.
	if err := mgr.EnsureCloned(context.Background(), "svc"); err != nil {
		t.Fatalf("prime EnsureCloned: %v", err)
	}
	// Force a fetch so FETCH_HEAD exists, then sync again after a pause
	// so mtime comparison is meaningful.
	gitCmdTest(t, workDir, "fetch", "origin", "develop")
	fetchHead := filepath.Join(workDir, ".git", "FETCH_HEAD")
	before, err := os.Stat(fetchHead)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := mgr.EnsureCloned(context.Background(), "svc"); err != nil {
		t.Fatalf("synced EnsureCloned: %v", err)
	}
	after, err := os.Stat(fetchHead)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("FETCH_HEAD mtime changed (%v → %v); expected skip-fetch no-op", before.ModTime(), after.ModTime())
	}
}

// TestEnsureClonedFetchesWhenTrackingRefStale is the snackytalky
// regression: HEAD equals the local origin/<branch> tracking ref, so the
// old skip-fetch path thought the clone was current, while origin had
// moved. A Fix then edited the previous (green) commit.
func TestEnsureClonedFetchesWhenTrackingRefStale(t *testing.T) {
	root := t.TempDir()
	bareDir := filepath.Join(root, "origin.git")
	seedDir := filepath.Join(root, "seed")
	workDir := filepath.Join(root, "work")

	gitCmdTest(t, root, "init", "--bare", bareDir)
	gitCmdTest(t, root, "clone", bareDir, seedDir)
	gitCmdTest(t, seedDir, "config", "user.email", "test@example.com")
	gitCmdTest(t, seedDir, "config", "user.name", "test")
	gitCmdTest(t, seedDir, "checkout", "-b", "develop")
	if err := os.WriteFile(filepath.Join(seedDir, "app.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmdTest(t, seedDir, "add", ".")
	gitCmdTest(t, seedDir, "commit", "-m", "v1")
	gitCmdTest(t, seedDir, "push", "origin", "develop")
	gitCmdTest(t, bareDir, "symbolic-ref", "HEAD", "refs/heads/develop")
	gitCmdTest(t, root, "clone", "--branch", "develop", bareDir, workDir)

	if err := os.WriteFile(filepath.Join(seedDir, "app.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmdTest(t, seedDir, "add", ".")
	gitCmdTest(t, seedDir, "commit", "-m", "v2")
	gitCmdTest(t, seedDir, "push", "origin", "develop")

	local := strings.TrimSpace(gitCmdTest(t, workDir, "rev-parse", "HEAD"))
	tracking := strings.TrimSpace(gitCmdTest(t, workDir, "rev-parse", "origin/develop"))
	if local != tracking {
		t.Fatalf("precondition: HEAD %s != origin/develop %s", local, tracking)
	}
	want := strings.TrimSpace(gitCmdTest(t, bareDir, "rev-parse", "develop"))
	if local == want {
		t.Fatal("precondition: clone already has origin's new tip")
	}

	mgr := NewManager("unused-root", []config.Repo{
		{Name: "svc", GitHub: "org/svc", Dir: workDir, Branch: "develop"},
	}, nil)
	if err := mgr.EnsureCloned(context.Background(), "svc"); err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(workDir, "app.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "v2" {
		t.Errorf("app.txt = %q after EnsureCloned, want v2 (origin moved, tracking ref was stale)", got)
	}
	head := strings.TrimSpace(gitCmdTest(t, workDir, "rev-parse", "HEAD"))
	if head != want {
		t.Errorf("HEAD %s != origin develop %s", head, want)
	}
}

// TestEnsureClonedFetchesProdFromShallowSingleBranch is shop dogfood F6:
// the old clone args left origin/main missing, so revert died on
// rev-parse and promote reported a non-fast-forward against a history
// that was a fast-forward on GitHub.
func TestEnsureClonedFetchesProdFromShallowSingleBranch(t *testing.T) {
	root := t.TempDir()
	bareDir := filepath.Join(root, "origin.git")
	seedDir := filepath.Join(root, "seed")
	workDir := filepath.Join(root, "work")

	gitCmdTest(t, root, "init", "--bare", bareDir)
	gitCmdTest(t, root, "clone", bareDir, seedDir)
	gitCmdTest(t, seedDir, "config", "user.email", "test@example.com")
	gitCmdTest(t, seedDir, "config", "user.name", "test")
	gitCmdTest(t, seedDir, "checkout", "-b", "main")
	if err := os.WriteFile(filepath.Join(seedDir, "app.txt"), []byte("prod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmdTest(t, seedDir, "add", ".")
	gitCmdTest(t, seedDir, "commit", "-m", "main")
	gitCmdTest(t, seedDir, "push", "origin", "main")
	gitCmdTest(t, seedDir, "checkout", "-b", "develop")
	if err := os.WriteFile(filepath.Join(seedDir, "app.txt"), []byte("dev\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmdTest(t, seedDir, "add", ".")
	gitCmdTest(t, seedDir, "commit", "-m", "develop")
	gitCmdTest(t, seedDir, "push", "origin", "develop")
	gitCmdTest(t, bareDir, "symbolic-ref", "HEAD", "refs/heads/develop")
	gitCmdTest(t, root, "clone", "--depth", "1", "--single-branch", "--branch", "develop", bareDir, workDir)

	if err := exec.CommandContext(context.Background(), "git", "-C", workDir, "rev-parse", "--verify", "refs/remotes/origin/main").Run(); err == nil {
		t.Fatal("fixture still has origin/main; the old clone args should not")
	}

	mgr := NewManager("unused-root", []config.Repo{
		{Name: "svc", GitHub: "org/svc", Dir: workDir, Branch: "develop", ProdBranch: "main"},
	}, nil)
	if err := mgr.EnsureCloned(context.Background(), "svc"); err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}
	mainSHA := strings.TrimSpace(gitCmdTest(t, workDir, "rev-parse", "refs/remotes/origin/main"))
	wantMain := strings.TrimSpace(gitCmdTest(t, bareDir, "rev-parse", "main"))
	if mainSHA != wantMain {
		t.Errorf("origin/main = %s, want %s", mainSHA, wantMain)
	}
	shallow := strings.TrimSpace(gitCmdTest(t, workDir, "rev-parse", "--is-shallow-repository"))
	if shallow == "true" {
		t.Error("clone still shallow after EnsureCloned")
	}
}

// TestFetchOriginHeadsSkipsMissingProd is the default ProdBranch=main
// case on a repo that only has develop: fetch must still update
// origin/develop instead of failing the whole call.
func TestFetchOriginHeadsSkipsMissingProd(t *testing.T) {
	root := t.TempDir()
	bareDir := filepath.Join(root, "origin.git")
	seedDir := filepath.Join(root, "seed")
	workDir := filepath.Join(root, "work")

	gitCmdTest(t, root, "init", "--bare", bareDir)
	gitCmdTest(t, root, "clone", bareDir, seedDir)
	gitCmdTest(t, seedDir, "config", "user.email", "test@example.com")
	gitCmdTest(t, seedDir, "config", "user.name", "test")
	gitCmdTest(t, seedDir, "checkout", "-b", "develop")
	if err := os.WriteFile(filepath.Join(seedDir, "app.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmdTest(t, seedDir, "add", ".")
	gitCmdTest(t, seedDir, "commit", "-m", "v1")
	gitCmdTest(t, seedDir, "push", "origin", "develop")
	gitCmdTest(t, bareDir, "symbolic-ref", "HEAD", "refs/heads/develop")
	gitCmdTest(t, root, "clone", "--branch", "develop", bareDir, workDir)

	if err := os.WriteFile(filepath.Join(seedDir, "app.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmdTest(t, seedDir, "add", ".")
	gitCmdTest(t, seedDir, "commit", "-m", "v2")
	gitCmdTest(t, seedDir, "push", "origin", "develop")

	if err := FetchOriginHeads(context.Background(), workDir, nil, "develop", "main"); err != nil {
		t.Fatalf("FetchOriginHeads: %v", err)
	}
	got := strings.TrimSpace(gitCmdTest(t, workDir, "rev-parse", "origin/develop"))
	want := strings.TrimSpace(gitCmdTest(t, bareDir, "rev-parse", "develop"))
	if got != want {
		t.Errorf("origin/develop = %s, want %s", got, want)
	}
}
