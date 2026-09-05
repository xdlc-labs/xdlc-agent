# Contributing

## Build & test

```sh
make build
make test
make lint   # needs golangci-lint v2.13.2
```

Ops console:

```sh
cd ui && bun install && bun run lint && bun run test && bun run build
```

Do not run bare `go test ./...` after `bun install` — `ui/node_modules` can ship Go files. Use `make test`.

Docs: [Getting started](docs/getting-started.md) · [Production loop](docs/production-loop.md). Console **Docs** nav after `bun run build` (syncs `docs/` → UI).


```sh
export CURSOR_API_KEY="$cursor_agent_key"   # or ANTHROPIC_API_KEY / OPENAI_API_KEY
./bin/xdlc demo --provider cursor --scenario all
```

Console Settings can also store provider + key in localStorage (Manual Fix only).

## PRs

- One logical change per PR; add tests with behavior changes
- Keep vendor-specific I/O behind existing interfaces (`gate.Gate`, `subagent.Runner`, …)
- Be respectful — [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
