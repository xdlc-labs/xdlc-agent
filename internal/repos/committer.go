package repos

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Default git identity xdlc commits under when neither the environment
// nor git's own config supplies one.
//
// This exists because a commit is the *only* delivery mechanism a Fix
// has in worktree mode (see internal/subagent's fixAction: the agent
// commits, xdlc pushes), and `git commit` refuses to run without a
// resolvable author. The shipped container runs as uid 65532 with an
// empty HOME and no .gitconfig, so every containerized Fix used to
// produce no commit, push nothing, and still be recorded as clean.
// Supplying the identity here rather than only baking a .gitconfig into
// the image keeps a bare-metal daemon — a service account with no git
// config of its own — working the same way.
//
// The email is GitHub's deliberately non-routable noreply form, so a
// commit made by an unattended daemon is attributable to the tool and
// bounces nothing to a real person.
const (
	DefaultCommitterName  = "xdlc-agent"
	DefaultCommitterEmail = "xdlc-agent@users.noreply.github.com"
)

// committerEnvKeys are the four variables git reads an identity from,
// in preference to any config file. Author and committer are set
// independently so an operator who overrides only one keeps the other.
var committerEnvKeys = [4]string{
	"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL",
	"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL",
}

// CommitterEnv builds the GIT_AUTHOR_*/GIT_COMMITTER_* entries to append
// to a git (or coding-agent) subprocess environment, so `git commit`
// always has an identity to attribute a Fix to.
//
// Precedence, highest first:
//
//  1. the daemon's own environment — an operator who exported
//     GIT_COMMITTER_NAME meant it, and the value has to be re-emitted
//     rather than merely left alone because the subagent env is an
//     allowlist (internal/subagent.ExtractEnv) that would drop it;
//  2. git's own config (user.name / user.email), so a GitHub App bot
//     identity in ~/.gitconfig or /etc/gitconfig keeps working;
//  3. name/email from config.yaml's agent.committer;
//  4. DefaultCommitterName / DefaultCommitterEmail.
//
// Nothing here is a credential, so these are safe to hand to the coding
// agent alongside repos.AuthEnv.
func CommitterEnv(name, email string) []string {
	cfgName, cfgEmail := hostGitIdentity()
	return committerEnv(name, email, cfgName, cfgEmail, os.LookupEnv)
}

// committerEnv is CommitterEnv's pure core: cfgName/cfgEmail are git
// config's answer ("" when git cannot resolve one) and lookupEnv is the
// process environment. Split out so the precedence is testable without
// mutating the real environment or a real git config.
func committerEnv(name, email, cfgName, cfgEmail string, lookupEnv func(string) (string, bool)) []string {
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	if name == "" {
		name = DefaultCommitterName
	}
	if email == "" {
		email = DefaultCommitterEmail
	}
	if cfgName != "" {
		name = cfgName
	}
	if cfgEmail != "" {
		email = cfgEmail
	}

	out := make([]string, 0, len(committerEnvKeys))
	for _, key := range committerEnvKeys {
		value := name
		if strings.HasSuffix(key, "_EMAIL") {
			value = email
		}
		if v, ok := lookupEnv(key); ok && strings.TrimSpace(v) != "" {
			value = v
		}
		out = append(out, key+"="+value)
	}
	return out
}

// hostGitIdentityTimeout bounds the one `git config` probe below. It is
// a local config read, so this is only here to keep a wedged git from
// stalling the first Fix.
const hostGitIdentityTimeout = 5 * time.Second

// hostGitIdentity reports git's own configured user.name / user.email,
// or "" for either when git cannot resolve one.
//
// `git config --get` (rather than `git var GIT_COMMITTER_IDENT`) is the
// probe on purpose: git var happily *guesses* an identity from
// /etc/passwd and the hostname, which is exactly the guess `git commit`
// then refuses to use ("Author identity unknown"), so a guess would
// suppress the default and reintroduce the bug.
//
// Resolved once per process and cached: it reads global and system
// config, which does not change under a running daemon, and it is on
// the path of every git operation.
var hostGitIdentity = sync.OnceValues(func() (string, string) {
	return gitConfigValue("user.name"), gitConfigValue("user.email")
})

// gitConfigValue returns git config's value for key, or "" when it is
// unset (or git is unavailable — an install with no git binary has
// bigger problems than its committer name, and the default covers it).
func gitConfigValue(key string) string {
	ctx, cancel := context.WithTimeout(context.Background(), hostGitIdentityTimeout)
	defer cancel()
	// gosec G204: key is one of the two literals above, never input.
	out, err := exec.CommandContext(ctx, "git", "config", "--get", key).Output() //nolint:gosec
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
