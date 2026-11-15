import { describe, expect, it } from "vitest";
import { waitFor } from "@testing-library/svelte";
import { openPreview, PAGE_TIMEOUT } from "./page.helpers";

// One page per file (the preview harness mounts one app per test file).
describe("preview page: activity", () => {
  it("lists recently ended sessions after the events, and the rows are buttons", async () => {
    const app = await openPreview("?route=/activity&fixture=busy");
    await waitFor(() => expect(app.querySelector("h1")?.textContent).toBe("Activity"), { timeout: PAGE_TIMEOUT });
    // The list is virtualized: the ended block is at the end, so scroll the list there.
    await waitFor(() => expect(app.querySelectorAll("button").length).toBeGreaterThan(0), { timeout: PAGE_TIMEOUT });
    const scroller = Array.from(app.querySelectorAll<HTMLElement>("*")).find((e) => e.scrollHeight > e.clientHeight + 50 && getComputedStyle(e).overflowY !== "visible");
    if (scroller) scroller.scrollTop = scroller.scrollHeight;
    await waitFor(() => expect(app.textContent).toContain("Recently ended"), { timeout: PAGE_TIMEOUT });
    expect(app.textContent).toContain("Retire the legacy period picker");
  }, PAGE_TIMEOUT * 2);
});
