package gatebuild

import (
	"testing"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
	"github.com/xdlc-labs/xdlc-agent/internal/ghclient"
)

func TestReposForGate(t *testing.T) {
	cfg := &config.Config{Repos: []config.Repo{
		{Name: "a", Gates: []string{"ci", "dev-smoke"}},
		{Name: "b", Gates: []string{"ci"}},
		{Name: "c", Gates: []string{"prod-health"}},
	}}
	got := ReposForGate(cfg, "ci")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v", got)
	}
	if got := ReposForGate(cfg, "missing"); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestDevSmoke_OverridesSkipDefaultNS(t *testing.T) {
	cfg := &config.Config{
		Repos: []config.Repo{
			{Name: "ok", ArgoCDApp: "app-a", ProbeJob: "job-a"},
			{Name: "skip"},                           // no override; shared defaults empty → skip
			{Name: "partial", ArgoCDApp: "only-app"}, // missing job → skip
		},
		Gates: config.GatesConfig{
			DevSmoke: config.DevSmokeGateConfig{}, // Namespace empty → "dev"
		},
	}

	gates := DevSmoke(cfg)
	if len(gates) != 1 {
		t.Fatalf("len = %d, keys %v", len(gates), gates)
	}
	g := gates["ok"]
	if g == nil || g.ArgoCDApp != "app-a" || g.ProbeJob != "job-a" {
		t.Fatalf("gate = %+v", g)
	}
	if g.AppHealthy == nil || g.ProbeResult == nil {
		t.Fatal("callbacks nil")
	}
}

func TestDevSmoke_UsesSharedDefaults(t *testing.T) {
	cfg := &config.Config{
		Repos: []config.Repo{{Name: "svc"}},
		Gates: config.GatesConfig{DevSmoke: config.DevSmokeGateConfig{
			ArgoCDApp: "shared-app",
			ProbeJob:  "shared-job",
			Namespace: "staging",
		}},
	}
	gates := DevSmoke(cfg)
	g := gates["svc"]
	if g == nil || g.ArgoCDApp != "shared-app" || g.ProbeJob != "shared-job" {
		t.Fatalf("gate = %+v", g)
	}
}

func TestProdHealth(t *testing.T) {
	cfg := &config.Config{Gates: config.GatesConfig{ProdHealth: config.ProdHealthGateConfig{
		MetricsURL:     "http://prom.example",
		Thresholds:     config.Thresholds{P95MS: 100, ErrorRate: 0.05},
		P95Query:       "p95",
		ErrorRateQuery: "err",
	}}}
	g := ProdHealth(cfg)
	if g.P95ThresholdMS != 100 || g.ErrorRateThresh != 0.05 {
		t.Fatalf("thresholds %+v", g)
	}
	if g.P95Query != "p95" || g.ErrorRateQuery != "err" || g.Query == nil {
		t.Fatalf("queries/callback %+v", g)
	}
}

func TestCI(t *testing.T) {
	g := CI(nil, ghclient.EmptyToken{})
	if g.Name() != "ci" || g.GetStatus == nil {
		t.Fatalf("%+v", g)
	}
}

// TestBranchByGitHub is the C2 wiring: the CI gate is asked about a repo
// by GitHub full name, so that is the key the per-repo branch has to be
// resolvable by — otherwise a repo on `branch: main` is checked against
// a develop branch it doesn't have and never produces a CI signal.
func TestBranchByGitHub(t *testing.T) {
	if got := branchByGitHub(nil); got != nil {
		t.Error("nil config should not produce a resolver")
	}
	if got := branchByGitHub(&config.Config{Repos: []config.Repo{{Name: "a", GitHub: "org/a"}}}); got != nil {
		t.Error("no repo sets branch: expected no resolver, so the client default applies")
	}

	resolve := branchByGitHub(&config.Config{Repos: []config.Repo{
		{Name: "trunk", GitHub: "org/trunk", Branch: "main"},
		{Name: "default", GitHub: "org/default"},
	}})
	if resolve == nil {
		t.Fatal("expected a resolver")
	}
	if got := resolve("org/trunk"); got != "main" {
		t.Errorf("org/trunk = %q, want main", got)
	}
	// Unset and unknown both fall through to the client's own default.
	if got := resolve("org/default"); got != "" {
		t.Errorf("org/default = %q, want \"\"", got)
	}
	if got := resolve("org/never-heard-of-it"); got != "" {
		t.Errorf("unknown repo = %q, want \"\"", got)
	}
}
