package gate

import (
	"context"
	"fmt"
)

// SmokeGate is "the gate" — DEV smoke/e2e probes run after ArgoCD syncs
// develop into the DEV namespace. Breaks here so prod doesn't.
// Fires OnSync.
type SmokeGate struct {
	ArgoCDApp string
	ProbeJob  string

	// AppHealthy reports whether the ArgoCD Application is Synced+Healthy.
	AppHealthy func(ctx context.Context, app string) (bool, error)

	// ProbeResult reports whether the probe Job (k6/Playwright) exited 0.
	ProbeResult func(ctx context.Context, job string) (passed bool, logs string, err error)
}

// Name implements Gate.
func (g *SmokeGate) Name() string { return "dev-smoke" }

// Trigger implements Gate.
func (g *SmokeGate) Trigger() TriggerKind { return OnSync }

// Check implements Gate. repo is unused — a SmokeGate is already scoped
// to one repo's ArgoCDApp/ProbeJob at construction (see
// internal/gatebuild.DevSmoke, one instance per repo).
func (g *SmokeGate) Check(ctx context.Context, repo string) (Result, error) {
	healthy, err := g.AppHealthy(ctx, g.ArgoCDApp)
	if err != nil {
		return Result{}, fmt.Errorf("smoke gate: argocd health: %w", err)
	}
	if !healthy {
		return Result{Status: StatusFail, Evidence: map[string]any{"reason": "argocd app not synced/healthy"}}, nil
	}

	passed, logs, err := g.ProbeResult(ctx, g.ProbeJob)
	if err != nil {
		return Result{}, fmt.Errorf("smoke gate: probe: %w", err)
	}

	status := StatusFail
	if passed {
		status = StatusPass
	}
	return Result{
		Status: status,
		Evidence: map[string]any{
			"probe_job": g.ProbeJob,
			"logs":      logs,
		},
	}, nil
}
