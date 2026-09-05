# Ops console

Dashboard for a running `xdlc-agent daemon`. Source in `ui/`; production
build embeds into the agent binary (`internal/console/dist`) and is
served at `/` on the same HTTP server as webhooks + `/api/*`.

Product name is **xdlc**; the process is still **`xdlc-agent`**.

## Auth

Two methods, additive — either one authenticates a request, neither
disables the other:

| Role | Bearer env (default) | OIDC (`server.oidc`) | Can |
|---|---|---|---|
| operator | `XDL_API_TOKEN` (`server.api_token_env`) | in `operator_groups` | GET + POST actions |
| viewer | `XDL_API_VIEWER_TOKEN` (`server.api_viewer_token_env`, optional) | authenticated, not in `operator_groups` (or in `viewer_groups` if that's set) | GET only |
| (none) | operator token unset **and** OIDC unset | not authenticated / no matching group | protected routes → 503 if neither method is configured at all, else 401; `/api/health` stays open |

Send `Authorization: Bearer <token>` on all `/api/*` except health, or
sign in via SSO at `GET /auth/login` (only mounted when `server.oidc.issuer_url`
is set) — the daemon issues a session cookie on success. `GET /auth/config`
reports whether OIDC is enabled so the console can show a login link
without hardcoding it. See `config.example.yaml`'s `server.oidc` block
and [secrets.md](secrets.md) for the client secret / session secret env
vars. A misconfigured or unreachable `issuer_url` fails the daemon
closed at startup (discovery runs once, eagerly) rather than silently
running without SSO.

The session cookie is a stateless, self-signed HMAC value, so there is
no server-side session to revoke. `server.oidc.session_ttl` (Go duration,
`0`/omitted → `8h`) is how long an issued cookie stays valid — shorten it
if you need a tighter revocation window; the only other lever is rotating
`OIDC_SESSION_SECRET`, which invalidates every active session at once.
`server.oidc.cookie_secure` defaults to `true`; set it `false` only for
local `http://` testing.

## Run locally

```sh
# terminal 1 — daemon (needs config.yaml + XDL_API_TOKEN)
export XDL_API_TOKEN=dev-op
go run ./cmd/xdlc-agent daemon --config config.yaml

# terminal 2 — UI (proxies /api → :8080)
cd ui
bun install
bun run dev
```

Open http://127.0.0.1:5173. Vite proxies `/api` to `http://127.0.0.1:8080`
(see `ui/vite.config.ts`).

Against a cluster agent:

```sh
kubectl port-forward -n xdlc svc/xdlc-agent 8080:8080
cd ui && bun run dev
```

There are no fixtures or mock data in the UI — `ui/src/lib/api.ts`
is the real `/api/*` client. With no daemon
reachable it returns an empty shell marked degraded
(`daemon.status=stopped`, `webhook=backend unreachable`), and
`components/degraded-banner.tsx` shows a `degraded` banner naming the
cause (401 → add a bearer token under Settings, 503 → the daemon has no
`XDL_API_TOKEN`, otherwise backend unreachable). You can browse the
layout, but every panel is empty by design so an outage never looks like
an empty config.

## API

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET` | `/api/health` | open | daemon uptime, version, provider |
| `GET` | `/api/whoami` | op/viewer | caller's own role — drives the console's show/hide of write actions |
| `GET` | `/api/overview` | op/viewer | repos + recent events + backlog snippet |
| `GET` | `/api/history?limit=N&repo=` | op/viewer | audit records (bbolt) |
| `GET` | `/api/backlog?repo=` | op/viewer | `BACKLOG.md` as markdown |
| `GET` | `/api/repos` | op/viewer | configured repos |
| `GET` | `/api/prs` | op/viewer | Fix-PR work queue — open PRs by default; `?all=1` includes closed/merged; live GitHub recheck when credentials are configured |
| `GET` | `/api/kpis` | op/viewer | Fix cost / success-rate / duration aggregates from the audit store |
| `POST` | `/api/actions/fix` | operator | enqueue Fix (`{"repo":"...","confirm":true}`) |
| `POST` | `/api/actions/promote` | operator | enqueue Promote |
| `POST` | `/api/actions/revert` | operator | enqueue Revert |

Manual actions inject synthetic signals so `Decide()` maps to the
desired action (CI fail→Fix, dev-gate pass→Promote, prod-health
breach→Revert). Orchestrator audits when it processes.

## Fix-PR work queue

Only relevant once `agent.fix_mode: pr` is set (default is `direct` —
see [architecture.md](architecture.md)). Each Fix dispatch in `pr` mode
pushes to an xdlc-generated branch (`xdlc-fix-<timestamp>`, not chosen
by the subagent) so the daemon can look the resulting PR up afterward
via the GitHub API and record `pr_number`/`pr_url`/`pr_state`/`pr_branch`
into that dispatch's audit evidence — the same evidence map that lands
in `BACKLOG.md` and `xdlc-agent history`, no new storage mechanism.
`GET /api/prs` reads that evidence back, deduped to the latest record
per repo+branch, then **re-checks each PR against GitHub** when the
daemon has GitHub credentials (maps short `repos[].name` →
`repos[].github`). Default response is open, non-merged only; pass
`?all=1` for closed/merged history. If GitHub is unreachable the
snapshot state is kept and the row is marked `stale: true` — the
console still loads. No PR found at Fix time (subagent left a
`BACKLOG.md` note instead) records nothing here.

## Pages

| Route | Shows |
|---|---|
| `/` | loop diagram + recent activity |
| `/activity` | history + backlog |
| `/gates` | gate status from overview |
| `/repos` | configured repos |
| `/actions` | Fix / Promote / Revert |
| `/settings` | daemon / config summary |

## Build / embed

`ui/` is a plain client-side SPA (Vite + `@tanstack/react-router`'s
client router, via `@tanstack/router-plugin/vite` for file-based route
generation) — no SSR, no Node server bundle. `vite build` produces a
standard `dist/` with `index.html` plus hashed JS/CSS assets, which is
exactly what `embed.go`'s `go:embed` + static file server expects.

```sh
cd ui && bun install && bun run build
# static assets → ui/dist → copy into internal/console/dist/
cp -r dist/. ../internal/console/dist/
go build ./cmd/xdlc-agent
```

`deploy/Dockerfile` runs the bun build and copies `ui/dist` into the
embed path before `go build`. If `dist/index.html` is missing, the
daemon skips mounting `/` and stays API-only.

Released GHCR images (built by `.goreleaser.yml` via
`deploy/Dockerfile.release`) include the console too:
`.github/workflows/release.yml` builds `ui/` and copies `ui/dist` into
`internal/console/dist` on the runner *before* the `goreleaser-action`
step, since goreleaser compiles `xdlc-agent` (and its `go:embed`)
directly on the runner rather than inside Docker — `Dockerfile.release`
itself only copies the already-built, already-embedded binary in.
