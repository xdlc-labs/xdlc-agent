# Ops console embed

`dist/` is the built UI (`ui/`), committed so `go build` / `go run` from
source ship the console without a bun toolchain. `embed.go` mounts it at
`/` when `dist/index.html` exists; with an empty `dist/` the daemon stays
API-only.

Refresh after changing `ui/`:

```sh
make ui   # bun install + vite build, then copies ui/dist/ into dist/
```

`deploy/Dockerfile` and the release workflow rebuild it the same way, so a
stale committed copy only affects local source builds.
