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
	"strings"
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
	action, repo, headRepo, event, branch, sha, conclusion, name, path string
}

func (r wfRun) body() []byte {
	repo := or(r.repo, testRepo)
	return []byte(fmt.Sprintf(`{
		"action": %q,
		"repository": {"full_name": %q},
		"workflow_run": {
			"event": %q,
			"name": %q,
			"path": %q,
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
		or(r.name, "CI"),
		or(r.path, ".github/workflows/ci.yml"),
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

func TestMatchCIWorkflow(t *testing.T) {
	cases := []struct {
		allow      []string
		name, path string
		want       bool
	}{
		{nil, "Deploy", ".github/workflows/deploy.yml", true},
		{[]string{}, "Deploy", ".github/workflows/deploy.yml", true},
		{[]string{"ci"}, "CI", ".github/workflows/ci.yml", true},
		{[]string{"CI"}, "ci", ".github/workflows/ci.yml", true},
		{[]string{"ci.yml"}, "CI", ".github/workflows/ci.yml", true},
		{[]string{".github/workflows/ci.yml"}, "CI", ".github/workflows/ci.yml", true},
		{[]string{"ci"}, "Deploy", ".github/workflows/deploy.yml", false},
		{[]string{"ci", "test"}, "Test", ".github/workflows/test.yaml", true},
	}
	for _, c := range cases {
		if got := matchCIWorkflow(c.allow, c.name, c.path); got != c.want {
			t.Errorf("matchCIWorkflow(%v, %q, %q) = %v, want %v", c.allow, c.name, c.path, got, c.want)
		}
	}
}

func TestHandleGitHubCIWorkflowAllowlist(t *testing.T) {
	ch := make(chan orchestrator.Signal, 1)
	srv := githubServer(ch, nil)
	srv.CIWorkflows = []string{"ci"}

	if got := postGitHub(t, srv, wfRun{name: "CI", path: ".github/workflows/ci.yml"}.body(), "d1"); got != http.StatusAccepted {
		t.Fatalf("CI workflow: status = %d, want 202", got)
	}
	if len(ch) != 1 {
		t.Fatalf("CI workflow emitted %d signals, want 1", len(ch))
	}
	<-ch

	if got := postGitHub(t, srv, wfRun{name: "Deploy", path: ".github/workflows/deploy.yml"}.body(), "d2"); got != http.StatusNoContent {
		t.Fatalf("Deploy workflow: status = %d, want 204", got)
	}
	if len(ch) != 0 {
		t.Fatalf("Deploy workflow emitted a CI signal: %+v", <-ch)
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

// gatedTestRepo enrols the one test repo in prod-health, the way
// `repos[].gates: [prod-health]` does for the real daemon.
func gatedTestRepo(repo string) bool { return repo == "example-service" }

// amServer is the minimum server an alert can act through: the label
// resolves *and* the repo opted into the prod-health gate.
func amServer(ch chan orchestrator.Signal) *Server {
	return &Server{
		Signals:         ch,
		ResolveRepo:     resolveTestRepo,
		ProdHealthGated: gatedTestRepo,
		Log:             silentLogger(),
	}
}

// amAlert builds a single-alert Alertmanager delivery. status is
// "firing" or "resolved"; labels is a JSON object literal.
func amAlert(status, labels string) []byte {
	return []byte(`{"status":"` + status + `","alerts":[{"status":"` + status + `","labels":` + labels + `}]}`)
}

// postAM delivers body to /webhooks/alertmanager. Each header in
// headers is applied as key/value pairs.
func postAM(t *testing.T, srv *Server, body []byte, headers ...string) int {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/alertmanager", bytes.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	srv.handleAlertmanager(w, req)
	return w.Code
}

func TestHandleAlertmanager(t *testing.T) {
	ch := make(chan orchestrator.Signal, 1)
	body := amAlert("firing", `{"repo":"example-service","alertname":"HighP95Latency"}`)
	if got := postAM(t, amServer(ch), body); got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", got)
	}
	sig := <-ch
	if sig.Source != orchestrator.SourceProdHealth || sig.Kind != orchestrator.KindBreach {
		t.Errorf("signal = %+v", sig)
	}
}

// TestHandleAlertmanagerSecret covers the accept path with a real
// credential, which had no coverage at all: every alertmanager test left
// AMSecret empty, so the one route into ActionRevert that a shared
// secret guards was only ever exercised unauthenticated.
func TestHandleAlertmanagerSecret(t *testing.T) {
	const secret = "am-shhh"
	body := amAlert("firing", `{"repo":"example-service"}`)

	cases := []struct {
		name          string
		secret        string
		requireSecret bool
		headers       []string
		want          int
		wantSignal    bool
	}{
		{
			name: "correct bearer accepted", secret: secret,
			headers: []string{"Authorization", "Bearer " + secret},
			want:    http.StatusAccepted, wantSignal: true,
		},
		{
			name: "correct x-webhook-secret accepted", secret: secret,
			headers: []string{"X-Webhook-Secret", secret},
			want:    http.StatusAccepted, wantSignal: true,
		},
		{
			name: "wrong bearer rejected", secret: secret,
			headers: []string{"Authorization", "Bearer not-it"},
			want:    http.StatusUnauthorized,
		},
		{
			name: "wrong x-webhook-secret rejected", secret: secret,
			headers: []string{"X-Webhook-Secret", "not-it"},
			want:    http.StatusUnauthorized,
		},
		{
			name: "no credential rejected when one is configured", secret: secret,
			want: http.StatusUnauthorized,
		},
		{
			// The prod posture: require_webhook_secret: true with the
			// env var unset must refuse, not fall through to the
			// "skipping verification" warning.
			name: "missing configured secret refuses the request", requireSecret: true,
			headers: []string{"Authorization", "Bearer " + secret},
			want:    http.StatusUnauthorized,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ch := make(chan orchestrator.Signal, 1)
			srv := amServer(ch)
			srv.AMSecret = c.secret
			srv.RequireSecret = c.requireSecret
			if got := postAM(t, srv, body, c.headers...); got != c.want {
				t.Fatalf("status = %d, want %d", got, c.want)
			}
			if c.wantSignal {
				sig := <-ch
				if sig.Repo != "example-service" || sig.Kind != orchestrator.KindBreach {
					t.Errorf("signal = %+v", sig)
				}
				return
			}
			if len(ch) != 0 {
				t.Fatalf("emitted a signal for an unauthenticated alert: %+v", <-ch)
			}
		})
	}
}

// TestHandleAlertmanagerRepeatFiringIsOneBreach is the revert-the-revert
// regression. Alertmanager is level-triggered: it re-sends a firing
// notification every repeat_interval while the alert holds. Each
// re-notification is a genuine, correctly credentialed delivery with its
// own id, so delivery-id dedupe does not see it — and treating it as a
// fresh breach reverted the revert, putting the bad commit back in prod
// on every even-numbered notification.
func TestHandleAlertmanagerRepeatFiringIsOneBreach(t *testing.T) {
	ch := make(chan orchestrator.Signal, 8)
	srv := amServer(ch)
	firing := amAlert("firing", `{"repo":"example-service","alertname":"HighP95Latency"}`)

	if got := postAM(t, srv, firing); got != http.StatusAccepted {
		t.Fatalf("first firing status = %d, want 202", got)
	}
	for i := 2; i <= 3; i++ {
		if got := postAM(t, srv, firing); got != http.StatusNoContent {
			t.Fatalf("firing notification %d status = %d, want 204 (level unchanged)", i, got)
		}
	}
	if len(ch) != 1 {
		t.Fatalf("three firing notifications produced %d signals, want 1", len(ch))
	}
	if sig := <-ch; sig.Kind != orchestrator.KindBreach {
		t.Fatalf("signal kind = %v, want breach", sig.Kind)
	}

	// A resolve clears the state, so the *next* breach is a real edge
	// and must revert again.
	resolved := amAlert("resolved", `{"repo":"example-service","alertname":"HighP95Latency"}`)
	if got := postAM(t, srv, resolved); got != http.StatusAccepted {
		t.Fatalf("resolved status = %d, want 202", got)
	}
	if sig := <-ch; sig.Kind != orchestrator.KindPass {
		t.Fatalf("resolved signal kind = %v, want pass", sig.Kind)
	}
	if got := postAM(t, srv, firing); got != http.StatusAccepted {
		t.Fatalf("new breach after resolve status = %d, want 202", got)
	}
	if sig := <-ch; sig.Kind != orchestrator.KindBreach {
		t.Fatalf("new breach kind = %v, want breach", sig.Kind)
	}

	// A repeated resolve is not an edge either.
	if got := postAM(t, srv, resolved); got != http.StatusAccepted {
		t.Fatalf("resolve after breach status = %d, want 202", got)
	}
	<-ch
	if got := postAM(t, srv, resolved); got != http.StatusNoContent {
		t.Fatalf("repeat resolve status = %d, want 204", got)
	}
	if len(ch) != 0 {
		t.Fatalf("repeat resolve emitted a signal: %+v", <-ch)
	}
}

// TestHandleAlertmanagerRepeatFiringPerRepo: the edge is per repo, so
// one repo's sustained breach must not mask another repo's first one.
func TestHandleAlertmanagerRepeatFiringPerRepo(t *testing.T) {
	ch := make(chan orchestrator.Signal, 8)
	srv := amServer(ch)
	srv.ResolveRepo = func(name string) (string, bool) {
		if name == "example-service" || name == "other-service" {
			return name, true
		}
		return "", false
	}
	srv.ProdHealthGated = func(string) bool { return true }

	for range 2 {
		_ = postAM(t, srv, amAlert("firing", `{"repo":"example-service"}`))
	}
	if got := postAM(t, srv, amAlert("firing", `{"repo":"other-service"}`)); got != http.StatusAccepted {
		t.Fatalf("second repo status = %d, want 202", got)
	}
	got := map[string]int{}
	for len(ch) > 0 {
		got[(<-ch).Repo]++
	}
	if got["example-service"] != 1 || got["other-service"] != 1 {
		t.Fatalf("signals per repo = %v, want one each", got)
	}
}

// TestHandleAlertmanagerRequiresProdHealthGate: resolving the label says
// the repo is configured, not that it opted into having prod reverted
// from a metric. Without this check every repo in config.yaml was
// revertable by anything holding the shared webhook secret — including
// repos deliberately left on `gates: [ci]`.
func TestHandleAlertmanagerRequiresProdHealthGate(t *testing.T) {
	body := amAlert("firing", `{"repo":"example-service"}`)
	cases := []struct {
		name  string
		gated func(string) bool
		want  int
	}{
		{"gate enabled", gatedTestRepo, http.StatusAccepted},
		{"gate not enabled for this repo", func(string) bool { return false }, http.StatusNoContent},
		{"no gate resolver wired", nil, http.StatusNoContent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ch := make(chan orchestrator.Signal, 1)
			srv := amServer(ch)
			srv.ProdHealthGated = c.gated
			if got := postAM(t, srv, body); got != c.want {
				t.Fatalf("status = %d, want %d", got, c.want)
			}
			if c.want == http.StatusNoContent && len(ch) != 0 {
				t.Fatalf("reverted a repo that never enabled prod-health: %+v", <-ch)
			}
		})
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
			srv := amServer(ch)
			srv.ResolveRepo = c.resolver
			if got := postAM(t, srv, amAlert("firing", c.labels)); got != c.want {
				t.Fatalf("status = %d, want %d", got, c.want)
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
	srv := amServer(ch)
	srv.Limiter = ratelimit.New(100, 2) // burst 2, then 429
	// firing → resolved → firing: each delivery is a prod-health edge,
	// so a 202 here is the rate limiter passing rather than the edge
	// guard coalescing.
	if code := postAM(t, srv, amAlert("firing", `{"repo":"example-service"}`)); code != http.StatusAccepted {
		t.Fatalf("1st = %d, want 202", code)
	}
	if code := postAM(t, srv, amAlert("resolved", `{"repo":"example-service"}`)); code != http.StatusAccepted {
		t.Fatalf("2nd = %d, want 202", code)
	}
	if code := postAM(t, srv, amAlert("firing", `{"repo":"example-service"}`)); code != http.StatusTooManyRequests {
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

// TestHandleGitHubLogsBranchMismatch is the release-blocker regression:
// repos[].branch defaults to "develop", so pointing xdlc at a repo whose
// trunk is "main" used to drop every delivery with GitHub reporting 204
// success and the daemon logging nothing at all — indistinguishable from
// "CI has not run yet". The drop is correct; the silence was not.
func TestHandleGitHubLogsBranchMismatch(t *testing.T) {
	var logs bytes.Buffer
	ch := make(chan orchestrator.Signal, 1)
	srv := githubServer(ch, func(string) string { return "develop" })
	srv.Log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	if code := postGitHub(t, srv, wfRun{branch: "main"}.body(), "d1"); code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", code, http.StatusNoContent)
	}
	select {
	case sig := <-ch:
		t.Fatalf("mismatched branch must not emit a signal: %+v", sig)
	default:
	}

	got := logs.String()
	if !strings.Contains(got, "level=WARN") {
		t.Errorf("branch mismatch must be audible at WARN, got:\n%s", got)
	}
	// The operator has to be able to see both branches to fix the config.
	for _, want := range []string{
		"branch mismatch",
		"repo=example-service",
		"head_branch=main",
		"configured_branch=develop",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("log missing %q:\n%s", want, got)
		}
	}
}

// A delivery on the configured branch is the happy path and must stay
// quiet — otherwise the new Warn is noise on every CI run.
func TestHandleGitHubMatchingBranchDoesNotWarn(t *testing.T) {
	var logs bytes.Buffer
	ch := make(chan orchestrator.Signal, 1)
	srv := githubServer(ch, func(string) string { return "main" })
	srv.Log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	if code := postGitHub(t, srv, wfRun{branch: "main"}.body(), "d1"); code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", code, http.StatusAccepted)
	}
	select {
	case <-ch:
	default:
		t.Fatal("configured branch must emit a signal")
	}
	if strings.Contains(logs.String(), "branch mismatch") {
		t.Errorf("matching branch must not warn:\n%s", logs.String())
	}
}

// TestHandleArgoCDGateThatCannotRunIsVisibleButNotAFail: the webhook leg
// of issue #45. A CheckSmoke error used to answer 204 and emit nothing,
// so a typo'd argocd_app produced no signal, no audit row and no
// BACKLOG.md entry — Promote just never fired again. It now emits
// KindBlocked, which records the reason without being mistaken for a
// fail (a dev-gate fail is ActionFix, i.e. a paid coding-agent run).
func TestHandleArgoCDGateThatCannotRunIsVisibleButNotAFail(t *testing.T) {
	ch := make(chan orchestrator.Signal, 1)
	srv := argoServer(ch)
	srv.CheckSmoke = func(context.Context, string) (bool, map[string]any, error) {
		return false, nil, errors.New(
			`smoke gate: argocd health: gitops: argocd app get dev-typo: exit status 20: applications.argoproj.io "dev-typo" not found`)
	}

	if got := postArgoCD(t, srv, syncedHealthy); got != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", got, http.StatusAccepted)
	}

	select {
	case sig := <-ch:
		if sig.Kind != orchestrator.KindBlocked {
			t.Fatalf("kind = %s, want %s", sig.Kind, orchestrator.KindBlocked)
		}
		if sig.Source != orchestrator.SourceDevGate {
			t.Fatalf("source = %s", sig.Source)
		}
		if got := orchestrator.Decide(sig); got != orchestrator.ActionNoop {
			t.Fatalf("Decide = %s, want %s — an unreachable ArgoCD must not buy a Fix", got, orchestrator.ActionNoop)
		}
		if sig.Evidence["escalate"] != orchestrator.EscalateGateUnavailable {
			t.Fatalf("escalate = %v", sig.Evidence["escalate"])
		}
		if sig.Evidence["argocd_app"] != "dev-example-service" {
			t.Fatalf("evidence does not name the app the notification was for: %v", sig.Evidence)
		}
		reason, _ := sig.Evidence["gate_error"].(string)
		if !strings.Contains(reason, `"dev-typo" not found`) {
			t.Fatalf("evidence does not say why the gate could not run: %q", reason)
		}
		if sig.SHA != testSHA {
			t.Fatalf("sha = %q, want the tip read before the probe (%s)", sig.SHA, testSHA)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no signal emitted: the gate failure is still invisible")
	}
}
