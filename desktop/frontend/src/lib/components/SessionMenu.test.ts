import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";
import SessionMenu from "./SessionMenu.svelte";
import SidebarRoster from "./SidebarRoster.svelte";
import SessionBar from "./session/SessionBar.svelte";
import { store, type SessionInfo } from "$lib/store.svelte";
import type { ProjectInfo } from "@bindings/internal/protocol";
import { nav } from "$lib/nav.svelte";
import { sessionMenu } from "$lib/sessionmenu.svelte";
import { confirm } from "$lib/confirm.svelte";
import { flushSync } from "svelte";

vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    public options = {};
    public loadAddon = vi.fn();
    public open = vi.fn();
    public onData = vi.fn();
    public onResize = vi.fn();
    public focus = vi.fn();
    public attachCustomKeyEventHandler = vi.fn();
    public attachCustomWheelEventHandler = vi.fn();
    public refresh = vi.fn();
    public write = vi.fn();
    public writeln = vi.fn();
    public dispose = vi.fn();
  },
}));
vi.mock("@xterm/addon-fit", () => ({
  FitAddon: class {
    public fit = vi.fn();
    public activate = vi.fn();
    public dispose = vi.fn();
  },
}));
vi.mock("@xterm/addon-webgl", () => ({
  WebglAddon: class {
    public onContextLoss = vi.fn();
    public clearTextureAtlas = vi.fn();
    public dispose = vi.fn();
  },
}));
vi.mock("@xterm/addon-web-links", () => ({
  WebLinksAddon: class {
    public activate = vi.fn();
    public dispose = vi.fn();
  },
}));

class StubResizeObserver {
  public observe(): void {}
  public unobserve(): void {}
  public disconnect(): void {}
}
globalThis.ResizeObserver ??= StubResizeObserver as unknown as typeof ResizeObserver;

function fakeSession(over: Partial<SessionInfo> = {}): SessionInfo {
  return {
    id: "acme-eng-1",
    issue: "ENG-1",
    title: "fix login",
    project: "acme",
    status: "working",
    reacting: "",
    age: "2m",
    prNumber: 0,
    prUrl: "",
    worktree: "/tmp/wt/acme-eng-1",
    tmuxName: "acme-eng-1",
    ...over,
  } as SessionInfo;
}

// The right-click menu is the one place every session surface shares its
// actions; these pin what it offers and that it opens/closes the same way
// everywhere.
describe("SessionMenu", () => {
  beforeEach(() => {
    cleanup();
    store.sessions = [fakeSession()];
    store.projects = [];
    store.connected = true;
    store.alive = true;
    nav.scoped = false;
    nav.project = "";
    nav.selectedId = "";
    nav.sidebarQuery = "";
    nav.triage = "";
    sessionMenu.close();
    confirm.cancel();
  });

  it("renders nothing without a pending request", () => {
    render(SessionMenu);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });



  it("keeps the menu focused on session management even when other features are available", () => {
    store.sessions = [fakeSession({ agent: 'claude', prNumber: 7, prUrl: 'https://example.test/pr/7', devActive: true, devCommands: ['npm run dev'], devUrls: ['http://localhost:3000'], board: { phase: 'done' } as never })];
    nav.focusedTerm = 'acme-eng-1';
    sessionMenu.request = { id: 'acme-eng-1', x: 10, y: 10 };
    render(SessionMenu);
    expect(screen.getAllByRole('menuitem').map(item => item.textContent?.trim())).toEqual([
      'Rename session…', 'Open in Finder', 'Switch to codex', 'Switch to opencode', 'Kill…',
    ]);
  });

  it("Open in Finder opens the session's folder", async () => {
    sessionMenu.request = { id: "acme-eng-1", x: 10, y: 10 };
    const open = vi.spyOn(store, "openFolder").mockResolvedValue();
    render(SessionMenu);
    await fireEvent.click(screen.getByRole("menuitem", { name: "Open in Finder" }));
    expect(open).toHaveBeenCalledWith("/tmp/wt/acme-eng-1");
    open.mockRestore();
  });

  it("offers revive for ended sessions while PR access stays outside the menu", () => {
    store.sessions = [fakeSession({ prNumber: 7, prUrl: "https://x/pr/7", status: "dead" })];
    sessionMenu.request = { id: "acme-eng-1", x: 10, y: 10 };
    render(SessionMenu);
    expect(screen.queryByRole("menuitem", { name: "Open PR" })).toBeNull();
    expect(screen.getByRole("menuitem", { name: "Revive" })).toBeInTheDocument();
  });

  it("offers 'Resolve conflicts' only for a conflicting session, naming the branch", () => {
    sessionMenu.request = { id: "acme-eng-1", x: 10, y: 10 };
    render(SessionMenu);
    expect(screen.queryByRole("menuitem", { name: "Resolve conflicts" })).not.toBeInTheDocument();

    cleanup();
    store.projects = [{ name: "acme", defaultBranch: "develop" } as ProjectInfo];
    store.sessions = [fakeSession({ status: "merge_conflict", prNumber: 7, prUrl: "https://x/pr/7" })];
    sessionMenu.request = { id: "acme-eng-1", x: 10, y: 10 };
    render(SessionMenu);
    const item = screen.getByRole("menuitem", { name: "Resolve conflicts" });
    expect(item).toHaveAttribute("title", expect.stringContaining("develop"));
  });

  it("routes kill through the shared confirm dialog and closes first", async () => {
    sessionMenu.request = { id: "acme-eng-1", x: 10, y: 10 };
    render(SessionMenu);
    await fireEvent.click(screen.getByRole("menuitem", { name: "Kill…" }));
    expect(sessionMenu.request).toBeNull();
    expect(confirm.request?.title).toBe("Kill session?");
  });

  it("offers switch agent options for the other kinds and confirms before switching", async () => {
    store.sessions = [fakeSession({ agent: "claude" })];
    sessionMenu.request = { id: "acme-eng-1", x: 10, y: 10 };
    render(SessionMenu);

    expect(screen.getByRole("menuitem", { name: "Switch to codex" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Switch to opencode" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Switch to claude" })).not.toBeInTheDocument();

    await fireEvent.click(screen.getByRole("menuitem", { name: "Switch to codex" }));
    expect(sessionMenu.request).toBeNull();
    expect(confirm.request?.title).toBe("Switch agent?");
    expect(confirm.request?.body).toBe("Switch ENG-1 from claude to codex?");
    expect(confirm.request?.detail).toBe("The pane is replaced on the same worktree.");
    expect(confirm.request?.confirmLabel).toBe("Switch");
  });

  it("updates switch agent options when session runs codex", () => {
    store.sessions = [fakeSession({ agent: "codex" })];
    sessionMenu.request = { id: "acme-eng-1", x: 10, y: 10 };
    render(SessionMenu);

    expect(screen.getByRole("menuitem", { name: "Switch to claude" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Switch to opencode" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Switch to codex" })).not.toBeInTheDocument();
  });

  it("omits switch agent options for shell sessions", () => {
    store.sessions = [fakeSession({ status: "shell" })];
    sessionMenu.request = { id: "acme-eng-1", x: 10, y: 10 };
    render(SessionMenu);

    expect(screen.queryByRole("menuitem", { name: /switch to/i })).not.toBeInTheDocument();
  });

  it("dismisses on a backdrop click", async () => {
    sessionMenu.request = { id: "acme-eng-1", x: 10, y: 10 };
    render(SessionMenu);
    await fireEvent.click(screen.getByRole("presentation"));
    expect(sessionMenu.request).toBeNull();
  });

  it("renders nothing when the session vanished from the store", () => {
    sessionMenu.request = { id: "gone", x: 10, y: 10 };
    render(SessionMenu);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });
});

describe("session surfaces open the menu", () => {
  beforeEach(() => {
    cleanup();
    store.sessions = [fakeSession()];
    store.connected = true;
    store.alive = true;
    nav.scoped = false;
    nav.project = "";
    nav.selectedId = "";
    nav.sidebarQuery = "";
    nav.triage = "";
    sessionMenu.close();
  });

  it("right-clicking a sidebar row selects the session and opens the menu at the pointer", async () => {
    render(SidebarRoster);
    const row = screen.getByText("ENG-1").closest("[data-roster-row]")!;
    await fireEvent.contextMenu(row, { clientX: 42, clientY: 24 });
    expect(nav.selectedId).toBe("acme-eng-1");
    expect(sessionMenu.request).toEqual({ id: "acme-eng-1", x: 42, y: 24 });
  });
});

// The filled agent chip left the header, and the agent's name went to the title's hover card.
describe("SessionBar hover card", () => {
  beforeEach(() => {
    cleanup();
    store.sessions = [];
    store.projects = [];
    store.connected = true;
    store.alive = true;
    nav.scoped = false;
    nav.project = "";
    nav.selectedId = "";
    nav.sidebarQuery = "";
    nav.triage = "";
  });

  async function hover() {
    render(SessionBar, { props: { sessionId: "acme-eng-1" } });
    await fireEvent.pointerEnter(document.querySelector("[data-session-bar] [tabindex='0']")!);
  }

  it("names the agent for a session with agent: 'codex', with no chip at rest", async () => {
    store.sessions = [fakeSession({ id: "acme-eng-1", agent: "codex" })];
    await hover();
    expect(await screen.findByText("Codex")).toBeInTheDocument();
  });

  it("reads as Claude for an older session with empty agent", async () => {
    store.sessions = [fakeSession({ id: "acme-eng-1", agent: "" })];
    await hover();
    expect(await screen.findByText("Claude")).toBeInTheDocument();
  });

  it("names no agent for a shell session", async () => {
    store.sessions = [fakeSession({ id: "acme-eng-1", status: "shell", agent: "codex" })];
    await hover();
    await screen.findByText("Worktree");
    expect(screen.queryByText("Codex")).not.toBeInTheDocument();
    expect(screen.queryByText("Claude")).not.toBeInTheDocument();
  });
});
