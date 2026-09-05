# xdlc-agent

[![ci](https://github.com/xdlc-labs/xdlc-agent/actions/workflows/ci.yml/badge.svg)](https://github.com/xdlc-labs/xdlc-agent/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/xdlc-labs/xdlc-agent)](https://github.com/xdlc-labs/xdlc-agent/releases)
[![Go](https://img.shields.io/github/go-mod/go-version/xdlc-labs/xdlc-agent)](go.mod)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![PRs welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](docs/CONTRIBUTING.md)

**Self-hosted agentic delivery loop.** 
One Go daemon watches your repos. When CI fails, DEV smoke fails, or prod SLOs breach, it **Fix**es, **Promote**s, or **Revert**s under policy - using *your* coding agent (`claude` / `codex` / `cursor`). MIT. No SaaS in the loop.

![Architecture: xdlc-agent loop with CI, DEV, and PROD gates](docs/assets/architecture.jpg)

---

## Why

Most “AI for CI” means a human pastes logs into a chat window. 
**xdlc-agent** is the opposite: the signal arrives at a daemon that is already authorized to act.

| This is for | This is not for |
|-------------|-----------------|
| Routine, shaped failures (red build, failed smoke, SLO breach) | Novel feature design / architecture debates |
| Teams that already trust CI + GitOps | Replacing your pipeline with a chatbot |
| Policy: *whether* an agent may touch a repo | Prompt-only “vibes” without gates |

---

## How it works

| Signal | Action |
|--------|--------|
| **CI fail** | **Fix** - coding-agent subagent (run URL / logs as evidence) |
| **DEV smoke fail** | **Fix** - probe logs → subagent |
| **DEV smoke pass** | **Promote** - fast-forward only `develop` → `main` |
| **PROD p95 / error-rate breach** | **Revert** - `git revert` on `main` |

Green CI → DEV stays your GitOps path (image tag + Argo CD). The agent does not invent deploys; it reacts to gates you already trust.

Details: [docs/architecture.md](docs/architecture.md).

---

## Ops console

The daemon serves an embedded console at `/` (API under `/api/*`).

![Console overview](docs/assets/screenshots/console-overview.jpg)

![Repos](docs/assets/screenshots/console-repos.jpg)

![Manual Fix / Promote / Revert](docs/assets/screenshots/console-actions.jpg)

On **401**, paste your operator bearer token (`XDL_API_TOKEN`) in the banner or Settings.

---

## Quick start

### 1. Build

```sh
git clone https://github.com/xdlc-labs/xdlc-agent.git
cd xdlc-agent

# optional: embed the console into the binary
cd ui && bun install && bun run build && cd ..
mkdir -p internal/console/dist && cp -a ui/dist/. internal/console/dist/

go build -o bin/xdlc-agent ./cmd/xdlc-agent
```

Or skip the UI build and use the published image (console already embedded).

### 2. Configure

```sh
./bin/xdlc-agent init
# edit config.yaml - set repos[].github and agent.provider
# see config.example.yaml for the full schema

./bin/xdlc-agent validate --config config.yaml
```

Minimal config:

```yaml
repos:
 - name: my-service
 github: your-org/my-service
 gates: [ci] # add dev-smoke, prod-health when ready

server:
 addr: "127.0.0.1:8080"
 require_webhook_secret: false # local only
 api_token_env: XDL_API_TOKEN
 github_webhook_secret_env: GITHUB_WEBHOOK_SECRET

gates:
 ci:
 trigger: on_push

agent:
 provider: claude # claude | codex | cursor
 timeout: 10m
```

### 3. Secrets

```sh
export GITHUB_TOKEN=ghp_... # or GitHub App envs (preferred)
export GITHUB_WEBHOOK_SECRET=... # match the GitHub webhook
export XDL_API_TOKEN=... # console /api operator token
export ANTHROPIC_API_KEY=... # if agent.provider: claude
# export OPENAI_API_KEY=... # codex
# export CURSOR_API_KEY=... # cursor
```

| Provider | CLI on `PATH` | API key |
|----------|---------------|---------|
| `claude` (default) | `claude` | `ANTHROPIC_API_KEY` |
| `codex` | `codex` | `OPENAI_API_KEY` |
| `cursor` | `cursor-agent` | `CURSOR_API_KEY` |

### 4. Run

```sh
./bin/xdlc-agent daemon --config config.yaml
```

- Console: http://127.0.0.1:8080/
- Health: `GET /api/health`
- Point GitHub `workflow_run` webhooks at `http://<host>:8080/webhooks/github`

```sh
./bin/xdlc-agent history
./bin/xdlc-agent backlog list
./bin/xdlc-agent gate check ci --config config.yaml
```

---

## Docker

```sh
docker run --rm -p 8080:8080 \
 -v "$PWD/config.yaml:/etc/xdlc-agent/config.yaml:ro" \
 -e GITHUB_TOKEN \
 -e GITHUB_WEBHOOK_SECRET \
 -e XDL_API_TOKEN \
 -e ANTHROPIC_API_KEY \
 ghcr.io/xdlc-labs/xdlc-agent:2.0.0 \
 daemon --config /etc/xdlc-agent/config.yaml
```

---

## Helm

```sh
kubectl create secret generic xdlc-agent-secrets \
 --from-literal=GITHUB_TOKEN="$GITHUB_TOKEN" \
 --from-literal=GITHUB_WEBHOOK_SECRET="$GITHUB_WEBHOOK_SECRET" \
 --from-literal=XDL_API_TOKEN="$XDL_API_TOKEN" \
 --from-literal=ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY"

helm install xdlc-agent deploy/helm/xdlc-agent \
 --set image.tag=2.0.0 \
 --set existingSecret=xdlc-agent-secrets \
 --set-file config=config.yaml
```

See [deploy/helm/xdlc-agent/values.yaml](deploy/helm/xdlc-agent/values.yaml). Chart enforces `replicaCount: 1` (single-writer audit DB).

---

## What you still provide

This repo is the **daemon**. You still need:

1. CI that fails clearly on `develop`
2. Deploy path green build → DEV (usually GitOps + Argo CD)
3. Optional smoke Job (`probe_job`) for DEV
4. Prometheus (or any PromQL API) for PROD health
5. Webhooks (GitHub; optional Argo CD / Alertmanager)

Optional: `xdlc-agent validate --config config.yaml --gitops-dir <path>` cross-checks `argocd_app` names against an app-of-apps tree.

---

## Project layout

```
cmd/xdlc-agent/ CLI + daemon entrypoint
internal/ orchestrator, gates, dispatch, API, store, webhooks, …
ui/ ops console (Vite; embedded via go:embed)
deploy/helm/ agent Helm chart
docs/ architecture, contributing, security, changelog
config.example.yaml reference config
```

---

## Develop

```sh
make test
make build
make lint # golangci-lint v2.13.2
cd ui && bun install && bun run test && bun run build
```

- [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md)
- [docs/SECURITY.md](docs/SECURITY.md) - private vulnerability reports
- [docs/CODE_OF_CONDUCT.md](docs/CODE_OF_CONDUCT.md)
- [docs/CHANGELOG.md](docs/CHANGELOG.md)

---

## License

[MIT](LICENSE) © xdlc contributors
