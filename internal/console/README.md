# Ops console embed

Built UI assets land in `dist/` (must include `index.html`).

Local / CI without a UI build: leave `dist/` empty aside from `.gitkeep`
so `go build` works; the daemon stays API-only and skips mounting `/`.

```sh
cd ui && bun install && bun run build
# vite build writes ui/dist/ — copy it into the embed path:
cp -r dist/. ../internal/console/dist/
```

`deploy/Dockerfile` does the bun build + copy before compiling the agent.
