package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/xdlc-labs/xdlc-agent/internal/session"
)

// fixture builds a daemon root with a config, a LESSONS.md, a BACKLOG.md,
// and one session for repo "svc" holding two failed jobs' logs, plus an
// earlier finished session with a patch.
func fixture(t *testing.T) (opts Options, priorID string) {
	t.Helper()
	root := t.TempDir()
	cfg := filepath.Join(root, "config.yaml")
	must(t, os.WriteFile(cfg, []byte(`
repos:
  - name: svc
    github: acme/svc
    branch: main
    gates: [ci, prod-health]
  - name: other
    github: acme/other
    gates: [ci]
server:
  addr: "127.0.0.1:8080"
gates:
  ci:
    trigger: on_push
  prod-health:
    trigger: continuous
    metrics_url: http://prom.test
    thresholds: { p95_ms: 500, error_rate: 0.01 }
    p95_query: p95{service="{{repo}}"}
    error_rate_query: errs{service="{{repo}}"}
`), 0o600))
	must(t, os.WriteFile(filepath.Join(root, "LESSONS.md"),
		[]byte("# LESSONS\n\n- [2026-09-01T00:00:00Z] repo=svc source=ci outcome=ok symptom=flaky seed\n- [2026-09-01T00:00:00Z] repo=other source=ci outcome=error symptom=x\n"), 0o600))
	must(t, os.WriteFile(filepath.Join(root, "BACKLOG.md"),
		[]byte("# BACKLOG\n\n## Log\n- [t1] repo=svc action=fix via=webhook \n- [t2] repo=other action=revert \n- [t3] repo=svc action=promote \n"), 0o600))

	st, err := session.Open(filepath.Join(root, "sessions"), 0, 0)
	must(t, err)
	prior, err := st.Start(session.Meta{ID: "20260901T000000Z-svc", Repo: "svc", Source: "ci", Kind: "fail", Provider: "claude"})
	must(t, err)
	must(t, prior.Write(session.FileDiff, "--- a/x\n+++ b/x\n@@\n-old\n+new\n"))
	prior.SetVerdict("fixed", "changed x", 1)
	prior.SetResult("ok", "", nil)
	must(t, prior.Finish())
	// A session on another repo must never be readable from svc's server.
	foreign, err := st.Start(session.Meta{ID: "20260901T000001Z-other", Repo: "other", Source: "ci", Kind: "fail", Provider: "claude"})
	must(t, err)
	must(t, foreign.Write(session.FileDiff, "secret patch\n"))
	must(t, foreign.Finish())

	cur, err := st.Start(session.Meta{ID: "20260902T000000Z-svc", Repo: "svc", Source: "ci", Kind: "fail", Provider: "claude",
		RunURL: "https://github.com/acme/svc/actions/runs/42"})
	must(t, err)
	logs := FormatCILogs([]JobLog{
		{Name: "unit (ubuntu)", Conclusion: "failure", Text: strings.Repeat("noise\n", 300) + "FAIL: TestPaginate expected 3 got 2\nexit status 1\n"},
		{Name: "lint", Conclusion: "failure", Text: "x.go:10: unused variable\n"},
	})
	must(t, cur.Write(session.FileCILogs, logs))
	must(t, cur.Write(session.FileCIRun, `{"run_url":"https://github.com/acme/svc/actions/runs/42","workflow":"ci","head_branch":"main"}`))

	return Options{
		SessionsDir: filepath.Join(root, "sessions"),
		SessionID:   cur.ID(),
		Root:        root,
		ConfigPath:  cfg,
		Now:         func() time.Time { return time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC) },
		Query: func(_ context.Context, url, q string) (float64, error) {
			if strings.HasPrefix(q, "p95") {
				return 812, nil
			}
			return 0.002, nil
		},
	}, prior.ID()
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// connect starts the server over an in-memory transport and returns a
// client session, so tests exercise the real MCP plumbing.
func connect(t *testing.T, opts Options) *mcp.ClientSession {
	t.Helper()
	srv, err := New(opts)
	must(t, err)
	ct, st := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Run(ctx, "test", st) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	must(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	must(t, err)
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func TestListsEveryRoadmapTool(t *testing.T) {
	opts, _ := fixture(t)
	cs := connect(t, opts)
	res, err := cs.ListTools(context.Background(), nil)
	must(t, err)
	got := map[string]bool{}
	for _, tl := range res.Tools {
		got[tl.Name] = true
		if tl.Annotations == nil || !tl.Annotations.ReadOnlyHint {
			t.Errorf("%s is not marked read-only", tl.Name)
		}
	}
	for _, want := range []string{"ci_logs", "ci_run", "prod_metrics", "prior_sessions", "session_diff", "lessons", "backlog", "repo_config"} {
		if !got[want] {
			t.Errorf("missing tool %s", want)
		}
	}
}

func TestCILogsGrepJobAndTail(t *testing.T) {
	opts, _ := fixture(t)
	cs := connect(t, opts)

	out, isErr := call(t, cs, "ci_logs", map[string]any{"grep": "TestPaginate"})
	if isErr || !strings.Contains(out, "FAIL: TestPaginate expected 3 got 2") {
		t.Fatalf("grep: err=%v out=%q", isErr, out)
	}
	// Two lines of context, and the lint job matched nothing so it is
	// shown with zero lines rather than dropped silently.
	if !strings.Contains(out, "noise\nnoise\nFAIL") || !strings.Contains(out, "lint (failure) === 0 lines") {
		t.Fatalf("context/summary wrong:\n%s", out)
	}

	out, isErr = call(t, cs, "ci_logs", map[string]any{"job": "lint"})
	if isErr || strings.Contains(out, "unit (ubuntu)") || !strings.Contains(out, "unused variable") {
		t.Fatalf("job filter: err=%v out=%q", isErr, out)
	}

	out, _ = call(t, cs, "ci_logs", map[string]any{"job": "unit", "tail": 2})
	if !strings.HasSuffix(strings.TrimSpace(out), "FAIL: TestPaginate expected 3 got 2\nexit status 1") || strings.Contains(out, "noise") {
		t.Fatalf("tail: %q", out)
	}

	out, isErr = call(t, cs, "ci_logs", map[string]any{"job": "deploy"})
	if !isErr || !strings.Contains(out, "failed jobs: unit (ubuntu), lint") {
		t.Fatalf("unknown job should name the real ones: err=%v %q", isErr, out)
	}
	if _, isErr = call(t, cs, "ci_logs", map[string]any{"grep": "("}); !isErr {
		t.Fatal("bad regexp should be a tool error")
	}
}

func TestCIRunAndRepoConfig(t *testing.T) {
	opts, _ := fixture(t)
	cs := connect(t, opts)
	out, isErr := call(t, cs, "ci_run", nil)
	if isErr || !strings.Contains(out, `"workflow":"ci"`) {
		t.Fatalf("ci_run: %v %q", isErr, out)
	}
	out, isErr = call(t, cs, "repo_config", nil)
	if isErr || !strings.Contains(out, "github: acme/svc") || !strings.Contains(out, "prod-health:") {
		t.Fatalf("repo_config: %v %q", isErr, out)
	}
	if strings.Contains(out, "acme/other") {
		t.Fatal("repo_config leaked another repo's entry")
	}
}

func TestProdMetricsUsesConfiguredQueries(t *testing.T) {
	opts, _ := fixture(t)
	cs := connect(t, opts)
	out, isErr := call(t, cs, "prod_metrics", nil)
	if isErr {
		t.Fatal(out)
	}
	var got map[string]any
	must(t, json.Unmarshal([]byte(out), &got))
	p95, _ := got["p95_ms"].(map[string]any)
	if p95["value"] != 812.0 || p95["query"] != `p95{service="svc"}` {
		t.Fatalf("p95=%v", p95)
	}
	if _, ok := got["thresholds"]; !ok {
		t.Fatal("thresholds missing")
	}
	out, _ = call(t, cs, "prod_metrics", map[string]any{"query": "up"})
	if !strings.Contains(out, `"query": "up"`) || strings.Contains(out, "thresholds") {
		t.Fatalf("custom query: %q", out)
	}
}

func TestPriorSessionsAndDiffAreScopedToRepo(t *testing.T) {
	opts, priorID := fixture(t)
	cs := connect(t, opts)
	out, isErr := call(t, cs, "prior_sessions", nil)
	if isErr || !strings.Contains(out, priorID) || strings.Contains(out, "-other") {
		t.Fatalf("prior_sessions: %v %q", isErr, out)
	}
	out, isErr = call(t, cs, "session_diff", map[string]any{"id": priorID})
	if isErr || !strings.Contains(out, "+new") {
		t.Fatalf("session_diff: %v %q", isErr, out)
	}
	out, isErr = call(t, cs, "session_diff", map[string]any{"id": "20260901T000001Z-other"})
	if !isErr || strings.Contains(out, "secret patch") {
		t.Fatalf("another repo's session must be refused: err=%v %q", isErr, out)
	}
	if _, isErr = call(t, cs, "session_diff", map[string]any{"id": "../../etc/passwd"}); !isErr {
		t.Fatal("traversal id must be refused")
	}
}

func TestLessonsAndBacklogFilterByRepo(t *testing.T) {
	opts, _ := fixture(t)
	cs := connect(t, opts)
	out, _ := call(t, cs, "lessons", nil)
	if !strings.Contains(out, "flaky seed") || strings.Contains(out, "repo=other") {
		t.Fatalf("lessons: %q", out)
	}
	out, _ = call(t, cs, "backlog", map[string]any{"tail": 1})
	if !strings.Contains(out, "action=promote") || strings.Contains(out, "action=fix") || strings.Contains(out, "repo=other") {
		t.Fatalf("backlog tail: %q", out)
	}
}

// Every call lands in tools.jsonl, error or not, so "what did the agent
// look at" is answerable next to what it was told.
func TestEveryCallIsRecorded(t *testing.T) {
	opts, _ := fixture(t)
	cs := connect(t, opts)
	call(t, cs, "ci_logs", map[string]any{"grep": "TestPaginate"})
	call(t, cs, "ci_logs", map[string]any{"job": "nope"})
	call(t, cs, "lessons", nil)

	raw, err := os.ReadFile(filepath.Join(opts.SessionsDir, opts.SessionID, session.FileTools))
	must(t, err)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 records, got %d: %s", len(lines), raw)
	}
	var first, second map[string]any
	must(t, json.Unmarshal([]byte(lines[0]), &first))
	must(t, json.Unmarshal([]byte(lines[1]), &second))
	if first["tool"] != "ci_logs" || first["ts"] != "2026-09-02T00:00:00Z" {
		t.Fatalf("first=%v", first)
	}
	if args, _ := first["args"].(map[string]any); args["grep"] != "TestPaginate" {
		t.Fatalf("args not recorded: %v", first)
	}
	if _, hasErr := second["error"]; !hasErr {
		t.Fatalf("failed call should record its error: %v", second)
	}
}

func TestNoLogsSavedIsAnExplanation(t *testing.T) {
	opts, _ := fixture(t)
	must(t, os.Remove(filepath.Join(opts.SessionsDir, opts.SessionID, session.FileCILogs)))
	cs := connect(t, opts)
	out, isErr := call(t, cs, "ci_logs", nil)
	if !isErr || !strings.Contains(out, "no CI logs were saved") {
		t.Fatalf("%v %q", isErr, out)
	}
}

func TestNewRejectsUnknownSession(t *testing.T) {
	opts, _ := fixture(t)
	opts.SessionID = "nope"
	if _, err := New(opts); err == nil {
		t.Fatal("expected error for unknown session")
	}
}

func TestFormatParseRoundTrip(t *testing.T) {
	jobs := []JobLog{{Name: "a (x)", Conclusion: "failure", Text: "l1\nl2\n"}, {Name: "b", Conclusion: "timed_out", Text: "only"}}
	got := parseCILogs(FormatCILogs(jobs))
	if len(got) != 2 || got[0].Name != "a (x)" || got[0].Conclusion != "failure" || got[0].Text != "l1\nl2\n" ||
		got[1].Name != "b" || got[1].Conclusion != "timed_out" || got[1].Text != "only\n" {
		t.Fatalf("round trip: %+v", got)
	}
}
