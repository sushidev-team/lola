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

  // The grid lens renders no detail panel, so focusing from it must leave the
  // grid — otherwise focusedTerm is set with no terminal mounted to own the
  // keyboard, and the global handler's early return wedges every shortcut.
  it("leaves the grid lens when focusing a terminal", () => {
    nav.setLens("grid");
    nav.toggleFocusTerm("s1");
    expect(nav.lens).toBe("list");
    expect(nav.focusedTerm).toBe("s1");
  });
});

describe("nav.setLens", () => {
  it("clears a focused terminal when switching INTO the grid", () => {
    nav.setLens("list");
    nav.toggleFocusTerm("s1");
    expect(nav.focusedTerm).toBe("s1");
    nav.setLens("grid");
    expect(nav.focusedTerm).toBe("");
  });

  it("leaves a focused terminal alone for the other lenses", () => {
    nav.setLens("list");
    nav.toggleFocusTerm("s1");
    nav.setLens("kanban");
    expect(nav.focusedTerm).toBe("s1");
  });

  it("cycleLens routes through setLens, so grid still clears the focus", () => {
    nav.setLens("kanban");
    nav.focusedTerm = "s1";
    nav.cycleLens(); // kanban → grid
    expect(nav.lens).toBe("grid");
    expect(nav.focusedTerm).toBe("");
  });
});
