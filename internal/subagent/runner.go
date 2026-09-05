// Package subagent runs a per-repo AI coding agent subprocess to
// fix-forward or investigate a failure. MVP wraps a headless CLI
// (Claude Code, OpenAI Codex, or Cursor CLI, and anything else that
// takes a prompt and edits/commits code); a future mode="sdk"
// implementation could talk to a provider's API directly for tighter
// control (see docs/architecture.md for the tradeoff).
package subagent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// allowlistEnvKeys are the only env vars passed to the coding-agent
// subprocess. Keeps PATH/locale basics plus the provider API keys the
// CLIs need; drops GITHUB_*, webhook secrets, XDL_*, etc.
var allowlistEnvKeys = map[string]struct{}{
	"PATH": {}, "HOME": {}, "USER": {}, "LOGNAME": {},
	"LANG": {}, "LC_ALL": {}, "LANGUAGE": {}, "TZ": {},
	"ANTHROPIC_API_KEY": {}, "OPENAI_API_KEY": {}, "CURSOR_API_KEY": {},
}

// ExtractAllowlistEnv filters environ (os.Environ-style KEY=value
// entries) down to allowlistEnvKeys. Exported for tests.
func ExtractAllowlistEnv(environ []string) []string {
	return ExtractEnv(environ, nil)
}

// ExtractEnv filters environ down to allowlistEnvKeys plus extraKeys
// (config.yaml's agent.extra_env_keys — e.g. HTTPS_PROXY,
// NODE_EXTRA_CA_CERTS for a corporate egress proxy the coding-agent CLI
// needs to reach its vendor API). extraKeys lets an operator widen the
// subprocess env without a code change; it does not disable the
// baseline allowlist's exclusion of GitHub/webhook/API secrets.
func ExtractEnv(environ []string, extraKeys []string) []string {
	keep := allowlistEnvKeys
	if len(extraKeys) > 0 {
		keep = make(map[string]struct{}, len(allowlistEnvKeys)+len(extraKeys))
		for k := range allowlistEnvKeys {
			keep[k] = struct{}{}
		}
		for _, k := range extraKeys {
			keep[k] = struct{}{}
		}
	}
	out := make([]string, 0, len(keep))
	for _, kv := range environ {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if _, ok := keep[key]; ok {
			out = append(out, kv)
		}
	}
	return out
}

// Mode selects how the subagent invokes the coding agent, see the
// package doc's subprocess-vs-SDK tradeoff.
type Mode string

// The two Modes config.yaml's agent.mode accepts. Only ModeSubprocess
// is implemented; ModeSDK is reserved.
const (
	ModeSubprocess Mode = "subprocess" // shells out to a headless CLI
	ModeSDK        Mode = "sdk"        // reserved, not yet implemented
)

// Provider selects which coding agent CLI SubprocessRunner shells out
// to. Each has a different headless/non-interactive invocation; see
// providerDefaults.
type Provider string

// The Providers built in. An unrecognized Provider falls back to
// ProviderClaude's argv shape (NewSubprocessRunner) — safest default
// for any provider config.yaml doesn't yet name.
const (
	ProviderClaude Provider = "claude" // Claude Code CLI
	ProviderCodex  Provider = "codex"  // OpenAI Codex CLI
	ProviderCursor Provider = "cursor" // Cursor CLI (cursor-agent)
)

// promptPlaceholder marks where the prompt used to sit on argv. At Run
// time that token is stripped and the prompt is fed on stdin instead, so
// it never appears in /proc/*/cmdline (issue #11). Keep the marker in
// defaults/templates so operator overrides still declare intent.
const promptPlaceholder = "{{prompt}}"

type providerSpec struct {
	binary string
	args   []string // may include promptPlaceholder (stripped → stdin)
}

// providerDefaults holds each Provider's default binary and headless
// invocation shape. These are each vendor's own CLI surface and can
// change between releases — override via config.yaml's agent.binary /
// agent.args if yours has drifted from what's baked in here.
var providerDefaults = map[Provider]providerSpec{
	ProviderClaude: {
		binary: "claude",
		args:   []string{"-p", promptPlaceholder, "--output-format", "json"},
	},
	ProviderCodex: {
		binary: "codex",
		args:   []string{"exec", promptPlaceholder},
	},
	ProviderCursor: {
		binary: "cursor-agent",
		args:   []string{"-p", promptPlaceholder},
	},
}

// Runner delegates one task to a coding agent, scoped to a single
// repo's working directory.
type Runner interface {
	// Run executes the agent in repoDir. extraEnv is appended after the
	// scrubbed allowlist (e.g. repos.AuthEnv GIT_CONFIG_* for git push).
	Run(ctx context.Context, repoDir, prompt string, extraEnv []string) (output string, err error)
}

// SubprocessRunner shells out to a headless CLI. Binary and Args are
// resolved from Provider by NewSubprocessRunner unless explicitly
// overridden.
type SubprocessRunner struct {
	Provider Provider
	Binary   string
	Args     []string // promptPlaceholder stripped; prompt goes on stdin
	Timeout  time.Duration
	// ExtraEnvKeys widens the subprocess env allowlist — see ExtractEnv.
	ExtraEnvKeys []string
}

// NewSubprocessRunner returns a SubprocessRunner for provider, applying
// its default binary/args unless binary or args override them. args may
// contain promptPlaceholder ("{{prompt}}") — it is stripped at Run and
// the prompt is written to stdin; pass nil to use the provider default.
// timeout defaults to 10 minutes. extraEnvKeys is config.yaml's
// agent.extra_env_keys, passed through to ExtractEnv on every Run.
func NewSubprocessRunner(provider Provider, binary string, args []string, timeout time.Duration, extraEnvKeys []string) *SubprocessRunner {
	spec, ok := providerDefaults[provider]
	if !ok {
		spec = providerDefaults[ProviderClaude]
	}
	if binary == "" {
		binary = spec.binary
	}
	if len(args) == 0 {
		args = spec.args
	}
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	return &SubprocessRunner{Provider: provider, Binary: binary, Args: args, Timeout: timeout, ExtraEnvKeys: extraEnvKeys}
}

// Run invokes the configured CLI in repoDir with prompt on stdin (never
// argv), bounded by r.Timeout. On timeout the whole process group is
// killed. extraEnv is appended after the allowlist filter — use it for
// git AuthEnv (GIT_CONFIG_*), never for GITHUB_*.
func (r *SubprocessRunner) Run(ctx context.Context, repoDir, prompt string, extraEnv []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	argv := stripPromptPlaceholder(r.Args)

	// gosec G204: r.Binary and argv come from operator config
	// (config.yaml's agent.binary/agent.provider/agent.args), not
	// attacker-controlled input. Prompt is on stdin, not argv.
	cmd := exec.CommandContext(ctx, r.Binary, argv...) //nolint:gosec
	cmd.Dir = repoDir
	cmd.Env = append(ExtractEnv(os.Environ(), r.ExtraEnvKeys), extraEnv...)
	cmd.Stdin = strings.NewReader(prompt)
	configureKillGroup(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("subagent: %s run in %s: %w: %s", r.Binary, repoDir, err, stderr.String())
	}
	return stdout.String(), nil
}

// stripPromptPlaceholder drops {{prompt}} from argv; content goes on stdin.
func stripPromptPlaceholder(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == promptPlaceholder {
			continue
		}
		out = append(out, a)
	}
	return out
}
