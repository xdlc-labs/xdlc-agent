# Changelog

All notable changes to this project will be documented in this file.

Format based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Fixed

- **The coding agent could not commit in the published container**, so a containerized Fix delivered nothing and was still recorded as clean. With `agent.worktree` on (the default) the agent's own `git commit` is the entire delivery mechanism, but the image has no git identity: as uid 65532 with no `.gitconfig`, `git commit` fails with `Author identity unknown`. The daemon now supplies `GIT_AUTHOR_*` / `GIT_COMMITTER_*` to the agent subprocess and its own git calls, configurable as `agent.committer.name` / `.email` and defaulting to `xdlc-agent <xdlc-agent@users.noreply.github.com>`. An identity already present in the environment or in git's own config still wins, so a bot identity is never overridden. Both images also carry a system `/etc/gitconfig` default. `xdlc demo` had been hiding this by setting a repo-local identity for its own throwaway repos
- **A branch mismatch dropped every webhook delivery silently.** `repos[].branch` defaults to `develop` and `xdlc init` wrote no `branch:` key, so pointing xdlc at a `main`-trunk repository produced: GitHub reporting each delivery as accepted, no log line, no signal, and no way to tell it apart from "CI has not run yet". The mismatch is now logged at warn with both branches, `xdlc init` scaffolds an explicit `branch:` (`--scan` detects the checkout's real default), and `config.example.yaml` says it must match the branch CI runs on and is never guessed. The default value is unchanged
- **A Fix that succeeded could be recorded as failed.** In worktree mode the prompt tells the agent not to push, while the verdict contract said to report `fixed` "only if you committed and pushed". An agent that followed both landed on `needs_human`, which is non-retryable, so the Fix failed with `escalate=agent_needs_human` even though the commit, push and PR had all worked. The verdict criterion now matches the hand-back each mode actually asks for, and a test asserts the two can never drift apart again
- **The documented first run could not start.** `xdlc init` scaffolded `server.addr: ":8080"` with `require_webhook_secret: false`, `xdlc doctor` reported "all checks passed", and then `xdlc daemon` refused to boot: `require_webhook_secret must be true when listening on ":8080" (non-loopback)`. All six `init` scaffolds and `config.example.yaml` now bind `127.0.0.1:8080`, which is what the README already tells you to open, and the comment spells out what to change before binding a non-loopback address
- `xdlc doctor` gained a **webhook secret for listen addr** check so doctor and the daemon can no longer disagree about whether a config will start. It calls the daemon's own predicate rather than restating the rule
- A flaky `internal/session` test. `Start` fires a rate-limited background prune that is ungated on a fresh store, so it raced `TestPruneDropsOldSessions` for the stale directory and the explicit `Prune()` then had nothing to remove (`want 1 pruned, got 0`, roughly once per 30 package runs). Test-only: the daemon tolerates a concurrent prune. The test now claims the rate limit so it owns the only prune
- The documented curl install is piped into **bash**, not `sh`. `scripts/install.sh` uses `set -o pipefail`, which dash rejects outright, so `curl … | sh` died with `set: Illegal option -o pipefail` before doing anything on Debian and Ubuntu, where `/bin/sh` is dash. The script itself was fine; only the documented invocation was wrong
- Console **Settings** no longer reads as one coding-agent control and a broken echo of it. The browser-local Manual Fix override and the daemon's `agent.provider` default are now two labelled, visually distinct cards, the daemon one explicitly read-only with the "edit config.yaml and restart" instruction. When the two differ, a calm note says which one wins for a Manual Fix from this browser instead of leaving what looked like a failed save (#29)

### Added

- `agent.committer.name` / `agent.committer.email` — the git identity Fix commits are attributed to
- A schema-coverage test asserting every yaml key the daemon accepts appears in `schema/config.schema.json`. The existing drift test compared only top-level keys, so it could not see a nested one: `agent.fix_budget` was documented in `config.example.yaml` and read by the daemon while the schema's `additionalProperties: false` rejected it, meaning an editor flagged a config the daemon loads happily. `fix_budget` is now in the schema
- `scripts/check-version-refs.sh`, run in CI: every copy-pasteable release reference in the README and the hosted install / deployment / getting-started guides must match the Helm chart's `appVersion`. The install snippets had drifted to `0.0.1-beta.1` and `0.0.1-beta.2` while the README was on `0.0.1-beta.4`, so a new user's first `docker run` pulled a stale image
- Airlock runs on pull requests (`.github/workflows/airlock.yml`)
- [ROADMAP.md](ROADMAP.md) — what is planned next, and what is deliberately out of scope; linked from the README
- [RELEASING.md](RELEASING.md) — the release checklist, including the manual GHCR package-visibility step that CI cannot do and the from-source image build's disk and podman requirements

### Changed

- Install and deployment guides pin the current release (`0.0.1-beta.4`) consistently
- The container and Kubernetes paths document that they need `server.require_webhook_secret: true` plus a `GITHUB_WEBHOOK_SECRET`, since a container never binds loopback. The Helm chart already set it
- The README no longer explains a GHCR `unauthorized` as a missing tag; the tags exist, the package is not public
- `deploy/Dockerfile.release` no longer points at `scripts/bootstrap-local.sh`, which does not exist

## [0.0.1-beta.4] - 2026-09-07

### Fixed

- Per-Fix worktree paths are absolute, and a checkout that would land inside the clone is refused. `git worktree add` of a relative `repos/.worktrees/...` path was resolved against the clone, so the agent `chdir` missed it (`fork/exec ... no such file or directory`).
- One Fix per repo+SHA after a successful run. A flake-ladder `workflow_run` for the same commit no longer opens a second PR. A failed Fix can still retry. Manual Fix (empty SHA) is unchanged.

### Added

- Optional `gates.ci.workflows` allowlist (name, path, or basename such as `ci`). Empty keeps today's behavior: every `workflow_run` on the tracked branch is CI, including Deploy.

## [0.0.1-beta.3] - 2026-09-07

### Fixed

- OTLP metrics export only when `OTEL_EXPORTER_OTLP_ENDPOINT` is set (no in-cluster collector default; local daemon no longer logs upload failures every 15s). `GET /metrics` is unchanged.
- Console status chip is **connecting** until overview 200, **token rejected** on 401 (was green “online” over a skeleton). Settings Save verifies `GET /api/whoami`.
- Manual Fix / Promote / Revert audit **source** is `daemon`, not `github-actions` / Argo / Prometheus. Decide() mapping is unchanged.
- `xdlc doctor --skip-network` warns (does not fail) when every repo has a local `dir:` and GitHub auth is unset.
- A Fix's recorded `total_cost_usd` / token counts are now the sum of every agent run it took, not just the last one. Previously an `agent.fix_plan` Fix reported only its patch pass, hiding the diagnose pass it also paid for.
- Evidence values containing spaces (an agent summary, a fleet escalation reason) are quoted in `BACKLOG.md` and the console Activity row, so a free-text value no longer reads as the start of the next `key=`. Both now use one formatter.
- `EnsureCloned` skip-fetch compares HEAD to `git ls-remote`, not the local `origin/<branch>` tracking ref. A clone that had not fetched since the failing push used to skip onto the previous (green) commit, so Fix never saw the break.
- Cursor CLI defaults include `--force` (with `--trust`). `--trust` only skips the workspace prompt. Without `--force`, a headless Fix gets `userRejected` on unallowlisted shell commands and never opens a PR.

### Changed

- Default install is **CI Fix** only (`gates: [ci]`). `xdlc init`, `config.example.yaml`, and Helm values no longer enable `dev-smoke` / `prod-health`. Opt in with `xdlc init --profile gitops` or `--profile full`. Helm `role.create` defaults to `false` (enable for GitOps smoke Jobs).
- CLI binary renamed to **`xdlc`** (was `xdlc-agent`). Repo, module, GHCR image, and Helm chart stay **`xdlc-agent`**.
- API bearer env is fixed **`XDLC_API_TOKEN`** (was `XDL_API_TOKEN` + configurable `server.api_token_env`). Optional viewer: **`XDLC_API_VIEWER_TOKEN`**.

### Added

- **Worktree per Fix** (`agent.worktree`, on by default): every Fix runs in its own `git worktree` on an `xdlc/<session id>` branch instead of the repo's shared clone. Two Fixes for one repo now run concurrently (the per-repo cap is gone; `max_concurrent_fixes` still applies), and a Fix killed mid-edit no longer leaves the shared clone dirty. The agent commits and xdlc pushes, so writing to a shared branch is no longer something the coding agent does; the push is non-force and fails loudly if the branch moved. Failed runs keep their worktree for `agent.worktree.keep_failed` (24h). Shared-clone git commands (`EnsureCloned`, worktree add/remove) are serialized per repo so overlapping Fixes cannot collide on git's index and ref locks, and a worktree belonging to a running Fix is never swept. Set `agent.worktree.enabled: false` for the old behavior ([Fix modes](https://xdlc.dev/agent/docs/fix-modes))
- Fix **verdict**: each Fix prompt asks the coding agent to close with one JSON line (`xdlc_outcome`: `fixed` / `gave_up` / `needs_human`, plus a one-line summary). A `gave_up` / `needs_human` run now fails the Fix with `escalate=agent_gave_up` / `agent_needs_human` instead of being recorded as clean because the CLI exited 0. The summary reaches the Activity row (`agent_outcome`, `agent_summary`), `meta.json` and `LESSONS.md`. An agent that prints no verdict behaves exactly as before ([Fix modes](https://xdlc.dev/agent/docs/fix-modes))
- `agent.fix_attempts` (default 1): when `fix_reverify` is on and the gate is still red, re-run the agent with what the last attempt reported doing, why the re-check failed, and freshly fetched logs from the run that is failing now. Stops early on a `gave_up` / `needs_human` verdict. Per-attempt session artifacts (`prompt-2.txt`, `output-2.txt`), `xdlc sessions show --attempt N`, and `xdlc_agent_fix_retries_total` ([Fix modes](https://xdlc.dev/agent/docs/fix-modes))
- Fix **sessions**: every Fix records prompt, agent output and diff under `sessions/`; `xdlc sessions ls|show|prune`; `session_id` in audit + `BACKLOG.md`; `agent.sessions.*` config ([Fix sessions](https://xdlc.dev/agent/docs/sessions))
- Rule sources widened: `CLAUDE.md`, `.xdlc/rules.md` and daemon-wide `agent.rules_file` join `AGENTS.md` / `.xdlc/skills/*.md`; per-file 8 KB cap instead of one tail chop; duplicates dropped; `xdlc doctor` lists what each repo contributes ([Rules and skills](https://xdlc.dev/agent/docs/rules-and-skills))
- Operator instructions on Manual Fix: optional free text in the console dialog and `POST /api/actions/fix` (`instructions`, ≤ 4096 bytes); trusted block placement, length-only in audit
- `gemini` coding-agent provider (`gemini` CLI, `GEMINI_API_KEY`); opt-in in the container image via `--build-arg GEMINI_CLI_VERSION`
- `xdlc init --scan <dir>` — seed `repos:` from local Git checkouts with a GitHub origin
- `xdlc init --profile ci|gitops|full` — CI Fix is the default scaffold; GitOps / paved road are opt-in
- `scripts/install.sh` — curl-install `xdlc` from GitHub Releases (checksum verified); [Install](https://xdlc.dev/agent/docs)
- `scripts/e2e-local.sh` — loopback CI → Fix → Argo Promote → Alertmanager Revert (+ unknown-app 204). Token `dev-token`, `agent.timeout: 10m`, `OTEL_SDK_DISABLED=true`. Minikube/ArgoCD is a precondition, not installed.
- `xdlc demo` — zero-infra Fix→Promote→Revert with `--provider fake` (#5)
- Console Settings: browser-local coding-agent provider + API key (localStorage); Manual Fix sends `X-XDLC-Agent-*` headers (never audit/disk)
- Typed contracts: `openapi/openapi.yaml` + `schema/config.schema.json`; drift tests; `docs/api-reference.md` (#15)
- `xdlc doctor` — PATH / token / config / optional Prometheus checks (#12)
- Docs: [vs alternatives](https://xdlc.dev/agent/docs/vs-alternatives), [why not a GitHub Action](https://xdlc.dev/agent/docs/why-not-github-action) (#13)
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
- Store `Since(repo,t)` via `by_repo` index; flap path no longer scans All (#16)
- Console `/repos/$id` timeline + real dev/prod tags / last promote/revert (#8)
- `GET /api/events` SSE fan-out on audit Append; console live invalidate (#6)
- Fix queue: latest-wins coalesce per source, 1 Fix/repo + global cap, optional `fix_budget`, `xdlc_agent_fix_queue_*` metrics + overview `fix_queue_depth` (#9)

### Changed

- Subagent: prompt on stdin (not argv); kill process group on timeout; external gates get env allowlist + same kill hygiene (#11)
- `EnsureCloned`: skip fetch when HEAD matches the remote tip (`ls-remote`) and the tree is clean; shallow first clone; parallel startup pre-clone (#17)
- Cursor CLI defaults: `-p --trust --force` for headless Fix (workspace trust plus auto-approved tools)
- Actions: Manual Fix/Promote/Revert post config repo `id` (not GitHub slug)
- Console `/repos/$id`: parent layout uses `<Outlet />` so timeline renders (was stuck on list)

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
  on tag push; verify notes in [docs/deployment.md](https://xdlc.dev/agent/docs/deployment)
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
- Docs: [console.md](https://xdlc.dev/agent/docs/console); naming table in README /
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

[0.0.1-beta.4]: https://github.com/xdlc-labs/xdlc-agent/releases/tag/v0.0.1-beta.4
[0.0.1-beta.3]: https://github.com/xdlc-labs/xdlc-agent/releases/tag/v0.0.1-beta.3
[0.0.1-beta.1]: https://github.com/xdlc-labs/xdlc-agent/releases/tag/v0.0.1-beta.1
[2.0.0]: https://github.com/xdlc-labs/xdlc-agent/releases/tag/v2.0.0
[1.0.0]: https://github.com/xdlc-labs/xdlc-agent/releases/tag/v1.0.0
[0.1.2]: https://github.com/xdlc-labs/xdlc-agent/releases/tag/v0.1.2
[0.1.1]: https://github.com/xdlc-labs/xdlc-agent/releases/tag/v0.1.1
[0.1.0]: https://github.com/xdlc-labs/xdlc-agent/releases/tag/v0.1.0
