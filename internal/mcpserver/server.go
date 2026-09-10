// Package mcpserver is the read-only MCP tool server a Fix agent can ask
// for more than the prompt carried: `xdlc mcp --session <id>`.
//
// The Fix prompt inlines the failed job's logs trimmed to 32 KB. On a
// large CI matrix the one useful line is often the one that got cut,
// and the agent had no way to ask for more. This server is that way.
// It is scoped to one session: the repo, the run and the recordings of
// that session, and nothing else the daemon knows.
//
// Every tool reads from disk or from the configured Prometheus. None of
// them holds a GitHub token, because the server runs inside the agent's
// scrubbed subprocess environment, which never has one. The daemon
// saves what GitHub had to say (ci-logs.txt, ci-run.json) into the
// session directory before the agent starts, and the tools read that.
//
// Every call is appended to the session's tools.jsonl, so "what did the
// agent look at" is answerable after the fact, next to what it was told
// and what it did.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
	"github.com/xdlc-labs/xdlc-agent/internal/lessons"
	"github.com/xdlc-labs/xdlc-agent/internal/promclient"
	"github.com/xdlc-labs/xdlc-agent/internal/session"
)

// ServerName is what the agent CLIs call this server, and the prefix
// they put on its tool names.
const ServerName = "xdlc"

// Options locate the one session this server answers for.
type Options struct {
	// SessionsDir is the store root; SessionID the directory under it.
	SessionsDir string
	SessionID   string
	// Root is the daemon's working directory, where LESSONS.md and
	// BACKLOG.md live. Empty disables the lessons and backlog tools'
	// file access (they answer "not available").
	Root string
	// ConfigPath is the daemon's config.yaml, for repo_config and the
	// prod_metrics queries. Empty disables both.
	ConfigPath string
	// Query overrides the Prometheus client, for tests. nil → real one
	// against the config's metrics_url.
	Query func(ctx context.Context, metricsURL, promQL string) (float64, error)
	// Now is the clock for tools.jsonl timestamps. nil → time.Now.
	Now func() time.Time
}

// Server holds the resolved session and serves its tools.
type Server struct {
	opts  Options
	store *session.Store
	meta  session.Meta
	cfg   *config.Config
	repo  *config.Repo
}

// New resolves the session and config. It fails when the session does
// not exist: a tool server for nothing is a misconfiguration worth
// hearing about at start rather than at the first call.
func New(opts Options) (*Server, error) {
	if opts.SessionID == "" || opts.SessionsDir == "" {
		return nil, fmt.Errorf("mcpserver: --session and --sessions-dir are required")
	}
	store, err := session.Open(opts.SessionsDir, 0, 0)
	if err != nil {
		return nil, err
	}
	meta, err := store.Load(opts.SessionID)
	if err != nil {
		return nil, fmt.Errorf("mcpserver: session %q: %w", opts.SessionID, err)
	}
	s := &Server{opts: opts, store: store, meta: meta}
	if opts.ConfigPath != "" {
		cfg, err := config.Load(opts.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("mcpserver: config: %w", err)
		}
		s.cfg = cfg
		for i := range cfg.Repos {
			if cfg.Repos[i].Name == meta.Repo {
				s.repo = &cfg.Repos[i]
			}
		}
	}
	if s.opts.Now == nil {
		s.opts.Now = time.Now
	}
	return s, nil
}

// Meta is the session this server is scoped to.
func (s *Server) Meta() session.Meta { return s.meta }

// MCP builds the protocol server with every tool registered.
func (s *Server) MCP(version string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: version}, &mcp.ServerOptions{
		Instructions: fmt.Sprintf("Read-only context for the xdlc Fix of repo %q (session %s). "+
			"ci_logs holds every failed job's complete log; the prompt only carried a slice of it.",
			s.meta.Repo, s.meta.ID),
	})
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}

	mcp.AddTool(srv, &mcp.Tool{Name: "ci_logs", Annotations: ro,
		Description: "Complete logs of every failed job in the CI run this Fix answers. " +
			"Filter with job (substring of the job name), grep (regexp, with two lines of context), " +
			"tail (last N lines; default 200). Output is capped at 64 KB; narrow with grep or job."},
		wrap(s, "ci_logs", s.ciLogs))
	mcp.AddTool(srv, &mcp.Tool{Name: "ci_run", Annotations: ro,
		Description: "The CI run's metadata: workflow, branch, commit, conclusion and failed jobs."},
		wrap(s, "ci_run", s.ciRun))
	mcp.AddTool(srv, &mcp.Tool{Name: "prod_metrics", Annotations: ro,
		Description: "Current production metrics for this repo from the configured Prometheus: " +
			"the p95 and error-rate queries from gates.prod-health, or a PromQL query of your own (instant)."},
		wrap(s, "prod_metrics", s.prodMetrics))
	mcp.AddTool(srv, &mcp.Tool{Name: "prior_sessions", Annotations: ro,
		Description: "Earlier Fix runs on this repo, newest first: what each changed, its verdict and the head of its patch. " +
			"limit defaults to 5."},
		wrap(s, "prior_sessions", s.priorSessions))
	mcp.AddTool(srv, &mcp.Tool{Name: "session_diff", Annotations: ro,
		Description: "The full patch an earlier Fix run on this repo produced, by session id (from prior_sessions)."},
		wrap(s, "session_diff", s.sessionDiff))
	mcp.AddTool(srv, &mcp.Tool{Name: "lessons", Annotations: ro,
		Description: "One line per past Fix outcome on this repo, from LESSONS.md."},
		wrap(s, "lessons", s.lessons))
	mcp.AddTool(srv, &mcp.Tool{Name: "backlog", Annotations: ro,
		Description: "The audit trail lines for this repo from BACKLOG.md: every Fix, Promote, Revert and noop with its evidence. " +
			"tail defaults to 50."},
		wrap(s, "backlog", s.backlog))
	mcp.AddTool(srv, &mcp.Tool{Name: "repo_config", Annotations: ro,
		Description: "How xdlc is configured for this repo: branches, gates, fix mode, worktree, and the gate settings."},
		wrap(s, "repo_config", s.repoConfig))
	return srv
}

// Run serves over t until the client disconnects or ctx ends.
func (s *Server) Run(ctx context.Context, version string, t mcp.Transport) error {
	return s.MCP(version).Run(ctx, t)
}

// toolFunc is a tool body: arguments in, text out.
type toolFunc[In any] func(ctx context.Context, in In) (string, error)

// wrap records the call, runs it, and turns the result into text
// content. A tool error is returned as an error result (IsError), not a
// protocol error, so the agent reads the reason and moves on.
func wrap[In any](s *Server, name string, fn toolFunc[In]) mcp.ToolHandlerFor[In, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		text, err := fn(ctx, in)
		s.record(name, in, len(text), err)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		if text == "" {
			text = "(empty)"
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
	}
}

// record appends one line to tools.jsonl. Best effort: a logging
// failure must not fail the tool.
func (s *Server) record(tool string, args any, bytes int, callErr error) {
	entry := map[string]any{
		"ts":    s.opts.Now().UTC().Format(time.RFC3339),
		"tool":  tool,
		"args":  args,
		"bytes": bytes,
	}
	if callErr != nil {
		entry["error"] = callErr.Error()
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_ = s.store.AppendLine(s.meta.ID, session.FileTools, string(line))
}

// --- ci_logs -----------------------------------------------------------

// CILogsArgs are ci_logs' arguments.
type CILogsArgs struct {
	Job  string `json:"job,omitempty" jsonschema:"substring of a job name; empty means every failed job"`
	Grep string `json:"grep,omitempty" jsonschema:"Go regexp; matching lines are returned with two lines of context"`
	Tail int    `json:"tail,omitempty" jsonschema:"last N lines after filtering; default 200, 0 means default"`
}

// maxToolBytes caps one tool result. A 64 KB answer is already more than
// a prompt could carry; past that, the agent should narrow the query.
const maxToolBytes = 64 << 10

const jobHeaderPrefix = "=== job: "

// FormatCILogs renders job logs into the ci-logs.txt layout ci_logs
// parses: one header line per job, then its text.
func FormatCILogs(jobs []JobLog) string {
	var b strings.Builder
	for _, j := range jobs {
		fmt.Fprintf(&b, "%s%s (%s) ===\n", jobHeaderPrefix, j.Name, j.Conclusion)
		b.WriteString(j.Text)
		if !strings.HasSuffix(j.Text, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// JobLog is one failed job's log, as saved by the daemon.
type JobLog struct {
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
	Text       string `json:"-"`
}

// parseCILogs splits ci-logs.txt back into jobs.
func parseCILogs(text string) []JobLog {
	var jobs []JobLog
	var cur *JobLog
	var buf strings.Builder
	flush := func() {
		if cur != nil {
			cur.Text = buf.String()
			jobs = append(jobs, *cur)
		}
		buf.Reset()
	}
	// Sections are newline-terminated; without this the final split
	// element is an empty line that would become a spurious blank.
	text = strings.TrimSuffix(text, "\n")
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(line, jobHeaderPrefix) && strings.HasSuffix(line, " ===") {
			flush()
			head := strings.TrimSuffix(strings.TrimPrefix(line, jobHeaderPrefix), " ===")
			name, concl := head, ""
			if i := strings.LastIndex(head, " ("); i >= 0 && strings.HasSuffix(head, ")") {
				name, concl = head[:i], head[i+2:len(head)-1]
			}
			cur = &JobLog{Name: name, Conclusion: concl}
			continue
		}
		if cur == nil {
			// Text before any header: a file written without sections.
			cur = &JobLog{Name: "log"}
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	flush()
	return jobs
}

func (s *Server) ciLogs(_ context.Context, in CILogsArgs) (string, error) {
	raw, err := s.store.ReadFile(s.meta.ID, session.FileCILogs)
	if err != nil {
		return "", err
	}
	if raw == "" {
		return "", fmt.Errorf("no CI logs were saved for this session (source %q); the prompt's evidence is all there is", s.meta.Source)
	}
	jobs := parseCILogs(raw)
	var re *regexp.Regexp
	if in.Grep != "" {
		re, err = regexp.Compile(in.Grep)
		if err != nil {
			return "", fmt.Errorf("grep: %w", err)
		}
	}
	tail := in.Tail
	if tail <= 0 {
		tail = 200
	}
	var b strings.Builder
	matched := 0
	for _, j := range jobs {
		if in.Job != "" && !strings.Contains(strings.ToLower(j.Name), strings.ToLower(in.Job)) {
			continue
		}
		matched++
		lines := strings.Split(strings.TrimRight(j.Text, "\n"), "\n")
		if re != nil {
			lines = grepContext(lines, re, 2)
		}
		if len(lines) > tail {
			lines = lines[len(lines)-tail:]
		}
		fmt.Fprintf(&b, "%s%s (%s) === %d lines shown\n", jobHeaderPrefix, j.Name, j.Conclusion, len(lines))
		b.WriteString(strings.Join(lines, "\n"))
		b.WriteByte('\n')
	}
	if matched == 0 {
		names := make([]string, 0, len(jobs))
		for _, j := range jobs {
			names = append(names, j.Name)
		}
		return "", fmt.Errorf("no failed job matches %q; failed jobs: %s", in.Job, strings.Join(names, ", "))
	}
	return capText(b.String()), nil
}

// grepContext keeps lines matching re plus n lines around each, with
// "--" between non-adjacent groups, like grep -C.
func grepContext(lines []string, re *regexp.Regexp, n int) []string {
	keep := make([]bool, len(lines))
	for i, l := range lines {
		if re.MatchString(l) {
			for j := max(0, i-n); j <= min(len(lines)-1, i+n); j++ {
				keep[j] = true
			}
		}
	}
	var out []string
	last := -2
	for i, k := range keep {
		if !k {
			continue
		}
		if last >= 0 && i != last+1 {
			out = append(out, "--")
		}
		out = append(out, lines[i])
		last = i
	}
	return out
}

func capText(s string) string {
	if len(s) <= maxToolBytes {
		return s
	}
	return s[:maxToolBytes] + fmt.Sprintf("\n...(capped at %d KB; narrow with grep, job or tail)\n", maxToolBytes>>10)
}

// --- ci_run ------------------------------------------------------------

// CIRunArgs are ci_run's arguments. run_url is accepted for symmetry
// with the roadmap but the server answers for its own session's run.
type CIRunArgs struct {
	RunURL string `json:"run_url,omitempty" jsonschema:"ignored; the server is scoped to one run"`
}

func (s *Server) ciRun(_ context.Context, _ CIRunArgs) (string, error) {
	raw, err := s.store.ReadFile(s.meta.ID, session.FileCIRun)
	if err != nil {
		return "", err
	}
	if raw == "" {
		if s.meta.RunURL == "" {
			return "", fmt.Errorf("this session was not started by a CI run (source %q)", s.meta.Source)
		}
		return fmt.Sprintf(`{"run_url": %q}`, s.meta.RunURL), nil
	}
	return raw, nil
}

// --- prod_metrics ------------------------------------------------------

// ProdMetricsArgs are prod_metrics' arguments.
type ProdMetricsArgs struct {
	Query string `json:"query,omitempty" jsonschema:"PromQL instant query; empty runs the configured p95 and error-rate queries"`
}

func (s *Server) prodMetrics(ctx context.Context, in ProdMetricsArgs) (string, error) {
	if s.cfg == nil {
		return "", fmt.Errorf("prod_metrics needs the daemon config; none was given to this server")
	}
	ph := s.cfg.Gates.ProdHealth
	if ph.MetricsURL == "" {
		ph.MetricsURL = ph.PrometheusURL
	}
	if ph.MetricsURL == "" {
		return "", fmt.Errorf("gates.prod-health.metrics_url is not configured; no Prometheus to ask")
	}
	query := s.opts.Query
	if query == nil {
		query = func(ctx context.Context, metricsURL, promQL string) (float64, error) {
			return promclient.New(metricsURL).Query(ctx, promQL)
		}
	}
	queries := map[string]string{}
	if in.Query != "" {
		queries["query"] = in.Query
	} else {
		sub := strings.NewReplacer("{{repo}}", s.meta.Repo)
		if ph.P95Query != "" {
			queries["p95_ms"] = sub.Replace(ph.P95Query)
		}
		if ph.ErrorRateQuery != "" {
			queries["error_rate"] = sub.Replace(ph.ErrorRateQuery)
		}
		if len(queries) == 0 {
			return "", fmt.Errorf("gates.prod-health has no p95_query or error_rate_query; pass a query")
		}
	}
	out := map[string]any{"metrics_url": ph.MetricsURL}
	for name, q := range queries {
		v, err := query(ctx, ph.MetricsURL, q)
		entry := map[string]any{"query": q}
		switch {
		case err != nil:
			entry["error"] = err.Error()
		default:
			entry["value"] = v
		}
		out[name] = entry
	}
	if in.Query == "" {
		out["thresholds"] = map[string]any{"p95_ms": ph.Thresholds.P95MS, "error_rate": ph.Thresholds.ErrorRate}
	}
	raw, _ := json.MarshalIndent(out, "", "  ")
	return string(raw), nil
}

// --- prior_sessions / session_diff ------------------------------------

// PriorSessionsArgs are prior_sessions' arguments.
type PriorSessionsArgs struct {
	Limit int `json:"limit,omitempty" jsonschema:"how many earlier runs; default 5"`
}

func (s *Server) priorSessions(_ context.Context, in PriorSessionsArgs) (string, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 5
	}
	prior := s.store.Recent(s.meta.Repo, "", s.meta.ID, limit, session.DefaultPriorDiffLines)
	if len(prior) == 0 {
		return "no earlier Fix runs recorded for this repo", nil
	}
	raw, _ := json.MarshalIndent(prior, "", "  ")
	return capText(string(raw)), nil
}

// SessionDiffArgs are session_diff's arguments.
type SessionDiffArgs struct {
	ID string `json:"id" jsonschema:"session id from prior_sessions"`
}

func (s *Server) sessionDiff(_ context.Context, in SessionDiffArgs) (string, error) {
	if in.ID == "" {
		return "", fmt.Errorf("id is required")
	}
	meta, err := s.store.Load(in.ID)
	if err != nil {
		return "", fmt.Errorf("unknown session %q", in.ID)
	}
	// Scoped: a Fix on one repo does not get to read another repo's
	// recordings, even on the same daemon.
	if meta.Repo != s.meta.Repo {
		return "", fmt.Errorf("session %q belongs to another repo", in.ID)
	}
	patch, err := s.store.ReadFile(in.ID, session.FileDiff)
	if err != nil {
		return "", err
	}
	if patch == "" {
		return "that run delivered no patch", nil
	}
	return capText(patch), nil
}

// --- lessons / backlog / repo_config ------------------------------------

// NoArgs is the empty argument object.
type NoArgs struct{}

func (s *Server) lessons(_ context.Context, _ NoArgs) (string, error) {
	if s.opts.Root == "" {
		return "", fmt.Errorf("lessons are not available to this server (no --root)")
	}
	ls, err := lessons.Open(filepath.Join(s.opts.Root, "LESSONS.md"))
	if err != nil {
		return "", err
	}
	out := ls.ForRepo(s.meta.Repo, 50)
	if strings.TrimSpace(out) == "" {
		return "no lessons recorded for this repo", nil
	}
	return out, nil
}

// BacklogArgs are backlog's arguments.
type BacklogArgs struct {
	Tail int `json:"tail,omitempty" jsonschema:"last N lines for this repo; default 50"`
}

func (s *Server) backlog(_ context.Context, in BacklogArgs) (string, error) {
	if s.opts.Root == "" {
		return "", fmt.Errorf("the backlog is not available to this server (no --root)")
	}
	raw, err := os.ReadFile(filepath.Join(s.opts.Root, "BACKLOG.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return "no BACKLOG.md yet", nil
		}
		return "", err
	}
	needle := "repo=" + s.meta.Repo + " "
	var lines []string
	for line := range strings.SplitSeq(string(raw), "\n") {
		if strings.Contains(line, needle) {
			lines = append(lines, line)
		}
	}
	tail := in.Tail
	if tail <= 0 {
		tail = 50
	}
	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	if len(lines) == 0 {
		return "no backlog lines for this repo", nil
	}
	return capText(strings.Join(lines, "\n")), nil
}

func (s *Server) repoConfig(_ context.Context, _ NoArgs) (string, error) {
	if s.cfg == nil {
		return "", fmt.Errorf("repo_config needs the daemon config; none was given to this server")
	}
	out := map[string]any{
		"session": map[string]any{
			"id": s.meta.ID, "repo": s.meta.Repo, "source": s.meta.Source, "kind": s.meta.Kind,
			"provider": s.meta.Provider, "fix_mode": s.meta.FixMode, "run_url": s.meta.RunURL,
		},
		"agent": map[string]any{
			"fix_mode": s.cfg.Agent.FixMode, "worktree": s.cfg.Agent.WorktreeEnabled(),
			"fix_reverify": s.cfg.Agent.FixReverify, "fix_attempts": s.cfg.Agent.FixAttempts,
		},
		"gates": s.cfg.Gates,
	}
	if s.repo != nil {
		out["repo"] = s.repo
	} else {
		out["repo"] = map[string]any{"name": s.meta.Repo, "note": "not in this config's repos[]"}
	}
	// YAML, not JSON: the config types carry yaml tags, so this reads
	// exactly like the config.yaml the operator wrote.
	raw, err := yaml.Marshal(out)
	if err != nil {
		return "", err
	}
	return capText(string(raw)), nil
}
