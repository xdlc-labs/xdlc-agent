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
