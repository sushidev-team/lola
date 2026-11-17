/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import { fileURLToPath } from "node:url";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import tailwindcss from "@tailwindcss/vite";

const r = (p: string) => fileURLToPath(new URL(p, import.meta.url));

// The desktop app's frontend, reused rather than copied. Mobile imports its
// src/lib and generated bindings; phone-only behaviour enters desktop code only
// through its seams (pushsource, composerattach, widgetsoff, store flags).
const desktop = r("../desktop/frontend");

// The alias table. ORDER MATTERS: Vite tries array-form aliases in order and
// matches on prefix, so each specific entry precedes the general one it would
// otherwise be captured by (`@bindings/desktop/models` before `@bindings/desktop`).
const alias = [
  // Generated DTO types: type-only, so the real files.
  { find: /^@bindings\/desktop\/models$/, replacement: `${desktop}/bindings/github.com/sushidev-team/lola/desktop/models.ts` },
  { find: /^@bindings\/internal\/protocol$/, replacement: `${desktop}/bindings/github.com/sushidev-team/lola/internal/protocol/index.ts` },

  // The runtime swaps: services and the event bus resolve to the shim.
  { find: /^@bindings\/desktop$/, replacement: r("./src/wailsshim/desktop.ts") },
  { find: /^@wailsio\/runtime$/, replacement: r("./src/wailsshim/runtime.ts") },
  // One service module imported directly gets the shim's module of that name.
  { find: /^@bindings\/desktop\/([a-z]+)service$/, replacement: `${r("./src/wailsshim/services")}/$1.ts` },

  // Anything else under @bindings falls through to the real generated tree.
  { find: /^@bindings\//, replacement: `${desktop}/bindings/github.com/sushidev-team/lola/` },

  // The shared component library itself.
  { find: /^\$lib\//, replacement: `${desktop}/src/lib/` },
  { find: /^\$lib$/, replacement: `${desktop}/src/lib` },

  // Mobile's own tree.
  { find: /^@mobile\//, replacement: r("./src/") },
];

// Every static import of the desktop LiveTerminal resolves to a wrapper that
// loads it on first render, so xterm stays out of each segment's static graph
// (scripts/bundle-budget.mjs checks it). Not under Vitest: tests mock xterm and
// mount the real component synchronously.
const lazyWrapper = r("./src/lib/components/LazyLiveTerminal.svelte");
const lazyLiveTerminal = () => ({
  name: "lola-lazy-live-terminal",
  enforce: "pre" as const,
  resolveId(source: string, importer?: string) {
    if (!/(^|[\\/])LiveTerminal\.svelte$/.test(source)) return null;
    if (!importer || importer.split("?")[0] === lazyWrapper) return null;
    return lazyWrapper;
  },
});

export default defineConfig(({ mode }) => ({
  resolve: {
    alias,
    // Under Vitest, force Svelte's *client* build so component render tests can
    // mount() in jsdom (the default Node resolution pulls index-server.js, whose
    // mount() throws). Copied from the desktop config for the same reason, and
    // it is just as load-bearing here.
    ...(mode === "test" ? { conditions: ["browser"] } : {}),
  },

  // Capacitor serves the bundle from the app container at capacitor://localhost/,
  // i.e. from the scheme ROOT. `base` therefore stays "/" — a relative "./" base
  // is the usual reflex for a file-served bundle and it breaks nested CSS url()
  // references, which is how the vendored terminal font would go missing.
  base: "/",

  build: {
    // MUST equal capacitor.config.ts's webDir: `cap sync` copies this directory
    // verbatim into ios/App/App/public. If the two disagree the app ships the
    // previous build, silently.
    outDir: "dist",
    emptyOutDir: true,
    // Safari on iOS 15 is the deployment floor Capacitor 8 sets. Anything newer
    // in the output is a blank screen on a supported device, not a syntax error
    // anyone sees.
    target: "es2020",
    // A packaged app has no network latency to amortise, and one file is easier
    // to reason about when the Web Inspector is the only debugger available.
    assetsInlineLimit: 4096,
    // scripts/bundle-budget.mjs reads dist/.vite/manifest.json to prove that
    // xterm and the diff viewer stay out of the entry chunk's static imports.
    manifest: true,
    rolldownOptions: {
      output: {
        advancedChunks: {
          // A group holds only the modules it names. Pulling in their
          // dependencies would drag the Svelte runtime and the stores the entry
          // shares into the lazy chunk, and the entry would then import it.
          includeDependenciesRecursively: false,
          groups: [
            { name: "terminal", test: /[\\/]node_modules[\\/]@xterm[\\/]/ },
            { name: "diff", test: /DiffView\.svelte|[\\/]diff\.ts$|diffnavigation/ },
          ],
        },
      },
    },
  },

  server: {
    // `cap run ios --live-reload` points the device's WebView at this server on
    // the Mac's LAN address, so it must not be bound to loopback only.
    host: true,
    port: 9246, // deliberately not the desktop's 9245: both can run at once
    strictPort: true,
    fs: {
      // Vite refuses to serve files outside the project root, and every shared
      // component is outside it by design. Without this the dev server 403s on
      // the first `$lib/...` import and the failure reads as a missing file.
      allow: [r("."), desktop],
    },
  },

  plugins: [...(mode === "test" ? [] : [lazyLiveTerminal()]), tailwindcss(), svelte()],

  // The vitest configuration lives here rather than in its own vitest.config.ts
  // so the tests run through the SAME alias table and Svelte plugin the app
  // builds with. A separate config file is a second place for the alias table to
  // be wrong, and an alias that is wrong only under test is the kind of thing
  // that is discovered on a device.
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.{test,spec}.{ts,js}"],
  },
}));
