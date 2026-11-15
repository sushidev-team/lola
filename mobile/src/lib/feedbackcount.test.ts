import { describe, expect, it } from "vitest";
import { feedback } from "$lib/feedback.svelte";
import { listedFeedbackCount } from "./feedbackcount";

const header = (n: number) => `Review package p${n}\nRepository: r\nBase: a\nHead: b\nSection: S${n}\n`;

describe("listedFeedbackCount", () => {
  it("counts each review-package comment, drafts and free text as items", () => {
    const id = "fc1";
    feedback.setNote(id, ["my note", header(1) + "one", header(2) + "two", header(3) + "three"].join("\n\n"));
    expect(listedFeedbackCount(id)).toBe(4);
    expect(feedback.count(id)).toBe(1);
  });
  it("is zero for an empty note", () => {
    expect(listedFeedbackCount("fc2")).toBe(0);
  });
  it("counts comments without free text", () => {
    feedback.setNote("fc3", header(1) + "only");
    expect(listedFeedbackCount("fc3")).toBe(1);
  });
});
