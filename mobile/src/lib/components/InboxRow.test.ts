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
});
