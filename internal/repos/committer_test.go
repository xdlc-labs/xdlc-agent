package repos

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
)

// envOf turns a KEY=value slice into a map for assertions.
func envOf(entries []string) map[string]string {
	out := map[string]string{}
	for _, kv := range entries {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

// noEnv is a lookupEnv that reports every variable as unset — a daemon
// started by systemd or a container entrypoint with no git identity.
func noEnv(string) (string, bool) { return "", false }

func TestCommitterEnvDefaults(t *testing.T) {
	got := envOf(committerEnv("", "", "", "", noEnv))
	for _, key := range committerEnvKeys {
		if _, ok := got[key]; !ok {
			t.Errorf("missing %s: git commit has no identity to use", key)
		}
	}
	if got["GIT_AUTHOR_NAME"] != DefaultCommitterName ||
		got["GIT_COMMITTER_NAME"] != DefaultCommitterName {
		t.Errorf("name = %q/%q, want %q", got["GIT_AUTHOR_NAME"], got["GIT_COMMITTER_NAME"], DefaultCommitterName)
	}
	if got["GIT_AUTHOR_EMAIL"] != DefaultCommitterEmail ||
		got["GIT_COMMITTER_EMAIL"] != DefaultCommitterEmail {
		t.Errorf("email = %q/%q, want %q", got["GIT_AUTHOR_EMAIL"], got["GIT_COMMITTER_EMAIL"], DefaultCommitterEmail)
	}
}

func TestCommitterEnvPrecedence(t *testing.T) {
	// config.yaml's agent.committer beats the built-in default.
	got := envOf(committerEnv("Fleet Bot", "bot@example.test", "", "", noEnv))
	if got["GIT_AUTHOR_NAME"] != "Fleet Bot" || got["GIT_COMMITTER_EMAIL"] != "bot@example.test" {
		t.Errorf("agent.committer ignored: %v", got)
	}

	// git's own config beats config.yaml: an operator who set a GitHub
	// App bot identity in ~/.gitconfig keeps it.
	got = envOf(committerEnv("Fleet Bot", "bot@example.test", "App Bot", "app@example.test", noEnv))
	if got["GIT_AUTHOR_NAME"] != "App Bot" || got["GIT_AUTHOR_EMAIL"] != "app@example.test" {
		t.Errorf("host git config must win over agent.committer: %v", got)
	}

	// The process environment beats everything, and is re-emitted rather
	// than merely left alone — the subagent env is an allowlist that
	// would otherwise drop it before the agent's git sees it.
	env := map[string]string{"GIT_COMMITTER_NAME": "Operator", "GIT_AUTHOR_EMAIL": "op@example.test"}
	got = envOf(committerEnv("Fleet Bot", "bot@example.test", "App Bot", "app@example.test",
		func(k string) (string, bool) { v, ok := env[k]; return v, ok }))
	if got["GIT_COMMITTER_NAME"] != "Operator" {
		t.Errorf("GIT_COMMITTER_NAME = %q, want the operator's own value", got["GIT_COMMITTER_NAME"])
	}
	if got["GIT_AUTHOR_EMAIL"] != "op@example.test" {
		t.Errorf("GIT_AUTHOR_EMAIL = %q, want the operator's own value", got["GIT_AUTHOR_EMAIL"])
	}
	// Overriding one variable must not silently change the other three.
	if got["GIT_AUTHOR_NAME"] != "App Bot" || got["GIT_COMMITTER_EMAIL"] != "app@example.test" {
		t.Errorf("un-overridden fields changed: %v", got)
	}

	// An exported-but-empty value is not an identity; fall through.
	got = envOf(committerEnv("", "", "", "", func(k string) (string, bool) {
		if k == "GIT_AUTHOR_NAME" {
			return "  ", true
		}
		return "", false
	}))
	if got["GIT_AUTHOR_NAME"] != DefaultCommitterName {
		t.Errorf("blank GIT_AUTHOR_NAME = %q, want the default", got["GIT_AUTHOR_NAME"])
	}
}

// TestAuthEnvCarriesCommitterWithoutToken: AuthEnv used to return nil
// when there was no GitHub token, which would have left the coding agent
// with no identity on every unauthenticated / public-repo install. A
// missing token means "cannot reach GitHub", not "cannot commit".
func TestAuthEnvCarriesCommitterWithoutToken(t *testing.T) {
	mgr := NewManager(t.TempDir(), []config.Repo{{Name: "api"}}, nil)
	got := envOf(mgr.AuthEnv())
	for _, key := range committerEnvKeys {
		if got[key] == "" {
			t.Errorf("AuthEnv without a token dropped %s", key)
		}
	}
	if _, ok := got["GIT_CONFIG_KEY_0"]; ok {
		t.Error("no token configured, so no credential should be injected")
	}

	mgr.SetCommitter("Fleet Bot", "bot@example.test")
	got = envOf(mgr.AuthEnv())
	// The host running these tests may well have a git identity of its
	// own, which legitimately wins; assert only that something usable is
	// there in every slot.
	for _, key := range committerEnvKeys {
		if got[key] == "" {
			t.Errorf("SetCommitter left %s empty", key)
		}
	}
}

// TestCommitSucceedsWithoutGitIdentity is the release-blocker
// regression, run the only way that actually proves it: a real `git
// commit` in a repo where git can resolve no identity at all (empty
// HOME, no system config, no repo-local user.*) — the shipped
// container's exact situation. Without the env AuthEnv now supplies,
// git refuses with "Author identity unknown", the agent's Fix produces
// no commit, and the run is still recorded as clean.
func TestCommitSucceedsWithoutGitIdentity(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	run := func(env []string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
		// A HOME with no .gitconfig plus GIT_CONFIG_NOSYSTEM leaves git
		// with nowhere to find an identity, which is what the image did.
		cmd.Env = append([]string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + t.TempDir(),
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_GLOBAL=" + filepath.Join(t.TempDir(), "absent"),
		}, env...)
		return cmd.CombinedOutput()
	}
	if out, err := run(nil, "init", "-q", "-b", "develop", "."); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "fix.txt"), []byte("fixed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := run(nil, "add", "fix.txt"); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}

	// Baseline: without an identity the commit must fail, or this test
	// proves nothing about the fix.
	if out, err := run(nil, "commit", "-m", "fix"); err == nil {
		t.Skipf("this git resolved an identity with none configured, so the "+
			"regression cannot be reproduced here: %s", out)
	}

	ident := committerEnv("", "", "", "", noEnv)
	if out, err := run(ident, "commit", "-m", "fix"); err != nil {
		t.Fatalf("commit with CommitterEnv identity: %v: %s", err, out)
	}
	out, err := run(ident, "log", "-1", "--format=%an <%ae>|%cn <%ce>")
	if err != nil {
		t.Fatalf("git log: %v: %s", err, out)
	}
	want := DefaultCommitterName + " <" + DefaultCommitterEmail + ">"
	if got := strings.TrimSpace(string(out)); got != want+"|"+want {
		t.Errorf("commit identity = %q, want %q", got, want+"|"+want)
	}
}
