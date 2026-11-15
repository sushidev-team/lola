import { describe, expect, it } from "vitest";
import { waitFor } from "@testing-library/svelte";
import { openPreview, PAGE_TIMEOUT } from "./page.helpers";

describe("preview page: session, merge-ready", () => {
  it("keeps the review segment inside a 390 px wide root and shows one merge button", async () => {
    const app = await openPreview("?fixture=merge-ready&route=/session/s5/review");
    await waitFor(() => expect(app.querySelector("[data-review-segment]")).not.toBeNull(), { timeout: PAGE_TIMEOUT });
    const root = app.querySelector<HTMLElement>("[data-session-screen]")!;
    expect(root.className).toContain("grid-cols-[minmax(0,1fr)]");
    root.style.width = "390px";
    expect(root.scrollWidth).toBeLessThanOrEqual(root.clientWidth);
    // BeforeMerge's own split button is hidden: the sticky bar owns the one Merge.
    await waitFor(() => expect(app.textContent).toContain("Approved"), { timeout: PAGE_TIMEOUT });
    const merges = Array.from(app.querySelector("[data-review-segment]")!.querySelectorAll("button")).filter((b) => /merge/i.test(b.textContent ?? "") && !/Files/.test(b.textContent ?? ""));
    expect(merges.map((b) => b.textContent?.trim())).toEqual(["Merge"]);
    // The actions are one grouped list; the old "Review PR" / "#n" button pair is gone.
    expect(app.querySelector("[data-review-actions]")).not.toBeNull();
    const all = Array.from(app.querySelector("[data-review-segment]")!.querySelectorAll("button"));
    expect(all.some((b) => /^\s*Review PR\s*$/.test(b.textContent ?? ""))).toBe(false);
  }, PAGE_TIMEOUT * 2);
});
