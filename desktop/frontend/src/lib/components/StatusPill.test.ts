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

// The pill is the AGENT axis now. Before the split it carried the rolled-up
// status, which meant the runner was invisible behind every delivery word once
// a PR existed — and 90% of the `needs_input` it did surface came from the
// coding agent's 60s idle nudge rather than from a question.
describe("StatusPill agent axis", () => {
  beforeEach(() => cleanup());

  it("says what the AGENT is doing, not what the PR is doing", () => {
    render(StatusPill, { agentState: "working", delivery: "review_pending", status: "review_pending" });
    expect(screen.getByText("working")).toBeInTheDocument();
    expect(screen.queryByText("review")).not.toBeInTheDocument();
  });

  it("reads 'idle' for a resting agent instead of the old 'needs you'", () => {
    render(StatusPill, { agentState: "idle", delivery: "none", status: "idle" });
    expect(screen.getByText("idle")).toBeInTheDocument();
  });

  it("collapses exited and dead into one word", () => {
    render(StatusPill, { agentState: "exited", delivery: "draft", status: "draft" });
    expect(screen.getByText("gone")).toBeInTheDocument();
  });

  it("falls back to the rolled-up status when the session carries no axes", () => {
    // A daemon predating the split sends neither axis, and displayFor("")
    // answers "working" by design — which would draw a parked PR as a live
    // agent. With the rollup word in hand the pre-axis pill is what it was.
    render(StatusPill, { status: "review_pending" });
    expect(screen.getByText("review")).toBeInTheDocument();
  });
});

// "needs you" is a status; "needs you · permission prompt" is an instruction.
// inputReason was on the wire and read by nothing in the app, so the only way to
// learn what was being asked was to open the terminal.
describe("StatusPill input reason", () => {
  beforeEach(() => cleanup());

  it("names WHY the agent is blocked", () => {
    render(StatusPill, { agentState: "waiting_input", inputReason: "permission_prompt" });
    expect(screen.getByText("needs you")).toBeInTheDocument();
    expect(screen.getByText(/permission prompt/)).toBeInTheDocument();
  });

  it("stays silent about the idle nudge, which is no longer a reason", () => {
    render(StatusPill, { agentState: "waiting_input", inputReason: "idle_notification" });
    expect(screen.getByText("needs you")).toBeInTheDocument();
    expect(screen.queryByText(/idle/)).not.toBeInTheDocument();
  });

  it("never prints a reason under a pill that is not 'needs you'", () => {
    // The daemon leaves inputReason set on records it has since moved off
    // waiting_input, so gating on the reason alone prints a stale explanation.
    render(StatusPill, { agentState: "working", inputReason: "question" });
    expect(screen.queryByText(/question/)).not.toBeInTheDocument();
  });
});

// The conflict action moved to the DELIVERY axis, because the pill's own word is
// now "working" on exactly the session worth offering the merge to.
describe("StatusPill conflict action follows the delivery axis", () => {
  beforeEach(() => cleanup());

  it("offers resolve on a working agent whose branch conflicts", () => {
    render(StatusPill, {
      agentState: "working",
      delivery: "merge_conflict",
      onResolve: () => {},
      resolveBranch: "develop",
    });
    const btn = screen.getByRole("button");
    expect(btn).toHaveTextContent("working");
    expect(btn).toHaveTextContent("resolve");
  });

  it("does not offer it when only the AGENT word looks alarming", () => {
    render(StatusPill, { agentState: "waiting_input", delivery: "ci_failed", onResolve: () => {} });
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
});
