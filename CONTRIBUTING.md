# Contributing

## Naming

| Name | What |
|---|---|
| **xdlc** | Product / repo / GHCR image (`ghcr.io/<you>/xdlc`) |
| **xdlc-agent** | Go binary, Helm chart, k8s Deployment |

Keep that split in docs and code comments.

## Dev setup (Go)

```sh
make build
make test          # race tests; skips ui/node_modules
make lint          # golangci-lint ./...
make validate      # config.example.yaml vs gitops/
test -z "$(gofmt -l cmd internal services)"
```

Do **not** run bare `go test ./...` after `cd ui && bun install` — some
npm packages ship Go sources under `ui/node_modules`. `make test` and CI
already filter those out.

`golangci-lint` (not the archived `golint`) is the linter. Install with
`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2`
(match `.github/workflows/ci.yml`). Config is `.golangci.yml`: standard
preset plus `revive`, `gosec`, `bodyclose`, `noctx`, `unconvert`,
`unparam`, `misspell`, `copyloopvar`, `errorlint`. A `//nolint:gosec`
needs a short comment explaining why it is safe.

## Ops console (`ui/`)

```sh
cd ui
bun install
bun run lint
bun run build
```

See [docs/console.md](docs/console.md). Keep the UI free of third-party
editor SDKs; it is a plain client-side SPA on Vite +
`@tanstack/react-router` (no SSR, no Node server bundle).

## Adding a gate

Implement `internal/gate.Gate` (`Name`, `Trigger`, `Check`). See
`internal/gate/{ci,smoke,prodhealth}.go` for the shape: favor
constructor-injected functions (`GetStatus`, `Query`, ...) over baking in
a concrete client, so the gate stays unit-testable without a live cluster.
Wire it into `cmd/xdlc-agent` and document it in `docs/gates.md`.

## Adding a coding-agent provider

`internal/subagent`'s `providerDefaults` maps a `Provider` to its
default binary and headless invocation shape. To add one beyond
Claude/Codex/Cursor, add an entry there with the CLI's non-interactive
flags and a `promptPlaceholder` in the right spot, then document it in
[docs/architecture.md](docs/architecture.md) and the root README.

## PRs

- Keep gate/orchestrator logic decoupled from any specific vendor
  (GitHub, ArgoCD, Prometheus, or a coding-agent CLI) behind the
  interfaces already in `internal/`, so forks can swap any of them out.
- One logical change per PR. Add/adjust tests alongside.
- `BACKLOG.md`-affecting behavior changes should update `docs/runbook.md`
  too: it's the doc that explains what the agent does and why.

## Code of Conduct

Be respectful. Standard Contributor Covenant applies (see
`CODE_OF_CONDUCT.md`).
