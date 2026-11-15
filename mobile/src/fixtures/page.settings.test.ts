import { describe, expect, it } from "vitest";
import { waitFor } from "@testing-library/svelte";
import { openPreview, PAGE_TIMEOUT } from "./page.helpers";

// One page per file (the preview harness mounts one app per test file).
describe("preview page: settings", () => {
  it("shows the away-from-Wi-Fi row from the scripted Mac's hubStatus", async () => {
    const app = await openPreview("?route=/settings&fixture=settings");
    await waitFor(() => expect(app.querySelector("h1")?.textContent).toBe("Settings"), { timeout: PAGE_TIMEOUT });
    await waitFor(() => expect(app.textContent).toContain("Away from Wi-Fi"), { timeout: PAGE_TIMEOUT });
    await waitFor(() => expect(app.textContent).toContain("This phone reaches your Mac through its team hub."), {
      timeout: PAGE_TIMEOUT,
    });
  }, PAGE_TIMEOUT * 2);
});
