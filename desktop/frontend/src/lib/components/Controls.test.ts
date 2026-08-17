import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/svelte";
import Checkbox from "./Checkbox.svelte";
import SelectHarness from "./SelectHarness.test.svelte";

// The whole point of these two components is that the app looks the SAME on
// every machine: a native checkbox and a native <select> are drawn by AppKit,
// so their art follows the user's macOS version rather than this repo. Losing
// `appearance-none` — or drawing the tick/caret with a pseudo-element WebKit
// declines to render — silently hands the control back to the OS, which is
// invisible in CI and only shows up as "my colleague's form looks different".
describe("Checkbox", () => {
  beforeEach(cleanup);

  it("drops the native widget and draws its own box", () => {
    render(Checkbox, { "aria-label": "Enabled" });
    expect(screen.getByRole("checkbox", { name: "Enabled" }).className).toContain("appearance-none");
  });

  it("draws the tick as a real element, not a pseudo-element", () => {
    const { container } = render(Checkbox, { "aria-label": "Enabled", checked: true });
    // A sibling <svg> in currentColor — reachable in every engine. `peer-checked`
    // is what reveals it, so the class has to survive alongside the base opacity.
    const tick = container.querySelector("svg");
    expect(tick).not.toBeNull();
    expect(tick!.getAttribute("class")).toContain("peer-checked:opacity-100");
  });

  it("passes a call site's props through to the input", () => {
    render(Checkbox, { "aria-label": "Enabled", checked: true, disabled: true });
    const box = screen.getByRole("checkbox", { name: "Enabled" });
    expect(box).toBeChecked();
    expect(box).toBeDisabled();
  });
});

describe("Select", () => {
  beforeEach(cleanup);

  it("drops the native menulist and keeps the value", () => {
    render(SelectHarness, { value: "b" });
    const field = screen.getByRole("combobox", { name: "Flavor" });
    expect(field.className).toContain("appearance-none");
    expect((field as HTMLSelectElement).value).toBe("b");
  });

  it("draws its own caret in the field's own colour", () => {
    const { container } = render(SelectHarness);
    const caret = container.querySelector("svg");
    expect(caret).not.toBeNull();
    // Never clickable: the click has to reach the select underneath it.
    expect(caret!.getAttribute("class")).toContain("pointer-events-none");
    expect(caret!.getAttribute("stroke")).toBe("currentColor");
  });
});

// The components only hold the line while every call site uses them. A raw
// control anywhere else is OS-drawn again — and it looks perfectly fine on
// whichever macOS the author happened to be running.
describe("no view ships a native control", () => {
  // Vite's glob rather than node:fs — the frontend carries no @types/node, and
  // `npm run check` fails on an import of one.
  const sources = import.meta.glob("../../**/*.svelte", { query: "?raw", import: "default", eager: true }) as Record<
    string,
    string
  >;
  const OWN = /\/(Checkbox|Select)\.svelte$/;

  // A glob that resolves to nothing would pass every assertion below forever.
  it("scans the app's components and views", () => {
    expect(Object.keys(sources).length).toBeGreaterThan(30);
  });

  it.each([
    ['<input type="checkbox">', /type="checkbox"/],
    ["<select>", /<select[\s>]/],
  ])("uses the component instead of a raw %s", (_what, pattern) => {
    const offenders = Object.entries(sources)
      .filter(([path]) => !OWN.test(path))
      .filter(([, src]) => pattern.test(src))
      .map(([path]) => path);
    expect(offenders).toEqual([]);
  });
});
