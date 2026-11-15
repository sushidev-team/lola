import { describe, expect, it } from "vitest";
import { waitFor } from "@testing-library/svelte";
import { openPreview, PAGE_TIMEOUT } from "./page.helpers";

// One page per test file: the scan state's web layer has its own.
describe("preview page: connect scan", () => {
  it("shows the scan line although the web layer has no camera", async () => {
    const app = await openPreview("?fixture=pairing&route=/connect");
    // Settled: the scan attempt found no camera and the fixture put the scan view back.
    await waitFor(
      () => {
        expect(app.textContent).toContain("Scan the code in Lola on your Mac.");
        expect(app.querySelector('[data-glyph="camera"]')).not.toBeNull();
      },
      { timeout: PAGE_TIMEOUT },
    );
  }, PAGE_TIMEOUT * 2);
});
