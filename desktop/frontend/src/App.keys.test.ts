import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor, screen } from "@testing-library/svelte";
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
    ProjectGitService: svc(),
    DesktopContextService: svc(),
  };
});
vi.mock("@bindings/desktop/contextservice", () => ({Publish: vi.fn().mockResolvedValue(undefined)}));
vi.mock("@wailsio/runtime", async (orig) => ({
  ...(await orig<object>()),
  Window: {SetTitle: vi.fn().mockResolvedValue(undefined)},
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
  store.sessions = [];
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
  flushSync();
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
  nav.focusedTerm = "";
  nav.selectedId = "";
  nav.scoped = false;
  nav.project = "";
  nav.lastGitProject = "";
  nav.triage = "";
  nav.sidebarQuery = "";
  nav.sidebarBrowsing = false;
  confirm.cancel();
  stop.mockReset().mockResolvedValue({ stopped: true });
});
afterEach(() => {
  nav.closeOverlay();
  confirm.cancel();
  cleanup();
});

describe("project workspace toolbar", () => {
  it("closes Go To and opens the last Git repository when there is no current project", async () => {
    await mount([]);
    store.projects = [{name:'p',path:'/repo'},{name:'other',path:'/other'}] as typeof store.projects;
    nav.rememberGitProject('p');
    nav.goHome();
    nav.project = '';
    nav.openOverlay('commands');
    chord('toggle-git');
    expect(nav.overlay).toBeNull();
    expect(nav.inGit).toBe(true);
    expect(nav.project).toBe('p');
    chord('toggle-git');
    expect(nav.view).toBe('home');
    expect(nav.project).toBe('');
  });
  it("uses the current session's repository before the remembered Git repository", async () => {
    await mount([plain(A)]);
    store.projects = [{name:'p',path:'/repo'},{name:'other',path:'/other'}] as typeof store.projects;
    nav.rememberGitProject('other');
    nav.openSession(A);
    chord('toggle-git');
    expect(nav.project).toBe('p');
  });
  it("opens Projects on first use without project context, even with only one repository", async () => {
    await mount([]);
    store.projects = [{name:'only',path:'/repo'}] as typeof store.projects;
    nav.goHome();
    chord('toggle-git');
    expect(nav.view).toBe('home');
    expect(nav.sidebarTab).toBe('projects');
    expect(nav.overlay).toBeNull();
  });
  it("toggles Git from the open session and returns with the active tab intact", async () => {
    await mount([plain(A)]);
    store.projects = [{name:'p',path:'/repo',agentOk:true}] as typeof store.projects;
    nav.openSession(A);
    terms.select(A, DIFF);
    chord('toggle-git');
    expect(nav.inGit).toBe(true);
    expect(nav.project).toBe('p');
    chord('toggle-git');
    expect(nav.focusedTerm).toBe(A);
    expect(nav.view).toBe('cockpit');
    expect(terms.activeTab(A)).toBe(DIFF);
  });
  it("blocks workspace shortcuts in a confirmation and never opens Go To as a fallback", async () => {
    await mount([]);
    store.projects = [];
    nav.selectedId = '';
    nav.project = '';
    confirm.ask({title:'Confirm',body:'Keep this open',confirmLabel:'OK',onConfirm:()=>{}});
    chord('toggle-git');
    expect(nav.view).toBe('cockpit');
    expect(confirm.request?.title).toBe('Confirm');
    confirm.cancel();
    chord('toggle-git');
    expect(nav.overlay).toBeNull();
    expect(nav.view).toBe('home');
    expect(nav.sidebarTab).toBe('projects');
  });
  it("opens global help and focuses the sessions sidebar through native actions", async () => {
    await mount([plain(A)]);
    nav.sidebarOpen = false;
    chord('focus-sessions');
    expect(nav.sidebarOpen).toBe(true);
    expect(nav.sidebarTab).toBe('sessions');
    expect(nav.sidebarBrowsing).toBe(true);
    chord('shortcuts');
    expect(nav.overlay).toBe('help');
  });
  it("does not mistake a stack with a saved Git tab for a repository", async () => {
    await mount([]);
    store.projects = [{name:'stack',kind:'stack',path:'/stack'}] as typeof store.projects;
    nav.goDetail('stack', 'git');
    chord('toggle-git');
    expect(nav.overlay).toBeNull();
    expect(nav.view).toBe('home');
    expect(nav.sidebarTab).toBe('projects');
    expect(nav.project).toBe('stack');
    expect(nav.projectTab).toBe('overview');
  });
  it("routes native Find to Git history only while that view is active", async () => {
    await mount([]);
    const initial = nav.gitSearchRequest;
    chord("find-project-history");
    expect(nav.gitSearchRequest).toBe(initial);
    nav.goDetail("missing-project", "git");
    chord("find-project-history");
    expect(nav.gitSearchRequest).toBe(initial + 1);
    nav.openOverlay("settings");
    chord("find-project-history");
    expect(nav.gitSearchRequest).toBe(initial + 1);
    nav.closeOverlay();
    nav.projectTab = "overview";
  });
  it("replaces the shared header and restores navigation for a missing project", async () => {
    await mount([]);
    store.projects = [{ name: "workspace", label: "Workspace", path: "/repo", agentOk: true }] as typeof store.projects;
    nav.sidebarOpen = false;
    nav.goDetail("workspace");
    flushSync();
    expect(screen.getByRole("banner", { name: "Project toolbar" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Back to sessions" })).toBeNull();
    nav.goDetail("missing-project");
    flushSync();
    expect(screen.getByRole("button", { name: "Back to sessions" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Show sidebar" })).toBeInTheDocument();
    nav.sidebarOpen = true;
  });
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
    expect(nav.focusedTerm).toBe(A);
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
    expect(nav.focusedTerm).toBe(A);
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
    expect(nav.focusedTerm).toBe(A);
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
    expect(nav.focusedTerm).toBe(A);
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

describe("in the sessions sidebar", () => {
  const sidebarRow = (id: string) => document.querySelector(`[data-roster-row="${id}"]`) as HTMLElement;
  it("opens the first session automatically without rendering the old views", async () => {
    await mount([plain(A)]);
    expect(nav.focusedTerm).toBe(A);
    expect(document.querySelector('[data-session-list]')).toBeNull();
    expect(document.querySelector('[aria-label="List lens"]')).toBeNull();
  });
  it("arrow keys open sessions in sidebar order and retain keyboard focus", async () => {
    await mount([plain(A), plain(B)]);
    const first = sidebarRow(A);
    first.focus();
    await press('ArrowDown', first);
    expect(nav.focusedTerm).toBe(B);
    await tick();
    expect(document.activeElement).toBe(sidebarRow(B));
  });
  it("leaves modifier chords alone in the sidebar", async () => {
    await mount([plain(A), plain(B)]);
    nav.openSession(B, true);
    flushSync();
    for (const key of ['k', 'g', 'ArrowUp']) {
      await fireEvent.keyDown(sidebarRow(B), { key, metaKey: true, bubbles: true });
      expect(nav.focusedTerm).toBe(B);
    }
  });
  it("keeps global Help and sidebar shortcuts available on a row", async () => {
    await mount([plain(A)]);
    await press('?', sidebarRow(A));
    expect(nav.overlay).toBe('help');
    nav.closeOverlay();
    const before = nav.sidebarOpen;
    chord('toggle-sidebar');
    expect(nav.sidebarOpen).toBe(!before);
    nav.sidebarOpen = true;
  });
  it("filtering the sidebar does not replace the open session", async () => {
    await mount([plain(A), plain(B)]);
    nav.sidebarQuery = B;
    flushSync();
    expect(sidebarRow(A)).toBeNull();
    expect(sidebarRow(B)).not.toBeNull();
    expect(nav.focusedTerm).toBe(A);
  });
  it("native New Shell opens a shell for the selected session", async () => {
    await mount([plain(A)]);
    const shell = vi.spyOn(terms, 'newShell').mockImplementation(() => {});
    chord('new-shell');
    expect(shell).toHaveBeenCalledWith(A, '/w');
    shell.mockRestore();
  });
});
