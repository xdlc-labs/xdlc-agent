// Package validate checks config.yaml for required fields and, when a
// gitops directory is provided, cross-checks argocd_app names against
// Application manifests under gitops/apps/dev.
package validate

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
)

// Issue is one problem found. Repo is empty for config-wide issues.
type Issue struct {
	Repo    string
	Message string
}

func (i Issue) String() string {
	if i.Repo == "" {
		return i.Message
	}
	return fmt.Sprintf("%s: %s", i.Repo, i.Message)
}

// Config validates cfg on its own — checks independent of gitops/ (repo
// identifiers well-formed, prod-health has the fields it needs).
func Config(cfg *config.Config) []Issue {
	var issues []Issue

	if len(cfg.Repos) == 0 {
		issues = append(issues, Issue{Message: "repos: at least one repo is required"})
	}

	knownGates := map[string]bool{
		"ci": true, "dev-smoke": true, "prod-health": true,
	}
	for _, eg := range cfg.Gates.External {
		if eg.Name != "" {
			knownGates[eg.Name] = true
		}
	}

	switch cfg.Agent.Mode {
	case "", "subprocess":
		// ok — default and only implemented mode
	case "sdk":
		issues = append(issues, Issue{Message: `agent.mode "sdk" is not implemented; use "subprocess" or omit`})
	default:
		issues = append(issues, Issue{Message: fmt.Sprintf("agent.mode %q unknown; use \"subprocess\" or omit", cfg.Agent.Mode)})
	}

	switch cfg.Agent.FixMode {
	case "", "direct", "pr":
		// ok — empty → direct
	default:
		issues = append(issues, Issue{Message: fmt.Sprintf("agent.fix_mode %q unknown; use \"direct\", \"pr\", or omit", cfg.Agent.FixMode)})
	}

	// A retry ladder needs a gate re-check to learn that the previous
	// attempt failed. Without one the daemon clamps back to a single
	// attempt, so say that here rather than at 3am in a log line.
	if cfg.Agent.FixAttempts > 1 && !cfg.Agent.FixReverify {
		issues = append(issues, Issue{Message: fmt.Sprintf(
			"agent.fix_attempts is %d but agent.fix_reverify is off; "+
				"without a gate re-check nothing can tell an attempt failed, so only one will run",
			cfg.Agent.FixAttempts)})
	}
	if cfg.Agent.FixAttempts < 0 {
		issues = append(issues, Issue{Message: fmt.Sprintf(
			"agent.fix_attempts %d is negative; use 1 (single shot) or higher", cfg.Agent.FixAttempts)})
	}

	if cfg.Server.OIDC.Enabled() {
		// Static checks only — authn.New (network-dependent: discovery +
		// JWKS) does the rest at daemon startup and fails closed there.
		if cfg.Server.OIDC.ClientID == "" {
			issues = append(issues, Issue{Message: "oidc.issuer_url is set but oidc.client_id is empty"})
		}
		if cfg.Server.OIDC.RedirectURL == "" {
			issues = append(issues, Issue{Message: "oidc.issuer_url is set but oidc.redirect_url is empty"})
		}
		issuer := cfg.Server.OIDC.IssuerURL
		if !strings.HasPrefix(strings.ToLower(issuer), "https://") &&
			!strings.HasPrefix(strings.ToLower(issuer), "http://127.") &&
			!strings.HasPrefix(strings.ToLower(issuer), "http://localhost") &&
			!strings.HasPrefix(strings.ToLower(issuer), "http://[::1]") {
			issues = append(issues, Issue{Message: fmt.Sprintf("oidc.issuer_url must use https:// (or http:// loopback for local IdPs), got %q", issuer)})
		}
		// Deliberately not flagging an empty operator_groups: that's a
		// valid "SSO for viewer-only access, operator stays bearer-token
		// only" configuration, not a mistake — see OIDCConfig's doc comment.
	}

	seen := map[string]bool{}
	for _, r := range cfg.Repos {
		if r.Name == "" {
			issues = append(issues, Issue{Message: "repo has no name"})
			continue
		}
		if seen[r.Name] {
			issues = append(issues, Issue{Repo: r.Name, Message: "duplicate repo name"})
		}
		seen[r.Name] = true

		for _, g := range r.Gates {
			if !knownGates[g] {
				issues = append(issues, Issue{Repo: r.Name, Message: fmt.Sprintf("unknown gate %q (want ci, dev-smoke, prod-health, or a gates.external name)", g)})
			}
		}

		if hasGate(r, "ci") && !strings.Contains(r.GitHub, "/") {
			issues = append(issues, Issue{Repo: r.Name, Message: fmt.Sprintf("github: %q must be \"owner/name\" (ci gate needs it)", r.GitHub)})
		}

		if hasGate(r, "dev-smoke") {
			if resolveArgoCDApp(cfg, r) == "" {
				issues = append(issues, Issue{Repo: r.Name, Message: "dev-smoke gate configured but no argocd_app (repo or gates.dev-smoke default)"})
			}
			if resolveProbeJob(cfg, r) == "" {
				issues = append(issues, Issue{Repo: r.Name, Message: "dev-smoke gate configured but no probe_job (repo or gates.dev-smoke default)"})
			}
		}
	}

	for _, r := range cfg.Repos {
		for _, dep := range r.DependsOn {
			if dep == "" {
				issues = append(issues, Issue{Repo: r.Name, Message: "depends_on contains an empty name"})
				continue
			}
			if dep == r.Name {
				issues = append(issues, Issue{Repo: r.Name, Message: "depends_on cannot include self"})
				continue
			}
			if !seen[dep] {
				issues = append(issues, Issue{Repo: r.Name, Message: fmt.Sprintf("depends_on %q is not a configured repo name", dep)})
			}
		}
		for _, pin := range r.PromoteRequires {
			if pin.Repo == "" || pin.MinTag == "" {
				issues = append(issues, Issue{Repo: r.Name, Message: "promote_requires entries need repo and min_tag"})
				continue
			}
			if !seen[pin.Repo] {
				issues = append(issues, Issue{Repo: r.Name, Message: fmt.Sprintf("promote_requires repo %q is not a configured repo name", pin.Repo)})
			}
		}
	}

	for _, eg := range cfg.Gates.External {
		if eg.Name == "" {
			issues = append(issues, Issue{Message: "gates.external entry has empty name"})
			continue
		}
		if len(eg.Command) == 0 {
			issues = append(issues, Issue{Message: fmt.Sprintf("gates.external %q has empty command", eg.Name)})
		}
		switch eg.Trigger {
		case "", "on_push", "on_sync", "continuous":
		default:
			issues = append(issues, Issue{Message: fmt.Sprintf("gates.external %q trigger %q unknown", eg.Name, eg.Trigger)})
		}
	}

	switch cfg.Agent.Route {
	case "", "static", "cheapest":
	default:
		issues = append(issues, Issue{Message: fmt.Sprintf("agent.route %q unknown; use \"static\", \"cheapest\", or omit", cfg.Agent.Route)})
	}

	if cfg.Fleet.CircuitBreachRatio < 0 || cfg.Fleet.CircuitBreachRatio > 1 {
		issues = append(issues, Issue{Message: fmt.Sprintf("fleet.circuit_breach_ratio %v must be between 0 and 1", cfg.Fleet.CircuitBreachRatio)})
	}
	if cfg.Fleet.FlapMaxCycles < 0 {
		issues = append(issues, Issue{Message: fmt.Sprintf("fleet.flap_max_cycles %d must be >= 0", cfg.Fleet.FlapMaxCycles)})
	}

	if reposForGate(cfg, "prod-health") != nil {
		if cfg.Gates.ProdHealth.MetricsEndpoint() == "" {
			issues = append(issues, Issue{Message: "prod-health gate used by a repo but gates.prod-health.metrics_url is empty"})
		}
		if cfg.Gates.ProdHealth.P95Query == "" {
			issues = append(issues, Issue{Message: "prod-health gate used by a repo but gates.prod-health.p95_query is empty"})
		}
		if cfg.Gates.ProdHealth.ErrorRateQuery == "" {
			issues = append(issues, Issue{Message: "prod-health gate used by a repo but gates.prod-health.error_rate_query is empty"})
		}
	}

	return issues
}

// RoleNamespace checks cfg's effective dev-smoke probe namespace
// (gates.dev-smoke.namespace, defaulting to "dev") against roleNamespace
// — the deploy/helm/xdlc-agent chart's role.namespace value, which
// nothing else cross-checks against config.yaml. The chart's RBAC Role
// only grants Job/Pod-log read access in role.namespace; a mismatch
// fails safe (kubectl gets a permission error) but shows up as a
// confusing dev-smoke gate failure instead of a clear config error.
// roleNamespace is operator-supplied (there's no way to read Helm
// values.yaml from config.yaml alone) — pass "" to skip this check.
func RoleNamespace(cfg *config.Config, roleNamespace string) []Issue {
	if roleNamespace == "" {
		return nil
	}
	if reposForGate(cfg, "dev-smoke") == nil {
		return nil // nothing to check — no repo uses dev-smoke
	}
	ns := cfg.Gates.DevSmoke.Namespace
	if ns == "" {
		ns = "dev"
	}
	if ns != roleNamespace {
		return []Issue{{Message: fmt.Sprintf(
			"gates.dev-smoke.namespace %q does not match --role-namespace %q — "+
				"the chart's RBAC Role only grants access in role.namespace; "+
				"dev-smoke will fail with a permission error, not a config error", ns, roleNamespace)}}
	}
	return nil
}

// GitOps cross-checks cfg against the ArgoCD Application manifests under
// gitopsDir/apps/dev — every repo's resolved argocd_app must match some
// Application's metadata.name there.
func GitOps(cfg *config.Config, gitopsDir string) ([]Issue, error) {
	if strings.TrimSpace(gitopsDir) == "" {
		return nil, nil // optional — full gitops tree lives outside this repo
	}
	appNames, err := applicationNames(filepath.Join(gitopsDir, "apps", "dev"))
	if err != nil {
		return nil, fmt.Errorf("validate: reading %s: %w", filepath.Join(gitopsDir, "apps", "dev"), err)
	}

	var issues []Issue
	for _, r := range cfg.Repos {
		if !hasGate(r, "dev-smoke") {
			continue
		}
		app := resolveArgoCDApp(cfg, r)
		if app == "" {
			continue // already reported by Config()
		}
		if !appNames[app] {
			issues = append(issues, Issue{Repo: r.Name, Message: fmt.Sprintf(
				"argocd_app %q has no matching Application (metadata.name) under %s/apps/dev", app, gitopsDir)})
		}
	}
	return issues, nil
}

func resolveArgoCDApp(cfg *config.Config, r config.Repo) string {
	if r.ArgoCDApp != "" {
		return r.ArgoCDApp
	}
	return cfg.Gates.DevSmoke.ArgoCDApp
}

func resolveProbeJob(cfg *config.Config, r config.Repo) string {
	if r.ProbeJob != "" {
		return r.ProbeJob
	}
	return cfg.Gates.DevSmoke.ProbeJob
}

func hasGate(r config.Repo, name string) bool {
	for _, g := range r.Gates {
		if g == name {
			return true
		}
	}
	return false
}

func reposForGate(cfg *config.Config, name string) []string {
	var out []string
	for _, r := range cfg.Repos {
		if hasGate(r, name) {
			out = append(out, r.Name)
		}
	}
	return out
}

type applicationManifest struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
}

// applicationNames reads every *.yaml in dir and collects the
// metadata.name of any document with kind: Application.
func applicationNames(dir string) (map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	names := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") && !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		// gosec G304: dir comes from the --gitops-dir CLI flag, entry
		// names from os.ReadDir on it — operator-controlled paths, not
		// external input.
		data, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec
		if err != nil {
			return nil, err
		}

		dec := yaml.NewDecoder(bytes.NewReader(data))
		for {
			var m applicationManifest
			if err := dec.Decode(&m); err != nil {
				break // EOF or malformed — either way, stop reading this file
			}
			if m.Kind == "Application" && m.Metadata.Name != "" {
				names[m.Metadata.Name] = true
			}
		}
	}
	return names, nil
}
