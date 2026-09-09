package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreRecordsArtifacts(t *testing.T) {
	st, err := Open(t.TempDir(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.Start(Meta{Repo: "example-service", Source: "ci", Kind: "fail", Provider: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID() == "" {
		t.Fatal("want a session id")
	}
	for name, body := range map[string]string{
		FilePrompt: "fix the build",
		FileOutput: "done",
		FileDiff:   "--- a\n+++ b\n",
	} {
		if err := sess.Write(name, body); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	sess.SetGit("aaa", "bbb", "develop", 1)
	sess.SetResult("ok", "", map[string]any{"total_cost_usd": 0.42})
	if err := sess.Finish(); err != nil {
		t.Fatal(err)
	}

	got, err := st.Load(sess.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got.Repo != "example-service" || got.Status != "ok" || got.HeadSHA != "bbb" {
		t.Fatalf("unexpected meta: %+v", got)
	}
	if got.DurationMS < 0 || got.EndedAt.IsZero() {
		t.Fatalf("Finish did not stamp the end: %+v", got)
	}
	body, err := st.ReadFile(sess.ID(), FilePrompt)
	if err != nil || body != "fix the build" {
		t.Fatalf("prompt readback: %q %v", body, err)
	}

	// Artifacts hold prompts built from CI logs: owner-only on disk.
	info, err := os.Stat(filepath.Join(sess.Dir(), FilePrompt))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("want 0600 artifact, got %o", perm)
	}
}

func TestDisabledStoreIsNoOp(t *testing.T) {
	st, err := Open("", 0, 0)
	if err != nil || st != nil {
		t.Fatalf("empty dir must disable recording: %v %v", st, err)
	}
	sess, err := st.Start(Meta{Repo: "x"})
	if err != nil || sess != nil {
		t.Fatalf("nil store must yield nil session: %v %v", sess, err)
	}
	// Every method on the nil session stays safe — callers do not branch.
	if err := sess.Write(FilePrompt, "ignored"); err != nil {
		t.Fatal(err)
	}
	sess.SetGit("a", "b", "c", 1)
	sess.SetResult("ok", "", nil)
	sess.SetPR("http://example.test/pr/1")
	if err := sess.Finish(); err != nil {
		t.Fatal(err)
	}
	if sess.ID() != "" || sess.Dir() != "" {
		t.Fatal("nil session must report empty id/dir")
	}
}

func TestListFiltersAndOrders(t *testing.T) {
	st, err := Open(t.TempDir(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()
	for i, repo := range []string{"alpha", "beta", "alpha"} {
		sess, err := st.Start(Meta{Repo: repo, StartedAt: base.Add(time.Duration(i) * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		if err := sess.Finish(); err != nil {
			t.Fatal(err)
		}
	}
	all, err := st.List("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("want 3 sessions, got %d", len(all))
	}
	if !all[0].StartedAt.After(all[1].StartedAt) {
		t.Fatal("want newest first")
	}
	alpha, err := st.List("alpha", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(alpha) != 2 {
		t.Fatalf("want 2 alpha sessions, got %d", len(alpha))
	}
	if got, err := st.List("", 1); err != nil || len(got) != 1 {
		t.Fatalf("limit ignored: %d %v", len(got), err)
	}
}

func TestStartCollidesWithinOneSecond(t *testing.T) {
	st, err := Open(t.TempDir(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	first, err := st.Start(Meta{Repo: "svc", StartedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.Start(Meta{Repo: "svc", StartedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID() == second.ID() {
		t.Fatalf("concurrent Fixes shared a session id: %s", first.ID())
	}
}

func TestPathRejectsTraversal(t *testing.T) {
	st, err := Open(t.TempDir(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "..", "../etc", "a/b", ".hidden"} {
		if _, err := st.Path(id); err == nil {
			t.Errorf("Path(%q) must be rejected", id)
		}
	}
}

func TestWriteTruncatesAtMaxFileBytes(t *testing.T) {
	st, err := Open(t.TempDir(), 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.Start(Meta{Repo: "svc"})
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Write(FileOutput, strings.Repeat("x", 500)); err != nil {
		t.Fatal(err)
	}
	body, err := st.ReadFile(sess.ID(), FileOutput)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "truncated by xdlc") {
		t.Fatalf("want truncation marker, got %d bytes", len(body))
	}
	if len(body) > 64+len(truncationMarker) {
		t.Fatalf("still too large: %d", len(body))
	}
}

func TestPruneDropsOldSessions(t *testing.T) {
	root := t.TempDir()
	st, err := Open(root, time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Start fires `go pruneAsync()`, which is rate-limited by lastPrune but
	// ungated on a fresh store, so that goroutine would race the explicit
	// Prune below for the stale directory and leave it with nothing to
	// remove. Claim the rate limit up front so this test owns the only
	// prune, and the returned count is the one being asserted.
	st.pruneMu.Lock()
	st.lastPrune = time.Now()
	st.pruneMu.Unlock()

	old, err := st.Start(Meta{Repo: "svc"})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := st.Start(Meta{Repo: "svc", StartedAt: time.Now().UTC().Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old.Dir(), stale, stale); err != nil {
		t.Fatal(err)
	}
	n, err := st.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want 1 pruned, got %d", n)
	}
	if _, err := st.Path(fresh.ID()); err != nil {
		t.Fatalf("fresh session was pruned: %v", err)
	}
}

func TestNewIDIsFilesystemSafe(t *testing.T) {
	id := NewID(time.Date(2026, 9, 5, 1, 5, 14, 0, time.UTC), "org/repo name")
	if strings.ContainsAny(id, "/ ") {
		t.Fatalf("unsafe id: %q", id)
	}
	if !strings.HasPrefix(id, "20260905T010514Z-") {
		t.Fatalf("want sortable timestamp prefix, got %q", id)
	}
}

// writeSession records one finished session, the way a Fix would.
func writeSession(t *testing.T, st *Store, repo, source, status, outcome, summary, diff string) string {
	t.Helper()
	sess, err := st.Start(Meta{Repo: repo, Source: source, Kind: "fail", Provider: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Write(FileDiff, diff); err != nil {
		t.Fatal(err)
	}
	sess.SetVerdict(outcome, summary, 1)
	sess.SetResult(status, "", nil)
	if err := sess.Finish(); err != nil {
		t.Fatal(err)
	}
	return sess.ID()
}

func TestRecentReturnsFinishedSessionsForRepoAndSource(t *testing.T) {
	st, err := Open(t.TempDir(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	old := writeSession(t, st, "svc", "ci", "error", "gave_up", "not fixable here", "--- a/one\n+++ b/one\n")
	recent := writeSession(t, st, "svc", "ci", "ok", "fixed", "bumped the pin", "--- a/two\n+++ b/two\n")
	writeSession(t, st, "svc", "prod", "ok", "fixed", "other source", "--- a/three\n")
	writeSession(t, st, "other", "ci", "ok", "fixed", "other repo", "--- a/four\n")

	got := st.Recent("svc", "ci", "", 5, 0)
	if len(got) != 2 {
		t.Fatalf("want the two ci sessions for svc, got %d: %+v", len(got), got)
	}
	// Newest first: the ladder cares most about what just happened.
	if got[0].ID != recent || got[1].ID != old {
		t.Fatalf("want %s then %s, got %s then %s", recent, old, got[0].ID, got[1].ID)
	}
	if got[0].Status != "ok" || got[0].Outcome != "fixed" || got[0].Summary != "bumped the pin" {
		t.Fatalf("verdict fields not carried: %+v", got[0])
	}
	if !strings.Contains(got[0].Diff, "b/two") {
		t.Fatalf("want the patch, got %q", got[0].Diff)
	}
	if got[0].DiffTruncated {
		t.Fatal("a two-line patch was reported as truncated")
	}
}

func TestRecentSkipsOwnAndUnfinishedSessions(t *testing.T) {
	st, err := Open(t.TempDir(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	done := writeSession(t, st, "svc", "ci", "ok", "fixed", "landed", "--- a/one\n")
	// A Fix running right now in another worktree: no status, no diff,
	// nothing a following run can learn from.
	if _, err := st.Start(Meta{Repo: "svc", Source: "ci"}); err != nil {
		t.Fatal(err)
	}
	mine, err := st.Start(Meta{Repo: "svc", Source: "ci"})
	if err != nil {
		t.Fatal(err)
	}

	got := st.Recent("svc", "ci", mine.ID(), 5, 0)
	if len(got) != 1 || got[0].ID != done {
		t.Fatalf("want only %s, got %+v", done, got)
	}
}

func TestRecentHonorsLimitAndDiffLines(t *testing.T) {
	st, err := Open(t.TempDir(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		writeSession(t, st, "svc", "ci", "ok", "fixed", "n", strings.Repeat("+line\n", 100))
	}
	got := st.Recent("svc", "ci", "", 2, 3)
	if len(got) != 2 {
		t.Fatalf("limit ignored: %d entries", len(got))
	}
	if lines := strings.Count(got[0].Diff, "\n") + 1; lines != 3 {
		t.Fatalf("want 3 diff lines, got %d", lines)
	}
	if !got[0].DiffTruncated {
		t.Fatal("a clipped patch must say so, or the agent reads a partial hunk as the whole change")
	}
}

func TestRecentDisabledAndEmpty(t *testing.T) {
	var nilStore *Store
	if got := nilStore.Recent("svc", "ci", "", 3, 0); got != nil {
		t.Fatalf("nil store must yield nothing, got %+v", got)
	}
	st, err := Open(t.TempDir(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Recent("svc", "ci", "", 0, 0); got != nil {
		t.Fatalf("limit 0 must yield nothing, got %+v", got)
	}
	if got := st.Recent("svc", "ci", "", 3, 0); len(got) != 0 {
		t.Fatalf("first Fix on a repo must yield nothing, got %+v", got)
	}
}

// A run that delivered no patch is still worth reporting: "the last
// attempt changed nothing" is exactly what stops a repeat.
func TestRecentKeepsSessionsWithNoDiff(t *testing.T) {
	st, err := Open(t.TempDir(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	id := writeSession(t, st, "svc", "ci", "error", "needs_human", "needs a decision", "")
	got := st.Recent("svc", "ci", "", 3, 0)
	if len(got) != 1 || got[0].ID != id || got[0].Diff != "" {
		t.Fatalf("want the empty-diff session, got %+v", got)
	}
}
