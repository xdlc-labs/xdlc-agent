import viteReact from "@vitejs/plugin-react";
import { defineConfig } from "vite";
import tsConfigPaths from "vite-tsconfig-paths";

// Separate from vite.config.ts on purpose: the app config wires in the
// TanStack Router file-based route generator and Tailwind, neither of
// which vitest needs — keeping this lean avoids slowing/complicating
// the test transform pipeline.
export default defineConfig({
  plugins: [tsConfigPaths({ projects: ["./tsconfig.json"] }), viteReact()],
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    css: false,
  },
});
