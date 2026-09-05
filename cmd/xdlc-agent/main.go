// Command xdlc is both the daemon (the "one loop") and a CLI for
// one-shot gate checks / manual promote, usable standalone or from CI.
// Built from ./cmd/xdlc-agent; binary name is xdlc.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/xdlc-labs/xdlc-agent/internal/api"
	"github.com/xdlc-labs/xdlc-agent/internal/authn"
	"github.com/xdlc-labs/xdlc-agent/internal/backlog"
	"github.com/xdlc-labs/xdlc-agent/internal/config"
	"github.com/xdlc-labs/xdlc-agent/internal/console"
	"github.com/xdlc-labs/xdlc-agent/internal/dispatch"
	"github.com/xdlc-labs/xdlc-agent/internal/gate"
	"github.com/xdlc-labs/xdlc-agent/internal/gatebuild"
	"github.com/xdlc-labs/xdlc-agent/internal/ghclient"
	"github.com/xdlc-labs/xdlc-agent/internal/lessons"
	"github.com/xdlc-labs/xdlc-agent/internal/orchestrator"
	agentotel "github.com/xdlc-labs/xdlc-agent/internal/otel"
	"github.com/xdlc-labs/xdlc-agent/internal/poller"
	"github.com/xdlc-labs/xdlc-agent/internal/promote"
	"github.com/xdlc-labs/xdlc-agent/internal/ratelimit"
	"github.com/xdlc-labs/xdlc-agent/internal/repos"
	"github.com/xdlc-labs/xdlc-agent/internal/session"
	"github.com/xdlc-labs/xdlc-agent/internal/store"
	"github.com/xdlc-labs/xdlc-agent/internal/subagent"
	"github.com/xdlc-labs/xdlc-agent/internal/validate"
	"github.com/xdlc-labs/xdlc-agent/internal/webhook"
)

var cfgPath string

// auditDBPath is where the bbolt-backed signal+action history lives —
// BACKLOG.md is the human-facing view, this is the queryable one
// (`xdlc history`). Filename kept for existing data dirs.
const auditDBPath = "xdlc-agent-history.db"

// version and commit are set via -ldflags at build time (see
// .goreleaser.yml); "dev" is what a plain `go build`/`go run` gets.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	root := &cobra.Command{
		Use:     "xdlc",
		Short:   "Agentic SDLC orchestrator — one loop, 3 gates",
		Version: fmt.Sprintf("%s (%s)", version, commit),
	}
	root.PersistentFlags().StringVar(&cfgPath, "config", "config.yaml", "path to config.yaml")

	root.AddCommand(
		daemonCmd(),
		gateCmd(),
		validateCmd(),
		promoteCmd(),
		backlogCmd(),
		historyCmd(),
		initCmd(),
		sessionsCmd(),
		doctorCmd(),
		demoCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func daemonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Run the orchestrator loop (webhook server + pollers)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			if issues := validate.Config(cfg); len(issues) > 0 {
				for _, i := range issues {
					fmt.Fprintln(os.Stderr, i.String())
				}
				return fmt.Errorf("daemon: %s: %d config issue(s) — fix before starting (or run xdlc validate)", cfgPath, len(issues))
			}
			if err := enforceWebhookSecrets(cfg); err != nil {
				return err
			}
			log := slog.New(slog.NewTextHandler(os.Stdout, nil))

			bl, err := backlog.Open("BACKLOG.md")
			if err != nil {
				return err
			}
			lessonStore, err := lessons.Open("LESSONS.md")
			if err != nil {
				return err
			}

			audit, err := store.Open(auditDBPath)
			if err != nil {
				return err
			}
			defer func() {
				if closeErr := audit.Close(); closeErr != nil {
					log.Error("audit store close failed", "error", closeErr)
				}
			}()

			tokens, err := ghclient.PreferAppThenPAT(cfg.GitHub, cfg.Repos...)
			if err != nil {
				return err
			}
			log.Info("github auth", "source", ghclient.SourceKind(tokens))

			metrics, otelShutdown, err := agentotel.Setup(cmd.Context(), log)
			if err != nil {
				log.Warn("otel setup failed", "error", err)
			} else {
				defer func() { _ = otelShutdown(context.Background()) }()
			}
			audit.Metrics = &metrics

			repoMgr := repos.NewManager("repos", cfg.Repos, tokens)
			runner := subagent.NewSubprocessRunner(subagent.Provider(cfg.Agent.Provider), cfg.Agent.Binary, cfg.Agent.Args, cfg.Agent.Timeout, cfg.Agent.ExtraEnvKeys)
			disp := dispatch.New(repoMgr, runner, log)
			disp.Metrics = &metrics
			disp.FixMode = cfg.Agent.FixMode
			disp.FixPlan = cfg.Agent.FixPlan
			disp.Lessons = lessonStore
			disp.RulesFile = cfg.Agent.RulesFile
			sessions, serr := session.Open(cfg.Agent.SessionsDir(), cfg.Agent.Sessions.Retain, cfg.Agent.Sessions.MaxFileBytes)
			if serr != nil {
				log.Warn("session recording disabled", "error", serr)
			} else if sessions != nil {
				log.Info("session recording", "dir", sessions.Root, "retain", sessions.Retain)
			}
			disp.Sessions = sessions
			disp.DefaultProvider = cfg.Agent.Provider
			disp.Route = cfg.Agent.Route
			disp.Providers = append([]string(nil), cfg.Agent.Providers...)
			disp.RouteMinSuccess = cfg.Agent.RouteMinSuccess
			disp.NewRunner = func(provider string) subagent.Runner {
				return subagent.NewSubprocessRunner(subagent.Provider(provider), cfg.Agent.Binary, cfg.Agent.Args, cfg.Agent.Timeout, cfg.Agent.ExtraEnvKeys)
			}
			disp.ProviderStats = func() map[string]dispatch.ProviderStats {
				all, err := audit.All()
				if err != nil {
					return nil
				}
				cutoff := time.Now().Add(-24 * time.Hour)
				var recent []store.Record
				for _, r := range all {
					if r.At.Before(cutoff) {
						continue
					}
					recent = append(recent, r)
				}
				return dispatch.StatsFromRecords(recent, cfg.Agent.Providers, cfg.Agent.Provider)
			}
			fixN := cfg.Agent.MaxConcurrentFixes
			if fixN == 0 {
				fixN = 2
			}
			disp.SetFixConcurrency(fixN)
			disp.FixBudget = cfg.Agent.FixBudget
			gh := ghclient.NewFromProvider(tokens)
			disp.FetchLogs = gh.FetchFailedJobLogs
			disp.FindPR = func(ctx context.Context, ownerRepo, branch string) (*dispatch.PRRef, error) {
				pr, err := gh.FindPRByBranch(ctx, ownerRepo, branch)
				if err != nil || pr == nil {
					return nil, err
				}
				return &dispatch.PRRef{Number: pr.Number, URL: pr.URL, State: pr.State}, nil
			}
			disp.CreatePR = func(ctx context.Context, ownerRepo, head, base, title, body string) (*dispatch.PRRef, error) {
				pr, err := gh.CreatePR(ctx, ownerRepo, head, base, title, body)
				if err != nil || pr == nil {
					return nil, err
				}
				return &dispatch.PRRef{Number: pr.Number, URL: pr.URL, State: pr.State}, nil
			}

			ciGate := gatebuild.CI(cfg, tokens)
			smokeGates := gatebuild.DevSmoke(cfg)
			if cfg.Agent.FixReverify {
				attempts := cfg.Agent.FixReverifyAttempts
				if attempts <= 0 {
					attempts = 6
				}
				interval := cfg.Agent.FixReverifyInterval
				if interval <= 0 {
					interval = 15 * time.Second
				}
				disp.Reverify = func(ctx context.Context, s orchestrator.Signal) error {
					return reverifyGate(ctx, s, repoMgr, ciGate, smokeGates, attempts, interval, log)
				}
			}

			o := orchestrator.New(disp, bl, log)
			rerunOn := true
			if cfg.Agent.CIRerunBeforeFix != nil {
				rerunOn = *cfg.Agent.CIRerunBeforeFix
			}
			if rerunOn {
				o.RerunCI = func(ctx context.Context, s orchestrator.Signal) (bool, error) {
					runURL, _ := s.Evidence["run_url"].(string)
					if runURL == "" {
						return false, fmt.Errorf("no run_url in evidence")
					}
					green, conclusion, err := gh.RerunAndWait(ctx, runURL)
					if err != nil {
						return false, err
					}
					if s.Evidence != nil {
						s.Evidence["rerun_conclusion"] = conclusion
					}
					if metrics.Reruns != nil {
						metrics.Reruns.Add(ctx, 1)
					}
					return green, nil
				}
			}
			o.Fleet = orchestrator.FleetPolicy{
				FlapMaxCycles:      cfg.Fleet.FlapMaxCycles,
				FlapWindow:         cfg.Fleet.FlapWindow,
				CircuitBreachRatio: cfg.Fleet.CircuitBreachRatio,
				RepoCount:          len(cfg.Repos),
				NotifyWebhookURL:   cfg.Fleet.NotifyWebhookURL,
				PatientZero:        cfg.Fleet.PatientZero,
			}
			o.RepoDeps = make(map[string][]string, len(cfg.Repos))
			o.PromotePins = make(map[string][]orchestrator.PromotePin)
			for _, r := range cfg.Repos {
				if len(r.DependsOn) > 0 {
					o.RepoDeps[r.Name] = append([]string(nil), r.DependsOn...)
				}
				if len(r.PromoteRequires) > 0 {
					pins := make([]orchestrator.PromotePin, 0, len(r.PromoteRequires))
					for _, p := range r.PromoteRequires {
						pins = append(pins, orchestrator.PromotePin{Repo: p.Repo, MinTag: p.MinTag})
					}
					o.PromotePins[r.Name] = pins
				}
			}
			o.RecentActions = audit.ActionsSince
			o.ProdTag = func(name string) (string, error) {
				return promote.ReadProdTag(repoMgr.Dir(name), name)
			}
			o.Suppressions = metrics.FleetSuppressions
			o.Audit = func(s orchestrator.Signal, action orchestrator.Action, dispatchErr error, started time.Time) error {
				status := store.StatusOK
				errMsg := ""
				if dispatchErr != nil {
					status = store.StatusError
					errMsg = dispatchErr.Error()
				}
				provider := ""
				if s.Evidence != nil {
					if v, ok := s.Evidence["agent_provider"].(string); ok {
						provider = v
					}
				}
				chainID := fmt.Sprintf("%s-%s-%d", s.Repo, s.Source, started.UnixNano())
				return audit.Append(store.Record{
					At:            time.Now().UTC(),
					Repo:          s.Repo,
					Source:        orchestrator.AuditSource(s),
					Kind:          string(s.Kind),
					Action:        string(action),
					Status:        status,
					Error:         errMsg,
					DurationMS:    time.Since(started).Milliseconds(),
					AgentProvider: provider,
					ChainID:       chainID,
					Evidence:      s.Evidence,
				})
			}

			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			// Bounded parallel pre-clone (issue #17); hard-reset still per-repo.
			var wg sync.WaitGroup
			sem := make(chan struct{}, 8)
			for _, r := range cfg.Repos {
				wg.Add(1)
				go func(name string) {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					if err := repoMgr.EnsureCloned(ctx, name); err != nil {
						log.Warn("pre-clone failed", "repo", name, "error", err)
					}
				}(r.Name)
			}
			wg.Wait()

			// Real-time sources: GitHub / ArgoCD / Alertmanager webhooks.
			addr := cfg.Server.Addr
			if addr == "" {
				addr = ":8080"
			}
			secretEnv := cfg.Server.GitHubWebhookSecretEnv
			if secretEnv == "" {
				secretEnv = "GITHUB_WEBHOOK_SECRET" //nolint:gosec // this is an env var *name*, not a credential value
			}
			argoSecretEnv := cfg.Server.ArgoCDWebhookSecretEnv
			if argoSecretEnv == "" {
				argoSecretEnv = "ARGOCD_WEBHOOK_SECRET" //nolint:gosec // env var name, not a secret value
			}
			amSecretEnv := cfg.Server.AlertmanagerWebhookSecretEnv
			if amSecretEnv == "" {
				amSecretEnv = "ALERTMANAGER_WEBHOOK_SECRET" //nolint:gosec // env var name, not a secret value
			}
			wh := &webhook.Server{
				Signals:       o.Signals,
				Secret:        os.Getenv(secretEnv),
				ArgoSecret:    os.Getenv(argoSecretEnv),
				AMSecret:      os.Getenv(amSecretEnv),
				RequireSecret: cfg.Server.RequireWebhookSecret,
				// Per-repo branch, so a repo on `branch: main` receives
				// CI deliveries (one resolver, shared with promote/revert).
				BranchFor:     repoMgr.Branch,
				DefaultBranch: repos.DefaultBranch,
				ResolveRepo:   repoMgr.Resolve,
				// An ArgoCD notification is a check-now trigger: the
				// verdict comes from the real gate, pinned to the dev tip
				// read before the probe.
				CheckSmoke: func(ctx context.Context, repo string) (bool, map[string]any, error) {
					g, ok := smokeGates[repo]
					if !ok {
						return false, nil, fmt.Errorf("no dev-smoke gate configured for repo %q", repo)
					}
					result, err := g.Check(ctx, repo)
					if err != nil {
						return false, nil, err
					}
					return result.Status == gate.StatusPass, result.Evidence, nil
				},
				ResolveSHA: repoMgr.RemoteSHA,
				ResolveArgoApp: func(appName string) (string, bool) {
					for _, r := range cfg.Repos {
						app := r.ArgoCDApp
						if app == "" {
							app = cfg.Gates.DevSmoke.ArgoCDApp
						}
						if app == appName {
							return r.Name, true
						}
					}
					return "", false
				},
				Log:     log,
				Metrics: &metrics,
			}
			rate := cfg.Server.WebhookRatePerSec
			if rate == 0 {
				rate = 20
			}
			burst := cfg.Server.WebhookRateBurst
			if burst == 0 {
				burst = 40
			}
			wh.Limiter = ratelimit.New(rate, burst)
			mux := http.NewServeMux()
			wh.Mount(mux)
			apiSrv := &api.Server{
				Cfg:         cfg,
				CfgPath:     cfgPath,
				Audit:       audit,
				BacklogPath: "BACKLOG.md",
				Version:     version,
				Started:     time.Now(),
				Log:         log,
				Token:       os.Getenv("XDLC_API_TOKEN"),        //nolint:gosec // G101: env lookup, not a hardcoded secret
				ViewerToken: os.Getenv("XDLC_API_VIEWER_TOKEN"), //nolint:gosec // G101: env lookup, not a hardcoded secret
				Enqueue:     func(sig orchestrator.Signal) { o.Signals <- sig },
				PRStatus: func(ctx context.Context, githubRepo string, number int) (api.PRLiveStatus, error) {
					pr, err := gh.GetPR(ctx, githubRepo, number)
					if err != nil {
						return api.PRLiveStatus{}, err
					}
					return api.PRLiveStatus{
						State: pr.State, Merged: pr.Merged,
						Title: pr.Title, CI: pr.CI, Reviewer: pr.Reviewer,
					}, nil
				},
				RepoDir:       repoMgr.Dir,
				FixQueueStats: disp.FixQueueStats,
			}
			if cfg.Server.OIDC.Enabled() {
				oidcAuth, err := setupOIDC(cmd.Context(), cfg.Server.OIDC)
				if err != nil {
					// Fatal, not a silent SSO no-op — same "reject, don't
					// ignore" policy as agent.mode/agent.fix_mode validation.
					return fmt.Errorf("oidc setup: %w", err)
				}
				oidcAuth.Mount(mux)
				apiSrv.SessionVerifier = oidcAuth.VerifySession
				log.Info("oidc sso enabled", "issuer", cfg.Server.OIDC.IssuerURL)
			}
			apiSrv.Mount(mux)
			console.Mount(mux)
			// Open scrape endpoint — no auth (Prometheus convention).
			if h := metrics.Handler(); h != nil {
				mux.Handle("/metrics", h)
			}
			httpSrv := &http.Server{
				Addr:              addr,
				Handler:           mux,
				ReadHeaderTimeout: 5 * time.Second,
				ReadTimeout:       15 * time.Second,
				WriteTimeout:      30 * time.Second,
				IdleTimeout:       60 * time.Second,
				MaxHeaderBytes:    1 << 20, // 1 MiB
			}
			go func() {
				log.Info("webhook server starting", "addr", addr)
				if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Error("webhook server failed", "error", err)
				}
			}()
			go func() {
				<-ctx.Done()
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = httpSrv.Shutdown(shutdownCtx)
			}()

			// Poll-driven sources (fallback when webhooks are quiet).
			for name, g := range smokeGates {
				interval := cfg.Gates.DevSmoke.Interval
				p := &poller.Poller{
					Gate:     g,
					Repos:    []string{name},
					Interval: interval,
					Source:   orchestrator.SourceDevGate,
					Signals:  o.Signals,
					Log:      log,
					Metrics:  &metrics,
					// A dev-smoke pass authorizes a promote, so pin it to
					// the commit that was probed.
					SHA: repoMgr.RemoteSHA,
				}
				go p.Run(ctx)
			}

			if cfg.Gates.ProdHealth.MetricsEndpoint() != "" {
				p := &poller.Poller{
					Gate:     gatebuild.ProdHealth(cfg),
					Repos:    gatebuild.ReposForGate(cfg, "prod-health"),
					Interval: cfg.Gates.ProdHealth.Interval,
					Source:   orchestrator.SourceProdHealth,
					Signals:  o.Signals,
					Log:      log,
					Metrics:  &metrics,
				}
				go p.Run(ctx)
			}

			for _, cfgEg := range cfg.Gates.External {
				if cfgEg.Name == "" || len(cfgEg.Command) == 0 {
					continue
				}
				trig := gate.Continuous
				switch cfgEg.Trigger {
				case "on_push":
					trig = gate.OnPush
				case "on_sync":
					trig = gate.OnSync
				}
				eg := &gate.ExternalGate{
					GateName: cfgEg.Name,
					Argv:     append([]string(nil), cfgEg.Command...),
					Trig:     trig,
					Timeout:  cfgEg.Timeout,
				}
				src := orchestrator.SourceCI
				if eg.Trigger() == gate.OnSync {
					src = orchestrator.SourceDevGate
				}
				p := &poller.Poller{
					Gate:     eg,
					Repos:    gatebuild.ExternalRepos(cfg, cfgEg),
					Interval: cfgEg.Interval,
					Source:   src,
					Signals:  o.Signals,
					Log:      log,
					Metrics:  &metrics,
				}
				go p.Run(ctx)
			}

			log.Info("xdlc daemon starting", "config", cfgPath)
			err = o.Run(ctx)
			if errors.Is(err, context.Canceled) {
				log.Info("shutting down")
				return nil
			}
			return err
		},
	}
}

func gateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gate",
		Short: "Run a single gate check (usable in CI too)",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "check <name>",
		Short: "Run one gate by name against every repo configured for it, exit non-zero on any fail",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			name := args[0]
			failed := false

			switch name {
			case "ci":
				tokens, err := ghclient.PreferAppThenPAT(cfg.GitHub, cfg.Repos...)
				if err != nil {
					return err
				}
				ciGate := gatebuild.CI(cfg, tokens)
				for _, r := range cfg.Repos {
					if !hasGate(r, "ci") {
						continue
					}
					// CIGate.Check needs "owner/repo", not the short config name.
					result, err := ciGate.Check(ctx, r.GitHub)
					if err != nil {
						fmt.Printf("ci %s: ERROR %v\n", r.Name, err)
						failed = true
						continue
					}
					fmt.Printf("ci %s: %s %v\n", r.Name, result.Status, result.Evidence)
					if result.Status == gate.StatusFail {
						failed = true
					}
				}

			case "dev-smoke":
				for repoName, g := range gatebuild.DevSmoke(cfg) {
					result, err := g.Check(ctx, repoName)
					if err != nil {
						fmt.Printf("dev-smoke %s: ERROR %v\n", repoName, err)
						failed = true
						continue
					}
					fmt.Printf("dev-smoke %s: %s %v\n", repoName, result.Status, result.Evidence)
					if result.Status == gate.StatusFail {
						failed = true
					}
				}

			case "prod-health":
				g := gatebuild.ProdHealth(cfg)
				for _, repoName := range gatebuild.ReposForGate(cfg, "prod-health") {
					result, err := g.Check(ctx, repoName)
					if err != nil {
						fmt.Printf("prod-health %s: ERROR %v\n", repoName, err)
						failed = true
						continue
					}
					fmt.Printf("prod-health %s: %s %v\n", repoName, result.Status, result.Evidence)
					if result.Status == gate.StatusFail {
						failed = true
					}
				}

			default:
				return fmt.Errorf("unknown gate %q (want ci, dev-smoke, or prod-health)", name)
			}

			if failed {
				os.Exit(1)
			}
			return nil
		},
	})
	return cmd
}

func validateCmd() *cobra.Command {
	var gitopsDir string
	var roleNamespace string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate config.yaml (required fields); optionally cross-check gitops/ argocd_app names",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}

			issues := validate.Config(cfg)
			gitopsIssues, err := validate.GitOps(cfg, gitopsDir)
			if err != nil {
				return fmt.Errorf("validate: %w", err)
			}
			issues = append(issues, gitopsIssues...)
			issues = append(issues, validate.RoleNamespace(cfg, roleNamespace)...)

			if len(issues) == 0 {
				fmt.Printf("%s OK\n", cfgPath)
				return nil
			}
			fmt.Fprintf(os.Stderr, "%s: %d issue(s)\n", cfgPath, len(issues))
			for _, i := range issues {
				fmt.Println(i.String())
			}
			os.Exit(1)
			return nil
		},
	}
	cmd.Flags().StringVar(&gitopsDir, "gitops-dir", "", "optional gitops/ dir to cross-check argocd_app names (empty skips)")
	cmd.Flags().StringVar(&roleNamespace, "role-namespace", "", "the Helm chart's role.namespace value, to catch it drifting from gates.dev-smoke.namespace (skipped if unset)")
	return cmd
}

// setupOIDC resolves env-held secrets and defaults from cfg, then runs
// OIDC discovery (authn.New) — see that function's doc for why a
// misconfigured/unreachable issuer is fatal here, not a silent no-op.
func setupOIDC(ctx context.Context, cfg config.OIDCConfig) (*authn.Authenticator, error) {
	clientSecretEnv := cfg.ClientSecretEnv
	if clientSecretEnv == "" {
		clientSecretEnv = "OIDC_CLIENT_SECRET" //nolint:gosec // env var name, not a secret value
	}
	sessionSecretEnv := cfg.SessionSecretEnv
	if sessionSecretEnv == "" {
		sessionSecretEnv = "OIDC_SESSION_SECRET" //nolint:gosec // env var name, not a secret value
	}
	cookieSecure := true
	if cfg.CookieSecure != nil {
		cookieSecure = *cfg.CookieSecure
	}

	return authn.New(ctx, authn.Config{
		IssuerURL:      cfg.IssuerURL,
		ClientID:       cfg.ClientID,
		ClientSecret:   os.Getenv(clientSecretEnv),
		RedirectURL:    cfg.RedirectURL,
		Scopes:         cfg.Scopes,
		GroupsClaim:    cfg.GroupsClaim,
		OperatorGroups: cfg.OperatorGroups,
		ViewerGroups:   cfg.ViewerGroups,
		SessionSecret:  []byte(os.Getenv(sessionSecretEnv)),
		SessionTTL:     cfg.SessionTTL,
		CookieSecure:   cookieSecure,
	})
}

// enforceWebhookSecrets fails closed when the daemon listens on a
// non-loopback address without require_webhook_secret. Local Kind/init
// may keep false on 127.0.0.1; binding :8080 (all interfaces) requires true.
func enforceWebhookSecrets(cfg *config.Config) error {
	if cfg.Server.RequireWebhookSecret {
		return nil
	}
	addr := cfg.Server.Addr
	if addr == "" {
		addr = ":8080"
	}
	if isLoopbackListenAddr(addr) {
		return nil
	}
	return fmt.Errorf("daemon: server.require_webhook_secret must be true when listening on %q (non-loopback); set it or bind 127.0.0.1", addr)
}

func isLoopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// Bare ":8080" → empty host → all interfaces.
		if strings.HasPrefix(addr, ":") {
			return false
		}
		host = addr
	}
	if host == "" {
		return false
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// reverifyGate polls the gate that triggered Fix until green or budget exhausted (#2).
func reverifyGate(
	ctx context.Context,
	s orchestrator.Signal,
	repoMgr *repos.Manager,
	ci *gate.CIGate,
	smoke map[string]*gate.SmokeGate,
	attempts int,
	interval time.Duration,
	log *slog.Logger,
) error {
	var g gate.Gate
	checkRepo := s.Repo
	switch s.Source {
	case orchestrator.SourceCI:
		g = ci
		if gh := repoMgr.GitHub(s.Repo); gh != "" {
			checkRepo = gh
		}
	case orchestrator.SourceDevGate:
		if sg, ok := smoke[s.Repo]; ok {
			g = sg
		}
	default:
		log.Info("fix reverify skipped for source", "source", s.Source, "repo", s.Repo)
		return nil
	}
	if g == nil {
		return fmt.Errorf("no gate for source %s", s.Source)
	}
	var last gate.Result
	for i := 0; i < attempts; i++ {
		res, err := g.Check(ctx, checkRepo)
		if err != nil {
			log.Warn("fix reverify check error", "repo", s.Repo, "attempt", i+1, "error", err)
		} else {
			last = res
			if res.Status == gate.StatusPass {
				return nil
			}
		}
		if i+1 == attempts {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
	return fmt.Errorf("gate %s still %s after %d attempts", g.Name(), last.Status, attempts)
}

func hasGate(r config.Repo, name string) bool {
	return slices.Contains(r.Gates, name)
}

func promoteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "promote <repo-dir>",
		Short: "Fast-forward develop -> main for a repo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), time.Minute)
			defer cancel()
			cfg, err := config.Load(cfgPath)
			if err != nil {
				// promote can run without full config; fall back to PAT only
				cfg = &config.Config{}
			}
			tokens, err := ghclient.PreferAppThenPAT(cfg.GitHub, cfg.Repos...)
			if err != nil {
				return err
			}
			tok, err := tokens.Token()
			if err != nil {
				return err
			}
			// CLI promote has no repo name and no gate result behind it:
			// default branches, and unpinned (empty SHA) because the
			// operator running this command is the authorization.
			return promote.FastForward(ctx, args[0], repos.AuthEnv(tok),
				repos.DefaultBranch, repos.DefaultProdBranch, "")
		},
	}
}

func backlogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backlog",
		Short: "Inspect BACKLOG.md",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Print BACKLOG.md",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile("BACKLOG.md")
			if err != nil {
				return err
			}
			fmt.Println(string(data))
			return nil
		},
	})
	return cmd
}

func historyCmd() *cobra.Command {
	var repoFilter string
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Print the structured signal+action audit log (see BACKLOG.md for the human-facing view)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat(auditDBPath); os.IsNotExist(err) {
				fmt.Println("no history yet — run `xdlc daemon` first")
				return nil
			}

			audit, err := store.OpenReadOnly(auditDBPath)
			if err != nil {
				return err
			}
			defer func() { _ = audit.Close() }()

			records, err := audit.All()
			if err != nil {
				return err
			}

			sort.Slice(records, func(i, j int) bool { return records[i].At.Before(records[j].At) })

			for _, r := range records {
				if repoFilter != "" && r.Repo != repoFilter {
					continue
				}
				fmt.Printf("%s repo=%s source=%s kind=%s action=%s %v\n",
					r.At.Format(time.RFC3339), r.Repo, r.Source, r.Kind, r.Action, r.Evidence)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoFilter, "repo", "", "only show entries for this repo")
	return cmd
}

func initCmd() *cobra.Command {
	var scanDir string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold a starter config.yaml (optionally from local clones)",
		Long: `Write a starter config.yaml.

With --scan, every Git checkout directly under DIR that has a GitHub
"origin" remote becomes a repos: entry with the ci gate, so you can point
the daemon at the repos already on this machine instead of typing them
out. Deploy-specific keys (argocd_app, probe_job) are left as comments
for you to fill in.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat("config.yaml"); err == nil {
				return fmt.Errorf("config.yaml already exists")
			}
			body := starterConfig
			if scanDir != "" {
				found, err := scanRepos(cmd.Context(), scanDir)
				if err != nil {
					return err
				}
				if len(found) == 0 {
					return fmt.Errorf("no git checkouts with a GitHub origin found under %s", scanDir)
				}
				body = configFromScan(found)
				for _, r := range found {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "found %s → %s\n", r.Name, r.GitHub)
				}
			}
			if err := os.WriteFile("config.yaml", []byte(body), 0o600); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "wrote config.yaml — next: xdlc doctor --config config.yaml")
			return nil
		},
	}
	cmd.Flags().StringVar(&scanDir, "scan", "", "directory of local git checkouts to seed repos: from")
	return cmd
}

// scannedRepo is one local checkout found by --scan.
type scannedRepo struct {
	Name   string // config short name (directory name)
	GitHub string // owner/repo from the origin remote
	Dir    string // absolute path to the checkout
	Branch string // current branch, when it is not the default
}

// scanRepos finds Git checkouts one level under root that have a GitHub
// origin. Non-Git directories, and Git repos whose origin is not GitHub,
// are skipped silently — a dev machine has plenty of both.
func scanRepos(ctx context.Context, root string) ([]scannedRepo, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", root, err)
	}
	var out []scannedRepo
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		dir := filepath.Join(root, ent.Name())
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			continue
		}
		remote, err := gitOutput(ctx, dir, "remote", "get-url", "origin")
		if err != nil || remote == "" {
			continue
		}
		ownerRepo := parseGitHubRemote(remote)
		if ownerRepo == "" {
			continue
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			abs = dir
		}
		branch, _ := gitOutput(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
		if branch == "develop" || branch == "HEAD" {
			branch = "" // develop is the daemon default; detached HEAD is not a branch
		}
		out = append(out, scannedRepo{Name: ent.Name(), GitHub: ownerRepo, Dir: abs, Branch: branch})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	// gosec G204: args are literals below; dir comes from the operator's
	// own --scan path.
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output() //nolint:gosec
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// parseGitHubRemote turns an SSH or HTTPS GitHub remote into
// "owner/repo". Anything else returns "".
func parseGitHubRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	remote = strings.TrimSuffix(remote, ".git")
	switch {
	case strings.HasPrefix(remote, "git@github.com:"):
		remote = strings.TrimPrefix(remote, "git@github.com:")
	case strings.HasPrefix(remote, "ssh://git@github.com/"):
		remote = strings.TrimPrefix(remote, "ssh://git@github.com/")
	case strings.HasPrefix(remote, "https://github.com/"):
		remote = strings.TrimPrefix(remote, "https://github.com/")
	case strings.HasPrefix(remote, "http://github.com/"):
		remote = strings.TrimPrefix(remote, "http://github.com/")
	default:
		return ""
	}
	parts := strings.Split(strings.Trim(remote, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// configFromScan renders a starter config whose repos: block is the
// scanned checkouts. Only the ci gate is enabled: dev-smoke and
// prod-health need cluster wiring this command cannot discover.
func configFromScan(found []scannedRepo) string {
	var b strings.Builder
	b.WriteString(scanConfigHeader)
	for _, r := range found {
		fmt.Fprintf(&b, "  - name: %s\n", r.Name)
		fmt.Fprintf(&b, "    github: %s\n", r.GitHub)
		fmt.Fprintf(&b, "    dir: %s\n", r.Dir)
		if r.Branch != "" {
			fmt.Fprintf(&b, "    branch: %s\n", r.Branch)
		}
		b.WriteString("    gates: [ci]\n")
		b.WriteString("    # argocd_app: dev-" + r.Name + "\n")
		b.WriteString("    # probe_job: smoke-e2e\n")
	}
	b.WriteString(scanConfigFooter)
	return b.String()
}

const scanConfigHeader = `# yaml-language-server: $schema=./schema/config.schema.json
# Generated by: xdlc init --scan
# Only the ci gate is on — add dev-smoke / prod-health once ArgoCD and
# Prometheus are wired (see docs/getting-started.md).
# API bearer: export XDLC_API_TOKEN (optional viewer: XDLC_API_VIEWER_TOKEN).
repos:
`

const scanConfigFooter = `
server:
  addr: ":8080"
  github_webhook_secret_env: GITHUB_WEBHOOK_SECRET
  require_webhook_secret: false # loopback only while this is false

gates:
  ci:
    trigger: on_push

agent:
  mode: subprocess
  provider: claude # claude | codex | cursor | gemini
  timeout: 10m
  # rules_file: ~/.xdlc/rules.md   # instructions added to every Fix prompt
  # sessions:
  #   dir: sessions               # prompt / output / diff per Fix
  #   retain: 720h
`

const starterConfig = `# yaml-language-server: $schema=./schema/config.schema.json
# See config.example.yaml for commented keys (fleet, oidc, …).
# API bearer: export XDLC_API_TOKEN (optional viewer: XDLC_API_VIEWER_TOKEN).
repos:
  - name: example-service
    github: your-org/example-service
    gates: [ci, dev-smoke, prod-health]
    argocd_app: dev-example-service
    probe_job: smoke-e2e

# github:  # App preferred; GITHUB_TOKEN is PAT fallback
#   app_id: 123456
#   installation_id: 12345678
#   private_key_env: GITHUB_APP_PRIVATE_KEY

server:
  addr: ":8080"
  github_webhook_secret_env: GITHUB_WEBHOOK_SECRET
  argocd_webhook_secret_env: ARGOCD_WEBHOOK_SECRET
  alertmanager_webhook_secret_env: ALERTMANAGER_WEBHOOK_SECRET
  require_webhook_secret: false # local Kind only; daemon fails if addr is not loopback with this false

gates:
  ci:
    trigger: on_push
  dev-smoke:
    trigger: on_sync
    namespace: dev
    interval: 30s
  prod-health:
    trigger: continuous
    metrics_url: http://prometheus.your-domain.io # any PromQL API
    thresholds:
      p95_ms: 500
      error_rate: 0.01
    interval: 30s
    p95_query: histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket{service="{{repo}}"}[5m])) by (le)) * 1000
    error_rate_query: sum(rate(http_requests_total{service="{{repo}}",status=~"5.."}[5m])) / sum(rate(http_requests_total{service="{{repo}}"}[5m]))

agent:
  mode: subprocess
  provider: claude # claude | codex | cursor
  timeout: 10m
`
