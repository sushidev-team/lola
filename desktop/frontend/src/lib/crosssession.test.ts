import { describe, it, expect } from "vitest";
import { mergeQueueChip, overlapChip } from "./crosssession";

describe("mergeQueueChip", () => {
  it("is absent when the session is not queued", () => {
    expect(mergeQueueChip(undefined)).toBeNull();
    expect(mergeQueueChip(null)).toBeNull();
  });

  it("names the position and the step, with severity as tone", () => {
    expect(mergeQueueChip({ position: 2, step: "queued" })).toMatchObject({ label: "Queue #2 · queued", tone: "neutral" });
    expect(mergeQueueChip({ position: 1, step: "syncing" })).toMatchObject({ label: "Queue #1 · syncing", tone: "warn" });
    expect(mergeQueueChip({ position: 1, step: "blocked" })).toMatchObject({ label: "Queue #1 · held", tone: "bad" });
  });

  it("passes an unknown step through rather than hiding the chip", () => {
    expect(mergeQueueChip({ position: 1, step: "novel" })?.label).toBe("Queue #1 · novel");
  });
});

describe("overlapChip", () => {
  it("is absent without overlaps", () => {
    expect(overlapChip(undefined)).toBeNull();
    expect(overlapChip([])).toBeNull();
  });

  it("names the other session and lists the shared files in the hint", () => {
    const c = overlapChip([{ session: "p1-fe-2", issue: "FE-2", files: ["app/user.go"] }]);
    expect(c?.label).toBe("Overlaps FE-2");
    expect(c?.tone).toBe("warn");
    expect(c?.hint).toContain("FE-2: app/user.go");
  });

  it("summarizes several sessions and counts what it left out", () => {
    const files = ["a", "b", "c", "d", "e", "f"];
    const c = overlapChip([
      { session: "p1-fe-2", issue: "FE-2", files, more: 4 },
      { session: "p1-fe-3", issue: "", files: ["a"] },
    ]);
    expect(c?.label).toBe("Overlaps 2 sessions");
    expect(c?.hint).toContain("FE-2: a, b, c, d, e +5 more");
    expect(c?.hint).toContain("p1-fe-3: a");
  });
});
