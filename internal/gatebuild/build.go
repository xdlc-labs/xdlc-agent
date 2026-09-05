// Package gatebuild wires internal/config into concrete gate.Gate
// instances backed by real clients (GitHub, ArgoCD, kubectl, PromQL store).
// Shared by the daemon and the `gate check` CLI so both build gates the
// same way.
package gatebuild

import (
	"context"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
	"github.com/xdlc-labs/xdlc-agent/internal/gate"
	"github.com/xdlc-labs/xdlc-agent/internal/ghclient"
	"github.com/xdlc-labs/xdlc-agent/internal/gitops"
	"github.com/xdlc-labs/xdlc-agent/internal/k8sprobe"
	"github.com/xdlc-labs/xdlc-agent/internal/promclient"
)

// CI builds the one CIGate instance shared across all repos —
// GetStatus is parameterized by repo, so one client suffices.
//
// cfg supplies the per-repo branch: the gate is asked about a repo by
// its "owner/name" GitHub identifier, and each repo's own
// config.Repo.Branch decides which branch's workflow runs answer, so a
// repo on `branch: main` is not silently checked against a develop
// branch it doesn't have. cfg may be nil (defaults for every repo).
func CI(cfg *config.Config, tokens ghclient.TokenProvider) *gate.CIGate {
	gh := ghclient.NewFromProvider(tokens)
	gh.BranchFor = branchByGitHub(cfg)
	return &gate.CIGate{GetStatus: gh.GetStatus}
}

// branchByGitHub indexes cfg.Repos by GitHub full name → configured
// branch. Repos without an explicit branch are absent, so the client
// falls back to its own default.
func branchByGitHub(cfg *config.Config) func(string) string {
	if cfg == nil {
		return nil
	}
	byName := make(map[string]string, len(cfg.Repos))
	for _, r := range cfg.Repos {
		if r.GitHub != "" && r.Branch != "" {
			byName[r.GitHub] = r.Branch
		}
	}
	if len(byName) == 0 {
		return nil
	}
	return func(ownerRepo string) string { return byName[ownerRepo] }
}

// DevSmoke builds one SmokeGate per repo name, since each repo maps to
// its own ArgoCD Application + probe Job. cfg.Repos entries without an
// ArgoCDApp override fall back to the shared gates.dev-smoke defaults.
func DevSmoke(cfg *config.Config) map[string]*gate.SmokeGate {
	argo := gitops.NewArgoCDClient()
	k8s := k8sprobe.New()

	ns := cfg.Gates.DevSmoke.Namespace
	if ns == "" {
		ns = "dev"
	}

	gates := make(map[string]*gate.SmokeGate, len(cfg.Repos))
	for _, r := range cfg.Repos {
		app := r.ArgoCDApp
		if app == "" {
			app = cfg.Gates.DevSmoke.ArgoCDApp
		}
		job := r.ProbeJob
		if job == "" {
			job = cfg.Gates.DevSmoke.ProbeJob
		}
		if app == "" || job == "" {
			continue // repo has no dev-smoke config, skip
		}

		gates[r.Name] = &gate.SmokeGate{
			ArgoCDApp:  app,
			ProbeJob:   job,
			AppHealthy: argo.AppHealthy,
			ProbeResult: func(ctx context.Context, job string) (bool, string, error) {
				return k8s.JobSucceeded(ctx, ns, job)
			},
		}
	}
	return gates
}

// ProdHealth builds one ProdHealthGate shared across repos. Org-wide
// gates.prod-health.thresholds are the default; repos with
// Repo.Thresholds get a per-Check override via RepoThresholds.
func ProdHealth(cfg *config.Config) *gate.ProdHealthGate {
	prom := promclient.New(cfg.Gates.ProdHealth.MetricsEndpoint())
	byRepo := make(map[string]gate.RepoThresholds)
	for _, r := range cfg.Repos {
		if r.Thresholds == nil {
			continue
		}
		byRepo[r.Name] = gate.RepoThresholds{
			P95MS:     r.Thresholds.P95MS,
			ErrorRate: r.Thresholds.ErrorRate,
		}
	}
	return &gate.ProdHealthGate{
		P95ThresholdMS:  cfg.Gates.ProdHealth.Thresholds.P95MS,
		ErrorRateThresh: cfg.Gates.ProdHealth.Thresholds.ErrorRate,
		RepoThresholds:  byRepo,
		P95Query:        cfg.Gates.ProdHealth.P95Query,
		ErrorRateQuery:  cfg.Gates.ProdHealth.ErrorRateQuery,
		Query:           prom.Query,
	}
}

// External builds plugin-shaped ExternalGate instances from config.
func External(cfg *config.Config) []*gate.ExternalGate {
	var out []*gate.ExternalGate
	for _, eg := range cfg.Gates.External {
		if eg.Name == "" || len(eg.Command) == 0 {
			continue
		}
		trig := gate.Continuous
		switch eg.Trigger {
		case "on_push":
			trig = gate.OnPush
		case "on_sync":
			trig = gate.OnSync
		case "continuous", "":
			trig = gate.Continuous
		}
		out = append(out, &gate.ExternalGate{
			GateName: eg.Name,
			Argv:     append([]string(nil), eg.Command...),
			Trig:     trig,
			Timeout:  eg.Timeout,
		})
	}
	return out
}

// ExternalRepos returns which repos an external gate applies to.
func ExternalRepos(cfg *config.Config, eg config.ExternalGateConfig) []string {
	if len(eg.Repos) > 0 {
		return append([]string(nil), eg.Repos...)
	}
	var all []string
	for _, r := range cfg.Repos {
		all = append(all, r.Name)
	}
	return all
}

// ReposForGate returns the names of repos that list gateName in their
// gates: list.
func ReposForGate(cfg *config.Config, gateName string) []string {
	var out []string
	for _, r := range cfg.Repos {
		for _, g := range r.Gates {
			if g == gateName {
				out = append(out, r.Name)
				break
			}
		}
	}
	return out
}
