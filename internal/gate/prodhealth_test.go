package gate

import (
	"context"
	"testing"
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
