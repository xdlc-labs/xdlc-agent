package gate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/xdlc-labs/xdlc-agent/internal/promclient"
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

	// Query runs a PromQL query and returns the scalar result. An error
	// — including promclient.ErrNoData for a query that matched no
	// series — makes Check return no verdict at all, which the runners
	// turn into an orchestrator.Blocked signal.
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
		return Result{}, queryErr("p95_query", err)
	}
	errRate, err := g.Query(ctx, errQ)
	if err != nil {
		return Result{}, queryErr("error_rate_query", err)
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

// queryErr wraps a failed metrics query, naming the config key that
// produced it so an operator reading a BACKLOG.md line, an audit row or
// the console Activity feed knows which of the two queries to go and
// look at.
//
// A query that matched no series gets its own wording, because the
// operator's next move is different: nothing needs to be wrong with the
// service for this to happen, and the thing to inspect is the query or
// the exporter behind it. Either way it is an error and not a verdict,
// so the runner emits orchestrator.Blocked (escalate=gate_unavailable,
// mapped to a noop) rather than a pass. Reporting a pass here is the
// issue #48 bug: no data used to arrive as p95=0 / error_rate=0, which
// this gate reads as healthy, silently disabling breach detection and
// clearing any breach already live.
func queryErr(key string, err error) error {
	if errors.Is(err, promclient.ErrNoData) {
		return fmt.Errorf("prod-health gate: %s matched no series, so prod health is unknown, not healthy: "+
			"check the metric name, its exporter, and any relabelling before trusting this gate again: %w", key, err)
	}
	return fmt.Errorf("prod-health gate: %s: %w", key, err)
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
