import { describe, it, expect, beforeEach } from "vitest";
import { nav } from "./nav.svelte";

// nav is a singleton; reset the pieces these tests touch so order can't matter.
beforeEach(() => {
  nav.lens = "list";
  nav.focusedTerm = "";
  nav.overlay = null;
});

describe("nav.cycleLens", () => {
  it("cycles list → kanban → grid → list (the V shortcut)", () => {
    expect(nav.lens).toBe("list");
    nav.cycleLens();
    expect(nav.lens).toBe("kanban");
    nav.cycleLens();
    expect(nav.lens).toBe("grid");
    nav.cycleLens();
    expect(nav.lens).toBe("list");
  });
});

describe("nav help overlay", () => {
  it("opens and closes the help overlay", () => {
    nav.openOverlay("help");
    expect(nav.overlay).toBe("help");
    nav.closeOverlay();
    expect(nav.overlay).toBeNull();
  });
});

describe("nav.toggleFocusTerm", () => {
  it("toggles a session's fullscreen terminal on and off", () => {
    nav.toggleFocusTerm("s1");
    expect(nav.focusedTerm).toBe("s1");
    nav.toggleFocusTerm("s1");
    expect(nav.focusedTerm).toBe("");
  });
});
