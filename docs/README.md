# Docs

Ops docs for **xdlc-agent** (CLI binary **`xdlc`**).

## Start

- [Install](install.md) — `curl … | sh`, Docker, from source
- [Getting started](getting-started.md) — demo or CI Fix daemon (`scripts/e2e-local.sh` is the full paved-road loopback)
- [API tokens](api-tokens.md) — create `XDLC_API_TOKEN` (shared secret)

## CI Fix

- [GitHub webhooks](github-webhooks.md) — `workflow_run` → Fix
- [Fix modes](fix-modes.md) — direct push vs PR
- [Rules and skills](rules-and-skills.md) — what the agent is told
- [Fix sessions](sessions.md) — what the agent did
- [Deployment](deployment.md) — Docker + Helm
- [Operations](operations.md) — day-2

## Optional gates

- [Profiles](production-loop.md) — `ci` / `gitops` / `full`
- [GitOps / ArgoCD](gitops-argo.md) — DEV smoke + Promote
- [Prod health](prod-health.md) — Prometheus / Alertmanager → Revert

## Reference

- [Configuration](configuration.md)
- [Console](console.md)
- [Architecture](architecture.md)
- [API reference](api-reference.md)
- [Security](SECURITY.md)
- [Contributing](CONTRIBUTING.md)
- [vs alternatives](vs-alternatives.md)
- [Why not a GitHub Action](why-not-github-action.md)

In the ops console: open **Docs** in the left nav (`/docs`).
