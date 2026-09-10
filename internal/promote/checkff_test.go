package promote

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// twoBranches builds a bare origin with develop and main, and a clone.
// divergeMain adds a commit to main that develop does not have.
func twoBranches(t *testing.T, divergeMain bool) (bare, clone string) {
	t.Helper()
	root := t.TempDir()
	bare = filepath.Join(root, "bare.git")
	work := filepath.Join(root, "work")
	git(t, root, "init", "-q", "--bare", bare)
	git(t, root, "init", "-q", work)
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", ".")
	git(t, work, "commit", "-qm", "base")
	git(t, work, "branch", "-M", "main")
	git(t, work, "checkout", "-qb", "develop")
	if err := os.WriteFile(filepath.Join(work, "b.txt"), []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", ".")
	git(t, work, "commit", "-qm", "dev work")
	if divergeMain {
		git(t, work, "checkout", "-q", "main")
		if err := os.WriteFile(filepath.Join(work, "docs.txt"), []byte("d"), 0o600); err != nil {
			t.Fatal(err)
		}
		git(t, work, "add", ".")
		git(t, work, "commit", "-qm", "docs on main")
		git(t, work, "checkout", "-q", "develop")
	}
	git(t, work, "remote", "add", "origin", bare)
	git(t, work, "push", "-q", "origin", "develop", "main")
	clone = filepath.Join(root, "clone")
	git(t, root, "clone", "-q", "--branch", "develop", bare, clone)
	return bare, clone
}

func TestCheckFastForwardAcceptsAncestor(t *testing.T) {
	_, clone := twoBranches(t, false)
	if err := CheckFastForward(context.Background(), clone, nil, "develop", "main"); err != nil {
		t.Fatalf("main is an ancestor of develop; got %v", err)
	}
}

// main holds a commit develop does not: the promote must be refused
// before anything is written, naming both tips and what to do.
func TestCheckFastForwardRefusesDivergedProd(t *testing.T) {
	_, clone := twoBranches(t, true)
	err := CheckFastForward(context.Background(), clone, nil, "develop", "main")
	if !errors.Is(err, ErrNotFastForward) {
		t.Fatalf("want ErrNotFastForward, got %v", err)
	}
	if !strings.Contains(err.Error(), "merge main into develop") {
		t.Fatalf("error should say how to reconcile: %v", err)
	}
	// And nothing was written to the clone's working tree or pushed.
	if out := git(t, clone, "status", "--porcelain"); out != "" {
		t.Fatalf("clone dirtied: %q", out)
	}
}

func TestRemoteTipReadsOrigin(t *testing.T) {
	bare, clone := twoBranches(t, false)
	want := git(t, bare, "rev-parse", "main")
	got, err := RemoteTip(context.Background(), clone, nil, "main")
	if err != nil || got != want {
		t.Fatalf("got %q (%v) want %q", got, err, want)
	}
}
