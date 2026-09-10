package subagent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testServer = MCPServer{Command: "/usr/local/bin/xdlc", Args: []string{"mcp", "--session", "20260901T000000Z-svc", "--sessions-dir", "/srv/sessions"}}

func TestWithMCPClaudeWritesConfigAndFlag(t *testing.T) {
	cfgDir := t.TempDir()
	r := NewSubprocessRunner(ProviderClaude, "", nil, time.Minute, nil)
	got, err := r.WithMCP(testServer, t.TempDir(), cfgDir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfgDir, "mcp.json")
	argv := strings.Join(got.Args, " ")
	if !strings.Contains(argv, "--mcp-config "+path) {
		t.Fatalf("argv=%v", got.Args)
	}
	if strings.Contains(strings.Join(r.Args, " "), "--mcp-config") {
		t.Fatal("WithMCP must not mutate the receiver")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]map[string]map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	srv := doc["mcpServers"]["xdlc"]
	if srv["command"] != testServer.Command {
		t.Fatalf("doc=%v", doc)
	}
	if args, _ := srv["args"].([]any); len(args) != 5 || args[0] != "mcp" {
		t.Fatalf("args=%v", srv["args"])
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
}

func TestWithMCPCodexUsesConfigOverrides(t *testing.T) {
	r := NewSubprocessRunner(ProviderCodex, "", nil, time.Minute, nil)
	got, err := r.WithMCP(testServer, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(got.Args, " ")
	if !strings.Contains(argv, `-c mcp_servers.xdlc.command="/usr/local/bin/xdlc"`) ||
		!strings.Contains(argv, `-c mcp_servers.xdlc.args=["mcp","--session","20260901T000000Z-svc","--sessions-dir","/srv/sessions"]`) {
		t.Fatalf("argv=%v", got.Args)
	}
}

func TestWithMCPCursorWritesWorkspaceFileAndExcludesIt(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	run(t, repo, "init", "-q")
	// The repo ships its own MCP file: it must survive with ours added.
	if err := os.MkdirAll(filepath.Join(repo, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".cursor", "mcp.json"), []byte(`{"mcpServers":{"theirs":{"command":"x"}},"other":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "add", ".")
	run(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init")

	r := NewSubprocessRunner(ProviderCursor, "", nil, time.Minute, nil)
	got, err := r.WithMCP(testServer, repo, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(got.Args, " "), "--approve-mcps") {
		t.Fatalf("argv=%v", got.Args)
	}
	raw, _ := os.ReadFile(filepath.Join(repo, ".cursor", "mcp.json"))
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	servers := doc["mcpServers"].(map[string]any)
	if servers["theirs"] == nil || servers["xdlc"] == nil || doc["other"] != 1.0 {
		t.Fatalf("merge lost content: %s", raw)
	}
	// The file is tracked here, so status shows it modified — that is the
	// project's own file. The exclude covers the case where it was not
	// there: an untracked file the agent could otherwise commit.
	exclude, _ := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if !strings.Contains(string(exclude), ".cursor/mcp.json") {
		t.Fatalf("exclude=%q", exclude)
	}
	// Second call does not add the pattern twice.
	if _, err := r.WithMCP(testServer, repo, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	exclude, _ = os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if strings.Count(string(exclude), ".cursor/mcp.json") != 1 {
		t.Fatalf("exclude repeated: %q", exclude)
	}
}

func TestWithMCPGeminiUntrackedFileIsInvisibleToStatus(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	run(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "add", ".")
	run(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init")

	r := NewSubprocessRunner(ProviderGemini, "", nil, time.Minute, nil)
	if _, err := r.WithMCP(testServer, repo, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".gemini", "settings.json")); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(context.Background(), "git", "-C", repo, "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("the MCP file must not show up for the agent to commit: %q", out)
	}
}

func TestWithMCPNoCommandIsNoop(t *testing.T) {
	r := NewSubprocessRunner(ProviderClaude, "", nil, time.Minute, nil)
	got, err := r.WithMCP(MCPServer{}, t.TempDir(), t.TempDir())
	if err != nil || got != r {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

// run executes a git command in dir and fails the test on error.
func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
