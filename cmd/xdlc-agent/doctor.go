package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
		Long: `Diagnose a local xdlc install before daemon start.

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
					_, _ = fmt.Fprintf(out, "[%s] %s\n", mark, name)
					return
				}
				_, _ = fmt.Fprintf(out, "[%s] %s — %s\n", mark, name, detail)
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
				bin = subagent.DefaultBinary(subagent.Provider(provider))
			}
			if !subagent.KnownProvider(provider) {
				var names []string
				for _, p := range subagent.Providers() {
					names = append(names, string(p))
				}
				check("agent provider ("+provider+")", false,
					"unknown provider — known: "+strings.Join(names, ", ")+" (falling back to the claude argv shape)")
			}
			check("agent CLI ("+bin+")", lookPath(bin), pathDetail(bin))
			// Informational, never a failure: the CLIs also accept an
			// interactive login, in which case no key env is set here.
			keyEnv := subagent.APIKeyEnvName(subagent.Provider(provider))
			keyDetail := "set"
			if os.Getenv(keyEnv) == "" {
				keyDetail = "not set — ok only if the CLI is already logged in"
			}
			check("agent API key ("+keyEnv+")", true, keyDetail)

			// Rules the Fix agent will be given, per repo. Missing files
			// are not a failure — they mean the agent runs on defaults.
			for _, r := range cfg.Repos {
				dir := r.Dir
				if dir == "" {
					dir = filepath.Join("repos", r.Name)
				}
				srcs := subagent.RuleSources(dir, cfg.Agent.RulesFile)
				if len(srcs) == 0 {
					check("agent rules ("+r.Name+")", true,
						"none found in "+dir+" — Fix runs with no repo conventions (see docs/rules-and-skills.md)")
					continue
				}
				var names []string
				for _, src := range srcs {
					name := src.Path
					if src.Truncated {
						name += " (truncated)"
					}
					names = append(names, name)
				}
				check("agent rules ("+r.Name+")", true, strings.Join(names, ", "))
			}

			hasApp := os.Getenv("GITHUB_APP_ID") != "" || (cfg.GitHub.AppID != 0)
			hasPAT := os.Getenv("GITHUB_TOKEN") != ""
			check("GitHub auth env", hasApp || hasPAT, "GITHUB_TOKEN or GitHub App (GITHUB_APP_ID / config github.app_id)")

			check("XDLC_API_TOKEN set", os.Getenv("XDLC_API_TOKEN") != "", "required for /api/* console")

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
					var detail string
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
				_, _ = fmt.Fprintf(out, "\n%d check(s) failed\n", failed)
				return errDoctor
			}
			_, _ = fmt.Fprintln(out, "\nall checks passed")
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
