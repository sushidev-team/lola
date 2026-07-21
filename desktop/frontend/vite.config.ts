/// <reference types="vitest/config" />
import { defineConfig, type Plugin } from "vite";
import { fileURLToPath } from "node:url";
import { writeFileSync } from "node:fs";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import tailwindcss from "@tailwindcss/vite";
import wails from "@wailsio/runtime/plugins/vite";

const r = (p: string) => fileURLToPath(new URL(p, import.meta.url));

// A production `vite build` empties dist/ (emptyOutDir defaults true), which
// also deletes dist/.gitkeep — the file that keeps the directory tracked in git
// (see .gitignore) so `//go:embed all:frontend/dist` in desktop/main.go still
// resolves on a clean checkout where the frontend hasn't been built (CI's Go
// test/vet jobs). Re-create it after the bundle is written so the build never
// leaves a spurious deletion in the working tree.
const keepDistGitkeep = (): Plugin => ({
  name: "lola-keep-dist-gitkeep",
  apply: "build",
  closeBundle() {
    writeFileSync(r("./dist/.gitkeep"), "");
  },
});

// https://vitejs.dev/config/
export default defineConfig(({ mode }) => ({
  resolve: {
    alias: {
      $lib: r("./src/lib"),
      "@bindings": r("./bindings/github.com/sushidev-team/lola"),
    },
    // Under Vitest, force Svelte's *client* build so component render tests can
    // mount() in jsdom (the default Node resolution pulls index-server.js, whose
    // mount() throws). Left untouched for the real webview build.
    ...(mode === "test" ? { conditions: ["browser"] } : {}),
  },
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  plugins: [tailwindcss(), svelte(), wails("./bindings"), keepDistGitkeep()],
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.{test,spec}.{ts,js}"],
  },
}));
