// Package subagent runs a per-repo AI coding agent subprocess to
// fix-forward or investigate a failure. MVP wraps a headless CLI
// (Claude Code, OpenAI Codex, or Cursor CLI, and anything else that
// takes a prompt and edits/commits code); a future mode="sdk"
// implementation could talk to a provider's API directly for tighter
// control (see docs/architecture.md for the tradeoff).
package subagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

// ErrStalled is returned when the agent produced no output for
// StallTimeout while its process was still alive, and xdlc killed it.
//
// It is a distinct error because a stall is not a timeout and not a
// crash: the run had budget left and the process was healthy enough to
// keep holding a worktree, an API session and the repo's Fix slot. An
// operator who sees "timeout" looks for a slow build; one who sees
// "stalled" looks for an agent waiting on something that will never
// come. Dispatch maps it to escalate=stalled.
var ErrStalled = errors.New("subagent: no output for the stall timeout")

// allowlistEnvKeys are the only env vars passed to the coding-agent
// subprocess. Keeps PATH/locale basics plus the provider API keys the
// CLIs need; drops GITHUB_*, webhook secrets, XDL_*, etc.
var allowlistEnvKeys = map[string]struct{}{
	"PATH": {}, "HOME": {}, "USER": {}, "LOGNAME": {},
	"LANG": {}, "LC_ALL": {}, "LANGUAGE": {}, "TZ": {},
	"ANTHROPIC_API_KEY": {}, "OPENAI_API_KEY": {}, "CURSOR_API_KEY": {},
	"GEMINI_API_KEY": {}, "GOOGLE_API_KEY": {},
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
	ProviderGemini Provider = "gemini" // Google Gemini CLI
)

// Providers lists every built-in Provider, for CLI help, `xdlc doctor`
// and config validation — one source of truth instead of a switch
// repeated per call site.
func Providers() []Provider {
	return []Provider{ProviderClaude, ProviderCodex, ProviderCursor, ProviderGemini}
}

// KnownProvider reports whether p is one of Providers(). An unknown
// provider still runs (Claude's argv shape is the fallback), so this is
// for warnings, not enforcement.
func KnownProvider(p string) bool {
	for _, known := range Providers() {
		if string(known) == p {
			return true
		}
	}
	return false
}

// DefaultBinary returns the CLI name a Provider shells out to with no
// agent.binary override — what `xdlc doctor` looks for on PATH.
func DefaultBinary(p Provider) string {
	spec, ok := providerDefaults[p]
	if !ok {
		return providerDefaults[ProviderClaude].binary
	}
	return spec.binary
}

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
		// -p headless. --dangerously-skip-permissions auto-approves the
		// Edit/Write/Bash calls a Fix needs; without it a headless run
		// denies every write (permission_denials in the JSON result) and
		// the agent can only report needs_human. Same tradeoff as
		// cursor's --force and gemini's --yolo: the worktree is the
		// sandbox.
		args: []string{"-p", promptPlaceholder, "--output-format", "json", "--dangerously-skip-permissions"},
	},
	ProviderCodex: {
		binary: "codex",
		args:   []string{"exec", promptPlaceholder},
	},
	ProviderCursor: {
		binary: "cursor-agent",
		// -p print/non-interactive. --trust skips the workspace prompt.
		// --force (alias --yolo) auto-approves file/shell tools; --trust
		// alone still blocks unallowlisted commands as userRejected, and
		// a headless Fix has no TTY to answer them.
		args: []string{"-p", "--trust", "--force", promptPlaceholder},
	},
	ProviderGemini: {
		binary: "gemini",
		// -p headless prompt; --yolo auto-approves the file/shell tool
		// calls a Fix needs (the CLI otherwise waits on a TTY prompt that
		// never comes). Same tradeoff as cursor's --force.
		args: []string{"-p", promptPlaceholder, "--yolo"},
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
	// StallTimeout kills the run when the CLI has printed nothing for
	// this long while still alive. 0 disables the watchdog.
	//
	// It only means anything with a CLI that streams: `claude -p
	// --output-format json` prints its whole result at exit, so every
	// healthy long run looks stalled to a byte-counting watchdog. Set it
	// through WithStallTimeout, which switches that argv to stream-json
	// at the same time.
	StallTimeout time.Duration
	// ExtraEnvKeys widens the subprocess env allowlist — see ExtractEnv.
	ExtraEnvKeys []string
}

// APIKeyEnvName is the subprocess env var each Provider's CLI reads.
func APIKeyEnvName(p Provider) string {
	switch p {
	case ProviderCodex:
		return "OPENAI_API_KEY"
	case ProviderCursor:
		return "CURSOR_API_KEY"
	case ProviderGemini:
		return "GEMINI_API_KEY"
	default:
		return "ANTHROPIC_API_KEY"
	}
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

// WithModel returns a copy of r that asks the CLI for a specific model,
// by appending `--model <name>` to its argv. All four provider CLIs
// accept that flag.
//
// This exists because the model, not the provider, is what a Fix's bill
// mostly depends on: the same Fix that cost $1.63 on the priciest model
// is well under half that on a mid-tier one. Overriding argv wholesale
// (agent.args) could already do this, but that means restating the
// provider's whole headless invocation to change one word, and the
// alternative of an ANTHROPIC_MODEL env var only works for one of the
// four and is not on the allowlist.
//
// An empty name returns r unchanged, so callers can pass through a flag
// the operator left unset.
func (r *SubprocessRunner) WithModel(name string) *SubprocessRunner {
	if name == "" {
		return r
	}
	clone := *r
	clone.Args = append(append([]string(nil), r.Args...), "--model", name)
	return &clone
}

// WithStallTimeout returns a copy of r that kills a run which has
// printed nothing for d, and — because a watchdog that cannot see
// output is worse than none — switches a buffered `--output-format
// json` argv to `stream-json --verbose` so there is output to watch.
//
// The two travel together on purpose. Streaming changes what lands in
// the session's output.txt (JSON events, one per line, instead of one
// result object), so an operator who never asked for the watchdog keeps
// the output they had. d <= 0 returns r unchanged, so a call site can
// pass an unset config value straight through.
//
// The rewrite is by argv shape, not by provider, so an operator who
// spelled out `--output-format json` in agent.args gets the same
// switch. Any other argv is passed through untouched — the other three
// CLIs already print as they work, so their watchdog needs nothing. A
// CLI that buffers behind some flag xdlc does not recognize would have
// every healthy run killed as stalled; that is what agent.args is for.
func (r *SubprocessRunner) WithStallTimeout(d time.Duration) *SubprocessRunner {
	if d <= 0 {
		return r
	}
	clone := *r
	clone.StallTimeout = d
	clone.Args = streamingArgsFor(r.Provider, r.Args)
	return &clone
}

// streamingArgsFor is streamingArgs plus the one provider whose silence
// is not visible in its argv: cursor-agent's default `--output-format
// text` prints nothing until exit, so a watchdog saw a healthy run as
// stalled and the console saw nothing at all. Cursor takes stream-json
// too; add it when the operator did not pick a format.
func streamingArgsFor(p Provider, args []string) []string {
	out := streamingArgs(args)
	if p != ProviderCursor {
		return out
	}
	for _, a := range out {
		if a == "--output-format" {
			return out
		}
	}
	return append(out, "--output-format", "stream-json")
}

// streamingArgs rewrites the one argv shape that buffers its whole
// output to exit — Claude Code's `--output-format json` — into its
// streaming form. `--verbose` is required alongside stream-json in
// headless mode, or the CLI refuses to start. Anything else is returned
// untouched: the other three CLIs already print as they work.
func streamingArgs(args []string) []string {
	out := make([]string, 0, len(args)+1)
	for i := 0; i < len(args); i++ {
		if args[i] == "--output-format" && i+1 < len(args) && args[i+1] == "json" {
			out = append(out, "--output-format", "stream-json", "--verbose")
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}

// Run invokes the configured CLI in repoDir with prompt on stdin (never
// argv), bounded by r.Timeout. On timeout, and on a stall (see
// StallTimeout), the whole process group is killed. extraEnv is
// appended after the allowlist filter — use it for git AuthEnv
// (GIT_CONFIG_*), never for GITHUB_*.
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

	// Bounded, not a bytes.Buffer: a verbose agent in stream-json mode
	// can print far more than anyone reads back, and every consumer of
	// the result — ParseVerdict, ParseCost, the session file's tail, the
	// error message — wants the end of the stream, not the start.
	stdout, stderr := newTailWriter(outputTailLimit), newTailWriter(outputTailLimit)
	// Both streams feed the watchdog: a CLI that is narrating its
	// progress on stderr is working, whatever stdout is doing.
	activity := &activityWriter{}
	outW := io.Writer(stdout)
	errW := io.Writer(stderr)
	if tap := outputTap(ctx); tap != nil {
		// The console's live view. Both streams, for the same reason
		// the watchdog watches both: several CLIs narrate on stderr.
		outW = io.MultiWriter(stdout, tap)
		errW = io.MultiWriter(stderr, tap)
	}
	cmd.Stdout = io.MultiWriter(outW, activity)
	cmd.Stderr = io.MultiWriter(errW, activity)

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("subagent: %s start in %s: %w", r.Binary, repoDir, err)
	}
	activity.touch()
	stopWatchdog, stalled := r.watchStall(cmd, activity)
	err := cmd.Wait()
	stopWatchdog()

	if stalled.Load() {
		// The kill is why Wait returned, so its "signal: killed" says
		// nothing an operator needs; the stall is the finding.
		return stdout.String(), fmt.Errorf("subagent: %s in %s: %w of %s: %s",
			r.Binary, repoDir, ErrStalled, r.StallTimeout, strings.TrimSpace(lastLines(stderr.String(), 5)))
	}
	if err != nil {
		// The last lines, not the whole stream: the error lands in a
		// log line and an audit row, and an agent's full stderr is a
		// transcript, not a reason.
		return stdout.String(), fmt.Errorf("subagent: %s run in %s: %w: %s",
			r.Binary, repoDir, err, strings.TrimSpace(lastLines(stderr.String(), 20)))
	}
	return stdout.String(), nil
}

// watchStall starts the stall watchdog for a running cmd. It returns a
// stop function the caller must call once Wait returns, and the flag
// that says whether the watchdog is what ended the run.
//
// A no-op when StallTimeout is unset, so the ordinary path adds one
// branch and no goroutine.
func (r *SubprocessRunner) watchStall(cmd *exec.Cmd, activity *activityWriter) (stop func(), stalled *atomic.Bool) {
	stalled = &atomic.Bool{}
	if r.StallTimeout <= 0 {
		return func() {}, stalled
	}
	done := make(chan struct{})
	// Check several times per window: the granularity is how long a
	// wedged agent keeps its worktree and its Fix slot after the
	// deadline, and a ticker at the full window could double the wait.
	interval := r.StallTimeout / 4
	if interval > 15*time.Second {
		interval = 15 * time.Second
	}
	if interval < time.Second {
		interval = time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if activity.idleFor() < r.StallTimeout {
					continue
				}
				stalled.Store(true)
				_ = killProcessGroup(cmd)
				return
			}
		}
	}()
	return func() { close(done) }, stalled
}

// outputTailLimit is how much of each subprocess stream Run keeps. The
// verdict and the cost line are the last thing an agent prints, and a
// session file that holds the final 8 MiB of a run holds everything an
// operator has ever gone back to read.
const outputTailLimit = 8 << 20

// tailWriter is an io.Writer that keeps only the last limit bytes it was
// given. It grows like a plain buffer until it reaches the limit and
// then writes in a ring, so a run that prints little costs little and a
// run that prints without end costs exactly the limit.
//
// The cut is by byte, not by line: the first line of the kept tail may
// be partial. Both readers cope — ParseVerdict scans backwards for a
// whole JSON object and ParseCost reads lines from the end.
type tailWriter struct {
	limit int
	buf   []byte
	// head is where the next byte goes once the ring is full, which is
	// also the index of the oldest byte kept. Meaningless until wrapped.
	head    int
	wrapped bool
}

func newTailWriter(limit int) *tailWriter {
	return &tailWriter{limit: limit}
}

// Write always reports the whole of p as written: dropping the front of
// the stream is this writer's job, never an error for the subprocess.
func (w *tailWriter) Write(p []byte) (int, error) {
	n := len(p)
	if !w.wrapped {
		room := w.limit - len(w.buf)
		if n <= room {
			w.buf = append(w.buf, p...)
			return n, nil
		}
		// Fill what is left, then treat the rest as ring writes.
		w.buf = append(w.buf, p[:room]...)
		p = p[room:]
		w.wrapped = true
		w.head = 0
	}
	if len(p) >= w.limit {
		// One write bigger than the whole tail: only its end survives,
		// and the ring may as well start over at zero.
		copy(w.buf, p[len(p)-w.limit:])
		w.head = 0
		return n, nil
	}
	k := copy(w.buf[w.head:], p)
	if k < len(p) {
		copy(w.buf, p[k:])
	}
	w.head = (w.head + len(p)) % w.limit
	return n, nil
}

// String returns the kept tail in write order.
func (w *tailWriter) String() string {
	if !w.wrapped {
		return string(w.buf)
	}
	out := make([]byte, 0, w.limit)
	out = append(out, w.buf[w.head:]...)
	out = append(out, w.buf[:w.head]...)
	return string(out)
}

// activityWriter records when the subprocess last wrote anything. It
// keeps no bytes: the output itself is buffered elsewhere, and all the
// watchdog needs is a timestamp it can read without locking.
type activityWriter struct {
	lastNanos atomic.Int64
}

func (w *activityWriter) Write(p []byte) (int, error) {
	w.touch()
	return len(p), nil
}

func (w *activityWriter) touch() { w.lastNanos.Store(time.Now().UnixNano()) }

// idleFor is how long since the last write. Zero before the first
// touch, so a watchdog can never fire on an unstarted process.
func (w *activityWriter) idleFor() time.Duration {
	last := w.lastNanos.Load()
	if last == 0 {
		return 0
	}
	return time.Since(time.Unix(0, last))
}

// lastLines returns the final n lines of s, for an error message that
// should carry the agent's last words without its whole transcript.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
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
