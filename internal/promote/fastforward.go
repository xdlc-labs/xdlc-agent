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
	"path/filepath"
	"regexp"
	"strings"
)

var tagLine = regexp.MustCompile(`(?m)^(\s*tag:\s*)(["']?)([^"'\n]+)(["']?)\s*$`)

// ReadProdTag returns image.tag from gitops/values/prod/<service>.yaml
// in repoDir. Empty string + nil if the file is missing (no gitops/).
func ReadProdTag(repoDir, service string) (string, error) {
	return readTag(filepath.Join(repoDir, "gitops", "values", "prod", service+".yaml"))
}

// ReadDevTag returns image.tag from gitops/values/dev/<service>.yaml.
func ReadDevTag(repoDir, service string) (string, error) {
	return readTag(filepath.Join(repoDir, "gitops", "values", "dev", service+".yaml"))
}

func readTag(path string) (string, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path under agent-owned repo clone
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("promote: read values: %w", err)
	}
	m := tagLine.FindSubmatch(raw)
	if m == nil {
		return "", fmt.Errorf("promote: no tag: line in %s", path)
	}
	return string(m[3]), nil
}

// CarryProdTag copies image.tag from gitops/values/dev/<service>.yaml
// into values/prod/<service>.yaml in repoDir. Returns true if the prod
// file changed. No-op (false, nil) if either file is missing — multi-repo
// service clones without gitops/ skip quietly.
func CarryProdTag(repoDir, service string) (bool, error) {
	devPath := filepath.Join(repoDir, "gitops", "values", "dev", service+".yaml")
	prodPath := filepath.Join(repoDir, "gitops", "values", "prod", service+".yaml")
	devRaw, err := os.ReadFile(devPath) //nolint:gosec // path under agent-owned repo clone
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("promote: read dev values: %w", err)
	}
	prodRaw, err := os.ReadFile(prodPath) //nolint:gosec // path under agent-owned repo clone
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("promote: read prod values: %w", err)
	}
	devTag := tagLine.FindSubmatch(devRaw)
	if devTag == nil {
		return false, fmt.Errorf("promote: no tag: line in %s", devPath)
	}
	tag := string(devTag[3])
	prodTag := tagLine.FindSubmatch(prodRaw)
	if prodTag == nil {
		return false, fmt.Errorf("promote: no tag: line in %s", prodPath)
	}
	if string(prodTag[3]) == tag {
		return false, nil
	}
	newProd := tagLine.ReplaceAllFunc(prodRaw, func(line []byte) []byte {
		m := tagLine.FindSubmatch(line)
		if m == nil {
			return line
		}
		return []byte(fmt.Sprintf("%s%s%s%s", m[1], m[2], tag, m[4]))
	})
	// GitOps values are world-readable by design (committed YAML).
	if err := os.WriteFile(prodPath, newProd, 0o644); err != nil { //nolint:gosec // G306: values.yaml must stay 0644 for git
		return false, fmt.Errorf("promote: write prod values: %w", err)
	}
	return true, nil
}

// CommitProdTag stages and commits the prod values file if dirty, then
// pushes to origin branch (the configured dev branch). It returns the
// SHA of the commit it pushed, or "" when there was nothing to commit —
// the caller needs it to keep a SHA-pinned promote pinned across the tag
// carry (see dispatch.Dispatcher.Promote).
//
// The push is deliberately not forced: if the dev branch moved between
// the caller's pin check and here, git rejects it and the promote fails
// instead of racing.
func CommitProdTag(ctx context.Context, repoDir, service string, env []string, branch string) (string, error) {
	rel := filepath.Join("gitops", "values", "prod", service+".yaml")
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
	refspec := "HEAD:" + branch
	push := exec.CommandContext(ctx, "git", "-C", repoDir, "push", "origin", refspec) //nolint:gosec
	applyEnv(push, env)
	if out, err := push.CombinedOutput(); err != nil {
		return "", fmt.Errorf("promote: push %s: %w: %s", branch, err, out)
	}
	sha, err := revParse(ctx, repoDir, env, "HEAD")
	if err != nil {
		return "", err
	}
	return sha, nil
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
	fetch := exec.CommandContext(ctx, "git", "-C", repoDir, "fetch", "origin", branch) //nolint:gosec // see FastForward
	applyEnv(fetch, env)
	if out, err := fetch.CombinedOutput(); err != nil {
		return fmt.Errorf("promote: fetch %s: %w: %s", branch, err, out)
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

func revParse(ctx context.Context, repoDir string, env []string, rev string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", rev) //nolint:gosec // see FastForward
	applyEnv(cmd, env)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("promote: rev-parse %s: %w: %s", rev, err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// FastForward pushes the gated commit onto toBranch. Git itself refuses
// the push if it is not a fast-forward, so this never silently rewrites
// prod history — a rejected push comes back as an error for the caller
// to turn into a Signal.
//
// gatedSHA is the commit a gate actually passed on. When set, that exact
// object is pushed (`git push origin <sha>:refs/heads/<toBranch>`) after
// checking that origin/<fromBranch> still points at it, so a commit that
// landed after the gate passed cannot ride along untested — the promote
// fails with ErrMoved instead. Empty gatedSHA pushes the branch tip, as
// before: that is the unpinned path used by operator-initiated promotes
// (`xdlc-agent promote`, POST /api/actions/promote), where a human is
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
	fetch := exec.CommandContext(ctx, "git", "-C", repoDir, "fetch", "origin", fromBranch, toBranch) //nolint:gosec
	applyEnv(fetch, env)
	if out, err := fetch.CombinedOutput(); err != nil {
		return fmt.Errorf("promote: fetch: %w: %s", err, out)
	}

	src := fromBranch
	dst := toBranch
	if gatedSHA != "" {
		if !isHexSHA(gatedSHA) {
			return fmt.Errorf("promote: %q is not a git object name", gatedSHA)
		}
		got, err := revParse(ctx, repoDir, env, "origin/"+fromBranch)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(got, gatedSHA) {
			return fmt.Errorf("%w: origin/%s is at %s, gated %s", ErrMoved, fromBranch, got, gatedSHA)
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
		return fmt.Errorf("promote: push %s->%s not fast-forwardable: %w: %s", src, toBranch, err, stderr.String())
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
