import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";
import { tick } from "svelte";

// The bindings are the process boundary. Stubbing them keeps this about what
// the sidebar OFFERS and what it asks the backend to persist — the arrangement
// maths itself is pinned in sidebarlayout.test.ts, without a DOM.
const { AddGroup, RemoveGroup, RenameGroup, SetGroupCollapsed, SetProjectLayout } = vi.hoisted(() => ({
  AddGroup: vi.fn(),
  RemoveGroup: vi.fn(),
  RenameGroup: vi.fn(),
  SetGroupCollapsed: vi.fn(),
  SetProjectLayout: vi.fn(),
}));

vi.mock("@bindings/desktop", () => ({
  DaemonService: {
    Alive: vi.fn().mockResolvedValue(false),
    Sessions: vi.fn(),
    Projects: vi.fn(),
    Status: vi.fn(),
  },
  ConfigService: {
    ConfigExists: vi.fn(),
    AddGroup: (...a: unknown[]) => AddGroup(...a),
    RemoveGroup: (...a: unknown[]) => RemoveGroup(...a),
    RenameGroup: (...a: unknown[]) => RenameGroup(...a),
    SetGroupCollapsed: (...a: unknown[]) => SetGroupCollapsed(...a),
    SetProjectLayout: (...a: unknown[]) => SetProjectLayout(...a),
  },
  TermService: {},
}));

const SidebarProjects = (await import("./SidebarProjects.svelte")).default;
const { store } = await import("$lib/store.svelte");
const { nav } = await import("$lib/nav.svelte");
const { confirm } = await import("$lib/confirm.svelte");

type ProjectInfo = (typeof store.projects)[number];

function fakeProject(over: Partial<ProjectInfo> = {}): ProjectInfo {
  return {
    name: "lola",
    label: "",
    path: "/tmp/lola",
    repo: "",
    defaultBranch: "main",
    agent: "claude",
    agentBin: "claude",
    agentOk: true,
    pathOk: true,
    repoConfigured: false,
    pollCount: 0,
    pollsEnabled: 0,
    sessions: 0,
    liveCounted: 0,
    needsYou: 0,
    ciRed: 0,
    openPrs: 0,
    ...over,
  } as ProjectInfo;
}

describe("SidebarProjects", () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    AddGroup.mockResolvedValue("clients");
    RemoveGroup.mockResolvedValue(undefined);
    RenameGroup.mockResolvedValue(undefined);
    SetGroupCollapsed.mockResolvedValue(undefined);
    SetProjectLayout.mockResolvedValue(undefined);
    store.projects = [fakeProject(), fakeProject({ name: "okane", group: "clients" })];
    store.groups = [{ name: "clients", label: "Clients", position: 1 }];
    store.status = null;
    confirm.cancel();
    nav.closeOverlay();
    nav.scoped = false;
    nav.project = "";
  });

  it("adds through a MENU, not straight into the project form", async () => {
    render(SidebarProjects);
    // The "+" is the whole point of the ticket: one affordance, two things to
    // add, so it must not jump into a form on click.
    await fireEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(nav.overlay).toBeNull();
    expect(screen.getByRole("menuitem", { name: "New project" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "New group" })).toBeInTheDocument();
  });

  it("opens the project form from the menu", async () => {
    render(SidebarProjects);
    await fireEvent.click(screen.getByRole("button", { name: "Add" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "New project" }));
    expect(nav.overlay).toBe("project");
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("creates a group from the menu's dialog", async () => {
    render(SidebarProjects);
    await fireEvent.click(screen.getByRole("button", { name: "Add" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "New group" }));
    const input = screen.getByRole("textbox");
    await fireEvent.input(input, { target: { value: "Clients" } });
    await fireEvent.click(screen.getByRole("button", { name: "Create" }));
    expect(AddGroup).toHaveBeenCalledWith("Clients");
  });

  it("renders a group header with its member count and its members under it", () => {
    render(SidebarProjects);
    // The count rides the accessible name: the header announces "Clients 1".
    const header = screen.getByRole("button", { name: "Clients 1" });
    expect(header).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("button", { name: "okane" })).toBeInTheDocument();
  });

  it("draws an empty group rather than hiding it", () => {
    // A folder is created BEFORE anything is dragged into it, so an empty one
    // that did not render could never receive a project.
    store.projects = [fakeProject()];
    render(SidebarProjects);
    expect(screen.getByRole("button", { name: "Clients 0" })).toBeInTheDocument();
  });

  it("persists a collapse toggle", async () => {
    render(SidebarProjects);
    await fireEvent.click(screen.getByRole("button", { name: "Clients 1" }));
    expect(SetGroupCollapsed).toHaveBeenCalledWith("clients", true);
  });

  it("hides a collapsed group's members", () => {
    store.groups = [{ name: "clients", label: "Clients", position: 1, collapsed: true }];
    render(SidebarProjects);
    expect(screen.getByRole("button", { name: "Clients 1" })).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("button", { name: "okane" })).not.toBeInTheDocument();
  });

  it("confirms before removing a group, and says the projects survive", async () => {
    render(SidebarProjects);
    await fireEvent.click(screen.getByRole("button", { name: "Remove Clients" }));
    expect(RemoveGroup).not.toHaveBeenCalled();
    expect(confirm.request?.title).toBe("Remove group?");
    expect(confirm.request?.detail).toMatch(/nothing is deleted/);
    confirm.accept();
    expect(RemoveGroup).toHaveBeenCalledWith("clients");
  });

  it("renames a group through the same dialog, seeded with its label", async () => {
    render(SidebarProjects);
    await fireEvent.click(screen.getByRole("button", { name: "Rename Clients" }));
    const input = screen.getByRole("textbox") as HTMLInputElement;
    expect(input.value).toBe("Clients");
    await fireEvent.input(input, { target: { value: "Client Work" } });
    await fireEvent.click(screen.getByRole("button", { name: "Rename" }));
    expect(RenameGroup).toHaveBeenCalledWith("clients", "Client Work");
  });

  it("reorders a project with alt+arrow, sending the WHOLE arrangement", async () => {
    // The keyboard path is not a convenience: the pointer drag is the only other
    // way to arrange the sidebar, so without it the feature is mouse-only.
    store.projects = [fakeProject(), fakeProject({ name: "nori" })];
    store.groups = [];
    render(SidebarProjects);
    const row = screen.getByRole("button", { name: "nori" });
    await fireEvent.keyDown(row, { key: "ArrowUp", altKey: true });
    expect(SetProjectLayout).toHaveBeenCalledWith({
      groups: [],
      projects: [
        { name: "nori", group: "" },
        { name: "lola", group: "" },
      ],
    });
  });

  it("writes nothing when alt+arrow would move past the end", async () => {
    store.projects = [fakeProject(), fakeProject({ name: "nori" })];
    store.groups = [];
    render(SidebarProjects);
    await fireEvent.keyDown(screen.getByRole("button", { name: "lola" }), { key: "ArrowUp", altKey: true });
    expect(SetProjectLayout).not.toHaveBeenCalled();
  });

  it("leaves a bare arrow key to normal navigation", async () => {
    store.groups = [];
    render(SidebarProjects);
    await fireEvent.keyDown(screen.getByRole("button", { name: "lola" }), { key: "ArrowUp" });
    expect(SetProjectLayout).not.toHaveBeenCalled();
  });

  it("reorders groups with alt+arrow", async () => {
    store.projects = [];
    store.groups = [
      { name: "clients", label: "Clients", position: 0 },
      { name: "internal", position: 1 },
    ];
    render(SidebarProjects);
    await fireEvent.keyDown(screen.getByRole("button", { name: "internal 0" }), { key: "ArrowUp", altKey: true });
    expect(SetProjectLayout).toHaveBeenCalledWith({
      groups: [
        { name: "internal", label: "", position: 0, collapsed: false },
        { name: "clients", label: "Clients", position: 1, collapsed: false },
      ],
      projects: [],
    });
  });

  // jsdom implements no PointerEvent, so fireEvent.pointerDown synthesizes a
  // bare Event and DROPS button/clientX/clientY — a drag fired that way never
  // starts (the handler bails on `button !== 0`) and the test passes vacuously.
  // A MouseEvent under the pointer event's name carries every field the handler
  // actually reads.
  function pointer(el: EventTarget, type: string, init: MouseEventInit = {}) {
    el.dispatchEvent(new MouseEvent(type, { bubbles: true, cancelable: true, ...init }));
  }

  // jsdom has no layout either, so a drag test has to supply the geometry the
  // resolver reads. Stubbing it is what makes the WHOLE path testable — resolve → re-base
  // onto post-lift coordinates → move → payload — which is exactly where the
  // "downward drags land one row too far" bug lived.
  function stubRect(el: Element, top: number, height: number) {
    Object.defineProperty(el, "getBoundingClientRect", {
      configurable: true,
      value: () => ({ top, bottom: top + height, height, left: 0, right: 200, width: 200, x: 0, y: top }),
    });
  }

  const ROW_H = 28;

  it("drags a project down past its neighbour and lands it THERE, not past it", async () => {
    // THREE rows, dropping into the MIDDLE gap. With two, a downward drag can
    // only end at the tail, where clamping quietly absorbs an off-by-one — the
    // shape of this test is what makes it able to fail.
    store.projects = [fakeProject(), fakeProject({ name: "nori" }), fakeProject({ name: "okane" })];
    store.groups = [];
    const { container } = render(SidebarProjects);
    const tops = Array.from(container.querySelectorAll("[data-toprow]"));
    tops.forEach((el, i) => stubRect(el, i * ROW_H, ROW_H));

    pointer(screen.getByRole("button", { name: "lola" }), "pointerdown", { button: 0, clientX: 0, clientY: 4 });
    // Past nori's midpoint (28..56, middle 42) but short of okane's (56..84,
    // middle 70): the indicator sits between them, and so must the row.
    pointer(window, "pointermove", { clientX: 0, clientY: 50 });
    pointer(window, "pointerup");
    await tick();

    expect(SetProjectLayout).toHaveBeenCalledWith({
      groups: [],
      projects: [
        { name: "nori", group: "" },
        { name: "lola", group: "" },
        { name: "okane", group: "" },
      ],
    });
  });

  it("files a project into the folder its row is dropped on", async () => {
    store.projects = [fakeProject(), fakeProject({ name: "nori" })];
    store.groups = [{ name: "clients", label: "Clients", position: 2 }];
    const { container } = render(SidebarProjects);
    const tops = Array.from(container.querySelectorAll("[data-toprow]"));
    tops.forEach((el, i) => stubRect(el, i * ROW_H, ROW_H));
    // The folder's own header is the drop target; its (empty) member list keeps
    // jsdom's zero-height rect, which the resolver skips.
    const head = container.querySelector("[data-head]")!;
    stubRect(head, 2 * ROW_H, ROW_H);

    pointer(screen.getByRole("button", { name: "lola" }), "pointerdown", { button: 0, clientX: 0, clientY: 4 });
    // The MIDDLE of the folder row — its edges would be the gaps either side.
    pointer(window, "pointermove", { clientX: 0, clientY: 2 * ROW_H + ROW_H / 2 });
    pointer(window, "pointerup");
    await tick();

    expect(SetProjectLayout).toHaveBeenCalledWith({
      groups: [{ name: "clients", label: "Clients", position: 1, collapsed: false }],
      projects: [
        { name: "nori", group: "" },
        { name: "lola", group: "clients" },
      ],
    });
  });

  it("writes nothing when a drag ends where it started", async () => {
    store.projects = [fakeProject(), fakeProject({ name: "nori" })];
    store.groups = [];
    const { container } = render(SidebarProjects);
    Array.from(container.querySelectorAll("[data-toprow]")).forEach((el, i) => stubRect(el, i * ROW_H, ROW_H));

    pointer(screen.getByRole("button", { name: "lola" }), "pointerdown", { button: 0, clientX: 0, clientY: 4 });
    pointer(window, "pointermove", { clientX: 0, clientY: 10 });
    pointer(window, "pointerup");
    await tick();
    expect(SetProjectLayout).not.toHaveBeenCalled();
  });

  it("dismisses the add menu on Escape and returns focus to the trigger", async () => {
    render(SidebarProjects);
    const trigger = screen.getByRole("button", { name: "Add" });
    await fireEvent.click(trigger);
    expect(screen.getByRole("menu")).toBeInTheDocument();
    await fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(document.activeElement).toBe(trigger);
  });

  it("abandons a drag on pointercancel without writing anything", async () => {
    // The OS can take the pointer away (a gesture, a system dialog) and no
    // pointerup follows; the half-drag must not survive into the next move.
    const { container } = render(SidebarProjects);
    Array.from(container.querySelectorAll("[data-toprow]")).forEach((el, i) => stubRect(el, i * ROW_H, ROW_H));
    pointer(screen.getByRole("button", { name: "lola" }), "pointerdown", { button: 0, clientX: 0, clientY: 4 });
    pointer(window, "pointermove", { clientX: 0, clientY: 50 }); // a real drag, past the threshold
    pointer(window, "pointercancel");
    pointer(window, "pointerup");
    pointer(window, "pointermove", { clientX: 0, clientY: 400 });
    await tick();
    expect(SetProjectLayout).not.toHaveBeenCalled();
  });

  it("files a project into a folder with alt+right and back out with alt+left", async () => {
    // The project form deliberately has no group field, so this is the only
    // pointer-free way to file a project — losing it makes the feature
    // mouse-only.
    store.projects = [fakeProject(), fakeProject({ name: "nori" })];
    store.groups = [{ name: "clients", label: "Clients", position: 1 }];
    render(SidebarProjects);
    await fireEvent.keyDown(screen.getByRole("button", { name: "nori" }), { key: "ArrowRight", altKey: true });
    expect(SetProjectLayout).toHaveBeenCalledWith({
      groups: [{ name: "clients", label: "Clients", position: 1, collapsed: false }],
      projects: [
        { name: "lola", group: "" },
        { name: "nori", group: "clients" },
      ],
    });

    vi.clearAllMocks();
    SetProjectLayout.mockResolvedValue(undefined);
    cleanup();
    store.projects = [fakeProject(), fakeProject({ name: "nori", group: "clients" })];
    render(SidebarProjects);
    await fireEvent.keyDown(screen.getByRole("button", { name: "nori" }), { key: "ArrowLeft", altKey: true });
    expect(SetProjectLayout).toHaveBeenCalledWith({
      groups: [{ name: "clients", label: "Clients", position: 1, collapsed: false }],
      projects: [
        { name: "lola", group: "" },
        { name: "nori", group: "" },
      ],
    });
  });

  it("draws a folder among the projects, not in a section below them", () => {
    store.projects = [fakeProject(), fakeProject({ name: "nori" })];
    store.groups = [{ name: "clients", label: "Clients", position: 1 }];
    const { container } = render(SidebarProjects);
    const rows = Array.from(container.querySelectorAll("[data-toprow]"));
    // The folder is a top-level row BETWEEN the two projects, not a section
    // under them — the whole point of the panel's shape.
    expect(rows.map((r) => r.getAttribute("data-group"))).toEqual([null, "clients", null]);
    expect(rows[0].textContent).toContain("lola");
    expect(rows[2].textContent).toContain("nori");
  });

  it("keeps the empty state only while there is neither a project nor a group", () => {
    store.projects = [];
    store.groups = [];
    render(SidebarProjects);
    expect(screen.getByRole("button", { name: "No projects — add one" })).toBeInTheDocument();

    cleanup();
    store.groups = [{ name: "clients", label: "Clients", position: 0 }];
    render(SidebarProjects);
    expect(screen.queryByRole("button", { name: "No projects — add one" })).not.toBeInTheDocument();
  });
});
