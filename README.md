<p align="center">
  <img src="https://xdlc.dev/images/brand/wordmark.png" width="360" alt="xdlc-labs">
</p>

<p align="center">
  <strong>Open-source CI Fix for your repos. You host it; nothing phones home.</strong>
</p>

<p align="center">
  <a href="https://github.com/xdlc-labs/xdlc-agent/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/xdlc-labs/xdlc-agent/ci.yml?style=flat-square&label=tests" alt="Tests"></a>
  <a href="https://github.com/xdlc-labs/xdlc-agent/releases"><img src="https://img.shields.io/github/v/release/xdlc-labs/xdlc-agent?include_prereleases&style=flat-square" alt="Release"></a>
  <a href="https://github.com/xdlc-labs/xdlc-agent/actions/workflows/release.yml"><img src="https://img.shields.io/github/actions/workflow/status/xdlc-labs/xdlc-agent/release.yml?style=flat-square&label=release" alt="Release workflow"></a>
  <a href="go.mod"><img src="https://img.shields.io/github/go-mod/go-version/xdlc-labs/xdlc-agent?style=flat-square" alt="Go"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue?style=flat-square" alt="License"></a>
  <img src="https://img.shields.io/badge/PRs-welcome-brightgreen?style=flat-square" alt="PRs Welcome">
</p>

---

You run one small daemon next to your repos. When CI breaks it can open a **Fix** with the coding agent you already use (`claude`, `codex`, `cursor`, or `gemini`). Promote and Revert are optional gates you add later. Open source (MIT). You host it; nothing phones home.

The CLI is called **`xdlc`**. The container image, Helm chart, and this repo are **`xdlc-agent`**.

## What you get

- **CI Fix** — a failed GitHub Actions run becomes a Fix, with the evidence the agent used
- **Your agent CLI** — `claude` / `codex` / `cursor` / `gemini` on `PATH`; your keys stay on your host
- **Policy before the agent** — gates decide Fix / Promote / Revert / noop before anything runs
- **Audit trail** — console, audit DB, `BACKLOG.md`, and `xdlc sessions show <id> --diff`
- **Optional Promote / Revert** — fast-forward `develop` → `main` after DEV smoke; revert `main` on a prod SLO breach
- **Ops console** — embedded on the same port as `/api/*`
- **Self-hosted MIT** — no SaaS in the path

## Quick start

One command. No cluster required.

```sh
curl -fsSL https://raw.githubusercontent.com/xdlc-labs/xdlc-agent/main/scripts/install.sh | bash
export PATH="$HOME/.local/bin:$PATH"   # if needed
xdlc demo --provider fake
```

Pin a release: `XDLC_VERSION=v0.0.1-beta.4`. More options: **[Install](https://xdlc.dev/agent/docs)**.

Then a real daemon:

```sh
xdlc init
# edit config.yaml: repos[].github + agent.provider (gates stay [ci])
export XDLC_API_TOKEN="$(openssl rand -hex 32)"
export GITHUB_TOKEN=...                           # or GitHub App (preferred)
xdlc doctor --config config.yaml --skip-network
xdlc daemon --config config.yaml
```

Open http://127.0.0.1:8080/ → **Settings** → paste the same `XDLC_API_TOKEN`. Full walkthrough: **[Getting started](https://xdlc.dev/agent/docs/getting-started)**.

**Docker** (console embedded). If `docker pull` returns `unauthorized`, the GHCR package is not public — run `docker login ghcr.io` with an account that has access, or build from source:

A container always binds a non-loopback address, so the container path needs
`server.require_webhook_secret: true` in `config.yaml` plus a
`GITHUB_WEBHOOK_SECRET` in the environment. The daemon refuses to start
without it. The local loopback run above (`127.0.0.1:8080`) does not need either.

```sh
# config.yaml for the container: addr: ":8080" + require_webhook_secret: true
export GITHUB_WEBHOOK_SECRET=...                  # same secret as the GitHub webhook
docker run --rm -p 8080:8080 \
  -v "$PWD/config.yaml:/etc/xdlc-agent/config.yaml:ro" \
  -e XDLC_API_TOKEN -e GITHUB_TOKEN -e GITHUB_WEBHOOK_SECRET \
  -e ANTHROPIC_API_KEY -e OPENAI_API_KEY -e CURSOR_API_KEY \
  ghcr.io/xdlc-labs/xdlc-agent:0.0.1-beta.4 \
  daemon --config /etc/xdlc-agent/config.yaml
```

**Helm** (single replica; audit DB is single-writer):

```sh
helm install xdlc-agent deploy/helm/xdlc-agent \
  --set image.tag=0.0.1-beta.4 \
  --set existingSecret=xdlc-agent-secrets \
  --set-file config=config.yaml
```

## How it works

Same diagram as the [architecture](https://xdlc.dev/agent/docs/architecture) page: one loop, three gates.

![One loop, three gates: xdlc-agent → GitHub → DEV → promote → PRODUCTION](https://xdlc.dev/images/architecture.jpg)

1. GitHub reports a failed `workflow_run` (or you enable DEV smoke / prod health later).
2. The daemon validates the webhook and asks policy what to do.
3. **Fix** runs your agent CLI with the failing logs and repo conventions (`AGENTS.md` / `CLAUDE.md`).
4. Evidence lands in the console, the audit store, and `xdlc sessions show`.

The default install is **CI Fix** only. GitOps promote and prod revert are opt-in. See [Optional profiles](https://xdlc.dev/agent/docs/production-loop).

## Ops console

Embedded at `/` when the daemon runs.

![Console overview](https://xdlc.dev/images/screenshots/console-overview.jpg)

![Repos](https://xdlc.dev/images/screenshots/console-repos.jpg)

![Manual Fix / Promote / Revert](https://xdlc.dev/images/screenshots/console-actions.jpg)

## Why xdlc?

| | xdlc-agent | Typical alternative |
|---|---|---|
| **CI Fix** | Long-lived daemon, fleet state, ops console | Paste logs into a coding agent by hand |
| **GitHub Actions as control plane** | No — you need a loop that outlives a job | A workflow that exits when the job does |
| **Copilot Autofix** | CI / DEV / prod gates under your policy | Security and dependency nits inside GitHub |
| **Flagger / Rollouts** | Complementary: they move traffic; we own agent + git policy | Canary / analysis only |
| **Deployment** | Self-hosted Docker / Helm / binary | SaaS |
| **Source** | MIT | Often proprietary |

## Docs

Guides (same order as the docs sidebar): **[xdlc.dev](https://xdlc.dev/docs)**.

Start: [Install](https://xdlc.dev/agent/docs) · [Getting started](https://xdlc.dev/agent/docs/getting-started) · [API tokens](https://xdlc.dev/agent/docs/api-tokens)

CI Fix: [GitHub webhooks](https://xdlc.dev/agent/docs/github-webhooks) · [Fix modes](https://xdlc.dev/agent/docs/fix-modes) · [Rules and skills](https://xdlc.dev/agent/docs/rules-and-skills) · [Fix sessions](https://xdlc.dev/agent/docs/sessions) · [Deployment](https://xdlc.dev/agent/docs/deployment) · [Operations](https://xdlc.dev/agent/docs/operations)

Optional: [Profiles](https://xdlc.dev/agent/docs/production-loop) · [GitOps](https://xdlc.dev/agent/docs/gitops-argo) · [Prod health](https://xdlc.dev/agent/docs/prod-health)

## Related

Shipping prompts, skills, MCP, or model pins? [Airlock](https://github.com/xdlc-labs/airlock) is a CI gate for those changes.

## Contribute

[Contributing](CONTRIBUTING.md) · [Roadmap](ROADMAP.md) · [Releasing](RELEASING.md) · [Security](SECURITY.md) · [Code of conduct](CODE_OF_CONDUCT.md) · [Changelog](CHANGELOG.md)

## License

[MIT](LICENSE) © xdlc contributors
