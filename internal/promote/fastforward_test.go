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

func TestCarryProdTag(t *testing.T) {
	dir := t.TempDir()
	dev := filepath.Join(dir, "gitops", "values", "dev")
	prod := filepath.Join(dir, "gitops", "values", "prod")
	if err := os.MkdirAll(dev, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(prod, 0o755); err != nil {
		t.Fatal(err)
	}
	devBody := "image:\n  repository: ghcr.io/org/svc\n  tag: \"sha-abc1234\"\n"
	prodBody := "image:\n  repository: ghcr.io/org/svc\n  tag: \"sha-0000000\"\n"
	if err := os.WriteFile(filepath.Join(dev, "svc.yaml"), []byte(devBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prod, "svc.yaml"), []byte(prodBody), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := CarryProdTag(dir, "svc")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected change")
	}
	got, _ := os.ReadFile(filepath.Join(prod, "svc.yaml"))
	if !strings.Contains(string(got), `tag: "sha-abc1234"`) {
		t.Fatalf("prod tag not updated: %s", got)
	}

	changed, err = CarryProdTag(dir, "svc")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("second call should be no-op")
	}
}

func TestCarryProdTagMissingGitops(t *testing.T) {
	changed, err := CarryProdTag(t.TempDir(), "svc")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("missing gitops should no-op")
	}
}

func TestFastForwardCustomBranches(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	work := filepath.Join(root, "work")
	run := func(dir string, args ...string) {
		t.Helper()
		out, err := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run(root, "init", "--bare", bare)
	run(root, "clone", bare, seed)
	run(seed, "config", "user.email", "t@e.com")
	run(seed, "config", "user.name", "t")
	run(seed, "checkout", "-b", "production")
	if err := os.WriteFile(filepath.Join(seed, "f"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(seed, "add", ".")
	run(seed, "commit", "-m", "a")
	run(seed, "push", "origin", "production")
	run(seed, "checkout", "-b", "release")
	if err := os.WriteFile(filepath.Join(seed, "f"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(seed, "add", ".")
	run(seed, "commit", "-m", "b")
	run(seed, "push", "origin", "release")
	run(root, "clone", "--branch", "release", bare, work)

	if err := FastForward(context.Background(), work, nil, "release", "production", ""); err != nil {
		t.Fatalf("FastForward: %v", err)
	}
	prod := strings.TrimSpace(string(mustOutput(t, bare, "rev-parse", "production")))
	dev := strings.TrimSpace(string(mustOutput(t, bare, "rev-parse", "release")))
	if prod != dev {
		t.Errorf("production (%s) != release (%s)", prod, dev)
	}
}

// setupDevProd builds a bare origin with main@A and develop@A+1, plus a
// working clone on develop — the fast-forwardable state promote expects.
func setupDevProd(t *testing.T) (bare, seed, work string) {
	t.Helper()
	root := t.TempDir()
	bare = filepath.Join(root, "origin.git")
	seed = filepath.Join(root, "seed")
	work = filepath.Join(root, "work")
	run := func(dir string, args ...string) {
		t.Helper()
		out, err := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run(root, "init", "--bare", bare)
	run(root, "clone", bare, seed)
	run(seed, "config", "user.email", "t@e.com")
	run(seed, "config", "user.name", "t")
	run(seed, "checkout", "-b", "main")
	if err := os.WriteFile(filepath.Join(seed, "f"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(seed, "add", ".")
	run(seed, "commit", "-m", "a")
	run(seed, "push", "origin", "main")
	run(seed, "checkout", "-b", "develop")
	if err := os.WriteFile(filepath.Join(seed, "f"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(seed, "add", ".")
	run(seed, "commit", "-m", "b")
	run(seed, "push", "origin", "develop")
	run(root, "clone", "--branch", "develop", bare, work)
	return bare, seed, work
}

func revParseT(t *testing.T, dir, rev string) string {
	t.Helper()
	return strings.TrimSpace(string(mustOutput(t, dir, "rev-parse", rev)))
}

// TestFastForwardPushesGatedSHA is the S3 regression: the promote must
// ship the commit the gate passed on, not whatever the dev branch has
// become since.
func TestFastForwardPushesGatedSHA(t *testing.T) {
	bare, _, work := setupDevProd(t)
	gated := revParseT(t, bare, "develop")

	if err := FastForward(context.Background(), work, nil, "develop", "main", gated); err != nil {
		t.Fatalf("FastForward: %v", err)
	}
	if got := revParseT(t, bare, "main"); got != gated {
		t.Errorf("origin main = %s, want the gated sha %s", got, gated)
	}
}

// TestFastForwardRefusesMovedDevBranch is the actual bug: a commit
// landing between the smoke pass and the promote used to reach prod
// untested, because the push was of the *branch*.
func TestFastForwardRefusesMovedDevBranch(t *testing.T) {
	bare, seed, work := setupDevProd(t)
	gated := revParseT(t, bare, "develop")

	// An ungated commit lands on develop after the gate passed.
	run := func(args ...string) {
		t.Helper()
		out, err := exec.CommandContext(context.Background(), "git", append([]string{"-C", seed}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(seed, "f"), []byte("ungated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "landed after the gate passed")
	run("push", "origin", "develop")
	moved := revParseT(t, bare, "develop")
	if moved == gated {
		t.Fatal("fixture did not move develop")
	}
	mainBefore := revParseT(t, bare, "main")

	err := FastForward(context.Background(), work, nil, "develop", "main", gated)
	if err == nil {
		t.Fatal("promote succeeded against a moved develop — the ungated commit reached prod")
	}
	if !errors.Is(err, ErrMoved) {
		t.Errorf("error = %v, want ErrMoved", err)
	}
	if got := revParseT(t, bare, "main"); got != mainBefore {
		t.Errorf("main moved to %s despite the failed promote", got)
	}
}

func TestFastForwardRejectsNonSHAPin(t *testing.T) {
	_, _, work := setupDevProd(t)
	// A pin that is a ref expression or a git option must never reach argv.
	for _, pin := range []string{"HEAD", "origin/develop", "--upload-pack=evil", "abc"} {
		if err := FastForward(context.Background(), work, nil, "develop", "main", pin); err == nil {
			t.Errorf("FastForward accepted pin %q", pin)
		}
	}
}

func TestVerifyRemoteTip(t *testing.T) {
	bare, seed, work := setupDevProd(t)
	gated := revParseT(t, bare, "develop")

	if err := VerifyRemoteTip(context.Background(), work, nil, "develop", gated); err != nil {
		t.Fatalf("VerifyRemoteTip on an unmoved branch: %v", err)
	}
	// Abbreviated pins compare by prefix.
	if err := VerifyRemoteTip(context.Background(), work, nil, "develop", gated[:8]); err != nil {
		t.Fatalf("VerifyRemoteTip with an abbreviated sha: %v", err)
	}
	if err := VerifyRemoteTip(context.Background(), work, nil, "develop", "0123456"); !errors.Is(err, ErrMoved) {
		t.Fatalf("error = %v, want ErrMoved", err)
	}
	if err := VerifyRemoteTip(context.Background(), work, nil, "develop", "HEAD"); err == nil {
		t.Fatal("accepted a ref expression as a pin")
	}

	run := func(args ...string) {
		t.Helper()
		out, err := exec.CommandContext(context.Background(), "git", append([]string{"-C", seed}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(seed, "f"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "c")
	run("push", "origin", "develop")

	if err := VerifyRemoteTip(context.Background(), work, nil, "develop", gated); !errors.Is(err, ErrMoved) {
		t.Fatalf("error = %v, want ErrMoved after develop moved", err)
	}
}

func mustOutput(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	out, err := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return out
}
