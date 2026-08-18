import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";

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
    store.groups = [{ name: "clients", label: "Clients" }];
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
    store.groups = [{ name: "clients", label: "Clients", collapsed: true }];
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
    store.groups = [{ name: "clients", label: "Clients" }, { name: "internal" }];
    render(SidebarProjects);
    await fireEvent.keyDown(screen.getByRole("button", { name: "internal 0" }), { key: "ArrowUp", altKey: true });
    expect(SetProjectLayout).toHaveBeenCalledWith({
      groups: [
        { name: "internal", label: "", collapsed: false },
        { name: "clients", label: "Clients", collapsed: false },
      ],
      projects: [],
    });
  });

  it("abandons a drag on pointercancel without writing anything", async () => {
    // The OS can take the pointer away (a gesture, a system dialog) and no
    // pointerup follows; the half-drag must not survive into the next move.
    render(SidebarProjects);
    const row = screen.getByRole("button", { name: "lola" });
    await fireEvent.pointerDown(row, { button: 0, clientX: 0, clientY: 0 });
    await fireEvent.pointerMove(window, { clientX: 0, clientY: 80 });
    await fireEvent.pointerCancel(window);
    await fireEvent.pointerUp(window);
    await fireEvent.pointerMove(window, { clientX: 0, clientY: 400 });
    expect(SetProjectLayout).not.toHaveBeenCalled();
  });

  it("keeps the empty state only while there is neither a project nor a group", () => {
    store.projects = [];
    store.groups = [];
    render(SidebarProjects);
    expect(screen.getByRole("button", { name: "No projects — add one" })).toBeInTheDocument();

    cleanup();
    store.groups = [{ name: "clients", label: "Clients" }];
    render(SidebarProjects);
    expect(screen.queryByRole("button", { name: "No projects — add one" })).not.toBeInTheDocument();
  });
});
