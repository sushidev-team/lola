// Behaviour across the session view that only shows when real pieces are mounted together: the App,
// the store's push events and each view's focus requests. Every test here started as a reproduction
// of something that went wrong for a person, with the daemon's answers arriving when they really do.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { waitFor, screen, render, cleanup, fireEvent } from "@testing-library/svelte";
import { flushSync, tick } from "svelte";

const h = vi.hoisted(() => ({
  handlers: new Map<string, (e: { data: unknown }) => void>(),
  state: { impl: null as null | ((a: unknown) => Promise<unknown>) },
  timeline: { impl: null as null | ((a: unknown) => Promise<unknown>), calls: 0 },
  say: { impl: null as null | ((a: unknown) => Promise<unknown>), calls: [] as unknown[] },
  shells: { impl: null as null | ((id: string) => Promise<string[]>) },
  stop: { calls: [] as unknown[] },
  feed: { impl: null as null | ((a: unknown) => Promise<unknown>), calls: 0 },
  run: { calls: [] as unknown[] },
  ended: { rows: [] as unknown[] },
}));

vi.mock("@bindings/desktop", () => {
  const svc = (own: Record<string, unknown> = {}) => new Proxy(own, { get: (t, k: string) => t[k] ?? (async () => null) });
  return {
    SSHService: svc({
      Timeline: (a: unknown) => {
        h.timeline.calls++;
        return h.timeline.impl ? h.timeline.impl(a) : Promise.resolve({ connections: null, items: [], lastSeq: 0 });
      },
      Hosts: async () => ({ hosts: [] }),
      RunCommand: async (a: unknown) => {
        h.run.calls.push(a);
        return { id: "" };
      },
      Trash: async () => ({}),
    }),
    DaemonService: svc({ Alive: async () => true, Sessions: () => new Promise(() => {}), Projects: async () => ({}), Status: async () => null, Diff: async () => null }),
    ConfigService: svc({ ConfigExists: async () => true, GetTheme: async () => "" }),
    TermService: svc({ Shells: (id: string) => (h.shells.impl ? h.shells.impl(id) : Promise.resolve([])), CaptureMany: async () => ({}), Detach: async () => {}, Attach: async () => {}, Shell: async () => "" }),
    TalkService: svc({
      State: (a: unknown) => (h.state.impl ? h.state.impl(a) : Promise.resolve({ caps: { feed: true, send: true, stop: true, cards: true }, composer: "resting", feedSeq: 0, health: "ok" })),
      Stop: async (a: unknown) => {
        h.stop.calls.push(a);
        return { stopped: true };
      },
      Say: (a: unknown) => {
        h.say.calls.push(a);
        return h.say.impl ? h.say.impl(a) : Promise.resolve({ item: "x", delivered: true });
      },
      Feed: (a: unknown) => {
        h.feed.calls++;
        return h.feed.impl ? h.feed.impl(a) : Promise.resolve({ items: [], lastSeq: 0, epoch: "e", health: "ok" });
      },
      Ended: async () => ({ sessions: h.ended.rows }),
    }),
    DoctorService: svc(),
    LinearService: svc(),
    UpdateService: svc(),
    GoalService: svc(),
    KnowledgeService: svc(),
  };
});
vi.mock("@wailsio/runtime", async (orig) => ({
  ...(await orig<object>()),
  Events: { On: (n: string, f: (e: { data: unknown }) => void) => (h.handlers.set(n, f), () => {}), Off: () => {} },
}));
vi.mock("$lib/components/LiveTerminal.svelte", async () => ({ default: (await import("$lib/components/ssh/TerminalStub.test.svelte")).default }));

class RO {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= RO as unknown as typeof ResizeObserver;
globalThis.IntersectionObserver ??= RO as unknown as typeof IntersectionObserver;
(Element.prototype as unknown as { getAnimations: () => unknown[] }).getAnimations ??= () => [];

const { default: App } = await import("../../../App.svelte");
const { default: SidebarRoster } = await import("$lib/components/SidebarRoster.svelte");
const { store } = await import("$lib/store.svelte");
const { nav } = await import("$lib/nav.svelte");
const { ssh } = await import("$lib/ssh.svelte");
const { talk } = await import("$lib/talk.svelte");
const { SessionFeed } = await import("$lib/feed.svelte");
const { terms, AGENT, DIFF, SSH } = await import("$lib/terms.svelte");
const { rosterLine } = await import("$lib/sessionview");
const { confirm } = await import("$lib/confirm.svelte");
const { sessionMenu } = await import("$lib/sessionmenu.svelte");

type Fields = Record<string, unknown>;
const plain = (id: string, o: Fields = {}) => ({ id, project: "p", title: id, tmuxName: id, worktree: "/w", agent: "claude", agentState: "idle", status: "idle", ...o });
const host = (id: string, o: Fields = {}) => ({ id, host: id, alias: id, tier: "dev", state: "connected", wheel: "agent", tabAlive: true, tab: `x-${id}`, ...o });
const perm = { kind: "permission", hash: "h1", lines: ["Bash command", "ls", "Do you want to proceed?"], choices: [{ key: "1", label: "Yes" }, { key: "2", label: "No" }] };
const answer = (o: Fields = {}) => ({ caps: { feed: true, send: true, stop: true, cards: true }, composer: "resting", feedSeq: 0, health: "ok", ...o });

let n = 0;
let A = "";
let B = "";
async function mount(sessions: unknown[]) {
  store.alive = true;
  h.handlers.get("daemon:sessions")?.({ data: { sessions: [] } });
  store.sessions = [];
  render(App);
  await tick();
  store.alive = true;
  store.configChecked = true;
  store.hasConfig = true;
  store.sessions = sessions as never;
  flushSync();
}
const settle = async () => {
  for (let i = 0; i < 6; i++) {
    await tick();
    await new Promise((r) => setTimeout(r, 5));
  }
  flushSync();
};
const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));
const press = (key: string, target: Element = document.body) => fireEvent.keyDown(target, { key, bubbles: true });
const chord = (a: string) => {
  h.handlers.get("app:session-action")?.({ data: a });
  flushSync();
};
const field = () => document.querySelector("[data-composer] textarea") as HTMLTextAreaElement | null;
const terminalName = () => document.querySelector('[data-testid="terminal"]')?.getAttribute("data-name");
const open = async (id: string) => {
  nav.openSession(id);
  flushSync();
  await settle();
};

beforeEach(() => {
  cleanup();
  n++;
  A = `logic-a${n}`;
  B = `logic-b${n}`;
  nav.closeOverlay();
  nav.view = "cockpit";
  nav.focusedTerm = "";
  nav.selectedId = "";
  nav.scoped = false;
  nav.project = "";
  nav.triage = "";
  nav.sidebarQuery = "";
  nav.sidebarBrowsing = false;
  confirm.cancel();
  sessionMenu.close();
  h.state.impl = null;
  h.timeline.impl = null;
  h.timeline.calls = 0;
  h.say.impl = null;
  h.say.calls = [];
  h.shells.impl = null;
  h.stop.calls = [];
  h.feed.impl = null;
  h.feed.calls = 0;
  h.run.calls = [];
  h.ended.rows = [];
  terms.setGlobalView("conversation");
  store.retainAll = false;
  localStorage.clear();
});
afterEach(() => cleanup());

describe("which host takes the keyboard", () => {
  it("a Run under a scope opens that host's shell, whichever was looked at before", async () => {
    const prod = host("prod", { tier: "production" });
    const stg = host("stg", { tier: "staging", termTracked: true, termAtPrompt: true });
    await mount([plain(A, { ssh: { connections: [prod, stg], lastSeq: 0 } })]);
    await open(A);
    ssh.selectCell(A, { key: "host:prod", kind: "host", conn: prod as never, label: "prod" });
    flushSync();
    await settle();
    expect(terminalName()).toBe("x-prod");
    ssh.selectCell(A, { key: "conversation", kind: "conversation" });
    flushSync();
    await settle();
    await fireEvent.click(document.querySelector('[data-host-chip="stg"] button') as HTMLElement);
    flushSync();
    await settle();
    expect(ssh.view(A).shellConn).toBe("stg");
    const f = field()!;
    await fireEvent.input(f, { target: { value: "vim x" } });
    await fireEvent.keyDown(f, { key: "Enter", metaKey: true });
    await settle();
    expect(h.run.calls).toEqual([expect.objectContaining({ conn: "stg", command: "vim x" })]);
    expect(terminalName()).toBe("x-stg");
  });

  it("showTerminal with no host named goes to the active connection, not a stale shell", async () => {
    const prod = host("prod");
    const stg = host("stg");
    await mount([plain(A, { ssh: { connections: [prod, stg], lastSeq: 0 } })]);
    await open(A);
    const v = ssh.view(A);
    v.shellConn = "prod";
    v.activeConn = "stg";
    ssh.showTerminal(A);
    flushSync();
    await settle();
    expect(v.shellConn).toBe("stg");
    expect(terminalName()).toBe("x-stg");
  });

  it("⌘\\ under All still returns to the last host after the conversation", async () => {
    const prod = host("prod");
    const stg = host("stg");
    const s = plain(A, { ssh: { connections: [prod, stg], lastSeq: 0 } });
    await mount([s]);
    await open(A);
    ssh.selectCell(A, { key: "host:prod", kind: "host", conn: prod as never, label: "prod" });
    ssh.selectCell(A, { key: "conversation", kind: "conversation" });
    ssh.toggleTerminalView(s as never);
    flushSync();
    await settle();
    expect(terminalName()).toBe("x-prod");
  });
});

describe("a rebuilt reader", () => {
  it("a sequence lower than the one held is a reset, not nothing new", async () => {
    const f = new SessionFeed("x");
    h.feed.impl = () => Promise.resolve({ items: [], lastSeq: 100, epoch: "e1", health: "ok" });
    f.sync(100);
    await settle();
    h.feed.impl = () => Promise.resolve({ items: [{ id: "new", order: 1, seq: 51 }], lastSeq: 51, epoch: "e2", health: "ok" });
    f.sync(51);
    await settle();
    expect(f.items.map((i) => i.id)).toEqual(["new"]);
    h.feed.impl = () => Promise.resolve({ items: [{ id: "next", order: 2, seq: 52 }], lastSeq: 52, epoch: "e2", health: "ok" });
    f.sync(52);
    await settle();
    expect(f.items.map((i) => i.id)).toEqual(["new", "next"]);
  });

  it("a new epoch fetches even when the sequence has not gone down", async () => {
    const f = new SessionFeed("x");
    h.feed.impl = () => Promise.resolve({ items: [{ id: "old", order: 1, seq: 10 }], lastSeq: 10, epoch: "e1", health: "ok" });
    f.sync(10, "e1");
    await settle();
    const before = h.feed.calls;
    f.sync(10, "e1");
    await settle();
    expect(h.feed.calls).toBe(before);
    h.feed.impl = () => Promise.resolve({ items: [{ id: "fresh", order: 1, seq: 10 }], lastSeq: 10, epoch: "e2", health: "ok" });
    f.sync(10, "e2");
    await settle();
    expect(f.items.map((i) => i.id)).toEqual(["fresh"]);
  });
});

describe("a focus request waits for what it is about", () => {
  it("⌘J keeps its request until the card arrives, one round trip later", async () => {
    h.state.impl = () => new Promise((r) => setTimeout(() => r(answer({ composer: "prompt", prompt: perm })), 30));
    await mount([plain(A, { agentState: "waiting_input", inputReason: "permission_prompt" })]);
    chord("next");
    await settle();
    await wait(60);
    await settle();
    const card = document.querySelector("[data-card]");
    expect(card).toBeTruthy();
    expect(document.activeElement).toBe(card);
  });

  it("with no card once the answers land, the keyboard goes to the composer", async () => {
    await mount([plain(A, { agentState: "waiting_input", inputReason: "permission_prompt" })]);
    chord("next");
    await settle();
    await wait(20);
    await settle();
    expect(document.querySelector("[data-card]")).toBeNull();
    expect(document.activeElement).toBe(field());
  });

  it("a card that appears much later does not take the keyboard by appearing", async () => {
    let ask = false;
    h.state.impl = () => Promise.resolve(ask ? answer({ composer: "prompt", prompt: perm }) : answer());
    await mount([plain(A, { agentState: "waiting_input", inputReason: "permission_prompt" })]);
    chord("next");
    await settle();
    await wait(20);
    await settle();
    expect(document.activeElement).toBe(field());
    ask = true;
    await talk.refresh(A);
    flushSync();
    await settle();
    expect(document.querySelector("[data-card]")).toBeTruthy();
    expect(document.activeElement).toBe(field());
  });

  it("a stream focus request is not spent before the story has loaded", async () => {
    let done: (v: unknown) => void = () => {};
    h.feed.impl = () => new Promise((r) => (done = r));
    await mount([plain(A, { delivery: "ci_failed" })]);
    chord("next");
    await settle();
    const v = ssh.view(A);
    expect(v.seen.stream).not.toBe(v.streamFocus);
    done({ items: [], lastSeq: 1, epoch: "e", health: "ok" });
    await settle();
    expect(v.seen.stream).toBe(v.streamFocus);
  });
});

describe("a daemon restart", () => {
  const restart = async (id: string) => {
    h.handlers.get("daemon:alive")?.({ data: false });
    flushSync();
    await settle();
    h.handlers.get("daemon:alive")?.({ data: true });
    flushSync();
    expect(store.sessionById(id)).toBeTruthy();
  };

  it("keeps the session through the gap between coming up and the first push", async () => {
    store.retainAll = true;
    await mount([plain(A)]);
    terms.select(A, AGENT);
    await open(A);
    const before = document.querySelector('[data-terminal-cell] [data-testid="terminal"]');
    expect(before).toBeTruthy();
    await restart(A);
    expect(document.querySelector('[data-terminal-cell] [data-testid="terminal"]')).toBe(before);
    expect(document.querySelector("[data-session-view]")?.textContent).not.toContain("no longer running");
    expect(store.rosterSessions.map((s) => s.id)).toEqual([A]);
    h.handlers.get("daemon:sessions")?.({ data: { sessions: [plain(A)], events: [] } });
    flushSync();
    await settle();
    expect(document.querySelector('[data-terminal-cell] [data-testid="terminal"]')).toBe(before);
    // Past the first push the list is the daemon's again, so a session gone from it is gone.
    h.handlers.get("daemon:sessions")?.({ data: { sessions: [], events: [] } });
    flushSync();
    expect(store.sessionById(A)).toBeUndefined();
  });

  it("asks again for a timeline that failed while the daemon was down", async () => {
    store.retainAll = true;
    let fail = false;
    h.timeline.impl = () => (fail ? Promise.reject(new Error("daemon not reachable")) : Promise.resolve({ connections: null, items: [], lastSeq: 0 }));
    await mount([plain(A), plain(B)]);
    await open(A);
    fail = true;
    h.handlers.get("daemon:alive")?.({ data: false });
    flushSync();
    await settle();
    await ssh.view(A).fetch();
    expect(ssh.view(A).error).toBeTruthy();
    fail = false;
    h.handlers.get("daemon:alive")?.({ data: true });
    flushSync();
    await settle();
    expect(ssh.view(A).error).toBe("");
  });
});

describe("arrival", () => {
  it("⌘L on an open conversation does not swallow the next arrival", async () => {
    await mount([plain(A)]);
    await open(A);
    chord("ssh-talk");
    await settle();
    expect(ssh.view(A).holdArrival).toBe(false);
    nav.focusSessions();
    flushSync();
    await settle();
    await open(A);
    expect(document.activeElement).toBe(field());
  });

  it("`t` on an open conversation does not swallow it either", async () => {
    await mount([plain(A)]);
    await open(A);
    (document.activeElement as HTMLElement)?.blur();
    await press("t");
    await settle();
    expect(ssh.view(A).holdArrival).toBe(false);
  });

  it("a jump that chose a card still keeps the composer from taking the keyboard", async () => {
    h.state.impl = () => Promise.resolve(answer({ composer: "prompt", prompt: perm }));
    await mount([plain(B), plain(A, { agentState: "waiting_input", inputReason: "permission_prompt" })]);
    await open(B);
    await talk.refresh(A);
    chord("next");
    await settle();
    expect(document.activeElement).toBe(document.querySelector("[data-card]"));
  });
});

describe("where ⌘J lands", () => {
  const waiting = { agentState: "waiting_input", inputReason: "permission_prompt" };

  it("on the terminal for someone who chose to always open there", async () => {
    terms.setGlobalView("terminal");
    await mount([plain(A, waiting)]);
    chord("next");
    await settle();
    expect(terms.activeTab(A)).toBe(AGENT);
  });

  it("on the terminal for an agent with no cards", async () => {
    h.state.impl = () => Promise.resolve({ caps: { feed: false, send: false, stop: false, cards: false }, composer: "unknown", feedSeq: 0, health: "unsupported" });
    await mount([plain(A, { agent: "codex", ...waiting })]);
    chord("next");
    await settle();
    expect(terms.activeTab(A)).toBe(AGENT);
  });

  it("on the terminal for a goal attempt", async () => {
    await mount([plain(A, { goalId: "g1", ...waiting })]);
    chord("next");
    await settle();
    expect(terms.activeTab(A)).toBe(AGENT);
  });

  it("on the terminal for an agent with no cards, even when the conversation was chosen", async () => {
    await mount([plain(A, { agent: "codex", ...waiting })]);
    terms.select(A, SSH);
    chord("next");
    await settle();
    expect(terms.activeTab(A)).toBe(AGENT);
  });

  it("on the conversation for an approval, which no terminal can answer", async () => {
    terms.setGlobalView("terminal");
    await mount([plain(A, { ssh: { needsHuman: true, nextApproval: { host: "h" }, connections: [host("h")], lastSeq: 0 } })]);
    chord("next");
    await settle();
    expect(terms.activeTab(A)).toBe(SSH);
  });
});

describe("a log nothing can read", () => {
  const bad = () => Promise.resolve(answer({ caps: { feed: false, send: true, stop: true, cards: true }, health: "unreadable" }));

  it("lands on the terminal from the next open, and never flips the open one", async () => {
    h.state.impl = bad;
    await mount([plain(A), plain(B)]);
    await open(A);
    await wait(10);
    await settle();
    expect(terms.activeTab(A)).toBe(SSH);
    // Closing keeps it on show in the band, so nothing flips until someone has gone elsewhere.
    nav.focusSessions();
    flushSync();
    await settle();
    expect(terms.activeTab(A)).toBe(SSH);
    nav.openSession(B);
    flushSync();
    await wait(150);
    await settle();
    await open(A);
    expect(terms.activeTab(A)).toBe(AGENT);
  });

  it("is remembered across a relaunch, for every session of that agent", async () => {
    h.state.impl = bad;
    await mount([plain(A), plain(B)]);
    await open(A);
    await wait(10);
    await settle();
    expect(JSON.parse(localStorage.getItem("lola.feed.bad") ?? "{}")).toEqual({ claude: true });
    nav.focusSessions();
    flushSync();
    await settle();
    h.state.impl = () => new Promise(() => {}); // the first answer has not come back yet
    await open(B);
    expect(terms.activeTab(B)).toBe(AGENT);
  });

  it("an open on a good log clears it, so the next one lands on the conversation", async () => {
    localStorage.setItem("lola.feed.bad", JSON.stringify({ claude: true }));
    await mount([plain(A), plain(B)]);
    await open(A);
    await wait(10);
    await settle();
    expect(terms.activeTab(A)).toBe(AGENT);
    expect(localStorage.getItem("lola.feed.bad")).toBe("{}");
    nav.focusSessions();
    flushSync();
    await settle();
    await open(B);
    expect(terms.activeTab(B)).toBe(SSH);
  });

  it("a human's choice outranks it", async () => {
    h.state.impl = bad;
    await mount([plain(A)]);
    await open(A);
    terms.select(A, SSH);
    await wait(10);
    await settle();
    nav.focusSessions();
    flushSync();
    await settle();
    await open(A);
    expect(terms.activeTab(A)).toBe(SSH);
  });
});

describe("the composer's draft", () => {
  it("keeps what was typed while a send was on its way", async () => {
    let done: (v: unknown) => void = () => {};
    h.say.impl = () => new Promise((r) => (done = r));
    await mount([plain(A)]);
    await open(A);
    const f = field()!;
    await fireEvent.input(f, { target: { value: "first" } });
    await fireEvent.keyDown(f, { key: "Enter" });
    await settle();
    expect(f.value).toBe("");
    await fireEvent.input(f, { target: { value: "second" } });
    done({ item: "x", delivered: true });
    await settle();
    expect(ssh.view(A).agentDraft).toBe("second");
  });

  it("leaves a field changed to something else alone", async () => {
    let done: (v: unknown) => void = () => {};
    h.say.impl = () => new Promise((r) => (done = r));
    await mount([plain(A)]);
    await open(A);
    const f = field()!;
    await fireEvent.input(f, { target: { value: "first" } });
    await fireEvent.keyDown(f, { key: "Enter" });
    await settle();
    await fireEvent.input(f, { target: { value: "other words" } });
    done({ item: "x", delivered: true });
    await settle();
    expect(ssh.view(A).agentDraft).toBe("other words");
  });

  it("clears exactly what was sent, trailing space included", async () => {
    await mount([plain(A)]);
    await open(A);
    const f = field()!;
    await fireEvent.input(f, { target: { value: "first  " } });
    await fireEvent.keyDown(f, { key: "Enter" });
    await settle();
    expect(h.say.calls).toEqual([expect.objectContaining({ text: "first" })]);
    expect(ssh.view(A).agentDraft).toBe("");
  });

  it("Stop & send opens a screen command in the terminal instead of typing it", async () => {
    h.state.impl = () => Promise.resolve(answer({ composer: "working" }));
    await mount([plain(A, { agentState: "working" })]);
    await open(A);
    await settle();
    const f = field()!;
    await fireEvent.input(f, { target: { value: " /usage" } });
    await settle();
    const stopSend = document.querySelector('[data-footer-primary="stopSend"], [data-footer-secondary="stopSend"]') as HTMLElement;
    expect(stopSend).toBeTruthy();
    await wait(850); // the guard on anything a send may just have changed
    await fireEvent.click(stopSend);
    await settle();
    expect(h.say.calls).toEqual([]);
    expect(terms.activeTab(A)).toBe(AGENT);
  });

  it("an indented screen command on a plain ↩ goes to the terminal too", async () => {
    await mount([plain(A)]);
    await open(A);
    const f = field()!;
    await fireEvent.input(f, { target: { value: " /config" } });
    await fireEvent.keyDown(f, { key: "Enter" });
    await settle();
    expect(h.say.calls).toEqual([]);
    expect(terms.activeTab(A)).toBe(AGENT);
  });
});

describe("what a person must be able to see", () => {
  const grant = { expiresAt: new Date(Date.now() + 600_000).toISOString(), scope: "any" };

  it("an active grant shows under All with several hosts, with its Revoke", async () => {
    await mount([plain(A, { ssh: { connections: [host("prod", { grant }), host("stg")], lastSeq: 0 } })]);
    await open(A);
    const bar = document.querySelector("[data-host-bar]")!;
    expect(bar.textContent).toContain("Grant");
    expect(bar.textContent).toContain("Revoke");
    expect(bar.textContent).toContain("prod");
  });

  it("a Root shell chip stays in the bar", async () => {
    await mount([plain(A, { ssh: { connections: [host("prod", { termElevated: "sudo -i" })], lastSeq: 0 } })]);
    await open(A);
    expect(document.querySelector("[data-host-bar]")?.querySelector("[data-root-shell]")).toBeTruthy();
  });

  it("a sign-in counts down on the active connection", async () => {
    const c = host("prod", { state: "authenticating", connectDeadline: new Date(Date.now() + 120_000).toISOString() });
    await mount([plain(A, { ssh: { connections: [c], lastSeq: 0 } })]);
    await open(A);
    await settle();
    expect(document.querySelector('[data-card="auth"]')?.textContent).toMatch(/Gives up in \d+:\d\d/);
  });



  it("a draft outlives the session it was written for", async () => {
    await mount([plain(A)]);
    await open(A);
    ssh.view(A).agentDraft = "half a follow-up";
    flushSync();
    h.ended.rows = [{ id: A, title: A, endedAt: new Date().toISOString(), reason: "PR #1 merged" }];
    store.sessions = [] as never;
    flushSync();
    await settle();
    await talk.loadEnded();
    await wait(10);
    await settle();
    const row = document.querySelector("[data-ended-draft]");
    expect(row?.textContent).toContain("half a follow-up");
    expect(row?.querySelector("button")).toBeTruthy();
  });

  it("✎ appears on a roster row with no push behind it", async () => {
    store.alive = true;
    store.sessions = [plain(A)] as never;
    flushSync();
    render(SidebarRoster);
    await settle();
    expect(document.querySelector("[data-draft-mark]")).toBeNull();
    ssh.view(A).agentDraft = "half a sentence";
    flushSync();
    await settle();
    expect(document.querySelector("[data-draft-mark]")).toBeTruthy();
  });

  it("the roster reads a stop off the story of a viewed session", async () => {
    store.alive = true;
    store.sessions = [plain(A, { agentStateSince: new Date(Date.now() - 1000).toISOString() })] as never;
    flushSync();
    render(SidebarRoster);
    await settle();
    expect(document.querySelector("[data-roster-line]")?.textContent).toMatch(/^Done/);
    ssh.view(A).items = [{ id: "e1", seq: 1, at: "2026-01-01T00:00:00Z", system: { kind: "stopped", text: "You stopped this turn." } }] as never;
    flushSync();
    await settle();
    expect(document.querySelector("[data-roster-line]")?.textContent).toMatch(/^Stopped/);
  });

  it("a stopped session says so on its roster line, while a finished one says Done", () => {
    const now = Date.now();
    const s = plain(A, { agentState: "idle", agentStateSince: new Date(now - 1000).toISOString() });
    expect(rosterLine(s as never, now).text).toMatch(/^Done/);
    expect(rosterLine(s as never, now, true).text).toMatch(/^Stopped/);
  });
});

describe("keys that belong to what is on show", () => {
  it("Esc in a terminal reaches it past a Report left open", async () => {
    await mount([plain(A)]);
    await open(A);
    ssh.view(A).reportOpen = true;
    flushSync();
    await settle();
    chord("ssh-terminal");
    await settle();
    expect(terms.activeTab(A)).toBe(AGENT);
    const t = document.querySelector('[data-terminal-cell] [data-testid="terminal"]') as HTMLElement;
    let got = 0;
    t.addEventListener("keydown", () => got++);
    await fireEvent.keyDown(t, { key: "Escape", bubbles: true });
    expect(got).toBe(1);
  });

  it("removing the last host stays on the conversation and leaves no preference", async () => {
    const c = host("c1", { state: "closed" });
    await mount([plain(A, { ssh: { connections: [c], lastSeq: 0 } })]);
    await open(A);
    terms.show(A, SSH);
    flushSync();
    ssh.confirmTrash(A, c as never);
    await settle();
    expect(terms.activeTab(A)).toBe(SSH);
    expect(terms.stickyTerminal(A)).toBe(false);
  });

  it("Esc leaves Changes when the shell it came from has gone", async () => {
    let shells = [`${A}-shell-1`];
    h.shells.impl = () => Promise.resolve(shells);
    await mount([plain(A)]);
    await open(A);
    await terms.refresh(A);
    terms.select(A, `${A}-shell-1`);
    flushSync();
    terms.toggleDiff(A);
    flushSync();
    expect(terms.activeTab(A)).toBe(DIFF);
    shells = [];
    await terms.refresh(A);
    flushSync();
    terms.toggleDiff(A);
    flushSync();
    expect(terms.activeTab(A)).not.toBe(DIFF);
  });

  it("`f` from the sidebar toggles Changes on the active session", async () => {
    await mount([plain(A)]);
    await open(A);
    terms.toggleDiff(A);
    flushSync();
    nav.focusSessions();
    flushSync();
    await settle();
    nav.select(A);
    await fireEvent.keyDown(document.querySelector(`[data-roster-row="${A}"]`)!, { key: "f", bubbles: true });
    await settle();
    expect(nav.focusedTerm).toBe(A);
    expect(terms.activeTab(A)).toBe(SSH);
  });

  it("⌘. never stops a session nobody is looking at", async () => {
    await mount([plain(A, { agentState: "working" })]);
    nav.select(A);
    nav.goHome();
    flushSync();
    await settle();
    chord("stop");
    await settle();
    expect(h.stop.calls).toEqual([]);
    nav.goCockpit("");
    flushSync();
    await settle();
    nav.select(A);
    chord("stop");
    await settle();
    expect(h.stop.calls.length).toBe(1);
  });

  it("a host shell that ends drops back to the story with the keyboard on the composer", async () => {
    const c = host("c1");
    await mount([plain(A, { ssh: { connections: [c], lastSeq: 0 } })]);
    await open(A);
    ssh.selectCell(A, { key: "host:c1", kind: "host", conn: c as never, label: "h" });
    flushSync();
    await settle();
    expect(terminalName()).toBe("x-c1");
    store.sessions = [plain(A, { ssh: { connections: [{ ...c, state: "closed" }], lastSeq: 0 } })] as never;
    flushSync();
    await settle();
    expect(ssh.view(A).narrow).toBe("stream");
    expect(document.querySelectorAll('[data-strip-cell][aria-selected="true"]').length).toBe(1);
    expect(document.activeElement).toBe(field());
  });

  it("⌘T counts as the last terminal used", async () => {
    await mount([plain(A)]);
    await open(A);
    h.shells.impl = () => Promise.resolve([`${A}-shell-1`]);
    await terms.openShell(A, "/w");
    flushSync();
    await settle();
    const shell = terms.activeTab(A);
    expect(shell).toBe(`${A}-shell-1`);
    expect(terms.lastTerminal(A)).toBe(shell);
  });

  it("the last shell closing goes back where a conversation-first session lives", async () => {
    h.shells.impl = () => Promise.resolve([`${A}-shell-1`]);
    await mount([plain(A)]);
    await open(A);
    await terms.refresh(A);
    terms.select(A, `${A}-shell-1`);
    flushSync();
    terms.show(A, SSH);
    terms.show(A, `${A}-shell-1`);
    terms.shellExited(A, `${A}-shell-1`);
    flushSync();
    expect(terms.activeTab(A)).toBe(SSH);
  });

  it("opening Changes refuses a session with no Changes", async () => {
    await mount([plain(A, { goalId: "g1" })]);
    nav.select(A);
    ssh.openChanges(store.sessionById(A)!);
    await settle();
    expect(terms.activeTab(A)).not.toBe(DIFF);
  });
});

it('keeps docked panels beside the terminal and resizes its main area', async () => {
  const width = vi.spyOn(Element.prototype, 'clientWidth', 'get').mockReturnValue(1400);
  const height = vi.spyOn(Element.prototype, 'clientHeight', 'get').mockReturnValue(800);
  try {
    await mount([plain(A)]);
    await open(A);
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Overview' })).toBeInTheDocument());
    terms.select(A, AGENT);
    await settle();
    expect(terminalName()).toBe(A);
    expect(screen.getByRole('tab', { name: 'Conversation' }).closest('[data-dock-main]')).not.toBeNull();
    const terminal = document.querySelector('[data-testid="terminal"]')!.closest('[data-terminal-cell]') as HTMLElement;
    const before = parseFloat(terminal.style.width);
    expect(before).toBeGreaterThan(420);
    expect(before).toBeLessThan(1400);
    expect(screen.getByRole('tab', { name: 'Overview' }).closest('[inert]')).toBeNull();
    await fireEvent.keyDown(screen.getByRole('separator', { name: 'Resize panels' }), { key: 'ArrowLeft' });
    await settle();
    expect(parseFloat(terminal.style.width)).toBeLessThan(before);
    expect(terminalName()).toBe(A);
  } finally {
    cleanup();
    width.mockRestore(); height.mockRestore();
  }
});
