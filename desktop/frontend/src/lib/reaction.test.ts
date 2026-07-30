import { describe, it, expect } from "vitest";
import { reactionNote, reactionIsAlarm } from "./reaction";

// The REACTING column was a second, vaguer status column: for four of the six
// postures the daemon derives, the label is a pure relabelling of the status
// sitting next to it. These pin which two actually carry new information.
describe("reactionNote", () => {
  it("drops the postures that only restate the status pill", () => {
    for (const restatement of ["awaiting review", "addressing review", "rebasing", "ready to merge"]) {
      expect(reactionNote(restatement)).toBe("");
    }
  });

  it("keeps the CI retry budget — it is not recoverable from the status", () => {
    expect(reactionNote("ci retry 1/2")).toBe("ci retry 1/2");
    expect(reactionNote("ci retry 0/2")).toBe("ci retry 0/2");
  });

  it("keeps escalation — the status still reads ci_failed either way", () => {
    expect(reactionNote("escalated")).toBe("escalated");
  });

  it("is empty for no posture at all", () => {
    expect(reactionNote("")).toBe("");
  });

  it("marks only escalation as an alarm", () => {
    expect(reactionIsAlarm("escalated")).toBe(true);
    expect(reactionIsAlarm("ci retry 1/2")).toBe(false);
  });
});
