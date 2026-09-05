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
3. **Settings** → paste the **same** token → save (Save checks `/api/whoami`; the top bar shows **token rejected** on 401, not a green online chip)
4. Overview should leave “degraded / unauthorized” state

## Manual Fix instructions

The Manual Fix dialog takes an optional free-text note — what you would tell a coding agent yourself ("the flake is in the seed data"). It joins the prompt's trusted block after the repo rules. Only its length reaches the audit trail; the text itself is in the session's `prompt.txt`. See [Rules and skills](rules-and-skills.md).

## Manual Fix agent override

Settings can store provider + API key in **localStorage** only. Manual Fix sends `X-XDLC-Agent-Provider` / `X-XDLC-Agent-Key`. Daemon default provider still comes from `config.yaml`. Webhook-driven Fixes ignore the browser override. Use `agent.timeout: 10m` (see `config.example.yaml`) for a real Cursor/Claude/Codex run.

## Live updates

When the daemon is reachable, Activity/overview invalidate over SSE (`/api/events`) instead of relying only on polling.
