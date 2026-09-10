package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/xdlc-labs/xdlc-agent/internal/oneshot"
)

func fixCmd() *cobra.Command {
	var (
		provider     string
		model        string
		mode         string
		workdir      string
		instructions string
		timeout      time.Duration
		stallTimeout time.Duration
		mcp          bool
	)
	cmd := &cobra.Command{
		Use:   "fix <github-actions-run-url>",
		Short: "Fix one failed GitHub Actions run with your coding agent — no daemon, no config",
		Long: `Fix a single failed GitHub Actions run and open a PR.

  xdlc fix https://github.com/you/repo/actions/runs/123456789

Needs a GitHub token (GITHUB_TOKEN, or a logged-in gh CLI) and the
agent CLI on PATH (claude, codex, cursor-agent or gemini). The repo is
cloned under --dir, the agent works in its own git worktree, xdlc pushes
the result and opens the PR against the branch that failed. The prompt,
agent output and diff are recorded as a session.

This is the same Fix the daemon runs on a webhook. Run the daemon when
you want it to happen without you: xdlc init.`,
		Args: cobra.ExactArgs(1),
		// The error already carries the agent's verdict; a usage dump after it buries the one line that matters.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := oneshot.Run(cmd.Context(), oneshot.Options{
				RunURL:       args[0],
				Provider:     provider,
				Model:        model,
				Mode:         mode,
				WorkDir:      workdir,
				Instructions: instructions,
				Timeout:      timeout,
				StallTimeout: stallTimeout,
				MCP:          mcp,
				Out:          cmd.OutOrStdout(),
			})
			if errors.Is(err, oneshot.ErrNotRed) {
				return fmt.Errorf("nothing to fix: %w", err)
			}
			return err
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "claude", "claude|codex|cursor|gemini")
	cmd.Flags().StringVar(&model, "model", "", "model to ask the agent CLI for, e.g. claude-opus-5 (default: the CLI's own)")
	cmd.Flags().StringVar(&mode, "mode", "pr", "pr (open a PR) | direct (push to the failing branch)")
	cmd.Flags().StringVar(&workdir, "dir", "", "clones and sessions root (default: $XDG_CACHE_HOME/xdlc)")
	cmd.Flags().StringVarP(&instructions, "message", "m", "", "hint for the agent, e.g. \"the flake is in the seed data\"")
	cmd.Flags().DurationVar(&timeout, "timeout", 20*time.Minute, "agent wall-clock limit")
	// Off by default, matching the daemon: it switches the agent CLI to
	// streaming output, so an operator has to ask for it.
	cmd.Flags().DurationVar(&stallTimeout, "stall-timeout", 0, "kill the agent after this long with no output (default off)")
	cmd.Flags().BoolVar(&mcp, "mcp", false, "attach the xdlc MCP tool server so the agent can pull every failed job's full logs on demand (default off)")
	return cmd
}
