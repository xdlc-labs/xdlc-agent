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
