// Package api serves the dashboard JSON endpoints on the daemon HTTP
// mux (alongside webhooks), plus operator write actions that enqueue
// synthetic signals onto the orchestrator.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
	"github.com/xdlc-labs/xdlc-agent/internal/orchestrator"
	"github.com/xdlc-labs/xdlc-agent/internal/repos"
	"github.com/xdlc-labs/xdlc-agent/internal/store"
)

// Server holds deps for /api/* handlers.
type Server struct {
	Cfg         *config.Config
	CfgPath     string
	Audit       *store.AuditStore
	BacklogPath string
	Version     string
	Started     time.Time
	Log         *slog.Logger
	// Token is the operator bearer secret. Empty → fail closed (503)
	// on protected routes; /api/health stays open.
	Token string
	// ViewerToken is the optional read-only bearer. Empty → no viewer role.
	ViewerToken string
	// SessionVerifier optionally checks a request for a role via some
	// other mechanism (OIDC SSO session cookie — internal/authn.Authenticator.VerifySession)
	// in addition to the bearer tokens above. nil → bearer-only, the
	// original behavior. Additive: either method authenticates a request.
	SessionVerifier func(r *http.Request) (role string, ok bool)
	// Enqueue injects a signal onto the orchestrator; nil → writes 503.
	Enqueue func(orchestrator.Signal)
	// PRStatus optionally re-checks a Fix PR against GitHub. githubRepo
	// is "owner/name"; number is the PR number. nil → snapshot-only
	// evidence from the audit store (legacy 2.8 behavior).
	PRStatus func(ctx context.Context, githubRepo string, number int) (state string, merged bool, err error)
}

// Mount registers dashboard routes on mux.
func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.Handle("GET /api/whoami", s.requireAuth(http.HandlerFunc(s.handleWhoami)))
	mux.Handle("GET /api/overview", s.requireAuth(http.HandlerFunc(s.handleOverview)))
	mux.Handle("GET /api/history", s.requireAuth(http.HandlerFunc(s.handleHistory)))
	mux.Handle("GET /api/backlog", s.requireAuth(http.HandlerFunc(s.handleBacklog)))
	mux.Handle("GET /api/repos", s.requireAuth(http.HandlerFunc(s.handleRepos)))
	mux.Handle("GET /api/prs", s.requireAuth(http.HandlerFunc(s.handlePRs)))
	mux.Handle("GET /api/kpis", s.requireAuth(http.HandlerFunc(s.handleKPIs)))
	mux.Handle("POST /api/actions/fix", s.requireOperator(http.HandlerFunc(s.handleActionFix)))
	mux.Handle("POST /api/actions/promote", s.requireOperator(http.HandlerFunc(s.handleActionPromote)))
	mux.Handle("POST /api/actions/revert", s.requireOperator(http.HandlerFunc(s.handleActionRevert)))
}

func bearerToken(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func tokenMatch(got, want string) bool {
	if want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// authenticate returns the caller's role ("operator" or "viewer") via
// bearer token or, if configured, an OIDC session cookie — whichever
// matches. Bearer tokens are checked first (cheap, no cookie parsing).
func (s *Server) authenticate(r *http.Request) (role string, ok bool) {
	got := bearerToken(r)
	if tokenMatch(got, s.Token) {
		return "operator", true
	}
	if tokenMatch(got, s.ViewerToken) {
		return "viewer", true
	}
	if s.SessionVerifier != nil {
		if role, ok := s.SessionVerifier(r); ok {
			return role, true
		}
	}
	return "", false
}

// unconfigured reports whether no authentication method is set up at
// all — the fail-closed 503 case (vs. 401 for a request that's simply
// missing/wrong credentials).
func (s *Server) unconfigured() bool {
	return s.Token == "" && s.SessionVerifier == nil
}

// requireAuth accepts operator or viewer role (GETs).
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.authenticate(r); ok {
			next.ServeHTTP(w, r)
			return
		}
		if s.unconfigured() {
			http.Error(w, "API token not configured", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

// requireOperator accepts only the operator role (writes). Viewer → 403.
func (s *Server) requireOperator(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, ok := s.authenticate(r)
		if ok && role == "operator" {
			next.ServeHTTP(w, r)
			return
		}
		if ok {
			http.Error(w, "forbidden: operator role required", http.StatusForbidden)
			return
		}
		if s.unconfigured() {
			http.Error(w, "API token not configured", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

type actionBody struct {
	Repo    string `json:"repo"`
	Confirm bool   `json:"confirm"`
}

func (s *Server) handleActionFix(w http.ResponseWriter, r *http.Request) {
	s.handleAction(w, r, "fix")
}

func (s *Server) handleActionPromote(w http.ResponseWriter, r *http.Request) {
	s.handleAction(w, r, "promote")
}

func (s *Server) handleActionRevert(w http.ResponseWriter, r *http.Request) {
	s.handleAction(w, r, "revert")
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request, action string) {
	if s.Enqueue == nil {
		http.Error(w, "actions not available", http.StatusServiceUnavailable)
		return
	}
	var body actionBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if body.Repo == "" {
		http.Error(w, `repo required`, http.StatusBadRequest)
		return
	}
	if !body.Confirm {
		http.Error(w, `confirm: true required`, http.StatusBadRequest)
		return
	}
	sig, ok := signalForManualAction(action, body.Repo)
	if !ok {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if s.Log != nil {
		s.Log.Info("manual action enqueued", "action", action, "repo", body.Repo,
			"source", sig.Source, "kind", sig.Kind)
	}
	s.Enqueue(sig)
	writeJSON(w, map[string]any{
		"enqueued": true,
		"action":   action,
		"repo":     body.Repo,
		"source":   string(sig.Source),
		"kind":     string(sig.Kind),
	})
}

// signalForManualAction maps console write actions onto Source/Kind pairs
// that Decide() already understands — no Action bypass field.
func signalForManualAction(action, repo string) (orchestrator.Signal, bool) {
	sig := orchestrator.Signal{
		Repo:     repo,
		At:       time.Now().UTC(),
		Evidence: map[string]any{"manual": true, "via": "api", "action": action},
	}
	switch action {
	case "fix":
		// Decide: SourceCI + KindFail → ActionFix
		sig.Source, sig.Kind = orchestrator.SourceCI, orchestrator.KindFail
	case "promote":
		// Decide: SourceDevGate + KindPass → ActionPromote
		sig.Source, sig.Kind = orchestrator.SourceDevGate, orchestrator.KindPass
	case "revert":
		// Decide: SourceProdHealth + KindBreach → ActionRevert
		sig.Source, sig.Kind = orchestrator.SourceProdHealth, orchestrator.KindBreach
	default:
		return orchestrator.Signal{}, false
	}
	return sig, true
}

// handleWhoami reports the caller's own role — lets the console show/hide
// operator-only UI and a "signed in as" indicator without guessing from
// 403s. Behind requireAuth: reaching this handler at all means role is set.
func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	role, _ := s.authenticate(r)
	writeJSON(w, map[string]any{"role": role})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"status":        "running",
		"version":       s.Version,
		"agentProvider": s.Cfg.Agent.Provider,
		"configPath":    s.CfgPath,
		"uptime":        formatUptime(time.Since(s.Started)),
		"addr":          s.Cfg.Server.Addr,
	})
}

func (s *Server) handleBacklog(w http.ResponseWriter, r *http.Request) {
	path := s.BacklogPath
	if path == "" {
		path = "BACKLOG.md"
	}
	b, err := os.ReadFile(path) //nolint:gosec // path from server config / default BACKLOG.md
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	md := string(b)
	if repo := r.URL.Query().Get("repo"); repo != "" {
		md = filterBacklogMarkdown(md, repo)
	}
	writeJSON(w, map[string]any{"markdown": md})
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	records, err := s.Audit.All()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if repo := r.URL.Query().Get("repo"); repo != "" {
		filtered := records[:0:0]
		for _, rec := range records {
			if rec.Repo == repo {
				filtered = append(filtered, rec)
			}
		}
		records = filtered
	}
	sort.Slice(records, func(i, j int) bool { return records[i].At.After(records[j].At) })
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if len(records) > limit {
		records = records[:limit]
	}
	events := make([]map[string]any, 0, len(records))
	for _, rec := range records {
		events = append(events, recordToEvent(rec))
	}
	writeJSON(w, map[string]any{"events": events})
}

func (s *Server) handleRepos(w http.ResponseWriter, r *http.Request) {
	records, err := s.Audit.All()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sort.Slice(records, func(i, j int) bool { return records[i].At.After(records[j].At) })
	writeJSON(w, map[string]any{"repos": s.buildRepos(records)})
}

// handlePRs is the Fix-PR work queue: every fix_mode: pr
// dispatch that resulted in a PR, one row per repo+branch, latest
// first. When PRStatus is set, each row is re-checked against GitHub
// (mapped via config repos[].github). Default response is open,
// non-merged only; pass ?all=1 for closed/merged history too.
func (s *Server) handlePRs(w http.ResponseWriter, r *http.Request) {
	records, err := s.Audit.All()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sort.Slice(records, func(i, j int) bool { return records[i].At.After(records[j].At) })

	type prRow struct {
		Repo   string `json:"repo"`
		Branch string `json:"branch"`
		Number int    `json:"number"`
		URL    string `json:"url"`
		State  string `json:"state"`
		Merged bool   `json:"merged"`
		Stale  bool   `json:"stale,omitempty"`
		At     string `json:"at"`
	}
	seen := map[string]bool{} // repo+branch — keep only the latest record per PR
	var prs []prRow
	for _, rec := range records {
		if rec.Action != "fix" {
			continue
		}
		url, _ := rec.Evidence["pr_url"].(string)
		if url == "" {
			continue
		}
		branch, _ := rec.Evidence["pr_branch"].(string)
		key := rec.Repo + "\x00" + branch
		if seen[key] {
			continue
		}
		seen[key] = true
		number, _ := rec.Evidence["pr_number"].(float64) // JSON round-trip through bbolt decodes numbers as float64
		state, _ := rec.Evidence["pr_state"].(string)
		prs = append(prs, prRow{
			Repo: rec.Repo, Branch: branch, Number: int(number), URL: url, State: state,
			At: rec.At.UTC().Format("2006-01-02 15:04:05Z"),
		})
	}

	if s.PRStatus != nil && len(prs) > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		sem := make(chan struct{}, 4)
		var wg sync.WaitGroup
		for i := range prs {
			ghRepo := s.githubFor(prs[i].Repo)
			if ghRepo == "" || prs[i].Number <= 0 {
				continue
			}
			wg.Add(1)
			go func(i int, ghRepo string) {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-ctx.Done():
					prs[i].Stale = true
					return
				}
				state, merged, err := s.PRStatus(ctx, ghRepo, prs[i].Number)
				if err != nil {
					prs[i].Stale = true
					return
				}
				prs[i].State = state
				prs[i].Merged = merged
			}(i, ghRepo)
		}
		wg.Wait()
	}

	wantAll := r.URL.Query().Get("all") == "1"
	if !wantAll {
		filtered := prs[:0]
		for _, p := range prs {
			if p.State == "open" && !p.Merged {
				filtered = append(filtered, p)
			}
		}
		prs = filtered
	}
	if prs == nil {
		prs = []prRow{}
	}
	writeJSON(w, map[string]any{"prs": prs})
}

// githubFor maps a config short repo name to repos[].github ("owner/name").
func (s *Server) githubFor(name string) string {
	if s.Cfg == nil {
		return ""
	}
	for _, r := range s.Cfg.Repos {
		if r.Name == name {
			return r.GitHub
		}
	}
	return ""
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	records, err := s.Audit.All()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sort.Slice(records, func(i, j int) bool { return records[i].At.After(records[j].At) })

	backlog := ""
	path := s.BacklogPath
	if path == "" {
		path = "BACKLOG.md"
	}
	if b, err := os.ReadFile(path); err == nil { //nolint:gosec // path from server config / default BACKLOG.md
		backlog = string(b)
	}

	events := make([]map[string]any, 0, min(10, len(records)))
	for i, rec := range records {
		if i >= 10 {
			break
		}
		events = append(events, recordToEvent(rec))
	}

	writeJSON(w, map[string]any{
		"daemon": map[string]any{
			"status":        "running",
			"version":       s.Version,
			"env":           "local",
			"uptime":        formatUptime(time.Since(s.Started)),
			"webhook":       "receiving → " + orDefault(s.Cfg.Server.Addr, ":8080"),
			"configPath":    s.CfgPath,
			"gitopsDir":     "gitops",
			"agentProvider": orDefault(s.Cfg.Agent.Provider, "claude"),
		},
		"pipeline":  s.buildPipeline(records),
		"kpis":      s.buildKPIs(records),
		"gates":     s.buildGates(records),
		"repos":     s.buildRepos(records),
		"events":    events,
		"backlogMd": backlog,
	})
}

func recordToEvent(r store.Record) map[string]any {
	ev := evidenceString(r.Evidence)
	url, _ := r.Evidence["run_url"].(string)
	if url == "" {
		url, _ = r.Evidence["url"].(string)
	}
	// ok = gate happy OR an action was taken (fix/promote/revert recorded)
	ok := r.Kind == "pass" || r.Action == "fix" || r.Action == "promote" || r.Action == "revert"
	return map[string]any{
		"id":       fmt.Sprintf("%s-%s-%s", r.At.UTC().Format("20060102150405"), r.Repo, r.Source),
		"ts":       r.At.UTC().Format("2006-01-02 15:04:05Z"),
		"repo":     r.Repo,
		"source":   mapSource(r.Source),
		"gate":     mapGate(r.Source),
		"signal":   r.Kind,
		"action":   mapAction(r.Action),
		"ok":       ok,
		"evidence": ev,
		"url":      url,
	}
}

func (s *Server) buildRepos(records []store.Record) []map[string]any {
	latest := map[string]store.Record{}
	for _, r := range records {
		if _, ok := latest[r.Repo]; !ok {
			latest[r.Repo] = r
		}
	}
	out := make([]map[string]any, 0, len(s.Cfg.Repos))
	for _, repo := range s.Cfg.Repos {
		branch := repo.Branch
		if branch == "" {
			branch = repos.DefaultBranch
		}
		rec, has := latest[repo.Name]
		lastGate := "CI"
		lastGateStatus := "idle"
		lastAction := "None"
		lastActionAt := "—"
		health := "healthy"
		if has {
			lastGate = mapGate(rec.Source)
			lastGateStatus = mapKindStatus(rec.Kind)
			lastAction = mapAction(rec.Action)
			lastActionAt = rec.At.UTC().Format("2006-01-02 15:04:05Z")
			health = mapHealth(rec)
		}
		app := repo.ArgoCDApp
		if app == "" {
			app = s.Cfg.Gates.DevSmoke.ArgoCDApp
		}
		out = append(out, map[string]any{
			"id":             repo.Name,
			"name":           repo.GitHub,
			"branch":         branch,
			"lastGate":       lastGate,
			"lastGateStatus": lastGateStatus,
			"lastAction":     lastAction,
			"lastActionAt":   lastActionAt,
			// ponytail: image tags live in gitops yaml; wire later if UI needs them
			"devTag":      "—",
			"prodTag":     "—",
			"health":      health,
			"cloneStatus": "repos/" + repo.Name,
			"lastPromote": "—",
			"lastRevert":  "—",
			"argocdApp":   app,
			"sloQueries": []map[string]string{
				{"label": "p95", "query": s.Cfg.Gates.ProdHealth.P95Query},
				{"label": "error rate", "query": s.Cfg.Gates.ProdHealth.ErrorRateQuery},
			},
		})
	}
	return out
}

func (s *Server) buildGates(records []store.Record) []map[string]any {
	sources := []struct {
		src, name, provider, interval, trigger string
	}{
		{"ci", "CI", "GitHub Actions", "edge-triggered (webhook)", "webhook"},
		{"dev-gate", "DEV smoke", "ArgoCD + k6 / Playwright", s.Cfg.Gates.DevSmoke.Interval.String(), s.Cfg.Gates.DevSmoke.Trigger},
		{"prod-health", "PROD health", "Prometheus", s.Cfg.Gates.ProdHealth.Interval.String(), s.Cfg.Gates.ProdHealth.Trigger},
	}
	latest := map[string]store.Record{}
	for _, r := range records {
		if _, ok := latest[r.Source]; !ok {
			latest[r.Source] = r
		}
	}
	out := make([]map[string]any, 0, len(sources))
	for _, g := range sources {
		status := "idle"
		lastCheck := "—"
		evidence := "no signals yet"
		url := ""
		if rec, ok := latest[g.src]; ok {
			status = mapKindStatus(rec.Kind)
			if rec.Action == "fix" || rec.Action == "promote" || rec.Action == "revert" {
				if rec.Kind == "pass" {
					status = "pass"
				} else if g.src == "dev-gate" && rec.Action == "fix" {
					status = "acting"
				}
			}
			lastCheck = rec.At.UTC().Format("2006-01-02 15:04:05Z")
			evidence = evidenceString(rec.Evidence)
			url, _ = rec.Evidence["run_url"].(string)
		}
		out = append(out, map[string]any{
			"name":      g.name,
			"provider":  g.provider,
			"status":    status,
			"lastCheck": lastCheck,
			"interval":  orDefault(g.interval, "—"),
			"trigger":   orDefault(g.trigger, "—"),
			"evidence":  evidence,
			"url":       url,
		})
	}
	return out
}

func (s *Server) buildPipeline(records []store.Record) []map[string]any {
	gates := s.buildGates(records)
	byName := map[string]map[string]any{}
	for _, g := range gates {
		byName[g["name"].(string)] = g
	}
	reposN := len(s.Cfg.Repos)
	stages := []struct {
		stage, label, fallbackDetail string
		gateName                     string
	}{
		{"github", "GitHub", fmt.Sprintf("%d repos watched", reposN), ""},
		{"ci", "CI gate", "idle", "CI"},
		{"dev", "DEV smoke", "idle", "DEV smoke"},
		{"promote", "Promote", "develop→main ff-only", ""},
		{"prod", "PROD health", "idle", "PROD health"},
	}
	out := make([]map[string]any, 0, len(stages))
	for _, st := range stages {
		status := "idle"
		detail := st.fallbackDetail
		if st.stage == "github" {
			status = "pass"
		}
		if st.gateName != "" {
			if g, ok := byName[st.gateName]; ok {
				status, _ = g["status"].(string)
				if ev, _ := g["evidence"].(string); ev != "" && ev != "no signals yet" {
					detail = firstLine(ev)
				}
			}
		}
		// promote: waiting unless latest action was promote
		if st.stage == "promote" {
			status = "waiting"
			for _, r := range records {
				if r.Action == "promote" {
					status = "pass"
					detail = r.Repo + " promoted"
					break
				}
			}
		}
		out = append(out, map[string]any{
			"stage":  st.stage,
			"label":  st.label,
			"status": status,
			"detail": detail,
		})
	}
	return out
}

func (s *Server) buildKPIs(records []store.Record) map[string]any {
	day := time.Now().UTC().Format("2006-01-02")
	var fixes, promotes, reverts int
	lastActionAt := "—"
	for _, r := range records {
		if r.At.UTC().Format("2006-01-02") != day {
			continue
		}
		switch r.Action {
		case "fix":
			fixes++
		case "promote":
			promotes++
		case "revert":
			reverts++
		}
		if lastActionAt == "—" && r.Action != "noop" {
			lastActionAt = r.At.UTC().Format("2006-01-02 15:04:05Z")
		}
	}
	return map[string]any{
		"reposWatched": len(s.Cfg.Repos),
		"fixes":        fixes,
		"promotes":     promotes,
		"reverts":      reverts,
		"lastActionAt": lastActionAt,
		"backlogOpen":  0, // ponytail: no open-item counter yet
	}
}

func mapAction(a string) string {
	switch a {
	case "fix":
		return "Fix"
	case "promote":
		return "Promote"
	case "revert":
		return "Revert"
	case "rerun":
		return "Rerun"
	default:
		return "None"
	}
}

func mapGate(source string) string {
	switch source {
	case "ci":
		return "CI"
	case "dev-gate":
		return "DEV smoke"
	case "prod-health":
		return "PROD health"
	default:
		return source
	}
}

func mapSource(source string) string {
	switch source {
	case "ci":
		return "github-actions"
	case "dev-gate":
		return "argocd"
	case "prod-health":
		return "prometheus"
	default:
		return "daemon"
	}
}

func mapKindStatus(kind string) string {
	switch kind {
	case "pass":
		return "pass"
	case "fail", "breach":
		return "fail"
	default:
		return "idle"
	}
}

func mapHealth(r store.Record) string {
	if r.Source == "prod-health" && r.Kind == "breach" {
		return "breach"
	}
	if r.Kind == "fail" {
		return "degraded"
	}
	return "healthy"
}

func evidenceString(ev map[string]any) string {
	if len(ev) == 0 {
		return ""
	}
	keys := make([]string, 0, len(ev))
	for k := range ev {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, ev[k]))
	}
	return strings.Join(parts, " ")
}

func formatUptime(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h >= 24 {
		return fmt.Sprintf("%dd %02dh %02dm", h/24, h%24, m)
	}
	return fmt.Sprintf("%dh %02dm", h, m)
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}

// filterBacklogMarkdown keeps lines that mention repo (exact substring).
// Backlog.Record format: `- [ts] repo=<name> action=...`.
func filterBacklogMarkdown(md, repo string) string {
	lines := strings.Split(md, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.Contains(line, repo) {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
