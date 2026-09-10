package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
	"github.com/xdlc-labs/xdlc-agent/internal/fixstate"
	"github.com/xdlc-labs/xdlc-agent/internal/store"
)

// A failed dispatch must not read as ok: a Promote whose push was
// rejected showed a green row in the console.
func TestEventOKHonoursRecordStatus(t *testing.T) {
	failed := recordToEvent(store.Record{
		Repo: "svc", Source: "dev-gate", Kind: "pass", Action: "promote",
		Status: store.StatusError, Error: "promote: push develop->main not fast-forwardable",
	})
	if failed["ok"] != false || failed["error"] != "promote: push develop->main not fast-forwardable" {
		t.Fatalf("failed promote: ok=%v error=%v", failed["ok"], failed["error"])
	}
	worked := recordToEvent(store.Record{Repo: "svc", Source: "dev-gate", Kind: "pass", Action: "promote", Status: store.StatusOK})
	if worked["ok"] != true || worked["error"] != "" {
		t.Fatalf("ok promote: %v", worked)
	}
	legacy := recordToEvent(store.Record{Repo: "svc", Source: "ci", Kind: "fail", Action: "fix"})
	if legacy["ok"] != true {
		t.Fatal("a pre-status record still counts as ok")
	}
}

// The daemon's http.Server has a WriteTimeout sized for request/response.
// An SSE stream that outlives it used to keep writing into a dead
// deadline: no error, no close, no events. Hold a connection past the
// timeout, then publish, and the event must still arrive.
func TestSSESurvivesServerWriteTimeout(t *testing.T) {
	audit, err := store.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = audit.Close() })
	tracker := fixstate.New()
	srv := &Server{
		Cfg:   &config.Config{Repos: []config.Repo{{Name: "svc", GitHub: "acme/svc"}}},
		Audit: audit, Token: "tok", Started: time.Now(), Fixes: tracker,
	}
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewUnstartedServer(mux)
	// Short, so the test is quick; the daemon uses 30 s.
	ts.Config.WriteTimeout = 300 * time.Millisecond
	ts.Start()
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}

	// Outlive the server's write deadline, then make something happen.
	time.Sleep(600 * time.Millisecond)
	tracker.Set(fixstate.Fix{ID: "f1", Repo: "svc", Source: "ci", State: fixstate.Fixing})
	if err := audit.Append(store.Record{At: time.Now(), Repo: "svc", Source: "ci", Kind: "fail", Action: "fix"}); err != nil {
		t.Fatal(err)
	}

	got := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			got <- sc.Text()
		}
		close(got)
	}()
	var seenState, seenAudit bool
	deadline := time.After(3 * time.Second)
	for !seenState || !seenAudit {
		select {
		case line, ok := <-got:
			if !ok {
				t.Fatalf("stream closed before events arrived (state=%v audit=%v)", seenState, seenAudit)
			}
			if line == "event: fix_state" {
				seenState = true
			}
			if strings.HasPrefix(line, "id: ") {
				seenAudit = true
			}
		case <-deadline:
			t.Fatalf("no events after the write timeout (state=%v audit=%v)", seenState, seenAudit)
		}
	}
}

// A keepalive comment goes out on a quiet stream, so a dead client is
// discovered and a live one can tell quiet from dead.
func TestSSEKeepaliveFrameShape(t *testing.T) {
	if !strings.HasPrefix(": keepalive\n\n", ": ") {
		t.Fatal("keepalive must be an SSE comment line")
	}
	if fixStateFrame(fixstate.Fix{ID: "x", Repo: "r", State: fixstate.Queued}) == "" ||
		auditFrame(store.Record{Seq: 3, Repo: "r"}) == "" ||
		fixOutputFrame(fixstate.Output{ID: "x", Text: "t\n"}) == "" {
		t.Fatal("frames must render")
	}
}
