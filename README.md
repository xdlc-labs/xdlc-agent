<p align="center">
  <img src="https://xdlc.dev/images/brand/wordmark.png" width="360" alt="xdlc-labs">
</p>

<p align="center">
  <strong>CI broke. Your coding agent opens the PR.</strong><br>
  Self-hosted. Bring your own agent (<code>claude</code>, <code>codex</code>, <code>cursor</code>, <code>gemini</code>). Your keys never leave your box. MIT.
</p>

<p align="center">
  <a href="https://github.com/xdlc-labs/xdlc-agent/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/xdlc-labs/xdlc-agent/ci.yml?style=flat-square&label=tests" alt="Tests"></a>
  <a href="https://github.com/xdlc-labs/xdlc-agent/releases"><img src="https://img.shields.io/github/v/release/xdlc-labs/xdlc-agent?include_prereleases&style=flat-square" alt="Release"></a>
  <a href="https://github.com/xdlc-labs/xdlc-agent/actions/workflows/release.yml"><img src="https://img.shields.io/github/actions/workflow/status/xdlc-labs/xdlc-agent/release.yml?style=flat-square&label=release" alt="Release workflow"></a>
  <a href="go.mod"><img src="https://img.shields.io/github/go-mod/go-version/xdlc-labs/xdlc-agent?style=flat-square" alt="Go"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue?style=flat-square" alt="License"></a>
  <img src="https://img.shields.io/badge/PRs-welcome-brightgreen?style=flat-square" alt="PRs Welcome">
</p>

<p align="center">
  <img src="docs/media/demo.gif" width="800" alt="xdlc demo: CI red → agent fixes in a worktree → tests green → promote → revert">
</p>

---

**One failed run in, one PR out.** Point `xdlc fix` at a red GitHub Actions run. It clones the repo, hands the failing logs and your repo's `AGENTS.md`/`CLAUDE.md` to the coding agent you already pay for, lets it commit in its own git worktree, pushes, and opens the PR. The prompt, the agent's output, the diff and what it cost are recorded. Run it from a laptop, from a workflow, or let the daemon do it on every failure without you.

The CLI is **`xdlc`**. The container image, Helm chart, and this repo are **`xdlc-agent`**.

## Try it in 30 seconds, no keys

```sh
curl -fsSL https://raw.githubusercontent.com/xdlc-labs/xdlc-agent/main/scripts/install.sh | bash
export PATH="$HOME/.local/bin:$PATH"   # if needed
xdlc demo
```

That is the GIF above: a throwaway repo with a broken test, a fake agent, a real worktree, a real push, tests green, then a promote and a revert. `xdlc demo --provider claude` swaps in the real agent. Pin a release with `XDLC_VERSION=v0.0.1-beta.5`.

## Fix a real failed run

```sh
gh auth login                  # or export GITHUB_TOKEN=...
npm i -g @anthropic-ai/claude-code   # or codex / cursor-agent / gemini, anything on PATH
xdlc fix https://github.com/you/repo/actions/runs/123456789
```

```
github auth: gh auth token
run: ci on develop@a1b2c3d → failure
agent: claude, mode: pr, clone: ~/.cache/xdlc/repos/you/repo
fixing… (a real agent usually takes 2–10 minutes)
agent said: TestParse expected RFC3339; parser dropped the zone. Restored it.
cost: $0.71
session: xdlc sessions show 20260908T104727Z-repo --diff --dir ~/.cache/xdlc/sessions
PR: https://github.com/you/repo/pull/124
```

No daemon, no webhook, no config file. `--mode direct` pushes to the failing branch instead of opening a PR. `-m "the flake is in the seed data"` gives the agent a hint. The agent's stdout, the exact prompt and the diff are on disk under `sessions/`.

## Or run it from GitHub Actions

The one-repo on-ramp. A second workflow fires when your CI fails and runs the same `xdlc fix`:

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

Turn on **Settings → Actions → Allow GitHub Actions to create and approve pull requests**, or pass a PAT as `github-token`. This is a Fix per failure, nothing more. Fleet policy, promote/revert and the console live in the daemon, [for reasons](https://xdlc.dev/agent/docs/why-not-github-action).

## Or run the daemon

Unattended, across repos, with policy in front of the agent:

```sh
xdlc init
# edit config.yaml: repos[].github + agent.provider (gates stay [ci])
export XDLC_API_TOKEN="$(openssl rand -hex 32)"
export GITHUB_TOKEN=...                           # or GitHub App (preferred)
xdlc doctor --config config.yaml --skip-network
xdlc daemon --config config.yaml
```

Open http://127.0.0.1:8080/ → **Settings** → paste the same `XDLC_API_TOKEN`. Full walkthrough: **[Getting started](https://xdlc.dev/agent/docs/getting-started)**.

<details>
<summary><strong>Docker</strong></summary>

A container always binds a non-loopback address, so the container path needs
`server.require_webhook_secret: true` in `config.yaml` plus a
`GITHUB_WEBHOOK_SECRET` in the environment. The daemon refuses to start
without it. If `docker pull` returns `unauthorized`, the GHCR package is not public yet — `docker login ghcr.io` or build from source.

```sh
# config.yaml for the container: addr: ":8080" + require_webhook_secret: true
export GITHUB_WEBHOOK_SECRET=...                  # same secret as the GitHub webhook
docker run --rm -p 8080:8080 \
  -v "$PWD/config.yaml:/etc/xdlc-agent/config.yaml:ro" \
  -e XDLC_API_TOKEN -e GITHUB_TOKEN -e GITHUB_WEBHOOK_SECRET \
  -e ANTHROPIC_API_KEY -e OPENAI_API_KEY -e CURSOR_API_KEY \
  ghcr.io/xdlc-labs/xdlc-agent:0.0.1-beta.5 \
  daemon --config /etc/xdlc-agent/config.yaml
```
</details>

<details>
<summary><strong>Helm</strong></summary>

Single replica; the audit DB is single-writer.

```sh
helm install xdlc-agent deploy/helm/xdlc-agent \
  --set image.tag=0.0.1-beta.5 \
  --set existingSecret=xdlc-agent-secrets \
  --set-file config=config.yaml
```
</details>

## What you get

- **Your agent, your keys** — `claude` / `codex` / `cursor` / `gemini` on `PATH`. Nothing phones home; there is no SaaS in the path
- **A worktree per Fix** — the agent commits on an `xdlc/<session>` branch in its own checkout; xdlc pushes. Two Fixes on one repo run side by side, and a killed run cannot dirty your clone
- **Repo conventions in the prompt** — `AGENTS.md`, `CLAUDE.md`, `.xdlc/rules.md`, `.xdlc/skills/*.md`
- **Receipts** — every Fix records prompt, output, diff, verdict and cost. `xdlc sessions show <id> --diff`. A Fix that committed nothing is recorded as exactly that, never as a success
- **Policy before the agent** — in daemon mode, gates decide Fix / Promote / Revert / noop before anything runs; flap and circuit breakers across the fleet
- **Optional Promote / Revert** — fast-forward `develop` → `main` after DEV smoke; revert `main` on a prod SLO breach
- **Ops console** — embedded on the same port as `/api/*`

## How it works

One loop, three gates. Same diagram as the [architecture](https://xdlc.dev/agent/docs/architecture) page.

![One loop, three gates: xdlc-agent → GitHub → DEV → promote → PRODUCTION](https://xdlc.dev/images/architecture.jpg)

1. GitHub reports a failed `workflow_run` (or you run `xdlc fix` by hand, or enable DEV smoke / prod health later).
2. The daemon validates the webhook and asks policy what to do.
3. **Fix** runs your agent CLI with the failing logs and repo conventions, in a fresh worktree.
4. Evidence lands in the console, the audit store, and `xdlc sessions show`.

The default install is **CI Fix** only. GitOps promote and prod revert are opt-in. See [Optional profiles](https://xdlc.dev/agent/docs/production-loop).

## Ops console

Embedded at `/` when the daemon runs.

![Console overview](https://xdlc.dev/images/screenshots/console-overview.jpg)

![Repos](https://xdlc.dev/images/screenshots/console-repos.jpg)

![Manual Fix / Promote / Revert](https://xdlc.dev/images/screenshots/console-actions.jpg)

## Why not just…

- **…paste the logs into Claude Code myself?** You can, at 3pm. xdlc does it at 3am, records what it sent and what came back, and tells you what it cost.
- **…Copilot Autofix?** It fixes security and dependency findings, inside GitHub, with GitHub's model. xdlc fixes whatever made your CI red, with any agent, on your machine.
- **…a Claude Code / Codex GitHub Action?** That is what `xdlc fix` in a workflow is. The daemon adds the part a job cannot: a loop that outlives the job, shared state across repos, policy, promote and revert. [Why not a GitHub Action](https://xdlc.dev/agent/docs/why-not-github-action)
- **…Flagger / Argo Rollouts?** Complementary. They move traffic; xdlc owns the agent and the git policy.

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
