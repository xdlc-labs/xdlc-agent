package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
	"github.com/xdlc-labs/xdlc-agent/internal/subagent"
	"github.com/xdlc-labs/xdlc-agent/internal/validate"
)

// errDoctor is returned when any doctor check fails (distinct exit path).
var errDoctor = errors.New("doctor: one or more checks failed")

func doctorCmd() *cobra.Command {
	var (
		gitopsDir string
		skipNet   bool
	)
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check PATH binaries, tokens, and optional gate reachability",
		Long: `Diagnose a local xdlc-agent install before daemon start.

Checks git / agent CLI on PATH, token env presence, config validation,
and (unless --skip-network) optional Prometheus / reachability probes.
Exit 1 when any required check fails.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			failed := 0
			check := func(name string, ok bool, detail string) {
				mark := "ok"
				if !ok {
					mark = "FAIL"
					failed++
				}
				if detail == "" {
					fmt.Fprintf(out, "[%s] %s\n", mark, name)
					return
				}
				fmt.Fprintf(out, "[%s] %s — %s\n", mark, name, detail)
			}

			cfg, cfgErr := config.Load(cfgPath)
			if cfgErr != nil {
				check("config load", false, cfgErr.Error())
				cfg = &config.Config{}
			} else {
				check("config load", true, cfgPath)
			}

			if cfgErr == nil {
				issues := validate.Config(cfg)
				if gitopsDir != "" {
					more, err := validate.GitOps(cfg, gitopsDir)
					if err != nil {
						check("config validate", false, err.Error())
					} else {
						issues = append(issues, more...)
					}
				}
				if len(issues) > 0 {
					var b strings.Builder
					for i, iss := range issues {
						if i > 0 {
							b.WriteString("; ")
						}
						b.WriteString(iss.String())
					}
					check("config validate", false, b.String())
				} else {
					check("config validate", true, "")
				}
			}

			check("git on PATH", lookPath("git"), pathDetail("git"))
			// kubectl / argocd: warn-style — required only when gates need them.
			needKubectl := false
			needArgo := false
			for _, r := range cfg.Repos {
				for _, g := range r.Gates {
					if g == "dev-smoke" {
						needKubectl = true
					}
				}
				if r.ArgoCDApp != "" {
					needArgo = true
				}
			}
			if needKubectl {
				check("kubectl on PATH", lookPath("kubectl"), pathDetail("kubectl"))
			} else {
				check("kubectl on PATH", true, optionalPath("kubectl"))
			}
			if needArgo {
				check("argocd on PATH", lookPath("argocd"), pathDetail("argocd"))
			} else {
				check("argocd on PATH", true, optionalPath("argocd"))
			}

			provider := cfg.Agent.Provider
			if provider == "" {
				provider = string(subagent.ProviderClaude)
			}
			bin := cfg.Agent.Binary
			if bin == "" {
				switch subagent.Provider(provider) {
				case subagent.ProviderCodex:
					bin = "codex"
				case subagent.ProviderCursor:
					bin = "cursor-agent"
				default:
					bin = "claude"
				}
			}
			check("agent CLI ("+bin+")", lookPath(bin), pathDetail(bin))

			hasApp := os.Getenv("GITHUB_APP_ID") != "" || (cfg.GitHub.AppID != 0)
			hasPAT := os.Getenv("GITHUB_TOKEN") != ""
			check("GitHub auth env", hasApp || hasPAT, "GITHUB_TOKEN or GitHub App (GITHUB_APP_ID / config github.app_id)")

			apiEnv := cfg.Server.APITokenEnv
			if apiEnv == "" {
				apiEnv = "XDL_API_TOKEN"
			}
			check(apiEnv+" set", os.Getenv(apiEnv) != "", "required for /api/* console")

			if cfg.Server.RequireWebhookSecret {
				for _, envName := range []string{
					cfg.Server.GitHubWebhookSecretEnv,
					cfg.Server.ArgoCDWebhookSecretEnv,
					cfg.Server.AlertmanagerWebhookSecretEnv,
				} {
					if envName == "" {
						continue
					}
					check(envName+" set", os.Getenv(envName) != "", "require_webhook_secret=true")
				}
			}

			if !skipNet {
				metricsURL := cfg.Gates.ProdHealth.MetricsEndpoint()
				if metricsURL != "" {
					url := strings.TrimRight(metricsURL, "/") + "/-/healthy"
					ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Second)
					defer cancel()
					req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
					ok := false
					detail := url
					if err != nil {
						detail = err.Error()
					} else if resp, err := http.DefaultClient.Do(req); err != nil {
						detail = err.Error()
					} else {
						_ = resp.Body.Close()
						ok = resp.StatusCode >= 200 && resp.StatusCode < 500
						detail = fmt.Sprintf("%s → HTTP %d", url, resp.StatusCode)
					}
					check("Prometheus reachable", ok, detail)
				}
			}

			if failed > 0 {
				fmt.Fprintf(out, "\n%d check(s) failed\n", failed)
				return errDoctor
			}
			fmt.Fprintln(out, "\nall checks passed")
			return nil
		},
	}
	cmd.Flags().StringVar(&gitopsDir, "gitops-dir", "", "optional gitops tree for validate.GitOps")
	cmd.Flags().BoolVar(&skipNet, "skip-network", false, "skip Prometheus / network reachability checks")
	return cmd
}

func lookPath(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

func pathDetail(bin string) string {
	p, err := exec.LookPath(bin)
	if err != nil {
		return "not found"
	}
	return p
}

func optionalPath(bin string) string {
	if p, err := exec.LookPath(bin); err == nil {
		return p + " (optional)"
	}
	return "not found (optional — ok)"
}
