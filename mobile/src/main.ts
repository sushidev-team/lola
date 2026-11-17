import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/500.css";
import "@fontsource/jetbrains-mono/700.css";
import "./app.css";
import { mount } from "svelte";
import App from "./App.svelte";
import { appearance } from "$lib/theme-runtime.svelte";
import { setPushSource } from "$lib/pushsource";
import { store } from "$lib/store.svelte";
import { setTouchSurface } from "$lib/touchsurface";
import { setWidgetsUnavailable } from "$lib/widgetsoff";
import { bridge, useTransport } from "./wailsshim/bridge";
import { connection } from "@mobile/lib/connection.svelte";
import { copy } from "@mobile/lib/copy";
import { seedDeskPrefs } from "@mobile/lib/deskprefs";
import { mobilePush } from "@mobile/lib/pushsource";
import { installResume } from "@mobile/lib/resume";
import { installTiers } from "@mobile/lib/tiers.svelte";
import { phoneTouchSurface } from "@mobile/lib/touchsurface";

// Painted before mounting and not awaited: the phone has no backend to ask, so
// the theme resolves from the cached flavor alone, and an await here would turn a
// possible one-repaint colour change into a guaranteed blank screen.
void appearance.init();

// Shared components read their stored preferences on mount; the phone's defaults
// must be in place first.
seedDeskPrefs();

// The offline snapshot keeps every session, not only the ssh ones the desktop's
// store keeps by default.
store.retainAll = true;

// The daemon never sends a widget's page to a phone.
setWidgetsUnavailable(true);
setTouchSurface(phoneTouchSurface);

// The daemon clears `force` on every remote request, so a dirty worktree is removed on the Mac.
store.noForceKill = copy.notices.dirtyKill.body;

// Pushed state replaces the desktop stores' polling; installed before mount so no
// store ever starts a poll the source would have made redundant.
setPushSource(mobilePush);

// The shared store subscribes to the pushed `daemon:*` events before any screen
// mounts, so a first snapshot that lands early is kept. Idempotent.
store.start();

// The one transport, created once and handed to every consumer: the shim turns
// service calls and pushed topics into frames on it, the connection store drives
// the connect state, and resume re-establishes the streams on every new epoch.
// The import is dynamic and survivable: a plain browser session has no Capacitor
// bridge, and the UI must still render and say that nothing is available.
async function installTransport(): Promise<void> {
  try {
    const { makeTransport } = await import("./wailsshim/capacitorchannel");
    const transport = makeTransport();
    useTransport(transport);
    connection.useTransport(transport);
    installResume(transport);
  } catch (err) {
    console.warn("lola: native transport unavailable; the app will run offline", err);
  }
}
void installTransport();

// Every epoch's `ready` replaces the device's tiers.
installTiers();

bridge.start();

const app = mount(App, { target: document.getElementById("app")! });

export default app;
