import { describe, expect, it } from "vitest";
import { waitFor } from "@testing-library/svelte";
import { openPreview, PAGE_TIMEOUT } from "./page.helpers";

// One page per file (the preview harness mounts one app per test file).
describe("preview page: settings usage", () => {
  it("shows today's tokens and the subscription windows from the scripted Mac's status", async () => {
    const app = await openPreview("?route=/settings&fixture=settings");
    await waitFor(() => expect(app.textContent).toContain("46.7M tokens"), { timeout: PAGE_TIMEOUT });
    expect(app.textContent).toContain("38% of budget");
    expect(app.textContent).toContain("Claude 5-hour");
    expect(app.textContent).toContain("Resets in 2h");
  }, PAGE_TIMEOUT * 2);
});
