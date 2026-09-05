package gate

import (
	"context"
	"fmt"
)

// CIGate checks the latest GitHub Actions workflow_run status for a repo.
// Fires OnPush. Backed by go-github; wired up in cmd/xdlc-agent.
type CIGate struct {
	// GetStatus fetches the latest workflow_run conclusion for repo
	// (e.g. "success", "failure"). Injected so it can be a real
	// go-github client in production and a fake in tests.
	GetStatus func(ctx context.Context, repo string) (conclusion string, runURL string, err error)
}

// Name implements Gate.
func (g *CIGate) Name() string { return "ci" }

// Trigger implements Gate.
func (g *CIGate) Trigger() TriggerKind { return OnPush }

// Check implements Gate: repo must be "owner/name" (GetStatus's
// contract), not a config.yaml short name.
func (g *CIGate) Check(ctx context.Context, repo string) (Result, error) {
	conclusion, runURL, err := g.GetStatus(ctx, repo)
	if err != nil {
		return Result{}, fmt.Errorf("ci gate: %w", err)
	}

	status := StatusFail
	if conclusion == "success" {
		status = StatusPass
	}

	return Result{
		Status: status,
		Evidence: map[string]any{
			"conclusion": conclusion,
			"run_url":    runURL,
		},
	}, nil
}
