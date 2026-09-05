# Console

Embedded SPA on the daemon HTTP port (`/`), API under `/api/*`.

## Pages

| Nav | Purpose |
|-----|---------|
| Overview | Pipeline snapshot, KPIs |
| Gates | CI / DEV / PROD gate cards |
| Repos | Fleet list + `/repos/$id` timeline |
| Activity | Audit trail |
| Actions | Manual Fix / Promote / Revert + Fix-PR queue |
| Settings | Bearer token, optional browser-local agent key |
| Docs | This documentation (same theme) |

## First login

1. [Create](api-tokens.md) and export `XDLC_API_TOKEN`, then start the daemon
2. Open the console URL
3. **Settings** → paste the **same** token → save
4. Overview should leave “degraded / unauthorized” state

## Manual Fix agent override

Settings can store provider + API key in **localStorage** only. Manual Fix sends `X-XDLC-Agent-Provider` / `X-XDLC-Agent-Key`. Daemon default provider still comes from `config.yaml`.

## Live updates

When the daemon is reachable, Activity/overview invalidate over SSE (`/api/events`) instead of relying only on polling.
