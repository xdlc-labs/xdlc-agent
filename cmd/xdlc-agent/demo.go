package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/xdlc-labs/xdlc-agent/internal/demo"
)

func demoCmd() *cobra.Command {
	var (
		provider string
		scenario string
	)
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Zero-infra Fix→Promote→Revert loop (no Kind/Argo/Prom/GitHub)",
		Long: `Run a self-contained Fix→Promote→Revert demo in a temp git repo.

Default --provider fake needs no accounts or API keys and finishes in
under three minutes. Real providers (claude|codex|cursor) require the
matching CLI on PATH.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := demo.Run(cmd.Context(), demo.Options{
				Provider: provider,
				Scenario: scenario,
				Out:      cmd.OutOrStdout(),
			})
			if err != nil {
				return fmt.Errorf("demo: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "fake", "claude|codex|cursor|fake")
	cmd.Flags().StringVar(&scenario, "scenario", "all", "ci-red|smoke-red|prod-breach|all")
	return cmd
}
