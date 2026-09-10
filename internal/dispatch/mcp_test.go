package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/mcpserver"
	"github.com/xdlc-labs/xdlc-agent/internal/orchestrator"
	"github.com/xdlc-labs/xdlc-agent/internal/session"
	"github.com/xdlc-labs/xdlc-agent/internal/subagent"
)

// scriptRunner is a real SubprocessRunner whose "claude" is a shell
// script that records its argv, commits a fix and pushes, so the MCP
// wiring is exercised on the runner type the daemon uses.
func scriptRunner(t *testing.T) (*subagent.SubprocessRunner, string) {
	t.Helper()
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv")
	script := filepath.Join(dir, "claude")
	body := fmt.Sprintf(`#!/bin/sh
cat >/dev/null
printf '%%s\n' "$@" > %q
echo fixed > app.txt && git add . && git commit -qm "fix from script" && git push -q origin develop
echo '{"result":"{\"xdlc_outcome\":\"fixed\",\"summary\":\"fixed app\"}"}'
`, argvFile)
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return subagent.NewSubprocessRunner(subagent.ProviderClaude, script, nil, time.Minute, nil), argvFile
}

func TestFixWithMCPSavesLogsTrimsPromptAndWiresServer(t *testing.T) {
	_, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	store, err := session.Open(t.TempDir(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	runner, argvFile := scriptRunner(t)
	d := New(mgr, runner, silentLogger())
	d.Sessions = store
	d.DefaultProvider = "claude"
	d.MCP = &MCPSetup{Binary: "/opt/xdlc", SessionsDir: store.Root, Root: "/srv/xdlc", ConfigPath: "/srv/xdlc/config.yaml"}
	d.FetchAllLogs = func(_ context.Context, runURL string) ([]mcpserver.JobLog, error) {
		return []mcpserver.JobLog{
			{Name: "unit", Conclusion: "failure", Text: "full unit log\n"},
			{Name: "lint", Conclusion: "failure", Text: "full lint log\n"},
		}, nil
	}
	d.FetchRun = func(_ context.Context, runURL string) (RunInfo, error) {
		return RunInfo{URL: runURL, Workflow: "ci", HeadBranch: "develop", Conclusion: "failure"}, nil
	}

	var logs strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&logs, "log line %d\n", i)
	}
	sig := orchestrator.Signal{
		Repo: "svc", Source: orchestrator.SourceCI, Kind: orchestrator.KindFail,
		Evidence: map[string]any{"run_url": "https://github.com/org/svc/actions/runs/7", "logs": logs.String()},
	}
	if _, err := d.Fix(context.Background(), sig); err != nil {
		t.Fatalf("Fix: %v", err)
	}

	metas, err := store.List("svc", 0)
	if err != nil || len(metas) != 1 {
		t.Fatalf("sessions: %v %v", metas, err)
	}
	m := metas[0]
	if m.RunURL != "https://github.com/org/svc/actions/runs/7" {
		t.Fatalf("meta.run_url=%q", m.RunURL)
	}

	// The material the tools read.
	ciLogs, _ := store.ReadFile(m.ID, session.FileCILogs)
	if !strings.Contains(ciLogs, "=== job: unit (failure) ===") || !strings.Contains(ciLogs, "full lint log") {
		t.Fatalf("ci-logs.txt:\n%s", ciLogs)
	}
	ciRun, _ := store.ReadFile(m.ID, session.FileCIRun)
	var info RunInfo
	if err := json.Unmarshal([]byte(ciRun), &info); err != nil || info.Workflow != "ci" || len(info.FailedJobs) != 2 {
		t.Fatalf("ci-run.json: %s (%v)", ciRun, err)
	}

	// The prompt carries a tail and says where the rest is.
	prompt, _ := store.ReadFile(m.ID, session.FilePrompt)
	if !strings.Contains(prompt, `MCP server named \"xdlc\"`) && !strings.Contains(prompt, `MCP server named "xdlc"`) {
		t.Fatalf("prompt lacks the tools block:\n%s", prompt)
	}
	if strings.Contains(prompt, "log line 1\\n") || !strings.Contains(prompt, "log line 100") {
		t.Fatalf("prompt logs not trimmed to a tail:\n%s", prompt)
	}
	if !strings.Contains(prompt, "complete logs of all 2 failed jobs are in the ci_logs tool") {
		t.Fatalf("prompt does not point at ci_logs:\n%s", prompt)
	}

	// The agent CLI was told where the server is.
	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	mcpPath := filepath.Join(store.Root, m.ID, session.FileMCP)
	if !strings.Contains(string(argv), "--mcp-config\n"+mcpPath) {
		t.Fatalf("argv lacks --mcp-config %s:\n%s", mcpPath, argv)
	}
	raw, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	srv := doc.Servers["xdlc"]
	want := []string{"mcp", "--session", m.ID, "--sessions-dir", store.Root, "--root", "/srv/xdlc", "--config", "/srv/xdlc/config.yaml"}
	if srv.Command != "/opt/xdlc" || strings.Join(srv.Args, " ") != strings.Join(want, " ") {
		t.Fatalf("server spec: %+v", srv)
	}
}

// The agent runs in the worktree, so a relative session root (the
// default "sessions") must still produce an absolute --mcp-config path.
// This is the bug the first end-to-end run hit.
func TestFixWithMCPHandsAgentAbsolutePaths(t *testing.T) {
	_, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	t.Chdir(t.TempDir())
	store, err := session.Open("sessions", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	runner, argvFile := scriptRunner(t)
	d := New(mgr, runner, silentLogger())
	d.Sessions = store
	d.DefaultProvider = "claude"
	d.MCP = &MCPSetup{Binary: "/opt/xdlc", SessionsDir: "/abs/sessions"}

	sig := orchestrator.Signal{Repo: "svc", Source: orchestrator.SourceCI, Kind: orchestrator.KindFail,
		Evidence: map[string]any{"run_url": "https://github.com/org/svc/actions/runs/9"}}
	if _, err := d.Fix(context.Background(), sig); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(argv)), "\n")
	for i, l := range lines {
		if l == "--mcp-config" && i+1 < len(lines) {
			if !filepath.IsAbs(lines[i+1]) {
				t.Fatalf("--mcp-config path is relative: %q", lines[i+1])
			}
			return
		}
	}
	t.Fatalf("no --mcp-config in argv: %q", argv)
}

// A runner that cannot take an MCP server (a fake, an SDK runner) still
// runs the Fix: tools are an addition, never a precondition.
func TestFixWithMCPFallsBackWhenRunnerCannotTakeIt(t *testing.T) {
	_, workDir := setupOrigin(t)
	mgr := testManager(t, workDir)
	store, err := session.Open(t.TempDir(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRunner{}
	d := New(mgr, fr, silentLogger())
	d.Sessions = store
	d.DefaultProvider = "claude"
	d.MCP = &MCPSetup{Binary: "/opt/xdlc", SessionsDir: store.Root}

	sig := orchestrator.Signal{
		Repo: "svc", Source: orchestrator.SourceCI, Kind: orchestrator.KindFail,
		Evidence: map[string]any{"run_url": "https://github.com/org/svc/actions/runs/8", "logs": "one\ntwo\n"},
	}
	if _, err := d.Fix(context.Background(), sig); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if strings.Contains(fr.gotPrompt, "MCP server named") {
		t.Fatal("prompt must not promise tools the agent does not have")
	}
	if !strings.Contains(fr.gotPrompt, "one") {
		t.Fatal("logs must stay inline when there is no tool to fetch them from")
	}
}

func TestTailLines(t *testing.T) {
	if got := tailLines("a\nb\nc\n", 2); got != "b\nc" {
		t.Fatalf("got %q", got)
	}
	if got := tailLines("a", 5); got != "a" {
		t.Fatalf("got %q", got)
	}
}
