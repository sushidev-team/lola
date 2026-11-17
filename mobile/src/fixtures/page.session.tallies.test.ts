import { describe, expect, it } from "vitest";
import { waitFor } from "@testing-library/svelte";
import { openPreview, PAGE_TIMEOUT } from "./page.helpers";

describe("preview page: review segment with a review tally, merge-ready", () => {
  it("shows the agent review's findings, the green CI summary and the review material row", async () => {
    const app = await openPreview("?fixture=merge-ready&route=/session/s5/review");
    await waitFor(() => expect(app.querySelector("[data-review-actions]")).not.toBeNull(), { timeout: PAGE_TIMEOUT });
    const actions = app.querySelector("[data-review-actions]")!;
    expect(actions.textContent).toContain("Agent review");
    expect(actions.textContent).toContain("1 major · 2 minor");
    expect(actions.textContent).toContain("Review material");
    await waitFor(() => expect(app.querySelector("[data-review-checks]")?.textContent).toContain("All 2 passed"), { timeout: PAGE_TIMEOUT });
  }, PAGE_TIMEOUT * 2);
});
