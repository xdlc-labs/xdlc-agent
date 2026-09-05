# API tokens

The ops console and `/api/*` use a **shared bearer secret you create yourself**. There is no “create token” button or signup — the daemon reads the secret from the environment at startup.

## Operator token (`XDLC_API_TOKEN`)

**1. Generate a secret** (any long random string):

```sh
# example — pick one
openssl rand -hex 32
# or
head -c 32 /dev/urandom | base64
```

Local eval can use a fixed string such as `dev-token` (never in production).

**2. Give it to the daemon before start:**

```sh
export XDLC_API_TOKEN='…paste secret…'
./bin/xdlc doctor --config config.yaml --skip-network   # checks it is set
./bin/xdlc daemon --config config.yaml
```

Docker / Compose: pass `-e XDLC_API_TOKEN=…` (or your secret manager).  
Helm: put the value in the chart’s existing Secret (see [Deployment](deployment.md)).

**3. Paste the same value in the console**

Open **Settings** → API token field → save. The browser stores it in `localStorage` and sends `Authorization: Bearer <token>` on API calls.

If the env var is **empty**, protected routes fail closed (**503**). `GET /api/health` stays open.

## Viewer token (optional)

```sh
export XDLC_API_VIEWER_TOKEN='…another secret…'
```

Read-only GETs accept this bearer; Fix / Promote / Revert still need the operator token (or an OIDC operator role).

## What this is not

| Thing | Relation |
|-------|----------|
| GitHub PAT / App | Separate — used for git and GitHub API (`GITHUB_TOKEN` / App) |
| Coding-agent keys | Separate — `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` / `CURSOR_API_KEY` (or console Manual Fix override) |
| OIDC SSO | Optional additive auth — see `server.oidc` in [Configuration](configuration.md) |

## Rotate

1. Generate a new secret  
2. Update daemon env / Kubernetes Secret and restart  
3. Update every console user’s Settings (or they will see unauthorized / degraded)

## Checklist

- [ ] `XDLC_API_TOKEN` set in the daemon process environment  
- [ ] Same value saved in console Settings  
- [ ] `xdlc doctor` shows `XDLC_API_TOKEN set` ok  
- [ ] Overview loads without the degraded “export token” banner  
