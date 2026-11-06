// A session that left the list stays readable, and nothing in it can act.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/svelte";
import { flushSync } from "svelte";

const talkSvc = vi.hoisted(() => ({ Ended: vi.fn(), State: vi.fn(), Feed: vi.fn(), Say: vi.fn() }));
const sshSvc = vi.hoisted(() => ({ Timeline: vi.fn(), Hosts: vi.fn().mockResolvedValue({ hosts: [] }) }));
vi.mock("@bindings/desktop", () => ({
  TalkService: talkSvc,
  SSHService: sshSvc,
  DaemonService: { Alive: vi.fn().mockResolvedValue(true), OpenURL: vi.fn().mockResolvedValue(undefined), Diff: vi.fn().mockResolvedValue({ files: [] }) },
  ConfigService: {},
  TermService: { Shells: vi.fn(async () => []) },
}));
vi.mock("../LiveTerminal.svelte", async () => ({ default: (await import("../ssh/TerminalStub.test.svelte")).default }));

const { default: EndedView } = await import("./EndedView.svelte");
const { default: SessionBar } = await import("./SessionBar.svelte");
const { default: SessionEmbed } = await import("../SessionEmbed.svelte");
const { default: SidebarRoster } = await import("../SidebarRoster.svelte");
const { store } = await import("$lib/store.svelte");
const { talk } = await import("$lib/talk.svelte");
const { nav } = await import("$lib/nav.svelte");

const record = (id: string) => ({ id, title: "Fix login redirect", issue: "ENG-42", agent: "claude", endedAt: "2026-01-01T00:00:00Z", reason: "PR #412 merged", prNumber: 412, prUrl: "https://example.test/412" });

let n = 0;
let SID = "";
beforeEach(async () => {
  cleanup();
  SID = `end${++n}`;
  store.alive = true;
  store.sessions = [];
  talkSvc.Ended.mockReset().mockResolvedValue({ sessions: [record(SID)] });
  talkSvc.State.mockReset().mockResolvedValue({ session: SID, composer: "gone", feedSeq: 0, health: "ok" });
  talkSvc.Feed.mockReset().mockResolvedValue({ session: SID, epoch: "e", lastSeq: 2, health: "ok", items: [{ id: "u:0", order: 1, kind: "text", at: "2026-01-01T00:00:00Z", text: "The cookie was cleared after the redirect fired." }] });
  sshSvc.Timeline.mockReset().mockResolvedValue({ session: SID, connections: [], lastSeq: 1, items: [{ id: "h1", kind: "human", order: 1, seq: 1, at: "2025-12-31T23:59:00Z", human: { origin: "say", text: "Why does it loop?", delivered: true } }] });
  await talk.loadEnded();
});

describe("EndedView", () => {
  it("reads the store when the session is gone", async () => {
    expect(store.sessionById(SID)).toBeUndefined();
    render(EndedView, { sessionId: SID });
    await waitFor(() => expect(sshSvc.Timeline).toHaveBeenCalledWith({ session: SID, after: 0 }));
    await waitFor(() => expect(talkSvc.Feed).toHaveBeenCalled());
    expect(await screen.findByText("Why does it loop?")).toBeInTheDocument();
    expect(await screen.findByText("The cookie was cleared after the redirect fired.")).toBeInTheDocument();
  });

  it("has no composer", async () => {
    render(EndedView, { sessionId: SID });
    await screen.findByText("Why does it loop?");
    expect(document.querySelector("[data-composer]")).toBeNull();
    expect(document.querySelector("[data-action-zone]")).toBeNull();
    expect(screen.queryByRole("textbox", { name: /message|reply/i })).toBeNull();
    expect(talkSvc.Say).not.toHaveBeenCalled();
  });

  it("says what happened and offers the PR", async () => {
    const open = vi.spyOn(store, "openURL").mockResolvedValue();
    render(EndedView, { sessionId: SID });
    expect(screen.getByText("This session has ended. Its worktree is gone.")).toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: /Open PR/ }));
    expect(open).toHaveBeenCalledWith("https://example.test/412");
  });

  it("the top bar says it ended and offers no actions", () => {
    render(SessionBar, { sessionId: SID });
    expect(document.querySelector("[data-ended-tag]")).toHaveTextContent("Ended · PR #412 merged");
    expect(screen.getAllByText("Fix login redirect").length).toBeGreaterThan(0);
    expect(screen.queryByRole("button", { name: "Session actions" })).toBeNull();
  });
});

describe("the ended trigger", () => {
  it("asks again the moment an open session leaves the list, and lands on its story", async () => {
    const live = `${SID}-live`;
    talkSvc.Ended.mockResolvedValue({ sessions: [] });
    await talk.loadEnded();
    store.sessions = [{ id: live, agent: "claude", agentState: "idle", worktree: "/w", tmuxName: live }] as never;
    talkSvc.State.mockResolvedValue({ session: live, caps: {}, composer: "resting", feedSeq: 0, health: "ok" });
    render(SessionEmbed, { sessionId: live });
    expect(document.querySelector("[data-ended-view]")).toBeNull();
    talkSvc.Ended.mockClear();
    talkSvc.Ended.mockResolvedValue({ sessions: [record(live)] });
    store.sessions = [];
    flushSync();
    await waitFor(() => expect(talkSvc.Ended).toHaveBeenCalled());
    await waitFor(() => expect(document.querySelector("[data-ended-view]")).not.toBeNull());
    expect(document.querySelector("[data-composer]")).toBeNull();
  });

  it("a session that left no record says so, on an empty column", async () => {
    talkSvc.Ended.mockResolvedValue({ sessions: [] });
    await talk.loadEnded();
    render(SessionEmbed, { sessionId: "shell-gone" });
    // Never listed, so it may still be on its way: nothing false in the meantime.
    expect(screen.queryByText("This session is no longer running.")).toBeNull();
    expect(await screen.findByText("This session is no longer running.", undefined, { timeout: 4000 })).toBeInTheDocument();
  });
});

describe("the Ended group", () => {
  it("is collapsed, counts, and a row opens the story", async () => {
    store.projects = [] as never;
    render(SidebarRoster);
    const toggle = await screen.findByRole("button", { name: /Ended\s+1/ });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(document.querySelector(`[data-roster-ended="${SID}"]`)).toBeNull();
    await fireEvent.click(toggle);
    const row = document.querySelector(`[data-roster-ended="${SID}"]`) as HTMLElement;
    expect(row).toHaveTextContent("PR #412 merged");
    await fireEvent.click(row);
    expect(nav.focusedTerm).toBe(SID);
    nav.focusedTerm = "";
  });

  it("does not list a respawned id twice", async () => {
    store.sessions = [{ id: SID, agent: "claude", agentState: "idle", title: "Back" }] as never;
    store.projects = [{ name: "", label: "" }] as never;
    render(SidebarRoster);
    await waitFor(() => expect(talkSvc.Ended).toHaveBeenCalled());
    expect(screen.queryByRole("button", { name: /Ended/ })).toBeNull();
  });
});
