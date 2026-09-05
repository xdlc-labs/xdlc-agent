# xdlc-agent

[![ci](https://github.com/xdlc-labs/xdlc-agent/actions/workflows/ci.yml/badge.svg)](https://github.com/xdlc-labs/xdlc-agent/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/xdlc-labs/xdlc-agent?include_prereleases)](https://github.com/xdlc-labs/xdlc-agent/releases/tag/v0.0.1-beta.1)
[![release workflow](https://github.com/xdlc-labs/xdlc-agent/actions/workflows/release.yml/badge.svg)](https://github.com/xdlc-labs/xdlc-agent/actions/workflows/release.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/xdlc-labs/xdlc-agent)](go.mod)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![PRs welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](docs/CONTRIBUTING.md)

**Self-hosted agentic delivery for platform teams.**  
One open-source daemon watches your repos. When CI fails, DEV smoke fails, or prod SLOs breach, it **Fix**es, **Promote**s, or **Revert**s under policy  -  using *your* coding agent (`claude` / `codex` / `cursor`). MIT. No SaaS in the loop.

![Architecture: xdlc-agent loop with CI, DEV, and PROD gates](docs/assets/architecture.jpg)

---

## Why agentic platform engineering?

Platform engineering already owns the paved road: CI, GitOps, environments, SLOs. What it usually *doesn't* own is the overnight grind when that road turns red  -  failed builds, flaky smokes, bad promotes, and “someone paste the logs into ChatGPT.”

**Agentic platform engineering** means the platform itself can act on those signals, within the same gates and policy you already trust:

| Without agents | With xdlc-agent |
|----------------|-----------------|
| Human notices red CI / alert | Daemon receives the same signal |
| Paste logs into a chat window | Coding agent runs with evidence + repo access |
| Manual PR, promote, or rollback | **Fix** / **Promote** / **Revert** under policy |
| Tribal “who owns this at 2am?” | Audit trail + backlog when it can't finish |

This is not “replace your pipeline with a chatbot.” It is **policy-gated automation on top of the delivery system you already run**  -  so routine, shaped failures drain without a human in the loop, and novel work stays with humans.

| This is for | This is not for |
|-------------|-----------------|
| Routine failures (red build, failed smoke, SLO breach) | Novel feature design / architecture debates |
| Teams that already trust CI + GitOps | Replacing pipelines with free-form agent chat |
| Deciding *whether* an agent may touch a repo | Prompt-only “vibes” without gates |

---

## How it works

| Signal | Action |
|--------|--------|
| **CI fail** | **Fix**  -  coding-agent subagent (run URL / logs as evidence) |
| **DEV smoke fail** | **Fix**  -  probe logs → subagent |
| **DEV smoke pass** | **Promote**  -  fast-forward only `develop` → `main` |
| **PROD p95 / error-rate breach** | **Revert**  -  `git revert` on `main` |

Green CI → DEV stays your GitOps path. The agent does not invent deploys; it reacts to gates you already trust.

More detail: [docs/architecture.md](docs/architecture.md).

---

## Ops console

Embedded console at `/` (API under `/api/*`).

![Console overview](docs/assets/screenshots/console-overview.jpg)

![Repos](docs/assets/screenshots/console-repos.jpg)

![Manual Fix / Promote / Revert](docs/assets/screenshots/console-actions.jpg)

---

## Quick start

**1. Config**  -  copy [`config.example.yaml`](config.example.yaml), set your repos and agent provider:

```yaml
repos:
  - name: my-service
    github: your-org/my-service
    gates: [ci]   # add dev-smoke, prod-health when ready

agent:
  provider: claude   # claude | codex | cursor
```

**2. Secrets** (env):

```sh
export GITHUB_TOKEN=...              # or GitHub App (preferred)
export GITHUB_WEBHOOK_SECRET=...
export XDL_API_TOKEN=...             # console / API
export ANTHROPIC_API_KEY=...         # if provider: claude
```

**3. Run**  -  Docker (console already embedded):

```sh
docker run --rm -p 8080:8080 \
  -v "$PWD/config.yaml:/etc/xdlc-agent/config.yaml:ro" \
  -e GITHUB_TOKEN -e GITHUB_WEBHOOK_SECRET -e XDL_API_TOKEN -e ANTHROPIC_API_KEY \
  ghcr.io/xdlc-labs/xdlc-agent:0.0.1-beta.1 \
  daemon --config /etc/xdlc-agent/config.yaml
```

Open http://127.0.0.1:8080/ · point GitHub `workflow_run` webhooks at `/webhooks/github`.

**Helm** (single replica  -  audit DB is single-writer):

```sh
helm install xdlc-agent deploy/helm/xdlc-agent \
  --set image.tag=0.0.1-beta.1 \
  --set existingSecret=xdlc-agent-secrets \
  --set-file config=config.yaml
```

From source: `go build -o bin/xdlc-agent ./cmd/xdlc-agent` then `./bin/xdlc-agent daemon --config config.yaml`.

---

## What you bring

The daemon is the loop. You still need:

1. CI that fails clearly on your integration branch  
2. Deploy path green build → DEV (usually GitOps)  
3. Optional smoke + Prometheus for promote / revert gates  
4. A coding-agent CLI on `PATH` (`claude`, `codex`, or `cursor-agent`)

---

## Benchmarks

Hot-path microbenchmarks (`make bench`). Numbers from a recent laptop run (AMD Ryzen 5 PRO 5650U, linux/amd64)  -  re-run locally for your machine.

| Benchmark | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| `orchestrator.Decide` | ~31 | 0 | 0 |
| `ratelimit.Allow` | ~79 | 0 | 0 |
| `validate.Config` (3 repos) | ~431 | 16 | 1 |
| `subagent.FixPrompt` | ~8.6k | ~10k | 13 |
| `store.Append` | ~27k | ~23k | 70 |

End-to-end Fix time is dominated by the coding agent (minutes), not Go decision latency. Policy (`Decide`) is allocation-free and cheap enough to sit on every webhook.

```sh
make bench   # go test -bench=. -benchmem
make test
```

---

## Contribute

- [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md)
- [docs/SECURITY.md](docs/SECURITY.md)
- [docs/CODE_OF_CONDUCT.md](docs/CODE_OF_CONDUCT.md)
- [docs/CHANGELOG.md](docs/CHANGELOG.md)

## License

[MIT](LICENSE) © xdlc contributors
