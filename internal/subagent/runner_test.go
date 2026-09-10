package subagent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestNewSubprocessRunnerProviderDefaults(t *testing.T) {
	cases := []struct {
		provider   Provider
		wantBinary string
		wantArgs   []string
	}{
		{ProviderClaude, "claude", []string{"-p", promptPlaceholder, "--output-format", "json", "--dangerously-skip-permissions"}},
		{ProviderCodex, "codex", []string{"exec", promptPlaceholder}},
		{ProviderCursor, "cursor-agent", []string{"-p", "--trust", "--force", promptPlaceholder}},
		{"some-unknown-future-provider", "claude", []string{"-p", promptPlaceholder, "--output-format", "json", "--dangerously-skip-permissions"}},
	}

	for _, c := range cases {
		t.Run(string(c.provider), func(t *testing.T) {
			r := NewSubprocessRunner(c.provider, "", nil, 0, nil)
			if r.Binary != c.wantBinary {
				t.Errorf("Binary = %q, want %q", r.Binary, c.wantBinary)
			}
			if strings.Join(r.Args, ",") != strings.Join(c.wantArgs, ",") {
				t.Errorf("Args = %v, want %v", r.Args, c.wantArgs)
			}
			if r.Timeout != 10*time.Minute {
				t.Errorf("Timeout = %v, want 10m default", r.Timeout)
			}
		})
	}
}

func TestNewSubprocessRunnerOverrides(t *testing.T) {
	r := NewSubprocessRunner(ProviderClaude, "my-claude-wrapper", []string{"run", promptPlaceholder}, 5*time.Minute, nil)
	if r.Binary != "my-claude-wrapper" {
		t.Errorf("Binary override not applied: %q", r.Binary)
	}
	if strings.Join(r.Args, ",") != "run,"+promptPlaceholder {
		t.Errorf("Args override not applied: %v", r.Args)
	}
	if r.Timeout != 5*time.Minute {
		t.Errorf("Timeout override not applied: %v", r.Timeout)
	}
}

func TestRunSubstitutesPromptAndReturnsOutput(t *testing.T) {
	// "cat" reads stdin: proves prompt is fed on stdin, not argv.
	r := NewSubprocessRunner(ProviderClaude, "cat", []string{promptPlaceholder}, time.Minute, nil)

	out, err := r.Run(context.Background(), t.TempDir(), "fix the failing test in svc-a", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "fix the failing test in svc-a") {
		t.Errorf("output = %q, want it to contain the prompt", out)
	}
}

func TestRunPromptNotOnArgv(t *testing.T) {
	dir := t.TempDir()
	outFile := dir + "/cmdline"
	secret := "UNIQUE_PROMPT_SECRET_xyzzy_not_on_argv"
	// Dump /proc/self/cmdline then exit. Prompt must not appear there.
	script := "tr '\\0' ' ' </proc/self/cmdline >" + outFile
	r := NewSubprocessRunner(ProviderClaude, "sh", []string{"-c", script, promptPlaceholder}, time.Minute, nil)
	if _, err := r.Run(context.Background(), dir, secret, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), secret) {
		t.Errorf("prompt leaked onto cmdline: %q", got)
	}
}

func TestRunTimeoutKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	childPIDFile := dir + "/child.pid"
	// Parent spawns a long-lived child, records its pid, then sleeps.
	// Timeout must kill the whole group so the child does not survive.
	script := "sleep 120 & echo $! >" + childPIDFile + "; sleep 120"
	r := NewSubprocessRunner(ProviderClaude, "sh", []string{"-c", script, promptPlaceholder}, 200*time.Millisecond, nil)
	_, err := r.Run(context.Background(), dir, "unused", nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	time.Sleep(300 * time.Millisecond)
	raw, err := os.ReadFile(childPIDFile)
	if err != nil {
		t.Fatalf("child pid file missing (script never started?): %v", err)
	}
	pidStr := strings.TrimSpace(string(raw))
	if pidStr == "" {
		t.Fatal("empty child pid")
	}
	// kill -0: process exists?
	ctx := context.Background()
	if err := exec.CommandContext(ctx, "kill", "-0", pidStr).Run(); err == nil {
		_ = exec.CommandContext(ctx, "kill", "-9", pidStr).Run()
		t.Fatalf("orphan child pid %s still alive after timeout", pidStr)
	}
}

func TestRunFailureIncludesStderr(t *testing.T) {
	// "false" always exits 1; sh -c lets us also emit stderr text to
	// confirm it's folded into the returned error.
	r := NewSubprocessRunner(ProviderClaude, "sh", []string{"-c", "echo boom >&2; exit 1"}, time.Minute, nil)

	_, err := r.Run(context.Background(), t.TempDir(), "unused prompt", nil)
	if err == nil {
		t.Fatal("expected an error from a failing subprocess")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want it to include stderr output", err)
	}
}

func TestExtractAllowlistEnvDropsSecrets(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"HOME=/home/agent",
		"USER=agent",
		"LANG=C.UTF-8",
		"TZ=UTC",
		"ANTHROPIC_API_KEY=sk-ant-ok",
		"OPENAI_API_KEY=sk-openai-ok",
		"CURSOR_API_KEY=sk-cursor-ok",
		"GITHUB_TOKEN=ghp_secret",
		"GITHUB_APP_PRIVATE_KEY=-----BEGIN",
		"GITHUB_WEBHOOK_SECRET=whsec",
		"ARGOCD_WEBHOOK_SECRET=argocd",
		"ALERTMANAGER_WEBHOOK_SECRET=am",
		"XDL_SOMETHING=nope",
		"RANDOM_SECRET=drop-me",
	}
	got := ExtractAllowlistEnv(in)
	joined := strings.Join(got, "\n")

	for _, want := range []string{
		"PATH=/usr/bin", "HOME=/home/agent", "USER=agent", "LANG=C.UTF-8", "TZ=UTC",
		"ANTHROPIC_API_KEY=sk-ant-ok", "OPENAI_API_KEY=sk-openai-ok", "CURSOR_API_KEY=sk-cursor-ok",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing allowlisted entry %q in %v", want, got)
		}
	}
	for _, secret := range []string{
		"GITHUB_TOKEN", "GITHUB_APP_PRIVATE_KEY", "GITHUB_WEBHOOK_SECRET",
		"ARGOCD_WEBHOOK_SECRET", "ALERTMANAGER_WEBHOOK_SECRET", "XDL_SOMETHING", "RANDOM_SECRET",
	} {
		for _, kv := range got {
			if strings.HasPrefix(kv, secret+"=") {
				t.Errorf("secret %q leaked into scrubbed env: %v", secret, got)
			}
		}
	}
}

func TestExtractEnvWithExtraKeys(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"HTTPS_PROXY=http://proxy.corp.internal:3128",
		"NODE_EXTRA_CA_CERTS=/etc/ssl/corp-ca.pem",
		"GITHUB_TOKEN=ghp_secret", // must stay dropped even with extras set
	}
	got := ExtractEnv(in, []string{"HTTPS_PROXY", "NODE_EXTRA_CA_CERTS"})
	joined := strings.Join(got, "\n")

	for _, want := range []string{"PATH=/usr/bin", "HTTPS_PROXY=http://proxy.corp.internal:3128", "NODE_EXTRA_CA_CERTS=/etc/ssl/corp-ca.pem"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	if strings.Contains(joined, "GITHUB_TOKEN") {
		t.Errorf("extra_env_keys must not reopen the baseline exclusions: %v", got)
	}

	// nil extras behaves exactly like ExtractAllowlistEnv.
	if strings.Join(ExtractEnv(in, nil), "\n") != strings.Join(ExtractAllowlistEnv(in), "\n") {
		t.Error("ExtractEnv(in, nil) should match ExtractAllowlistEnv(in)")
	}
}

func TestCursorProviderDefaults(t *testing.T) {
	r := NewSubprocessRunner(ProviderCursor, "", nil, 0, nil)
	if r.Binary != "cursor-agent" {
		t.Fatalf("want cursor-agent binary, got %q", r.Binary)
	}
	if !slices.Contains(r.Args, "--trust") {
		t.Fatalf("want --trust in argv, got %v", r.Args)
	}
	// --force auto-approves tool calls; --trust only skips the workspace
	// prompt. Without --force a headless Fix gets userRejected on shell.
	if !slices.Contains(r.Args, "--force") {
		t.Fatalf("want --force in argv, got %v", r.Args)
	}
}

func TestGeminiProviderDefaults(t *testing.T) {
	r := NewSubprocessRunner(ProviderGemini, "", nil, 0, nil)
	if r.Binary != "gemini" {
		t.Fatalf("want gemini binary, got %q", r.Binary)
	}
	// --yolo auto-approves tool calls; without it the CLI blocks on a
	// TTY prompt that a headless Fix can never answer.
	if !slices.Contains(r.Args, "--yolo") {
		t.Fatalf("want --yolo in argv, got %v", r.Args)
	}
	if got := APIKeyEnvName(ProviderGemini); got != "GEMINI_API_KEY" {
		t.Fatalf("want GEMINI_API_KEY, got %s", got)
	}
	if !KnownProvider("gemini") || DefaultBinary(ProviderGemini) != "gemini" {
		t.Fatal("gemini missing from the provider table")
	}
}

func TestGeminiKeyReachesSubprocessEnv(t *testing.T) {
	env := ExtractAllowlistEnv([]string{"GEMINI_API_KEY=k", "GITHUB_TOKEN=nope"})
	if !slices.Contains(env, "GEMINI_API_KEY=k") {
		t.Fatalf("gemini key dropped by allowlist: %v", env)
	}
	if slices.Contains(env, "GITHUB_TOKEN=nope") {
		t.Fatal("GITHUB_TOKEN must never reach the coding agent")
	}
}

func TestWithModelAppendsFlag(t *testing.T) {
	base := NewSubprocessRunner(ProviderClaude, "", nil, 0, nil)
	got := base.WithModel("claude-opus-5")

	want := []string{"-p", promptPlaceholder, "--output-format", "json",
		"--dangerously-skip-permissions", "--model", "claude-opus-5"}
	if len(got.Args) != len(want) {
		t.Fatalf("Args = %v, want %v", got.Args, want)
	}
	for i := range want {
		if got.Args[i] != want[i] {
			t.Fatalf("Args = %v, want %v", got.Args, want)
		}
	}

	// The default runner must not have grown a --model of its own: the
	// clone shares a backing array with it until append copies.
	for _, a := range base.Args {
		if a == "--model" {
			t.Fatal("WithModel mutated the receiver's Args")
		}
	}
	if base.WithModel("") != base {
		t.Error("empty model should return the receiver unchanged")
	}
}

func TestWithModelIsPerProvider(t *testing.T) {
	for _, p := range Providers() {
		r := NewSubprocessRunner(p, "", nil, 0, nil).WithModel("m")
		last := r.Args[len(r.Args)-2:]
		if last[0] != "--model" || last[1] != "m" {
			t.Errorf("%s: tail = %v", p, last)
		}
	}
}

// The watchdog exists for the failure agent.timeout cannot describe: a
// process that is alive, has budget left, and has stopped working.
func TestRunKillsAStalledAgent(t *testing.T) {
	r := NewSubprocessRunner(ProviderClaude, "sh", []string{"-c", "echo starting; sleep 30"}, time.Minute, nil)
	r.StallTimeout = 500 * time.Millisecond

	start := time.Now()
	out, err := r.Run(context.Background(), t.TempDir(), "prompt", nil)
	if err == nil {
		t.Fatal("a silent agent must not report success")
	}
	if !errors.Is(err, ErrStalled) {
		t.Fatalf("want ErrStalled, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("watchdog waited for the run timeout instead of the stall timeout: %s", elapsed)
	}
	// Whatever the agent managed to say before going quiet is still the
	// operator's best clue, so the killed run keeps its output.
	if !strings.Contains(out, "starting") {
		t.Fatalf("output collected before the stall was dropped: %q", out)
	}
}

// A long Fix that keeps narrating is healthy, and killing it would be
// worse than the stall the watchdog is there for.
func TestRunLetsAChattyAgentFinish(t *testing.T) {
	script := "for i in 1 2 3 4 5 6; do echo working; sleep 0.2; done; echo done"
	r := NewSubprocessRunner(ProviderClaude, "sh", []string{"-c", script}, time.Minute, nil)
	r.StallTimeout = 600 * time.Millisecond

	out, err := r.Run(context.Background(), t.TempDir(), "prompt", nil)
	if err != nil {
		t.Fatalf("a streaming agent was killed: %v (%q)", err, out)
	}
	if !strings.Contains(out, "done") {
		t.Fatalf("run did not finish: %q", out)
	}
}

// Progress on stderr counts: several CLIs narrate there and print the
// result on stdout only at the end.
func TestRunTreatsStderrAsActivity(t *testing.T) {
	script := "for i in 1 2 3 4 5 6; do echo tool call >&2; sleep 0.2; done; echo '{\"ok\":true}'"
	r := NewSubprocessRunner(ProviderClaude, "sh", []string{"-c", script}, time.Minute, nil)
	r.StallTimeout = 600 * time.Millisecond

	out, err := r.Run(context.Background(), t.TempDir(), "prompt", nil)
	if err != nil {
		t.Fatalf("an agent narrating on stderr was killed as stalled: %v", err)
	}
	if !strings.Contains(out, `{"ok":true}`) {
		t.Fatalf("stdout lost: %q", out)
	}
}

func TestRunWithoutStallTimeoutHasNoWatchdog(t *testing.T) {
	r := NewSubprocessRunner(ProviderClaude, "sh", []string{"-c", "sleep 0.4; echo late"}, time.Minute, nil)
	out, err := r.Run(context.Background(), t.TempDir(), "prompt", nil)
	if err != nil {
		t.Fatalf("silent run without a watchdog must succeed: %v", err)
	}
	if !strings.Contains(out, "late") {
		t.Fatalf("output lost: %q", out)
	}
}

// A watchdog with nothing to watch would kill every healthy run, so
// opting in has to switch the buffered argv to a streaming one.
func TestWithStallTimeoutSwitchesToStreamingArgv(t *testing.T) {
	r := NewSubprocessRunner(ProviderClaude, "", nil, time.Minute, nil)
	got := r.WithStallTimeout(2 * time.Minute)

	if got.StallTimeout != 2*time.Minute {
		t.Fatalf("stall timeout not set: %s", got.StallTimeout)
	}
	argv := strings.Join(got.Args, " ")
	if !strings.Contains(argv, "--output-format stream-json") || !strings.Contains(argv, "--verbose") {
		t.Fatalf("claude argv not switched to streaming: %v", got.Args)
	}
	if strings.Contains(argv, "--output-format json") {
		t.Fatalf("buffered output format left on argv: %v", got.Args)
	}
	// The flags a Fix cannot work without must survive the rewrite.
	for _, want := range []string{"-p", "--dangerously-skip-permissions"} {
		if !strings.Contains(argv, want) {
			t.Fatalf("rewrite dropped %s: %v", want, got.Args)
		}
	}
	// The original is untouched, so a second provider built from the
	// same defaults is unaffected.
	if strings.Contains(strings.Join(r.Args, " "), "stream-json") {
		t.Fatalf("WithStallTimeout mutated the receiver: %v", r.Args)
	}
}

func TestWithStallTimeoutZeroChangesNothing(t *testing.T) {
	r := NewSubprocessRunner(ProviderClaude, "", nil, time.Minute, nil)
	if got := r.WithStallTimeout(0); got != r {
		t.Fatal("an unset stall timeout must pass the runner through unchanged")
	}
}

// The CLIs that already stream need no rewrite; touching their argv
// could only break an invocation that works. Cursor is not one of them:
// its default text mode prints at exit, so it gets stream-json too.
func TestWithStallTimeoutLeavesStreamingProvidersAlone(t *testing.T) {
	for _, p := range []Provider{ProviderCodex, ProviderGemini} {
		base := NewSubprocessRunner(p, "", nil, time.Minute, nil)
		got := base.WithStallTimeout(time.Minute)
		if strings.Join(got.Args, " ") != strings.Join(base.Args, " ") {
			t.Fatalf("%s argv rewritten: %v -> %v", p, base.Args, got.Args)
		}
		if got.StallTimeout != time.Minute {
			t.Fatalf("%s watchdog not enabled", p)
		}
	}
	cur := NewSubprocessRunner(ProviderCursor, "", nil, time.Minute, nil).WithStallTimeout(time.Minute)
	if !strings.HasSuffix(strings.Join(cur.Args, " "), "--output-format stream-json") {
		t.Fatalf("cursor must stream under the watchdog, got %v", cur.Args)
	}
}
