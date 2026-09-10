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
GitHub auth is required when a repo has a github slug and no local dir,
or when not --skip-network. Local dir clones warn if GITHUB_TOKEN is unset.
Also fails when server.addr is non-loopback while server.require_webhook_secret
is false — the same rule the daemon enforces at startup.
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
				// The same resolver gatebuild.DevSmoke builds the gate
				// with, so the shared gates.dev-smoke.argocd_app
				// fallback counts here too. Reading r.ArgoCDApp alone
				// gave two semantically identical configs opposite
				// verdicts and green-lit one whose dev-smoke gate could
				// not run (issue #45).
				if validate.ResolveArgoCDApp(cfg, r) != "" {
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
			// route: cheapest can pick any CLI in agent.providers; a
			// missing one used to go unmentioned here and then be chosen
			// for every Fix.
			for _, p := range cfg.Agent.Providers {
				if p == "" || p == provider {
					continue
				}
				pb := subagent.DefaultBinary(subagent.Provider(p))
				check("agent CLI ("+pb+", providers)", lookPath(pb), pathDetail(pb)+" — in agent.providers, so the router may pick it")
			}
			// Informational, never a failure: the CLIs also accept an
			// interactive login, in which case no key env is set here.
			keyEnv := subagent.APIKeyEnvName(subagent.Provider(provider))
			keyDetail := "set"
			if os.Getenv(keyEnv) == "" {
				keyDetail = "not set — ok only if the CLI is already logged in"
			}
			check("agent API key ("+keyEnv+")", true, keyDetail)

			// The MCP tool server is started by the agent CLI from an
			// absolute path, so the binary has to exist where the daemon
			// will resolve it, and recording has to be on for the tools
			// to have anything to read.
			if e := cfg.Agent.MCP.Enabled; e != nil && *e {
				switch {
				case !cfg.Agent.SessionsEnabled():
					check("agent MCP tools", false, "agent.mcp.enabled needs agent.sessions.enabled (the tools read the session directory)")
				default:
					bin := cfg.Agent.MCP.Binary
					if bin == "" {
						bin, _ = os.Executable()
					}
					_, statErr := os.Stat(bin)
					check("agent MCP tools", statErr == nil, "xdlc mcp via "+bin+" — ci_logs, ci_run, prod_metrics, prior_sessions, session_diff, lessons, backlog, repo_config")
				}
			}

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
						"none found in "+dir+" — Fix runs with no repo conventions (see https://xdlc.dev/agent/docs/rules-and-skills)")
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
			hasAuth := hasApp || hasPAT
			if githubAuthRequired(cfg, skipNet) {
				check("GitHub auth env", hasAuth, "GITHUB_TOKEN or GitHub App (GITHUB_APP_ID / config github.app_id)")
			} else if hasAuth {
				check("GitHub auth env", true, "set")
			} else {
				_, _ = fmt.Fprintf(out, "[warn] GitHub auth env — not set — ok for local dir clones (no GitHub API); export GITHUB_TOKEN or a GitHub App for CI log fetch / rerun\n")
			}

			check("XDLC_API_TOKEN set", os.Getenv("XDLC_API_TOKEN") != "", "required for /api/* console")

			// Same predicate the daemon enforces at startup
			// (enforceWebhookSecrets) so doctor and daemon can never
			// disagree: a non-loopback bind without require_webhook_secret
			// makes the daemon refuse to start, so this is a FAIL here.
			if cfgErr == nil {
				addr := cfg.Server.Addr
				if addr == "" {
					addr = ":8080"
				}
				secretEnv := cfg.Server.GitHubWebhookSecretEnv
				if secretEnv == "" {
					secretEnv = "GITHUB_WEBHOOK_SECRET" //nolint:gosec // env var name, not a secret value
				}
				if err := enforceWebhookSecrets(cfg); err != nil {
					check("webhook secret for listen addr", false,
						fmt.Sprintf("server.addr %q is non-loopback — set server.require_webhook_secret: true and export %s, or bind 127.0.0.1:8080 (daemon refuses to start otherwise)", addr, secretEnv))
				} else if cfg.Server.RequireWebhookSecret {
					check("webhook secret for listen addr", true, addr+" with require_webhook_secret=true")
				} else {
					check("webhook secret for listen addr", true, addr+" is loopback — require_webhook_secret not needed")
				}
			}

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

// githubAuthRequired is true when this install cannot run without GitHub
// credentials: a repo has a github slug but no local dir (needs clone),
// or --skip-network is off and any repo is wired to GitHub (log fetch / rerun).
func githubAuthRequired(cfg *config.Config, skipNet bool) bool {
	if cfg == nil {
		return false
	}
	if skipNet {
		for _, r := range cfg.Repos {
			if strings.TrimSpace(r.GitHub) != "" && strings.TrimSpace(r.Dir) == "" {
				return true
			}
		}
		return false
	}
	for _, r := range cfg.Repos {
		if strings.TrimSpace(r.GitHub) != "" {
			return true
		}
	}
	return false
}
