package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDoctorMissingAgentBinaryFails(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	body := `
repos:
  - name: svc
    github: org/svc
    gates: [ci]
server:
  addr: "127.0.0.1:8080"
  require_webhook_secret: false
gates:
  ci:
    trigger: on_push
agent:
  provider: claude
  binary: xdlc-doctor-missing-binary-xyz
`
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDLC_API_TOKEN", "dev-token")
	t.Setenv("GITHUB_TOKEN", "ghp_test")

	cfgPath = cfg
	cmd := doctorCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--skip-network"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected failure, got output:\n%s", buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("FAIL")) {
		t.Fatalf("expected FAIL in output:\n%s", buf.String())
	}
}

func TestDoctorLocalDirSkipNetworkWarnsWithoutGitHub(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	body := `
repos:
  - name: svc
    github: org/svc
    dir: ` + dir + `
    gates: [ci]
server:
  addr: "127.0.0.1:8080"
  require_webhook_secret: false
gates:
  ci:
    trigger: on_push
agent:
  provider: claude
  binary: git
`
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDLC_API_TOKEN", "dev-token")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_APP_ID", "")

	cfgPath = cfg
	cmd := doctorCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--skip-network"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected pass with warn, got %v\n%s", err, buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("[warn] GitHub auth env")) {
		t.Fatalf("expected GitHub warn, got:\n%s", buf.String())
	}
	if bytes.Contains(buf.Bytes(), []byte("[FAIL] GitHub auth env")) {
		t.Fatalf("GitHub auth must not FAIL for local dir + --skip-network:\n%s", buf.String())
	}
}

func TestDoctorGitHubSlugWithoutDirFailsAuth(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	body := `
repos:
  - name: svc
    github: org/svc
    gates: [ci]
server:
  addr: "127.0.0.1:8080"
  require_webhook_secret: false
gates:
  ci:
    trigger: on_push
agent:
  provider: claude
  binary: git
`
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDLC_API_TOKEN", "dev-token")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_APP_ID", "")

	cfgPath = cfg
	cmd := doctorCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--skip-network"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected GitHub auth FAIL when github slug has no dir:\n%s", buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("[FAIL] GitHub auth env")) {
		t.Fatalf("expected FAIL GitHub auth env:\n%s", buf.String())
	}
}

// A non-loopback addr with require_webhook_secret: false is what the daemon
// refuses to start on (enforceWebhookSecrets), so doctor must FAIL it too —
// otherwise doctor says "all checks passed" and daemon then exits.
func TestDoctorNonLoopbackAddrWithoutWebhookSecretFails(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	body := `
repos:
  - name: svc
    github: org/svc
    dir: ` + dir + `
    gates: [ci]
server:
  addr: ":8080"
  github_webhook_secret_env: GITHUB_WEBHOOK_SECRET
  require_webhook_secret: false
gates:
  ci:
    trigger: on_push
agent:
  provider: claude
  binary: git
`
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDLC_API_TOKEN", "dev-token")
	t.Setenv("GITHUB_TOKEN", "ghp_test")

	cfgPath = cfg
	cmd := doctorCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--skip-network"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected FAIL for non-loopback addr without webhook secret:\n%s", buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("[FAIL] webhook secret for listen addr")) {
		t.Fatalf("expected FAIL webhook secret for listen addr:\n%s", buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("server.require_webhook_secret: true")) {
		t.Fatalf("expected remediation in detail:\n%s", buf.String())
	}
}

// Loopback with the flag false, and non-loopback with the flag true, are both
// startable — doctor must pass them.
func TestDoctorListenAddrCombinationsPass(t *testing.T) {
	cases := map[string]string{
		"loopback with flag false": `
server:
  addr: "127.0.0.1:8080"
  github_webhook_secret_env: GITHUB_WEBHOOK_SECRET
  require_webhook_secret: false
`,
		"non-loopback with flag true": `
server:
  addr: ":8080"
  github_webhook_secret_env: GITHUB_WEBHOOK_SECRET
  require_webhook_secret: true
`,
	}
	for name, serverBlock := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := filepath.Join(dir, "config.yaml")
			body := `
repos:
  - name: svc
    github: org/svc
    dir: ` + dir + `
    gates: [ci]
` + serverBlock + `
gates:
  ci:
    trigger: on_push
agent:
  provider: claude
  binary: git
`
			if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("XDLC_API_TOKEN", "dev-token")
			t.Setenv("GITHUB_TOKEN", "ghp_test")
			t.Setenv("GITHUB_WEBHOOK_SECRET", "whsec")

			cfgPath = cfg
			cmd := doctorCmd()
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetArgs([]string{"--skip-network"})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("expected pass, got %v:\n%s", err, buf.String())
			}
			if !bytes.Contains(buf.Bytes(), []byte("[ok] webhook secret for listen addr")) {
				t.Fatalf("expected ok webhook secret for listen addr:\n%s", buf.String())
			}
		})
	}
}
