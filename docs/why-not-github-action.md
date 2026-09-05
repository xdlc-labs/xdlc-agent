# Why not a GitHub Action?

A workflow job that runs on `workflow_run` failure is the obvious first sketch. It is the wrong long-term shape for what xdlc-agent does.

## What an Action gets you

- Easy install on one repo
- Ephemeral compute per event
- Native `GITHUB_TOKEN` for that repository

## What breaks for this product

| Need | Why Actions hurt |
|------|------------------|
| **Continuous prod-health** | p95 / error-rate polls every ~30s. Actions are event-driven or cron (min ~5m); not a long-lived PromQL loop with low lag to Revert. |
| **Long-lived git credentials** | Fix/Promote/Revert need App installation tokens (or carefully scoped PATs) across clones under `repos/`. Actions’ job token is short-lived and usually single-repo; refreshing App tokens in every job is awkward and leak-prone in logs. |
| **Cross-repo fleet** | `depends_on`, flap/circuit, patient-zero style policy need shared state (audit DB, backlog). Per-repo workflows do not share a single writer without an external store you still have to run. |
| **DEV smoke + Argo webhooks** | Same process should take GitHub, ArgoCD, and Alertmanager webhooks and feed one `Decide` loop. Wiring three Action entrypoints duplicates policy. |
| **Ops console + audit** | Embedded `/` + `/api/*` and a local bbolt history assume a daemon, not a finished job log. |
| **Subagent runtime** | Coding-agent CLIs want a warm disk clone, `PATH`, and multi-minute timeouts. Re-cloning on every Action is slow and burns minutes before the agent starts. |

## What we do instead

One self-hosted daemon (binary or Helm, **single replica** — audit DB is single-writer):

1. Webhooks + pollers → `orchestrator.Signal`
2. `Decide` → Fix / Promote / Revert / noop (fleet suppressions)
3. Local clones + BYO agent CLI
4. Console + `BACKLOG.md` + history DB

GitHub Actions remain fine for **your** app CI. Point `workflow_run` at the daemon’s `/webhooks/github`; do not replace the daemon with another workflow that tries to be the control plane.

See also: [vs alternatives](vs-alternatives.md), [architecture](architecture.md).
