package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
	"github.com/xdlc-labs/xdlc-agent/internal/orchestrator"
	"github.com/xdlc-labs/xdlc-agent/internal/store"
)

func TestOverviewAndHistory(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	audit, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = audit.Close() })

	if err := audit.Append(store.Record{
		At: time.Now().UTC(), Repo: "example-service", Source: "ci", Kind: "fail", Action: "fix",
		Evidence: map[string]any{"run_url": "https://example.com/1"},
	}); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Repos:  []config.Repo{{Name: "example-service", GitHub: "acme/example-service", Gates: []string{"ci"}}},
		Agent:  config.AgentConfig{Provider: "claude"},
		Server: config.ServerConfig{Addr: ":9090"},
	}
	srv := &Server{Cfg: cfg, CfgPath: "config.yaml", Audit: audit, Version: "test", Started: time.Now(), Token: "test-token"}
	mux := http.NewServeMux()
	srv.Mount(mux)

	res := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/overview", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	mux.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("overview status %d: %s", res.Code, res.Body.String())
	}
	var overview map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	daemon, _ := overview["daemon"].(map[string]any)
	if daemon["status"] != "running" {
		t.Fatalf("daemon=%v", daemon)
	}
	events, _ := overview["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("events=%v", events)
	}

	res2 := httptest.NewRecorder()
	req2 := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/health", nil)
	mux.ServeHTTP(res2, req2)
	if res2.Code != 200 {
		t.Fatalf("health %d", res2.Code)
	}
}

func TestFixPRWorkQueue(t *testing.T) {
	audit, err := store.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = audit.Close() })

	older := time.Now().Add(-time.Hour).UTC()
	newer := time.Now().UTC()
	records := []store.Record{
		{ // a real fix_mode: pr result — should appear
			At: newer, Repo: "svc-a", Source: "ci", Kind: "fail", Action: "fix",
			Evidence: map[string]any{"pr_number": 12, "pr_url": "https://github.com/org/svc-a/pull/12", "pr_state": "open", "pr_branch": "xdlc-fix-2"},
		},
		{ // an older record for the same repo+branch — must be superseded by the one above
			At: older, Repo: "svc-a", Source: "ci", Kind: "fail", Action: "fix",
			Evidence: map[string]any{"pr_number": 12, "pr_url": "https://github.com/org/svc-a/pull/12", "pr_state": "open", "pr_branch": "xdlc-fix-2"},
		},
		{ // fix_mode: direct (or "pr" that never got a PR) — no pr_url, must be excluded
			At: newer, Repo: "svc-b", Source: "ci", Kind: "fail", Action: "fix",
			Evidence: map[string]any{"run_url": "https://ci/1"},
		},
		{ // a revert — must be excluded even if it somehow carried a pr_url
			At: newer, Repo: "svc-c", Source: "prod-health", Kind: "breach", Action: "revert",
			Evidence: map[string]any{"pr_url": "https://github.com/org/svc-c/pull/1"},
		},
	}
	for _, r := range records {
		if err := audit.Append(r); err != nil {
			t.Fatal(err)
		}
	}

	srv := &Server{Cfg: &config.Config{}, Audit: audit, Started: time.Now(), Token: "op"}
	mux := http.NewServeMux()
	srv.Mount(mux)

	res := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/prs", nil)
	req.Header.Set("Authorization", "Bearer op")
	mux.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("status %d: %s", res.Code, res.Body.String())
	}

	var body struct {
		PRs []struct {
			Repo, Branch, URL, State string
			Number                   int
		} `json:"prs"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.PRs) != 1 {
		t.Fatalf("expected exactly 1 PR (deduped, direct/revert excluded), got %+v", body.PRs)
	}
	pr := body.PRs[0]
	if pr.Repo != "svc-a" || pr.Branch != "xdlc-fix-2" || pr.Number != 12 || pr.State != "open" {
		t.Fatalf("pr = %+v", pr)
	}
}

func TestFixPRLiveRecheck(t *testing.T) {
	audit, err := store.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = audit.Close() })
	if err := audit.Append(store.Record{
		At: time.Now().UTC(), Repo: "svc-a", Source: "ci", Kind: "fail", Action: "fix",
		Evidence: map[string]any{"pr_number": 12, "pr_url": "https://github.com/org/svc-a/pull/12", "pr_state": "open", "pr_branch": "xdlc-fix-2"},
	}); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{Repos: []config.Repo{{Name: "svc-a", GitHub: "org/svc-a"}}}
	srv := &Server{
		Cfg: cfg, Audit: audit, Started: time.Now(), Token: "op",
		PRStatus: func(_ context.Context, githubRepo string, number int) (PRLiveStatus, error) {
			if githubRepo != "org/svc-a" || number != 12 {
				t.Fatalf("lookup %s#%d", githubRepo, number)
			}
			return PRLiveStatus{State: "closed", Merged: true, Title: "fix boom", CI: "success"}, nil
		},
	}
	mux := http.NewServeMux()
	srv.Mount(mux)

	// Default: open-only → merged PR filtered out.
	res := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/prs", nil)
	req.Header.Set("Authorization", "Bearer op")
	mux.ServeHTTP(res, req)
	var body struct {
		PRs []map[string]any `json:"prs"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.PRs) != 0 {
		t.Fatalf("expected empty open queue, got %+v", body.PRs)
	}

	// ?all=1 keeps closed/merged with live state.
	res = httptest.NewRecorder()
	req = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/prs?all=1", nil)
	req.Header.Set("Authorization", "Bearer op")
	mux.ServeHTTP(res, req)
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.PRs) != 1 {
		t.Fatalf("all=1 got %+v", body.PRs)
	}
	if body.PRs[0]["state"] != "closed" || body.PRs[0]["merged"] != true {
		t.Fatalf("%+v", body.PRs[0])
	}
	if body.PRs[0]["title"] != "fix boom" || body.PRs[0]["ci"] != "success" {
		t.Fatalf("live fields: %+v", body.PRs[0])
	}
}

func TestFixPRLiveRecheckStaleOnError(t *testing.T) {
	audit, err := store.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = audit.Close() })
	if err := audit.Append(store.Record{
		At: time.Now().UTC(), Repo: "svc-a", Source: "ci", Kind: "fail", Action: "fix",
		Evidence: map[string]any{"pr_number": 3, "pr_url": "https://github.com/org/svc-a/pull/3", "pr_state": "open", "pr_branch": "xdlc-fix-1"},
	}); err != nil {
		t.Fatal(err)
	}
	srv := &Server{
		Cfg:   &config.Config{Repos: []config.Repo{{Name: "svc-a", GitHub: "org/svc-a"}}},
		Audit: audit, Started: time.Now(), Token: "op",
		PRStatus: func(context.Context, string, int) (PRLiveStatus, error) {
			return PRLiveStatus{}, fmt.Errorf("github down")
		},
	}
	mux := http.NewServeMux()
	srv.Mount(mux)
	res := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/prs", nil)
	req.Header.Set("Authorization", "Bearer op")
	mux.ServeHTTP(res, req)
	var body struct {
		PRs []struct {
			State string `json:"state"`
			Stale bool   `json:"stale"`
		} `json:"prs"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.PRs) != 1 || !body.PRs[0].Stale || body.PRs[0].State != "open" {
		t.Fatalf("%+v", body.PRs)
	}
}

func TestBearerAuth(t *testing.T) {
	cfg := &config.Config{Agent: config.AgentConfig{Provider: "claude"}}
	audit, err := store.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = audit.Close() })

	t.Run("health open without token", func(t *testing.T) {
		srv := &Server{Cfg: cfg, Audit: audit, Started: time.Now()}
		mux := http.NewServeMux()
		srv.Mount(mux)
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/health", nil))
		if res.Code != 200 {
			t.Fatalf("health %d", res.Code)
		}
	})

	t.Run("unset token returns 503", func(t *testing.T) {
		srv := &Server{Cfg: cfg, Audit: audit, Started: time.Now()}
		mux := http.NewServeMux()
		srv.Mount(mux)
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/overview", nil))
		if res.Code != http.StatusServiceUnavailable {
			t.Fatalf("status %d, want 503", res.Code)
		}
	})

	t.Run("wrong bearer returns 401", func(t *testing.T) {
		srv := &Server{Cfg: cfg, Audit: audit, Started: time.Now(), Token: "secret"}
		mux := http.NewServeMux()
		srv.Mount(mux)
		res := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/overview", nil)
		req.Header.Set("Authorization", "Bearer nope")
		mux.ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("status %d, want 401", res.Code)
		}
	})

	t.Run("correct bearer returns 200", func(t *testing.T) {
		srv := &Server{Cfg: cfg, Audit: audit, Started: time.Now(), Token: "secret"}
		mux := http.NewServeMux()
		srv.Mount(mux)
		res := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/repos", nil)
		req.Header.Set("Authorization", "Bearer secret")
		mux.ServeHTTP(res, req)
		if res.Code != 200 {
			t.Fatalf("status %d: %s", res.Code, res.Body.String())
		}
	})

	t.Run("viewer bearer can GET", func(t *testing.T) {
		srv := &Server{Cfg: cfg, Audit: audit, Started: time.Now(), Token: "op", ViewerToken: "view"}
		mux := http.NewServeMux()
		srv.Mount(mux)
		res := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/repos", nil)
		req.Header.Set("Authorization", "Bearer view")
		mux.ServeHTTP(res, req)
		if res.Code != 200 {
			t.Fatalf("status %d: %s", res.Code, res.Body.String())
		}
	})

	t.Run("whoami reports the caller's role", func(t *testing.T) {
		srv := &Server{Cfg: cfg, Audit: audit, Started: time.Now(), Token: "op", ViewerToken: "view"}
		mux := http.NewServeMux()
		srv.Mount(mux)

		res := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/whoami", nil)
		req.Header.Set("Authorization", "Bearer op")
		mux.ServeHTTP(res, req)
		if res.Code != 200 || !strings.Contains(res.Body.String(), `"role": "operator"`) {
			t.Fatalf("operator whoami: status %d, body %s", res.Code, res.Body.String())
		}

		res = httptest.NewRecorder()
		req = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/whoami", nil)
		req.Header.Set("Authorization", "Bearer view")
		mux.ServeHTTP(res, req)
		if res.Code != 200 || !strings.Contains(res.Body.String(), `"role": "viewer"`) {
			t.Fatalf("viewer whoami: status %d, body %s", res.Code, res.Body.String())
		}

		res = httptest.NewRecorder()
		mux.ServeHTTP(res, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/whoami", nil))
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated whoami: status %d, want 401", res.Code)
		}
	})

	t.Run("session verifier is additive to bearer, not a replacement", func(t *testing.T) {
		sessionRole := "" // "" = no valid session for this sub-case
		srv := &Server{Cfg: cfg, Audit: audit, Started: time.Now(), Token: "op",
			SessionVerifier: func(r *http.Request) (string, bool) {
				if sessionRole == "" {
					return "", false
				}
				return sessionRole, true
			}}
		mux := http.NewServeMux()
		srv.Mount(mux)

		// No cookie, no bearer, SessionVerifier configured but returns
		// false -> still 401, not 503 (a method IS configured, it just
		// didn't match this request).
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/repos", nil))
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("no session, no bearer: status %d, want 401", res.Code)
		}

		// SessionVerifier grants viewer -> GET succeeds, write is forbidden.
		sessionRole = "viewer"
		res = httptest.NewRecorder()
		mux.ServeHTTP(res, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/repos", nil))
		if res.Code != 200 {
			t.Fatalf("session viewer GET: status %d: %s", res.Code, res.Body.String())
		}
		res = httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/actions/fix",
			strings.NewReader(`{"repo":"svc","confirm":true}`))
		mux.ServeHTTP(res, req)
		if res.Code != http.StatusForbidden {
			t.Fatalf("session viewer POST: status %d, want 403", res.Code)
		}

		// SessionVerifier grants operator -> bearer token still works too
		// (additive: either method authenticates).
		sessionRole = "operator"
		res = httptest.NewRecorder()
		mux.ServeHTTP(res, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/repos", nil))
		if res.Code != 200 {
			t.Fatalf("session operator GET: status %d", res.Code)
		}
		res = httptest.NewRecorder()
		req = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/repos", nil)
		req.Header.Set("Authorization", "Bearer op")
		mux.ServeHTTP(res, req)
		if res.Code != 200 {
			t.Fatalf("bearer still works alongside SessionVerifier: status %d", res.Code)
		}
	})
}

func TestHistoryAndBacklogRepoFilter(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	audit, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = audit.Close() })

	now := time.Now().UTC()
	for _, rec := range []store.Record{
		{At: now, Repo: "svc-a", Source: "ci", Kind: "fail", Action: "fix"},
		{At: now.Add(time.Second), Repo: "svc-b", Source: "ci", Kind: "pass", Action: "noop"},
		{At: now.Add(2 * time.Second), Repo: "svc-a", Source: "prod-health", Kind: "breach", Action: "revert"},
	} {
		if err := audit.Append(rec); err != nil {
			t.Fatal(err)
		}
	}

	backlogPath := filepath.Join(t.TempDir(), "BACKLOG.md")
	backlog := `# BACKLOG

## Log
- [2026-01-01T00:00:00Z] repo=svc-a action=fix run_url=https://x
- [2026-01-01T00:01:00Z] repo=svc-b action=noop
- [2026-01-01T00:02:00Z] repo=svc-a action=revert
`
	if err := os.WriteFile(backlogPath, []byte(backlog), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Repos: []config.Repo{
			{Name: "svc-a", GitHub: "acme/svc-a"},
			{Name: "svc-b", GitHub: "acme/svc-b"},
		},
	}
	srv := &Server{
		Cfg: cfg, Audit: audit, BacklogPath: backlogPath,
		Started: time.Now(), Token: "tok",
	}
	mux := http.NewServeMux()
	srv.Mount(mux)

	authGet := func(path string) *httptest.ResponseRecorder {
		res := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer tok")
		mux.ServeHTTP(res, req)
		return res
	}

	t.Run("history filters exact repo", func(t *testing.T) {
		res := authGet("/api/history?repo=svc-a")
		if res.Code != 200 {
			t.Fatalf("status %d: %s", res.Code, res.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		events, _ := body["events"].([]any)
		if len(events) != 2 {
			t.Fatalf("events=%v", events)
		}
		for _, e := range events {
			m := e.(map[string]any)
			if m["repo"] != "svc-a" {
				t.Fatalf("got repo %v", m["repo"])
			}
		}
	})

	t.Run("history without filter returns all", func(t *testing.T) {
		res := authGet("/api/history")
		var body map[string]any
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		events, _ := body["events"].([]any)
		if len(events) != 3 {
			t.Fatalf("events=%v", events)
		}
	})

	t.Run("backlog filters lines mentioning repo", func(t *testing.T) {
		res := authGet("/api/backlog?repo=svc-a")
		if res.Code != 200 {
			t.Fatalf("status %d: %s", res.Code, res.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		md, _ := body["markdown"].(string)
		if !strings.Contains(md, "repo=svc-a") {
			t.Fatalf("missing svc-a lines: %q", md)
		}
		if strings.Contains(md, "repo=svc-b") {
			t.Fatalf("svc-b leaked: %q", md)
		}
	})
}

func TestManualActions(t *testing.T) {
	cfg := &config.Config{Repos: []config.Repo{{Name: "svc-a"}}}
	audit, err := store.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = audit.Close() })

	ch := make(chan orchestrator.Signal, 4)
	srv := &Server{
		Cfg: cfg, Audit: audit, Started: time.Now(),
		Token: "op", ViewerToken: "view",
		Enqueue: func(sig orchestrator.Signal) { ch <- sig },
	}
	mux := http.NewServeMux()
	srv.Mount(mux)

	post := func(path, token, body string) *httptest.ResponseRecorder {
		res := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		mux.ServeHTTP(res, req)
		return res
	}

	t.Run("viewer gets 403", func(t *testing.T) {
		res := post("/api/actions/fix", "view", `{"repo":"svc-a","confirm":true}`)
		if res.Code != http.StatusForbidden {
			t.Fatalf("status %d, want 403", res.Code)
		}
	})

	t.Run("missing confirm rejected", func(t *testing.T) {
		res := post("/api/actions/fix", "op", `{"repo":"svc-a"}`)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("status %d, want 400", res.Code)
		}
	})

	t.Run("fix enqueues CI fail", func(t *testing.T) {
		res := post("/api/actions/fix", "op", `{"repo":"svc-a","confirm":true}`)
		if res.Code != 200 {
			t.Fatalf("status %d: %s", res.Code, res.Body.String())
		}
		sig := <-ch
		if sig.Source != orchestrator.SourceCI || sig.Kind != orchestrator.KindFail || sig.Repo != "svc-a" {
			t.Fatalf("sig=%+v", sig)
		}
		if orchestrator.Decide(sig) != orchestrator.ActionFix {
			t.Fatalf("Decide=%v", orchestrator.Decide(sig))
		}
	})

	t.Run("promote enqueues dev-gate pass", func(t *testing.T) {
		res := post("/api/actions/promote", "op", `{"repo":"svc-a","confirm":true}`)
		if res.Code != 200 {
			t.Fatalf("status %d: %s", res.Code, res.Body.String())
		}
		sig := <-ch
		if sig.Source != orchestrator.SourceDevGate || sig.Kind != orchestrator.KindPass {
			t.Fatalf("sig=%+v", sig)
		}
		if orchestrator.Decide(sig) != orchestrator.ActionPromote {
			t.Fatalf("Decide=%v", orchestrator.Decide(sig))
		}
	})

	t.Run("revert enqueues prod-health breach", func(t *testing.T) {
		res := post("/api/actions/revert", "op", `{"repo":"svc-a","confirm":true}`)
		if res.Code != 200 {
			t.Fatalf("status %d: %s", res.Code, res.Body.String())
		}
		sig := <-ch
		if sig.Source != orchestrator.SourceProdHealth || sig.Kind != orchestrator.KindBreach {
			t.Fatalf("sig=%+v", sig)
		}
		if orchestrator.Decide(sig) != orchestrator.ActionRevert {
			t.Fatalf("Decide=%v", orchestrator.Decide(sig))
		}
	})

	t.Run("no enqueue returns 503", func(t *testing.T) {
		bare := &Server{Cfg: cfg, Audit: audit, Started: time.Now(), Token: "op"}
		m := http.NewServeMux()
		bare.Mount(m)
		res := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/actions/fix",
			strings.NewReader(`{"repo":"svc-a","confirm":true}`))
		req.Header.Set("Authorization", "Bearer op")
		m.ServeHTTP(res, req)
		if res.Code != http.StatusServiceUnavailable {
			t.Fatalf("status %d, want 503", res.Code)
		}
	})
}
