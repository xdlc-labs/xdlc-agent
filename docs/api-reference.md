# API reference

Source of truth: [`openapi/openapi.yaml`](../openapi/openapi.yaml).
Auth: `Authorization: Bearer <token>` (except `GET /api/health`). SSE may use `?access_token=`.

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/health` | Liveness / version (no auth) |
| GET | `/api/whoami` | Caller role (`operator` \| `viewer`) |
| GET | `/api/overview` | Console home snapshot |
| GET | `/api/history` | Audit event history (`?limit`, `?repo`) |
| GET | `/api/backlog` | `BACKLOG.md` (`?repo` filter) |
| GET | `/api/repos` | Watched repos |
| GET | `/api/repos/{id}` | One repo + timeline |
| GET | `/api/events` | SSE audit fan-out |
| GET | `/api/prs` | Fix-PR work queue (`?all=1`) |
| GET | `/api/kpis` | Fix cost / outcome KPIs |
| POST | `/api/actions/fix` | Enqueue manual Fix (operator) |
| POST | `/api/actions/promote` | Enqueue manual Promote (operator) |
| POST | `/api/actions/revert` | Enqueue manual Revert (operator) |

Action body: `{ "repo": "<name>", "confirm": true }`.
