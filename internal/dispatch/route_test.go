package dispatch

import (
	"testing"

	"github.com/xdlc-labs/xdlc-agent/internal/store"
)

func TestPickProviderCheapest(t *testing.T) {
	got := PickProvider("cheapest", "claude", []string{"claude", "codex"}, 0.5, nil)
	if got != "codex" {
		t.Fatalf("want codex (cheaper), got %s", got)
	}
	stats := map[string]ProviderStats{
		"codex":  {Fixes: 10, Success: 2},
		"claude": {Fixes: 10, Success: 8},
	}
	got = PickProvider("cheapest", "claude", []string{"claude", "codex"}, 0.5, stats)
	if got != "claude" {
		t.Fatalf("codex below threshold, want claude, got %s", got)
	}
	if PickProvider("static", "claude", []string{"codex"}, 0, nil) != "claude" {
		t.Fatal("static should use fallback")
	}
}

func TestStatsFromActions(t *testing.T) {
	st := StatsFromActions([]string{"fix", "revert", "fix"}, []string{"claude"}, "claude")
	if st["claude"].Fixes != 2 || st["claude"].Success != 1 {
		t.Fatalf("%+v", st["claude"])
	}
}

func TestStatsFromRecordsPerProvider(t *testing.T) {
	recs := []store.Record{
		{Action: "fix", Status: store.StatusOK, AgentProvider: "codex"},
		{Action: "fix", Status: store.StatusError, AgentProvider: "codex", Error: "timeout"},
		{Action: "fix", Status: store.StatusOK, AgentProvider: "claude"},
		{Action: "revert", Status: store.StatusOK},
	}
	st := StatsFromRecords(recs, []string{"claude", "codex"}, "claude")
	if st["codex"].Fixes != 1 || st["codex"].Success != 1 {
		t.Fatalf("codex %+v (failed dispatch must not count)", st["codex"])
	}
	if st["claude"].Fixes != 1 || st["claude"].Success != 0 {
		t.Fatalf("claude %+v (followed by revert)", st["claude"])
	}
}
