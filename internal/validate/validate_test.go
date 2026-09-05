package validate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
			name: "prod-health needs metrics_url",
			cfg: &config.Config{
				Repos: []config.Repo{{Name: "svc", GitHub: "org/svc", Gates: []string{"prod-health"}}},
			},
			wantMsg: "metrics_url is empty",
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
