package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

// runDoctor executes `doctor --skip-network` against body and returns
// its output plus whether it failed.
func runDoctor(t *testing.T, body string) (string, bool) {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// A PATH we control, so the verdict does not depend on whether the
	// machine running the test happens to have `argocd` installed.
	// doctor only LookPath's these, so a stub is enough; `git` doubles
	// as the agent CLI, as the other doctor tests do.
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "git"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("XDLC_API_TOKEN", "dev-token")
	t.Setenv("GITHUB_TOKEN", "ghp_test")

	cfgPath = cfg
	cmd := doctorCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--skip-network"})
	err := cmd.Execute()
	return buf.String(), err != nil
}

// argocdLine extracts the "argocd on PATH" verdict line.
func argocdLine(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "argocd on PATH") {
			return line
		}
	}
	t.Fatalf("no argocd check in doctor output:\n%s", out)
	return ""
}

// TestDoctorTreatsSharedAndPerRepoArgoCDAppIdentically is issue #45's
// third part. doctor read repos[].argocd_app only, while gatebuild and
// validate both fall back to gates.dev-smoke.argocd_app — so with
// `argocd` off PATH two semantically identical configs got opposite
// verdicts and doctor green-lit one whose dev-smoke gate cannot run.
func TestDoctorTreatsSharedAndPerRepoArgoCDAppIdentically(t *testing.T) {
	const perRepo = `
repos:
  - name: svc
    github: org/svc
    branch: develop
    gates: [dev-smoke]
    argocd_app: dev-svc
    probe_job: smoke-e2e
server:
  addr: "127.0.0.1:8080"
  require_webhook_secret: false
gates:
  dev-smoke:
    trigger: on_sync
agent:
  provider: claude
  binary: git
`
	const sharedDefault = `
repos:
  - name: svc
    github: org/svc
    branch: develop
    gates: [dev-smoke]
server:
  addr: "127.0.0.1:8080"
  require_webhook_secret: false
gates:
  dev-smoke:
    trigger: on_sync
    argocd_app: dev-svc
    probe_job: smoke-e2e
agent:
  provider: claude
  binary: git
`
	perRepoOut, perRepoFailed := runDoctor(t, perRepo)
	sharedOut, sharedFailed := runDoctor(t, sharedDefault)

	perRepoArgo := argocdLine(t, perRepoOut)
	sharedArgo := argocdLine(t, sharedOut)
	if perRepoArgo != sharedArgo {
		t.Fatalf("two semantically identical configs got different argocd verdicts:\n"+
			"per-repo:  %s\nshared:    %s", perRepoArgo, sharedArgo)
	}
	// Both must be a FAIL: argocd is not on the PATH the test built, and
	// the dev-smoke gate cannot run without it.
	if !strings.Contains(perRepoArgo, "FAIL") {
		t.Fatalf("argocd is off PATH but the check passed:\n%s", perRepoArgo)
	}
	if !perRepoFailed || !sharedFailed {
		t.Fatalf("doctor green-lit a config whose dev-smoke gate cannot run "+
			"(per-repo failed=%v, shared failed=%v)\n--- per-repo ---\n%s\n--- shared ---\n%s",
			perRepoFailed, sharedFailed, perRepoOut, sharedOut)
	}
}

// TestDoctorArgoCDNotRequiredWithoutAnApp: the check stays warn-style —
// a config with no resolved argocd_app at all must not demand the binary.
func TestDoctorArgoCDNotRequiredWithoutAnApp(t *testing.T) {
	const ciOnly = `
repos:
  - name: svc
    github: org/svc
    branch: develop
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
	out, failed := runDoctor(t, ciOnly)
	if line := argocdLine(t, out); strings.Contains(line, "FAIL") {
		t.Fatalf("argocd required by a ci-only config:\n%s", line)
	}
	if failed {
		t.Fatalf("ci-only config failed doctor:\n%s", out)
	}
}
