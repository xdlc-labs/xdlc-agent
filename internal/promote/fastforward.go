// Package promote implements the dev -> prod promotion step. It is
// deliberately fast-forward-only: the artifact built and gated on the
// dev branch is the exact artifact that reaches prod, no rebuild.
package promote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/xdlc-labs/xdlc-agent/internal/repos"
)

// CommitProdTag stages and commits the prod values file if dirty. It
// does not push: dispatch pushes the carry SHA to prod first, then to
// the dev branch, so a failed prod push cannot leave develop carrying a
// tag for a release that never landed. Returns the local HEAD SHA, or
// "" when there was nothing to commit (see dispatch.Dispatcher.Promote).
func CommitProdTag(ctx context.Context, repoDir, service string, env []string) (string, error) {
	rel := valuesRel("prod", service)
	add := exec.CommandContext(ctx, "git", "-C", repoDir, "add", rel) //nolint:gosec
	applyEnv(add, env)
	if out, err := add.CombinedOutput(); err != nil {
		return "", fmt.Errorf("promote: git add: %w: %s", err, out)
	}
	status := exec.CommandContext(ctx, "git", "-C", repoDir, "diff", "--cached", "--quiet") //nolint:gosec
	applyEnv(status, env)
	if err := status.Run(); err == nil {
		return "", nil // nothing staged
	}
	msg := fmt.Sprintf("promote(%s): carry image tag to prod values", service)
	commit := exec.CommandContext(ctx, "git", "-C", repoDir, "commit", "-m", msg) //nolint:gosec
	applyEnv(commit, env)
	if out, err := commit.CombinedOutput(); err != nil {
		return "", fmt.Errorf("promote: git commit: %w: %s", err, out)
	}
	sha, err := revParse(ctx, repoDir, env, "HEAD")
	if err != nil {
		return "", err
	}
	return sha, nil
}

// PushSHA fast-forwards origin/branch to sha. No-op when origin/branch
// already points at sha. Used after a successful prod push so develop
// catches up with a local tag-carry commit that was not pushed first.
func PushSHA(ctx context.Context, repoDir string, env []string, sha, branch string) error {
	if sha == "" {
		return nil
	}
	if !isHexSHA(sha) {
		return fmt.Errorf("promote: %q is not a git object name", sha)
	}
	if err := repos.FetchOriginHeads(ctx, repoDir, env, branch); err != nil {
		return fmt.Errorf("promote: fetch %s: %w", branch, err)
	}
	got, err := revParse(ctx, repoDir, env, "origin/"+branch)
	if err == nil && strings.HasPrefix(got, sha) {
		return nil
	}
	refspec := sha + ":refs/heads/" + branch
	push := exec.CommandContext(ctx, "git", "-C", repoDir, "push", "origin", refspec) //nolint:gosec
	applyEnv(push, env)
	var stderr bytes.Buffer
	push.Stderr = &stderr
	if err := push.Run(); err != nil {
		return fmt.Errorf("promote: push %s to %s: %w: %s", short(sha), branch, err, stderr.String())
	}
	return nil
}

// ErrMoved is returned when the branch a gate passed on no longer points
// at the SHA that was gated — the promote is refused rather than
// silently shipping whatever landed since.
var ErrMoved = errors.New("promote: gated commit is no longer the branch tip")

// VerifyRemoteTip fetches branch and fails with ErrMoved unless
// origin/<branch> is exactly wantSHA.
//
// This is the check that makes a gate result mean something. A smoke
// pass is a statement about one commit; between that pass and this
// push, minutes can go by and any number of commits can land on the dev
// branch. Fast-forwarding the *branch* would carry all of them to prod
// with the passing commit's blessing.
func VerifyRemoteTip(ctx context.Context, repoDir string, env []string, branch, wantSHA string) error {
	if !isHexSHA(wantSHA) {
		return fmt.Errorf("promote: %q is not a git object name", wantSHA)
	}
	if err := repos.FetchOriginHeads(ctx, repoDir, env, branch); err != nil {
		return fmt.Errorf("promote: fetch %s: %w", branch, err)
	}
	return VerifyFetchedTip(ctx, repoDir, env, branch, wantSHA)
}

// VerifyFetchedTip is VerifyRemoteTip without the fetch: it judges the
// origin/<branch> the clone already has. For a caller that has just
// fetched every branch a Promote touches in one go (dispatch's Promote
// does, so the pinned path fetches twice rather than four times) and
// does not want each check to fetch again.
func VerifyFetchedTip(ctx context.Context, repoDir string, env []string, branch, wantSHA string) error {
	if !isHexSHA(wantSHA) {
		return fmt.Errorf("promote: %q is not a git object name", wantSHA)
	}
	got, err := revParse(ctx, repoDir, env, "origin/"+branch)
	if err != nil {
		return err
	}
	// Abbreviated pins (a 7-char SHA) compare by prefix; a full SHA
	// compares exactly.
	if !strings.HasPrefix(got, wantSHA) {
		return fmt.Errorf("%w: origin/%s is at %s, gated %s", ErrMoved, branch, got, wantSHA)
	}
	return nil
}

// RemoteTip fetches branch and returns the full SHA of origin/<branch>.
func RemoteTip(ctx context.Context, repoDir string, env []string, branch string) (string, error) {
	if err := repos.FetchOriginHeads(ctx, repoDir, env, branch); err != nil {
		return "", fmt.Errorf("promote: fetch %s: %w", branch, err)
	}
	return revParse(ctx, repoDir, env, "origin/"+branch)
}

// ErrNotFastForward is returned by CheckFastForward when the prod branch
// holds a commit the dev branch does not: pushing dev onto it would need
// a merge or a force, and a promote does neither.
var ErrNotFastForward = errors.New("promote: prod branch is not an ancestor of the dev branch")

// CheckFastForward fetches both branches and reports whether
// origin/<toBranch> can be fast-forwarded to origin/<fromBranch>. Run it
// before anything is written: a Promote that is not a fast-forward must
// refuse without creating a local tag-carry commit.
func CheckFastForward(ctx context.Context, repoDir string, env []string, fromBranch, toBranch string) error {
	if err := repos.FetchOriginHeads(ctx, repoDir, env, fromBranch, toBranch); err != nil {
		return fmt.Errorf("promote: fetch: %w", err)
	}
	return CheckFetchedFastForward(ctx, repoDir, env, fromBranch, toBranch)
}

// CheckFetchedFastForward is CheckFastForward without the fetch,
// judging the origin/ refs the clone already has. See VerifyFetchedTip
// for when that is the right call.
func CheckFetchedFastForward(ctx context.Context, repoDir string, env []string, fromBranch, toBranch string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "merge-base", "--is-ancestor", "origin/"+toBranch, "origin/"+fromBranch) //nolint:gosec // see FastForward
	applyEnv(cmd, env)
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			prodTip, _ := revParse(ctx, repoDir, env, "origin/"+toBranch)
			devTip, _ := revParse(ctx, repoDir, env, "origin/"+fromBranch)
			return fmt.Errorf("%w: origin/%s is at %s, origin/%s at %s; reconcile the branches (merge %s into %s) before promoting",
				ErrNotFastForward, toBranch, short(prodTip), fromBranch, short(devTip), toBranch, fromBranch)
		}
		return fmt.Errorf("promote: merge-base: %w", err)
	}
	return nil
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func revParse(ctx context.Context, repoDir string, env []string, rev string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", rev) //nolint:gosec // see FastForward
	applyEnv(cmd, env)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("promote: rev-parse %s: %w: %s", rev, err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// originAllowsPin reports whether origin/fromBranch may be promoted as
// gatedSHA: either it still is that commit, or it is an ancestor of it
// (a local tag-carry child that has not been pushed yet). An origin tip
// that moved to an unrelated commit is ErrMoved.
func originAllowsPin(ctx context.Context, repoDir string, env []string, fromBranch, gatedSHA string) error {
	got, err := revParse(ctx, repoDir, env, "origin/"+fromBranch)
	if err != nil {
		return err
	}
	if strings.HasPrefix(got, gatedSHA) {
		return nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "merge-base", "--is-ancestor", "origin/"+fromBranch, gatedSHA) //nolint:gosec
	applyEnv(cmd, env)
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return fmt.Errorf("%w: origin/%s is at %s, gated %s", ErrMoved, fromBranch, got, gatedSHA)
		}
		return fmt.Errorf("promote: merge-base: %w", err)
	}
	return nil
}

// FastForward pushes the gated commit onto toBranch. Git itself refuses
// the push if it is not a fast-forward, so this never silently rewrites
// prod history — a rejected push comes back as an error for the caller
// to turn into a Signal.
//
// gatedSHA is the commit a gate actually passed on, or the local
// tag-carry child of that commit. When set, that exact object is pushed
// (`git push origin <sha>:refs/heads/<toBranch>`) after checking that
// origin/<fromBranch> still points at it, or is an ancestor of it (the
// carry is local and not pushed yet). A commit that landed after the
// gate passed cannot ride along untested: the promote fails with
// ErrMoved instead. Empty gatedSHA pushes the branch tip, as before:
// that is the unpinned path used by operator-initiated promotes
// (`xdlc promote`, POST /api/actions/promote), where a human is
// the authorization.
//
// env carries extra environment variables for git auth (see
// internal/repos.AuthEnv) — nil is fine for a repo git can already push
// to unauthenticated (rare) or via an ambient credential helper.
func FastForward(ctx context.Context, repoDir string, env []string, fromBranch, toBranch, gatedSHA string) error {
	// gosec G204: repoDir is this daemon's own local clone path
	// (internal/repos.Manager.Dir), not external input; branch names
	// come from config.yaml via repos.Manager, and gatedSHA is checked
	// by isHexSHA before it reaches argv.
	if err := repos.FetchOriginHeads(ctx, repoDir, env, fromBranch, toBranch); err != nil {
		return fmt.Errorf("promote: fetch: %w", err)
	}

	src := fromBranch
	dst := toBranch
	if gatedSHA != "" {
		if !isHexSHA(gatedSHA) {
			return fmt.Errorf("promote: %q is not a git object name", gatedSHA)
		}
		if err := originAllowsPin(ctx, repoDir, env, fromBranch, gatedSHA); err != nil {
			return err
		}
		src = gatedSHA
		// A bare object name needs a fully-qualified destination ref;
		// git can't infer refs/heads/ from an object on the left.
		dst = "refs/heads/" + toBranch
	}

	refspec := src + ":" + dst
	push := exec.CommandContext(ctx, "git", "-C", repoDir, "push", "origin", refspec) //nolint:gosec // see above
	applyEnv(push, env)
	var stderr bytes.Buffer
	push.Stderr = &stderr
	if err := push.Run(); err != nil {
		msg := stderr.String()
		if strings.Contains(msg, "non-fast-forward") || strings.Contains(msg, "not fast-forward") {
			return fmt.Errorf("promote: push %s->%s not fast-forwardable: %w: %s", src, toBranch, err, msg)
		}
		return fmt.Errorf("promote: push %s->%s: %w: %s", src, toBranch, err, msg)
	}
	return nil
}

// isHexSHA reports whether s is a bare git object name (lowercase hex,
// abbreviated 7 up to SHA-256's 64). Anything else — a ref expression,
// something starting with "-" — is refused before it reaches argv.
func isHexSHA(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

func applyEnv(cmd *exec.Cmd, env []string) {
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
}
