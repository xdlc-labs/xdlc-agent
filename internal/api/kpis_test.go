package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/store"
)

func TestBuildKPIs(t *testing.T) {
	now := time.Now().UTC()
	records := []store.Record{
		{At: now.Add(-3 * time.Hour), Repo: "a", Action: "fix", Evidence: map[string]any{"total_cost_usd": 0.02, "duration_ms": 1000.0}},
		{At: now.Add(-2 * time.Hour), Repo: "a", Action: "revert"},
		{At: now.Add(-1 * time.Hour), Repo: "a", Action: "fix", Evidence: map[string]any{"total_cost_usd": 0.01, "duration_ms": 2000.0}},
		{At: now, Repo: "b", Action: "fix", Evidence: map[string]any{"total_cost_usd": 0.05, "duration_ms": 500.0}},
		{At: now, Repo: "b", Action: "promote"},
	}
	k := buildKPIs(records)
	if k.Totals.Fixes != 3 || k.Totals.Reverts != 1 || k.Totals.Promotes != 1 {
		t.Fatalf("totals counts = %+v", k.Totals)
	}
	if k.Totals.TotalCostUSD < 0.079 || k.Totals.TotalCostUSD > 0.081 {
		t.Fatalf("cost = %v", k.Totals.TotalCostUSD)
	}
	if k.Totals.FixSuccessRate == nil || *k.Totals.FixSuccessRate < 0.66 || *k.Totals.FixSuccessRate > 0.67 {
		// 2 of 3 fixes not followed by revert before next fix: first fix→revert (fail), second ok, b ok → 2/3
		t.Fatalf("success = %v", k.Totals.FixSuccessRate)
	}
	if len(k.Repos) != 2 {
		t.Fatalf("repos = %+v", k.Repos)
	}
}

func TestBuildKPIsEmpty(t *testing.T) {
	k := buildKPIs(nil)
	if k.Totals.Fixes != 0 || k.Totals.FixSuccessRate != nil {
		t.Fatalf("%+v", k.Totals)
	}
	if k.Repos == nil {
		t.Fatal("repos should be empty slice not nil for JSON")
	}
}

func TestHandleKPIs(t *testing.T) {
	audit, err := store.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = audit.Close() })
	if err := audit.Append(store.Record{
		At: time.Now().UTC(), Repo: "svc", Action: "fix",
		Evidence: map[string]any{"total_cost_usd": 0.1, "duration_ms": 1200.0},
	}); err != nil {
		t.Fatal(err)
	}
	srv := &Server{Audit: audit, Started: time.Now(), Token: "op"}
	mux := http.NewServeMux()
	srv.Mount(mux)
	res := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/kpis", nil)
	req.Header.Set("Authorization", "Bearer op")
	mux.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("%d %s", res.Code, res.Body.String())
	}
	var body kpiResponse
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Totals.Fixes != 1 || body.Totals.TotalCostUSD != 0.1 {
		t.Fatalf("%+v", body.Totals)
	}
}
