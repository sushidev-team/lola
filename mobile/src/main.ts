import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/500.css";
import "@fontsource/jetbrains-mono/700.css";
import "./app.css";
import { mount } from "svelte";
import App from "./App.svelte";
import { appearance } from "$lib/theme-runtime.svelte";
import { bridge, useTransport } from "./wailsshim";
import { connection } from "@mobile/lib/connection.svelte";

// ---------------------------------------------------------------------------
// Theme
//
// Painted before mounting, exactly as the desktop does: `init()` synchronously
// writes the tokens, data-theme and color-scheme of the last flavor this app
// painted (cached in localStorage, falling back to the compiled default), then
// asks the backend and repaints only if the two disagree.
//
// On mobile there is no backend to ask — the shim deliberately does not export
// ConfigService.GetTheme, and theme-runtime feature-probes for it and degrades
// to the cache. So this resolves from localStorage alone, which is both correct
// and the right product answer: the phone's flavor is the phone's, and no
// remote command could read the Mac's `[ui].theme` anyway.
//
// Deliberately NOT awaited, for the desktop's reason plus one of its own: on a
// cold launch the WebView paints the bundle straight out of the app container,
// so putting an await in front of the mount turns a possible one-repaint colour
// change into a guaranteed blank screen of unbounded length.
// ---------------------------------------------------------------------------
void appearance.init();

// ---------------------------------------------------------------------------
// The transport, created ONCE and handed to both consumers.
//
// The shim turns `DaemonService.Sessions()` into a `req` frame on it; the
// connection store drives the connect screen and the pane streams from it. They
// must be the same instance — two would authenticate twice, hold two of the
// daemon's eight connection slots, and let one show a connected UI over the
// other's dead pipe.
//
// The import is dynamic and its failure is survivable on purpose. The module
// pulls in @capacitor/core and the native plugin, neither of which exists in a
// plain `npm run dev` browser session; without the guard the whole app would
// fail to boot there, and the browser session is how most of this UI is built.
// With it, the UI renders and the connect screen says plainly that no transport
// is available — which is also what a device build shows if the plugin has not
// been synced.
// ---------------------------------------------------------------------------
async function installTransport(): Promise<void> {
  try {
    const { makeTransport } = await import("./wailsshim/capacitorchannel");
    const transport = makeTransport();
    useTransport(transport);
    connection.useTransport(transport);
  } catch (err) {
    console.warn("lola: native transport unavailable; the app will run offline", err);
  }
}
void installTransport();

// Start the loop that synthesises `daemon:sessions` / `daemon:projects` /
// `daemon:status` / `daemon:alive` / `daemon:pusherr` from polled requests, so
// the shared store receives the same events it does on the desktop. Safe to
// start before a transport is installed and before one connects: with none,
// every tick simply reports the daemon as unreachable, which is the state the
// connect screen renders.
bridge.startPolling();

const app = mount(App, { target: document.getElementById("app")! });

export default app;
