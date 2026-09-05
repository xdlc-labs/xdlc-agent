# Getting started

**Audience:** operators evaluating xdlc-agent, or platform engineers wiring a first install.

By the end you have either a zero-infra **demo** loop or a local **daemon** + ops console. This is not a production cluster yet — see [Production loop](production-loop.md) next.

## Prerequisites

- Linux / macOS (or WSL)
- `git`
- Either:
  - **Path A:** Go 1.25+ to build `xdlc`, or
  - **Path B:** Docker (or Podman) to run `ghcr.io/xdlc-labs/xdlc-agent`
- For a real Fix (optional): coding-agent CLI on `PATH` (`claude`, `codex`, or `cursor-agent`) + matching API key env

## Overview

One daemon watches configured repos. Gates emit signals; policy picks **Fix**, **Promote**, or **Revert**. Evidence and outcomes land in audit history + `BACKLOG.md`. The ops console is embedded on the same HTTP port as `/api/*`.

| Binary / artifact | Name |
|-------------------|------|
| CLI + daemon | `xdlc` |
| Container image / Helm chart / repo | `xdlc-agent` |

## Path A — demo (zero infra)

No Kind, Argo, Prometheus, or GitHub required. Seeds a temp git repo, runs Fix → Promote → Revert with a fake agent (or a real provider).

```sh
git clone https://github.com/xdlc-labs/xdlc-agent.git
cd xdlc-agent
go build -o bin/xdlc ./cmd/xdlc-agent
./bin/xdlc demo --provider fake
```

Expect lines like `signal=… action=Fix status=ok` and a final `demo: ok`.

Real coding agent (maps your key to the env the CLI expects):

```sh
export CURSOR_API_KEY="$cursor_agent_key"   # or ANTHROPIC_API_KEY / OPENAI_API_KEY
./bin/xdlc demo --provider cursor --scenario all
```

## Path B — doctor + daemon

**1. Config**

```sh
cp config.example.yaml config.yaml
# edit repos[].github, agent.provider, gates as needed
```

**2. Secrets**

Create the console API token yourself (shared secret — not issued by GitHub). See **[API tokens](api-tokens.md)**.

```sh
export XDLC_API_TOKEN="$(openssl rand -hex 32)"   # or any long secret; local: dev-token
export GITHUB_TOKEN=...                  # or GitHub App (preferred) when talking to GH
# export ANTHROPIC_API_KEY=...           # if provider: claude
# export OPENAI_API_KEY=...              # if provider: codex
# export CURSOR_API_KEY=...              # if provider: cursor
```

**3. Sanity**

```sh
./bin/xdlc doctor --config config.yaml --skip-network
```

**4. Run**

```sh
./bin/xdlc daemon --config config.yaml
```

Or Docker (tag must exist on GHCR for your release):

```sh
docker run --rm -p 8080:8080 \
  -v "$PWD/config.yaml:/etc/xdlc-agent/config.yaml:ro" \
  -e XDLC_API_TOKEN -e GITHUB_TOKEN -e GITHUB_WEBHOOK_SECRET \
  -e ANTHROPIC_API_KEY -e OPENAI_API_KEY -e CURSOR_API_KEY \
  ghcr.io/xdlc-labs/xdlc-agent:0.0.1-beta.2 \
  daemon --config /etc/xdlc-agent/config.yaml
```

Open http://127.0.0.1:8080/ — paste `XDLC_API_TOKEN` in **Settings**. Browse **Docs** in the nav for the rest of this guide.

## Next steps

| Role | Go to |
|------|--------|
| Operator / eval | [Console](console.md), Manual Actions |
| Platform admin | [Production loop](production-loop.md) |
| Contributors | [CONTRIBUTING](CONTRIBUTING.md) |
