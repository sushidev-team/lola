import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/svelte";
import { flushSync } from "svelte";

vi.mock("./LiveTerminal.svelte", async () => ({ default: (await import("./ssh/TerminalStub.test.svelte")).default }));
vi.mock("@bindings/desktop", () => ({
  SSHService: { Timeline: vi.fn().mockResolvedValue({ connections: null, items: [], lastSeq: 0 }), Hosts: vi.fn().mockResolvedValue({ hosts: [] }) },
  DaemonService: { Sessions: vi.fn(), Diff: vi.fn() },
  ConfigService: {},
  TermService: { Shells: vi.fn(async () => []), CaptureMany: vi.fn(async () => ({})) },
  TalkService: { State: vi.fn(async () => ({ caps: {}, composer: "unknown", feedSeq: 0, health: "ok" })) },
}));

const { default: SessionsColumn } = await import("./SessionsColumn.svelte");
const { store } = await import("$lib/store.svelte");
const { nav } = await import("$lib/nav.svelte");
const { terms, AGENT } = await import("$lib/terms.svelte");

class RO {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= RO as unknown as typeof ResizeObserver;

beforeEach(() => {
  cleanup();
  store.alive = true;
  store.connected = true;
  store.sessions = [{ id: "s1", title: "one", tmuxName: "s1", worktree: "/w", agentState: "working", status: "working", project: "p", issue: "E-1" }] as never;
  nav.view = "cockpit";
  nav.focusedTerm = "";
  nav.selectedId = "s1";
});

describe("full-height session workspace", () => {
  it("does not render a duplicate session list or title band", () => {
    nav.openSession("s1");
    render(SessionsColumn);
    expect(document.querySelector("[data-band-bar]")).toBeNull();
    expect(document.querySelector("[data-session-list]")).toBeNull();
  });
  it("sidebar focus does not remount the session or terminal", () => {
    nav.openSession("s1");
    terms.select("s1", AGENT);
    render(SessionsColumn);
    const embed = document.querySelector("[data-session-view]");
    const term = screen.getByTestId("terminal");
    nav.focusSessions();
    nav.sidebarOpen = false;
    nav.setSidebarWidth(440);
    flushSync();
    expect(document.querySelector("[data-session-view]")).toBe(embed);
    expect(screen.getByTestId("terminal")).toBe(term);
  });
  it("attaches nothing while Home covers the cockpit", () => {
    terms.select("s1", AGENT);
    nav.openSession("s1");
    render(SessionsColumn);
    expect(screen.getByTestId("terminal")).toBeInTheDocument();
    nav.view = "home";
    flushSync();
    expect(screen.queryByTestId("terminal")).toBeNull();
  });
  it("draws no accent frame around an open session", () => {
    nav.openSession("s1");
    render(SessionsColumn);
    expect(document.querySelector("[data-session-embed] .border-accent")).toBeNull();
  });
});
