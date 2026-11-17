import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { tick } from "svelte";

vi.mock("@mobile/native", () => ({ transport: { haptic: async () => {}, addListener: async () => ({ remove() {} }) } }));
const talkMock = vi.hoisted(() => ({ stop: vi.fn(), poll: vi.fn() }));
vi.mock("$lib/talk.svelte", () => ({ talk: { state: () => null, poll: talkMock.poll, choose: vi.fn() } }));

import Inbox from "./Inbox.svelte";
import { INTERACTIVE_MARK, inbox, inboxView } from "@mobile/lib/inbox.svelte";
import { inboxSearch } from "@mobile/lib/inboxsearch.svelte";
import { router } from "@mobile/lib/router.svelte";
import { connection } from "@mobile/lib/connection.svelte";
import { emit } from "../wailsshim/events";
import type { SessionInfo } from "$lib/store.svelte";

const s = (id: string, over: Partial<SessionInfo> = {}) =>
  ({ id, project: "p", issue: id.toUpperCase(), title: `title ${id}`, branch: "b", agentState: "working", delivery: "none", age: "1m", prNumber: 0, ...over }) as SessionInfo;

beforeEach(() => {
  talkMock.stop.mockReset();
  talkMock.poll.mockReset().mockImplementation(() => talkMock.stop);
  inbox.ingest([]);
  inboxView.set("status");
  inboxSearch.clear();
  connection.phase = "ready";
  connection.failure = null;
  router.stack = [{ name: "tab", tab: "inbox" }];
  router.sheet = "";
});
afterEach(cleanup);

describe("Inbox", () => {
  describe("search", () => {
    const list = () => [
      s("a", { title: "Fix login", issue: "NOR-1", agentState: "waiting_input" }),
      s("b", { title: "Rework billing", issue: "OKA-2", project: "okane" }),
      s("c", { title: "Docs", issue: "NOR-3", branch: "lola/docs-tweak", agentState: "dead" }),
    ];
    const type = async (v: string) => fireEvent.input(screen.getByRole("searchbox", { name: "Search sessions" }), { target: { value: v } });

    it("shows no field while there are no sessions", async () => {
      render(Inbox);
      await tick();
      expect(screen.queryByRole("searchbox")).toBeNull();
    });

    it("narrows by title, issue key, branch and project, across sections", async () => {
      render(Inbox);
      emit("daemon:sessions", { sessions: list(), events: [] });
      await tick();
      await type("billing");
      expect(screen.getByText("Rework billing")).toBeTruthy();
      expect(screen.queryByText("Fix login")).toBeNull();
      await type("nor-");
      expect(screen.getByText("Fix login")).toBeTruthy();
      expect(screen.getByText("Docs")).toBeTruthy();
      expect(screen.queryByText("Rework billing")).toBeNull();
      await type("docs-tweak");
      expect(screen.getByText("Docs")).toBeTruthy();
      expect(screen.queryByText("Fix login")).toBeNull();
    });

    it("composes with the status filter and shows its own count", async () => {
      router.stack = [{ name: "tab", tab: "inbox", filter: "working" }];
      render(Inbox);
      emit("daemon:sessions", { sessions: [s("a", { title: "Fix login" }), s("b", { title: "Fix billing" }), s("c", { title: "Docs" })], events: [] });
      await tick();
      await type("fix");
      expect(screen.getByRole("button", { name: "Clear filter" }).textContent).toContain("Working · 2");
      await type("billing");
      expect(screen.getByRole("button", { name: "Clear filter" }).textContent).toContain("Working · 1");
    });

    it("composes with grouping by project", async () => {
      inboxView.set("project");
      render(Inbox);
      emit("daemon:sessions", { sessions: list(), events: [] });
      await tick();
      await type("nor-");
      expect(screen.getByRole("heading", { name: /^p\b/ })).toBeTruthy();
      expect(screen.queryByRole("heading", { name: /okane/i })).toBeNull();
    });

    it("says there are no matches instead of the all-clear, and clears", async () => {
      render(Inbox);
      emit("daemon:sessions", { sessions: list(), events: [] });
      await tick();
      await type("zzzz");
      expect(screen.getByText("No matches")).toBeTruthy();
      expect(screen.queryByText("Nothing needs you")).toBeNull();
      await fireEvent.click(screen.getByRole("button", { name: "Clear search" }));
      expect(screen.getByText("Fix login")).toBeTruthy();
      expect(inboxSearch.query).toBe("");
    });
  });

  it("shows a compact all-clear row, with no hero and no summary, once loaded", async () => {
    render(Inbox);
    await tick();
    const row = screen.getByText("Nothing needs you");
    expect(row.className).toContain("text-headline");
    expect(row.parentElement?.className).toContain("h-16");
    expect(row.parentElement?.className).toContain("bg-edge-soft");
    expect(screen.queryByText("All clear")).toBeNull();
    expect(screen.queryByRole("button", { name: /working/ })).toBeNull();
  });

  it("keeps the compact row above the other sections when nothing needs you", async () => {
    render(Inbox);
    emit("daemon:sessions", { sessions: [s("a"), s("b"), s("c", { agentState: "idle", delivery: "review_pending" })], events: [] });
    await tick();
    expect(screen.getByText("Nothing needs you")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Working 2" })).toBeTruthy();
  });

  it("shows three skeleton cards and the banner when offline with no snapshot, never a blank", async () => {
    inbox.ingest([]);
    connection.phase = "closed";
    connection.failure = "offline";
    const { container } = render(Inbox);
    await tick();
    expect(container.querySelectorAll("[data-skeleton] .skeleton")).toHaveLength(3);
    expect(screen.getByRole("status")).toHaveTextContent("No network.");
    expect(screen.queryByText("Nothing needs you")).toBeNull();
  });

  it("says Connecting… when there is no snapshot and no banner", async () => {
    connection.phase = "idle";
    connection.failure = null;
    render(Inbox);
    await tick();
    expect(screen.getByText("Connecting…")).toBeTruthy();
  });

  it("puts the banner below the large title and above the list", async () => {
    connection.phase = "closed";
    connection.failure = "unreachable";
    render(Inbox);
    emit("daemon:sessions", { sessions: [s("b")], events: [] });
    await tick();
    const title = screen.getByRole("heading", { name: "Inbox", level: 1 });
    const banner = screen.getByRole("button", { name: /Can't reach/ });
    const section = screen.getByRole("heading", { name: "Working 1" });
    expect(title.compareDocumentPosition(banner) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(banner.compareDocumentPosition(section) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("labels a card for VoiceOver in one phrase and hides the drawn text", async () => {
    render(Inbox);
    emit("daemon:sessions", { sessions: [s("a", { agentState: "waiting_input", inputReason: "question", title: "Which period?", issue: "NOR-411", agentStateSince: new Date(Date.now() - 22 * 60_000).toISOString() })], events: [] });
    await tick();
    expect(screen.getByRole("button", { name: "Question, Which period?, p NOR-411, 22 minutes ago" })).toBeTruthy();
  });

  it("shows a 32pt filter chip that clears the filter", async () => {
    router.stack = [{ name: "tab", tab: "inbox", filter: "working" }];
    render(Inbox);
    emit("daemon:sessions", { sessions: [s("b")], events: [] });
    await tick();
    const chip = screen.getByRole("button", { name: /Clear/i });
    expect(chip.textContent).toContain("Working · 1");
    expect(chip.className).toContain("h-8");
    await fireEvent.click(chip);
    expect(router.top).toMatchObject({ name: "tab", tab: "inbox" });
    expect((router.top as { filter?: string }).filter ?? "").toBe("");
  });

  it("sections sessions and follows pushed events", async () => {
    render(Inbox);
    emit("daemon:sessions", { sessions: [s("a", { agentState: "waiting_input" }), s("b"), s("c", { agentState: "dead" })], events: [] });
    await tick();
    expect(screen.getByRole("heading", { name: "Needs you 1" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Working 1" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Done 1" })).toBeTruthy();
    // Every needs card carries its one next-step button.
    expect(screen.getByRole("button", { name: "Answer" })).toBeTruthy();
    expect(screen.getByRole("button", { name: /title a/ })).toBeTruthy();
  });

  it("watches the first blocked cards only while the Inbox is on top", async () => {
    render(Inbox);
    emit("daemon:sessions", { sessions: ["a", "b", "c", "d", "e"].map((id) => s(id, { agentState: "waiting_input" })), events: [] });
    await tick();
    expect(talkMock.poll).toHaveBeenCalledTimes(3);
    router.push({ name: "session", id: "z", seg: "conversation" });
    await tick();
    expect(talkMock.stop).toHaveBeenCalledTimes(3);
    router.back();
    await tick();
    expect(talkMock.poll).toHaveBeenCalledTimes(6);
  });

  it("opens a session on row tap", async () => {
    render(Inbox);
    emit("daemon:sessions", { sessions: [s("b"), s("n", { agentState: "waiting_input" })], events: [] });
    await tick();
    await fireEvent.click(screen.getByRole("button", { name: /title b/ }));
    expect(router.top).toMatchObject({ name: "session", id: "b", seg: "conversation" });
  });

  it("applies the route's filter and opens the filter sheet", async () => {
    router.stack = [{ name: "tab", tab: "inbox", filter: "done" }];
    render(Inbox);
    emit("daemon:sessions", { sessions: [s("b"), s("c", { agentState: "dead" })], events: [] });
    await tick();
    expect(screen.queryByRole("heading", { name: /Working/ })).toBeNull();
    // A filtered list has no section header: the chip carries the name and the count.
    expect(screen.queryByRole("heading", { name: /Done/ })).toBeNull();
    expect(screen.getByRole("button", { name: /Clear/ }).textContent).toContain("Done · 1");
    await fireEvent.click(screen.getByRole("button", { name: /Filter, showing Done/ }));
    expect(router.sheet).toBe("filter");
  });

  it("marks the first painted list once for the boot budget", async () => {
    performance.clearMarks(INTERACTIVE_MARK);
    render(Inbox);
    emit("daemon:sessions", { sessions: [s("b")], events: [] });
    await waitFor(() => expect(performance.getEntriesByName(INTERACTIVE_MARK)).toHaveLength(1));
    emit("daemon:sessions", { sessions: [s("b"), s("c")], events: [] });
    await new Promise((r) => requestAnimationFrame(() => r(null)));
    expect(performance.getEntriesByName(INTERACTIVE_MARK)).toHaveLength(1);
    performance.clearMarks(INTERACTIVE_MARK);
  });
});

describe("Inbox grouped by project", () => {
  const sessions = [
    s("a1", { project: "alpha" }),
    s("a2", { project: "alpha", agentState: "waiting_input" }),
    s("b1", { project: "beta" }),
    s("n1", { project: "" }),
  ];

  it("draws one header per project, needs card first, and keeps taps working", async () => {
    inboxView.set("project");
    render(Inbox);
    emit("daemon:sessions", { sessions, events: [] });
    await tick();
    expect(screen.getByRole("heading", { name: "alpha 2" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "beta 1" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "No project 1" })).toBeTruthy();
    expect(screen.queryByRole("heading", { name: /Needs you/ })).toBeNull();
    const keys = [...document.querySelectorAll("[data-vkey]")].map((n) => n.getAttribute("data-vkey"));
    expect(keys).toEqual(["group:alpha", "card:a2", "row:a1", "group:beta", "row:b1", "group:", "row:n1"]);
    expect(screen.getByRole("button", { name: "Filter, grouped by project" })).toBeTruthy();
  });

  it("composes with the status filter and keeps Show all", async () => {
    inboxView.set("project");
    router.stack = [{ name: "tab", tab: "inbox", filter: "done" }];
    const done = Array.from({ length: 25 }, (_, i) => s(`d${i}`, { project: i % 2 ? "alpha" : "beta", agentState: "dead", delivery: "merged" }));
    render(Inbox);
    emit("daemon:sessions", { sessions: done, events: [] });
    await tick();
    expect(document.querySelectorAll('[data-vkey^="row:"]')).toHaveLength(20);
    await fireEvent.click(screen.getByRole("button", { name: /Show all/ }));
    await tick();
    expect(document.querySelectorAll('[data-vkey^="row:"]')).toHaveLength(25);
  });

  it("switching the mode keeps every row's element, so no row plays an enter or leave", async () => {
    render(Inbox);
    emit("daemon:sessions", { sessions, events: [] });
    await tick();
    await new Promise((r) => requestAnimationFrame(() => r(null)));
    await tick();
    const before = new Map([...document.querySelectorAll('[data-vkey^="row:"],[data-vkey^="card:"]')].map((n) => [n.getAttribute("data-vkey"), n]));
    inboxView.set("project");
    await tick();
    const after = new Map([...document.querySelectorAll('[data-vkey^="row:"],[data-vkey^="card:"]')].map((n) => [n.getAttribute("data-vkey"), n]));
    expect([...after.keys()].sort()).toEqual([...before.keys()].sort());
    for (const [k, n] of after) expect(n).toBe(before.get(k));
  });
});
