package repos

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DefaultKeepFailed is how long a Fix worktree survives after a failed
// run so an operator can look at the half-finished work.
const DefaultKeepFailed = 24 * time.Hour

// worktreeBranchPrefix namespaces the branches Fix worktrees are created
// on, so `git branch --list "xdlc/*"` in a base clone shows exactly what
// this daemon made and nothing a human did.
const worktreeBranchPrefix = "xdlc/"

// worktreesDirName is the subdirectory of the manager root that holds
// every Fix worktree. It sits beside the clones rather than inside one:
// a worktree nested in a clone's own working tree shows up as untracked
// content, which would make EnsureCloned see the clone as dirty and
// hard-reset it on every pass.
const worktreesDirName = ".worktrees"

// unsafeWorktreeID rejects anything that could escape the worktree root
// or confuse git's ref parser. Session ids are already
// timestamp-plus-slug, so this only ever fires on a caller bug.
var unsafeWorktreeID = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// Worktree is one isolated checkout for a single Fix run.
//
// Fixes used to run in the repo's one shared clone, which forced two
// compromises: only one Fix per repo could run at a time, and a Fix that
// died mid-edit left a dirty tree that the next operation silently
// hard-reset. A worktree gives each run its own directory and its own
// branch off origin/<tracked>, so concurrent Fixes on one repo cannot
// see each other's edits and a crashed run leaves the shared clone
// untouched.
type Worktree struct {
	// Dir is the checkout the coding agent runs in.
	Dir string
	// Branch is the local branch created for this run (xdlc/<id>). It is
	// deliberately created with no upstream: the orchestrator pushes it
	// explicitly to the tracked branch (or a PR branch), so a stray
	// `git push` inside the agent cannot land somewhere unintended.
	Branch string
	// Base is the shared clone this worktree was added to; every
	// worktree bookkeeping command has to run there.
	Base string
	// Target is the remote branch a successful Fix pushes to.
	Target string
	// repo is the config short name, for the per-repo clone lock.
	repo string
}

// WorktreeRoot is where this manager keeps Fix worktrees.
func (m *Manager) WorktreeRoot() string {
	return filepath.Join(m.root, worktreesDirName)
}

// worktreeDir is the path for one run's checkout.
func (m *Manager) worktreeDir(repo, id string) string {
	return filepath.Join(m.WorktreeRoot(), safeSegment(repo), safeSegment(id))
}

func safeSegment(s string) string {
	out := strings.Trim(unsafeWorktreeID.ReplaceAllString(s, "-"), "-.")
	if out == "" {
		out = "x"
	}
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

// Worktree creates an isolated checkout of repo's tracked branch for the
// run named id (the Fix session id, so a worktree and its recording
// share one name).
//
// The caller must have run EnsureCloned first: the worktree branches off
// the base clone's origin/<branch> ref, so the base clone decides how
// current the starting point is.
func (m *Manager) Worktree(ctx context.Context, repo, id string) (*Worktree, error) {
	if _, ok := m.repos[repo]; !ok {
		return nil, fmt.Errorf("repos: unknown repo %q", repo)
	}
	base := m.Dir(repo)
	target := m.Branch(repo)
	dir := m.worktreeDir(repo, id)
	branch := worktreeBranchPrefix + safeSegment(id)

	if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
		return nil, fmt.Errorf("repos: mkdir %s: %w", filepath.Dir(dir), err)
	}

	// `worktree add` writes the shared clone's .git, so it takes the same
	// lock EnsureCloned does — concurrent Fixes for one repo must not run
	// two of these at once.
	unlock := m.lockRepo(repo)
	// A leftover from a killed run with the same id would make
	// `worktree add` fail; clear it (and any stale registration) first.
	_ = m.removeWorktreeAt(ctx, base, dir, branch)

	env := m.AuthEnv()
	// --no-track: the branch must have no upstream, so the only way code
	// leaves this worktree is the explicit push below.
	err := runGit(ctx, base, env, "worktree", "add", "--no-track",
		"-b", branch, dir, "origin/"+target)
	if err == nil {
		// Claimed before the lock drops, so a sweep running in another
		// Fix cannot observe this directory as finished-and-unowned in
		// the gap between creating it and marking it live.
		m.markLive(dir, true)
	}
	unlock()
	if err != nil {
		return nil, fmt.Errorf("repos: worktree add %s: %w", repo, err)
	}
	return &Worktree{Dir: dir, Branch: branch, Base: base, Target: target, repo: repo}, nil
}

// markLive records (or clears) a worktree as belonging to a running Fix.
func (m *Manager) markLive(dir string, live bool) {
	m.liveMu.Lock()
	defer m.liveMu.Unlock()
	if m.live == nil {
		m.live = map[string]struct{}{}
	}
	if live {
		m.live[dir] = struct{}{}
		return
	}
	delete(m.live, dir)
}

func (m *Manager) isLive(dir string) bool {
	m.liveMu.Lock()
	defer m.liveMu.Unlock()
	_, ok := m.live[dir]
	return ok
}

// Done marks a worktree's Fix as finished without deleting it — the
// keep-on-failure path. Until this is called the sweep will not touch it,
// however old its timestamp looks.
func (m *Manager) Done(w *Worktree) {
	if w == nil {
		return
	}
	m.markLive(w.Dir, false)
}

// Push sends the worktree's branch to remoteBranch on origin.
//
// This is a plain (non-force) push, so it is refused when the target has
// moved on since the worktree was created. That refusal is the point: a
// Fix that would have to overwrite someone else's commit to land should
// fail loudly and be re-run against the new tip, not win the race.
func (m *Manager) Push(ctx context.Context, w *Worktree, remoteBranch string) error {
	if w == nil {
		return fmt.Errorf("repos: push: nil worktree")
	}
	if remoteBranch == "" {
		remoteBranch = w.Target
	}
	refspec := w.Branch + ":" + remoteBranch
	if err := runGit(ctx, w.Dir, m.AuthEnv(), "push", "origin", refspec); err != nil {
		return fmt.Errorf("repos: push %s: %w", refspec, err)
	}
	return nil
}

// HasCommits reports whether the worktree's branch has moved past the
// commit it was created at — i.e. whether the agent committed anything.
// Pushing an unchanged branch is a no-op that still costs a round trip
// and, in pr mode, would open an empty PR.
func (m *Manager) HasCommits(ctx context.Context, w *Worktree) bool {
	if w == nil {
		return false
	}
	head, err := gitOutput(ctx, w.Dir, "rev-parse", "HEAD")
	if err != nil {
		return false
	}
	base, err := gitOutput(ctx, w.Dir, "rev-parse", "origin/"+w.Target)
	if err != nil {
		// Without a comparison point, assume there is something to push
		// rather than silently dropping the agent's work.
		return true
	}
	return head != base
}

// Remove deletes the worktree and its branch. Safe to call twice.
func (m *Manager) Remove(ctx context.Context, w *Worktree) error {
	if w == nil {
		return nil
	}
	m.markLive(w.Dir, false)
	if w.repo != "" {
		defer m.lockRepo(w.repo)()
	}
	return m.removeWorktreeAt(ctx, w.Base, w.Dir, w.Branch)
}

// removeWorktreeAt tears down one worktree directory and its branch,
// best-effort: each step is expected to fail when the previous run
// already cleaned up, and the directory itself is removed last so a
// worktree git has forgotten about still leaves no litter on disk.
func (m *Manager) removeWorktreeAt(ctx context.Context, base, dir, branch string) error {
	env := m.AuthEnv()
	_ = runGit(ctx, base, env, "worktree", "remove", "--force", dir)
	_ = runGit(ctx, base, env, "worktree", "prune")
	if branch != "" {
		_ = runGit(ctx, base, env, "branch", "-D", branch)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("repos: remove worktree %s: %w", dir, err)
	}
	return nil
}

// PruneWorktrees deletes this repo's Fix worktrees last modified more
// than olderThan ago — the ones kept behind by failed runs, past the
// window an operator would still want them in. olderThan <= 0 uses
// DefaultKeepFailed. Returns how many were removed.
//
// Best-effort: a worktree that cannot be removed is logged by the caller
// and skipped, never fatal to the Fix that triggered the sweep.
func (m *Manager) PruneWorktrees(ctx context.Context, repo string, olderThan time.Duration) (int, error) {
	if olderThan <= 0 {
		olderThan = DefaultKeepFailed
	}
	dir := filepath.Join(m.WorktreeRoot(), safeSegment(repo))
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("repos: prune worktrees %s: %w", repo, err)
	}
	base := m.Dir(repo)
	cutoff := time.Now().Add(-olderThan)
	removed := 0
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		path := filepath.Join(dir, ent.Name())
		// A Fix still running owns its worktree no matter how old the
		// directory looks: the timestamp is set when the worktree is
		// created, so a run lasting longer than keep_failed would
		// otherwise have the ground removed from under it mid-edit.
		if m.isLive(path) {
			continue
		}
		info, err := ent.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		unlock := m.lockRepo(repo)
		err = m.removeWorktreeAt(ctx, base, path, worktreeBranchPrefix+ent.Name())
		unlock()
		if err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
