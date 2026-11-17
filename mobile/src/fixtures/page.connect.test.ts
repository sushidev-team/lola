import { describe, expect, it } from "vitest";
import { waitFor } from "@testing-library/svelte";
import { openPreview, PAGE_TIMEOUT } from "./page.helpers";

describe("preview page: connect", () => {
  it("opens the unpaired fixture on the connect state the route names", async () => {
    const app = await openPreview("?fixture=pairing&route=/connect/expired");
    await waitFor(() => expect(app.querySelector('[data-state="expired"]')).not.toBeNull(), { timeout: PAGE_TIMEOUT });
    expect(app.querySelector('[data-glyph="timer-off"]')).not.toBeNull();
    expect(app.textContent).toContain("Code expired");
    expect(app.textContent).toContain("Make a new code on your Mac.");
    const cta = [...app.querySelectorAll("button")].find((b) => b.textContent?.includes("Scan new code"));
    expect(cta).toBeTruthy();
  }, PAGE_TIMEOUT * 2);
});
