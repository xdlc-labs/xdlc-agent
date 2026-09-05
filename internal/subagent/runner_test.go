package subagent

import (
	"context"
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
		{ProviderCursor, "cursor-agent", []string{"-p", promptPlaceholder}},
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
	// "echo" as a stand-in CLI: Args puts the prompt where a real
	// provider's flag value would go, proving promptPlaceholder
	// substitution reaches argv correctly.
	r := NewSubprocessRunner(ProviderClaude, "echo", []string{promptPlaceholder}, time.Minute, nil)

	out, err := r.Run(context.Background(), t.TempDir(), "fix the failing test in svc-a", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "fix the failing test in svc-a") {
		t.Errorf("output = %q, want it to contain the prompt", out)
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
