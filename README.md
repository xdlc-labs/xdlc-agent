<p align="center">
  <img src="https://xdlc.dev/images/brand/wordmark.png" width="300" alt="xdlc-labs">
</p>

<p align="center">
  <strong>xdlc</strong>
</p>

<p align="center">
  When CI fails, your coding agent opens a pull request.
</p>

<p align="center">
  <a href="https://github.com/xdlc-labs/xdlc-agent/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/xdlc-labs/xdlc-agent/ci.yml?style=flat-square&label=tests" alt="Tests"></a>
  <a href="https://github.com/xdlc-labs/xdlc-agent/releases"><img src="https://img.shields.io/github/v/release/xdlc-labs/xdlc-agent?include_prereleases&style=flat-square" alt="Release"></a>
  <a href="go.mod"><img src="https://img.shields.io/github/go-mod/go-version/xdlc-labs/xdlc-agent?style=flat-square" alt="Go"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue?style=flat-square" alt="License"></a>
</p>

---

A failed GitHub Actions run becomes a **Fix**. Your agent CLI (`claude`, `codex`, `cursor-agent`, or `gemini`) gets the failing logs and the repo's conventions, works in a throwaway git worktree, commits, and xdlc pushes the pull request. Policy decides whether the agent runs. Promote and Revert stay off until you turn them on.

The CLI is **`xdlc`**. The image, Helm chart, and this repository are **xdlc-agent**.

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/loop-dark.svg">
    <img src="docs/assets/loop-light.svg" width="900" alt="CI, DEV smoke, and prod health enter a policy gate. Only Fix reaches the coding agent.">
  </picture>
</p>

## Install

Linux and macOS, `amd64` and `arm64`. Releases are pre-releases, so GitHub's "latest" link skips them. Pin a tag.

```bash
curl -fsSL https://raw.githubusercontent.com/xdlc-labs/xdlc-agent/main/scripts/install.sh \
  | XDLC_VERSION=v0.0.1-beta.7 bash
export PATH="$HOME/.local/bin:$PATH"   # if needed
xdlc demo --provider fake
```

`xdlc demo --provider fake` needs no API key. It builds a throwaway repo with a failing test and runs Fix, Promote, and Revert against it.

<p align="center">
  <img src="docs/assets/demo.gif" width="760" alt="xdlc demo: Fix, then a promote, then a revert on a throwaway repo">
</p>

Docker and Helm: [Install](https://xdlc.dev/agent/docs) and [Deployment](https://xdlc.dev/agent/docs/deployment).

## Fix one failed run

No daemon, no webhook, no config file.

```bash
gh auth login                        # or export GITHUB_TOKEN=...
npm i -g @anthropic-ai/claude-code   # or codex / cursor-agent / gemini
xdlc fix https://github.com/you/repo/actions/runs/123456789
```

[#51](https://github.com/xdlc-labs/xdlc-agent/pull/51) is one this repo opened on itself.

## Use it on your repo

```yaml
# .github/workflows/xdlc-fix.yml
name: xdlc fix
on:
  workflow_run:
    workflows: [ci]          # the name of your CI workflow
    types: [completed]
permissions:
  contents: write
  pull-requests: write
  actions: read
jobs:
  fix:
    if: github.event.workflow_run.conclusion == 'failure'
    runs-on: ubuntu-latest
    steps:
      - uses: xdlc-labs/xdlc-agent@main
        with:
          provider: claude
        env:
          ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
```

Turn on **Settings → Actions → Allow GitHub Actions to create and approve pull requests**, or pass a PAT as `github-token`.

## Run the daemon

```bash
xdlc init
# edit config.yaml: repos[].github + agent.provider (gates stay [ci])
export XDLC_API_TOKEN="$(openssl rand -hex 32)"
export GITHUB_TOKEN=...              # or a GitHub App
xdlc doctor --config config.yaml --skip-network
xdlc daemon --config config.yaml
```

Open http://127.0.0.1:8080/ → **Settings** → paste the same `XDLC_API_TOKEN`. Full walkthrough: [Getting started](https://xdlc.dev/agent/docs/getting-started).

Public beta. [Airlock](https://github.com/xdlc-labs/airlock) gates prompts, skills, MCP, and model pins.

[Docs](https://xdlc.dev/docs) ·
[Getting started](https://xdlc.dev/agent/docs/getting-started) ·
[Changelog](CHANGELOG.md) ·
[Contributing](CONTRIBUTING.md) ·
[Security](SECURITY.md) ·
[MIT](LICENSE)
