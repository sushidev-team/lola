// Entry point for the dev-only preview page. See Preview.svelte.
import { mount } from "svelte";
import "./app.css";
import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/500.css";
import "@fontsource/jetbrains-mono/700.css";
import Preview from "./Preview.svelte";

mount(Preview, { target: document.getElementById("app")! });
