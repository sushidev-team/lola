import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor } from "@testing-library/svelte";
import { flushSync, tick } from "svelte";

// One Proxy per service: anything a test does not name answers null, as an old daemon's field would.
const stop = vi.hoisted(() => vi.fn());
const handlers = vi.hoisted(() => new Map<string, (e: { data: unknown }) => void>());
vi.mock("@bindings/desktop", () => {
  const svc = (own: Record<string, unknown> = {}) => new Proxy(own, { get: (t, k: string) => t[k] ?? (async () => null) });
  return {
    SSHService: svc({ Timeline: async () => ({ connections: null, items: [], lastSeq: 0 }), Hosts: async () => ({ hosts: [] }) }),
    DaemonService: svc({ Alive: async () => true, Sessions: () => new Promise(() => {}), Projects: async () => ({}), Status: async () => null }),
    ConfigService: svc({ ConfigExists: async () => true, GetTheme: async () => "" }),
    TermService: svc({ Shells: async () => [], CaptureMany: async () => ({}) }),
    TalkService: svc({ State: async () => ({ caps: {}, composer: "unknown", feedSeq: 0, health: "ok" }), Stop: stop }),
    DoctorService: svc(),
    LinearService: svc(),
    UpdateService: svc(),
    GoalService: svc(),
    KnowledgeService: svc(),
  };
});
vi.mock("@wailsio/runtime", async (orig) => ({
  ...(await orig<object>()),
  Events: { On: (n: string, f: (e: { data: unknown }) => void) => (handlers.set(n, f), () => {}), Off: () => {} },
}));
vi.mock("$lib/components/LiveTerminal.svelte", async () => ({ default: (await import("$lib/components/ssh/TerminalStub.test.svelte")).default }));

class RO {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= RO as unknown as typeof ResizeObserver;

const { default: App } = await import("./App.svelte");
const { store } = await import("$lib/store.svelte");
const { nav } = await import("$lib/nav.svelte");
const { ssh } = await import("$lib/ssh.svelte");
const { terms, AGENT, DIFF, SSH } = await import("$lib/terms.svelte");
const { confirm } = await import("$lib/confirm.svelte");

const conn = { id: "c1", host: "prod", tier: "production", state: "connected", wheel: "agent", tabAlive: true, tab: "x-ssh-1" };
const plain = (id: string, o: Record<string, unknown> = {}) => ({ id, project: "p", title: id, tmuxName: id, worktree: "/w", agent: "claude", agentState: "idle", status: "idle", ...o });
let n = 0;
let A = "";
let B = "";

async function mount(sessions: unknown[]) {
  render(App);
  await tick();
  store.alive = true;
  store.configChecked = true;
  store.hasConfig = true;
  store.sessions = sessions as never;
  flushSync();
}
const press = (key: string, target: Element = document.body) => fireEvent.keyDown(target, { key, bubbles: true });
const chord = (action: string) => {
  handlers.get("app:session-action")?.({ data: action });
  flushSync();
};

beforeEach(() => {
  cleanup();
  n++;
  A = `a${n}`;
  B = `b${n}`;
  nav.closeOverlay();
  nav.view = "cockpit";
  nav.lens = "list";
  nav.focusedTerm = "";
  nav.returnLens = "";
  nav.selectedId = "";
  nav.scoped = false;
  nav.project = "";
  nav.triage = "";
  confirm.cancel();
  stop.mockReset().mockResolvedValue({ stopped: true });
});
afterEach(() => {
  nav.closeOverlay();
  confirm.cancel();
  cleanup();
});

describe("in a session", () => {
  it("? works in a session", async () => {
    await mount([plain(A)]);
    nav.openSession(A);
    flushSync();
    await press("?");
    expect(nav.overlay).toBe("help");
  });
  it("x does nothing in a session", async () => {
    await mount([plain(A)]);
    nav.openSession(A);
    flushSync();
    await press("x");
    expect(confirm.request).toBeNull();
    expect(nav.focusedTerm).toBe(A);
  });
  it("j does not move the list behind a session", async () => {
    await mount([plain(A), plain(B)]);
    nav.openSession(A);
    flushSync();
    await press("j");
    expect(nav.selectedId).toBe(A);
  });
  it("Enter does not close a session", async () => {
    await mount([plain(A)]);
    nav.openSession(A);
    flushSync();
    await press("Enter");
    expect(nav.focusedTerm).toBe(A);
  });
  it("Esc climbs one step", async () => {
    await mount([plain(A, { ssh: { connections: [conn], lastSeq: 0 } })]);
    terms.select(A, SSH);
    nav.openSession(A);
    flushSync();
    await tick();
    const field = document.querySelector("[data-action-zone] textarea") as HTMLElement;
    field.focus();
    await press("Escape", field);
    expect(document.activeElement).not.toBe(field);
    expect(nav.focusedTerm).toBe(A);
    await press("Escape");
    expect(nav.focusedTerm).toBe("");
  });
  it("Esc from Changes goes back to the view it came from, and then out", async () => {
    await mount([plain(A)]);
    nav.openSession(A);
    flushSync();
    terms.select(A, AGENT);
    terms.toggleDiff(A);
    flushSync();
    await press("Escape");
    expect(terms.activeTab(A)).toBe(AGENT);
    expect(nav.focusedTerm).toBe(A);
    // A terminal without the keyboard steps back too: to the conversation, and then out.
    await press("Escape");
    expect(terms.activeTab(A)).toBe(SSH);
    expect(nav.focusedTerm).toBe(A);
    await press("Escape");
    expect(nav.focusedTerm).toBe("");
  });
  it("leaves a terminal's Esc alone: it belongs to the agent", async () => {
    await mount([plain(A)]);
    nav.openSession(A);
    flushSync();
    const xterm = document.createElement("textarea");
    document.body.appendChild(xterm);
    await press("Escape", xterm);
    expect(nav.focusedTerm).toBe(A);
    xterm.remove();
  });
  it("< and > ring every cell of the strip", async () => {
    await mount([plain(A, { ssh: { connections: [conn], lastSeq: 0 } })]);
    terms.select(A, SSH);
    nav.openSession(A);
    flushSync();
    await press(">");
    expect(terms.activeTab(A)).toBe(AGENT);
    await press(">");
    expect(terms.activeTab(A)).toBe(DIFF);
    await press(">");
    expect(ssh.view(A).shellConn).toBe("c1");
    expect(ssh.view(A).narrow).toBe("terminal");
    await press("<");
    expect(terms.activeTab(A)).toBe(DIFF);
  });
});

describe("the chords", () => {
  it("⌘J lands on the card", async () => {
    await mount([plain(A), plain(B, { agentState: "idle", ssh: { connections: [conn], needsHuman: true, lastSeq: 0 } })]);
    const before = ssh.view(B).dockFocus;
    chord("next");
    expect(nav.focusedTerm).toBe(B);
    expect(terms.activeTab(B)).toBe(SSH);
    expect(ssh.view(B).dockFocus).toBe(before + 1);
  });
  it("⌘J says so when nothing waits, and goes nowhere", async () => {
    await mount([plain(A)]);
    nav.select(A);
    chord("next");
    expect(nav.focusedTerm).toBe("");
    expect(store.flash?.text).toBe("Nothing is waiting on you.");
  });
  it("⌘. targets the open session when the selection differs", async () => {
    await mount([plain(A, { agentState: "working" }), plain(B, { agentState: "working" })]);
    nav.openSession(A);
    nav.select(B);
    flushSync();
    chord("stop");
    await waitFor(() => expect(stop).toHaveBeenCalledWith({ session: A }));
  });
  it("⌘. shows the daemon's refusal", async () => {
    stop.mockRejectedValue(new Error("not at a turn"));
    await mount([plain(A, { agentState: "working" })]);
    nav.openSession(A);
    flushSync();
    chord("stop");
    await waitFor(() => expect(store.flash?.text).toContain("not at a turn"));
  });
  it("⌘. shows a refusal the daemon answered as data, in its own words", async () => {
    stop.mockResolvedValue({ stopped: false, why: "lola can't stop this agent from here. Use its terminal." });
    await mount([plain(A, { agentState: "working" })]);
    nav.openSession(A);
    flushSync();
    chord("stop");
    await waitFor(() => expect(store.flash?.text).toBe("lola can't stop this agent from here. Use its terminal."));
  });
  it("⌘⇧D toggles Changes and returns to the previous view", async () => {
    await mount([plain(A)]);
    nav.select(A);
    chord("changes");
    expect(nav.focusedTerm).toBe(A);
    expect(terms.activeTab(A)).toBe(DIFF);
    chord("changes");
    expect(terms.activeTab(A)).toBe(SSH);
  });
  it("⌘\\ needs no connection: it opens the session, and from a terminal goes to the conversation", async () => {
    await mount([plain(A)]);
    nav.select(A);
    chord("ssh-terminal");
    expect(nav.focusedTerm).toBe(A);
    expect(terms.activeTab(A)).toBe(AGENT);
    chord("ssh-terminal");
    expect(terms.activeTab(A)).toBe(SSH);
    expect(nav.focusedTerm).toBe(A);
  });
  it("⌘\\ from an agentless session's terminal goes back out", async () => {
    await mount([plain(A, { location: "temporary", status: "shell", agentState: "shell" })]);
    nav.select(A);
    chord("ssh-terminal");
    expect(terms.activeTab(A)).toBe(AGENT);
    chord("ssh-terminal");
    expect(nav.focusedTerm).toBe("");
  });
  it("⌘\\ from Changes goes where its footer says: the conversation", async () => {
    await mount([plain(A)]);
    nav.openSession(A);
    terms.select(A, AGENT);
    terms.toggleDiff(A);
    flushSync();
    chord("ssh-terminal");
    expect(terms.activeTab(A)).toBe(SSH);
  });
  it("⌘\\ goes between a conversation and the terminal last used", async () => {
    await mount([plain(A, { ssh: { connections: [conn], lastSeq: 0 } })]);
    terms.select(A, SSH);
    nav.openSession(A);
    flushSync();
    chord("ssh-terminal");
    expect(terms.activeTab(A)).toBe(AGENT);
    chord("ssh-terminal");
    expect(terms.activeTab(A)).toBe(SSH);
    expect(nav.focusedTerm).toBe(A);
  });
  it("⌘L needs no connection, and from a terminal goes to the conversation", async () => {
    await mount([plain(A, { ssh: { connections: [conn], lastSeq: 0 } }), plain(B)]);
    terms.select(A, AGENT);
    nav.openSession(A);
    flushSync();
    const before = ssh.view(A).composerFocus;
    chord("ssh-talk");
    expect(terms.activeTab(A)).toBe(SSH);
    expect(ssh.view(A).composerFocus).toBeGreaterThan(before);
    nav.openSession(B);
    flushSync();
    chord("ssh-talk");
    expect(store.flash?.text ?? "").not.toContain("no SSH connection");
    expect(nav.focusedTerm).toBe(B);
  });
});

describe("in Overview", () => {
  it("i in Overview bumps no focus counter", async () => {
    await mount([plain(A, { ssh: { connections: [conn], lastSeq: 0 } })]);
    nav.select(A);
    terms.select(A, SSH);
    const v = ssh.view(A);
    const before = [v.composerFocus, v.streamFocus, v.findFocus, v.filter];
    for (const k of ["i", "t", "F", "/"]) await press(k);
    expect([v.composerFocus, v.streamFocus, v.findFocus, v.filter]).toEqual(before);
    expect(nav.focusedTerm).toBe("");
  });
  it("s in Overview opens the session", async () => {
    await mount([plain(A)]);
    nav.select(A);
    const shell = vi.spyOn(terms, "newShell").mockImplementation(() => {});
    await press("s");
    expect(nav.focusedTerm).toBe(A);
    expect(shell).toHaveBeenCalledWith(A, "/w");
    shell.mockRestore();
  });
  it("f and h open the session too, and < does nothing", async () => {
    await mount([plain(A)]);
    nav.select(A);
    await press("<");
    expect(nav.focusedTerm).toBe("");
    await press("f");
    expect(nav.focusedTerm).toBe(A);
    expect(terms.activeTab(A)).toBe(DIFF);
    nav.closeSession();
    terms.select(A, AGENT);
    await press("h");
    expect(nav.focusedTerm).toBe(A);
  });
  it("n selects the next row waiting and stays in Overview", async () => {
    await mount([plain(A), plain(B, { agentState: "waiting_input", status: "needs_input" })]);
    nav.select(A);
    flushSync();
    await press("n");
    expect(nav.selectedId).toBe(B);
    expect(nav.focusedTerm).toBe("");
  });
  it("Enter opens and j moves", async () => {
    await mount([plain(A), plain(B)]);
    nav.select(A);
    await press("j");
    expect(nav.selectedId).toBe(B);
    await press("Enter");
    expect(nav.focusedTerm).toBe(B);
  });
});
