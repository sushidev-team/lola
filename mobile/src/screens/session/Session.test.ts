import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import type { SessionInfo } from "$lib/store.svelte";

vi.mock("@bindings/desktop", () => ({
  DaemonService: { ReviewPackages: vi.fn(async () => ({ packages: [] })) },
}));
vi.mock("$lib/components/session/EndedView.svelte", async () => await import("./Stub.test.svelte"));
vi.mock("./ConversationSegment.svelte", async () => await import("./Stub.test.svelte"));
vi.mock("./ChangesSegment.svelte", async () => await import("./Stub.test.svelte"));
vi.mock("./ReviewSegment.svelte", async () => await import("./Stub.test.svelte"));
vi.mock("./TerminalSegment.svelte", async () => await import("./Stub.test.svelte"));

import { DaemonService } from "@bindings/desktop";
import Session, { offeredSegments } from "./Session.svelte";
import { store } from "$lib/store.svelte";
import { router } from "@mobile/lib/router.svelte";
import { tiers } from "@mobile/lib/tiers.svelte";

function s(over: Partial<SessionInfo> = {}): SessionInfo {
  return {
    id: "s1",
    project: "p",
    issue: "NOR-1",
    title: "Email ingest",
    status: "working",
    agentState: "working",
    delivery: "",
    worktree: "/w/s1",
    tmuxName: "t",
    prNumber: 0,
    ...over,
  } as SessionInfo;
}

describe("offeredSegments", () => {
  it("always offers Conversation and nothing else without a session", () => {
    expect(offeredSegments(undefined, 3)).toEqual(["conversation"]);
  });
  it("hides Review without a PR or package and Terminal without a pane", () => {
    expect(offeredSegments(s({ tmuxName: "" }), 0)).toEqual(["conversation", "changes"]);
  });
  it("offers Review for a PR or a package, Terminal for a pane or a shell", () => {
    expect(offeredSegments(s({ prNumber: 4 }), 0)).toEqual(["conversation", "changes", "review", "terminal"]);
    expect(offeredSegments(s({ tmuxName: "" }), 2)).toContain("review");
    expect(offeredSegments(s({ tmuxName: "", status: "shell" }), 0)).toContain("terminal");
  });
});

describe("Session screen", () => {
  beforeEach(() => {
    tiers.set(["read"]);
    store.sessions = [s()];
    router.stack = [{ name: "tab", tab: "inbox" }, { name: "session", id: "s1", seg: "conversation" }];
    router.sheet = "";
  });
  afterEach(() => vi.clearAllMocks());

  it("names the session and shows only the segments its data offers", async () => {
    render(Session);
    expect(screen.getByRole("heading", { name: "Email ingest" })).toBeInTheDocument();
    const tabs = screen.getAllByRole("tab").map((t) => t.textContent?.trim());
    expect(tabs).toEqual(["Conversation", "Changes", "Terminal"]);
    await waitFor(() => expect(document.querySelector("[data-stub]")).not.toBeNull());
  });

  it("changing segment replaces the route", async () => {
    render(Session);
    await fireEvent.click(screen.getByRole("tab", { name: "Changes" }));
    expect(router.top).toEqual({ name: "session", id: "s1", seg: "changes" });
    expect(router.stack).toHaveLength(2);
  });

  it("lays out as one shrinkable column so no segment can widen the screen", () => {
    render(Session);
    const root = document.querySelector<HTMLElement>("[data-session-screen]")!;
    expect(root.className).toContain("grid-cols-[minmax(0,1fr)]");
    expect(root.className).toContain("grid-rows-[auto_auto_minmax(0,1fr)]");
    for (const child of Array.from(root.children)) expect(child.className).toContain("min-w-0");
  });

  it("opens the menu from the header", async () => {
    render(Session);
    await fireEvent.click(screen.getByRole("button", { name: "Session menu" }));
    expect(router.sheet).toBe("sessionMenu");
  });

  it("falls back to Conversation when the route names a segment not offered", async () => {
    router.stack = [{ name: "tab", tab: "inbox" }, { name: "session", id: "s1", seg: "review" }];
    render(Session);
    await waitFor(() => expect(router.top).toEqual({ name: "session", id: "s1", seg: "conversation" }));
  });

  it("says the session ended when nothing is left to read", () => {
    store.sessions = [];
    render(Session);
    expect(screen.getByText("Session ended")).toBeInTheDocument();
    expect(screen.queryByRole("tab")).toBeNull();
  });

  it("offers Review for a pre-PR session that has review packages", async () => {
    tiers.set(["read"]);
    vi.mocked(DaemonService.ReviewPackages).mockResolvedValueOnce({ packages: [{}] } as never);
    render(Session);
    await waitFor(() =>
      expect(screen.getAllByRole("tab").map((t) => t.textContent?.trim())).toEqual([
        "Conversation",
        "Changes",
        "Review",
        "Terminal",
      ]),
    );
    expect(DaemonService.ReviewPackages).toHaveBeenCalledWith("s1");
  });

  it("asks for packages once, not on every sessions push", async () => {
    render(Session);
    await waitFor(() => expect(DaemonService.ReviewPackages).toHaveBeenCalledTimes(1));
    store.sessions = [s({ title: "Email ingest" }), s({ id: "s2" })];
    store.sessions = [s({ agentState: "idle" }), s({ id: "s2", agentState: "working" })];
    await new Promise((r) => setTimeout(r, 0));
    expect(DaemonService.ReviewPackages).toHaveBeenCalledTimes(1);
  });
});

describe("Session header", () => {
  it("shows a red status dot for a ci_failed session and names where back goes", async () => {
    store.sessions = [s({ delivery: "ci_failed", prNumber: 3 })];
    router.stack = [{ name: "tab", tab: "projects" }, { name: "session", id: "s1", seg: "conversation" }];
    const { container } = render(Session);
    expect(container.querySelector("[data-status-dot]")?.className).toContain("bg-bad");
    expect(screen.getByRole("button", { name: "Back to Projects" })).toBeTruthy();
  });
});
