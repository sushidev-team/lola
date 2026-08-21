import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";
import SessionMenu from "./SessionMenu.svelte";
import SessionsTable from "./SessionsTable.svelte";
import SessionEmbed from "./SessionEmbed.svelte";
import { store, type SessionInfo } from "$lib/store.svelte";
import type { ProjectInfo } from "@bindings/internal/protocol";
import { nav } from "$lib/nav.svelte";
import { sessionMenu } from "$lib/sessionmenu.svelte";
import { confirm } from "$lib/confirm.svelte";

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
    sessionMenu.close();
    confirm.cancel();
  });

  it("renders nothing without a pending request", () => {
    render(SessionMenu);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("offers add shell and trigger review for a live session", () => {
    sessionMenu.request = { id: "acme-eng-1", x: 10, y: 10 };
    render(SessionMenu);
    expect(screen.getByRole("menu")).toBeInTheDocument();
    // By ROLE + accessible name, not by text: a <MenuItem> wraps its label in a
    // span beside an aria-hidden glyph, so getByText would hand back the span
    // and `toBeEnabled` would assert on the wrong node.
    expect(screen.getByRole("menuitem", { name: "Add shell" })).toBeEnabled();
    expect(screen.getByRole("menuitem", { name: "Trigger review" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "CodeRabbit" })).toBeInTheDocument();
    // No PR observed and not dead: the conditional items stay hidden.
    expect(screen.queryByRole("menuitem", { name: "Open PR" })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Revive" })).not.toBeInTheDocument();
  });

  it("disables add shell when the session has no worktree", () => {
    store.sessions = [fakeSession({ worktree: "" })];
    sessionMenu.request = { id: "acme-eng-1", x: 10, y: 10 };
    render(SessionMenu);
    expect(screen.getByRole("menuitem", { name: "Add shell" })).toBeDisabled();
  });

  it("shows open PR and revive only when the session state warrants them", () => {
    store.sessions = [fakeSession({ prNumber: 7, prUrl: "https://x/pr/7", status: "dead" })];
    sessionMenu.request = { id: "acme-eng-1", x: 10, y: 10 };
    render(SessionMenu);
    expect(screen.getByRole("menuitem", { name: "Open PR" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Revive" })).toBeInTheDocument();
  });

  it("omits the dev toggle for a project with no dev_commands", () => {
    sessionMenu.request = { id: "acme-eng-1", x: 10, y: 10 };
    render(SessionMenu);
    expect(screen.queryByRole("menuitem", { name: /dev/i })).not.toBeInTheDocument();
  });

  it("offers 'Make active' when the project has dev_commands, and reads back the running state", () => {
    store.sessions = [fakeSession({ devCommands: ["composer dev"], devActive: false })];
    sessionMenu.request = { id: "acme-eng-1", x: 10, y: 10 };
    render(SessionMenu);
    expect(screen.getByRole("menuitem", { name: "Make active" })).toBeInTheDocument();

    cleanup();
    store.sessions = [fakeSession({ devCommands: ["composer dev"], devActive: true })];
    sessionMenu.request = { id: "acme-eng-1", x: 10, y: 10 };
    render(SessionMenu);
    expect(screen.getByRole("menuitem", { name: "Active — stop dev" })).toBeInTheDocument();
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
    sessionMenu.close();
  });

  it("right-clicking a table row selects the session and opens the menu at the pointer", async () => {
    render(SessionsTable);
    const row = screen.getByText("ENG-1").closest("tr")!;
    await fireEvent.contextMenu(row, { clientX: 42, clientY: 24 });
    expect(nav.selectedId).toBe("acme-eng-1");
    expect(sessionMenu.request).toEqual({ id: "acme-eng-1", x: 42, y: 24 });
  });
});

describe("SessionEmbed header", () => {
  beforeEach(() => {
    cleanup();
    store.sessions = [];
    store.projects = [];
    store.connected = true;
    store.alive = true;
    nav.scoped = false;
    nav.project = "";
    nav.selectedId = "";
  });

  it("renders the agent kind chip for a session with agent: 'codex'", () => {
    store.sessions = [fakeSession({ id: "acme-eng-1", agent: "codex" })];
    render(SessionEmbed, { props: { sessionId: "acme-eng-1" } });
    expect(screen.getByText("codex")).toBeInTheDocument();
  });

  it("renders claude for an older session with empty agent", () => {
    store.sessions = [fakeSession({ id: "acme-eng-1", agent: "" })];
    render(SessionEmbed, { props: { sessionId: "acme-eng-1" } });
    expect(screen.getByText("claude")).toBeInTheDocument();
  });

  it("omits the agent kind chip for a shell session", () => {
    store.sessions = [fakeSession({ id: "acme-eng-1", status: "shell", agent: "codex" })];
    render(SessionEmbed, { props: { sessionId: "acme-eng-1" } });
    expect(screen.queryByText("codex")).not.toBeInTheDocument();
    expect(screen.queryByText("claude")).not.toBeInTheDocument();
  });
});
