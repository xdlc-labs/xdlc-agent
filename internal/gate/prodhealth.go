package gate

import (
	"context"
	"fmt"
	"strings"
)

// RepoThresholds is an optional per-repo override of the gate's default
// p95 / error-rate limits.
type RepoThresholds struct {
	P95MS     float64
	ErrorRate float64
}

// ProdHealthGate polls a PromQL-compatible store continuously for p95
// latency and error rate against configured thresholds. Real users,
// rollback-first: a breach here is what drives an auto-revert decision
// upstream.
//
// Queries may contain the literal "{{repo}}" placeholder, replaced with
// the config short name on each Check so multi-service installs get
// per-service SLOs from one query pair. RepoThresholds (when set for a
// repo) override P95ThresholdMS / ErrorRateThresh for that Check.
type ProdHealthGate struct {
	P95ThresholdMS  float64
	ErrorRateThresh float64
	// RepoThresholds maps repo short name → override; missing entries
	// use P95ThresholdMS / ErrorRateThresh.
	RepoThresholds map[string]RepoThresholds

	// Query runs a PromQL query and returns the scalar result.
	Query func(ctx context.Context, promQL string) (float64, error)

	P95Query       string
	ErrorRateQuery string
}

// Name implements Gate.
func (g *ProdHealthGate) Name() string { return "prod-health" }

// Trigger implements Gate.
func (g *ProdHealthGate) Trigger() TriggerKind { return Continuous }

// Check implements Gate.
func (g *ProdHealthGate) Check(ctx context.Context, repo string) (Result, error) {
	p95Q := expandRepo(g.P95Query, repo)
	errQ := expandRepo(g.ErrorRateQuery, repo)

	p95Thresh, errThresh := g.thresholdsFor(repo)

	p95, err := g.Query(ctx, p95Q)
	if err != nil {
		return Result{}, fmt.Errorf("prod-health gate: p95 query: %w", err)
	}
	errRate, err := g.Query(ctx, errQ)
	if err != nil {
		return Result{}, fmt.Errorf("prod-health gate: error-rate query: %w", err)
	}

	status := StatusPass
	if p95 > p95Thresh || errRate > errThresh {
		status = StatusFail
	}

	return Result{
		Status: status,
		Evidence: map[string]any{
			"repo":              repo,
			"p95_ms":            p95,
			"error_rate":        errRate,
			"p95_threshold_ms":  p95Thresh,
			"error_rate_thresh": errThresh,
			"p95_query":         p95Q,
			"error_rate_query":  errQ,
		},
	}, nil
}

func (g *ProdHealthGate) thresholdsFor(repo string) (p95MS, errRate float64) {
	if t, ok := g.RepoThresholds[repo]; ok {
		return t.P95MS, t.ErrorRate
	}
	return g.P95ThresholdMS, g.ErrorRateThresh
}

func expandRepo(q, repo string) string {
	return strings.ReplaceAll(q, "{{repo}}", repo)
}
