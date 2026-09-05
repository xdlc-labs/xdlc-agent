// Package repos manages local clones of the repos listed in config.yaml
// — the working directories subagents edit in, and promote/revert push
// from.
package repos

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
	"github.com/xdlc-labs/xdlc-agent/internal/ghclient"
)

// Branch defaults used when a config.Repo leaves branch/prod_branch
// unset. Exported so the webhook filter, the CI gate and the CLI resolve
// the same trunk this package clones and pushes, instead of each
// re-spelling "develop".
//
// ponytail: config.Load does not apply these defaults to config.Repo
// (internal/config is owned elsewhere and gets a defaulting pass later);
// until it does, Manager.Branch is the one resolver and everything else
// asks it.
const (
	DefaultBranch     = "develop"
	DefaultProdBranch = "main"
)

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

// Manager resolves a repo name to a local clone path, cloning it on
// first use if the directory doesn't exist yet.
type Manager struct {
	root   string // base dir clones live under when Repo.Dir is unset
	repos  map[string]config.Repo
	tokens ghclient.TokenProvider // App or PAT; refreshed per git op when App
}

// NewManager returns a Manager for cfgRepos, cloning under root by
// default (a Repo.Dir override takes precedence) and authenticating git
// operations via tokens (see AuthEnv; nil/empty for public-repo,
// read-only use).
func NewManager(root string, cfgRepos []config.Repo, tokens ghclient.TokenProvider) *Manager {
	if tokens == nil {
		tokens = ghclient.EmptyToken{}
	}
	m := &Manager{root: root, tokens: tokens, repos: make(map[string]config.Repo, len(cfgRepos))}
	for _, r := range cfgRepos {
		m.repos[r.Name] = r
	}
	return m
}

// Resolve maps a GitHub full name (org/repo) or config short name to
// the config short name. Webhooks emit org/repo; pollers use short name.
func (m *Manager) Resolve(name string) (string, bool) {
	if _, ok := m.repos[name]; ok {
		return name, true
	}
	for short, r := range m.repos {
		if r.GitHub == name {
			return short, true
		}
	}
	return "", false
}

// Dir returns the local clone path for repo, without touching disk.
// Used as the RepoDir func passed to dispatch.Dispatcher.
func (m *Manager) Dir(repo string) string {
	r, ok := m.repos[repo]
	if !ok {
		return filepath.Join(m.root, repo)
	}
	if r.Dir != "" {
		return r.Dir
	}
	return filepath.Join(m.root, repo)
}

// Branch returns the dev branch subagents/promote operate on for repo,
// defaulting to DefaultBranch. This is also the branch CI signals are
// accepted for (webhook.Server.BranchFor) — one resolver, so a repo
// configured with `branch: main` is watched on main everywhere.
func (m *Manager) Branch(repo string) string {
	if r, ok := m.repos[repo]; ok && r.Branch != "" {
		return r.Branch
	}
	return DefaultBranch
}

// BranchByGitHub is Branch keyed by the "owner/name" GitHub identifier
// instead of the config short name — what the CI gate has on hand,
// since gate.CIGate.Check is called with the GitHub full name.
// Unknown repos get DefaultBranch.
func (m *Manager) BranchByGitHub(ownerRepo string) string {
	for _, r := range m.repos {
		if r.GitHub == ownerRepo && r.Branch != "" {
			return r.Branch
		}
	}
	return DefaultBranch
}

// ProdBranch returns the production branch promote/revert target for
// repo, defaulting to DefaultProdBranch.
func (m *Manager) ProdBranch(repo string) string {
	if r, ok := m.repos[repo]; ok && r.ProdBranch != "" {
		return r.ProdBranch
	}
	return DefaultProdBranch
}

// RemoteSHA returns the commit origin's copy of repo's dev branch points
// at, via `git ls-remote` — the authoritative answer, not the local
// clone's possibly-stale remote-tracking ref.
//
// It exists so a gate result can be pinned to the artifact it applies
// to: the dev-smoke poller and the ArgoCD webhook read this *before*
// running the probe, and the resulting orchestrator.Signal.SHA is what
// Promote later has to still find at the branch tip.
func (m *Manager) RemoteSHA(ctx context.Context, repo string) (string, error) {
	r, ok := m.repos[repo]
	if !ok {
		return "", fmt.Errorf("repos: unknown repo %q", repo)
	}
	branch := m.Branch(repo)
	url := fmt.Sprintf("https://github.com/%s.git", r.GitHub)
	dir := m.Dir(repo)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		// Use the existing clone's remote when there is one, so a
		// file:// origin (tests, mirrors) resolves the same way.
		url = "origin"
	} else {
		dir = ""
	}

	// gosec G204: branch and url come from config.yaml via this
	// manager, never from a webhook payload.
	args := []string{"ls-remote", "--exit-code", url, "refs/heads/" + branch}
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec
	if env := m.AuthEnv(); len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("repos: ls-remote %s %s: %w", repo, branch, err)
	}
	sha, _, ok := strings.Cut(strings.TrimSpace(string(out)), "\t")
	if !ok || sha == "" {
		return "", fmt.Errorf("repos: ls-remote %s %s: no ref returned", repo, branch)
	}
	return sha, nil
}

// GitHub returns the "owner/name" GitHub identifier configured for
// repo, or "" if repo is unknown.
func (m *Manager) GitHub(repo string) string {
	return m.repos[repo].GitHub
}

// AgentInstructions returns repos[].agent_instructions for repo.
func (m *Manager) AgentInstructions(repo string) string {
	return m.repos[repo].AgentInstructions
}

// AuthEnv returns the extra environment variables that authenticate git
// against GitHub, for any git command that touches the network (clone,
// fetch, push). See AuthEnv (package-level) for how the credential is
// carried — never in argv or the remote URL, so it can't leak into `ps`,
// `git remote -v`, or a git error message. Refreshes App installation
// tokens when needed.
func (m *Manager) AuthEnv() []string {
	tok, err := m.tokens.Token()
	if err != nil || tok == "" {
		return nil
	}
	return AuthEnv(tok)
}

// AuthEnv builds git config-via-environment variables that inject an
// HTTP Authorization header (git's http.extraHeader), instead of the
// more common "https://token@github.com/..." URL embedding. That
// approach persists the token into .git/config on disk and echoes it
// back in git's own error messages ("repository 'https://<token>@...'
// not found") — this doesn't: the header value only ever exists in this
// process's environment, for the one command it's set on.
func AuthEnv(token string) []string {
	if token == "" {
		return nil
	}
	auth := "AUTHORIZATION: basic " + basicAuth("x-access-token", token)
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.extraHeader",
		"GIT_CONFIG_VALUE_0=" + auth,
	}
}

// EnsureCloned makes dir a clean checkout of repo's branch: clones it if
// the directory doesn't exist, or fetches + hard-resets it to
// origin/<branch> if it does. When HEAD already matches origin/<branch>
// and the working tree is clean, the network fetch is skipped (issue #17).
// The hard reset still runs when dirty or diverged — a plain `git fetch`
// alone would leave the working tree on a stale commit.
func (m *Manager) EnsureCloned(ctx context.Context, repo string) error {
	r, ok := m.repos[repo]
	if !ok {
		return fmt.Errorf("repos: unknown repo %q", repo)
	}
	dir := m.Dir(repo)
	branch := m.Branch(repo)
	env := m.AuthEnv()

	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		if synced, _ := localMatchesOrigin(ctx, dir, branch); synced {
			return nil
		}
		if err := runGit(ctx, dir, env, "fetch", "origin", branch); err != nil {
			return err
		}
		if err := runGit(ctx, dir, env, "checkout", branch); err != nil {
			return err
		}
		return runGit(ctx, dir, env, "reset", "--hard", "origin/"+branch)
	}

	if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
		return fmt.Errorf("repos: mkdir %s: %w", dir, err)
	}
	url := fmt.Sprintf("https://github.com/%s.git", r.GitHub)
	return runGit(ctx, "", env, "clone", "--depth", "1", "--single-branch", "--branch", branch, url, dir)
}

// localMatchesOrigin is true when HEAD is on branch, equals
// origin/<branch>, and the working tree is clean — no network needed.
func localMatchesOrigin(ctx context.Context, dir, branch string) (bool, error) {
	cur, err := gitOutput(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || cur != branch {
		return false, err
	}
	head, err := gitOutput(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return false, err
	}
	origin, err := gitOutput(ctx, dir, "rev-parse", "origin/"+branch)
	if err != nil || head != origin {
		return false, err
	}
	status, err := gitOutput(ctx, dir, "status", "--porcelain")
	if err != nil || status != "" {
		return false, err
	}
	return true, nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...) //nolint:gosec // fixed git verbs
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func runGit(ctx context.Context, dir string, env []string, args ...string) error {
	cmdArgs := args
	if dir != "" {
		cmdArgs = append([]string{"-C", dir}, args...)
	}
	// gosec G204: args are always git subcommands this package
	// constructs itself (fetch/checkout/reset/clone against a
	// config.yaml-configured repo), never raw external input.
	cmd := exec.CommandContext(ctx, "git", cmdArgs...) //nolint:gosec
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("repos: git %v: %w: %s", args, err, out)
	}
	return nil
}
