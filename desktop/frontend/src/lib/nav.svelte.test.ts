import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
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
  nav.projectTab = "overview";
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
  it("returns from project detail without turning it into a session filter", () => {
    nav.goCockpit("web");
    nav.goDetail("web");
    nav.openSession("s1");
    expect(nav.view).toBe("cockpit");
    expect(nav.scoped).toBe(false);
    expect(nav.project).toBe("web");
  });
});

describe("nav.goDetail", () => {
  it("returns from Git to the exact session and scope, including on repeated toggles", () => {
    nav.goCockpit("web");
    nav.openSession("s1", true);
    nav.setSidebarTab("sessions");
    nav.goDetail("web", "git");
    nav.goDetail("other-repo", "git");
    nav.returnFromGit();
    expect(nav.view).toBe("cockpit");
    expect(nav.focusedTerm).toBe("s1");
    expect(nav.project).toBe("web");
    expect(nav.scoped).toBe(true);
    expect(nav.sidebarBrowsing).toBe(true);
    expect(nav.sidebarTab).toBe("sessions");
    nav.openSession("s2");
    nav.goDetail("web", "git");
    nav.returnFromGit();
    expect(nav.focusedTerm).toBe("s2");
  });

  it("returns from the Git tab to overview and from project navigation to its prior picker", () => {
    nav.goDetail("web", "overview");
    nav.projectTab = "git";
    nav.returnFromGit();
    expect(nav.view).toBe("detail");
    expect(nav.projectTab).toBe("overview");
    nav.goPRPicker("web");
    nav.goDetail("other", "git");
    nav.returnFromGit();
    expect(nav.view).toBe("prpicker");
    expect(nav.project).toBe("web");
  });
  it("opens the project workspace without scoping the session list", () => {
    nav.goCockpit("old-project");
    nav.openSession("s1");
    nav.goDetail("nori");
    expect(nav.view).toBe("detail");
    expect(nav.scoped).toBe(false);
    expect(nav.project).toBe("nori");
    expect(nav.sidebarTab).toBe("projects");
    expect(nav.focusedTerm).toBe("");
    expect(nav.projectTab).toBe("overview");
  });

  it("repeated project navigation stays in the workspace", () => {
    nav.goDetail("nori");
    nav.goDetail("nori");
    expect(nav.view).toBe("detail");
    expect(nav.project).toBe("nori");
    expect(nav.scoped).toBe(false);
  });

  it("can open a member repository directly in Git", () => {
    nav.goDetail("nori", "git");
    expect(nav.projectTab).toBe("git");
    nav.goDetail("okane");
    expect(nav.projectTab).toBe("git");
  });

  it("preserves the user's session triage preference", () => {
    nav.setTriage("Needs You");
    nav.goDetail("nori");
    expect(nav.triage).toBe("Needs You");
  });
});

describe("project view preference", () => {
  afterEach(() => vi.restoreAllMocks());
  it("keeps the last tab across projects and session navigation", () => {
    nav.goDetail("nori");
    nav.projectTab = "git";
    nav.goCockpit();
    nav.goDetail("okane");
    expect(nav.projectTab).toBe("git");
    expect(localStorage.getItem("lola.project.tab")).toBe("git");
    nav.projectTab = "overview";
    nav.goDetail("nori");
    expect(nav.projectTab).toBe("overview");
  });
  it("restores the project view from storage after a fresh load", async () => {
    nav.projectTab = "git";
    vi.resetModules();
    const { nav: restored } = await import("./nav.svelte");
    restored.goDetail("another-project");
    expect(restored.projectTab).toBe("git");
  });
  it("ignores an invalid saved view and tolerates unavailable storage", async () => {
    localStorage.setItem("lola.project.tab", "removed-tab");
    vi.resetModules();
    const { nav: restored } = await import("./nav.svelte");
    expect(restored.projectTab).toBe("overview");
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("Storage unavailable"); });
    restored.projectTab = "git";
    restored.goDetail("nori");
    expect(restored.projectTab).toBe("git");
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
