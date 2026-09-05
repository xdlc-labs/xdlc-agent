# Runbook

What `xdlc-agent` does when each gate fires, and how to audit it.

## Policy summary

| Signal source | Kind | Action |
|---|---|---|
| CI | fail | **fix**: coding-agent subagent with failure evidence |
| CI | pass | noop |
| dev-smoke | fail | **fix** |
| dev-smoke | pass | **promote**: FF `develop` → `main` |
| prod-health | breach | **revert**: `git revert HEAD` on **main** + push |
| prod-health | ok | noop |

Policy is a pure function in `internal/orchestrator/decide.go`, forks can swap it.

## Fix action

1. `EnsureCloned`: fresh checkout of `origin/<branch>` (hard reset)
2. The coding-agent subprocess (Claude Code / Codex / Cursor, per
   `agent.provider`) runs in the repo dir with evidence (CI: conclusion +
   run URL plus a truncated failed-job log excerpt pulled from the GitHub
   API via `ghclient.FetchFailedJobLogs`; smoke: probe job logs; prod:
   metric values)
3. Subagent expected to commit+push a fix, or append to `BACKLOG.md` if blocked

If fix fails repeatedly, inspect:

```sh
kubectl logs -n xdlc deploy/xdlc-agent
./bin/xdlc-agent history
cat BACKLOG.md
```

## Promote action

1. Copy gated `image.tag` from `gitops/values/dev/<repo>.yaml` →
   `values/prod/<repo>.yaml` when those files exist in the clone
2. `git push origin develop:main` — git refuses if not fast-forwardable

ArgoCD sync to prod is a side-effect of the ref moving (auto-sync / next
reconcile), not an explicit helm sync in the agent.

Manual promote:

```sh
./bin/xdlc-agent promote repos/example-service --config config.yaml
```

## Revert action

Rollback-first on prod breach:

1. Fetch `origin/main` + `origin/develop`
2. `git revert --no-edit HEAD` on **main**, push
3. If `develop` still pointed at the pre-revert main tip (normal right
   after promote), push `main:develop` so both stay aligned

ArgoCD prod tracks `main`, so the cluster rolls back on next sync.

## Audit trail

Two stores, same events (plus a read-only HTTP view):

| Store | Command / path | Audience |
|---|---|---|
| Human | `BACKLOG.md` | PR reviewers, on-call |
| Queryable | `xdlc-agent history` | automation, debugging |
| Dashboard | `GET /api/history`, `/api/overview` | ops console (`ui/`) |

Every signal + chosen action is appended, pass or fail. Console setup:
[console.md](console.md). When fleet policy suppresses an action, the
audited action is `noop` and evidence includes `escalate=root_cause`,
`escalate=circuit`, `escalate=flap`, or `escalate=deps_unhealthy` (see
table below). Optional `fleet.notify_webhook_url` POSTs a Slack-compatible
`{"text":…}` body on each suppress.

## BACKLOG.md format

Each entry includes timestamp, repo, gate source, action taken, and evidence snippet. Subagents may also append notes when they cannot auto-fix.

## When to intervene manually

| Situation | Action |
|---|---|
| Agent fix loop spinning | Check subagent logs; fix root cause; reset stale clone in PVC |
| `escalate=deps_pin` in backlog | Promote blocked — dependency prod `image.tag` below `promote_requires.min_tag` |
| `escalate=deps_unhealthy` in backlog | Promote blocked — a `depends_on` upstream is still prod-health breaching |
| `escalate=flap` in backlog | Automation paused Fix/Revert after alternating cycles — fix root cause, wait out `fleet.flap_window`, or raise `flap_max_cycles` |
| `escalate=circuit` in backlog | Too many repos prod-health breaching at once — stop automated thrash; restore shared deps / infra; lower load before re-enabling |
| `escalate=root_cause` in backlog | Downstream Revert suppressed because an upstream `depends_on` is still breaching — fix upstream first |
| Promote refused (non-FF) | Someone committed to `main`, rebase or reset `main` to match policy |
| False prod-health breach | Tune thresholds in `config.yaml` + `observability/prometheus/rules/prod-health.yaml` |
| Unknown repo in webhook | Add repo to `config.yaml`, run `validate` |
| Swap metrics backend | Point `gates.prod-health.metrics_url` at VictoriaMetrics/OpenObserve/Mimir; update Collector exporter in `observability/otel/collector.yaml` |
| Webhooks rejected (401) | Set webhook secret envs, or set `server.require_webhook_secret: false` for local only |

## Gate check CLI (CI integration)

Run gates in GitHub Actions without the daemon:

```sh
xdlc-agent gate check ci --config config.yaml       # exit 1 on fail
xdlc-agent gate check dev-smoke --config config.yaml
xdlc-agent gate check prod-health --config config.yaml
```

## Related

- [Gates](gates.md): implementation details
- [Architecture](architecture.md): loop design
- [Console](console.md): ops UI + `/api/*`
- [SECURITY.md](../SECURITY.md): token scoping
