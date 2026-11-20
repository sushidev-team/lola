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
  });
  it("returns from project detail without turning it into a session filter", () => {
    nav.goCockpit("web");
    nav.goGit("web");
    nav.openSession("s1");
    expect(nav.view).toBe("cockpit");
    expect(nav.scoped).toBe(false);
    expect(nav.project).toBe("web");
  });
});

describe("nav.goGit", () => {
  it("returns from Git to the exact session and scope, including on repeated toggles", () => {
    nav.goCockpit("web");
    nav.openSession("s1", true);
    nav.goGit("web");
    nav.goGit("web");
    nav.returnFromGit();
    expect(nav.view).toBe("cockpit");
    expect(nav.focusedTerm).toBe("s1");
    expect(nav.project).toBe("web");
    expect(nav.scoped).toBe(true);
    expect(nav.sidebarBrowsing).toBe(true);
    nav.openSession("s2");
    nav.goGit("web");
    nav.returnFromGit();
    expect(nav.focusedTerm).toBe("s2");
  });

  it("scopes Code to the last project chosen in Git and releases the previous session", () => {
    nav.goCockpit("web");
    nav.openSession("web-1");
    nav.goGit("web");
    nav.goGit("api");
    nav.goGit("mobile");
    nav.returnFromGit();
    expect(nav.view).toBe("cockpit");
    expect(nav.project).toBe("mobile");
    expect(nav.scopeNames).toEqual(["mobile"]);
    expect(nav.focusedTerm).toBe("");
    expect(nav.selectedId).toBe("");
  });

  it("returns from Git to the prior picker", () => {
    nav.goPRPicker("web");
    nav.goGit("other");
    nav.returnFromGit();
    expect(nav.view).toBe("prpicker");
    expect(nav.project).toBe("web");
  });
  it("opens the project workspace without scoping the session list", () => {
    nav.goCockpit("old-project");
    nav.openSession("s1");
    nav.goGit("nori");
    expect(nav.view).toBe("detail");
    expect(nav.scoped).toBe(false);
    expect(nav.project).toBe("nori");
    expect(nav.focusedTerm).toBe("");
    expect(nav.inGit).toBe(true);
  });

  it("repeated project navigation stays in the workspace", () => {
    nav.goGit("nori");
    nav.goGit("nori");
    expect(nav.view).toBe("detail");
    expect(nav.project).toBe("nori");
    expect(nav.scoped).toBe(false);
  });

  it("can open a member repository directly in Git", () => {
    nav.goGit("nori");
    expect(nav.inGit).toBe(true);
    nav.goGit("okane");
    expect(nav.inGit).toBe(true);
  });

  it("preserves the user's session triage preference", () => {
    nav.setTriage("Needs You");
    nav.goGit("nori");
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

describe("nav.focusSessions in Git", () => {
  it("leaves Git mode so the sessions column can take focus", () => {
    nav.goCockpit("");
    nav.goGit("lola");
    nav.pendingMode = "git";
    expect(nav.gitMode).toBe(true);
    nav.focusSessions();
    expect(nav.inGit).toBe(false);
    expect(nav.gitMode).toBe(false);
    expect(nav.sidebarBrowsing).toBe(true);
  });
});
