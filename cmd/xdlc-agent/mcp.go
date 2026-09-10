package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
	"github.com/xdlc-labs/xdlc-agent/internal/dispatch"
	"github.com/xdlc-labs/xdlc-agent/internal/ghclient"
	"github.com/xdlc-labs/xdlc-agent/internal/mcpserver"
)

// mcpSetup resolves what a Fix needs to start `xdlc mcp` under the agent
// CLI: an absolute binary, an absolute session root and config path,
// and the daemon's working directory. Absolute because the agent runs
// in a worktree, not where the daemon was started.
func mcpSetup(cfg *config.Config, cfgPath, sessionsRoot string) (*dispatch.MCPSetup, error) {
	bin := cfg.Agent.MCP.Binary
	if bin == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("agent.mcp: cannot locate the xdlc binary (%w); set agent.mcp.binary", err)
		}
		bin = exe
	}
	if _, err := os.Stat(bin); err != nil {
		return nil, fmt.Errorf("agent.mcp.binary %q: %w", bin, err)
	}
	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	sessAbs, err := filepath.Abs(sessionsRoot)
	if err != nil {
		return nil, err
	}
	cfgAbs, err := filepath.Abs(cfgPath)
	if err != nil {
		return nil, err
	}
	return &dispatch.MCPSetup{Binary: bin, SessionsDir: sessAbs, Root: root, ConfigPath: cfgAbs}, nil
}

// runInfo converts a GitHub run into what the session's ci-run.json holds.
func runInfo(run ghclient.Run) dispatch.RunInfo {
	return dispatch.RunInfo{
		URL: run.HTMLURL, Workflow: run.Workflow, HeadBranch: run.HeadBranch,
		HeadSHA: run.HeadSHA, Status: run.Status, Conclusion: run.Conclusion,
	}
}

// jobLogs converts GitHub job logs into the session's ci-logs.txt shape.
func jobLogs(jobs []ghclient.JobLog) []mcpserver.JobLog {
	out := make([]mcpserver.JobLog, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, mcpserver.JobLog{Name: j.Name, Conclusion: j.Conclusion, Text: j.Text})
	}
	return out
}

// mcpCmd is the stdio MCP server an agent CLI starts for one Fix. The
// daemon (or `xdlc fix`) writes the invocation into the agent's MCP
// config with agent.mcp.enabled; an operator can also run it by hand
// against a finished session to see what the agent could have asked.
func mcpCmd() *cobra.Command {
	var sessionID, sessionsDir, root string
	cmd := &cobra.Command{
		Use:   "mcp --session <id>",
		Short: "Serve read-only Fix context (CI logs, metrics, prior runs) to an agent over stdio MCP",
		Long: `Serve one Fix session's context to a coding agent as MCP tools over stdio.

Tools: ci_logs, ci_run, prod_metrics, prior_sessions, session_diff,
lessons, backlog, repo_config. All read-only, all scoped to the session's
repo. Every call is appended to the session's tools.jsonl.

Normally started by the agent CLI, not by hand: with agent.mcp.enabled
the daemon writes this command into the agent's MCP config for each Fix.
Run it by hand with an MCP inspector against a finished session to see
what the agent could have asked.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if sessionID == "" {
				return fmt.Errorf("mcp: --session is required")
			}
			if sessionsDir == "" {
				sessionsDir = "sessions"
			}
			opts := mcpserver.Options{SessionsDir: sessionsDir, SessionID: sessionID, Root: root}
			// --config is the root persistent flag; only pass it on when
			// the file exists, so a hand run in a bare directory still
			// serves the disk-only tools.
			if cfgPath != "" {
				if abs, err := filepath.Abs(cfgPath); err == nil {
					if _, err := os.Stat(abs); err == nil {
						opts.ConfigPath = abs
					}
				}
			}
			srv, err := mcpserver.New(opts)
			if err != nil {
				return err
			}
			return srv.Run(cmd.Context(), version, &mcp.StdioTransport{})
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "session id under --sessions-dir (required)")
	cmd.Flags().StringVar(&sessionsDir, "sessions-dir", "", "session store root (default: sessions, or agent.sessions.dir)")
	cmd.Flags().StringVar(&root, "root", "", "daemon working directory holding LESSONS.md and BACKLOG.md (default: none)")
	return cmd
}
