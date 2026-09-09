// Package validate checks config.yaml for required fields and, when a
// gitops directory is provided, cross-checks argocd_app names against
// Application manifests under gitops/apps/dev.
package validate

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

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

	// pr mode lands the fix on its own xdlc-fix-* branch, but re-verification
	// re-reads the latest run on the tracked branch, which is still red while
	// the fix sits in an unmerged PR. So the combination cannot ever pass: every
	// Fix ends escalate=reverify_failed, and with a retry ladder the agent is
	// paid again per attempt for a fix that was already correct. Both flags are
	// off by default, so reaching here means someone opted into both, and
	// refusing to start beats failing every Fix in a way that looks like the
	// agent's fault.
	if cfg.Agent.FixMode == "pr" && cfg.Agent.FixReverify {
		issues = append(issues, Issue{Message: "agent.fix_reverify cannot be used with agent.fix_mode \"pr\": " +
			"the fix lands on a PR branch while the re-check reads the tracked branch, which stays red until the PR " +
			"merges, so every Fix would fail with escalate=reverify_failed. Turn fix_reverify off, or use fix_mode \"direct\""})
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

	// Negative reads as "off" at runtime, which is probably what was
	// meant, but a config that says -1 is more likely a typo than an
	// intent to disable history.
	if n := cfg.Agent.Sessions.PriorFixes; n != nil && *n < 0 {
		issues = append(issues, Issue{Message: fmt.Sprintf(
			"agent.sessions.prior_fixes %d is negative; use 0 to send no prior Fixes "+
				"into the prompt, or a small positive count", *n)})
	}

	// A stall watchdog that cannot fire before the run's own deadline is
	// only a slower version of the timeout it duplicates.
	if st := cfg.Agent.StallTimeout; st > 0 {
		if t := cfg.Agent.Timeout; t > 0 && st >= t {
			issues = append(issues, Issue{Message: fmt.Sprintf(
				"agent.stall_timeout (%s) is not shorter than agent.timeout (%s), so the run "+
					"is killed by the timeout before the watchdog can report a stall; "+
					"use a fraction of the timeout", st, t)})
		}
		if st < 30*time.Second {
			issues = append(issues, Issue{Message: fmt.Sprintf(
				"agent.stall_timeout %s is shorter than a coding agent's normal pause between "+
					"tool calls; a healthy Fix would be killed as stalled. Use minutes", st)})
		}
	}
	if cfg.Agent.StallTimeout < 0 {
		issues = append(issues, Issue{Message: fmt.Sprintf(
			"agent.stall_timeout %s is negative; use 0 to disable the watchdog", cfg.Agent.StallTimeout)})
	}

	// A malformed committer identity is not caught until the coding
	// agent's `git commit` fails inside a worktree, which reads as "the
	// Fix produced nothing" rather than "the config is wrong".
	if name := cfg.Agent.Committer.Name; name != strings.TrimSpace(name) || strings.ContainsAny(name, "<>\n") {
		issues = append(issues, Issue{Message: fmt.Sprintf(
			"agent.committer.name %q is not usable as a git author name "+
				"(no angle brackets, newlines, or leading/trailing spaces)", name)})
	}
	if email := cfg.Agent.Committer.Email; email != "" {
		if email != strings.TrimSpace(email) || strings.ContainsAny(email, "<> \n") || !strings.Contains(email, "@") {
			issues = append(issues, Issue{Message: fmt.Sprintf(
				"agent.committer.email %q is not an email address git will accept", email)})
		}
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
			if ResolveArgoCDApp(cfg, r) == "" {
				issues = append(issues, Issue{Repo: r.Name, Message: "dev-smoke gate configured but no argocd_app (repo or gates.dev-smoke default)"})
			}
			if ResolveProbeJob(cfg, r) == "" {
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

	// prod-health has two routes, and only one of them polls: the
	// Alertmanager webhook (POST /webhooks/alertmanager) pushes
	// breaches, the poller queries PromQL for them. metrics_url is what
	// switches the poller on (cmd/xdlc-agent only starts it when the
	// endpoint is set), so it is the switch we key off here too —
	// demanding it, p95_query and error_rate_query from every
	// prod-health user made a webhook-only deployment unconfigurable
	// without naming a metrics endpoint it never intends to query.
	if reposForGate(cfg, "prod-health") != nil {
		ph := cfg.Gates.ProdHealth
		switch {
		case ph.MetricsEndpoint() != "":
			// Polled route: the queries are what it polls with.
			if ph.P95Query == "" {
				issues = append(issues, Issue{Message: "prod-health gate used by a repo but gates.prod-health.p95_query is empty"})
			}
			if ph.ErrorRateQuery == "" {
				issues = append(issues, Issue{Message: "prod-health gate used by a repo but gates.prod-health.error_rate_query is empty"})
			}
			if ph.Timeout > 0 && ph.Interval > 0 && ph.Timeout >= ph.Interval {
				issues = append(issues, Issue{Message: fmt.Sprintf(
					"gates.prod-health.timeout %v must be shorter than interval %v, or ticks overrun each other",
					ph.Timeout, ph.Interval)})
			}
		case ph.P95Query != "" || ph.ErrorRateQuery != "":
			// Queries with nothing to run them against: the poller
			// never starts, so this reads as a webhook-only setup that
			// is really a missing/typo'd metrics_url.
			issues = append(issues, Issue{Message: "gates.prod-health.p95_query/error_rate_query are set but " +
				"gates.prod-health.metrics_url is empty, so the prod-health poller will not run; set metrics_url, " +
				"or drop the queries if breaches arrive via the Alertmanager webhook"})
		}
		// Otherwise: webhook-only prod-health. Nothing to require —
		// the alert supplies the verdict, so there is no endpoint and
		// no query.
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
	appsDev := filepath.Join(gitopsDir, "apps", "dev")
	// Guard before reading, the way RoleNamespace guards before
	// comparing: a GitOps tree that simply does not use the apps/dev
	// layout is a *finding about the config*, not a reason to abort the
	// command. Reading first turned it into a returned error, which
	// `validate` and `doctor` both surface as a cobra usage dump instead
	// of the one-line issue every other check produces (issue #45).
	if _, err := os.Stat(appsDev); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []Issue{{Message: fmt.Sprintf(
				"--gitops-dir %s has no %s directory, so no ArgoCD Application manifests could be "+
					"cross-checked against argocd_app; point it at the root of a tree laid out as "+
					"apps/dev/<app>.yaml, or drop the flag", gitopsDir, filepath.Join("apps", "dev"))}}, nil
		}
		return nil, fmt.Errorf("validate: reading %s: %w", appsDev, err)
	}
	appNames, err := applicationNames(appsDev)
	if err != nil {
		return nil, fmt.Errorf("validate: reading %s: %w", appsDev, err)
	}

	var issues []Issue
	for _, r := range cfg.Repos {
		if !hasGate(r, "dev-smoke") {
			continue
		}
		app := ResolveArgoCDApp(cfg, r)
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

// ResolveArgoCDApp returns the ArgoCD Application name r's dev-smoke
// gate will actually be built with: the repo's own argocd_app, falling
// back to the shared gates.dev-smoke.argocd_app. Exported because
// gatebuild.DevSmoke (which builds the gate) and `xdlc doctor` (which
// decides whether the `argocd` binary is required) have to answer the
// same question the same way. doctor used to read r.ArgoCDApp alone, so
// two semantically identical configs — one naming the app per repo, one
// sharing it under gates.dev-smoke — got opposite verdicts, and doctor
// green-lit a config whose dev-smoke gate could not run (issue #45).
func ResolveArgoCDApp(cfg *config.Config, r config.Repo) string {
	if r.ArgoCDApp != "" {
		return r.ArgoCDApp
	}
	return cfg.Gates.DevSmoke.ArgoCDApp
}

// ResolveProbeJob returns the probe Job name r's dev-smoke gate will be
// built with — the repo's own probe_job, falling back to the shared
// gates.dev-smoke.probe_job. Same single-source-of-truth reason as
// ResolveArgoCDApp.
func ResolveProbeJob(cfg *config.Config, r config.Repo) string {
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
