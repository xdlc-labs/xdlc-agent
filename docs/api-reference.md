# API reference

Dashboard JSON served by `xdlc-agent daemon` on the same HTTP server as
webhooks (`server.addr`, default `:8080`). Implementation:
`internal/api`. Ops UI notes: [console.md](console.md).

No OpenAPI spec yet — schemas below match what the handlers encode
today.

## Auth

Protected `/api/*` routes (everything below except health) accept **either**:

| Method | How | Access |
|---|---|---|
| Bearer | `Authorization: Bearer <token>` | Operator or viewer token |
| OIDC session | Cookie set after `GET /auth/login` (when `server.oidc.issuer_url` is set) | Role from IdP groups → operator/viewer |

| Token source | Config | Default env | Access |
|---|---|---|---|
| Operator | `server.api_token_env` | `XDL_API_TOKEN` | GET + POST actions |
| Viewer (optional) | `server.api_viewer_token_env` | `XDL_API_VIEWER_TOKEN` | GET only |

Bearer comparison is constant-time. If **neither** an operator bearer token
**nor** OIDC is configured → protected routes **503** (fail closed). If
auth is configured but the request has no valid credential → **401**.
Viewer alone cannot POST. OIDC setup (client/session secrets, groups):
[console.md](console.md), [secrets.md](secrets.md).

## Errors

Bodies are **plain text** today (`http.Error`), not JSON. Status codes:

| Status | When | Typical body |
|---|---|---|
| 401 | Missing/wrong bearer | `unauthorized` |
| 403 | Viewer token on a write route | `forbidden: operator token required` |
| 503 | Operator token unset; or actions enqueue unavailable | `API token not configured` / `actions not available` |
| 400 | Bad action body | `invalid JSON body` / `repo required` / `confirm: true required` |
| 500 | Audit DB / backlog read failure | error string |

Clients should key off status code; do not assume a JSON error envelope.

## Endpoints

### `GET /metrics` — open

Prometheus text exposition. **No auth** (standard scrape practice). Same
port as webhooks/API (`server.addr`). Series from `internal/otel`
(`xdlc_agent_*`). Available when `OTEL_SDK_DISABLED=true` too.

### `GET /api/health` — open

Liveness-ish. No auth.

```json
{
  "status": "running",
  "version": "…",
  "agentProvider": "claude",
  "configPath": "config.yaml",
  "uptime": "1h 02m",
  "addr": ":8080"
}
```

### `GET /api/overview` — operator or viewer

Aggregated console payload: daemon summary, pipeline stages, KPIs,
gates, repos, last ~10 events, full backlog markdown.

```json
{
  "daemon": {
    "status": "running",
    "version": "…",
    "env": "local",
    "uptime": "…",
    "webhook": "receiving → :8080",
    "configPath": "…",
    "gitopsDir": "gitops",
    "agentProvider": "claude"
  },
  "pipeline": [{ "stage": "ci", "label": "CI gate", "status": "idle", "detail": "…" }],
  "kpis": {
    "reposWatched": 1,
    "fixes": 0,
    "promotes": 0,
    "reverts": 0,
    "lastActionAt": "—",
    "backlogOpen": 0
  },
  "gates": [{ "name": "CI", "provider": "GitHub Actions", "status": "idle", "lastCheck": "—", "interval": "—", "trigger": "…", "evidence": "…", "url": "" }],
  "repos": [ /* same objects as GET /api/repos */ ],
  "events": [ /* same shape as history events, max 10 */ ],
  "backlogMd": "# BACKLOG\n…"
}
```

`daemon.env` is currently always `"local"` (placeholder).

### `GET /api/history` — operator or viewer

Audit records from the bbolt store, newest first.

| Query | Default | Notes |
|---|---|---|
| `repo` | (all) | Filter by config short name |
| `limit` | `100` | Positive int; invalid values ignored → default |

```json
{
  "events": [
    {
      "id": "20260904120000-example-service-ci",
      "ts": "2026-09-04 12:00:00Z",
      "repo": "example-service",
      "source": "github-actions",
      "gate": "CI",
      "signal": "fail",
      "action": "Fix",
      "ok": true,
      "evidence": "key=value …",
      "url": "https://…"
    }
  ]
}
```

`ok` is true when the record is a gate pass or an action
(`fix` / `promote` / `revert`) was recorded.

### `GET /api/backlog` — operator or viewer

| Query | Notes |
|---|---|
| `repo` | Keep markdown lines that contain this substring (Backlog lines look like `repo=<name>`) |

```json
{
  "markdown": "# BACKLOG\n\n## Log\n…"
}
```

Missing file → 500 with the OS error string.

### `GET /api/whoami` — operator or viewer

Caller's resolved role (drives console show/hide of write actions).

```json
{ "role": "operator" }
```

`role` is `"operator"` or `"viewer"`. Unauthenticated → **401**.

### `GET /api/prs` — operator or viewer

Fix-PR work queue. Only populated when Fix dispatches ran with
`agent.fix_mode: pr` and recorded `pr_url` evidence. Rows are deduped to
the latest audit record per repo+branch. When GitHub credentials are
wired, each row is live-rechecked (short timeout); on GH error the
snapshot is kept and `stale: true` is set (never 500s the console).

| Query | Default | Notes |
|---|---|---|
| `all` | unset | Omit or anything other than `1` → **open and not merged** only. `?all=1` includes closed/merged history. |

```json
{
  "prs": [
    {
      "repo": "example-service",
      "branch": "xdlc-fix-…",
      "number": 42,
      "url": "https://github.com/org/example-service/pull/42",
      "state": "open",
      "merged": false,
      "stale": false,
      "at": "2026-09-04 12:00:00Z"
    }
  ]
}
```

`stale` is omitted when false. Empty queue → `"prs": []`.

### `GET /api/kpis` — operator or viewer

Cost / outcome aggregates over the audit store. No new
persistence — pure read of `Audit.All()`. `fix_success_rate` is the share
of fixes not followed by a revert before the next fix (same repo /
totals stream). Duration percentiles use `duration_ms` evidence when
present; `fix_success_rate` is `null` when there are zero fixes.

```json
{
  "totals": {
    "repo": "",
    "fixes": 3,
    "reverts": 1,
    "promotes": 1,
    "total_cost_usd": 0.08,
    "fix_success_rate": 0.6667,
    "duration_p50_ms": 1000,
    "duration_p95_ms": 2000
  },
  "repos": [
    {
      "repo": "example-service",
      "fixes": 2,
      "reverts": 1,
      "promotes": 0,
      "total_cost_usd": 0.03,
      "fix_success_rate": 0.5
    }
  ]
}
```

Overview's embedded `kpis` object is a separate, lighter activity
summary (counts only) — do not confuse it with this endpoint.

### `GET /api/repos` — operator or viewer

One object per `config.yaml` repo, enriched with latest audit snapshot
when present.

```json
{
  "repos": [
    {
      "id": "example-service",
      "name": "your-org/example-service",
      "branch": "develop",
      "lastGate": "CI",
      "lastGateStatus": "idle",
      "lastAction": "None",
      "lastActionAt": "—",
      "devTag": "—",
      "prodTag": "—",
      "health": "healthy",
      "cloneStatus": "repos/example-service",
      "lastPromote": "—",
      "lastRevert": "—",
      "argocdApp": "dev-example-service",
      "sloQueries": [
        { "label": "p95", "query": "…" },
        { "label": "error rate", "query": "…" }
      ]
    }
  ]
}
```

`devTag` / `prodTag` / `lastPromote` / `lastRevert` are placeholders
today (`—`).

### `POST /api/actions/{fix|promote|revert}` — operator only

Enqueue a synthetic signal so `orchestrator.Decide` maps to the action
(CI fail → Fix, dev-gate pass → Promote, prod-health breach → Revert).
Does not run the action inline; the orchestrator audits when it
processes.

**Body (JSON):**

```json
{
  "repo": "example-service",
  "confirm": true
}
```

Both fields required. `confirm` must be literal `true`.

**Success:**

```json
{
  "enqueued": true,
  "action": "fix",
  "repo": "example-service",
  "source": "ci",
  "kind": "fail"
}
```

Viewer token → **403**. No enqueue hook wired → **503**
`actions not available`.

## Content type

Success responses: `application/json`, indented, `Cache-Control: no-store`.

## Related

- Auth env + secrets: [secrets.md](secrets.md), [SECURITY.md](../SECURITY.md)
- Compatibility policy: [upgrade.md](upgrade.md#compatibility-semver)
- Upgrade notes when auth was introduced: [upgrade.md](upgrade.md)
