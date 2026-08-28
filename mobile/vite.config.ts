/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import { fileURLToPath } from "node:url";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import tailwindcss from "@tailwindcss/vite";

const r = (p: string) => fileURLToPath(new URL(p, import.meta.url));

// The desktop app's frontend, reused rather than copied. Everything mobile
// borrows lives under its src/lib; nothing outside that directory is imported,
// and no file under desktop/ is ever written by this project.
const desktop = r("../desktop/frontend");

// ---------------------------------------------------------------------------
// THE ALIAS TABLE
//
// This is the single mechanism the whole reuse bet rests on, so it is worth
// stating plainly what it does and why the entries are in this order.
//
// The desktop's components import three families of specifier:
//
//   $lib/...                    its own library modules (pure logic + Svelte)
//   @bindings/internal/protocol the Wails-generated daemon DTOs
//   @bindings/desktop           the Wails-generated SERVICE call surface
//   @wailsio/runtime            the Wails event bus (Events.On/Emit)
//
// The first two are free. `$lib` is just a directory, so pointing it at the
// desktop's tree makes those modules compile here unchanged. The generated DTO
// modules contain nothing but `export interface` declarations — no classes, no
// imports, no runtime code at all — so they are erased at build time and can be
// aliased straight at the real generated files. A DTO is a shape, and the shape
// is identical on both platforms because the same daemon produces it.
//
// The last two are the ones that would otherwise force a fork. `@bindings/
// desktop` and `@wailsio/runtime` are RUNTIME modules that reach the Go side
// through a Wails IPC bridge that does not exist inside a Capacitor WebView.
// Pointing them at src/wailsshim swaps the backend out from under 26 components
// that never learn about it: they keep calling `DaemonService.Sessions()` and
// `Events.On("daemon:sessions", ...)`, and the shim turns each into a frame on
// the daemon's remote listener and each incoming frame back into the event they
// already subscribe to.
//
// ORDER MATTERS. Vite resolves array-form aliases in order and matches on
// prefix, so the specific entries must precede the general ones: without the
// first entry, `@bindings/desktop/models` would be captured by the
// `@bindings/desktop` entry and resolve to the shim, which exports services
// rather than model types.
// ---------------------------------------------------------------------------
const alias = [
  // Generated DTO types. Type-only, therefore free — see above.
  { find: /^@bindings\/desktop\/models$/, replacement: `${desktop}/bindings/github.com/sushidev-team/lola/desktop/models.ts` },
  { find: /^@bindings\/internal\/protocol$/, replacement: `${desktop}/bindings/github.com/sushidev-team/lola/internal/protocol/index.ts` },

  // The two runtime swaps. Everything reusable resolves its backend here.
  { find: /^@bindings\/desktop$/, replacement: r("./src/wailsshim/desktop.ts") },
  { find: /^@wailsio\/runtime$/, replacement: r("./src/wailsshim/runtime.ts") },

  // Anything else under @bindings falls through to the real generated tree.
  { find: /^@bindings\//, replacement: `${desktop}/bindings/github.com/sushidev-team/lola/` },

  // The shared component library itself.
  { find: /^\$lib\//, replacement: `${desktop}/src/lib/` },
  { find: /^\$lib$/, replacement: `${desktop}/src/lib` },

  // Mobile's own additions, so a mobile view can say `@mobile/wire` rather than
  // counting `../..` segments out of a nested views directory.
  { find: /^@mobile\//, replacement: r("./src/") },
];

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

  plugins: [tailwindcss(), svelte()],

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
