# Getting started

**Audience:** operators evaluating xdlc-agent, or platform engineers wiring a first install.

By the end you have either a zero-infra **demo** or a local **daemon** that can **Fix** a red CI run. GitOps promote and prod revert are optional profiles — see [Optional profiles](production-loop.md) after this works.

## Prerequisites

- Linux / macOS (or WSL)
- `git` (demo / from-source paths)
- **`xdlc` on PATH** — see **[Install](install.md)** (`curl … | sh`, Docker, or `go build`)
- For a real Fix (optional): coding-agent CLI on `PATH` (`claude`, `codex`, `cursor-agent`, or `gemini`) + matching API key env

## Overview

One daemon watches configured repos. The default install is **CI Fix**: a GitHub `workflow_run` failure becomes a Fix. You can later add DEV smoke → Promote and prod SLO → Revert. Evidence lands in audit history + `BACKLOG.md`. The ops console is embedded on the same HTTP port as `/api/*`.

| Binary / artifact | Name |
|-------------------|------|
| CLI + daemon | `xdlc` |
| Container image / Helm chart / repo | `xdlc-agent` |

## Install CLI

```sh
curl -fsSL https://raw.githubusercontent.com/xdlc-labs/xdlc-agent/main/scripts/install.sh | sh
export PATH="$HOME/.local/bin:$PATH"   # if needed
xdlc version
```

Full options (pin version, Docker, from source): [Install](install.md).

## Path A — demo (zero infra)

No Kind, Argo, Prometheus, or GitHub required. Seeds a temp git repo, runs Fix → Promote → Revert with a fake agent (or a real provider). Demo always exercises the full loop; your install does not have to.

```sh
xdlc demo --provider fake
```

Expect lines like `signal=… action=Fix status=ok` and a final `demo: ok`.

Real coding agent (maps your key to the env the CLI expects):

```sh
export CURSOR_API_KEY="$cursor_agent_key"   # or ANTHROPIC_API_KEY / OPENAI_API_KEY
xdlc demo --provider cursor --scenario all
```

## Path B — doctor + daemon (CI Fix)

**1. Config**

`xdlc init` writes the **CI Fix** profile (`gates: [ci]` only). That is enough to Fix a red GitHub Actions run.

```sh
xdlc init
# or seed repos from checkouts already on this box:
xdlc init --scan ~/src
# GitOps / paved road (needs Argo / Prom):
# xdlc init --profile gitops
# xdlc init --profile full
# or: cp config.example.yaml config.yaml
# edit repos[].github and agent.provider
```

**2. Secrets**

Create the console API token yourself (shared secret — not issued by GitHub). See **[API tokens](api-tokens.md)**.

```sh
export XDLC_API_TOKEN="$(openssl rand -hex 32)"   # or any long secret; local: dev-token
export GITHUB_TOKEN=...                  # or GitHub App (preferred) when talking to GH
# export ANTHROPIC_API_KEY=...           # if provider: claude
# export OPENAI_API_KEY=...              # if provider: codex
# export CURSOR_API_KEY=...              # if provider: cursor
# export GEMINI_API_KEY=...              # if provider: gemini
```

CI Fix also needs `GITHUB_WEBHOOK_SECRET` when the daemon is not loopback-only. You do **not** need Argo, Prometheus, `kubectl`, or `argocd` for this profile.

**3. Sanity**

```sh
xdlc doctor --config config.yaml --skip-network
```

Local clones with `repos[].dir` set do not need `GITHUB_TOKEN`; doctor **warns** instead of failing. Export a token (or GitHub App) when the daemon must clone, fetch CI logs, or rerun jobs.

OTLP is off unless you set `OTEL_EXPORTER_OTLP_ENDPOINT`. Local `xdlc daemon` will not talk to `otel-collector.monitoring.svc`.

Repeatable loopback of the full paved road (stub agent, optional Minikube Argo app): `scripts/e2e-local.sh` in this repo. Console token: `dev-token`. `agent.timeout` in that harness is **10m**. You do not need that script for CI Fix.

**4. Run**

```sh
xdlc daemon --config config.yaml
```

Or Docker (tag must exist on GHCR for your release):

```sh
docker run --rm -p 8080:8080 \
  -v "$PWD/config.yaml:/etc/xdlc-agent/config.yaml:ro" \
  -e XDLC_API_TOKEN -e GITHUB_TOKEN -e GITHUB_WEBHOOK_SECRET \
  -e ANTHROPIC_API_KEY -e OPENAI_API_KEY -e CURSOR_API_KEY -e GEMINI_API_KEY \
  ghcr.io/xdlc-labs/xdlc-agent:0.0.1-beta.2 \
  daemon --config /etc/xdlc-agent/config.yaml
```

Open http://127.0.0.1:8080/ — paste `XDLC_API_TOKEN` in **Settings**. Point GitHub `workflow_run` at `/webhooks/github` ([GitHub](github-webhooks.md)).

## After the first Fix

```sh
xdlc sessions ls                  # which Fixes ran
xdlc sessions show <id> --diff    # what one of them changed
```

Then teach it your conventions with an `AGENTS.md` or `CLAUDE.md` in the repo — see [Rules and skills](rules-and-skills.md).

## Next steps

| Role | Go to |
|------|--------|
| Operator / eval | [Console](console.md), Manual Actions |
| Reviewing agent work | [Fix sessions](sessions.md), [Rules and skills](rules-and-skills.md) |
| Wire GitHub → Fix | [GitHub webhooks](github-webhooks.md) |
| Add Promote / Revert later | [Optional profiles](production-loop.md) |
| Contributors | [CONTRIBUTING](CONTRIBUTING.md) |
