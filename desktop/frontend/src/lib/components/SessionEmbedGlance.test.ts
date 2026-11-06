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
  nav.lens = "list";
  nav.view = "cockpit";
  nav.focusedTerm = "";
  nav.selectedId = "s1";
});

describe("two depths", () => {
  it("draws the session in the band with its own title row", () => {
    render(SessionsColumn);
    expect(document.querySelector("[data-band-bar]")).toHaveTextContent("one");
  });
  it("Enter does not remount the embed", () => {
    terms.select("s1", AGENT);
    render(SessionsColumn);
    const embed = document.querySelector("[data-session-view]");
    expect(embed?.closest("[data-session-embed]")).not.toHaveAttribute("data-open");
    nav.openSession("s1");
    flushSync();
    expect(document.querySelector("[data-session-view]")).toBe(embed);
    expect(embed?.closest("[data-session-embed]")).toHaveAttribute("data-open");
    const term = screen.getByTestId("terminal");
    expect(term.dataset.name).toBe("s1");
    nav.closeSession();
    flushSync();
    expect(document.querySelector("[data-session-view]")).toBe(embed);
    // The same view, shorter: the terminal stays attached and gives up nothing.
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
