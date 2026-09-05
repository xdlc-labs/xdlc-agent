// Package webhook runs the daemon's HTTP listener for real-time signal
// sources: GitHub workflow_run (CI), ArgoCD notifications (dev-smoke),
// and Alertmanager (prod-health). Pollers remain the fallback.
//
// Trust model: a delivery to this server is *evidence that something
// happened*, never a verdict. Every handler here has to answer three
// questions before it may emit a Signal — because a Signal is what
// eventually pushes code to a repo or fast-forwards prod:
//
//  1. Is the payload authentic? (HMAC / shared secret, and not a replay)
//  2. Does it describe a commit this daemon is allowed to act on? (a push
//     to a configured repo's own dev branch — never a fork's PR head)
//  3. Is the outcome it claims one we verified ourselves? (the ArgoCD
//     handler re-runs the real smoke gate rather than trusting
//     "Synced+Healthy" from the body)
package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/xdlc-labs/xdlc-agent/internal/orchestrator"
	"github.com/xdlc-labs/xdlc-agent/internal/otel"
	"github.com/xdlc-labs/xdlc-agent/internal/ratelimit"
)

// maxBodyBytes caps webhook POST bodies (~1 MiB).
const maxBodyBytes = 1 << 20

// defaultSmokeCheckTimeout bounds the synchronous dev-smoke check the
// ArgoCD handler runs. Kept under the daemon's 30s http.Server
// WriteTimeout so the response is still writable when it returns.
const defaultSmokeCheckTimeout = 20 * time.Second

// defaultMaxSmokeChecks caps concurrent dev-smoke checks started from
// webhook deliveries. Verifying a notification instead of trusting it
// means each delivery can spawn `argocd` + `kubectl` processes, so an
// unauthenticated flood would otherwise be an amplifier. Excess
// deliveries are refused with 429; the poller still covers the repo.
const defaultMaxSmokeChecks = 2

// Server handles webhook deliveries and turns them into orchestrator.Signals.
type Server struct {
	Signals    chan<- orchestrator.Signal
	Secret     string // GitHub HMAC; empty skips verification unless RequireSecret
	ArgoSecret string // optional bearer/shared secret for /webhooks/argocd
	AMSecret   string // optional bearer/shared secret for /webhooks/alertmanager
	// RequireSecret refuses requests when the path's secret is empty.
	RequireSecret bool
	// BranchFor reports the dev branch to accept workflow_run deliveries
	// for, keyed by the *config short name* the repo resolved to — so a
	// repo configured with `branch: main` receives CI signals instead of
	// silently receiving none (repos.Manager.Branch is the intended
	// implementation). nil falls back to DefaultBranch.
	BranchFor func(repo string) string
	// DefaultBranch is the branch filter used when BranchFor is nil or
	// returns "". Empty means "no branch configured", and workflow_run
	// deliveries are then rejected rather than defaulted — this handler
	// does not get to guess which branch is a repo's trunk.
	DefaultBranch string
	ResolveRepo   func(fullName string) (shortName string, ok bool) // org/repo -> config short name
	// ResolveArgoApp maps ArgoCD app name -> config short repo name.
	ResolveArgoApp func(appName string) (shortName string, ok bool)
	// CheckSmoke runs the repo's real dev-smoke gate — ArgoCD
	// Synced+Healthy *and* the k6/Playwright probe Job — and reports the
	// outcome plus the gate's own evidence. It is what makes the ArgoCD
	// handler a "check now" trigger instead of a promote oracle; nil
	// disables promote-capable signals on that path entirely (the
	// dev-smoke poller still covers the repo).
	CheckSmoke func(ctx context.Context, repo string) (passed bool, evidence map[string]any, err error)
	// ResolveSHA reports the commit a repo's dev branch currently points
	// at, so a dev-gate pass can be pinned to the artifact that was
	// probed (orchestrator.Signal.SHA). Required, with CheckSmoke, for
	// this server to emit a promote-capable pass.
	ResolveSHA func(ctx context.Context, repo string) (string, error)
	// SmokeCheckTimeout bounds the CheckSmoke call. 0 → defaultSmokeCheckTimeout.
	SmokeCheckTimeout time.Duration
	// MaxConcurrentChecks caps in-flight CheckSmoke calls.
	// 0 → defaultMaxSmokeChecks.
	MaxConcurrentChecks int
	Log                 *slog.Logger
	Metrics             *otel.Metrics      // optional
	Limiter             *ratelimit.Limiter // optional shared webhook rate limit; nil = unlimited

	initOnce   sync.Once
	deliveries *dedupe
	smokeSem   chan struct{}
}

// Handler returns the http.Handler serving webhook paths.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.Mount(mux)
	return mux
}

// Mount registers webhook paths on an existing mux (so /api can share the listener).
func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/webhooks/github", s.handleGitHub)
	mux.HandleFunc("/webhooks/argocd", s.handleArgoCD)
	mux.HandleFunc("/webhooks/alertmanager", s.handleAlertmanager)
}

type workflowRunEvent struct {
	Action     string `json:"action"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	WorkflowRun struct {
		// Event is the GitHub event that triggered the run ("push",
		// "pull_request", "workflow_dispatch", ...). Only "push" means
		// the run tested a commit that is actually on a branch of the
		// repository — see handleGitHub.
		Event          string `json:"event"`
		Conclusion     string `json:"conclusion"`
		HeadBranch     string `json:"head_branch"`
		HeadSHA        string `json:"head_sha"`
		HTMLURL        string `json:"html_url"`
		HeadRepository struct {
			FullName string `json:"full_name"`
		} `json:"head_repository"`
	} `json:"workflow_run"`
}

func (s *Server) handleGitHub(w http.ResponseWriter, r *http.Request) {
	if !s.allow(w) {
		return
	}
	body, ok := readBody(w, r)
	if !ok {
		return
	}

	if s.Secret != "" {
		if !verifySignature(s.Secret, r.Header.Get("X-Hub-Signature-256"), body) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	} else if s.RequireSecret {
		http.Error(w, "webhook secret required", http.StatusUnauthorized)
		return
	} else {
		s.Log.Warn("github webhook: no secret configured, skipping signature verification")
	}

	if r.Header.Get("X-GitHub-Event") != "workflow_run" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Replay guard. A valid HMAC says the body came from GitHub; it says
	// nothing about whether we already acted on it, and a captured
	// delivery re-POSTed verifies forever. Checked after verification so
	// an unauthenticated flood can't fill the set with chosen ids.
	if delivery := r.Header.Get("X-GitHub-Delivery"); s.deliverySeen(delivery) {
		s.Log.Warn("github webhook: duplicate delivery ignored", "delivery", delivery)
		w.WriteHeader(http.StatusOK)
		return
	}

	var evt workflowRunEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}

	if evt.Action != "completed" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// The trust boundary. A fork PR whose branch is literally named
	// "develop" produces a genuine, correctly signed delivery from
	// GitHub; head_repository is the only field that says whose code ran.
	// Without this check a failing fork workflow becomes KindFail →
	// ActionFix, and the fork's own job logs — attacker-authored text —
	// are fed to a coding agent that has push access to the real branch.
	if !strings.EqualFold(evt.WorkflowRun.HeadRepository.FullName, evt.Repository.FullName) {
		s.Log.Warn("github webhook: rejected run from a different head repository",
			"repository", evt.Repository.FullName,
			"head_repository", evt.WorkflowRun.HeadRepository.FullName,
			"head_branch", evt.WorkflowRun.HeadBranch)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Only a push run tested a commit that is on the branch itself. A
	// pull_request / workflow_dispatch / schedule run reports a merge or
	// arbitrary ref, so its conclusion is not a verdict on the branch.
	if !strings.EqualFold(evt.WorkflowRun.Event, "push") {
		s.Log.Debug("github webhook: ignoring non-push workflow_run",
			"repository", evt.Repository.FullName, "event", evt.WorkflowRun.Event)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// The SHA is what a later Promote pins against and what ends up in
	// git argv, so it has to be a plain object name, not a ref
	// expression or an option that looks like one.
	if !isHexSHA(evt.WorkflowRun.HeadSHA) {
		s.Log.Warn("github webhook: rejected run with unusable head_sha",
			"repository", evt.Repository.FullName, "head_sha", evt.WorkflowRun.HeadSHA)
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}

	repo, ok := s.resolveRepo(evt.Repository.FullName)
	if !ok {
		s.Log.Warn("github webhook: unknown repo", "full_name", evt.Repository.FullName)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Per-repo branch (not one global "develop"): a repo configured with
	// `branch: main` gets its CI signals here.
	branch, ok := s.branchFor(repo)
	if !ok {
		s.Log.Warn("github webhook: no branch configured, dropping delivery", "repo", repo)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if evt.WorkflowRun.HeadBranch != branch {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	kind := orchestrator.KindFail
	if evt.WorkflowRun.Conclusion == "success" {
		kind = orchestrator.KindPass
	}

	sig := orchestrator.Signal{
		Source: orchestrator.SourceCI,
		Repo:   repo,
		Kind:   kind,
		SHA:    evt.WorkflowRun.HeadSHA,
		At:     time.Now().UTC(),
		Evidence: map[string]any{
			"conclusion":  evt.WorkflowRun.Conclusion,
			"run_url":     evt.WorkflowRun.HTMLURL,
			"head_sha":    evt.WorkflowRun.HeadSHA,
			"head_branch": evt.WorkflowRun.HeadBranch,
			"run_event":   evt.WorkflowRun.Event,
		},
	}
	s.emit(sig, "github")
	w.WriteHeader(http.StatusAccepted)
}

// argoCDNotification is the payload shape from Argo CD notifications
// webhook (JSON). We accept a minimal subset.
type argoCDNotification struct {
	App      string `json:"app"`
	AppName  string `json:"appName"`
	Name     string `json:"name"`
	Sync     string `json:"sync"`   // Synced | OutOfSync | ...
	Health   string `json:"health"` // Healthy | Degraded | ...
	Phase    string `json:"phase"`  // optional
	Status   string `json:"status"` // optional aggregate
	Resource struct {
		Name string `json:"name"`
	} `json:"resource"`
}

// handleArgoCD treats an ArgoCD notification as a *check-now trigger*,
// not as a gate result.
//
// Why: SourceDevGate + KindPass is ActionPromote — a fast-forward of the
// dev branch onto prod. This handler used to mint that pass from the
// body's own "sync":"Synced","health":"Healthy" fields, so the promote
// decision rested on two strings supplied by whoever could POST here:
// anyone holding the shared secret, and — with
// `require_webhook_secret: false`, which is what the example config and
// `xdlc init` ship — anyone at all. Meanwhile the poller path
// (gate.SmokeGate.Check) required the k6/Playwright probe Job to have
// exited 0 before the same promote. Two paths to prod with different
// evidence requirements is not a design, it's a bypass.
//
// So the notification only tells us *when* to look. The verdict comes
// from CheckSmoke — the same gate the poller runs, ArgoCD health plus
// the probe Job — and is pinned to the dev tip read before the probe.
// Without both of those wired, this path emits no signal at all and the
// poller remains the only route to a promote: losing webhook latency is
// strictly better than promoting on an unverified claim.
func (s *Server) handleArgoCD(w http.ResponseWriter, r *http.Request) {
	if !s.allow(w) {
		return
	}
	if !s.checkSharedSecret(w, r, s.ArgoSecret, "argocd") {
		return
	}
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var evt argoCDNotification
	if err := json.Unmarshal(body, &evt); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	app := firstNonEmpty(evt.App, evt.AppName, evt.Name, evt.Resource.Name)
	if app == "" {
		http.Error(w, "missing app name", http.StatusBadRequest)
		return
	}
	repo, ok := s.resolveArgoRepo(app)
	if !ok {
		s.Log.Warn("argocd webhook: unknown app", "app", app)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if s.CheckSmoke == nil || s.ResolveSHA == nil {
		s.Log.Warn("argocd webhook: no verifiable smoke check wired, ignoring notification",
			"app", app, "repo", repo)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Verifying costs real work (argocd + kubectl per delivery), so the
	// number of deliveries that can be verifying at once is bounded.
	release, ok := s.acquireSmokeSlot()
	if !ok {
		s.Log.Warn("argocd webhook: smoke checks busy, dropping notification", "repo", repo)
		http.Error(w, "smoke checks busy", http.StatusTooManyRequests)
		return
	}
	defer release()

	// The request context dies when this handler returns; the check must
	// not. Bound it instead so a wedged probe can't hold the handler
	// (and the shared rate-limited listener) open indefinitely.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), s.smokeTimeout())
	defer cancel()

	// Read the dev tip *before* the probe. A commit landing mid-probe
	// must not inherit the probe's verdict: pinned to the pre-check SHA,
	// the promote later refuses because origin/<dev> has moved.
	sha, err := s.ResolveSHA(ctx, repo)
	if err != nil || !isHexSHA(sha) {
		s.Log.Error("argocd webhook: cannot resolve dev tip, not emitting",
			"repo", repo, "sha", sha, "error", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	passed, gateEvidence, err := s.CheckSmoke(ctx, repo)
	if err != nil {
		s.Log.Error("argocd webhook: smoke check failed", "repo", repo, "error", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	kind := orchestrator.KindFail
	if passed {
		kind = orchestrator.KindPass
	}

	// The body's sync/health are recorded as *what we were told*, next to
	// the verdict we checked ourselves.
	evidence := map[string]any{
		"argocd_app":      app,
		"reported_sync":   firstNonEmpty(evt.Sync, evt.Status),
		"reported_health": evt.Health,
		"gated_sha":       sha,
		"via":             "webhook+probe",
	}
	for k, v := range gateEvidence {
		evidence[k] = v
	}

	s.emit(orchestrator.Signal{
		Source:   orchestrator.SourceDevGate,
		Repo:     repo,
		Kind:     kind,
		SHA:      sha,
		At:       time.Now().UTC(),
		Evidence: evidence,
	}, "argocd")
	w.WriteHeader(http.StatusAccepted)
}

// alertmanagerPayload is the webhook body Alertmanager sends.
type alertmanagerPayload struct {
	Status string `json:"status"` // firing | resolved
	Alerts []struct {
		Status string            `json:"status"`
		Labels map[string]string `json:"labels"`
	} `json:"alerts"`
}

// handleAlertmanager turns firing alerts into prod-health signals.
//
// SourceProdHealth + KindBreach is ActionRevert, so the `repo` label is
// as load-bearing as a SHA: it decides which repo gets a revert pushed
// to its prod branch. It is still only a label — this handler cannot
// verify that the alert is true, which is why the alert source needs a
// secret (`require_webhook_secret: true`) to be trusted at all. What it
// can do is refuse to invent repos: a label that does not resolve to a
// configured repo is dropped, instead of being carried into the
// orchestrator, which would spawn a worker goroutine and a 64-slot
// channel per unknown name for the lifetime of the process.
func (s *Server) handleAlertmanager(w http.ResponseWriter, r *http.Request) {
	if !s.allow(w) {
		return
	}
	if !s.checkSharedSecret(w, r, s.AMSecret, "alertmanager") {
		return
	}
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var payload alertmanagerPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}

	emitted := 0
	for _, a := range payload.Alerts {
		label := a.Labels["repo"]
		if label == "" {
			label = a.Labels["service"]
		}
		if label == "" {
			continue
		}
		repo, ok := s.resolveRepo(label)
		if !ok {
			s.Log.Warn("alertmanager webhook: unknown repo label, dropping alert", "label", label)
			continue
		}
		kind := orchestrator.KindBreach
		if a.Status == "resolved" || payload.Status == "resolved" {
			kind = orchestrator.KindPass
		}
		s.emit(orchestrator.Signal{
			Source: orchestrator.SourceProdHealth,
			Repo:   repo,
			Kind:   kind,
			At:     time.Now().UTC(),
			// No SHA: a breach says "prod is unhealthy now", and the
			// remediation target is prod's current tip, whatever it is.
			// dispatch.Revert re-reads origin/<prod> and pushes without
			// force, so a concurrent prod change fails the push rather
			// than being clobbered.
			Evidence: map[string]any{
				"alert_status": a.Status,
				"labels":       a.Labels,
				"via":          "webhook",
			},
		}, "alertmanager")
		emitted++
	}
	if emitted == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) allow(w http.ResponseWriter) bool {
	if s.Limiter == nil || s.Limiter.Allow() {
		return true
	}
	http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
	return false
}

// resolveRepo maps an external identifier (GitHub full name, alert
// label) to a configured repo's short name. A nil ResolveRepo is a
// misconfiguration, not a wildcard: it resolves nothing, so an
// unconfigured caller can't name a repo of its own choosing.
func (s *Server) resolveRepo(name string) (string, bool) {
	if name == "" || s.ResolveRepo == nil {
		return "", false
	}
	return s.ResolveRepo(name)
}

// resolveArgoRepo maps an ArgoCD Application name to a configured repo,
// falling back to the repo resolver for setups that name the app after
// the repo.
func (s *Server) resolveArgoRepo(app string) (string, bool) {
	if s.ResolveArgoApp != nil {
		if short, ok := s.ResolveArgoApp(app); ok {
			return short, true
		}
	}
	return s.resolveRepo(app)
}

// branchFor returns the dev branch configured for a resolved repo.
func (s *Server) branchFor(repo string) (string, bool) {
	if s.BranchFor != nil {
		if b := s.BranchFor(repo); b != "" {
			return b, true
		}
	}
	if s.DefaultBranch != "" {
		return s.DefaultBranch, true
	}
	return "", false
}

func (s *Server) smokeTimeout() time.Duration {
	if s.SmokeCheckTimeout > 0 {
		return s.SmokeCheckTimeout
	}
	return defaultSmokeCheckTimeout
}

// deliverySeen reports whether this delivery id was already handled.
func (s *Server) deliverySeen(id string) bool {
	s.lazyInit()
	return s.deliveries.seenBefore(id)
}

// acquireSmokeSlot takes one of the bounded check slots, returning false
// when they are all busy.
func (s *Server) acquireSmokeSlot() (release func(), ok bool) {
	s.lazyInit()
	select {
	case s.smokeSem <- struct{}{}:
		return func() { <-s.smokeSem }, true
	default:
		return nil, false
	}
}

// lazyInit builds the state a zero-value Server can't have — callers
// construct Server as a struct literal, so there is no constructor to
// hang it off.
func (s *Server) lazyInit() {
	s.initOnce.Do(func() {
		s.deliveries = newDedupe()
		n := s.MaxConcurrentChecks
		if n <= 0 {
			n = defaultMaxSmokeChecks
		}
		s.smokeSem = make(chan struct{}, n)
	})
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			return nil, false
		}
		http.Error(w, "read body", http.StatusBadRequest)
		return nil, false
	}
	return body, true
}

func (s *Server) checkSharedSecret(w http.ResponseWriter, r *http.Request, secret, name string) bool {
	if secret == "" {
		if s.RequireSecret {
			http.Error(w, "webhook secret required", http.StatusUnauthorized)
			return false
		}
		s.Log.Warn(name + " webhook: no secret configured, skipping verification")
		return true
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if got == "" {
		got = r.Header.Get("X-Webhook-Secret")
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
		http.Error(w, "invalid secret", http.StatusUnauthorized)
		return false
	}
	return true
}

func (s *Server) emit(sig orchestrator.Signal, source string) {
	s.Signals <- sig
	if s.Metrics != nil {
		s.Metrics.Webhooks.Add(context.Background(), 1, metric.WithAttributes(
			otel.AttrSource(source), otel.AttrKind(string(sig.Kind))))
	}
	s.Log.Info(source+" webhook: signal emitted", "repo", sig.Repo, "kind", sig.Kind, "sha", sig.SHA)
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// isHexSHA reports whether s is a bare git object name — lowercase hex,
// abbreviated (7) up to SHA-256 (64) length. Anything else (a ref name,
// "HEAD~1", something starting with "-") is refused before it can reach
// a git command line or a promote pin.
func isHexSHA(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

func verifySignature(secret, header string, body []byte) bool {
	const prefix = "sha256="
	if len(header) <= len(prefix) || header[:len(prefix)] != prefix {
		return false
	}
	expected, err := hex.DecodeString(header[len(prefix):])
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	got := mac.Sum(nil)

	return subtle.ConstantTimeCompare(expected, got) == 1
}
