# Architecture

**xdlc** is the product; **`xdlc`** is the CLI/daemon binary.
Repo, GHCR image, and Helm chart stay **`xdlc-agent`**.

![One loop, three gates, light theme: xdlc-agent → GitHub → DEV → promote → PRODUCTION](assets/architecture.jpg)

## The loop

`xdlc daemon` runs one process: an HTTP server for GitHub
`workflow_run`, ArgoCD notification, and Alertmanager webhooks, plus
dashboard JSON under `/api/*` (reads for the console; operator
Fix/Promote/Revert writes),
plus tickers that poll the dev-smoke and prod-health gates (fallback when
webhooks are quiet), all feeding a single `chan orchestrator.Signal`.
One goroutine (`orchestrator.Run`) reads that channel, calls `Decide`
(pure function, `Signal -> Action`), then applies optional **fleet
policy** that may suppress Fix/Revert/Promote to `noop`
with `escalate=*` evidence, and dispatches the Action:

- `fix`: run a per-repo coding-agent subagent (`internal/subagent`) with
  the failure evidence, expecting it to commit+push a fix or leave a
  note in `BACKLOG.md` if it can't.
- `revert`: `git revert HEAD` on the prod branch (`main` by default) and
  push; the dev branch is realigned only when it still pointed at the
  pre-revert prod tip.
- `promote`: fast-forward `develop` -> `main` (`internal/promote`),
  refused by git itself if not FF-able.

Every dispatch, pass or fail, is appended to `BACKLOG.md`
(human-auditable) and `internal/store` (queryable, `xdlc history`).

### Fleet policy

Declared `repos[].depends_on` plus optional `fleet:` knobs (all off by
default). After `Decide`, suppressions become `noop` with evidence
`escalate=root_cause|circuit|flap|deps_unhealthy|deps_pin|structural` — still
written to backlog/audit; metric `xdlc_agent_fleet_suppressions_total`.
`structural` is evidence-heuristic (C4), not fleet topology; operator
manual Fix bypasses it.
Optional `fleet.notify_webhook_url` POSTs Slack-compatible JSON.
Cross-repo SemVer/tag promote pins: `repos[].promote_requires`.

## Vs the diagram

The hero diagram is the target shape. What ships today:

| Diagram claim | Today |
|---|---|
| One loop, 3 gates, per-repo subagents, `BACKLOG.md` | Wired |
| Push / revert / FF promote | Wired |
| GitOps: DEV tracks `develop`, PROD tracks `main` | Wired |
| Smoke/e2e before promote | Wired (poller) |
| Prod p95 / error-rate → revert | Wired (poller; `{{repo}}` in PromQL for per-service) |
| "Red build logs" into the agent | Wired on Fix — truncated failed-job logs via GitHub API |
| Green CI → DEV sync | Side-effect: CI tag write-back + ArgoCD auto-sync; agent does not sync |
| Rebase / linear history | Policy only; not enforced by the agent |
| ArgoCD / Alertmanager webhooks | Wired (`/webhooks/argocd`, `/webhooks/alertmanager`); pollers remain fallback |
| Instant prod rollback | Wired: revert on `main`, align `develop` when tips matched |
| Promote carries the gated image tag | Wired: `CarryProdTag` then FF `develop→main` |
| OTel → PromQL store | Wired: apps export OTLP; gate queries `metrics_url` |
| GitHub App auth | Wired: App preferred, `GITHUB_TOKEN` fallback |
| `agent.mode: sdk` | Reserved, unimplemented |
| Ops console (`ui/`) over `/api/*` | Wired: reads plus operator Fix/Promote/Revert writes (`POST /api/actions/*`). No mocks — when the daemon is unreachable it renders an empty shell behind a `degraded` banner |

## Coding-agent providers: subprocess vs SDK

The subagent that actually edits and pushes code isn't tied to one
vendor. `agent.provider` in `config.yaml` picks which headless CLI
`internal/subagent.SubprocessRunner` shells out to:

| Provider | CLI | Default headless invocation |
|---|---|---|
| `claude` (default) | Claude Code | `claude -p --output-format json` (prompt on **stdin**) |
| `codex` | OpenAI Codex CLI | `codex exec` (prompt on **stdin**) |
| `cursor` | Cursor CLI (`cursor-agent`) | `cursor-agent -p` (prompt on **stdin**) |

Prompt text is never placed on argv (so it cannot leak via `/proc/*/cmdline`). `agent.args` may still include the literal `{{prompt}}` marker — it is stripped at run time; the prompt is written to the subprocess stdin. On timeout the runner kills the whole process group (`Setpgid` + `Cancel`) so nested `node`/`git` children do not orphan.

Each of these reuses the CLI's own built-in file-edit/bash/git tool use
instead of reimplementing it. Tradeoff: less control over intermediate
steps than a hand-rolled tool loop, and you're parsing CLI output rather
than a typed API response. `agent.binary` overrides the default binary
name (point it at a wrapper script if you need custom flags); `agent.args`
overrides the argv shape (optional `{{prompt}}` marker). Adding a new provider is a `providerDefaults` entry in
`internal/subagent/runner.go`, see [CONTRIBUTING.md](CONTRIBUTING.md).

A `mode: sdk` alternative (`internal/subagent`, unimplemented) would talk
to a provider's API directly with a hand-rolled tool loop: more control,
more code to maintain, and provider-specific rather than one shape that
fits any headless CLI. Switch is `agent.mode` in `config.yaml`.

## Why fast-forward-only promotion

The artifact gated on `develop` (image digest, test results) is meant to
be the exact artifact that reaches prod, no rebuild between DEV and
PROD. That is why promote is `git push origin develop:main`, not a merge
commit: git refuses the push if `main` has diverged, so a promote can
never silently rewrite production history.

Promote first copies `image.tag` from `values/dev/<service>.yaml` into
`values/prod/<service>.yaml` when those files exist in the clone, then
fast-forwards.

## Gates are pluggable

See `internal/gate.Gate` and the built-ins under `internal/gate/`.

## What the Fix agent is given, and what it leaves behind

Two files sit either side of a Fix run. `internal/subagent.ReadTeamInstructions`
collects the *trusted* half of the prompt — `AGENTS.md`, `CLAUDE.md`,
`.xdlc/rules.md`, `.xdlc/skills/*.md`, the daemon-wide `agent.rules_file`,
`repos[].agent_instructions`, and an operator's per-run note — each capped at
8 KB so no single file crowds the others out, all of it placed above the
untrusted evidence block ([Rules and skills](rules-and-skills.md)).

`internal/session` writes the other side: one directory per Fix holding the
exact prompt, the agent's stdout, the diff between the pre-Fix commit and
whatever the agent committed, and a `meta.json` carrying status, provider and
cost. The audit store answers *whether* a Fix ran; this answers *what it did*
([Fix sessions](sessions.md)). Recording is best-effort — a recorder failure
logs a warning and never fails a Fix — and the session id is written into the
audit record so the console's Activity row and the directory line up.

## Local repo clones

`internal/repos.Manager` keeps one working clone per repo on disk
(`repos/<name>` by default, `Repo.Dir` to override). `EnsureCloned` runs
before every `Fix`/`Revert`/`Promote` (and at daemon start, in parallel
with a small semaphore): clone if missing (`--depth 1 --single-branch`),
otherwise skip the network when `HEAD` already matches `origin/<branch>`
and the tree is clean; if dirty or diverged, `fetch` + `checkout` +
`reset --hard origin/<branch>`. That reset is deliberate when needed:
without it a stale local clone would drift from origin. Anything in these
directories is agent-owned and disposable; don't hand-edit them.

## Git authentication

Prefer a GitHub App installation token (`internal/ghclient.PreferAppThenPAT`).
`internal/repos.AuthEnv` turns that token (or a PAT) into git config
passed via `GIT_CONFIG_*` environment variables on the git subprocess
(an HTTP `Authorization` header, via `http.extraHeader`), not the more
common `https://token@github.com/...` URL embedding, which persists into
`.git/config` on disk and gets echoed back verbatim in git's own error
messages. `promote.FastForward` and `dispatch.Dispatcher.Revert` take
the same env and apply it to their own git calls; see [SECURITY.md](SECURITY.md) for
what this does and doesn't protect against.

## Container image

`deploy/Dockerfile` bundles `git`, `kubectl`, `argocd`, and the
Claude Code and Codex CLIs on top of the `xdlc` binary (Cursor
CLI isn't npm-installable, see the Dockerfile's comment for how to add
it). Every one of those is a real `exec.Command` call somewhere in this
codebase (dispatch, repos, gitops, k8sprobe, subagent), so a
scratch/distroless image with just the Go binary cannot actually run a
`daemon`. Versions are pinned via build args, not resolved at build time.

The ops console (`ui/`) is a separate Vite app, but it *is* part of this
image: the Dockerfile's `ui` stage runs `bun run build` and copies
`ui/dist` into `internal/console/dist`, which `internal/console/embed.go`
serves via `go:embed` off the same port as `/api/*`. The release workflow
builds it the same way. A `go build` without that copy leaves `dist/`
empty and the daemon simply stays API-only.
