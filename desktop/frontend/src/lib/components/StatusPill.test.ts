import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";
import StatusPill from "./StatusPill.svelte";
import PillHarness from "./PillHarness.test.svelte";

// The conflict pill is the one status badge that is also an ACTION: it morphs to
// "resolve" under the cursor and hands the merge to the session's agent. These
// pin the three things that make it safe to put on a clickable row — it only
// appears where a call site opted in, the swap is CSS on a real button (so the
// keyboard reaches it), and its click never falls through to the row.
describe("StatusPill conflict action", () => {
  beforeEach(() => cleanup());

  it("stays a plain badge when the call site offers no resolve action", () => {
    render(StatusPill, { status: "merge_conflict" });
    expect(screen.getByText("conflict")).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("stays a plain badge for every other status, even with a handler", () => {
    render(StatusPill, { status: "ci_failed", onResolve: () => {} });
    expect(screen.getByText("ci failed")).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("becomes a button carrying BOTH words, so the swap costs no reflow", () => {
    render(StatusPill, { status: "merge_conflict", onResolve: () => {}, resolveBranch: "develop" });
    const btn = screen.getByRole("button");
    // Both labels are in the DOM at once — one is hidden with `invisible`, which
    // still reserves its width. The hover swap is CSS (group-hover), so it needs
    // no state and cannot leave the pill mid-morph.
    expect(btn).toHaveTextContent("conflict");
    expect(btn).toHaveTextContent("resolve");
  });

  it("names the project's default branch in its tooltip", () => {
    render(StatusPill, { status: "merge_conflict", onResolve: () => {}, resolveBranch: "develop" });
    expect(screen.getByRole("button")).toHaveAttribute("title", expect.stringContaining("develop"));
  });

  it("says 'the default branch' rather than guessing one when the project is unknown", () => {
    render(StatusPill, { status: "merge_conflict", onResolve: () => {} });
    const title = screen.getByRole("button").getAttribute("title") ?? "";
    expect(title).toContain("the default branch");
    expect(title).not.toContain("main");
  });

  it("runs the action and keeps the click off the row underneath", async () => {
    const onResolve = vi.fn();
    const rowClick = vi.fn();
    render(PillHarness, { status: "merge_conflict", onResolve, rowClick });
    await fireEvent.click(screen.getByRole("button"));
    expect(onResolve).toHaveBeenCalledOnce();
    expect(rowClick).not.toHaveBeenCalled();
  });
});
