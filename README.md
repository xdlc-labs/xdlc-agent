# xdlc-agent

Self-hosted daemon for an agentic delivery loop: watch CI / DEV smoke / PROD health, then **Fix**, **Promote**, or **Revert** under policy. Bring your own coding agent (`claude` / `codex` / `cursor`). MIT. No SaaS in the loop.

[![ci](https://github.com/xdlc-labs/xdlc-agent/actions/workflows/ci.yml/badge.svg)](https://github.com/xdlc-labs/xdlc-agent/actions/workflows/ci.yml)
[![license: mit](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

## Quick start

```sh
git clone https://github.com/xdlc-labs/xdlc-agent.git
cd xdlc-agent
cp config.example.yaml config.yaml   # edit for your repos
go build -o bin/xdlc-agent ./cmd/xdlc-agent
./bin/xdlc-agent validate --config config.yaml
./bin/xdlc-agent daemon --config config.yaml
```

Docker / Helm:

```sh
docker run --rm -p 8080:8080 -v "$PWD/config.yaml:/etc/xdlc-agent/config.yaml:ro" \
  ghcr.io/xdlc-labs/xdlc-agent:2.0.0 daemon --config /etc/xdlc-agent/config.yaml

helm install xdlc-agent deploy/helm/xdlc-agent \
  --set image.tag=2.0.0 \
  --set-file config=config.yaml
```

Ops console is embedded at `/` when the daemon serves HTTP (build UI with `cd ui && bun install && bun run build` before `go build`, or use the published image).

## How it works

| Signal | Action |
|--------|--------|
| CI fail / DEV smoke fail | **Fix** — coding-agent subagent |
| DEV smoke pass | **Promote** — fast-forward `develop` → `main` |
| PROD SLO breach | **Revert** — `git revert` on `main` |

Details: [docs/architecture.md](docs/architecture.md). Config reference: [config.example.yaml](config.example.yaml).

## Develop

```sh
make test
make build
```

See [CONTRIBUTING.md](CONTRIBUTING.md). Security reports: [SECURITY.md](SECURITY.md).

## Layout

```
cmd/xdlc-agent/     CLI + daemon
internal/           orchestrator, gates, dispatch, API, store, …
ui/                 ops console (embedded)
deploy/helm/        agent Helm chart
config.example.yaml sample config
```
