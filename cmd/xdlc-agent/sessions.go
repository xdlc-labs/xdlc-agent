package main

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
	"github.com/xdlc-labs/xdlc-agent/internal/session"
)

// sessionsCmd inspects what the Fix agent was told and what it did.
// `xdlc history` answers which Fixes ran; this answers what happened
// inside one of them.
func sessionsCmd() *cobra.Command {
	var dirFlag string

	openStore := func() (*session.Store, error) {
		dir := dirFlag
		if dir == "" {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				// No config on this host: fall back to the default root
				// so `xdlc sessions ls` works from a plain checkout.
				dir = "sessions"
			} else {
				dir = cfg.Agent.SessionsDir()
			}
		}
		if dir == "" {
			return nil, fmt.Errorf("session recording is disabled (agent.sessions.enabled: false)")
		}
		st, err := session.Open(dir, 0, 0)
		if err != nil {
			return nil, err
		}
		if st == nil {
			return nil, fmt.Errorf("session recording is disabled")
		}
		return st, nil
	}

	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "Inspect recorded Fix sessions (prompt, agent output, diff)",
		Long: `Read the on-disk record of past Fix runs.

Each Fix writes a directory holding the exact prompt the coding agent
received, everything it printed, and the patch it produced. Use this to
review an automated Fix instead of re-reading truncated logs.`,
	}
	cmd.PersistentFlags().StringVar(&dirFlag, "dir", "", "session root (default: agent.sessions.dir from config)")

	var (
		repoFilter string
		limit      int
	)
	lsCmd := &cobra.Command{
		Use:   "ls",
		Short: "List recorded sessions, newest first",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			metas, err := st.List(repoFilter, limit)
			if err != nil {
				return err
			}
			if len(metas) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no sessions recorded")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "ID\tSTARTED\tREPO\tSOURCE\tPROVIDER\tSTATUS\tFILES\tCOST")
			for _, m := range metas {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\n",
					m.ID, m.StartedAt.Format(time.RFC3339), m.Repo, m.Source,
					m.Provider, orDash(m.Status), m.Changed, costString(m.Cost))
			}
			return w.Flush()
		},
	}
	lsCmd.Flags().StringVar(&repoFilter, "repo", "", "only this repo")
	lsCmd.Flags().IntVar(&limit, "limit", 20, "max rows (0 = all)")

	var showPrompt, showOutput, showDiff, showPlan, showPath bool
	showCmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one session's metadata or a single artifact",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			id := args[0]
			out := cmd.OutOrStdout()

			if showPath {
				dir, err := st.Path(id)
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintln(out, dir)
				return nil
			}
			for _, want := range []struct {
				on   bool
				file string
			}{
				{showPrompt, session.FilePrompt},
				{showPlan, session.FilePlan},
				{showOutput, session.FileOutput},
				{showDiff, session.FileDiff},
			} {
				if !want.on {
					continue
				}
				body, err := st.ReadFile(id, want.file)
				if err != nil {
					return err
				}
				if body == "" {
					_, _ = fmt.Fprintf(out, "(no %s recorded for %s)\n", want.file, id)
					continue
				}
				_, _ = fmt.Fprintln(out, strings.TrimRight(body, "\n"))
			}
			if showPrompt || showPlan || showOutput || showDiff {
				return nil
			}

			m, err := st.Load(id)
			if err != nil {
				return err
			}
			dir, _ := st.Path(id)
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			row := func(k, v string) {
				if v != "" {
					_, _ = fmt.Fprintf(w, "%s\t%s\n", k, v)
				}
			}
			row("id", m.ID)
			row("dir", dir)
			row("repo", m.Repo)
			row("signal", strings.TrimSpace(m.Source+" "+m.Kind))
			row("provider", m.Provider)
			row("fix_mode", m.FixMode)
			row("started", m.StartedAt.Format(time.RFC3339))
			if m.DurationMS > 0 {
				row("duration", (time.Duration(m.DurationMS) * time.Millisecond).String())
			}
			row("status", m.Status)
			row("error", m.Error)
			row("branch", m.Branch)
			row("base_sha", shortSHA(m.BaseSHA))
			row("head_sha", shortSHA(m.HeadSHA))
			if m.Changed > 0 {
				row("changed_files", fmt.Sprint(m.Changed))
			}
			row("pr", m.PRURL)
			row("cost", costString(m.Cost))
			if err := w.Flush(); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "\nartifacts: --prompt --plan --output --diff (or --path)\n")
			return nil
		},
	}
	showCmd.Flags().BoolVar(&showPrompt, "prompt", false, "print the prompt sent to the agent")
	showCmd.Flags().BoolVar(&showPlan, "plan", false, "print the diagnose-pass plan (fix_plan runs only)")
	showCmd.Flags().BoolVar(&showOutput, "output", false, "print the agent's stdout")
	showCmd.Flags().BoolVar(&showDiff, "diff", false, "print the patch the agent produced")
	showCmd.Flags().BoolVar(&showPath, "path", false, "print the session directory path")

	pruneCmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete sessions older than agent.sessions.retain",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			if dirFlag == "" {
				if cfg, cerr := config.Load(cfgPath); cerr == nil && cfg.Agent.Sessions.Retain > 0 {
					st.Retain = cfg.Agent.Sessions.Retain
				}
			}
			n, err := st.Prune()
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "pruned %d session(s) older than %s\n", n, st.Retain)
			return nil
		},
	}

	cmd.AddCommand(lsCmd, showCmd, pruneCmd)
	return cmd
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// costString renders the provider cost fields for one table cell.
func costString(cost map[string]any) string {
	if len(cost) == 0 {
		return "—"
	}
	if v, ok := cost["total_cost_usd"]; ok {
		return fmt.Sprintf("$%v", v)
	}
	if v, ok := cost["output_tokens"]; ok {
		return fmt.Sprintf("%v out-tok", v)
	}
	return "—"
}
