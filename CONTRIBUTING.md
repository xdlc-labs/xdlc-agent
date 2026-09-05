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

## PRs

- One logical change per PR; add tests with behavior changes
- Keep vendor-specific I/O behind existing interfaces (`gate.Gate`, `subagent.Runner`, …)
- Be respectful — [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
