package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	yaml := `
repos:
  - name: example-service
    github: acme/example-service
    gates: [ci, dev-smoke, prod-health]
    argocd_app: dev-example-service
    probe_job: smoke-e2e
    thresholds:
      p95_ms: 800
      error_rate: 0.02

server:
  addr: ":9090"
  webhook_rate_per_sec: 15
  webhook_rate_burst: 30

gates:
  ci:
    trigger: on_push
  dev-smoke:
    trigger: on_sync
    namespace: dev
    interval: 45s
  prod-health:
    trigger: continuous
    metrics_url: http://prom.local
    thresholds:
      p95_ms: 500
      error_rate: 0.01
    interval: 30s
    p95_query: some_query
    error_rate_query: other_query

agent:
  mode: subprocess
  provider: codex
  timeout: 5m
  max_concurrent_fixes: 3
  fix_mode: pr

fleet:
  flap_max_cycles: 3
  flap_window: 1h
  circuit_breach_ratio: 0.3
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Repos) != 1 || cfg.Repos[0].Name != "example-service" {
		t.Fatalf("unexpected repos: %+v", cfg.Repos)
	}
	if cfg.Repos[0].GitHub != "acme/example-service" {
		t.Errorf("github = %q", cfg.Repos[0].GitHub)
	}
	if cfg.Repos[0].Thresholds == nil || cfg.Repos[0].Thresholds.P95MS != 800 {
		t.Errorf("repo thresholds = %+v", cfg.Repos[0].Thresholds)
	}
	if cfg.Server.Addr != ":9090" {
		t.Errorf("server.addr = %q", cfg.Server.Addr)
	}
	if cfg.Server.WebhookRatePerSec != 15 {
		t.Errorf("webhook_rate_per_sec = %v, want 15", cfg.Server.WebhookRatePerSec)
	}
	if cfg.Server.WebhookRateBurst != 30 {
		t.Errorf("webhook_rate_burst = %d, want 30", cfg.Server.WebhookRateBurst)
	}
	if cfg.Gates.DevSmoke.Interval != 45*time.Second {
		t.Errorf("dev-smoke.interval = %v", cfg.Gates.DevSmoke.Interval)
	}
	if cfg.Gates.ProdHealth.Thresholds.P95MS != 500 {
		t.Errorf("prod-health p95_ms = %v", cfg.Gates.ProdHealth.Thresholds.P95MS)
	}
	if cfg.Gates.ProdHealth.MetricsEndpoint() != "http://prom.local" {
		t.Errorf("metrics endpoint = %q", cfg.Gates.ProdHealth.MetricsEndpoint())
	}
	if cfg.Agent.Timeout != 5*time.Minute {
		t.Errorf("agent.timeout = %v", cfg.Agent.Timeout)
	}
	if cfg.Agent.Provider != "codex" {
		t.Errorf("agent.provider = %q, want codex", cfg.Agent.Provider)
	}
	if cfg.Agent.MaxConcurrentFixes != 3 {
		t.Errorf("max_concurrent_fixes = %d, want 3", cfg.Agent.MaxConcurrentFixes)
	}
	if cfg.Agent.FixMode != "pr" {
		t.Errorf("fix_mode = %q, want pr", cfg.Agent.FixMode)
	}
	if cfg.Fleet.FlapMaxCycles != 3 || cfg.Fleet.FlapWindow != time.Hour || cfg.Fleet.CircuitBreachRatio != 0.3 {
		t.Errorf("fleet = %+v", cfg.Fleet)
	}
}

func TestLoadDependsOn(t *testing.T) {
	yaml := `
repos:
  - name: api
    github: acme/api
  - name: web
    github: acme/web
    depends_on: [api]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Repos) != 2 || len(cfg.Repos[1].DependsOn) != 1 || cfg.Repos[1].DependsOn[0] != "api" {
		t.Fatalf("%+v", cfg.Repos)
	}
}
func TestMetricsEndpointLegacyAlias(t *testing.T) {
	yaml := `
repos: []
gates:
  prod-health:
    prometheus_url: http://legacy.prom
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Gates.ProdHealth.MetricsEndpoint(); got != "http://legacy.prom" {
		t.Errorf("MetricsEndpoint = %q, want legacy URL", got)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load("/nonexistent/config.yaml"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("rep:\n  - name: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for unknown top-level key")
	}
}
