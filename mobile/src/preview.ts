// Entry point of the dev-only preview page (spec §5.6).
//
//   preview.html?route=<path>&sheet=<name>&fixture=<name>&theme=<id>&kb=<px>
//
// It wires the real app exactly as `main.ts` does, with a scripted Mac in place
// of the native plugin, then mounts `Preview.svelte`, which holds the real
// `App.svelte` and puts the router where `route` says.
//
// Invariants:
//  - Webview-local state is seeded BEFORE any shared store is imported: the
//    stores read it once, when their module first runs. Every store import below
//    is therefore dynamic and comes after `seedStorage`.
//  - Nothing here reaches a daemon, a Keychain or a camera, and nothing is
//    written outside this page's own storage.
//  - `route`, `sheet`, `theme` and `kb` are read once and never trusted: an
//    unknown value is ignored and the default stands.

import "./previewweb";
import "./app.css";
import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/500.css";
import "@fontsource/jetbrains-mono/700.css";
import { fixtureFor, loadFixture, type Fixture } from "./fixtures";
import { installPreviewPlugin, previewTransport } from "./fixtures/daemon";
import { readParams } from "./fixtures/params";

function seedStorage(entries: Record<string, string>): void {
  try {
    for (const [k, v] of Object.entries(entries)) localStorage.setItem(k, v);
  } catch {
    /* storage unavailable: the shared store starts empty */
  }
}

/** Wires the app's stores to the fixture's scripted Mac, as `main.ts` does to the plugin. */
async function wire(fx: Fixture, theme: string | undefined): Promise<void> {
  installPreviewPlugin(fx);

  const { appearance } = await import("$lib/theme-runtime.svelte");
  const { appearancePreference } = await import("$lib/appearance");
  // A preview never persists: `preview` paints and stops there.
  appearance.preview(appearancePreference({ theme }));

  const { seedDeskPrefs } = await import("@mobile/lib/deskprefs");
  seedDeskPrefs();
  const { store } = await import("$lib/store.svelte");
  store.retainAll = true;
  (await import("$lib/widgetsoff")).setWidgetsUnavailable(true);
  (await import("$lib/touchsurface")).setTouchSurface((await import("@mobile/lib/touchsurface")).phoneTouchSurface);
  store.noForceKill = (await import("@mobile/lib/copy")).copy.notices.dirtyKill.body;
  // The store subscribes to the pushed `daemon:*` events; a screen that mounts after the first
  // snapshot reads it from here.
  store.start();
  const { setPushSource } = await import("$lib/pushsource");
  const { mobilePush } = await import("@mobile/lib/pushsource");
  setPushSource(mobilePush);

  const { bridge, useTransport } = await import("./wailsshim/bridge");
  const { connection } = await import("@mobile/lib/connection.svelte");
  const { installResume } = await import("@mobile/lib/resume");
  const { installTiers } = await import("@mobile/lib/tiers.svelte");
  const transport = previewTransport(fx);
  useTransport(transport);
  connection.useTransport(transport);
  installResume(transport);
  installTiers();
  bridge.start();

  await fx.seed?.();
}

async function main(): Promise<void> {
  const params = readParams(globalThis.location?.search ?? "");
  const name = params.fixture ?? (params.path !== "" ? await fixtureFor(params.path) : null) ?? "busy";
  const fx = await loadFixture(name);
  seedStorage(fx.storage);
  await wire(fx, params.theme);

  const { mount } = await import("svelte");
  const { default: Preview } = await import("./Preview.svelte");
  mount(Preview, { target: document.getElementById("app")!, props: { fixture: fx, path: params.path, kb: params.kb } });
}

void main();
