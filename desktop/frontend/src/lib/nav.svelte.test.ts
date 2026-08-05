import { describe, it, expect, beforeEach } from "vitest";
import { nav } from "./nav.svelte";

// nav is a singleton; reset the pieces these tests touch so order can't matter.
beforeEach(() => {
  nav.lens = "list";
  nav.focusedTerm = "";
  nav.overlay = null;
  nav.triage = "";
  nav.sidebarOpen = true;
  nav.scoped = false;
  nav.project = "";
  nav.view = "cockpit";
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

describe("nav.toggleProjectScope", () => {
  it("scopes to a project, then clears the scope on a second click", () => {
    nav.toggleProjectScope("nori");
    expect(nav.scoped).toBe(true);
    expect(nav.project).toBe("nori");
    nav.toggleProjectScope("nori");
    expect(nav.scoped).toBe(false);
    expect(nav.project).toBe("");
  });

  it("switches scope rather than clearing when a DIFFERENT project is clicked", () => {
    nav.toggleProjectScope("nori");
    nav.toggleProjectScope("okane");
    expect(nav.scoped).toBe(true);
    expect(nav.project).toBe("okane");
  });

  // `scoped` outlives goDetail, so the row is still drawn active on the project
  // hub. A click there must NAVIGATE to that project's sessions — clearing would
  // send the user somewhere they never asked for and read as a swallowed click.
  it("navigates instead of clearing when the cockpit isn't the current view", () => {
    nav.toggleProjectScope("nori");
    nav.goDetail("nori");
    nav.toggleProjectScope("nori");
    expect(nav.view).toBe("cockpit");
    expect(nav.scoped).toBe(true);
    expect(nav.project).toBe("nori");
  });

  // Same rule the "All" breadcrumb follows: dropping the project scope must not
  // silently widen the status filter back to everything.
  it("keeps the triage filter when it clears the scope", () => {
    nav.setTriage("Needs You");
    nav.toggleProjectScope("nori");
    nav.toggleProjectScope("nori");
    expect(nav.scoped).toBe(false);
    expect(nav.triage).toBe("Needs You");
  });
});

describe("nav.toggleSidebar", () => {
  it("toggles the sidebar on and off (the b shortcut)", () => {
    expect(nav.sidebarOpen).toBe(true);
    nav.toggleSidebar();
    expect(nav.sidebarOpen).toBe(false);
    nav.toggleSidebar();
    expect(nav.sidebarOpen).toBe(true);
  });
});

describe("nav.setTriage", () => {
  it("sets and clears the triage filter", () => {
    nav.setTriage("Needs You");
    expect(nav.triage).toBe("Needs You");
    nav.setTriage("");
    expect(nav.triage).toBe("");
  });

  // Project scope and triage COMPOSE: scoping to a project must not silently
  // widen the list back to every status, or "show me what needs me" evaporates
  // the moment you click a project.
  it("survives goCockpit — scope and filter compose", () => {
    nav.setTriage("Fixing");
    nav.goCockpit("nori");
    expect(nav.scoped).toBe(true);
    expect(nav.project).toBe("nori");
    expect(nav.triage).toBe("Fixing");
    nav.goCockpit("");
    expect(nav.triage).toBe("Fixing");
  });
});
