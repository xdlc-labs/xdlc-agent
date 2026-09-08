package validate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
)

func TestConfig(t *testing.T) {
	cases := []struct {
		name    string
		cfg     *config.Config
		wantMsg string // substring expected in the one issue; "" means no issues
	}{
		{
			name: "clean",
			cfg: &config.Config{
				Repos: []config.Repo{{
					Name: "svc", GitHub: "org/svc", Gates: []string{"ci", "dev-smoke"},
					ArgoCDApp: "dev-svc", ProbeJob: "smoke-e2e",
				}},
			},
			wantMsg: "",
		},
		{
			name: "ci gate needs owner/repo",
			cfg: &config.Config{
				Repos: []config.Repo{{Name: "svc", GitHub: "svc", Gates: []string{"ci"}}},
			},
			wantMsg: "must be \"owner/name\"",
		},
		{
			name: "dev-smoke needs argocd_app",
			cfg: &config.Config{
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc", Gates: []string{"dev-smoke"}, ProbeJob: "j"}},
			},
			wantMsg: "no argocd_app",
		},
		{
			name: "dev-smoke needs probe_job",
			cfg: &config.Config{
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc", Gates: []string{"dev-smoke"}, ArgoCDApp: "a"}},
			},
			wantMsg: "no probe_job",
		},
		{
			name: "duplicate repo name",
			cfg: &config.Config{
				Repos: []config.Repo{
					{Name: "svc", GitHub: "org/svc"},
					{Name: "svc", GitHub: "org/svc2"},
				},
			},
			wantMsg: "duplicate repo name",
		},
		{
			// Webhook-only prod-health: breaches arrive on
			// /webhooks/alertmanager, so there is no endpoint to poll
			// and no query to poll it with. Requiring metrics_url here
			// made that deployment unconfigurable.
			name: "prod-health webhook-only needs no metrics_url",
			cfg: &config.Config{
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc", Gates: []string{"prod-health"}}},
			},
			wantMsg: "",
		},
		{
			name: "polled prod-health needs p95_query",
			cfg: &config.Config{
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc", Gates: []string{"prod-health"}}},
				Gates: config.GatesConfig{ProdHealth: config.ProdHealthGateConfig{
					MetricsURL:     "http://prom.local",
					ErrorRateQuery: "err",
				}},
			},
			wantMsg: "p95_query is empty",
		},
		{
			name: "polled prod-health needs error_rate_query",
			cfg: &config.Config{
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc", Gates: []string{"prod-health"}}},
				Gates: config.GatesConfig{ProdHealth: config.ProdHealthGateConfig{
					MetricsURL: "http://prom.local",
					P95Query:   "p95",
				}},
			},
			wantMsg: "error_rate_query is empty",
		},
		{
			name: "fully configured polled prod-health ok",
			cfg: &config.Config{
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc", Gates: []string{"prod-health"}}},
				Gates: config.GatesConfig{ProdHealth: config.ProdHealthGateConfig{
					MetricsURL:     "http://prom.local",
					P95Query:       "p95",
					ErrorRateQuery: "err",
					Interval:       30 * time.Second,
					Timeout:        10 * time.Second,
				}},
			},
			wantMsg: "",
		},
		{
			// Queries with nothing to run them against: the poller
			// never starts, which reads as a typo'd metrics_url rather
			// than an intentional webhook-only setup.
			name: "prod-health queries without metrics_url flagged",
			cfg: &config.Config{
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc", Gates: []string{"prod-health"}}},
				Gates: config.GatesConfig{ProdHealth: config.ProdHealthGateConfig{P95Query: "p95"}},
			},
			wantMsg: "metrics_url is empty, so the prod-health poller will not run",
		},
		{
			name: "prod-health timeout must be under interval",
			cfg: &config.Config{
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc", Gates: []string{"prod-health"}}},
				Gates: config.GatesConfig{ProdHealth: config.ProdHealthGateConfig{
					MetricsURL:     "http://prom.local",
					P95Query:       "p95",
					ErrorRateQuery: "err",
					Interval:       10 * time.Second,
					Timeout:        30 * time.Second,
				}},
			},
			wantMsg: "must be shorter than interval",
		},
		{
			name: "agent.mode sdk rejected",
			cfg: &config.Config{
				Agent: config.AgentConfig{Mode: "sdk"},
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc"}},
			},
			wantMsg: `agent.mode "sdk" is not implemented`,
		},
		{
			name: "agent.mode unknown rejected",
			cfg: &config.Config{
				Agent: config.AgentConfig{Mode: "remote"},
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc"}},
			},
			wantMsg: `agent.mode "remote" unknown`,
		},
		{
			name: "agent.mode subprocess ok",
			cfg: &config.Config{
				Agent: config.AgentConfig{Mode: "subprocess"},
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc"}},
			},
			wantMsg: "",
		},
		{
			name: "agent.fix_mode empty ok",
			cfg: &config.Config{
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc"}},
			},
			wantMsg: "",
		},
		{
			name: "agent.fix_mode direct ok",
			cfg: &config.Config{
				Agent: config.AgentConfig{FixMode: "direct"},
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc"}},
			},
			wantMsg: "",
		},
		{
			name: "agent.fix_mode pr ok",
			cfg: &config.Config{
				Agent: config.AgentConfig{FixMode: "pr"},
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc"}},
			},
			wantMsg: "",
		},
		{
			name: "agent.fix_mode unknown rejected",
			cfg: &config.Config{
				Agent: config.AgentConfig{FixMode: "branch"},
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc"}},
			},
			wantMsg: `agent.fix_mode "branch" unknown`,
		},
		{
			name: "oidc enabled with client_id and redirect_url ok",
			cfg: &config.Config{
				Server: config.ServerConfig{OIDC: config.OIDCConfig{
					IssuerURL: "https://idp.example.com", ClientID: "c", RedirectURL: "https://agent.example.com/auth/callback",
				}},
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc"}},
			},
			wantMsg: "",
		},
		{
			name: "oidc enabled but missing client_id",
			cfg: &config.Config{
				Server: config.ServerConfig{OIDC: config.OIDCConfig{
					IssuerURL: "https://idp.example.com", RedirectURL: "https://agent.example.com/auth/callback",
				}},
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc"}},
			},
			wantMsg: "oidc.client_id is empty",
		},
		{
			name: "oidc enabled but missing redirect_url",
			cfg: &config.Config{
				Server: config.ServerConfig{OIDC: config.OIDCConfig{
					IssuerURL: "https://idp.example.com", ClientID: "c",
				}},
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc"}},
			},
			wantMsg: "oidc.redirect_url is empty",
		},
		{
			name: "oidc disabled (no issuer_url) skips all oidc checks",
			cfg: &config.Config{
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc"}},
			},
			wantMsg: "",
		},
		{
			name: "depends_on valid peer",
			cfg: &config.Config{
				Repos: []config.Repo{
					{Name: "api", GitHub: "org/api"},
					{Name: "web", GitHub: "org/web", DependsOn: []string{"api"}},
				},
			},
			wantMsg: "",
		},
		{
			name: "depends_on unknown rejected",
			cfg: &config.Config{
				Repos: []config.Repo{
					{Name: "web", GitHub: "org/web", DependsOn: []string{"missing"}},
				},
			},
			wantMsg: `depends_on "missing" is not a configured repo name`,
		},
		{
			name: "depends_on self rejected",
			cfg: &config.Config{
				Repos: []config.Repo{
					{Name: "web", GitHub: "org/web", DependsOn: []string{"web"}},
				},
			},
			wantMsg: "depends_on cannot include self",
		},
		{
			name: "fleet circuit ratio out of range",
			cfg: &config.Config{
				Fleet: config.FleetConfig{CircuitBreachRatio: 1.5},
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc"}},
			},
			wantMsg: "fleet.circuit_breach_ratio",
		},
		{
			name:    "empty repos rejected",
			cfg:     &config.Config{},
			wantMsg: "at least one repo is required",
		},
		{
			name: "unknown gate name rejected",
			cfg: &config.Config{
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc", Gates: []string{"ci", "bogus-gate"}}},
			},
			wantMsg: `unknown gate "bogus-gate"`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			issues := Config(c.cfg)
			if c.wantMsg == "" {
				if len(issues) != 0 {
					t.Fatalf("expected no issues, got %v", issues)
				}
				return
			}
			found := false
			for _, i := range issues {
				if strings.Contains(i.String(), c.wantMsg) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected an issue containing %q, got %v", c.wantMsg, issues)
			}
		})
	}
}

func TestGitOps(t *testing.T) {
	dir := t.TempDir()
	appsDev := filepath.Join(dir, "apps", "dev")
	if err := os.MkdirAll(appsDev, 0o755); err != nil {
		t.Fatal(err)
	}
	appYAML := `apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: dev-example-service
  namespace: argocd
spec:
  project: default
`
	if err := os.WriteFile(filepath.Join(appsDev, "example-service.yaml"), []byte(appYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Repos: []config.Repo{
			{Name: "example-service", GitHub: "org/example-service", Gates: []string{"dev-smoke"}, ArgoCDApp: "dev-example-service", ProbeJob: "smoke-e2e"},
			{Name: "typo-service", GitHub: "org/typo-service", Gates: []string{"dev-smoke"}, ArgoCDApp: "dev-typo-svc-wrong-name", ProbeJob: "smoke-e2e"},
		},
	}

	issues, err := GitOps(cfg, dir)
	if err != nil {
		t.Fatalf("GitOps: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected exactly 1 issue (typo-service), got %v", issues)
	}
	if issues[0].Repo != "typo-service" {
		t.Errorf("issue repo = %q, want typo-service", issues[0].Repo)
	}
}

func TestRoleNamespace(t *testing.T) {
	cfgWithDevSmoke := &config.Config{
		Repos: []config.Repo{{Name: "svc", Gates: []string{"dev-smoke"}, ArgoCDApp: "dev-svc", ProbeJob: "smoke"}},
	}
	cfgNoDevSmoke := &config.Config{
		Repos: []config.Repo{{Name: "svc", Gates: []string{"ci"}}},
	}

	if got := RoleNamespace(cfgWithDevSmoke, ""); got != nil {
		t.Errorf("empty roleNamespace should skip the check, got %v", got)
	}
	if got := RoleNamespace(cfgNoDevSmoke, "dev"); got != nil {
		t.Errorf("no dev-smoke repos should skip the check, got %v", got)
	}
	if got := RoleNamespace(cfgWithDevSmoke, "dev"); got != nil {
		t.Errorf("default namespace (dev) matching --role-namespace dev should pass, got %v", got)
	}
	if got := RoleNamespace(cfgWithDevSmoke, "staging"); len(got) != 1 {
		t.Fatalf("expected 1 mismatch issue, got %v", got)
	}

	cfgWithDevSmoke.Gates.DevSmoke.Namespace = "qa"
	if got := RoleNamespace(cfgWithDevSmoke, "qa"); got != nil {
		t.Errorf("explicit gates.dev-smoke.namespace matching --role-namespace should pass, got %v", got)
	}
	if got := RoleNamespace(cfgWithDevSmoke, "dev"); len(got) != 1 {
		t.Fatalf("explicit namespace vs default role-namespace should mismatch, got %v", got)
	}
}

func TestFixAttemptsNeedsReverify(t *testing.T) {
	cfg := &config.Config{
		Repos: []config.Repo{{Name: "svc", GitHub: "o/svc"}},
	}
	cfg.Agent.FixAttempts = 3

	issues := Config(cfg)
	if !hasIssueContaining(issues, "fix_reverify is off") {
		t.Fatalf("expected a fix_attempts/fix_reverify issue, got %v", issues)
	}

	cfg.Agent.FixReverify = true
	if issues := Config(cfg); hasIssueContaining(issues, "fix_reverify is off") {
		t.Fatalf("issue should clear once fix_reverify is on: %v", issues)
	}
}

// pr mode plus reverify can never pass: the fix lands on a PR branch while
// the re-check reads the tracked branch, which stays red until the PR merges.
// Rejecting it at startup beats failing every Fix with escalate=reverify_failed.
func TestPRModeRejectsReverify(t *testing.T) {
	const want = "cannot be used with agent.fix_mode"

	base := func() *config.Config {
		return &config.Config{Repos: []config.Repo{{Name: "svc", GitHub: "o/svc"}}}
	}

	cfg := base()
	cfg.Agent.FixMode = "pr"
	cfg.Agent.FixReverify = true
	if !hasIssueContaining(Config(cfg), want) {
		t.Fatalf("pr + fix_reverify should be rejected, got %v", Config(cfg))
	}

	// Each half on its own is a supported configuration.
	cfg = base()
	cfg.Agent.FixMode = "pr"
	if issues := Config(cfg); hasIssueContaining(issues, want) {
		t.Fatalf("pr mode alone is valid: %v", issues)
	}

	cfg = base()
	cfg.Agent.FixReverify = true
	if issues := Config(cfg); hasIssueContaining(issues, want) {
		t.Fatalf("fix_reverify alone is valid: %v", issues)
	}

	// "" means direct, so it must not be swept up with "pr".
	for _, mode := range []string{"", "direct"} {
		cfg = base()
		cfg.Agent.FixMode = mode
		cfg.Agent.FixReverify = true
		if issues := Config(cfg); hasIssueContaining(issues, want) {
			t.Fatalf("fix_mode %q with fix_reverify is valid: %v", mode, issues)
		}
	}
}

// A bad committer identity surfaces as "the Fix produced no commit"
// rather than as a config error, so it has to be caught here.
func TestCommitterIdentity(t *testing.T) {
	cases := []struct {
		name, cName, cEmail, wantMsg string
	}{
		{name: "unset is fine (defaults apply)"},
		{name: "explicit is fine", cName: "Fleet Bot", cEmail: "bot@example.test"},
		{name: "email without @", cEmail: "bot.example.test", wantMsg: "agent.committer.email"},
		{name: "email with angle brackets", cEmail: "<bot@example.test>", wantMsg: "agent.committer.email"},
		{name: "email with a space", cEmail: "bot @example.test", wantMsg: "agent.committer.email"},
		{name: "name with angle brackets", cName: "Bot <bot>", wantMsg: "agent.committer.name"},
		{name: "name with padding", cName: " Bot ", wantMsg: "agent.committer.name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{Repos: []config.Repo{{Name: "svc", GitHub: "o/svc"}}}
			cfg.Agent.Committer.Name = tc.cName
			cfg.Agent.Committer.Email = tc.cEmail
			issues := Config(cfg)
			switch {
			case tc.wantMsg == "" && len(issues) > 0:
				t.Fatalf("want no issues, got %v", issues)
			case tc.wantMsg != "" && !hasIssueContaining(issues, tc.wantMsg):
				t.Fatalf("want an issue mentioning %q, got %v", tc.wantMsg, issues)
			}
		})
	}
}

func hasIssueContaining(issues []Issue, want string) bool {
	for _, i := range issues {
		if strings.Contains(i.Message, want) {
			return true
		}
	}
	return false
}

// TestGitOpsMissingAppsDevReportsAnIssue: pointing --gitops-dir at a
// tree that does not use the apps/dev layout used to hard-error, which
// cobra turns into a usage dump instead of the one-line issue every
// other check produces (issue #45). It is a finding about the config,
// not a reason to abort the command — the same shape RoleNamespace has.
func TestGitOpsMissingAppsDevReportsAnIssue(t *testing.T) {
	cfg := &config.Config{
		Repos: []config.Repo{
			{Name: "svc", GitHub: "org/svc", Gates: []string{"dev-smoke"}, ArgoCDApp: "dev-svc", ProbeJob: "smoke-e2e"},
		},
	}

	// A real directory that simply has no apps/dev in it.
	issues, err := GitOps(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("GitOps returned an error instead of an issue: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected exactly 1 issue, got %v", issues)
	}
	if !strings.Contains(issues[0].Message, filepath.Join("apps", "dev")) {
		t.Errorf("issue does not name the missing directory: %q", issues[0].Message)
	}

	// And a --gitops-dir that does not exist at all.
	issues, err = GitOps(cfg, filepath.Join(t.TempDir(), "no-such-tree"))
	if err != nil {
		t.Fatalf("GitOps returned an error instead of an issue: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected exactly 1 issue, got %v", issues)
	}

	// An empty flag is still "not configured", not an issue.
	if issues, err := GitOps(cfg, ""); err != nil || issues != nil {
		t.Fatalf("empty gitops-dir = (%v, %v), want (nil, nil)", issues, err)
	}
}

// TestResolveArgoCDAppSharesTheFallback pins the one resolver
// gatebuild.DevSmoke, validate and `xdlc doctor` all read, so the app
// doctor tests for is the app the gate is built with (issue #45).
func TestResolveArgoCDAppSharesTheFallback(t *testing.T) {
	shared := &config.Config{Repos: []config.Repo{{Name: "svc"}}}
	shared.Gates.DevSmoke.ArgoCDApp = "dev-svc"
	shared.Gates.DevSmoke.ProbeJob = "smoke-e2e"
	perRepo := &config.Config{
		Repos: []config.Repo{{Name: "svc", ArgoCDApp: "dev-svc", ProbeJob: "smoke-e2e"}},
	}

	for _, c := range []struct {
		name string
		cfg  *config.Config
	}{
		{"shared default", shared},
		{"per-repo override", perRepo},
	} {
		if got := ResolveArgoCDApp(c.cfg, c.cfg.Repos[0]); got != "dev-svc" {
			t.Errorf("%s: ResolveArgoCDApp = %q, want dev-svc", c.name, got)
		}
		if got := ResolveProbeJob(c.cfg, c.cfg.Repos[0]); got != "smoke-e2e" {
			t.Errorf("%s: ResolveProbeJob = %q, want smoke-e2e", c.name, got)
		}
	}

	// The per-repo value wins when both are set.
	both := &config.Config{Repos: []config.Repo{{Name: "svc", ArgoCDApp: "dev-mine"}}}
	both.Gates.DevSmoke.ArgoCDApp = "dev-shared"
	if got := ResolveArgoCDApp(both, both.Repos[0]); got != "dev-mine" {
		t.Errorf("ResolveArgoCDApp = %q, want the per-repo dev-mine", got)
	}
}
