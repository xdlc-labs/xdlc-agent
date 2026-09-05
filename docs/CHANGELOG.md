# Changelog

All notable changes to this project will be documented in this file.

Format based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- `xdlc-agent doctor` — PATH / token / config / optional Prometheus checks (#12)
- Docs: [vs alternatives](vs-alternatives.md), [why not a GitHub Action](why-not-github-action.md) (#13)
- Console: distinct loading / empty / error states (fetch throws; Skeleton + QueryError) (#7)
- FixPrompt honors `AGENTS.md`, `.xdlc/skills/*.md`, `repos[].agent_instructions` (#21)
- CI flake ladder: rerun-failed-jobs once per `run_url` before Fix (`ci_rerun_before_fix`, default on) (#3)
- Optional `agent.fix_reverify` — Fix Status=ok only after gate re-check (#2)
- Structural escalate before Runner: `escalate=structural` on rewrite / cross-service smells; manual Fix bypasses (#22)
- Console Fix-PR work queue: title, age, CI, merged, reviewer, `?all=1`, Actions nav badge (#14)
- Optional `agent.fix_plan` — diagnose-then-patch two-pass Fix; default off (#23)
- Selective FixPrompt evidence: keep logs/conclusion/run_url, drop filler first (#20)
- LESSONS.md inject into FixPrompt across runs (#19)
- Prod-health poller: bounded parallel Checks (default 8) + slow-tick warn (#10)
- Fleet `patient_zero`: enqueue Fix on upstream when leaf suppressed root_cause (#4)

### Changed

- Subagent: prompt on stdin (not argv); kill process group on timeout; external gates get env allowlist + same kill hygiene (#11)
- `EnsureCloned`: skip fetch when HEAD matches `origin/<branch>` and tree clean; shallow first clone; parallel startup pre-clone (#17)

## [0.0.1-beta.1] - 2026-09-05

First public beta of the open-source `xdlc-agent` daemon (MIT).

### Added

- Open-core public release: daemon, ops console, agent Helm chart
- Signal loop: CI / DEV smoke / PROD health → Fix / Promote / Revert
- BYO coding agents: `claude`, `codex`, `cursor`
- Ops console with bearer auth, overview, repos, gates, activity, actions
- `make bench` + microbenchmarks for Decide, validate.Config, store, ratelimit, FixPrompt
- Docs: architecture, contributing, security, CoC, changelog

### Notes

- Helm chart / image tag: `0.0.1-beta.1`
- Prior `1.x` / `2.x` CHANGELOG entries below describe pre-public stealth work;
  treat this beta as the first installable OSS cut

## [2.0.0] - 2026-09-04

### Added

- SemVer/tag promote pins: `repos[].promote_requires` (`escalate=deps_pin`)
- External/plugin gates: `gates.external[]` command protocol +
  `scripts/gates/example-external-gate.sh`
- Provider routing: `agent.route: cheapest` among `agent.providers`

### Changed

- Helm chart `version` / `appVersion` aligned to **2.0.0**
- Release workflow: GHCR cosign/SBOM uses GoReleaser version (no leading `v`);
  `workflow_dispatch` can re-sign an existing tag

## [1.0.0] - 2026-09-04

### Added

- Fleet policy: `repos[].depends_on`, `fleet.flap_*` /
  `fleet.circuit_breach_ratio`, promote gate while deps unhealthy
  (`escalate=deps_unhealthy`), optional `fleet.notify_webhook_url`;
  suppress Fix/Revert/Promote with escalate evidence + metric
  `xdlc_agent_fleet_suppressions_total`
- OIDC/SSO for the ops console (`server.oidc`): authorization-code + PKCE,
  signed session cookie, additive to bearer tokens; `GET /api/whoami`,
  `/auth/login`, `/auth/config`
- `agent.fix_mode: direct|pr` — `pr` opens a scratch-branch Fix PR;
  work queue at `GET /api/prs` with live GitHub recheck (open-only default;
  `?all=1` for history; `stale` when GH is unreachable)
- `GET /api/kpis` — Fix cost / success-rate / duration aggregates from the
  audit store; thin strip on the Activity page
- Opt-in PVC backup Helm templates (`backup.velero`, `backup.csiSnapshot`)
- Multi-cloud starters: `infra/{aws-eks,gcp-gke,azure-aks}`, bootstrap
  scripts, overlays; `make terraform-validate` + CI job
  (`terraform init -backend=false` + `validate`)
- Release supply chain: SPDX SBOM (Syft) + keyless cosign (GitHub OIDC)
  on tag push; verify notes in [docs/deployment.md](docs/deployment.md)
- Launch-readiness docs: upgrade, versioning, API reference, DR,
  threat model, compliance, support, capacity; GOVERNANCE
- Pod security hardening, PDB, NetworkPolicy; AWS/GCP/Azure overlays
- Console write actions, theme/i18n, degraded-state banner; UI in CI
- CI: `govulncheck`, Trivy fs scan

### Changed

- Stack freshness: Contour ingress, Argo CD 3.5 / chart 10.7, kubectl 1.34,
  OTel Collector 0.160, EKS module/provider bumps, go-github v90 (#44)
- Helm chart `version` / `appVersion` aligned to **1.0.0**

### Removed

- Dependabot config (PR noise outweighed value — bump deps intentionally)
- CodeQL workflow (needs GitHub Advanced Security on private repos)

### Fixed

- Fleet notify webhook `Body.Close` errcheck for CI lint

## [0.1.2] - 2026-09-04

### Changed

- Project renamed to **xdlc** (product / repo / GHCR image). Binary,
  Helm chart, and Deployment remain **`xdlc-agent`**
- Local Kind cluster name, agent namespace, and default domain are
  `xdlc` / `*.xdlc.local` (was `agentic` / `*.agentic.local`)
- Ops console (`ui/`) rebuilt as a stock Vite +
  `@tanstack/react-router` SPA; Lovable scaffolding and unused
  shadcn/radix deps removed
- CI / `make test` skip `ui/node_modules` Go package walk

### Added

- Read-only dashboard API on the daemon: `/api/health`, `/api/overview`,
  `/api/history`, `/api/backlog`, `/api/repos` (`internal/api`)
- Docs: [console.md](docs/console.md); naming table in README /
  CONTRIBUTING
- GitHub App auth preferred over PAT: `GITHUB_APP_ID` +
  `GITHUB_APP_INSTALLATION_ID` + `GITHUB_APP_PRIVATE_KEY` (or `_FILE`);
  `GITHUB_TOKEN` remains the fallback (`internal/ghclient`)
- OTel metrics: `example-service` + `xdlc-agent` export OTLP; Collector
  manifests under `observability/otel/`; PromQL gate stays backend-agnostic
- ArgoCD (`/webhooks/argocd`) and Alertmanager (`/webhooks/alertmanager`)
  webhook handlers; pollers remain edge-triggered fallback
- `server.require_webhook_secret` to fail closed when secrets are unset

### Changed (earlier)

- `gates.prod-health.metrics_url` replaces `prometheus_url` (legacy alias
  still accepted); any PromQL instant-query API works (Prometheus,
  VictoriaMetrics, OpenObserve, Mimir)

### Fixed

- Poller edge-trigger: emit Signal only on Kind change (stops
  promote/fix/revert-every-tick; #19)
- CI write-back: drop `gitops/values/dev` from path filters; `[skip ci]`
  on tag commits; document `GITOPS_TOKEN` for cross-repo (#16)
- Promote copies gated image tag `values/dev` → `values/prod` before FF (#15)
- Prod-health revert targets `main` (ArgoCD prod); aligns `develop` when
  tips matched (#17)

### Added (earlier)

- Prod-health PromQL `{{repo}}` placeholder for per-service SLOs (#10)
- CI Fix evidence includes truncated failed-job logs via GitHub API (#18)
- Coding-agent subprocess is now pluggable: `agent.provider` selects
  `claude` (Claude Code), `codex` (OpenAI Codex CLI), or `cursor`
  (Cursor CLI), each with its own default headless invocation in
  `internal/subagent`; `agent.binary`/`agent.args` override either.
  `config.yaml`'s old `claude:` block is now `agent:`.
- Docker image bundles the Codex CLI alongside Claude Code (Cursor CLI
  isn't npm-installable, left out with a note on how to add it)

### Changed (earlier)

- Docs honesty pass: architecture diagram embedded; webhook/poll and
  promote/revert semantics match what ships; viral README with full doc
  index and two try-paths (Kind vs binary)

## [0.1.1] - 2026-09-04

### Fixed

- GoReleaser Docker release: use `deploy/Dockerfile.release` that copies the
  pre-built binary (GoReleaser context does not include `go.mod`/`go.sum`)
- CI lint: bump `golangci-lint-action` to v7 (required for golangci-lint v2)
- Remove accidentally committed `example-service` binary from git

## [0.1.0] - 2026-09-04

### Added

- `xdlc-agent` orchestrator: one loop, three gates (CI, dev-smoke, prod-health)
- Actions: fix (Claude subagent), revert, fast-forward promote
- GitHub `workflow_run` webhook with HMAC verification and repo name resolution
- Pollers for dev-smoke and prod-health gates
- GitOps templates: ArgoCD app-of-apps, Helm service chart, example-service manifests
- Reference Go service (`services/example-service`) with `/healthz` and `/metrics`
- Local bootstrap: `scripts/setup.sh`, `scripts/bootstrap-local.sh`, Kind config
- Helm chart for agent deployment (`deploy/helm/xdlc-agent`)
- Docker image bundling git, kubectl, argocd, Claude Code CLI
- Audit trail: `BACKLOG.md` + `xdlc-agent history` (bbolt)
- `xdlc-agent validate`: config/gitops cross-check
- CI workflows: test, helm lint, GoReleaser release on tag
- Service CI template (`templates/service-ci.yaml`) and monorepo example workflow
- Documentation: getting-started, runbook, deployment, service-onboarding, local-setup

### Fixed

- Webhook repo identifier mismatch (`org/repo` → config short name)
- Stale local clone after fetch (now hard-resets to `origin/<branch>`)
- Git auth via `http.extraHeader` env (token not persisted in `.git/config`)

### Known limitations

- dev-smoke and prod-health are poll-driven (no ArgoCD/Alertmanager webhooks yet)
- prod-health uses org-wide PromQL, not per-service SLOs
- `claude.mode: sdk` reserved but unimplemented
- AWS/EKS bootstrap not included (local Kind only)

[0.0.1-beta.1]: https://github.com/xdlc-labs/xdlc-agent/releases/tag/v0.0.1-beta.1
[2.0.0]: https://github.com/xdlc-labs/xdlc-agent/releases/tag/v2.0.0
[1.0.0]: https://github.com/xdlc-labs/xdlc-agent/releases/tag/v1.0.0
[0.1.2]: https://github.com/xdlc-labs/xdlc-agent/releases/tag/v0.1.2
[0.1.1]: https://github.com/xdlc-labs/xdlc-agent/releases/tag/v0.1.1
[0.1.0]: https://github.com/xdlc-labs/xdlc-agent/releases/tag/v0.1.0
