package gate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/xdlc-labs/xdlc-agent/internal/promclient"
)

func TestProdHealthExpandsRepoPlaceholder(t *testing.T) {
	var got []string
	g := &ProdHealthGate{
		P95ThresholdMS:  500,
		ErrorRateThresh: 0.01,
		P95Query:        `p95{service="{{repo}}"}`,
		ErrorRateQuery:  `err{service="{{repo}}"}`,
		Query: func(_ context.Context, q string) (float64, error) {
			got = append(got, q)
			return 0.001, nil
		},
	}
	res, err := g.Check(context.Background(), "api")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusPass {
		t.Fatalf("status = %v", res.Status)
	}
	if len(got) != 2 || got[0] != `p95{service="api"}` || got[1] != `err{service="api"}` {
		t.Fatalf("queries = %v", got)
	}
}

func TestProdHealthPerRepoThresholds(t *testing.T) {
	// Same metrics for both repos; only thresholds differ.
	g := &ProdHealthGate{
		P95ThresholdMS:  500, // org-wide default
		ErrorRateThresh: 0.01,
		RepoThresholds: map[string]RepoThresholds{
			"noisy": {P95MS: 2000, ErrorRate: 0.05},
			"quiet": {P95MS: 100, ErrorRate: 0.001},
		},
		P95Query:       "p95",
		ErrorRateQuery: "err",
		Query: func(_ context.Context, q string) (float64, error) {
			if q == "p95" {
				return 300, nil
			}
			return 0.002, nil
		},
	}

	// noisy: 300 < 2000 and 0.002 < 0.05 → pass
	res, err := g.Check(context.Background(), "noisy")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusPass {
		t.Fatalf("noisy: status=%v evidence=%v", res.Status, res.Evidence)
	}
	if res.Evidence["p95_threshold_ms"] != 2000.0 {
		t.Fatalf("noisy threshold = %v", res.Evidence["p95_threshold_ms"])
	}

	// quiet: 300 > 100 → fail
	res, err = g.Check(context.Background(), "quiet")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFail {
		t.Fatalf("quiet: status=%v evidence=%v", res.Status, res.Evidence)
	}
	if res.Evidence["p95_threshold_ms"] != 100.0 {
		t.Fatalf("quiet threshold = %v", res.Evidence["p95_threshold_ms"])
	}

	// unset repo: org-wide 500 → 300 < 500 → pass
	res, err = g.Check(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusPass {
		t.Fatalf("other: status=%v", res.Status)
	}
	if res.Evidence["p95_threshold_ms"] != 500.0 {
		t.Fatalf("other threshold = %v", res.Evidence["p95_threshold_ms"])
	}
}

// noData is what promclient.Query returns for a query that matched no
// series: the sentinel, wrapped with the PromQL text.
func noData(promQL string) error {
	return fmt.Errorf("promclient: %w: %q", promclient.ErrNoData, promQL)
}

// TestProdHealthNoDataBlocksInsteadOfPassing is the issue #48 case. The
// gate fails only on value > threshold, so a query returning 0 always
// passed — and before this fix a vanished series (metric renamed,
// exporter down, relabelled away, typo in the query) arrived as 0. The
// gate reported healthy, and the poller's edge trigger then cleared any
// breach already live: breach detection silently switched off at the
// exact moment its inputs broke.
//
// Check must return an error instead, which the runners turn into an
// orchestrator.Blocked signal (escalate=gate_unavailable, ActionNoop) -
// unknown, neither pass nor fail.
func TestProdHealthNoDataBlocksInsteadOfPassing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		empty   string // the query that matched nothing
		wantKey string // config key the error must name
	}{
		{name: "p95", empty: "p95", wantKey: "p95_query"},
		{name: "error rate", empty: "err", wantKey: "error_rate_query"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &ProdHealthGate{
				P95ThresholdMS:  500,
				ErrorRateThresh: 0.01,
				P95Query:        "p95",
				ErrorRateQuery:  "err",
				Query: func(_ context.Context, q string) (float64, error) {
					if q == tc.empty {
						return 0, noData(q)
					}
					return 1, nil
				},
			}

			res, err := g.Check(context.Background(), "api")
			if err == nil {
				t.Fatalf("no data reported a verdict: status=%v evidence=%v", res.Status, res.Evidence)
			}
			if res.Status == StatusPass {
				t.Fatalf("no data returned StatusPass alongside its error")
			}
			if !errors.Is(err, promclient.ErrNoData) {
				t.Fatalf("error does not wrap promclient.ErrNoData: %v", err)
			}
			// The text reaches BACKLOG.md, an audit row and the console
			// Activity feed. An operator must be able to tell which of
			// the two queries broke, and that the service itself is not
			// being accused of anything.
			msg := err.Error()
			for _, want := range []string{tc.wantKey, "matched no series", "unknown, not healthy", "exporter"} {
				if !strings.Contains(msg, want) {
					t.Errorf("error text missing %q: %v", want, msg)
				}
			}
			if other := map[string]string{"p95_query": "error_rate_query", "error_rate_query": "p95_query"}[tc.wantKey]; strings.Contains(msg, other) {
				t.Errorf("error text blames %q as well as %q: %v", other, tc.wantKey, msg)
			}
		})
	}
}

// TestProdHealthGenuineZeroStillPasses: the counterpart to the above. A
// series that exists and reads 0 — p95 of 0, an error rate of 0 for a
// window with no errors — is real data and a real pass. The #48 fix
// must not turn the healthiest possible reading into a blocked gate.
func TestProdHealthGenuineZeroStillPasses(t *testing.T) {
	calls := 0
	g := &ProdHealthGate{
		P95ThresholdMS:  500,
		ErrorRateThresh: 0.01,
		P95Query:        "p95",
		ErrorRateQuery:  "err",
		Query: func(_ context.Context, _ string) (float64, error) {
			calls++
			return 0, nil // a series is present; its value is 0
		},
	}

	res, err := g.Check(context.Background(), "api")
	if err != nil {
		t.Fatalf("genuine zero blocked the gate: %v", err)
	}
	if res.Status != StatusPass {
		t.Fatalf("status = %v, want pass", res.Status)
	}
	if calls != 2 {
		t.Fatalf("queries run = %d, want 2", calls)
	}
	if res.Evidence["p95_ms"] != 0.0 || res.Evidence["error_rate"] != 0.0 {
		t.Fatalf("evidence = %v", res.Evidence)
	}
}

// TestProdHealthQueryErrorNamesQueryKey: a transport failure (the #42
// timeout case included) still errors, and still says which query it
// was, without borrowing the no-data wording.
func TestProdHealthQueryErrorNamesQueryKey(t *testing.T) {
	boom := errors.New("context deadline exceeded")
	g := &ProdHealthGate{
		P95ThresholdMS: 500,
		P95Query:       "p95",
		ErrorRateQuery: "err",
		Query: func(_ context.Context, _ string) (float64, error) {
			return 0, boom
		},
	}

	_, err := g.Check(context.Background(), "api")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
	if !strings.Contains(err.Error(), "p95_query") {
		t.Fatalf("error does not name the query: %v", err)
	}
	if strings.Contains(err.Error(), "matched no series") {
		t.Fatalf("transport failure reported as no data: %v", err)
	}
}
