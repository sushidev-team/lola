import { describe, it, expect } from "vitest";
import {
  pillKind,
  pillClasses,
  statusLabel,
  statusBadge,
  sortRank,
  isAttention,
  kanbanColumn,
  eventPhrase,
  reactingText,
  attentionCount,
} from "./theme";

describe("pillKind", () => {
  it("maps urgent/broken/work/done/grey buckets", () => {
    expect(pillKind("needs_input")).toBe("urgent");
    expect(pillKind("ci_failed")).toBe("broken");
    expect(pillKind("changes_requested")).toBe("broken");
    expect(pillKind("merge_conflict")).toBe("broken");
    expect(pillKind("working")).toBe("work");
    expect(pillKind("ci_pending")).toBe("work");
    expect(pillKind("draft")).toBe("work");
    expect(pillKind("approved")).toBe("done");
    expect(pillKind("pr_open")).toBe("done");
    expect(pillKind("review_pending")).toBe("grey");
  });
  it("falls back to plain for terminal/idle/unknown", () => {
    for (const s of ["merged", "dead", "session_ended", "idle", "wat"])
      expect(pillKind(s)).toBe("plain");
  });
});

describe("pillClasses", () => {
  it("gives dead a solid red fill even though it is plain", () => {
    expect(pillClasses("dead")).toContain("bg-bad");
  });
  it("uses the urgent fill for needs_input", () => {
    expect(pillClasses("needs_input")).toContain("bg-pill-urgent");
  });
});

describe("statusLabel", () => {
  it("shortens the noisy labels", () => {
    expect(statusLabel("changes_requested")).toBe("changes");
    expect(statusLabel("review_pending")).toBe("review");
    expect(statusLabel("merge_conflict")).toBe("conflict");
    expect(statusLabel("session_ended")).toBe("ended");
    expect(statusLabel("ci_pending")).toBe("pending");
    expect(statusLabel("needs_input")).toBe("needs you");
  });
  it("passes unknown through unchanged", () => {
    expect(statusLabel("working")).toBe("working");
  });
});

describe("statusBadge", () => {
  it("is two chars for known statuses and ?? otherwise", () => {
    expect(statusBadge("needs_input")).toBe("!!");
    expect(statusBadge("approved")).toBe("ok");
    expect(statusBadge("nope")).toBe("??");
  });
});

describe("sortRank", () => {
  it("orders attention first, terminal last", () => {
    expect(sortRank("needs_input")).toBeLessThan(sortRank("ci_failed"));
    expect(sortRank("ci_failed")).toBeLessThan(sortRank("working"));
    expect(sortRank("working")).toBeLessThan(sortRank("review_pending"));
    expect(sortRank("review_pending")).toBeLessThan(sortRank("merged"));
    expect(sortRank("mystery")).toBe(4);
  });
});

describe("isAttention / attentionCount", () => {
  it("counts only the needs-human statuses", () => {
    expect(isAttention("needs_input")).toBe(true);
    expect(isAttention("working")).toBe(false);
    const sessions = [
      { status: "needs_input" },
      { status: "ci_failed" },
      { status: "working" },
      { status: "approved" },
    ];
    expect(attentionCount(sessions)).toBe(2);
  });
});

describe("kanbanColumn", () => {
  it("buckets statuses and defaults unknown to Working", () => {
    expect(kanbanColumn("needs_input")).toBe("Needs You");
    expect(kanbanColumn("approved")).toBe("In Review");
    expect(kanbanColumn("merged")).toBe("Done");
    expect(kanbanColumn("banana")).toBe("Working");
  });
});

describe("eventPhrase", () => {
  it("says spawned for a fresh session", () => {
    expect(eventPhrase("", "working")).toBe("spawned");
  });
  it("maps transitions to human phrases", () => {
    expect(eventPhrase("working", "needs_input")).toBe("needs you");
    expect(eventPhrase("working", "merged")).toBe("merged");
    expect(eventPhrase("working", "whatever")).toBe("whatever");
  });
});

describe("reactingText", () => {
  it("colors escalation red and ready green", () => {
    expect(reactingText("escalated")).toBe("text-bad");
    expect(reactingText("ready to merge")).toBe("text-good");
    expect(reactingText("ci retry 1/2")).toBe("text-warn");
    expect(reactingText("")).toBe("text-ink");
  });
});
