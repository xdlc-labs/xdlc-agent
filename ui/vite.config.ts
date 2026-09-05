import path from "node:path";
import { fileURLToPath } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import viteReact from "@vitejs/plugin-react";
import { defineConfig } from "vite";
import tsConfigPaths from "vite-tsconfig-paths";

const uiRoot = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(uiRoot, "..");

// Plain client-side SPA build: no SSR, no server bundle. `vite build`
// produces a standard dist/ (index.html + hashed assets) that
// internal/console/embed.go serves as static files with an SPA
// fallback to index.html. Auth (cookies/OIDC) and data fetching
// (/api/* via react-query) are handled elsewhere — this UI doesn't
// need a Node server of its own.
export default defineConfig({
  server: {
    port: 5173,
    strictPort: false,
    fs: { allow: [repoRoot] },
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8080",
        changeOrigin: true,
      },
    },
  },
  plugins: [
    tsConfigPaths({ projects: ["./tsconfig.json"] }),
    tailwindcss(),
    tanstackRouter({
      target: "react",
      autoCodeSplitting: true,
      // Colocated *.test.tsx files under src/routes/ (see e.g.
      // actions.test.tsx) aren't route modules — keep the generator
      // from treating them as one.
      routeFileIgnorePattern: "\\.test\\.tsx$",
    }),
    viteReact(),
  ],
});
