import { describe, expect, it } from "vitest";
import { waitFor } from "@testing-library/svelte";
import { openPreview, PAGE_TIMEOUT } from "./page.helpers";

describe("preview page: session, ci-red", () => {
  it("makes See checks primary with the failing count and a Merge anyway… text link, no 'Review PR'", async () => {
    const app = await openPreview("?fixture=ci-red&route=/session/s6/review");
    await waitFor(() => expect(app.querySelector("[data-review-bar]")).not.toBeNull(), { timeout: PAGE_TIMEOUT });
    const bar = app.querySelector("[data-review-bar]")!;
    const labels = Array.from(bar.querySelectorAll("button")).map((b) => b.textContent?.trim());
    expect(labels).toContain("See checks");
    expect(labels).toContain("Merge anyway…");
    expect(labels).not.toContain("Merge");
    expect(bar.querySelector("[data-ci-failed]")?.textContent).toBe("2 checks failed");
    const all = Array.from(app.querySelector("[data-review-segment]")!.querySelectorAll("button"));
    expect(all.some((b) => /^\s*Review PR\s*$/.test(b.textContent ?? ""))).toBe(false);
  }, PAGE_TIMEOUT * 2);
});
