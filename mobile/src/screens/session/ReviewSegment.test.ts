import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/svelte";

vi.mock("$lib/components/session/BeforeMerge.svelte", async () => await import("./Stub.test.svelte"));
vi.mock("$lib/components/DiffView.svelte", async () => await import("./Stub.test.svelte"));

import ReviewSegment, { mergeBlock } from "./ReviewSegment.svelte";
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
  });

  it("counts drafts on the feedback button and opens the sheets", async () => {
    feedback.add("s1", { path: "a.go", side: "new" as never, line: 1, endLine: 1, quote: "", body: "x" });
    render(ReviewSegment, { props: { id: "s1", packages: 2 } });
    await fireEvent.click(screen.getByRole("button", { name: "Send feedback (1)" }));
    expect(router.sheet).toBe("feedback");
    await fireEvent.click(screen.getByRole("button", { name: "Merge" }));
    expect(router.sheet).toBe("merge");
    await fireEvent.click(screen.getByText("Review packages (2)"));
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
    expect(open).toHaveBeenCalledWith("https://github.com/a/b/pull/4/checks");
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
    expect(group).toHaveTextContent("Review packages (1)");
    expect(group).toHaveTextContent("Open #4 on GitHub");
  });

  it("asks the agent for a review through the Agent review row, not once approved", async () => {
    const review = vi.spyOn(store, "review").mockImplementation(() => undefined as never);
    render(ReviewSegment, { props: { id: "s1" } });
    expect(screen.queryByText("Agent review")).toBeNull(); // delivery is approved in beforeEach
    store.sessions = [{ id: "s1", prNumber: 4, delivery: "review_pending" } as never];
    await fireEvent.click(await screen.findByText("Agent review"));
    expect(review).toHaveBeenCalledWith("s1");
    review.mockRestore();
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
