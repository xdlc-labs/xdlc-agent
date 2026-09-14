package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
	"github.com/xdlc-labs/xdlc-agent/internal/fixstate"
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
		if res.Code != 200 || !strings.Contains(res.Body.String(), `"role":"operator"`) {
			t.Fatalf("operator whoami: status %d, body %s", res.Code, res.Body.String())
		}

		res = httptest.NewRecorder()
		req = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/whoami", nil)
		req.Header.Set("Authorization", "Bearer view")
		mux.ServeHTTP(res, req)
		if res.Code != 200 || !strings.Contains(res.Body.String(), `"role":"viewer"`) {
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

	t.Run("fix carries operator instructions", func(t *testing.T) {
		res := post("/api/actions/fix", "op",
			`{"repo":"svc-a","confirm":true,"instructions":"the flake is in the seed data"}`)
		if res.Code != 200 {
			t.Fatalf("status %d: %s", res.Code, res.Body.String())
		}
		sig := <-ch
		if sig.OperatorInstructions != "the flake is in the seed data" {
			t.Fatalf("instructions=%q", sig.OperatorInstructions)
		}
		// The text can name internal systems; evidence reaches BACKLOG.md
		// and the audit DB, so only its length may go there.
		if got, ok := sig.Evidence["operator_instructions_len"]; !ok || got != 29 {
			t.Fatalf("want length-only evidence, got %v (%v)", got, ok)
		}
		for k, v := range sig.Evidence {
			if str, ok := v.(string); ok && strings.Contains(str, "seed data") {
				t.Fatalf("instruction text leaked into evidence[%s]", k)
			}
		}
	})

	t.Run("oversized instructions rejected", func(t *testing.T) {
		body := `{"repo":"svc-a","confirm":true,"instructions":"` + strings.Repeat("x", maxOperatorInstructions+1) + `"}`
		res := post("/api/actions/fix", "op", body)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("status %d, want 400", res.Code)
		}
	})

	t.Run("promote ignores instructions", func(t *testing.T) {
		res := post("/api/actions/promote", "op", `{"repo":"svc-a","confirm":true,"instructions":"ignored"}`)
		if res.Code != 200 {
			t.Fatalf("status %d: %s", res.Code, res.Body.String())
		}
		sig := <-ch
		if sig.OperatorInstructions != "" {
			t.Fatalf("promote must not carry instructions: %q", sig.OperatorInstructions)
		}
	})

	t.Run("fix agent headers stay off evidence", func(t *testing.T) {
		res := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/actions/fix",
			strings.NewReader(`{"repo":"svc-a","confirm":true}`))
		req.Header.Set("Authorization", "Bearer op")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-XDLC-Agent-Provider", "cursor")
		req.Header.Set("X-XDLC-Agent-Key", "secret-test-key")
		mux.ServeHTTP(res, req)
		if res.Code != 200 {
			t.Fatalf("status %d: %s", res.Code, res.Body.String())
		}
		sig := <-ch
		if sig.OperatorAgentProvider != "cursor" {
			t.Fatalf("provider=%q", sig.OperatorAgentProvider)
		}
		if sig.OperatorAgentKey != "secret-test-key" {
			t.Fatalf("key not threaded")
		}
		if _, ok := sig.Evidence["OperatorAgentKey"]; ok {
			t.Fatal("key leaked into Evidence")
		}
		body := res.Body.String()
		if strings.Contains(body, "secret-test-key") {
			t.Fatal("key leaked into response body")
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

func TestHistoryManualSourceIsDaemon(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	audit, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = audit.Close() })

	if err := audit.Append(store.Record{
		At: time.Now().UTC(), Repo: "svc-a", Source: "daemon", Kind: "fail", Action: "fix",
		Evidence: map[string]any{"manual": true, "via": "api", "action": "fix"},
	}); err != nil {
		t.Fatal(err)
	}

	srv := &Server{
		Cfg:   &config.Config{Repos: []config.Repo{{Name: "svc-a"}}},
		Audit: audit, Started: time.Now(), Token: "tok",
	}
	mux := http.NewServeMux()
	srv.Mount(mux)
	res := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/history", nil)
	req.Header.Set("Authorization", "Bearer tok")
	mux.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("status %d: %s", res.Code, res.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	events, _ := body["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("events=%v", events)
	}
	m := events[0].(map[string]any)
	if m["source"] != "daemon" {
		t.Fatalf("source=%v, want daemon (not github-actions)", m["source"])
	}
	if m["gate"] != "CI" {
		t.Fatalf("gate=%v, want CI for a manual Fix", m["gate"])
	}
}

// TestBlockedRecordIsNotShownAsHealthyOrIdle: the console reads the same
// audit rows as `xdlc history`. A gate that could not run has no
// verdict, so it must not render as a quiet ("idle") gate on a
// ("healthy") repo — that is the invisibility of issue #45 reappearing
// in the browser.
func TestBlockedRecordIsNotShownAsHealthyOrIdle(t *testing.T) {
	blocked := string(orchestrator.KindBlocked)

	if got := mapKindStatus(blocked); got == "idle" {
		t.Error("a gate that could not run renders as idle")
	}
	if got := mapKindStatus(blocked); got == "pass" || got == "fail" {
		t.Errorf("blocked renders as %q — indistinguishable from a verdict", got)
	}
	if got := mapKindStatus(blocked); got != "waiting" {
		t.Errorf("mapKindStatus(blocked) = %q, want waiting", got)
	}

	rec := store.Record{Repo: "svc", Source: "dev-gate", Kind: blocked, Action: "noop"}
	if got := mapHealth(rec); got == "healthy" {
		t.Error("a repo whose gate could not run reports healthy")
	}
	if got := mapHealth(rec); got != "degraded" {
		t.Errorf("mapHealth(blocked) = %q, want degraded", got)
	}

	// Unchanged for the verdicts.
	if got := mapKindStatus("pass"); got != "pass" {
		t.Errorf("pass = %q", got)
	}
	if got := mapKindStatus("fail"); got != "fail" {
		t.Errorf("fail = %q", got)
	}
	if got := mapHealth(store.Record{Source: "prod-health", Kind: "breach"}); got != "breach" {
		t.Errorf("breach = %q", got)
	}
	if got := mapHealth(store.Record{Source: "ci", Kind: "pass"}); got != "healthy" {
		t.Errorf("pass = %q", got)
	}
}

// openTestAudit returns an empty audit store for a handler test.
func openTestAudit(t *testing.T) *store.AuditStore {
	t.Helper()
	audit, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = audit.Close() })
	return audit
}

func TestActiveFixesReportsWhatIsRunning(t *testing.T) {
	tracker := fixstate.New()
	tracker.Set(fixstate.Fix{
		ID: "run-1", SessionID: "20260909T000000Z-svc", Repo: "svc",
		Source: "ci", Provider: "claude", State: fixstate.Fixing, Attempt: 2,
	})
	srv := &Server{Cfg: &config.Config{}, Audit: openTestAudit(t), Started: time.Now(), Token: "op", Fixes: tracker}
	mux := http.NewServeMux()
	srv.Mount(mux)

	res := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/fixes/active", nil)
	req.Header.Set("Authorization", "Bearer op")
	mux.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d", res.Code)
	}
	var body struct {
		Fixes []fixstate.Fix `json:"fixes"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Fixes) != 1 {
		t.Fatalf("want one in-flight Fix, got %+v", body.Fixes)
	}
	got := body.Fixes[0]
	if got.ID != "run-1" || got.State != fixstate.Fixing || got.Repo != "svc" || got.Attempt != 2 {
		t.Fatalf("unexpected row: %+v", got)
	}
	if got.SessionID != "20260909T000000Z-svc" {
		t.Fatalf("session link missing: %+v", got)
	}
}

// No Fix running, and a daemon with no tracker at all, both have to
// answer with an empty list — a console cannot render a null.
func TestActiveFixesEmptyIsAList(t *testing.T) {
	for name, tracker := range map[string]*fixstate.Tracker{
		"nothing running": fixstate.New(),
		"no tracker":      nil,
	} {
		srv := &Server{Cfg: &config.Config{}, Audit: openTestAudit(t), Started: time.Now(), Token: "op", Fixes: tracker}
		mux := http.NewServeMux()
		srv.Mount(mux)

		res := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/fixes/active", nil)
		req.Header.Set("Authorization", "Bearer op")
		mux.ServeHTTP(res, req)

		if res.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", name, res.Code)
		}
		var body struct {
			Fixes *[]fixstate.Fix `json:"fixes"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if body.Fixes == nil {
			t.Fatalf("%s: fixes was null; a console cannot render that", name)
		}
		if len(*body.Fixes) != 0 {
			t.Fatalf("%s: want an empty list, got %+v", name, *body.Fixes)
		}
	}
}

func TestActiveFixesRequiresAuth(t *testing.T) {
	srv := &Server{Cfg: &config.Config{}, Audit: openTestAudit(t), Started: time.Now(), Token: "op", Fixes: fixstate.New()}
	mux := http.NewServeMux()
	srv.Mount(mux)

	res := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/fixes/active", nil)
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.Code)
	}
}

// Fix transitions ride the console's existing SSE connection under a
// named event, so a client written before they existed is untouched: an
// audit record must stay on the default event with its sequence id, and
// a state frame must carry neither.
func TestEventsStreamsFixStateUnderANamedEvent(t *testing.T) {
	audit := openTestAudit(t)
	tracker := fixstate.New()
	tracker.Set(fixstate.Fix{ID: "run-1", Repo: "svc", Source: "ci", State: fixstate.Cloning})
	srv := &Server{Cfg: &config.Config{}, Audit: audit, Started: time.Now(), Token: "op", Fixes: tracker}
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	stream, stop := openEventStream(t, ts.URL)
	defer stop()

	// What is already running has to arrive on connect, or a long agent
	// run stays invisible until it ends.
	if frame := stream.next(t); !strings.Contains(frame, "event: fix_state") || !strings.Contains(frame, "run-1") {
		t.Fatalf("in-flight Fix not replayed on connect:\n%s", frame)
	}

	tracker.Set(fixstate.Fix{ID: "run-1", State: fixstate.Fixing})
	frame := stream.next(t)
	if !strings.Contains(frame, `"state":"fixing"`) {
		t.Fatalf("transition not streamed:\n%s", frame)
	}
	if strings.Contains(frame, "id: ") {
		t.Fatalf("a fix_state frame carried an SSE id, which would poison audit replay:\n%s", frame)
	}

	if err := audit.Append(store.Record{At: time.Now().UTC(), Repo: "svc", Source: "ci", Kind: "fail", Action: "fix"}); err != nil {
		t.Fatal(err)
	}
	frame = stream.next(t)
	if strings.Contains(frame, "event:") {
		t.Fatalf("audit record left the default SSE event:\n%s", frame)
	}
	if !strings.Contains(frame, "id: 1") || !strings.Contains(frame, `"repo":"svc"`) {
		t.Fatalf("audit frame lost its shape:\n%s", frame)
	}
}

// A daemon with no tracker must still stream audit events rather than
// spinning on a closed channel.
func TestEventsWithoutATrackerStillStreamsAudit(t *testing.T) {
	audit := openTestAudit(t)
	srv := &Server{Cfg: &config.Config{}, Audit: audit, Started: time.Now(), Token: "op"}
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	stream, stop := openEventStream(t, ts.URL)
	defer stop()

	if err := audit.Append(store.Record{At: time.Now().UTC(), Repo: "svc", Source: "ci", Kind: "fail", Action: "fix"}); err != nil {
		t.Fatal(err)
	}
	frame := stream.next(t)
	if !strings.Contains(frame, `"repo":"svc"`) {
		t.Fatalf("audit event not streamed:\n%s", frame)
	}
	if strings.Contains(frame, "fix_state") {
		t.Fatal("no tracker means no state frames")
	}
}

// eventStream reads SSE frames from a live test server. A recorder
// cannot be used here: the handler only returns when the request is
// canceled, so reading its buffer while it writes is a data race.
type eventStream struct {
	body io.ReadCloser
	buf  *bufio.Reader
}

func openEventStream(t *testing.T, baseURL string) (*eventStream, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/events", nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer op")
	// The body is a stream the test reads across several assertions, so
	// it is closed by the returned stop function that every caller
	// defers, not here.
	res, err := http.DefaultClient.Do(req) //nolint:bodyclose
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		cancel()
		t.Fatalf("status = %d", res.StatusCode)
	}
	return &eventStream{body: res.Body, buf: bufio.NewReader(res.Body)}, func() {
		_ = res.Body.Close()
		cancel()
	}
}

// next returns the next complete SSE frame (up to the blank line).
func (s *eventStream) next(t *testing.T) string {
	t.Helper()
	var frame strings.Builder
	for {
		line, err := s.buf.ReadString('\n')
		if err != nil {
			t.Fatalf("read stream: %v (so far: %q)", err, frame.String())
		}
		if line == "\n" {
			return frame.String()
		}
		frame.WriteString(line)
	}
}
