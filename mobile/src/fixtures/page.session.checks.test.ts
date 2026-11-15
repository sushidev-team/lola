import { describe, expect, it } from "vitest";
import { waitFor } from "@testing-library/svelte";
import { openPreview, PAGE_TIMEOUT } from "./page.helpers";

describe("preview page: checks sheet, ci-red", () => {
  it("groups the PR's checks, labels the review gate and agent-reported checks, and offers Ask agent to fix", async () => {
    const app = await openPreview("?fixture=ci-red&route=/session/s6/review&sheet=checks");
    await waitFor(() => expect(document.querySelector("[data-checks-section=failing]")).not.toBeNull(), { timeout: PAGE_TIMEOUT });
    const failing = document.querySelector("[data-checks-section=failing]")!;
    expect(failing.textContent).toContain("Unit tests");
    expect(failing.textContent).toContain("Required");
    expect(failing.textContent).toContain("Parallel suite");
    expect(document.querySelector("[data-checks-section=running]")?.textContent).toContain("Browser tests");
    expect(document.querySelector("[data-checks-section=passed]")?.textContent).toContain("Typecheck");
    expect(document.querySelector("[data-checks-section=skipped]")?.textContent).toContain("Deploy preview");
    expect(document.querySelector("[data-checks-section=gates]")?.textContent).toContain("lola/review/codex-session");
    expect(document.querySelector("[data-checks-section=local]")?.textContent).toContain("Reported by the agent, not CI.");
    const buttons = Array.from(document.querySelectorAll("button")).map((b) => b.textContent?.trim());
    expect(buttons).toContain("Ask agent to fix");
    // The Review segment behind it summarises the same list.
    await waitFor(() => expect(app.querySelector("[data-review-checks]")?.textContent).toContain("2 failed · 2 passed"), { timeout: PAGE_TIMEOUT });
  }, PAGE_TIMEOUT * 2);
});
