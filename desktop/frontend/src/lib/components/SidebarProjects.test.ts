import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, cleanup, fireEvent, within } from "@testing-library/svelte";
import { tick } from "svelte";

// The bindings are the process boundary. Stubbing them keeps this about what
// the sidebar OFFERS and what it asks the backend to persist — the arrangement
// maths itself is pinned in sidebarlayout.test.ts, without a DOM.
const { AddGroup, RemoveGroup, RenameGroup, SetGroupCollapsed, SetProjectLayout, CreateStack, SetProjectStack, PickFolder, ScanStackFolder, CreateStackFromFolder, RemoveProject, RemoveStack } = vi.hoisted(() => ({
  CreateStack: vi.fn(),
  PickFolder: vi.fn(),
  ScanStackFolder: vi.fn(),
  CreateStackFromFolder: vi.fn(),
  SetProjectStack: vi.fn(),
  RemoveProject: vi.fn(),
  RemoveStack: vi.fn(),
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
    CreateStack: (...a: unknown[]) => CreateStack(...a),
    PickFolder: (...a: unknown[]) => PickFolder(...a),
    ScanStackFolder: (...a: unknown[]) => ScanStackFolder(...a),
    CreateStackFromFolder: (...a: unknown[]) => CreateStackFromFolder(...a),
    SetProjectStack: (...a: unknown[]) => SetProjectStack(...a),
    RemoveProject: (...a: unknown[]) => RemoveProject(...a),
    RemoveStack: (...a: unknown[]) => RemoveStack(...a),
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
    RemoveProject.mockResolvedValue(undefined);
    RenameGroup.mockResolvedValue(undefined);
    SetGroupCollapsed.mockResolvedValue(undefined);
    SetProjectLayout.mockResolvedValue(undefined);
    CreateStack.mockResolvedValue("nori");
    SetProjectStack.mockResolvedValue(undefined);
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

  it("offers creation actions when right-clicking empty project space", async () => {
    render(SidebarProjects);
    await fireEvent.contextMenu(screen.getByRole("navigation", { name: "Projects" }), { clientX: 160, clientY: 600 });
    expect(screen.getByRole("menuitem", { name: "New project" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "New group" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "New stack" })).toBeInTheDocument();
    await fireEvent.click(screen.getByRole("menuitem", { name: "New project" }));
    expect(nav.overlay).toBe("project");
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("keeps project row menus and the search field's native context menu", async () => {
    render(SidebarProjects);
    await fireEvent.contextMenu(screen.getByLabelText("Search projects"));
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    await fireEvent.contextMenu(screen.getByRole("button", { name: "lola" }));
    expect(screen.getByRole("menuitem", { name: "Edit project" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "New project" })).not.toBeInTheDocument();
  });

  it("dismisses the background context menu with Escape", async () => {
    render(SidebarProjects);
    await fireEvent.contextMenu(screen.getByRole("navigation", { name: "Projects" }));
    await fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Search projects")).toHaveFocus();
  });

  it("creates a group from the menu's dialog", async () => {
    render(SidebarProjects);
    await fireEvent.click(screen.getByRole("button", { name: "Add" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "New group" }));
    const input = within(screen.getByRole("dialog")).getByRole("textbox");
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
    const input = within(screen.getByRole("dialog")).getByRole("textbox") as HTMLInputElement;
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

  // --- stacks -------------------------------------------------------------------

  const stackProjects = () => [
    fakeProject(),
    fakeProject({ name: "nori", kind: "stack", path: "" }),
    fakeProject({ name: "web", stack: "nori", group: "clients" }),
  ];

  it("creates a stack from the + menu and opens its form", async () => {
    render(SidebarProjects);
    await fireEvent.click(screen.getByRole("button", { name: "Add" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "New stack" }));
    await fireEvent.input(within(screen.getByRole("dialog")).getByRole("textbox"), { target: { value: "Nori" } });
    await fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await tick();
    await tick();
    expect(CreateStack).toHaveBeenCalledWith("Nori");
    expect(nav.overlay).toBe("project");
  });

  it("creates a stack from a parent folder with the apps left checked", async () => {
    PickFolder.mockResolvedValue("/src/nori");
    const apps = [
      { path: "/src/nori/api", label: "Api", repo: "acme/api", defaultBranch: "main", project: "", blocked: "" },
      { path: "/src/nori/web", label: "Web", repo: "acme/web", defaultBranch: "main", project: "web", blocked: "" },
      { path: "/src/nori/old", label: "Old", repo: "", defaultBranch: "", project: "old", blocked: "already in stack Other" },
      { path: "/src/nori/docs", label: "Docs", repo: "", defaultBranch: "main", project: "", blocked: "" },
    ];
    ScanStackFolder.mockResolvedValue({ path: "/src/nori", suggestedLabel: "Nori", apps });
    CreateStackFromFolder.mockResolvedValue("nori");
    render(SidebarProjects);
    await fireEvent.click(screen.getByRole("button", { name: "Add" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "New stack" }));
    await fireEvent.click(screen.getByRole("button", { name: "Choose parent folder…" }));
    await vi.waitFor(() => expect(screen.getByRole("list", { name: "Apps in folder" })).toBeInTheDocument());
    const dialog = screen.getByRole("dialog");
    // The name is suggested from the folder.
    expect((within(dialog).getByRole("textbox") as HTMLInputElement).value).toBe("Nori");
    expect(within(dialog).getByText("joins existing project")).toBeInTheDocument();
    const old = within(dialog).getByRole("checkbox", { name: "Include Old" }) as HTMLInputElement;
    expect(old.disabled).toBe(true);
    expect(old.checked).toBe(false);
    await fireEvent.click(within(dialog).getByRole("checkbox", { name: "Include Docs" }));
    expect(within(dialog).getByText("2 of 4 included")).toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await tick();
    await tick();
    expect(CreateStack).not.toHaveBeenCalled();
    expect(CreateStackFromFolder).toHaveBeenCalledTimes(1);
    const [label, sent] = CreateStackFromFolder.mock.calls[0];
    expect(label).toBe("Nori");
    expect(sent.map((a: { path: string }) => a.path)).toEqual(["/src/nori/api", "/src/nori/web"]);
    expect(nav.overlay).toBe("project");
  });

  it("nests a stack's members under it, whatever their folder", () => {
    store.projects = stackProjects();
    const { container } = render(SidebarProjects);
    const tops = Array.from(container.querySelectorAll("[data-toprow]"));
    // web names the "clients" folder but is drawn under nori, so the folder is empty.
    expect(screen.getByRole("button", { name: "Clients 0" })).toBeInTheDocument();
    const nori = tops.find((t) => t.querySelector("[data-stack-head='nori']"))!;
    expect(nori.querySelector("[data-stack-members='nori']")!.textContent).toContain("web");
  });

  it("shows stack members using their stack's polling status, not their inactive own poll", async () => {
    store.projects = stackProjects();
    const polls = (enabled: boolean, lastError = '') => ({ polls: [
      { name: 'nori', enabled, lastError },
      { name: 'web', enabled: false, lastError: 'old standalone error' },
    ] }) as NonNullable<typeof store.status>;
    store.status = polls(true);
    render(SidebarProjects);
    const row = screen.getByRole('button', {name:'web'}).parentElement!;
    expect(row).toHaveAttribute('title', 'Part of nori · stack polling enabled');
    expect(row.querySelector('.text-good svg')).toHaveClass('lucide-circle-dot');
    store.status = polls(false);
    await tick();
    expect(row).toHaveAttribute('title', 'Part of nori · stack polling paused');
    expect(row.querySelector('.text-good')).toBeNull();
    store.status = polls(true, 'Cannot reach Linear');
    await tick();
    expect(row).toHaveAttribute('title', 'Part of nori · poll error: Cannot reach Linear');
    expect(row.querySelector('.text-bad svg')).toHaveClass('lucide-circle-dot');
    store.status = null;
    await tick();
    expect(row).toHaveAttribute('title', 'Part of nori · polling status unavailable');
    expect(row.querySelector('.text-good')).toBeNull();
  });

  it("asks before adding a project to the stack its row is dropped on", async () => {
    store.projects = stackProjects();
    store.groups = [];
    const { container } = render(SidebarProjects);
    Array.from(container.querySelectorAll("[data-toprow]")).forEach((el, i) => stubRect(el, i * ROW_H * 2, ROW_H));
    stubRect(container.querySelector("[data-stack-head='nori']")!, 2 * ROW_H, ROW_H);

    pointer(screen.getByRole("button", { name: "lola" }), "pointerdown", { button: 0, clientX: 0, clientY: 4 });
    pointer(window, "pointermove", { clientX: 0, clientY: 2 * ROW_H + ROW_H / 2 });
    pointer(window, "pointerup");
    await tick();

    expect(SetProjectLayout).not.toHaveBeenCalled();
    expect(SetProjectStack).not.toHaveBeenCalled();
    expect(confirm.request?.body).toBe("Add lola to stack nori?");
    expect(confirm.request?.detail).toMatch(/kept but ignored/);
    confirm.accept();
    expect(SetProjectStack).toHaveBeenCalledWith("lola", "nori");
  });

  it("asks before taking a member out when it is dropped outside its stack", async () => {
    store.projects = stackProjects();
    store.groups = [];
    const { container } = render(SidebarProjects);
    Array.from(container.querySelectorAll("[data-toprow]")).forEach((el, i) => stubRect(el, 100 + i * ROW_H, ROW_H));
    stubRect(container.querySelector("[data-stack-head='nori']")!, 100 + ROW_H, ROW_H);

    pointer(screen.getByRole("button", { name: "web" }), "pointerdown", { button: 0, clientX: 0, clientY: 150 });
    pointer(window, "pointermove", { clientX: 0, clientY: 0 }); // above everything: top gap 0
    pointer(window, "pointerup");
    await tick();

    expect(confirm.request?.body).toBe("Remove web from stack nori?");
    confirm.accept();
    expect(SetProjectStack).toHaveBeenCalledWith("web", "");
    // Then placed where it was dropped (the top level).
    await vi.waitFor(() => expect(SetProjectLayout).toHaveBeenCalled());
    expect(SetProjectLayout).toHaveBeenCalledWith({
      groups: [],
      projects: [
        { name: "web", group: "" },
        { name: "lola", group: "" },
        { name: "nori", group: "" },
      ],
    });
  });

  it("offers stack membership on a project row's context menu", async () => {
    store.projects = stackProjects();
    render(SidebarProjects);
    await fireEvent.contextMenu(screen.getByRole("button", { name: "lola" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Add to stack…" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "nori" }));
    expect(confirm.request?.body).toBe("Add lola to stack nori?");

    confirm.cancel();
    await fireEvent.contextMenu(screen.getByRole("button", { name: "web" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Remove from stack" }));
    expect(confirm.request?.body).toBe("Remove web from stack nori?");
    confirm.accept();
    expect(SetProjectStack).toHaveBeenCalledWith("web", "");
  });

  it("confirms removal of the context-menu project by id and allows cancellation", async () => {
    store.projects = [fakeProject({ name: "lola", label: "Lola App" }), fakeProject({ name: "other" })];
    nav.project = "other";
    render(SidebarProjects);
    const openRemove = async () => {
      await fireEvent.contextMenu(screen.getByRole("button", { name: "Lola App" }));
      await fireEvent.click(screen.getByRole("menuitem", { name: "Remove project…" }));
    };
    await openRemove();
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(confirm.request?.title).toBe("Remove project?");
    expect(confirm.request?.body).toBe("Remove Lola App from lola?");
    expect(RemoveProject).not.toHaveBeenCalled();
    confirm.cancel();
    expect(RemoveProject).not.toHaveBeenCalled();
    await openRemove();
    confirm.accept();
    expect(RemoveProject).toHaveBeenCalledExactlyOnceWith("lola");
  });

  it("reports a failed project removal", async () => {
    RemoveProject.mockRejectedValueOnce(new Error("Could not save config"));
    const flash = vi.spyOn(store, "setFlash");
    render(SidebarProjects);
    await fireEvent.contextMenu(screen.getByRole("button", { name: "lola" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Remove project…" }));
    confirm.accept();
    await vi.waitFor(() => expect(flash).toHaveBeenCalledWith("Error: Could not save config", "bad"));
    expect(store.projectByName("lola")).toBeDefined();
    flash.mockRestore();
  });

  it("distinguishes removing a stack from removing a member's configuration", async () => {
    store.projects = stackProjects();
    render(SidebarProjects);
    await fireEvent.contextMenu(screen.getByRole("button", { name: "web" }));
    expect(screen.getByRole("menuitem", { name: "Remove from stack" })).toBeInTheDocument();
    await fireEvent.click(screen.getByRole("menuitem", { name: "Remove project…" }));
    expect(confirm.request?.title).toBe("Remove project?");
    confirm.accept();
    expect(RemoveProject).toHaveBeenCalledWith("web");
    expect(SetProjectStack).not.toHaveBeenCalled();

    await fireEvent.contextMenu(screen.getByRole("button", { name: "nori" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Remove stack…" }));
    expect(confirm.request?.title).toBe("Remove stack?");
    expect(confirm.request?.choices?.items.every((item) => item.checked)).toBe(true);
    confirm.cancel();
  });

  it("removes a member project from lola, not only from its stack", async () => {
    store.projects = stackProjects();
    render(SidebarProjects);
    await fireEvent.contextMenu(screen.getByRole("button", { name: "web" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Remove project…" }));
    expect(confirm.request?.body).toBe("Remove web from lola?");
    confirm.accept();
    expect(RemoveProject).toHaveBeenCalledWith("web");
  });

  it("removes a stack, keeping only the members left checked", async () => {
    store.projects = stackProjects();
    render(SidebarProjects);
    await fireEvent.contextMenu(screen.getByRole("button", { name: "nori" }));
    expect(screen.queryByRole("menuitem", { name: "Add to stack…" })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "lola" })).not.toBeInTheDocument();
    await fireEvent.click(screen.getByRole("menuitem", { name: "Remove stack…" }));
    const items = confirm.request?.choices?.items ?? [];
    expect(items.length).toBeGreaterThan(0);
    expect(items.every((i) => i.checked)).toBe(true);
    items[0].checked = false;
    confirm.accept();
    expect(RemoveStack).toHaveBeenCalledWith("nori", items.slice(1).map((i) => i.id));
  });
});

it("searches project names, repositories and paths without changing layout", async () => {
  cleanup(); store.groups=[];
  store.projects = [fakeProject({name:"alpha", label:"Alpha Website",path:"/code/customer-a",repo:"acme/website" }), fakeProject({name:"beta",label:"Beta",path:"/code/customer-b"})];
  render(SidebarProjects);
  await fireEvent.input(screen.getByLabelText("Search projects"),{target:{value:"customer-a"}});
  expect(screen.getByText("Alpha Website")).toBeInTheDocument();
  expect(screen.queryByText("Beta")).not.toBeInTheDocument();
  expect(screen.getByText("Path ·")).toBeInTheDocument();
  expect(screen.getByText("customer-a")).toHaveClass("text-accent-ink");
  await fireEvent.input(screen.getByLabelText("Search projects"),{target:{value:"aws"}});
  const result = screen.getByRole("list", {name:"Matching projects"});
  expect(result.textContent).toContain("Alpha Website");
  expect(Array.from(result.querySelectorAll(".text-accent-ink"), el => el.textContent).join("")).toBe("AWs");
  await fireEvent.click(screen.getByRole("button", {name:"Clear project search"}));
  expect(screen.getByLabelText("Search projects")).toHaveValue("");
  expect(screen.getByLabelText("Search projects")).toHaveFocus();
  expect(screen.queryByRole("button", {name:"Clear project search"})).not.toBeInTheDocument();
  expect(screen.getByText("Beta")).toBeInTheDocument();
});
