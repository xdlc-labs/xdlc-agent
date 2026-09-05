package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/orchestrator"
	"github.com/xdlc-labs/xdlc-agent/internal/ratelimit"
)

const (
	testRepo = "your-org/example-service"
	testSHA  = "0123456789abcdef0123456789abcdef01234567"
)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// resolveTestRepo is the only resolver the tests configure: "org/repo"
// (or the short name) → short name, everything else unknown.
func resolveTestRepo(name string) (string, bool) {
	if name == testRepo || name == "example-service" {
		return "example-service", true
	}
	return "", false
}

// wfRun builds a workflow_run delivery body. Zero fields fall back to a
// legitimate same-repo push run, so each case only states its deviation.
type wfRun struct {
	action, repo, headRepo, event, branch, sha, conclusion string
}

func (r wfRun) body() []byte {
	repo := or(r.repo, testRepo)
	return []byte(fmt.Sprintf(`{
		"action": %q,
		"repository": {"full_name": %q},
		"workflow_run": {
			"event": %q,
			"conclusion": %q,
			"head_branch": %q,
			"head_sha": %q,
			"head_repository": {"full_name": %q},
			"html_url": "https://github.com/%s/actions/runs/1"
		}
	}`,
		or(r.action, "completed"),
		repo,
		or(r.event, "push"),
		or(r.conclusion, "failure"),
		or(r.branch, "develop"),
		or(r.sha, testSHA),
		or(r.headRepo, repo),
		repo))
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// postGitHub delivers body to the github handler and returns the status.
func postGitHub(t *testing.T, srv *Server, body []byte, deliveryID string) int {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "workflow_run")
	if deliveryID != "" {
		req.Header.Set("X-GitHub-Delivery", deliveryID)
	}
	w := httptest.NewRecorder()
	srv.handleGitHub(w, req)
	return w.Code
}

func githubServer(ch chan orchestrator.Signal, branchFor func(string) string) *Server {
	return &Server{
		Signals:       ch,
		ResolveRepo:   resolveTestRepo,
		BranchFor:     branchFor,
		DefaultBranch: "develop",
		Log:           silentLogger(),
	}
}

// TestHandleGitHubTrustsOnlySameRepoPushes is the S1 regression: a fork
// PR delivery is a genuine, correctly signed GitHub payload, and the
// only thing separating it from a trunk push is head_repository +
// workflow_run.event. Accepting one turns into ActionFix, which feeds
// the fork's own job logs to an agent with push access to the real
// branch.
func TestHandleGitHubTrustsOnlySameRepoPushes(t *testing.T) {
	cases := []struct {
		name       string
		run        wfRun
		wantStatus int
		wantSignal bool
		wantKind   orchestrator.Kind
	}{
		{
			name:       "same-repo push failure is accepted",
			run:        wfRun{},
			wantStatus: http.StatusAccepted,
			wantSignal: true,
			wantKind:   orchestrator.KindFail,
		},
		{
			name:       "same-repo push success is accepted",
			run:        wfRun{conclusion: "success"},
			wantStatus: http.StatusAccepted,
			wantSignal: true,
			wantKind:   orchestrator.KindPass,
		},
		{
			// The attack: a fork whose branch is literally named
			// "develop", claiming a push run.
			name:       "fork PR on a branch named develop is rejected",
			run:        wfRun{headRepo: "attacker/example-service"},
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "fork PR run is rejected",
			run:        wfRun{headRepo: "attacker/example-service", event: "pull_request"},
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "missing head_repository is rejected",
			run:        wfRun{headRepo: "-"}, // sentinel; overwritten below
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "same-repo pull_request run is not a branch verdict",
			run:        wfRun{event: "pull_request"},
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "workflow_dispatch run is not a branch verdict",
			run:        wfRun{event: "workflow_dispatch"},
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "other branch is ignored",
			run:        wfRun{branch: "feature/x"},
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "in-progress run is ignored",
			run:        wfRun{action: "requested"},
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "unknown repo is rejected",
			run:        wfRun{repo: "someone-else/other"},
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "missing head_sha is a bad payload",
			run:        wfRun{sha: "-"}, // sentinel; overwritten below
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "ref expression as head_sha is a bad payload",
			run:        wfRun{sha: "--upload-pack=evil"},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ch := make(chan orchestrator.Signal, 1)
			srv := githubServer(ch, nil)
			body := c.run.body()
			// "-" means "field absent entirely", which the sprintf
			// template can't express.
			if c.run.headRepo == "-" {
				body = bytes.Replace(body, []byte(`"head_repository": {"full_name": "-"}`), []byte(`"head_repository": null`), 1)
			}
			if c.run.sha == "-" {
				body = bytes.Replace(body, []byte(`"head_sha": "-"`), []byte(`"head_sha": ""`), 1)
			}

			if got := postGitHub(t, srv, body, ""); got != c.wantStatus {
				t.Fatalf("status = %d, want %d", got, c.wantStatus)
			}
			select {
			case sig := <-ch:
				if !c.wantSignal {
					t.Fatalf("emitted a signal for a delivery that must be dropped: %+v", sig)
				}
				if sig.Source != orchestrator.SourceCI || sig.Kind != c.wantKind {
					t.Errorf("signal = %s/%s, want ci/%s", sig.Source, sig.Kind, c.wantKind)
				}
				if sig.Repo != "example-service" {
					t.Errorf("repo = %q, want example-service", sig.Repo)
				}
				if sig.SHA != testSHA {
					t.Errorf("sha = %q, want %q", sig.SHA, testSHA)
				}
				if sig.Evidence["head_sha"] != testSHA {
					t.Errorf("evidence head_sha = %v", sig.Evidence["head_sha"])
				}
			default:
				if c.wantSignal {
					t.Fatal("no signal emitted")
				}
			}
		})
	}
}

// TestHandleGitHubIgnoresReplayedDelivery is the other half of S1: a
// captured delivery re-POSTed verifies its HMAC forever, so the
// delivery id is what makes it single-use.
func TestHandleGitHubIgnoresReplayedDelivery(t *testing.T) {
	ch := make(chan orchestrator.Signal, 4)
	srv := githubServer(ch, nil)
	body := wfRun{}.body()

	if got := postGitHub(t, srv, body, "delivery-1"); got != http.StatusAccepted {
		t.Fatalf("first delivery status = %d, want 202", got)
	}
	if got := postGitHub(t, srv, body, "delivery-1"); got != http.StatusOK {
		t.Fatalf("replayed delivery status = %d, want 200", got)
	}
	// A different id for the same body is a real re-run, not a replay.
	if got := postGitHub(t, srv, body, "delivery-2"); got != http.StatusAccepted {
		t.Fatalf("second delivery status = %d, want 202", got)
	}

	close(ch)
	var got int
	for range ch {
		got++
	}
	if got != 2 {
		t.Fatalf("emitted %d signals, want 2 (the replay must not dispatch)", got)
	}
}

// TestHandleGitHubUsesPerRepoBranch is the C2 regression: the branch
// filter used to be one global "develop", so a repo configured with
// `branch: main` never received a CI signal at all.
func TestHandleGitHubUsesPerRepoBranch(t *testing.T) {
	branchFor := func(repo string) string {
		if repo == "example-service" {
			return "main"
		}
		return ""
	}

	ch := make(chan orchestrator.Signal, 1)
	srv := githubServer(ch, branchFor)
	if got := postGitHub(t, srv, wfRun{branch: "main"}.body(), ""); got != http.StatusAccepted {
		t.Fatalf("push to configured branch main: status = %d, want 202", got)
	}
	sig := <-ch
	if sig.Repo != "example-service" || sig.Kind != orchestrator.KindFail {
		t.Errorf("signal = %+v", sig)
	}

	// The global default must not leak back in for this repo.
	ch2 := make(chan orchestrator.Signal, 1)
	srv2 := githubServer(ch2, branchFor)
	if got := postGitHub(t, srv2, wfRun{branch: "develop"}.body(), ""); got != http.StatusNoContent {
		t.Fatalf("push to develop on a main-configured repo: status = %d, want 204", got)
	}
	if len(ch2) != 0 {
		t.Fatalf("emitted a signal for a branch this repo doesn't gate on: %+v", <-ch2)
	}
}

func TestHandleGitHubNoBranchConfigured(t *testing.T) {
	ch := make(chan orchestrator.Signal, 1)
	srv := &Server{Signals: ch, ResolveRepo: resolveTestRepo, Log: silentLogger()}
	if got := postGitHub(t, srv, wfRun{}.body(), ""); got != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", got)
	}
	if len(ch) != 0 {
		t.Fatal("emitted a signal with no branch configured")
	}
}

// postArgoCD delivers an ArgoCD notification body.
func postArgoCD(t *testing.T, srv *Server, body []byte) int {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/argocd", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleArgoCD(w, req)
	return w.Code
}

func argoServer(ch chan orchestrator.Signal) *Server {
	return &Server{
		Signals: ch,
		ResolveArgoApp: func(app string) (string, bool) {
			if app == "dev-example-service" {
				return "example-service", true
			}
			return "", false
		},
		ResolveSHA: func(context.Context, string) (string, error) { return testSHA, nil },
		Log:        silentLogger(),
	}
}

// syncedHealthy is the body that used to be enough, on its own, to
// fast-forward develop onto main.
var syncedHealthy = []byte(`{"app":"dev-example-service","sync":"Synced","health":"Healthy"}`)

// TestHandleArgoCDSyncAloneDoesNotPromote is the S2 regression. The
// webhook path emitted KindPass — and so ActionPromote — from the
// body's own sync/health strings, while the poller path required the
// probe Job to have passed. Both routes to prod must need the probe.
func TestHandleArgoCDSyncAloneDoesNotPromote(t *testing.T) {
	cases := []struct {
		name       string
		check      func(ctx context.Context, repo string) (bool, map[string]any, error)
		resolveSHA func(ctx context.Context, repo string) (string, error)
		wantStatus int
		wantSignal bool
		wantKind   orchestrator.Kind
	}{
		{
			// The critical one: nothing can verify the sync, so the
			// notification produces no promote-capable signal at all.
			name:       "no smoke check wired: no signal",
			check:      nil,
			wantStatus: http.StatusNoContent,
		},
		{
			name: "probe failed: fail, never a promote",
			check: func(context.Context, string) (bool, map[string]any, error) {
				return false, map[string]any{"probe_job": "smoke-e2e", "logs": "3 checks failed"}, nil
			},
			wantStatus: http.StatusAccepted,
			wantSignal: true,
			wantKind:   orchestrator.KindFail,
		},
		{
			name: "probe passed: pass",
			check: func(context.Context, string) (bool, map[string]any, error) {
				return true, map[string]any{"probe_job": "smoke-e2e", "logs": "ok"}, nil
			},
			wantStatus: http.StatusAccepted,
			wantSignal: true,
			wantKind:   orchestrator.KindPass,
		},
		{
			name: "probe errored: no signal",
			check: func(context.Context, string) (bool, map[string]any, error) {
				return false, nil, errors.New("kubectl: connection refused")
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "unattributable commit: no signal",
			check: func(context.Context, string) (bool, map[string]any, error) {
				return true, nil, nil
			},
			resolveSHA: func(context.Context, string) (string, error) {
				return "", errors.New("ls-remote failed")
			},
			wantStatus: http.StatusNoContent,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ch := make(chan orchestrator.Signal, 1)
			srv := argoServer(ch)
			srv.CheckSmoke = c.check
			if c.resolveSHA != nil {
				srv.ResolveSHA = c.resolveSHA
			}

			if got := postArgoCD(t, srv, syncedHealthy); got != c.wantStatus {
				t.Fatalf("status = %d, want %d", got, c.wantStatus)
			}
			select {
			case sig := <-ch:
				if !c.wantSignal {
					t.Fatalf("emitted a signal without a verified probe: %+v", sig)
				}
				if sig.Source != orchestrator.SourceDevGate || sig.Kind != c.wantKind {
					t.Errorf("signal = %s/%s, want dev-gate/%s", sig.Source, sig.Kind, c.wantKind)
				}
				if sig.SHA != testSHA {
					t.Errorf("sha = %q, want the tip read before the probe (%s)", sig.SHA, testSHA)
				}
				if sig.Evidence["probe_job"] != "smoke-e2e" {
					t.Errorf("gate evidence missing from signal: %v", sig.Evidence)
				}
				want := orchestrator.ActionPromote
				if c.wantKind != orchestrator.KindPass {
					want = orchestrator.ActionFix
				}
				if got := orchestrator.Decide(sig); got != want {
					t.Errorf("Decide = %s, want %s", got, want)
				}
			default:
				if c.wantSignal {
					t.Fatal("no signal emitted")
				}
			}
		})
	}
}

// TestHandleArgoCDReadsTipBeforeProbe pins the ordering: the commit a
// pass is attributed to must be read before the probe runs, so a commit
// landing mid-probe cannot inherit its verdict.
func TestHandleArgoCDReadsTipBeforeProbe(t *testing.T) {
	var order []string
	ch := make(chan orchestrator.Signal, 1)
	srv := argoServer(ch)
	srv.ResolveSHA = func(context.Context, string) (string, error) {
		order = append(order, "resolve-sha")
		return testSHA, nil
	}
	srv.CheckSmoke = func(context.Context, string) (bool, map[string]any, error) {
		order = append(order, "probe")
		return true, nil, nil
	}

	if got := postArgoCD(t, srv, syncedHealthy); got != http.StatusAccepted {
		t.Fatalf("status = %d", got)
	}
	<-ch
	if len(order) != 2 || order[0] != "resolve-sha" || order[1] != "probe" {
		t.Fatalf("order = %v, want [resolve-sha probe]", order)
	}
}

// TestHandleArgoCDBoundsConcurrentChecks: re-running the real gate per
// delivery means each POST can spawn argocd/kubectl, so the number
// verifying at once has to be capped or the endpoint becomes an
// amplifier for anyone who can reach it.
func TestHandleArgoCDBoundsConcurrentChecks(t *testing.T) {
	entered := make(chan struct{})
	unblock := make(chan struct{})
	ch := make(chan orchestrator.Signal, 2)
	srv := argoServer(ch)
	srv.MaxConcurrentChecks = 1
	srv.CheckSmoke = func(context.Context, string) (bool, map[string]any, error) {
		entered <- struct{}{}
		<-unblock
		return true, nil, nil
	}

	first := make(chan int, 1)
	go func() { first <- postArgoCD(t, srv, syncedHealthy) }()
	<-entered // the only slot is now held

	if got := postArgoCD(t, srv, syncedHealthy); got != http.StatusTooManyRequests {
		t.Errorf("second concurrent delivery status = %d, want 429", got)
	}

	close(unblock)
	if got := <-first; got != http.StatusAccepted {
		t.Errorf("first delivery status = %d, want 202", got)
	}
	// The slot is released, so the next delivery proceeds.
	srv.CheckSmoke = func(context.Context, string) (bool, map[string]any, error) { return true, nil, nil }
	if got := postArgoCD(t, srv, syncedHealthy); got != http.StatusAccepted {
		t.Errorf("delivery after release status = %d, want 202", got)
	}
}

func TestHandleArgoCDUnknownApp(t *testing.T) {
	ch := make(chan orchestrator.Signal, 1)
	srv := argoServer(ch)
	srv.CheckSmoke = func(context.Context, string) (bool, map[string]any, error) { return true, nil, nil }
	if got := postArgoCD(t, srv, []byte(`{"app":"not-mine","sync":"Synced","health":"Healthy"}`)); got != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", got)
	}
	if len(ch) != 0 {
		t.Fatal("emitted a signal for an unconfigured app")
	}
}

func TestHandleAlertmanager(t *testing.T) {
	body := []byte(`{"status":"firing","alerts":[{"status":"firing","labels":{"repo":"example-service","alertname":"HighP95Latency"}}]}`)
	ch := make(chan orchestrator.Signal, 1)
	srv := &Server{Signals: ch, ResolveRepo: resolveTestRepo, Log: silentLogger()}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/alertmanager", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleAlertmanager(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d", w.Code)
	}
	sig := <-ch
	if sig.Source != orchestrator.SourceProdHealth || sig.Kind != orchestrator.KindBreach {
		t.Errorf("signal = %+v", sig)
	}
}

// TestHandleAlertmanagerRejectsUnknownRepo: the repo label decides which
// repo gets a revert pushed to prod, and an unresolvable one used to be
// carried through as-is — minting a repo the orchestrator then keeps a
// worker goroutine and channel for, forever.
func TestHandleAlertmanagerRejectsUnknownRepo(t *testing.T) {
	cases := []struct {
		name     string
		labels   string
		resolver func(string) (string, bool)
		want     int
	}{
		{"unknown repo label", `{"repo":"attacker-invented"}`, resolveTestRepo, http.StatusNoContent},
		{"unknown service label", `{"service":"attacker-invented"}`, resolveTestRepo, http.StatusNoContent},
		{"no resolver configured", `{"repo":"example-service"}`, nil, http.StatusNoContent},
		{"known repo", `{"repo":"example-service"}`, resolveTestRepo, http.StatusAccepted},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ch := make(chan orchestrator.Signal, 1)
			srv := &Server{Signals: ch, ResolveRepo: c.resolver, Log: silentLogger()}
			body := []byte(`{"status":"firing","alerts":[{"status":"firing","labels":` + c.labels + `}]}`)
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/alertmanager", bytes.NewReader(body))
			w := httptest.NewRecorder()
			srv.handleAlertmanager(w, req)
			if w.Code != c.want {
				t.Fatalf("status = %d, want %d", w.Code, c.want)
			}
			if c.want == http.StatusNoContent && len(ch) != 0 {
				t.Fatalf("emitted a signal for an unverifiable repo label: %+v", <-ch)
			}
		})
	}
}

func TestRequireSecret(t *testing.T) {
	ch := make(chan orchestrator.Signal, 1)
	srv := &Server{Signals: ch, RequireSecret: true, Log: silentLogger()}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/argocd", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()
	srv.handleArgoCD(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	body := []byte(`{"action":"completed"}`)
	secret := "shhh"
	valid := sign(secret, body)

	cases := []struct {
		name   string
		secret string
		header string
		body   []byte
		want   bool
	}{
		{"valid", secret, valid, body, true},
		{"wrong secret", "other", valid, body, false},
		{"tampered body", secret, valid, []byte(`{"action":"tampered"}`), false},
		{"missing prefix", secret, hex.EncodeToString([]byte("x")), body, false},
		{"empty header", secret, "", body, false},
		{"bad hex", secret, "sha256=not-hex!!", body, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := verifySignature(c.secret, c.header, c.body); got != c.want {
				t.Errorf("verifySignature(%q, %q, %s) = %v, want %v", c.secret, c.header, c.body, got, c.want)
			}
		})
	}
}

func TestIsHexSHA(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{testSHA, true},
		{"0123456", true},
		{"", false},
		{"012345", false},                    // too short to be an object name
		{"HEAD", false},                      // ref expression
		{"origin/develop", false},            // ref
		{"--upload-pack=/bin/sh", false},     // git option
		{"0123456789ABCDEF0123456", false},   // uppercase
		{"0123456789abcdefg", false},         // non-hex
		{testSHA + testSHA, false},           // longer than SHA-256
		{"0123456 --exec=whoami", false},     // argv smuggling
		{"refs/heads/develop:refs/x", false}, // refspec
	}
	for _, c := range cases {
		if got := isHexSHA(c.in); got != c.want {
			t.Errorf("isHexSHA(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestBodyTooLarge(t *testing.T) {
	srv := &Server{Signals: make(chan orchestrator.Signal, 1), Log: silentLogger()}
	big := bytes.Repeat([]byte("x"), maxBodyBytes+1)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/github", bytes.NewReader(big))
	req.Header.Set("X-GitHub-Event", "workflow_run")
	w := httptest.NewRecorder()
	srv.handleGitHub(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
}

func TestWebhookRateLimit429(t *testing.T) {
	ch := make(chan orchestrator.Signal, 8)
	srv := &Server{
		Signals:     ch,
		ResolveRepo: resolveTestRepo,
		Limiter:     ratelimit.New(100, 2), // burst 2, then 429
		Log:         silentLogger(),
	}
	body := []byte(`{"status":"firing","alerts":[{"status":"firing","labels":{"repo":"example-service"}}]}`)
	post := func() int {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/alertmanager", bytes.NewReader(body))
		w := httptest.NewRecorder()
		srv.handleAlertmanager(w, req)
		return w.Code
	}
	if code := post(); code != http.StatusAccepted {
		t.Fatalf("1st = %d, want 202", code)
	}
	if code := post(); code != http.StatusAccepted {
		t.Fatalf("2nd = %d, want 202", code)
	}
	if code := post(); code != http.StatusTooManyRequests {
		t.Fatalf("3rd = %d, want 429", code)
	}
}

func TestDedupe(t *testing.T) {
	now := time.Now()
	d := &dedupe{ttl: time.Minute, max: 3, now: func() time.Time { return now }}

	if d.seenBefore("a") {
		t.Fatal("first sighting of a reported as seen")
	}
	if !d.seenBefore("a") {
		t.Fatal("second sighting of a not reported as seen")
	}
	// An id-less delivery can't be de-duplicated; it must not be dropped.
	for i := range 2 {
		if d.seenBefore("") {
			t.Fatalf("empty delivery id treated as a replay (call %d)", i+1)
		}
	}

	// Bounded: filling past max evicts the oldest, so "a" is forgotten
	// while the newest ids are still remembered.
	for _, id := range []string{"b", "c", "d"} {
		if d.seenBefore(id) {
			t.Fatalf("%q reported as seen", id)
		}
	}
	if len(d.seen) > d.max {
		t.Fatalf("set grew to %d, want <= %d", len(d.seen), d.max)
	}
	if d.seenBefore("a") {
		t.Error("evicted id still remembered — the set is not bounded")
	}
	if !d.seenBefore("d") {
		t.Error("newest id forgotten")
	}

	// Expiry: past the TTL the same id is accepted again.
	now = now.Add(2 * time.Minute)
	if d.seenBefore("d") {
		t.Error("id still suppressed after its TTL")
	}
	if !d.seenBefore("d") {
		t.Error("re-recorded id not remembered")
	}
}
