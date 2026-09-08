package oneshot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/ghclient"
	"github.com/xdlc-labs/xdlc-agent/internal/subagent"
)

const runURL = "https://github.com/local/demo/actions/runs/42"

type fakeGitHub struct {
	run     ghclient.Run
	created []string // "head→base"
	title   string
	body    string
}

func (f *fakeGitHub) GetRun(context.Context, string) (ghclient.Run, error) { return f.run, nil }
func (f *fakeGitHub) FetchFailedJobLogs(context.Context, string) (string, error) {
	return "--- FAIL: TestAdd (0.00s)\n    add_test.go:6: Add(2,3)=-1 want 5\n", nil
}
func (f *fakeGitHub) FindPRByBranch(context.Context, string, string) (*ghclient.PRRef, error) {
	return nil, nil
}
func (f *fakeGitHub) CreatePR(_ context.Context, _, head, base, title, body string) (*ghclient.PRRef, error) {
	f.created = append(f.created, head+"→"+base)
	f.title, f.body = title, body
	return &ghclient.PRRef{Number: 7, URL: "https://github.com/local/demo/pull/7", State: "open"}, nil
}

// fakeRunner repairs add.go and commits, like a coding agent told not to push.
type fakeRunner struct{}

func (fakeRunner) Run(ctx context.Context, dir, _ string, extraEnv []string) (string, error) {
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte("package demo\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil { //nolint:gosec // G306: test fixture
		return "", err
	}
	for _, args := range [][]string{{"add", "add.go"}, {"commit", "-m", "fix: Add returns a+b"}} {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...) //nolint:gosec // G204: fixed args
		cmd.Env = append(os.Environ(), extraEnv...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("git %v: %w: %s", args, err, out)
		}
	}
	return fmt.Sprintf(`{"%s":"%s","summary":"Add subtracted; now adds","total_cost_usd":0.42}`, subagent.VerdictKey, subagent.OutcomeFixed), nil
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...) //nolint:gosec // G204: test
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// setup builds a bare origin whose develop has a broken Add, plus a clone.
func setup(t *testing.T) (bare, clone, headSHA string) {
	t.Helper()
	root := t.TempDir()
	bare = filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	clone = filepath.Join(root, "clone")
	git(t, root, "init", "--bare", "-b", "develop", bare)
	git(t, root, "clone", bare, seed)
	files := map[string]string{
		"go.mod":      "module example.com/demo\n\ngo 1.21\n",
		"add.go":      "package demo\n\nfunc Add(a, b int) int { return a - b }\n",
		"add_test.go": "package demo\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(Add(2, 3))\n\t}\n}\n",
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(seed, n), []byte(c), 0o644); err != nil { //nolint:gosec // G306: test fixture
			t.Fatal(err)
		}
	}
	git(t, seed, "checkout", "-b", "develop")
	git(t, seed, "add", ".")
	git(t, seed, "commit", "-m", "break Add")
	git(t, seed, "push", "origin", "develop")
	headSHA = git(t, seed, "rev-parse", "HEAD")
	git(t, root, "clone", "--branch", "develop", bare, clone)
	return bare, clone, headSHA
}

func redRun(sha string) ghclient.Run {
	return ghclient.Run{Owner: "local", Name: "demo", ID: 42, HTMLURL: runURL, Workflow: "ci",
		HeadBranch: "develop", HeadSHA: sha, Status: "completed", Conclusion: "failure"}
}

func TestRunOpensPR(t *testing.T) {
	bare, clone, sha := setup(t)
	gh := &fakeGitHub{run: redRun(sha)}
	work := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	res, err := Run(ctx, Options{
		RunURL: runURL, Provider: "claude", Out: io.Discard, WorkDir: work,
		GitHub: gh, Runner: fakeRunner{}, Tokens: ghclient.EmptyToken{}, RepoDir: clone,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.PRURL != "https://github.com/local/demo/pull/7" {
		t.Fatalf("PRURL = %q", res.PRURL)
	}
	if len(gh.created) != 1 || !strings.HasPrefix(gh.created[0], "xdlc-fix-") || !strings.HasSuffix(gh.created[0], "→develop") {
		t.Fatalf("CreatePR calls = %v, want one xdlc-fix-*→develop", gh.created)
	}
	if gh.title != "fix: Add subtracted; now adds" {
		t.Fatalf("PR title = %q", gh.title)
	}
	if !strings.Contains(gh.body, runURL) || !strings.Contains(gh.body, "Opened by [xdlc]") {
		t.Fatalf("PR body missing run url / footer:\n%s", gh.body)
	}
	if !res.Delivered || res.Outcome != "fixed" || res.Branch != strings.TrimSuffix(gh.created[0], "→develop") {
		t.Fatalf("result = %+v", res)
	}
	refs := git(t, bare, "for-each-ref", "--format=%(refname:short)")
	if !strings.Contains(refs, res.Branch) {
		t.Fatalf("PR branch %s not on origin; refs:\n%s", res.Branch, refs)
	}
	if got := git(t, bare, "rev-parse", "develop"); got != sha {
		t.Fatalf("pr mode moved develop: %s → %s", sha, got)
	}
	if res.SessionID == "" {
		t.Fatal("no session recorded")
	}
	if _, err := os.Stat(filepath.Join(res.SessionDir, res.SessionID, "meta.json")); err != nil {
		t.Fatalf("session meta: %v", err)
	}
}

func TestRunDirectPushesBranch(t *testing.T) {
	bare, clone, sha := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	res, err := Run(ctx, Options{
		RunURL: runURL, Mode: "direct", Out: io.Discard, WorkDir: t.TempDir(),
		GitHub: &fakeGitHub{run: redRun(sha)}, Runner: fakeRunner{}, Tokens: ghclient.EmptyToken{}, RepoDir: clone,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.PRURL != "" || res.Branch != "develop" {
		t.Fatalf("direct mode result = %+v", res)
	}
	if got := git(t, bare, "rev-parse", "develop"); got == sha {
		t.Fatal("direct mode did not move develop")
	}
	if !strings.Contains(git(t, bare, "show", "develop:add.go"), "a + b") {
		t.Fatal("develop tip does not carry the fix")
	}
}

func TestRunRefusesGreenRun(t *testing.T) {
	run := redRun("abc")
	run.Conclusion = "success"
	_, err := Run(context.Background(), Options{
		RunURL: runURL, Out: io.Discard, WorkDir: t.TempDir(),
		GitHub: &fakeGitHub{run: run}, Runner: fakeRunner{}, Tokens: ghclient.EmptyToken{},
	})
	if !errors.Is(err, ErrNotRed) {
		t.Fatalf("err = %v, want ErrNotRed", err)
	}
}

func TestRunRejectsBadInput(t *testing.T) {
	cases := map[string]Options{
		"url":      {RunURL: "https://github.com/x/y/pull/1"},
		"provider": {RunURL: runURL, Provider: "copilot"},
		"mode":     {RunURL: runURL, Mode: "yolo"},
	}
	for name, o := range cases {
		o.Out = io.Discard
		o.Tokens = ghclient.EmptyToken{}
		o.GitHub = &fakeGitHub{}
		if _, err := Run(context.Background(), o); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
