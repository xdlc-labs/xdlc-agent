import viteReact from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// Separate from vite.config.ts on purpose: the app config wires in the
// TanStack Router file-based route generator and Tailwind, neither of
// which vitest needs — keeping this lean avoids slowing/complicating
// the test transform pipeline.
export default defineConfig({
  // Same `@/` alias as the app build, resolved from tsconfig.json by Vite.
  resolve: { tsconfigPaths: true },
  plugins: [viteReact()],
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    css: false,
  },
});
