import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";
import PrBadge from "./PrBadge.svelte";
import PrBadgeHarness from "./PrBadgeHarness.test.svelte";
import type { SessionInfo } from "$lib/store.svelte";

function pr(over: Partial<SessionInfo> = {}) {
  return {
    prNumber: 42,
    prUrl: "https://github.com/acme/eng/pull/42",
    checks: "",
    review: "",
    ...over,
  } as Pick<SessionInfo, "prNumber" | "prUrl" | "checks" | "review">;
}

// The PR badge is the DELIVERY axis' whole surface now that the pill beside it
// carries the agent axis, so it states the delivery state in words rather than
// leaving it to be inferred from a check glyph.
describe("PrBadge delivery chip", () => {
  beforeEach(() => cleanup());

  it("states the delivery state in words", () => {
    render(PrBadge, { session: pr(), delivery: "ci_failed" });
    expect(screen.getByText("#42")).toBeInTheDocument();
    expect(screen.getByText(/ci failed/)).toBeInTheDocument();
  });

  it("says nothing about a PR that is not asking for anything", () => {
    render(PrBadge, { session: pr(), delivery: "none" });
    expect(screen.getByText("#42")).toBeInTheDocument();
    expect(screen.queryByText(/none/)).not.toBeInTheDocument();
  });

  it("keeps the legacy glyph pair when no delivery axis is supplied", () => {
    // That call shape is the mobile companion's — it prints the rolled-up status
    // word itself — and it must keep working through the $lib alias unchanged.
    render(PrBadge, { session: pr({ checks: "pass" }) });
    expect(screen.getByTitle("checks pass")).toBeInTheDocument();
  });

  it("renders an em-dash when there is no PR at all", () => {
    render(PrBadge, { session: pr({ prNumber: 0 }) });
    expect(screen.getByText("—")).toBeInTheDocument();
  });
});

// The number was drawn on four surfaces and was a control on none of them:
// opening a PR needed the detail panel, the context menu, or knowing that `o` is
// bound.
describe("PrBadge open action", () => {
  beforeEach(() => cleanup());

  it("is a plain number unless the call site opts in", () => {
    // Opt-in because a kanban card and a phone row are themselves <button>s, and
    // a nested button is not parseable.
    render(PrBadge, { session: pr(), delivery: "draft" });
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("opens the PR and keeps the click off the row underneath", async () => {
    const onOpen = vi.fn();
    const rowClick = vi.fn();
    render(PrBadgeHarness, { session: pr(), onOpen, rowClick });
    await fireEvent.click(screen.getByRole("button", { name: "#42" }));
    expect(onOpen).toHaveBeenCalledOnce();
    expect(rowClick).not.toHaveBeenCalled();
  });

  it("stays a plain number when the record carries no URL to open", () => {
    // A record restored from an older snapshot can have the number and no link,
    // and a control that opens nothing is worse than a number that never claimed
    // to be one.
    render(PrBadge, { session: pr({ prUrl: "" }), onOpen: () => {} });
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(screen.getByText("#42")).toBeInTheDocument();
  });
});
