# xdlc-agent

[![ci](https://github.com/xdlc-labs/xdlc-agent/actions/workflows/ci.yml/badge.svg)](https://github.com/xdlc-labs/xdlc-agent/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/xdlc-labs/xdlc-agent?include_prereleases)](https://github.com/xdlc-labs/xdlc-agent/releases)
[![release workflow](https://github.com/xdlc-labs/xdlc-agent/actions/workflows/release.yml/badge.svg)](https://github.com/xdlc-labs/xdlc-agent/actions/workflows/release.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/xdlc-labs/xdlc-agent)](go.mod)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![PRs welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](docs/CONTRIBUTING.md)

You run one small daemon next to your repos. When CI breaks, a DEV smoke test fails, or prod looks unhealthy, it can open a **Fix**, **Promote** a good build, or **Revert** a bad one, using the coding agent you already use (`claude`, `codex`, `cursor`, or `gemini`). Open source (MIT). You host it; nothing phones home.

The CLI is called **`xdlc`**. The container image, Helm chart, and this repo are **`xdlc-agent`**.

## Why this exists

Built for platform and SRE teams that already have CI and GitOps. Overnight, someone still pastes failing logs into a coding agent by hand. This daemon does that job under your gates, then can promote a good DEV build or revert a bad prod one, and leaves an audit trail.

**You keep control.** Self-hosted. MIT. Your agent CLI and keys (`claude` / `codex` / `cursor` / `gemini`). Policy runs before the agent. History goes to the console, the audit DB, and `BACKLOG.md`. Nothing phones home.

**Not a substitute for:**

- **GitHub Actions** as your control plane ([why](docs/why-not-github-action.md)): you need a long-lived loop, fleet state, and an ops console
- **Copilot Autofix**: security/dependency nits inside GitHub; we cover CI → DEV → prod Fix / Promote / Revert
- **Flagger / Rollouts**: they move traffic; we own agent + git policy when gates fail ([vs alternatives](docs/vs-alternatives.md))

Beta: `xdlc demo`, Fix / Promote / Revert, and the console are the path to try first. See [Changelog](docs/CHANGELOG.md).

## How it works

Gates fire in order: **CI → DEV → PROD**.

| Signal | Action |
|--------|--------|
| **CI fail** | **Fix** (coding agent + evidence) |
| **DEV smoke fail** | **Fix** |
| **DEV smoke pass** | **Promote** (fast-forward `develop` → `main` only) |
| **PROD SLO breach** | **Revert** on `main` |

Every Fix records what the agent was told and what it changed: `xdlc sessions show <id> --diff` ([Fix sessions](docs/sessions.md)). Teach it your conventions with `AGENTS.md` / `CLAUDE.md` ([Rules and skills](docs/rules-and-skills.md)).

Green CI → DEV stays your GitOps path. The agent reacts to gates; it does not invent deploys.

![Architecture: xdlc-agent loop with CI, DEV, and PROD gates](docs/assets/architecture.jpg)

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/xdlc-labs/xdlc-agent/main/scripts/install.sh | sh
export PATH="$HOME/.local/bin:$PATH"   # if needed
xdlc demo --provider fake
```

Pin a release: `XDLC_VERSION=v0.0.1-beta.1`. More options (Docker, from source): **[Install](docs/install.md)**.

## Quick start

Walkthrough: **[Getting started](docs/getting-started.md)** · **[API tokens](docs/api-tokens.md)**.

```sh
xdlc init
# edit config.yaml: repos + agent.provider
export XDLC_API_TOKEN="$(openssl rand -hex 32)"   # or dev-token locally
export GITHUB_TOKEN=...                           # or GitHub App (preferred)
# export ANTHROPIC_API_KEY=...                    # if provider: claude
xdlc doctor --config config.yaml --skip-network
xdlc daemon --config config.yaml
```

Open http://127.0.0.1:8080/ → **Settings** → paste the same `XDLC_API_TOKEN`.

**Docker** (console embedded; tag must exist on GHCR):

```sh
docker run --rm -p 8080:8080 \
  -v "$PWD/config.yaml:/etc/xdlc-agent/config.yaml:ro" \
  -e XDLC_API_TOKEN -e GITHUB_TOKEN -e GITHUB_WEBHOOK_SECRET \
  -e ANTHROPIC_API_KEY -e OPENAI_API_KEY -e CURSOR_API_KEY \
  ghcr.io/xdlc-labs/xdlc-agent:0.0.1-beta.2 \
  daemon --config /etc/xdlc-agent/config.yaml
```

**Helm** (single replica; audit DB is single-writer):

```sh
helm install xdlc-agent deploy/helm/xdlc-agent \
  --set image.tag=0.0.1-beta.2 \
  --set existingSecret=xdlc-agent-secrets \
  --set-file config=config.yaml
```

Cluster detail: [Deployment](docs/deployment.md). Point GitHub `workflow_run` webhooks at `/webhooks/github`.

## Ops console

Embedded at `/` when the daemon runs (API under `/api/*`).

![Console overview](docs/assets/screenshots/console-overview.jpg)

![Repos](docs/assets/screenshots/console-repos.jpg)

![Manual Fix / Promote / Revert](docs/assets/screenshots/console-actions.jpg)

## What you bring

1. CI that fails clearly on the integration branch
2. Deploy path green build → DEV (usually GitOps)
3. Optional smoke + Prometheus for promote / revert
4. Coding-agent CLI on `PATH` (`claude`, `codex`, `cursor-agent`, or `gemini`)

## Documentation

Full guides live under [`docs/`](docs/README.md). Same pages appear in the ops console under **Docs**.

| Start | Production | Reference | More |
|-------|------------|-----------|------|
| [Install](docs/install.md) | [Production loop](docs/production-loop.md) | [Configuration](docs/configuration.md) | [vs alternatives](docs/vs-alternatives.md) |
| [Getting started](docs/getting-started.md) | [GitHub](docs/github-webhooks.md) · [Fix](docs/fix-modes.md) · [GitOps](docs/gitops-argo.md) | [Deployment](docs/deployment.md) · [Operations](docs/operations.md) | [Why not a GitHub Action](docs/why-not-github-action.md) |
| [Rules and skills](docs/rules-and-skills.md) | [Fix sessions](docs/sessions.md) | [Configuration](docs/configuration.md) | |
| [API tokens](docs/api-tokens.md) | [Prod health](docs/prod-health.md) | [Architecture](docs/architecture.md) · [API](docs/api-reference.md) · [Security](docs/SECURITY.md) | [Changelog](docs/CHANGELOG.md) |

## Contribute

[Contributing](docs/CONTRIBUTING.md) · [Security](docs/SECURITY.md) · [Code of conduct](docs/CODE_OF_CONDUCT.md)

## License

[MIT](LICENSE) © xdlc contributors
