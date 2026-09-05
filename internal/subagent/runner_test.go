package subagent

import (
	"context"
	"os"
	"os/exec"
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
		{ProviderClaude, "claude", []string{"-p", promptPlaceholder, "--output-format", "json"}},
		{ProviderCodex, "codex", []string{"exec", promptPlaceholder}},
		{ProviderCursor, "cursor-agent", []string{"-p", "--trust", promptPlaceholder}},
		{"some-unknown-future-provider", "claude", []string{"-p", promptPlaceholder, "--output-format", "json"}},
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
