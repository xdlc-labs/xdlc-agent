package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
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
