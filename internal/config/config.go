// Package config loads the YAML config that describes an org's repos,
// which gates apply to each, and thresholds for continuous gates.
package config

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root of config.yaml: which repos to watch, which gates
// apply org-wide, and the subagent (AI coding agent) settings.
type Config struct {
	Repos  []Repo       `yaml:"repos"`
	Gates  GatesConfig  `yaml:"gates"`
	Agent  AgentConfig  `yaml:"agent"`
	Server ServerConfig `yaml:"server"`
	GitHub GitHubConfig `yaml:"github"`
	Fleet  FleetConfig  `yaml:"fleet"`
}

// Repo is one entry under config.yaml's repos: list.
type Repo struct {
	Name   string   `yaml:"name"`
	GitHub string   `yaml:"github"` // "org/repo"
	Gates  []string `yaml:"gates"`
	// Dir is the local clone path used for subagent runs, revert, and
	// promote. Defaults to "repos/<name>" if empty.
	Dir string `yaml:"dir"`
	// Branch is the dev branch subagents/promote operate on; defaults to "develop".
	Branch string `yaml:"branch"`
	// ProdBranch is the production branch promote/revert target; defaults to "main".
	ProdBranch string `yaml:"prod_branch"`

	// DependsOn lists short names of other configured repos this service
	// needs at runtime (fleet policy). Used to suppress Revert on this repo
	// while an upstream dependency is still breaching. Empty = independent.
	DependsOn []string `yaml:"depends_on"`
	// PromoteRequires are SemVer/tag pins: Promote is suppressed unless
	// each named repo's gitops values/prod image.tag is >= MinTag.
	PromoteRequires []PromoteRequire `yaml:"promote_requires"`

	// Per-repo overrides for the dev-smoke gate — each repo maps to its
	// own ArgoCD Application and probe Job.
	ArgoCDApp string `yaml:"argocd_app"`
	ProbeJob  string `yaml:"probe_job"`
	// Thresholds optionally overrides gates.prod-health.thresholds for
	// this repo. Nil → use the org-wide defaults.
	Thresholds *Thresholds `yaml:"thresholds"`
	// AgentInstructions is optional trusted team text prepended to
	// FixPrompt (in addition to AGENTS.md / .xdlc/skills from the clone).
	AgentInstructions string `yaml:"agent_instructions"`
}

// PromoteRequire is a min image tag pin on a dependency repo (v2).
type PromoteRequire struct {
	Repo   string `yaml:"repo"`
	MinTag string `yaml:"min_tag"`
}

// FleetConfig is the dependency-aware / anti-thrash fleet policy.
// Zero values disable each guard (safe default for independent repos).
type FleetConfig struct {
	// FlapMaxCycles suppresses Fix/Revert after this many alternating
	// fix↔revert transitions for one repo inside FlapWindow. 0 = off.
	FlapMaxCycles int `yaml:"flap_max_cycles"`
	// FlapWindow is the lookback for flap detection. 0 → default 2h when
	// FlapMaxCycles > 0.
	FlapWindow time.Duration `yaml:"flap_window"`
	// CircuitBreachRatio pauses Fix/Revert/Promote fleet-wide when the
	// fraction of repos currently breaching (prod-health) is ≥ this
	// value (e.g. 0.3 = 30%). 0 = off. Must be in (0, 1] when set.
	CircuitBreachRatio float64 `yaml:"circuit_breach_ratio"`
	// NotifyWebhookURL, when set, receives a JSON POST on each fleet
	// suppression (Slack-compatible: {"text":"..."}). Empty = off.
	NotifyWebhookURL string `yaml:"notify_webhook_url"`
	// PatientZero, when true, enqueues Fix on upstream repos when a
	// leaf Revert is suppressed with escalate=root_cause (issue #4).
	PatientZero bool `yaml:"patient_zero"`
}

// ServerConfig configures the daemon's webhook HTTP listener.
type ServerConfig struct {
	Addr string `yaml:"addr"` // defaults to ":8080"
	// GitHubWebhookSecretEnv names the env var holding the HMAC secret
	// used to verify GitHub webhook deliveries. Defaults to
	// GITHUB_WEBHOOK_SECRET. Verification is skipped (with a warning)
	// if the env var is unset — fine for local dev, not for prod —
	// unless RequireWebhookSecret is true.
	GitHubWebhookSecretEnv string `yaml:"github_webhook_secret_env"`
	// ArgoCDWebhookSecretEnv / AlertmanagerWebhookSecretEnv name env
	// vars for those webhook paths (defaults below).
	ArgoCDWebhookSecretEnv       string `yaml:"argocd_webhook_secret_env"`
	AlertmanagerWebhookSecretEnv string `yaml:"alertmanager_webhook_secret_env"`
	// RequireWebhookSecret refuses webhook requests when the relevant
	// secret env is unset. Default false for local Kind; set true in prod.
	RequireWebhookSecret bool `yaml:"require_webhook_secret"`
	// WebhookRatePerSec is the shared token-bucket refill rate for all
	// /webhooks/* paths. 0 → default 20. Per-process only (see docs/capacity.md).
	WebhookRatePerSec float64 `yaml:"webhook_rate_per_sec"`
	// WebhookRateBurst is the token-bucket capacity. 0 → default 40.
	WebhookRateBurst int `yaml:"webhook_rate_burst"`
	// OIDC configures SSO login for the ops console, additive to the
	// bearer-token auth above (both work at once; OIDC sessions map to
	// the same operator/viewer roles). Disabled unless IssuerURL is set.
	OIDC OIDCConfig `yaml:"oidc"`
}

// OIDCConfig configures OIDC SSO for the console. See docs/console.md
// and docs/secrets.md. Discovery (issuer's
// /.well-known/openid-configuration) runs once at daemon startup — a
// misconfigured or unreachable issuer fails the daemon closed rather
// than silently running without SSO, matching this project's other
// "reject, don't ignore" config validation.
type OIDCConfig struct {
	// IssuerURL is the OIDC provider's issuer (e.g.
	// https://accounts.google.com, an Okta/Auth0/Keycloak realm URL).
	// Empty (default) disables OIDC entirely.
	IssuerURL string `yaml:"issuer_url"`
	ClientID  string `yaml:"client_id"`
	// ClientSecretEnv names the env var holding the OAuth2 client
	// secret. Defaults to OIDC_CLIENT_SECRET.
	ClientSecretEnv string `yaml:"client_secret_env"`
	// RedirectURL is this daemon's own callback, e.g.
	// https://xdlc-agent.example.com/auth/callback — must be registered
	// with the IdP.
	RedirectURL string `yaml:"redirect_url"`
	// Scopes defaults to [openid, email, profile] if empty.
	Scopes []string `yaml:"scopes"`
	// GroupsClaim names the ID token claim holding the user's groups
	// (a []string or single string). Defaults to "groups" — many IdPs
	// use a different claim name or a custom scope; check your IdP.
	GroupsClaim string `yaml:"groups_claim"`
	// OperatorGroups: any overlap with the token's GroupsClaim grants
	// the operator role (Fix/Promote/Revert). Empty → SSO can never
	// grant operator (fail-safe default; operator still works via the
	// bearer XDLC_API_TOKEN).
	OperatorGroups []string `yaml:"operator_groups"`
	// ViewerGroups: any overlap grants the viewer (read-only) role.
	// Empty → any authenticated user who isn't an operator gets viewer
	// (documented default — set this to restrict who can even view).
	ViewerGroups []string `yaml:"viewer_groups"`
	// SessionSecretEnv names the env var holding the HMAC key that
	// signs the session cookie issued after login. Defaults to
	// OIDC_SESSION_SECRET. Required (fails startup) whenever IssuerURL
	// is set — an OIDC login with no way to sign a session is a
	// half-built feature, not a safe default.
	SessionSecretEnv string `yaml:"session_secret_env"`
	// SessionTTL is how long an issued session cookie is valid. 0 →
	// default 8h.
	SessionTTL time.Duration `yaml:"session_ttl"`
	// CookieSecure sets the session/flow cookies' Secure flag. Defaults
	// to true (HTTPS only); set false only for local http:// testing —
	// never in a real deployment.
	CookieSecure *bool `yaml:"cookie_secure"`
}

// Enabled reports whether OIDC SSO is configured.
func (o OIDCConfig) Enabled() bool { return o.IssuerURL != "" }

// GitHubConfig configures GitHub App auth (preferred) with PAT fallback
// via GITHUB_TOKEN. Env vars win when yaml fields are empty.
type GitHubConfig struct {
	AppID             int64  `yaml:"app_id"`
	InstallationID    int64  `yaml:"installation_id"`
	PrivateKeyEnv     string `yaml:"private_key_env"`      // env holding PEM; default GITHUB_APP_PRIVATE_KEY
	PrivateKeyFileEnv string `yaml:"private_key_file_env"` // env holding path to PEM file
}

// GatesConfig holds the org-wide settings for each of the 3 built-in
// gates — see docs/gates.md.
type GatesConfig struct {
	CI         CIGateConfig         `yaml:"ci"`
	DevSmoke   DevSmokeGateConfig   `yaml:"dev-smoke"`
	ProdHealth ProdHealthGateConfig `yaml:"prod-health"`
	// External are out-of-tree gates invoked as commands (v2 plugin shape).
	External []ExternalGateConfig `yaml:"external"`
}

// ExternalGateConfig is one plugin-shaped gate: a command that reads JSON
// on stdin and writes {"ok":bool,"evidence":{…}} on stdout.
type ExternalGateConfig struct {
	Name     string        `yaml:"name"`     // config key / Gate.Name()
	Command  []string      `yaml:"command"`  // argv; required
	Trigger  string        `yaml:"trigger"`  // on_push | on_sync | continuous (default continuous)
	Interval time.Duration `yaml:"interval"` // for continuous; 0 → 30s
	Timeout  time.Duration `yaml:"timeout"`  // 0 → 60s
	Repos    []string      `yaml:"repos"`    // empty → all configured repos
}

// CIGateConfig configures the ci gate.
type CIGateConfig struct {
	Trigger string `yaml:"trigger"`
}

// DevSmokeGateConfig configures the dev-smoke gate's shared defaults —
// individual repos can override ArgoCDApp/ProbeJob (see Repo).
type DevSmokeGateConfig struct {
	Trigger   string        `yaml:"trigger"`
	ArgoCDApp string        `yaml:"argocd_app"`
	ProbeJob  string        `yaml:"probe_job"`
	Namespace string        `yaml:"namespace"` // k8s namespace the probe Job runs in, defaults to "dev"
	Interval  time.Duration `yaml:"interval"`  // poll interval, defaults to 30s
}

// ProdHealthGateConfig configures the prod-health gate — one shared
// PromQL query pair and org-wide default thresholds. Individual repos
// may override thresholds via Repo.Thresholds.
type ProdHealthGateConfig struct {
	Trigger string `yaml:"trigger"`
	// MetricsURL is any PromQL instant-query API base
	// (Prometheus, VictoriaMetrics, OpenObserve Prom API, Mimir).
	MetricsURL string `yaml:"metrics_url"`
	// PrometheusURL is a legacy alias for MetricsURL; used only when
	// MetricsURL is empty.
	PrometheusURL  string        `yaml:"prometheus_url"`
	Thresholds     Thresholds    `yaml:"thresholds"`
	Interval       time.Duration `yaml:"interval"`
	P95Query       string        `yaml:"p95_query"`
	ErrorRateQuery string        `yaml:"error_rate_query"`
}

// MetricsEndpoint returns metrics_url, falling back to prometheus_url.
func (p ProdHealthGateConfig) MetricsEndpoint() string {
	if p.MetricsURL != "" {
		return p.MetricsURL
	}
	return p.PrometheusURL
}

// Thresholds are the pass/fail limits the prod-health gate checks
// PromQL query results against.
type Thresholds struct {
	P95MS     float64 `yaml:"p95_ms"`
	ErrorRate float64 `yaml:"error_rate"`
}

// AgentConfig configures how the subagent runner invokes an AI coding
// agent — Claude Code, OpenAI Codex CLI, Cursor CLI, or anything else a
// fork wires into internal/subagent's provider table.
type AgentConfig struct {
	Mode     string `yaml:"mode"`     // "subprocess" | "sdk"
	Provider string `yaml:"provider"` // "claude" | "codex" | "cursor" | "gemini"
	Binary   string `yaml:"binary"`   // overrides the provider's default binary name
	// Args overrides the provider's default headless invocation. May
	// include the literal "{{prompt}}" marker (stripped at run; prompt
	// goes on stdin — see internal/subagent). Leave unset for defaults.
	Args    []string      `yaml:"args"`
	Timeout time.Duration `yaml:"timeout"`
	// MaxConcurrentFixes caps how many Fix subagent runs may execute at
	// once. 0 → default 2. Per-process only (see docs/capacity.md).
	MaxConcurrentFixes int `yaml:"max_concurrent_fixes"`
	// FixBudget is a soft deadline for one Fix run (issue #9). 0 = unlimited.
	FixBudget time.Duration `yaml:"fix_budget"`
	// FixMode controls how the Fix subagent lands changes:
	// "direct" (default, empty) commit+push current branch;
	// "pr" scratch branch + open PR, return when PR exists.
	FixMode string `yaml:"fix_mode"`
	// Providers is the candidate set for agent.route: cheapest.
	// Empty → use Provider only.
	Providers []string `yaml:"providers"`
	// Route selects Fix provider: "static" (default) uses Provider;
	// "cheapest" picks among Providers by cost weight × recent success.
	Route string `yaml:"route"`
	// RouteMinSuccess is the minimum recent Fix success rate (0–1) for a
	// provider to be eligible under route: cheapest. 0 → default 0.5;
	// providers with no history are always eligible.
	RouteMinSuccess float64 `yaml:"route_min_success"`
	// ExtraEnvKeys names additional env vars (by key, values taken from
	// the daemon's own process env) to pass through to the subagent
	// subprocess on top of the built-in allowlist (PATH/HOME/USER/
	// LOGNAME/LANG*/TZ + the provider API keys). Use this for e.g.
	// HTTPS_PROXY/NODE_EXTRA_CA_CERTS/SSL_CERT_FILE when the coding-agent
	// CLI needs them to reach its vendor's API from behind a corporate
	// egress proxy — see internal/subagent and docs/secrets.md. Never
	// add a key that holds a credential you don't want the subagent
	// (and the untrusted content it processes) to have.
	ExtraEnvKeys []string `yaml:"extra_env_keys"`
	// FixReverify, when true, re-checks the failing gate after a Fix
	// subagent exits successfully and only then records Status=ok.
	// Default false (exit-code-only success).
	FixReverify bool `yaml:"fix_reverify"`
	// FixReverifyAttempts is max gate polls after Fix (0 → 6).
	FixReverifyAttempts int `yaml:"fix_reverify_attempts"`
	// FixReverifyInterval between polls (0 → 15s).
	FixReverifyInterval time.Duration `yaml:"fix_reverify_interval"`
	// CIRerunBeforeFix, when true (default), tries GitHub
	// rerun-failed-jobs once per run_url before invoking the Fix agent.
	// Set false to skip the ladder.
	CIRerunBeforeFix *bool `yaml:"ci_rerun_before_fix"`
	// FixPlan enables optional diagnose-then-patch two-pass Fix (#23).
	// Default false — one-shot remains.
	FixPlan bool `yaml:"fix_plan"`
	// RulesFile is an optional daemon-wide instructions file appended to
	// every Fix prompt after the repo's own AGENTS.md / CLAUDE.md /
	// .xdlc/rules.md / .xdlc/skills. Use it for conventions that hold
	// across every repo this daemon watches ("never touch generated
	// files", "conventional commits"). Path is read at Fix time, so
	// edits apply without a restart. See docs/rules-and-skills.md.
	RulesFile string `yaml:"rules_file"`
	// Sessions configures on-disk recording of what each Fix agent was
	// told and what it did. See docs/sessions.md.
	Sessions SessionsConfig `yaml:"sessions"`
}

// SessionsConfig controls the per-Fix session recorder — the prompt,
// the agent's output, and the diff it produced, written to disk for the
// operator who reviews an automated Fix after the fact.
type SessionsConfig struct {
	// Enabled defaults to true. Set false to record nothing.
	Enabled *bool `yaml:"enabled"`
	// Dir is where session directories live. Empty → "sessions" beside
	// the working directory (same place BACKLOG.md and the audit DB go).
	Dir string `yaml:"dir"`
	// Retain is how long a session directory is kept. 0 → 30 days.
	Retain time.Duration `yaml:"retain"`
	// MaxFileBytes caps one artifact (prompt, output, diff). 0 → 2 MiB.
	MaxFileBytes int64 `yaml:"max_file_bytes"`
}

// SessionsEnabled reports whether Fix session recording is on
// (default true).
func (a AgentConfig) SessionsEnabled() bool {
	return a.Sessions.Enabled == nil || *a.Sessions.Enabled
}

// SessionsDir returns the configured session root, defaulting to
// "sessions". Empty string when recording is disabled.
func (a AgentConfig) SessionsDir() string {
	if !a.SessionsEnabled() {
		return ""
	}
	if a.Sessions.Dir != "" {
		return a.Sessions.Dir
	}
	return "sessions"
}

// Load reads and parses the YAML config at path. Unknown fields are
// rejected (KnownFields) so typos like `rep:` or `fix_mode: PR` under a
// misspelled parent fail closed instead of silently no-opping.
func Load(path string) (*Config, error) {
	// gosec G304: path is an operator-supplied CLI flag (--config),
	// the same trust level as any config file a CLI tool reads — not
	// user input from an untrusted request.
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return &c, nil
}
