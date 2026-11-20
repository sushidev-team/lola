import { describe, expect, it } from "vitest";
import { waitFor } from "@testing-library/svelte";
import { openPreview, PAGE_TIMEOUT } from "./page.helpers";

const banners = () => [...document.querySelectorAll(".banner")];

// One page per file (the preview harness mounts one app per test file).
describe("preview page: inbox offline", () => {
  it("draws the connection banner once, below the Inbox's large title", async () => {
    const app = await openPreview("?route=/inbox&fixture=offline");
    await waitFor(() => expect(app.querySelector("h1")?.textContent).toBe("Sessions"), { timeout: PAGE_TIMEOUT });
    await waitFor(() => expect(banners().length).toBeGreaterThan(0), { timeout: PAGE_TIMEOUT });
    // Let both owners (the App shell and the Inbox) settle before counting.
    await new Promise((r) => setTimeout(r, 300));
    // The App shell must not draw its own copy over the Inbox.
    expect(banners().map((b) => b.textContent?.trim())).toHaveLength(1);
    const title = app.querySelector("h1")!;
    expect(title.compareDocumentPosition(banners()[0]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  }, PAGE_TIMEOUT * 2);
});
