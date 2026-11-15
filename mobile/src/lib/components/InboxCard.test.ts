import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";

const mocks = vi.hoisted(() => ({
  state: null as unknown,
  choose: vi.fn(),
  poll: vi.fn(() => () => {}),
  resolve: vi.fn(),
  address: vi.fn(),
}));

vi.mock("$lib/talk.svelte", () => ({
  talk: { state: () => mocks.state, choose: mocks.choose, poll: mocks.poll },
}));
vi.mock("@bindings/desktop", () => ({ DaemonService: { AddressPRFeedback: mocks.address } }));
vi.mock("$lib/store.svelte", () => ({
  store: { resolveConflict: mocks.resolve, flash: null },
}));
vi.mock("@mobile/native", () => ({ transport: { haptic: async () => {}, addListener: async () => ({ remove() {} }) } }));

import InboxCard from "./InboxCard.svelte";
import { APPROVE_SETTLE_MS, rowModel } from "@mobile/lib/inbox.svelte";
import { router } from "@mobile/lib/router.svelte";
import { tiers } from "@mobile/lib/tiers.svelte";
import { store } from "$lib/store.svelte";
import type { SessionInfo } from "$lib/store.svelte";

const base = { id: "a", project: "p", issue: "NOR-1", title: "Fix it", branch: "b", age: "1m", prNumber: 0 };
const session = (over: Partial<SessionInfo>) => ({ ...base, ...over }) as SessionInfo;
const talkState = (hash: string, labels = ["Yes", "No"]) => ({
  prompt: {
    kind: "permission",
    hash,
    lines: ["Run rm -rf?"],
    choices: labels.map((label, i) => ({ key: String(i + 1), label })),
  },
});

beforeEach(() => {
  vi.useFakeTimers();
  tiers.set(["read", "answer", "control"]);
  router.stack = [{ name: "tab", tab: "inbox" }];
  router.sheet = "";
  mocks.state = null;
  mocks.choose.mockReset().mockResolvedValue({ sent: true });
  mocks.resolve.mockReset();
  mocks.address.mockReset().mockResolvedValue({});
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const waiting = () => rowModel(session({ agentState: "waiting_input", delivery: "none" }));

describe("InboxCard", () => {
  it("offers Approve for a lone Yes and presses it with the hash on screen", async () => {
    mocks.state = talkState("h1");
    render(InboxCard, { props: { model: waiting(), projectLabel: "Proj", watch: true } });
    expect(screen.getByText("Run rm -rf?")).toBeTruthy();
    const btn = screen.getByRole("button", { name: "Approve" });
    expect(btn).toBeDisabled();
    await vi.advanceTimersByTimeAsync(APPROVE_SETTLE_MS + 1);
    expect(btn).toBeEnabled();
    await fireEvent.click(btn);
    expect(mocks.choose).toHaveBeenCalledWith("a", "h1", "1");
  });

  it("shows Answer as the one button, opening the conversation like the card tap", async () => {
    mocks.state = talkState("h1", ["Yes, always", "No"]);
    render(InboxCard, { props: { model: waiting(), projectLabel: "Proj", watch: true } });
    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Answer" }));
    expect(router.top).toMatchObject({ name: "session", id: "a", seg: "conversation" });
    cleanup();
    router.stack = [{ name: "tab", tab: "inbox" }];
    render(InboxCard, { props: { model: waiting(), projectLabel: "Proj", watch: true } });
    await fireEvent.click(screen.getByRole("button", { name: /Fix it/ }));
    expect(router.top).toMatchObject({ name: "session", id: "a", seg: "conversation" });
  });

  it("says Choose on a dialog card", () => {
    const m = rowModel(session({ agentState: "waiting_input", inputReason: "dialog" }));
    render(InboxCard, { props: { model: m, projectLabel: "P" } });
    expect(screen.getByRole("button", { name: "Choose" })).toBeTruthy();
    expect(screen.getByText("Needs a choice")).toBeTruthy();
  });

  it("says Run command? when the prompt asks to run one", () => {
    mocks.state = { prompt: { kind: "permission", hash: "h", lines: ["", "Bash command", "ls -la"], choices: [] } };
    const m = rowModel(session({ agentState: "waiting_input", inputReason: "permission_prompt" }));
    render(InboxCard, { props: { model: m, projectLabel: "P", watch: true } });
    expect(screen.getByText("Run command?")).toBeTruthy();
  });

  it("drops the frame lines and the command label from the well", () => {
    mocks.state = {
      prompt: {
        kind: "permission",
        hash: "h",
        lines: ["Bash command", "git commit -m x", "Do you want to proceed?"],
        choices: [],
      },
    };
    const { container } = render(InboxCard, { props: { model: waiting(), projectLabel: "P", watch: true } });
    const well = container.querySelector("[data-prompt]")!;
    expect(well.textContent).toBe("git commit -m x");
    expect(well.className).toContain("font-mono");
    expect(well.className).toContain("line-clamp-2");
  });

  it("joins project, issue and age in the meta line", () => {
    render(InboxCard, { props: { model: rowModel(session({ delivery: "ci_failed", issue: "NOR-411", age: "22m" })), projectLabel: "Nori App" } });
    expect(document.querySelector("[data-meta]")!.textContent!.replace(/\s+/g, " ").trim()).toBe("Nori App \u00B7 NOR-411 \u00B7 22m");
  });

  it("drops an empty project from the meta line", () => {
    render(InboxCard, { props: { model: rowModel(session({ delivery: "ci_failed", issue: "NOR-411", age: "22m" })), projectLabel: "" } });
    expect(document.querySelector("[data-meta]")!.textContent!.replace(/\s+/g, " ").trim()).toBe("NOR-411 \u00B7 22m");
  });

  it("flashes the approval after a sent Approve", async () => {
    const flash = vi.fn();
    (store as unknown as { setFlash: unknown }).setFlash = flash;
    mocks.state = talkState("h1");
    render(InboxCard, { props: { model: waiting(), projectLabel: "Proj", watch: true } });
    await vi.advanceTimersByTimeAsync(APPROVE_SETTLE_MS + 1);
    await fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    await vi.advanceTimersByTimeAsync(1);
    expect(flash).toHaveBeenCalledWith("Approved \u00B7 NOR-1", "good");
  });

  it("shows Approve only for the exact Yes and not when unwatched", () => {
    mocks.state = talkState("h1");
    render(InboxCard, { props: { model: waiting(), projectLabel: "Proj", watch: false } });
    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull();
  });

  it("puts the kind in the chip and Project · issue in the meta line", () => {
    const m = rowModel(session({ agentState: "waiting_input", inputReason: "permission_prompt" }));
    render(InboxCard, { props: { model: m, projectLabel: "Nori App" } });
    expect(screen.getByText("Allow this?")).toBeTruthy();
    expect(document.querySelector("[data-meta]")!.textContent!.replace(/\s+/g, " ").trim()).toBe("Nori App \u00B7 NOR-1 \u00B7 1m");
  });

  it("does not trigger the card tap from the action button", async () => {
    render(InboxCard, { props: { model: rowModel(session({ delivery: "merge_conflict" })), projectLabel: "P" } });
    await fireEvent.click(screen.getByRole("button", { name: "Resolve" }));
    expect(router.top).toMatchObject({ name: "tab", tab: "inbox" });
  });

  it("falls back to Answer without the answer tier", () => {
    tiers.set(["read"]);
    mocks.state = talkState("h1");
    render(InboxCard, { props: { model: waiting(), projectLabel: "Proj", watch: true } });
    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull();
  });

  it("disables Approve again when the prompt changes", async () => {
    mocks.state = talkState("h1");
    const { rerender } = render(InboxCard, { props: { model: waiting(), projectLabel: "Proj", watch: true } });
    await vi.advanceTimersByTimeAsync(APPROVE_SETTLE_MS + 1);
    mocks.state = talkState("h2");
    await rerender({ model: rowModel(session({ agentState: "waiting_input", title: "x" })), projectLabel: "Proj", watch: true });
    expect(screen.getByRole("button", { name: "Approve" })).toBeDisabled();
  });

  it("settles once for a prompt pushed again with the same hash", async () => {
    mocks.state = talkState("h1");
    const { rerender } = render(InboxCard, { props: { model: waiting(), projectLabel: "Proj", watch: true } });
    await vi.advanceTimersByTimeAsync(APPROVE_SETTLE_MS / 2);
    mocks.state = talkState("h1");
    await rerender({ model: rowModel(session({ agentState: "waiting_input", title: "y" })), projectLabel: "Proj", watch: true });
    await vi.advanceTimersByTimeAsync(APPROVE_SETTLE_MS / 2 + 1);
    expect(screen.getByRole("button", { name: "Approve" })).toBeEnabled();
  });

  it.each([
    ["merge_conflict", "Resolve"],
    ["changes_requested", "Fix"],
    ["approved", "Merge"],
  ])("%s shows %s", (delivery, label) => {
    render(InboxCard, { props: { model: rowModel(session({ agentState: "idle", delivery })), projectLabel: "P" } });
    expect(screen.getByRole("button", { name: label })).toBeTruthy();
  });

  it("opens the Review segment for a card in the review bucket and for an approved PR", async () => {
    render(InboxCard, { props: { model: rowModel(session({ agentState: "idle", delivery: "review_pending", prNumber: 3 })), projectLabel: "P" } });
    await fireEvent.click(screen.getByRole("button", { name: /Fix it/ }));
    expect(router.top).toMatchObject({ name: "session", id: "a", seg: "review" });
    cleanup();
    router.stack = [{ name: "tab", tab: "inbox" }];
    render(InboxCard, { props: { model: rowModel(session({ agentState: "idle", delivery: "approved", prNumber: 3 })), projectLabel: "P" } });
    await fireEvent.click(screen.getByRole("button", { name: /Fix it/ }));
    expect(router.top).toMatchObject({ name: "session", id: "a", seg: "review" });
  });

  it("routes the non-command actions", async () => {
    render(InboxCard, { props: { model: rowModel(session({ delivery: "ci_failed" })), projectLabel: "P" } });
    await fireEvent.click(screen.getByRole("button", { name: /Fix it/ }));
    expect(router.top).toMatchObject({ name: "session", seg: "review" });
    cleanup();
    router.stack = [{ name: "tab", tab: "inbox" }];
    render(InboxCard, { props: { model: rowModel(session({ delivery: "ci_failed" })), projectLabel: "P" } });
    await fireEvent.click(screen.getByRole("button", { name: "See checks" }));
    expect(router.top).toMatchObject({ name: "session", seg: "review" });
    cleanup();
    router.stack = [{ name: "tab", tab: "inbox" }];
    render(InboxCard, { props: { model: rowModel(session({ agentState: "idle", delivery: "approved" })), projectLabel: "P" } });
    await fireEvent.click(screen.getByRole("button", { name: "Merge" }));
    expect(router.sheet).toBe("merge");
  });

  it("runs Resolve and Address through their commands", async () => {
    render(InboxCard, { props: { model: rowModel(session({ delivery: "merge_conflict" })), projectLabel: "P" } });
    await fireEvent.click(screen.getByRole("button", { name: "Resolve" }));
    expect(mocks.resolve).toHaveBeenCalledWith("a");
    cleanup();
    render(InboxCard, { props: { model: rowModel(session({ delivery: "changes_requested", prNumber: 9 })), projectLabel: "P" } });
    await fireEvent.click(screen.getByRole("button", { name: "Fix" }));
    expect(mocks.address).toHaveBeenCalledWith("a", 9);
  });

  it("shows a queued Address answer and holds the action at Requested", async () => {
    mocks.address.mockResolvedValue({ item: "q1", delivered: false, why: "Queued until the agent rests." });
    const model = () =>
      rowModel(session({ id: "q", delivery: "changes_requested", prNumber: 4, agentStateSince: "2026-11-14T10:00:00Z" }));
    render(InboxCard, { props: { model: model(), projectLabel: "P" } });
    await fireEvent.click(screen.getByRole("button", { name: "Fix" }));
    await vi.waitFor(() => expect(screen.getByText("Queued until the agent rests.")).toBeTruthy());
    const held = screen.getByRole("button", { name: "Requested" });
    expect(held).toBeDisabled();
    await fireEvent.click(held);
    // A remount (a row scrolled back into the window) still knows.
    cleanup();
    render(InboxCard, { props: { model: model(), projectLabel: "P" } });
    expect(screen.getByRole("button", { name: "Requested" })).toBeDisabled();
    expect(mocks.address).toHaveBeenCalledTimes(1);
  });

  it("says the agent is gone and leaves Address pressable", async () => {
    mocks.address.mockResolvedValue({ item: "", delivered: false, agentGone: true });
    render(InboxCard, {
      props: { model: rowModel(session({ id: "g", delivery: "changes_requested", prNumber: 5 })), projectLabel: "P" },
    });
    await fireEvent.click(screen.getByRole("button", { name: "Fix" }));
    await vi.waitFor(() => expect(screen.getByRole("alert").textContent).toBe("The agent has exited."));
    expect(screen.getByRole("button", { name: "Fix" })).not.toBeDisabled();
  });

  it("disables a control action the device lacks the tier for", () => {
    tiers.set(["read", "answer"]);
    render(InboxCard, { props: { model: rowModel(session({ delivery: "merge_conflict" })), projectLabel: "P" } });
    expect(screen.getByRole("button", { name: "Resolve" })).toBeDisabled();
    expect(screen.getByText("This phone can't do that.")).toBeTruthy();
  });

  it("keeps Show all while the well is clamped, and expands in place", async () => {
    mocks.state = {
      prompt: { kind: "permission", hash: "h", lines: Array.from({ length: 9 }, (_, i) => `l${i}`), choices: [] },
    };
    // jsdom has no layout: report a clamped well.
    const scroll = vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(100);
    const client = vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(40);
    const { container } = render(InboxCard, { props: { model: waiting(), projectLabel: "P", watch: true } });
    await vi.advanceTimersByTimeAsync(1);
    expect(container.querySelector("[data-prompt]")!.className).toContain("line-clamp-2");
    await fireEvent.click(screen.getByRole("button", { name: "Show all" }));
    expect(container.querySelector("[data-prompt]")!.className).not.toContain("line-clamp");
    expect(container.querySelector("[data-prompt]")!.textContent).toContain("l8");
    scroll.mockRestore();
    client.mockRestore();
  });

  it("offers no Show all when the text fits", () => {
    mocks.state = talkState("h1");
    render(InboxCard, { props: { model: waiting(), projectLabel: "P", watch: true } });
    expect(screen.queryByRole("button", { name: "Show all" })).toBeNull();
  });
});
