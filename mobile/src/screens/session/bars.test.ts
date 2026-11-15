import { beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/svelte";

vi.mock("$lib/changes.svelte", () => ({ changes: { load: vi.fn(), of: () => undefined } }));
vi.mock("$lib/components/session/BeforeMerge.svelte", async () => await import("./Stub.test.svelte"));
vi.mock("$lib/components/DiffView.svelte", async () => await import("./Stub.test.svelte"));
vi.mock("$lib/components/session/ChangesView.svelte", async () => await import("./Stub.test.svelte"));

import ReviewSegment from "./ReviewSegment.svelte";
import ChangesSegment from "./ChangesSegment.svelte";
import { feedback } from "$lib/feedback.svelte";
import { store } from "$lib/store.svelte";
import { tiers } from "@mobile/lib/tiers.svelte";

// The sticky bars end inside the 34pt home-indicator zone unless they pad by the bottom safe-area inset.
describe("sticky bars clear the home indicator", () => {
  beforeEach(() => {
    tiers.set(["read", "control"]);
    store.sessions = [{ id: "s1", prNumber: 4, delivery: "approved" } as never];
    feedback.clear("s1");
    feedback.add("s1", { path: "a.go", side: "new" as never, line: 1, endLine: 1, quote: "", body: "x" });
  });

  it("the review bar", () => {
    render(ReviewSegment, { props: { id: "s1" } });
    const bars = document.querySelectorAll("[data-review-bar]");
    expect(bars.length).toBeGreaterThan(0);
    for (const b of bars) expect((b.getAttribute("class") ?? "") + (b.getAttribute("style") ?? "")).toContain("safe-area-inset-bottom");
  });

  it("the changes bar", () => {
    render(ChangesSegment, { props: { id: "s1" } });
    const bars = document.querySelectorAll("[data-changes-bar]");
    expect(bars.length).toBeGreaterThan(0);
    for (const b of bars) expect((b.getAttribute("class") ?? "") + (b.getAttribute("style") ?? "")).toContain("safe-area-inset-bottom");
  });
});
