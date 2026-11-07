import { describe, it, expect, beforeEach } from "vitest";
import { nav } from "./nav.svelte";

// nav is a singleton; reset the pieces these tests touch so order can't matter.
beforeEach(() => {
  nav.focusedTerm = "";
  nav.overlay = null;
  nav.triage = "";
  nav.sidebarOpen = true;
  nav.scoped = false;
  nav.project = "";
  nav.view = "cockpit";
});

describe("nav help overlay", () => {
  it("opens and closes the help overlay", () => {
    nav.openOverlay("help");
    expect(nav.overlay).toBe("help");
    nav.closeOverlay();
    expect(nav.overlay).toBeNull();
  });
});

describe("nav.openSession", () => {
  it("openSession on the open session keeps it open", () => {
    nav.openSession("s1");
    nav.openSession("s1");
    expect(nav.focusedTerm).toBe("s1");
    expect(nav.selectedId).toBe("s1");
  });
  it("returning focus to the sidebar keeps the session open", () => {
    nav.openSession("s1");
    nav.sidebarOpen = false;
    nav.focusSessions();
    expect(nav.focusedTerm).toBe("s1");
    expect(nav.sidebarOpen).toBe(true);
    expect(nav.sidebarTab).toBe("sessions");
  });
  it("leaves the project scope alone and returns to the cockpit", () => {
    nav.goCockpit("web");
    nav.goDetail("web");
    nav.openSession("s1");
    expect(nav.view).toBe("cockpit");
    expect(nav.scoped).toBe(true);
    expect(nav.project).toBe("web");
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
