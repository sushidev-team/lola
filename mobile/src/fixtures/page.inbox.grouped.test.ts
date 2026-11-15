import { describe, expect, it } from "vitest";
import { waitFor } from "@testing-library/svelte";
import { openPreview, PAGE_TIMEOUT } from "./page.helpers";

describe("preview page: grouped inbox", () => {
  it("busy-grouped seeds the grouping, so the list is grouped by project with no tap", async () => {
    const app = await openPreview("?fixture=busy-grouped&route=/inbox");
    await waitFor(() => expect(app.querySelector('[data-fixture="busy-grouped"]')).not.toBeNull(), { timeout: PAGE_TIMEOUT });
    await waitFor(() => expect(app.querySelector("h2")).not.toBeNull(), { timeout: PAGE_TIMEOUT });
    const heads = [...app.querySelectorAll("h2")].map((h) => h.textContent ?? "");
    expect(heads.some((t) => t.includes("Nori App"))).toBe(true);
    expect(heads.some((t) => t.includes("Okane"))).toBe(true);
    expect(heads.some((t) => t.includes("Needs you"))).toBe(false);
    expect(app.textContent).toContain("Allow this?");
  }, PAGE_TIMEOUT * 2);
});
