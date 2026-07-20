import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/500.css";
import "@fontsource/jetbrains-mono/700.css";
import "./app.css";
import { mount } from "svelte";
import App from "./App.svelte";
import { appearance } from "$lib/theme-runtime.svelte";

// Paint the theme before mounting. `init()` synchronously writes the tokens,
// data-theme and color-scheme of the last flavor this app painted (cached in
// localStorage, falling back to the compiled default), then asks config.toml
// over the Wails bridge and repaints only if the two disagree.
//
// Deliberately NOT awaited. index.html is a static file — main.go serves it
// with a plain AssetFileServerFS and there is no templating step anywhere — so
// nothing can inject the flavor into the document ahead of JS, and the config
// read is a real IPC round-trip. Awaiting it would turn a possible one-repaint
// colour change into a guaranteed blank window of unbounded length. The
// localStorage cache is what removes the flash on every launch after the first;
// see Appearance.init for why the bridge answer still wins.
void appearance.init();

const app = mount(App, { target: document.getElementById("app")! });

export default app;
