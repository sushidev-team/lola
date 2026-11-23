import { beforeEach, it, expect, vi } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/svelte";

vi.mock("./LiveTerminal.svelte", async () => ({ default: (await import("./ssh/TerminalStub.test.svelte")).default }));

const OpenFolder = vi.fn();
vi.mock("@bindings/desktop", () => ({
  DaemonService: {
    Diff: vi.fn().mockResolvedValue({ session: "s1", base: "main", mergeBase: "abc", files: [] }),
    OpenFolder: (...args: unknown[]) => OpenFolder(...args),
  },
  ConfigService: {},
  TermService: { Shells: vi.fn(async () => []) },
  TalkService: { State: vi.fn(async () => ({ caps: {}, composer: "unknown", feedSeq: 0, health: "ok" })) },
}));

const { default: SessionEmbed } = await import("./SessionEmbed.svelte");
const { default: SessionMenu } = await import("./SessionMenu.svelte");
const { store } = await import("$lib/store.svelte");
const { nav } = await import("$lib/nav.svelte");
const { sessionMenu } = await import("$lib/sessionmenu.svelte");
const { terms, SSH } = await import("$lib/terms.svelte");

beforeEach(() => {
  cleanup();
  OpenFolder.mockReset().mockResolvedValue(undefined);
  store.flash = null;
  terms.select("s1", SSH);
  nav.focusedTerm = "s1";
});

async function fromTheMenu(name: string) {
  sessionMenu.request = { id: "s1", x: 1, y: 1 };
  render(SessionMenu);
  await fireEvent.click(screen.getByRole("menuitem", { name }));
}

it.each(["", "main", "temporary"])("opens the session directory for location %s", async (location) => {
  store.sessions = [{ id: "s1", worktree: "/code/My session", location }] as never;
  await fromTheMenu("Open in Finder");
  expect(OpenFolder).toHaveBeenCalledWith("/code/My session");
});

it("does not offer Finder without a session directory", () => {
  store.sessions = [{ id: "s1", worktree: "" }] as never;
  sessionMenu.request = { id: "s1", x: 1, y: 1 };
  render(SessionMenu);
  expect(screen.queryByRole("menuitem", { name: "Open in Finder" })).not.toBeInTheDocument();
});

it("shows failures from opening the directory", async () => {
  OpenFolder.mockRejectedValue(new Error("folder no longer exists"));
  store.sessions = [{ id: "s1", worktree: "/missing" }] as never;
  await fromTheMenu("Open in Finder");
  await waitFor(() => expect(store.flash?.text).toContain("folder no longer exists"));
});

it("offers Connect from the strip's +, on every kind of session that can have a host", async () => {
  for (const extra of [{}, { location: "ssh" }]) {
    store.sessions = [{ id: "s1", worktree: "/code/x", ...extra }] as never;
    terms.select("s1", SSH);
    render(SessionEmbed, { sessionId: "s1" });
    await fireEvent.click(screen.getByRole("button", { name: "Add tab" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Connect to host…" }));
    expect(nav.overlay).toBe("sshconnect");
    expect(nav.overlayProject).toBe("s1");
    nav.closeOverlay();
    cleanup();
  }
});

it("gives each live host one `$` cell with no ×, and a click leaves the scope alone", async () => {
  const { ssh } = await import("$lib/ssh.svelte");
  const a = { id: "c1", host: "dev", label: "Dev", state: "connected" };
  const b = { id: "c2", host: "dev", label: "Dev", state: "closed" };
  const c = { id: "c3", host: "api", label: "API", state: "connected" };
  const d = { id: "c4", host: "api", label: "API", state: "denied" };
  store.sessions = [{ id: "s1", worktree: "/code/x", location: "ssh", ssh: { connections: [a, b, c, d] } }] as never;
  terms.select("s1", SSH);
  render(SessionEmbed, { sessionId: "s1" });
  const cells = [...document.querySelectorAll("[data-strip-cell]")].map((t) => t.getAttribute("data-strip-cell"));
  expect(cells).toEqual(["conversation", "host:c1", "host:c3"]);
  const cell = document.querySelector('[data-strip-cell="host:c3"]') as HTMLElement;
  expect(cell.parentElement?.querySelector('[aria-label*="close"]')).toBeNull();
  await fireEvent.click(cell);
  expect(ssh.view("s1").shellConn).toBe("c3");
  expect(ssh.view("s1").scope).toBe("");
  expect(terms.activeTab("s1")).toBe(SSH);
});

it("falls back to the terminal when the conversation it was showing is no longer offered", () => {
  store.sessions = [{ id: "s1", worktree: "/code/x", location: "temporary", status: "shell", agentState: "shell" }] as never;
  terms.select("s1", "ssh");
  render(SessionEmbed, { sessionId: "s1" });
  expect(document.querySelector("[data-ssh-workspace]")).toBeNull();
  // Agentless with one terminal: no strip at all, and the terminal is what is on show.
  expect(screen.queryByRole("tab", { name: "Conversation" })).toBeNull();
  expect(terms.activeTab("s1")).toBe("agent");
});

it("puts Raw beside the activity bar, and the same toggle over the agent terminal to go back", async () => {
  const { ssh } = await import("$lib/ssh.svelte");
  ssh.view("s1").narrow = "stream";
  store.sessions = [{ id: "s1", worktree: "/code/x", location: "ssh", tmuxName: "lola-s1" }] as never;
  terms.select("s1", SSH);
  render(SessionEmbed, { sessionId: "s1" });
  expect(screen.queryByRole("tab", { name: "Terminal" })).toBeNull();
  const raw = screen.getByRole("button", { name: /^raw$/i });
  expect(raw).toHaveAttribute("aria-pressed", "false");
  expect(raw.closest("[data-view-strip]")).toBeNull();
  await fireEvent.click(raw);
  expect(terms.activeTab("s1")).toBe("agent");
  const back = screen.getAllByRole("button", { name: /^raw$/i }).find((b) => b.getAttribute("aria-pressed") === "true");
  expect(back).toBeTruthy();
  await fireEvent.click(back!);
  expect(terms.activeTab("s1")).toBe(SSH);
});

it("shows no Raw toggle over a dev terminal, and keeps Conversation selected in the strip", async () => {
  const { ssh } = await import("$lib/ssh.svelte");
  ssh.view("s1").narrow = "stream";
  store.sessions = [{ id: "s1", worktree: "/code/x", location: "ssh" }] as never;
  (terms as unknown as { shells: Map<string, string[]> }).shells.set("s1", ["s1-dev-1"]);
  terms.select("s1", "s1-dev-1");
  render(SessionEmbed, { sessionId: "s1" });
  const tabs = screen.getAllByRole("tab", { name: "Conversation" });
  expect(tabs.some((t) => t.getAttribute("aria-selected") === "true" && t.tabIndex === 0)).toBe(true);
  for (const b of screen.queryAllByRole("button", { name: /^raw$/i })) expect(b).toHaveAttribute("aria-pressed", "false");
});

it("keeps every shell in the tab row at a narrow width and opens the menu outside the clipped pane", async () => {
  const { default: ViewStrip } = await import("./session/ViewStrip.svelte");
  const width = vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(530);
  const names = ["s1-shell-1", "s1-shell-2"];
  (terms as unknown as { shells: Map<string, string[]> }).shells.set("s1", names);
  store.sessions = [{ id: "s1", worktree: "/checkout", agent: "claude" }] as never;
  try {
    const { container } = render(ViewStrip, { session: store.sessions[0], sidePanels: true });
    const cells = screen.getAllByRole("tab").map(t => t.dataset.stripCell);
    expect(cells).toEqual(["conversation", ...names]);
    const add = screen.getByRole("button", { name: "Add tab" });
    vi.spyOn(add, "getBoundingClientRect").mockReturnValue({ left: 5, bottom: 60 } as DOMRect);
    await fireEvent.click(add);
    const menu = screen.getByRole("menu");
    expect(container.contains(menu)).toBe(false);
    expect(menu.style.left).toBe("5px");
    await fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("menu")).toBeNull();
    await fireEvent.click(screen.getAllByRole("tab")[2]);
    expect(terms.activeTab("s1")).toBe(names[1]);
  } finally {
    width.mockRestore();
  }
});

it("opens and switches independent Conversation tabs on the same checkout", async () => {
  const { default: ViewStrip } = await import("./session/ViewStrip.svelte");
  const owner = { id:"primary", project:"web", agent:"codex", worktree:"/checkout", canOpenAgentTab:true };
  const child = { ...owner, id:"second", agentTabParent:owner.id };
  store.sessions = [owner] as never;
  nav.openSession(owner.id);
  const open = vi.spyOn(store,"openManual").mockResolvedValue({sessionId:child.id} as never);
  try {
    const strip = render(ViewStrip, { session:store.sessions[0], sidePanels:true });
    await fireEvent.click(screen.getByRole("button",{ name:"Add tab" }));
    await fireEvent.click(screen.getByRole("menuitem",{ name:"New agent conversation" }));
    expect(open).toHaveBeenCalledWith(expect.objectContaining({parentSession:"primary",location:"agent-tab",agentKind:"codex"}));
    expect(nav.focusedTerm).toBe(child.id);
    expect(terms.activeTab(child.id)).toBe(SSH);
    store.sessions = [owner,child] as never;
    await strip.rerender({session:store.sessions[1],sidePanels:true});
    expect(screen.getByRole("tab",{name:"Conversation 2"})).toHaveAttribute("aria-selected","true");
    await fireEvent.click(screen.getByRole("tab",{name:"Conversation"}));
    expect(nav.focusedTerm).toBe(owner.id);
    expect(terms.activeTab(owner.id)).toBe(SSH);
    const close = vi.spyOn(store,"askKill").mockImplementation(() => {});
    try {
      await fireEvent.click(screen.getByRole("button",{name:"Close Conversation 2"}));
      expect(close).toHaveBeenCalledWith(child.id,undefined,expect.objectContaining({onRemoved:expect.any(Function)}));
    } finally {close.mockRestore();}
  } finally {open.mockRestore();}
});
