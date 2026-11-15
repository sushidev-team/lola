import { describe, expect, it } from "vitest";
import { fireEvent, waitFor } from "@testing-library/svelte";
import { openPreview, PAGE_TIMEOUT } from "./page.helpers";

describe("preview page: review package on the screenshots tab, review-package", () => {
  it("opens on stacked screenshot cards with no Capture, and a tap opens the full-screen viewer", async () => {
    await openPreview("?fixture=review-package&route=/session/s5/review&sheet=reviewPackage&tab=visuals");
    await waitFor(() => expect(document.querySelector("[data-review-screens]")).not.toBeNull(), { timeout: PAGE_TIMEOUT });
    const cards = Array.from(document.querySelectorAll("[data-screen-card]")).map((c) => c.getAttribute("aria-label"));
    expect(cards).toEqual(["Screen 1: Light", "Screen 2: Dark"]);
    expect(document.querySelector("[data-review-capture]")).toBeNull();
    expect(document.querySelector(".review-visuals")).toBeNull();
    const open = await waitFor(() => {
      const b = document.querySelector<HTMLButtonElement>('[data-screen-card="0"] button[aria-label^="Open"]');
      expect(b?.disabled).toBe(false);
      return b!;
    }, { timeout: PAGE_TIMEOUT });
    await fireEvent.click(open);
    expect(document.querySelector("[data-screen-viewer]")?.textContent).toContain("1 of 2");
  }, PAGE_TIMEOUT * 2);
});
