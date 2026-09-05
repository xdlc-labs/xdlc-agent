# xdlc console (`ui/`)

Ops UI for **xdlc** / `xdlc-agent`. Full guide: [docs/console.md](../docs/console.md).

```sh
# daemon on :8080 first — without one, every panel is an empty shell
# behind a "degraded" banner (there are no mocks/fixtures)
cd ui
bun install
bun run dev      # http://127.0.0.1:5173 — proxies /api → :8080
bun run build
bun run lint
```

Stack: React, Vite, `@tanstack/react-router` (client-side only — no SSR,
no Node server bundle), Tailwind. `vite build` emits a static `dist/`
that the daemon embeds. No third-party app-builder SDKs.
