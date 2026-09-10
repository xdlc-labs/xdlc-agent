package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MCPServer is a stdio MCP server the agent CLI should start: `xdlc mcp
// --session <id> ...` in practice. Name is what the CLI calls it.
type MCPServer struct {
	Name    string
	Command string
	Args    []string
}

// WithMCP returns a copy of the runner wired to start srv as an MCP
// server, in whatever way r.Provider takes one.
//
// Each provider takes an MCP server a different way, and none of them
// the same way twice:
//
//   - Claude Code reads `--mcp-config <file>`.
//   - Codex takes `-c mcp_servers.<name>.command=… -c mcp_servers.<name>.args=[…]`.
//   - Cursor reads .cursor/mcp.json from the workspace and wants
//     `--approve-mcps` to use it without a prompt.
//   - Gemini reads .gemini/settings.json from the project.
//
// The two that read from the workspace get a file written into the
// per-Fix worktree, which the agent must not commit: the path is added
// to the repository's info/exclude, which is not a tracked file.
//
// repoDir is the directory the agent runs in (the worktree); configDir
// is a private directory for files that do not need to sit in the
// workspace — the session directory.
func (r *SubprocessRunner) WithMCP(srv MCPServer, repoDir, configDir string) (*SubprocessRunner, error) {
	if srv.Command == "" {
		return r, nil
	}
	if srv.Name == "" {
		srv.Name = "xdlc"
	}
	clone := *r
	clone.Args = append([]string(nil), r.Args...)
	switch r.Provider {
	case ProviderClaude:
		path := filepath.Join(configDir, "mcp.json")
		if err := writeMCPServersFile(path, srv, false); err != nil {
			return nil, err
		}
		clone.Args = append(clone.Args, "--mcp-config", path)
	case ProviderCodex:
		args, _ := json.Marshal(srv.Args)
		clone.Args = append(clone.Args,
			"-c", fmt.Sprintf("mcp_servers.%s.command=%q", srv.Name, srv.Command),
			"-c", fmt.Sprintf("mcp_servers.%s.args=%s", srv.Name, args),
		)
	case ProviderCursor:
		path := filepath.Join(repoDir, ".cursor", "mcp.json")
		if err := writeMCPServersFile(path, srv, true); err != nil {
			return nil, err
		}
		if err := gitExclude(repoDir, ".cursor/mcp.json"); err != nil {
			return nil, err
		}
		// cursor-agent drops its own scratch directory into the workspace;
		// an agent that runs `git add -A` would otherwise commit it.
		if err := gitExclude(repoDir, "toolbox/"); err != nil {
			return nil, err
		}
		clone.Args = append(clone.Args, "--approve-mcps")
	case ProviderGemini:
		path := filepath.Join(repoDir, ".gemini", "settings.json")
		if err := writeMCPServersFile(path, srv, true); err != nil {
			return nil, err
		}
		if err := gitExclude(repoDir, ".gemini/settings.json"); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("subagent: no MCP wiring for provider %q", r.Provider)
	}
	return &clone, nil
}

// writeMCPServersFile writes {"mcpServers": {name: {command, args}}}.
// With merge, an existing file's other keys and other servers survive:
// a repository may ship its own .gemini/settings.json or .cursor/mcp.json,
// and the Fix must not blank it.
func writeMCPServersFile(path string, srv MCPServer, merge bool) error {
	doc := map[string]any{}
	if merge {
		if raw, err := os.ReadFile(path); err == nil { //nolint:gosec // G304: path is under the Fix worktree xdlc created
			_ = json.Unmarshal(raw, &doc)
		}
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers[srv.Name] = map[string]any{"command": srv.Command, "args": srv.Args}
	doc["mcpServers"] = servers
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("subagent: mcp config dir: %w", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("subagent: write mcp config: %w", err)
	}
	return nil
}

// gitExclude adds pattern to the repository's info/exclude so a file
// xdlc dropped into the workspace never shows up in `git status` for the
// agent to commit. info/exclude is per repository, not tracked, and
// shared by every worktree of the clone, which is what we want: the
// pattern is xdlc's, not the project's.
func gitExclude(repoDir, pattern string) error {
	out, err := exec.CommandContext(context.Background(), "git", "-C", repoDir, "rev-parse", "--git-path", "info/exclude").Output() //nolint:gosec // G204: fixed argv, repoDir is xdlc's own worktree
	if err != nil {
		// Not a git checkout (tests, or a bare directory): nothing to
		// exclude from.
		return nil
	}
	path := strings.TrimSpace(string(out))
	if !filepath.IsAbs(path) {
		path = filepath.Join(repoDir, path)
	}
	existing, _ := os.ReadFile(path) //nolint:gosec // G304: path comes from git rev-parse for xdlc's own clone
	for line := range strings.SplitSeq(string(existing), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // G304: see above
	if err != nil {
		return fmt.Errorf("subagent: git exclude: %w", err)
	}
	defer func() { _ = f.Close() }()
	prefix := ""
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		prefix = "\n"
	}
	_, err = fmt.Fprintf(f, "%s# xdlc: MCP client config for the Fix agent\n%s\n", prefix, pattern)
	return err
}
