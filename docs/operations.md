# Operations

Day-2 for a running daemon.

## Health

- Console top bar: daemon online / degraded / stopped
- `GET /api/health` — unauthenticated liveness
- `GET /metrics` — Prometheus metrics (Fix queue depths, fleet suppressions, …)

## Auth

- Operator bearer: env **`XDLC_API_TOKEN`** — [how to create it](api-tokens.md)
- Optional viewer: **`XDLC_API_VIEWER_TOKEN`**
- Optional OIDC SSO — see `server.oidc` in `config.example.yaml` and [Security](SECURITY.md)

Paste the operator token in console **Settings** (localStorage).

## Audit

- `BACKLOG.md` in the daemon working directory — human trail
- `xdlc history` — structured store (`xdlc-agent-history.db`)
- Console **Activity** — same stream; live SSE on `/api/events` when connected

## Manual actions

**Actions** tab: Fix / Promote / Revert for a config repo **id** (not the GitHub slug). Manual Fix can override agent provider/key from Settings.

## Doctor in CI

```sh
./bin/xdlc doctor --config config.yaml --skip-network
./bin/xdlc validate --config config.yaml
```

## Upgrades

1. Read [CHANGELOG](CHANGELOG.md)
2. Bump image/Helm `appVersion`
3. Re-run doctor; confirm webhook secrets still set
4. Watch Activity for a known signal after cutover

## Capacity

Fix concurrency and budgets: `agent.max_concurrent_fixes`, `agent.fix_budget`. Webhook rate limits: `server.webhook_rate_per_sec` / `webhook_rate_burst`. Fleet circuit breakers: `fleet.*`.
