import { describe, expect, it } from "vitest";
import { waitFor } from "@testing-library/svelte";
import { openPreview, PAGE_TIMEOUT } from "./page.helpers";

describe("preview page: inbox", () => {
  it("mounts the real app on the busy fixture and lists the pushed snapshot", async () => {
    const app = await openPreview("?route=/inbox");
    await waitFor(() => expect(app.querySelector('[data-fixture="busy"]')).not.toBeNull(), { timeout: PAGE_TIMEOUT });
    await waitFor(() => expect(app.textContent).toContain("Rename the ledger columns"), { timeout: PAGE_TIMEOUT });
    expect(app.textContent).toContain("Retire the legacy period picker");
    // The needs cards lead with their kind, not "Needs you · ...".
    for (const kind of ["Allow this?", "Question", "Needs a choice", "CI failed"]) expect(app.textContent).toContain(kind);
    expect(app.textContent).not.toContain("Needs you ·");
    // The watched permission session shows its request (mono, clamped) and the one-tap Approve.
    await waitFor(() => expect(app.querySelector("[data-prompt]")).not.toBeNull(), { timeout: PAGE_TIMEOUT });
    expect(app.querySelector("[data-prompt]")?.textContent).toContain("git commit");
    await waitFor(
      () => expect([...app.querySelectorAll("button")].some((b) => b.textContent?.trim() === "Approve")).toBe(true),
      { timeout: PAGE_TIMEOUT },
    );
  }, PAGE_TIMEOUT * 2);
});
