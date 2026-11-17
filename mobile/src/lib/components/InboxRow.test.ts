import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import InboxRow from "./InboxRow.svelte";
import { rowModel } from "@mobile/lib/inbox.svelte";
import type { SessionInfo } from "$lib/store.svelte";

afterEach(cleanup);

const model = () =>
  rowModel({ id: "a", project: "p", issue: "NOR-411", title: "Do it", branch: "b", agentState: "working", delivery: "none", age: "3m", prNumber: 0 } as SessionInfo);

describe("InboxRow", () => {
  it("in a sectioned list drops the state word and shows project and issue", async () => {
    const onopen = vi.fn();
    render(InboxRow, { props: { model: model(), projectLabel: "Nori App", onopen } });
    expect(screen.getByText("Do it")).toBeTruthy();
    expect(screen.queryByText("Working")).toBeNull();
    expect(screen.getByText("Nori App · NOR-411")).toBeTruthy();
    expect(screen.getByText("3m")).toBeTruthy();
    await fireEvent.click(screen.getByRole("button"));
    expect(onopen).toHaveBeenCalled();
  });

  it("opens the Review segment for a row in the review bucket", async () => {
    const { router } = await import("@mobile/lib/router.svelte");
    router.stack = [{ name: "tab", tab: "inbox" }];
    const onopen = vi.fn();
    const m = rowModel({ id: "r", project: "p", issue: "NOR-9", title: "Ready", branch: "b", agentState: "idle", delivery: "review_pending", age: "1m", prNumber: 2 } as SessionInfo);
    expect(m.bucket).toBe("review");
    render(InboxRow, { props: { model: m, projectLabel: "P", onopen } });
    await fireEvent.click(screen.getByRole("button"));
    expect(onopen).not.toHaveBeenCalled();
    expect(router.top).toMatchObject({ name: "session", id: "r", seg: "review" });
  });

  it("leads every row with a status dot, and says the state in its composite label, not its meta line", () => {
    const { container } = render(InboxRow, { props: { model: model(), projectLabel: "Nori App", onopen: () => {} } });
    expect(container.querySelector("span.rounded-full")).not.toBeNull();
    expect(screen.queryByText("Working")).toBeNull();
    expect(screen.getByRole("button").getAttribute("aria-label")).toMatch(/^Working, Do it, Nori App /);
  });

  it("truncates the title to one line", () => {
    render(InboxRow, { props: { model: model(), projectLabel: "P", onopen: () => {} } });
    expect(screen.getByText("Do it").className).toContain("truncate");
  });

  it("shows the queue place and says it in the composite label", () => {
    const m = rowModel({ id: "a", project: "p", issue: "NOR-411", title: "Do it", branch: "b", agentState: "idle", delivery: "approved", age: "3m", prNumber: 4, mergeQueue: { position: 2, step: "waiting" } } as SessionInfo);
    render(InboxRow, { props: { model: m, projectLabel: "Nori App", onopen: () => {} } });
    expect(screen.getByText("Queue #2")).toBeTruthy();
    expect(screen.getByRole("button").getAttribute("aria-label")).toMatch(/Queue #2 · waiting$/);
  });

  it("has no queue chip for a session that is not queued", () => {
    render(InboxRow, { props: { model: model(), projectLabel: "Nori App", onopen: () => {} } });
    expect(screen.queryByText(/Queue/)).toBeNull();
  });
});
