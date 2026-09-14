package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
	"github.com/xdlc-labs/xdlc-agent/internal/repos"
	"github.com/xdlc-labs/xdlc-agent/internal/validate"
)

func TestParseGitHubRemote(t *testing.T) {
	cases := map[string]string{
		"git@github.com:acme/api.git":          "acme/api",
		"https://github.com/acme/api.git":      "acme/api",
		"https://github.com/acme/api":          "acme/api",
		"ssh://git@github.com/acme/api.git":    "acme/api",
		"https://gitlab.com/acme/api.git":      "",
		"git@bitbucket.org:acme/api.git":       "",
		"https://github.com/acme":              "",
		"https://github.com/acme/api/extra":    "",
		"/srv/git/local.git":                   "",
		"https://user@github.com/acme/api.git": "",
	}
	for remote, want := range cases {
		if got := parseGitHubRemote(remote); got != want {
			t.Errorf("parseGitHubRemote(%q) = %q, want %q", remote, got, want)
		}
	}
}

func TestScanReposFindsGitHubCheckouts(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()

	withRemote := filepath.Join(root, "api")
	initRepo(t, withRemote, "git@github.com:acme/api.git")

	gitlab := filepath.Join(root, "other")
	initRepo(t, gitlab, "https://gitlab.com/acme/other.git")

	noRemote := filepath.Join(root, "bare")
	initRepo(t, noRemote, "")

	found, err := scanRepos(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("want only the GitHub checkout, got %d: %+v", len(found), found)
	}
	if found[0].Name != "api" || found[0].GitHub != "acme/api" {
		t.Fatalf("unexpected repo: %+v", found[0])
	}
	if !filepath.IsAbs(found[0].Dir) {
		t.Fatalf("dir must be absolute for config.yaml: %q", found[0].Dir)
	}

	cfg := configFromScan(found, "ci")
	for _, want := range []string{"name: api", "github: acme/api", "gates: [ci]", "provider: claude"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("generated config missing %q:\n%s", want, cfg)
		}
	}
	if strings.Contains(cfg, "dev-smoke") {
		t.Errorf("ci profile must not enable dev-smoke:\n%s", cfg)
	}
}

func TestParseInitProfile(t *testing.T) {
	cases := map[string]string{
		"":       "ci",
		"ci":     "ci",
		"CI":     "ci",
		"gitops": "gitops",
		"full":   "full",
	}
	for in, want := range cases {
		got, err := parseInitProfile(in)
		if err != nil {
			t.Errorf("parseInitProfile(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseInitProfile(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := parseInitProfile("paved"); err == nil {
		t.Fatal("expected error for unknown profile")
	}
}

func TestStarterYAMLProfiles(t *testing.T) {
	ci := starterYAML("ci")
	if !strings.Contains(ci, "gates: [ci]") || strings.Contains(ci, "dev-smoke") {
		t.Fatalf("ci starter must be CI-only:\n%s", ci)
	}
	gitops := starterYAML("gitops")
	if !strings.Contains(gitops, "gates: [ci, dev-smoke]") || strings.Contains(gitops, "prod-health") {
		t.Fatalf("gitops starter must omit prod-health:\n%s", gitops)
	}
	full := starterYAML("full")
	if !strings.Contains(full, "prod-health") || !strings.Contains(full, "argocd_app") {
		t.Fatalf("full starter missing paved-road keys:\n%s", full)
	}
}

func TestConfigFromScanGitops(t *testing.T) {
	found := []scannedRepo{{Name: "api", GitHub: "acme/api", Dir: "/src/api"}}
	cfg := configFromScan(found, "gitops")
	for _, want := range []string{"gates: [ci, dev-smoke]", "argocd_app: dev-api", "ARGOCD_WEBHOOK_SECRET"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("gitops scan config missing %q:\n%s", want, cfg)
		}
	}
	if strings.Contains(cfg, "prod-health") {
		t.Errorf("gitops scan must not enable prod-health:\n%s", cfg)
	}
}

func TestStarterYAMLValidates(t *testing.T) {
	for _, profile := range []string{"ci", "gitops", "full"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(starterYAML(profile)), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(path)
		if err != nil {
			t.Errorf("profile %s load: %v", profile, err)
			continue
		}
		if issues := validate.Config(cfg); len(issues) > 0 {
			t.Errorf("profile %s validate: %v", profile, issues)
		}
	}
}

func initRepo(t *testing.T, dir, remote string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	if out, err := exec.CommandContext(context.Background(), "git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if remote != "" {
		run("remote", "add", "origin", remote)
	}
}

// Every scaffolded config must be one the daemon will actually start on:
// the profiles pair addr with require_webhook_secret: false, so addr has to
// be loopback or enforceWebhookSecrets rejects it on `xdlc daemon`.
func TestScaffoldedConfigsAreStartable(t *testing.T) {
	found := []scannedRepo{{Name: "api", GitHub: "acme/api", Dir: "/src/api"}}
	for _, profile := range []string{"ci", "gitops", "full"} {
		bodies := map[string]string{
			"starter": starterYAML(profile),
			"scan":    configFromScan(found, profile),
		}
		for kind, body := range bodies {
			name := profile + "/" + kind
			if !strings.Contains(body, `addr: "127.0.0.1:8080"`) {
				t.Errorf("%s: want loopback addr in scaffolded config:\n%s", name, body)
			}
			if strings.Contains(body, `addr: ":8080"`) {
				t.Errorf("%s: scaffolded addr must not be all-interfaces:\n%s", name, body)
			}
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load(path)
			if err != nil {
				t.Errorf("%s load: %v", name, err)
				continue
			}
			if err := enforceWebhookSecrets(cfg); err != nil {
				t.Errorf("%s: daemon would refuse to start: %v", name, err)
			}
		}
	}
}

// TestScaffoldedConfigsSetBranchExplicitly is the release-blocker
// regression: `xdlc init` used to write a repos[] entry with no
// `branch:` key at all, so the "develop" default applied silently. A
// repo whose trunk is "main" then had every workflow_run delivery
// dropped while GitHub reported 204 success. The key must be present
// (and commented) in every scaffold so the mismatch is visible before
// it costs anyone a day.
func TestScaffoldedConfigsSetBranchExplicitly(t *testing.T) {
	found := []scannedRepo{{Name: "api", GitHub: "acme/api", Dir: "/src/api", Branch: "main"}}
	for _, profile := range []string{"ci", "gitops", "full"} {
		bodies := map[string]string{
			"starter": starterYAML(profile),
			"scan":    configFromScan(found, profile),
		}
		for kind, body := range bodies {
			name := profile + "/" + kind
			if !strings.Contains(body, "branch:") {
				t.Errorf("%s: repos[] entry has no explicit branch key:\n%s", name, body)
				continue
			}
			if !strings.Contains(body, branchNote) {
				t.Errorf("%s: branch key is not annotated with why it matters:\n%s", name, body)
			}
			// The key has to land inside the repos[] entry, not somewhere
			// under gates: or agent:.
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load(path)
			if err != nil {
				t.Errorf("%s load: %v", name, err)
				continue
			}
			if len(cfg.Repos) == 0 || cfg.Repos[0].Branch == "" {
				t.Errorf("%s: parsed repos[0].branch is empty:\n%s", name, body)
			}
		}
	}

	// A scan of a checkout on "main" must write main, not the default.
	scan := configFromScan(found, "ci")
	if !strings.Contains(scan, "branch: main") {
		t.Errorf("--scan must carry the checkout's own branch:\n%s", scan)
	}
	// And a checkout whose branch could not be determined still gets an
	// explicit key rather than none.
	blank := configFromScan([]scannedRepo{{Name: "api", GitHub: "acme/api", Dir: "/src/api"}}, "ci")
	if !strings.Contains(blank, "branch: "+repos.DefaultBranch) {
		t.Errorf("--scan must fall back to an explicit default branch:\n%s", blank)
	}
}

// TestDefaultBranchOfPrefersRemoteDefault: the branch written into the
// scaffold should be the branch CI runs on, which is the remote's
// default — not whatever the operator happens to have checked out.
func TestDefaultBranchOfPrefersRemoteDefault(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	ctx := context.Background()

	// No origin/HEAD and no commits: falls back to the initial branch.
	plain := filepath.Join(t.TempDir(), "plain")
	if out, err := exec.CommandContext(ctx, "git", "init", "-q", "-b", "trunk", plain).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if got := defaultBranchOf(ctx, plain); got != "trunk" {
		t.Errorf("defaultBranchOf(no origin/HEAD) = %q, want the checked-out branch", got)
	}

	// With origin/HEAD recorded (what `git clone` leaves behind), that
	// wins over the currently checked-out branch.
	run := func(dir string, args ...string) {
		t.Helper()
		if out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run(plain, "remote", "add", "origin", "https://github.com/acme/api.git")
	run(plain, "update-ref", "refs/remotes/origin/main", emptyCommit(t, plain))
	run(plain, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	run(plain, "checkout", "-q", "-b", "feature/local")
	if got := defaultBranchOf(ctx, plain); got != "main" {
		t.Errorf("defaultBranchOf = %q, want the remote default \"main\"", got)
	}

	// Not a git repo at all: the daemon default, never "".
	if got := defaultBranchOf(ctx, t.TempDir()); got != repos.DefaultBranch {
		t.Errorf("defaultBranchOf(non-repo) = %q, want %q", got, repos.DefaultBranch)
	}
}

// emptyCommit creates one commit in dir and returns its SHA, supplying
// the identity so it works on a machine with no git config of its own.
func emptyCommit(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", "-C", dir,
		"commit", "-q", "--allow-empty", "-m", "init")
	cmd.Env = append(os.Environ(), repos.CommitterEnv("", "")...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
	sha, err := repos.GitOutput(context.Background(), dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

// TestGitopsScaffoldsAreHonest: the promote-capable profiles used to
// scaffold argocd_app and probe_job and nothing else — no
// gitops/values/** and no prod_branch. An operator who opted into
// gitops therefore landed on the default prod branch and on a promote
// that carried no image tag, with the config file implying everything
// was configured. Both facts have to be on the page.
func TestGitopsScaffoldsAreHonest(t *testing.T) {
	found := []scannedRepo{{Name: "api", GitHub: "acme/api", Dir: "/src/api"}}
	for _, profile := range []string{"gitops", "full"} {
		bodies := map[string]string{
			"starter": starterYAML(profile),
			"scan":    configFromScan(found, profile),
		}
		for kind, body := range bodies {
			name := profile + "/" + kind
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load(path)
			if err != nil {
				t.Errorf("%s load: %v", name, err)
				continue
			}
			if len(cfg.Repos) == 0 || cfg.Repos[0].ProdBranch == "" {
				t.Errorf("%s: parsed repos[0].prod_branch is empty; promote would use the silent default:\n%s", name, body)
			}
			if !strings.Contains(body, prodBranchNote) {
				t.Errorf("%s: prod_branch is not annotated with what it does:\n%s", name, body)
			}
			for _, want := range []string{"gitops/values/dev/", "gitops/values/prod/", "image.tag", "repos[].name"} {
				if !strings.Contains(body, want) {
					t.Errorf("%s: scaffold never mentions %q, so the operator does not know to create it:\n%s", name, want, body)
				}
			}
		}
	}
	// The CI profile does not promote, so it must not carry the note.
	if strings.Contains(starterYAML("ci"), "gitops/values/") {
		t.Error("ci profile should not talk about gitops values files")
	}
}
