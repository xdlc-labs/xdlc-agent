package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

	cfg := configFromScan(found)
	for _, want := range []string{"name: api", "github: acme/api", "gates: [ci]", "provider: claude"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("generated config missing %q:\n%s", want, cfg)
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
