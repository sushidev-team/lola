import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/svelte";

const review = vi.hoisted(() => vi.fn());
vi.mock("$lib/components/session/BeforeMerge.svelte", async () => await import("./BeforeMergeStub.test.svelte"));
vi.mock("@bindings/desktop", async (orig) => {
  const real = await orig<typeof import("@bindings/desktop")>();
  return { ...real, DaemonService: { ...real.DaemonService, Review: review } };
});
vi.mock("$lib/components/DiffView.svelte", async () => await import("./Stub.test.svelte"));

import ReviewSegment, { ciSummary, mergeBlock, reviewTally } from "./ReviewSegment.svelte";
import { reviewChecks } from "@mobile/lib/reviewchecks.svelte";
import { feedback } from "$lib/feedback.svelte";
import { store } from "$lib/store.svelte";
import { router } from "@mobile/lib/router.svelte";
import { tiers } from "@mobile/lib/tiers.svelte";

describe("mergeBlock", () => {
  it("names why Merge is off", () => {
    expect(mergeBlock({ prNumber: 1 }, false)).toBe("This phone can't do that.");
    expect(mergeBlock({ prNumber: 0 }, true)).toBe("No PR yet.");
    expect(mergeBlock({ prNumber: 1, delivery: "merged" }, true)).toBe("Already merged.");
    expect(mergeBlock({ prNumber: 1, delivery: "ci_failed" }, true)).toBe("");
    expect(mergeBlock({ prNumber: 1, delivery: "approved" }, true, true)).toBe("Couldn't check the PR.");
    expect(mergeBlock({ prNumber: 1, delivery: "approved" }, true, false)).toBe("");
  });
});

describe("ReviewSegment", () => {
  beforeEach(() => {
    tiers.set(["read", "control"]);
    store.sessions = [{ id: "s1", prNumber: 4, delivery: "approved" } as never];
    feedback.clear("s1");
    router.sheet = "";
    review.mockReset();
    reviewChecks.last = null;
    (globalThis as { __reviewFacts?: unknown }).__reviewFacts = undefined;
  });

  it("counts drafts on the feedback button and opens the sheets", async () => {
    feedback.add("s1", { path: "a.go", side: "new" as never, line: 1, endLine: 1, quote: "", body: "x" });
    render(ReviewSegment, { props: { id: "s1", packages: 2 } });
    await fireEvent.click(screen.getByRole("button", { name: "Send feedback (1)" }));
    expect(router.sheet).toBe("feedback");
    await fireEvent.click(screen.getByRole("button", { name: "Merge" }));
    expect(router.sheet).toBe("merge");
    await fireEvent.click(screen.getByText("Review material"));
    expect(router.sheet).toBe("reviewPackage");
  });

  it("hides Send feedback at zero drafts and lets Merge take the bar", () => {
    render(ReviewSegment, { props: { id: "s1" } });
    expect(screen.queryByRole("button", { name: /Send feedback/ })).toBeNull();
    expect(screen.getByRole("button", { name: "Merge" })).toBeEnabled();
  });

  it("disables Merge without the tier and says why, visibly", () => {
    tiers.set(["read"]);
    render(ReviewSegment, { props: { id: "s1" } });
    const merge = screen.getByRole("button", { name: "Merge" });
    expect(merge).toBeDisabled();
    expect(merge).toHaveAttribute("aria-describedby", "merge-block");
    const why = document.getElementById("merge-block")!;
    expect(why).toHaveTextContent("This phone can't do that.");
    expect(why.className).not.toContain("sr-only");
  });

  it("on a red CI the safe step is primary: See checks, with Merge anyway as a quiet text link", async () => {
    store.sessions = [
      { id: "s1", prNumber: 4, prUrl: "https://github.com/a/b/pull/4", delivery: "ci_failed", board: { checks: [{ name: "a", state: "fail" }, { name: "b", state: "fail" }] } } as never,
    ];
    const open = vi.spyOn(store, "openURL").mockImplementation(async () => {});
    render(ReviewSegment, { props: { id: "s1" } });
    expect(document.querySelector("[data-ci-failed]")).toHaveTextContent("2 checks failed");
    expect(screen.queryByRole("button", { name: "Merge" })).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "See checks" }));
    expect(router.sheet).toBe("checks");
    expect(open).not.toHaveBeenCalled();
    const anyway = screen.getByRole("button", { name: "Merge anyway…" });
    expect(anyway).toHaveAttribute("data-merge-anyway");
    expect(anyway.className).toContain("h-11");
    await fireEvent.click(anyway);
    expect(router.sheet).toBe("merge");
    open.mockRestore();
  });

  it("falls back to 'CI failed' from copy when no failing check is named", () => {
    store.sessions = [{ id: "s1", prNumber: 4, prUrl: "https://github.com/a/b/pull/4", delivery: "ci_failed" } as never];
    render(ReviewSegment, { props: { id: "s1" } });
    expect(document.querySelector("[data-ci-failed]")).toHaveTextContent("CI failed");
  });

  it("names the unknown-GitHub block in copy (drawn sr-only, never visibly)", () => {
    const mergeBlockOf = mergeBlock({ prNumber: 4, delivery: "approved" }, true, true);
    expect(mergeBlockOf).toBe("Couldn't check the PR.");
  });

  it("the Agent review row carries the review value", () => {
    store.sessions = [{ id: "s1", prNumber: 4, prUrl: "https://github.com/a/b/pull/4", delivery: "pr_open" } as never];
    const first = render(ReviewSegment, { props: { id: "s1" } });
    expect(document.querySelector("[data-review-actions]")).toHaveTextContent("Not run");
    first.unmount();
    store.sessions = [{ id: "s1", prNumber: 4, prUrl: "https://github.com/a/b/pull/4", delivery: "review_pending" } as never];
    render(ReviewSegment, { props: { id: "s1" } });
    expect(document.querySelector("[data-review-actions]")).toHaveTextContent("Required");
  });

  it("has no 'Review PR' or '#n' button; the actions are one grouped list", () => {
    store.sessions = [{ id: "s1", prNumber: 4, prUrl: "https://github.com/a/b/pull/4", delivery: "review_pending" } as never];
    render(ReviewSegment, { props: { id: "s1", packages: 1 } });
    expect(screen.queryByRole("button", { name: "Review PR" })).toBeNull();
    expect(screen.queryByRole("button", { name: /^#4/ })).toBeNull();
    const group = document.querySelector("[data-review-actions]")!;
    expect(group).toHaveTextContent("Agent review");
    expect(group).toHaveTextContent("Review material");
    expect(group).toHaveTextContent("1 version");
    expect(group).toHaveTextContent("Open #4 on GitHub");
  });

  it("asks the agent for a review through the Agent review row, not once approved", async () => {
    review.mockResolvedValue({ started: true, message: "review started: codex" });
    render(ReviewSegment, { props: { id: "s1" } });
    expect(screen.queryByText("Agent review")).toBeNull(); // delivery is approved in beforeEach
    store.sessions = [{ id: "s1", prNumber: 4, delivery: "review_pending" } as never];
    await fireEvent.click(await screen.findByText("Agent review"));
    expect(review).toHaveBeenCalledWith("s1", "");
    expect(await screen.findByText("Review started; findings appear on the pull request.")).toBeInTheDocument();
  });

  it("shows a running review and the current PR's tally, and does not start a second one", async () => {
    store.sessions = [{ id: "s1", prNumber: 4, delivery: "review_pending", reviewRunning: true } as never];
    const first = render(ReviewSegment, { props: { id: "s1" } });
    expect(document.querySelector("[data-review-actions]")).toHaveTextContent("Running");
    await fireEvent.click(screen.getByText("Agent review"));
    expect(review).not.toHaveBeenCalled();
    first.unmount();
    store.sessions = [{ id: "s1", prNumber: 4, delivery: "approved", reviewTallies: [{ kind: "codex", major: 2, minor: 1 }] } as never];
    render(ReviewSegment, { props: { id: "s1" } });
    // Shown once a review ran, even where a new one is not offered (approved).
    expect(document.querySelector("[data-review-actions]")).toHaveTextContent("2 major · 1 minor");
  });

  it("offers Review material on an open PR with no package yet", async () => {
    store.sessions = [{ id: "s1", prNumber: 4, delivery: "pr_open" } as never];
    render(ReviewSegment, { props: { id: "s1", packages: 0 } });
    expect(document.querySelector("[data-review-actions]")).toHaveTextContent("None yet");
    await fireEvent.click(screen.getByText("Review material"));
    expect(router.sheet).toBe("reviewPackage");
  });

  it("summarises CI from BeforeMerge's answer and hands its PR to the checks sheet", async () => {
    (globalThis as { __reviewFacts?: unknown }).__reviewFacts = {
      target: "s1",
      number: 4,
      data: { checks: "fail", checkList: [{ name: "a", state: "FAILURE", bucket: "fail" }, { name: "b", state: "SUCCESS", bucket: "pass" }] },
    };
    store.sessions = [{ id: "s1", prNumber: 4, prUrl: "https://github.com/a/b/pull/4", delivery: "ci_failed" } as never];
    render(ReviewSegment, { props: { id: "s1" } });
    expect(document.querySelector("[data-review-checks]")).toHaveTextContent("1 failed · 1 passed");
    expect(document.querySelector("[data-ci-failed]")).toHaveTextContent("1 checks failed");
    expect(reviewChecks.last).toEqual({ session: "s1", target: "s1", number: 4 });
    await fireEvent.click(screen.getByText("CI checks"));
    expect(router.sheet).toBe("checks");
  });

  it("replaces the diff with one Files changed row when Changes is offered", async () => {
    store.sessions = [{ id: "s1", prNumber: 4, delivery: "approved", worktree: "/w", tmuxName: "t" } as never];
    router.stack = [{ name: "tab", tab: "inbox" }, { name: "session", id: "s1", seg: "review" }];
    render(ReviewSegment, { props: { id: "s1" } });
    // Only BeforeMerge remains; the DiffView stub is gone.
    expect(document.querySelectorAll("[data-stub]")).toHaveLength(1);
    await fireEvent.click(screen.getByText("Files changed"));
    expect(router.top).toEqual({ name: "session", id: "s1", seg: "changes" });
  });

  it("keeps the diff when there is no checkout", () => {
    render(ReviewSegment, { props: { id: "s1" } });
    expect(screen.queryByText("Files changed")).toBeNull();
    expect(document.querySelectorAll("[data-stub]")).toHaveLength(2);
  });
});

describe("reviewTally", () => {
  it("says running, counts findings across kinds, and falls back", () => {
    expect(reviewTally({ reviewRunning: true, reviewTallies: [{ major: 1 }] }, "x")).toBe("Running");
    expect(reviewTally({ reviewTallies: [{ blocker: 1 }, { blocker: 1, other: 3 }] }, "x")).toBe("2 blockers · 3 other");
    expect(reviewTally({ reviewTallies: [{}] }, "x")).toBe("Clean");
    expect(reviewTally({}, "Not run")).toBe("Not run");
  });
});

describe("ciSummary", () => {
  const c = (bucket: string) => ({ name: bucket, state: "", bucket });
  it("counts the list, else names the rollup", () => {
    expect(ciSummary([c("fail"), c("pass"), c("pass")], "fail")).toBe("1 failed · 2 passed");
    expect(ciSummary([c("pending"), c("pass")], "pending")).toBe("1 running");
    expect(ciSummary([c("pass"), c("skipped")], "pass")).toBe("All 2 passed");
    expect(ciSummary([], "none")).toBe("No checks");
    expect(ciSummary(undefined, "pass")).toBe("Passed");
    expect(ciSummary(null, "")).toBe("");
  });
});
