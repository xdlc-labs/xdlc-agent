package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
	"github.com/xdlc-labs/xdlc-agent/internal/session"
	"github.com/xdlc-labs/xdlc-agent/internal/store"
)

// newSessionServer builds an API server over a session store holding one
// finished recording. It returns the mux and the recording's id.
func newSessionServer(t *testing.T, st *session.Store) (*http.ServeMux, string) {
	t.Helper()
	audit, err := store.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = audit.Close() })

	id := ""
	if st != nil {
		sess, err := st.Start(session.Meta{Repo: "svc", Source: "ci", Kind: "fail", Provider: "claude"})
		if err != nil {
			t.Fatal(err)
		}
		id = sess.ID()
		must(t, sess.Write(session.FilePrompt, "FIX THIS\nsecret=hunter2\n"))
		must(t, sess.Write(session.FileOutput, strings.Repeat("line\n", 100)+"done\n"))
		must(t, sess.Write(session.FileDiff, "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n"))
		sess.SetVerdict("fixed", "changed x", 1)
		sess.SetResult("ok", "", nil)
		must(t, sess.Finish())
	}

	srv := &Server{
		Cfg:         &config.Config{Repos: []config.Repo{{Name: "svc", GitHub: "acme/svc", Gates: []string{"ci"}}}},
		Audit:       audit,
		Version:     "test",
		Started:     time.Now(),
		Token:       "op-token",
		ViewerToken: "view-token",
		Sessions:    st,
	}
	mux := http.NewServeMux()
	srv.Mount(mux)
	return mux, id
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func get(mux *http.ServeMux, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	return res
}

func openSessionStore(t *testing.T) *session.Store {
	t.Helper()
	st, err := session.Open(filepath.Join(t.TempDir(), "sessions"), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// Recordings are unscrubbed, so the viewer role must never reach them —
// not the list, not a file. Missing token is 401 like every other route.
func TestSessionsOperatorOnly(t *testing.T) {
	mux, id := newSessionServer(t, openSessionStore(t))
	for _, path := range []string{
		"/api/sessions",
		"/api/sessions/" + id,
		"/api/sessions/" + id + "/diff",
		"/api/sessions/" + id + "/prompt",
		"/api/sessions/" + id + "/output",
	} {
		if res := get(mux, path, "view-token"); res.Code != http.StatusForbidden {
			t.Errorf("%s viewer: want 403, got %d %s", path, res.Code, res.Body.String())
		}
		if res := get(mux, path, ""); res.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous: want 401, got %d", path, res.Code)
		}
		if res := get(mux, path, "op-token"); res.Code != http.StatusOK {
			t.Errorf("%s operator: want 200, got %d %s", path, res.Code, res.Body.String())
		}
	}
}

func TestSessionsListAndDetail(t *testing.T) {
	mux, id := newSessionServer(t, openSessionStore(t))

	res := get(mux, "/api/sessions?repo=svc", "op-token")
	var list struct {
		Enabled  bool           `json:"enabled"`
		Sessions []session.Meta `json:"sessions"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if !list.Enabled || len(list.Sessions) != 1 || list.Sessions[0].ID != id {
		t.Fatalf("list=%+v", list)
	}
	if list.Sessions[0].Outcome != "fixed" || list.Sessions[0].Status != "ok" {
		t.Fatalf("meta not the finished one: %+v", list.Sessions[0])
	}
	if res := get(mux, "/api/sessions?repo=other", "op-token"); !strings.Contains(res.Body.String(), `"sessions": []`) {
		t.Fatalf("filter by unknown repo should be an empty list, got %s", res.Body.String())
	}

	res = get(mux, "/api/sessions/"+id, "op-token")
	var detail struct {
		Session    session.Meta `json:"session"`
		Files      []string     `json:"files"`
		OutputTail string       `json:"output_tail"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Session.ID != id {
		t.Fatalf("detail id=%q", detail.Session.ID)
	}
	want := []string{"diff.patch", "meta.json", "output.txt", "prompt.txt"}
	if strings.Join(detail.Files, ",") != strings.Join(want, ",") {
		t.Fatalf("files=%v", detail.Files)
	}
	lines := strings.Split(detail.OutputTail, "\n")
	if len(lines) != outputTailLines || lines[len(lines)-1] != "done" {
		t.Fatalf("tail: %d lines, last %q", len(lines), lines[len(lines)-1])
	}
}

func TestSessionFilesAreText(t *testing.T) {
	mux, id := newSessionServer(t, openSessionStore(t))

	res := get(mux, "/api/sessions/"+id+"/diff", "op-token")
	if ct := res.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("diff content-type %q", ct)
	}
	if !strings.HasPrefix(res.Body.String(), "--- a/x") {
		t.Fatalf("diff body %q", res.Body.String())
	}
	if res := get(mux, "/api/sessions/"+id+"/prompt", "op-token"); !strings.Contains(res.Body.String(), "secret=hunter2") {
		t.Fatalf("prompt is served unscrubbed by design; got %q", res.Body.String())
	}
	// An attempt that never ran is a 404, not an empty 200.
	if res := get(mux, "/api/sessions/"+id+"/prompt?attempt=2", "op-token"); res.Code != http.StatusNotFound {
		t.Fatalf("attempt 2: want 404, got %d", res.Code)
	}
}

// A session id is a directory name. Anything else must read as unknown,
// with the same message as a genuinely missing id.
func TestSessionIDCannotTraverse(t *testing.T) {
	mux, _ := newSessionServer(t, openSessionStore(t))
	// A literal ".." never reaches a handler: ServeMux cleans the path and
	// redirects (307) to a route that does not exist. The encoded form
	// does reach PathValue, decoded, and the store must refuse it.
	for _, id := range []string{".hidden", "nope-20260101T000000Z", "..%2F..%2Fetc", "%2E%2E"} {
		res := get(mux, "/api/sessions/"+id+"/prompt", "op-token")
		if res.Code != http.StatusNotFound {
			t.Errorf("id %q: want 404, got %d %s", id, res.Code, res.Body.String())
		}
	}
}

// Recording off: the list says so instead of pretending to be empty
// history, and every id is 404.
func TestSessionsDisabled(t *testing.T) {
	mux, _ := newSessionServer(t, nil)
	res := get(mux, "/api/sessions", "op-token")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"enabled": false`) {
		t.Fatalf("list with recording off: %d %s", res.Code, res.Body.String())
	}
	if res := get(mux, "/api/sessions/20260101T000000Z-svc", "op-token"); res.Code != http.StatusNotFound {
		t.Fatalf("detail with recording off: want 404, got %d", res.Code)
	}
}

// The timeline row needs the session id as its own field to link from.
func TestEventCarriesSessionID(t *testing.T) {
	ev := recordToEvent(store.Record{
		At: time.Now(), Repo: "svc", Source: "ci", Kind: "fail", Action: "fix",
		Evidence: map[string]any{"session_id": "20260101T000000Z-svc", "run_url": "https://x"},
	})
	if ev["session_id"] != "20260101T000000Z-svc" {
		t.Fatalf("session_id=%v", ev["session_id"])
	}
	if ev := recordToEvent(store.Record{Repo: "svc", Kind: "pass"}); ev["session_id"] != "" {
		t.Fatalf("gate signal should carry no session id, got %v", ev["session_id"])
	}
}

func TestTailLines(t *testing.T) {
	if got := tailLines("a\nb\nc\n", 2); got != "b\nc" {
		t.Fatalf("got %q", got)
	}
	if got := tailLines("a\nb", 5); got != "a\nb" {
		t.Fatalf("got %q", got)
	}
	if got := tailLines("", 5); got != "" {
		t.Fatalf("got %q", got)
	}
}
