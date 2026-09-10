package orchestrator

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

// The three memory maps drop entries older than memoryWindow on insert
// and keep everything younger, so a long-running daemon holds a day's
// worth of keys rather than every run and commit it ever saw.
func TestPruneStaleDropsOnlyOldEntries(t *testing.T) {
	now := time.Now()
	old := now.Add(-memoryWindow - time.Minute)
	fresh := now.Add(-memoryWindow + time.Minute)

	m := map[string]time.Time{"old": old, "fresh": fresh, "now": now}
	pruneStale(m, now, func(at time.Time) time.Time { return at }, nil)
	if _, ok := m["old"]; ok {
		t.Fatal("entry older than the window survived")
	}
	if len(m) != 2 {
		t.Fatalf("map = %v, want fresh and now", m)
	}
}

// An in-flight Fix is never pruned, however old its claim.
func TestPruneStaleKeepsInflightFix(t *testing.T) {
	now := time.Now()
	old := now.Add(-2 * memoryWindow)
	m := map[string]fixSHAState{
		"running": {inflight: true, at: old},
		"done":    {ok: true, at: old},
	}
	pruneStale(m, now, fixSHAStamp, fixSHAInflight)
	if _, ok := m["running"]; !ok {
		t.Fatal("in-flight claim was pruned")
	}
	if _, ok := m["done"]; ok {
		t.Fatal("day-old finished Fix was kept")
	}
}

// End to end through the Orchestrator's own inserts: a rerun and an
// own-push recorded a day ago are gone after the next insert, and a
// fresh one is remembered.
func TestOrchestratorMemoryMapsPruneOnInsert(t *testing.T) {
	o := New(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	stale := time.Now().Add(-memoryWindow - time.Hour)
	o.reran["https://gh/run/old"] = reranEntry{attempt: 2, at: stale}
	o.ownSHA[fixSHAKey("svc", "aaaaaaa")] = stale
	o.fixSHA[fixSHAKey("svc", "aaaaaaa")] = fixSHAState{ok: true, at: stale}

	o.RerunCI = func(_ context.Context, _ Signal) (bool, error) { return false, nil }
	sig := Signal{Source: SourceCI, Repo: "svc", Kind: KindFail, SHA: "bbbbbbb",
		Evidence: map[string]any{"run_url": "https://gh/run/new"}}
	if o.tryCIRerun(context.Background(), &sig) {
		t.Fatal("red rerun reported green")
	}
	if _, ok := o.reran["https://gh/run/old"]; ok {
		t.Fatal("day-old rerun entry survived an insert")
	}
	if _, ok := o.reran["https://gh/run/new"]; !ok {
		t.Fatal("new rerun entry not recorded")
	}

	if skip := o.claimFixSHA(sig); skip != "" {
		t.Fatalf("claim skipped: %s", skip)
	}
	if _, ok := o.fixSHA[fixSHAKey("svc", "aaaaaaa")]; ok {
		t.Fatal("day-old fixSHA entry survived a claim")
	}
	o.finishFixSHA(sig, FixResult{Delivered: true, PushedSHAs: []string{"ccccccc"}}, nil)
	if _, ok := o.ownSHA[fixSHAKey("svc", "aaaaaaa")]; ok {
		t.Fatal("day-old ownSHA entry survived an insert")
	}
	if _, ok := o.ownSHA[fixSHAKey("svc", "ccccccc")]; !ok {
		t.Fatal("pushed SHA not recorded as own")
	}
}
