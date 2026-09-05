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
