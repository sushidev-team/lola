import { describe, it, expect } from "vitest";
import { TRIAGE_FILTERS, matchesTriage, matchesTriageFor, triageOf, triaged } from "./filters";
import { KANBAN_COLUMNS } from "./theme";
import type { SessionInfo } from "./store.svelte";

// The sidebar's triage rows and the kanban lens must be the SAME partition —
// they are both KANBAN_COLUMNS, in KANBAN_COLUMNS order. A hand-written list
// here would drift from Go's state.KanbanColumns() the first time it changed.
describe("TRIAGE_FILTERS", () => {
  it("is exactly the kanban column titles, in order", () => {
    expect(TRIAGE_FILTERS).toEqual(KANBAN_COLUMNS.map((c) => c.title));
  });

  it("starts with Needs You and ends with Done", () => {
    expect(TRIAGE_FILTERS[0]).toBe("Needs You");
    expect(TRIAGE_FILTERS[TRIAGE_FILTERS.length - 1]).toBe("Done");
  });
});

describe("matchesTriage", () => {
  it('treats "" as everything', () => {
    expect(matchesTriage("working", "")).toBe(true);
    expect(matchesTriage("merged", "")).toBe(true);
  });

  it("matches a status against its own column", () => {
    expect(matchesTriage("needs_input", "Needs You")).toBe(true);
    expect(matchesTriage("ci_failed", "Fixing")).toBe(true);
  });

  it("rejects a status from another column", () => {
    expect(matchesTriage("merged", "Working")).toBe(false);
    expect(matchesTriage("needs_input", "Done")).toBe(false);
  });
});

function s(id: string, status: string): SessionInfo {
  return { id, status } as SessionInfo;
}

describe("triaged", () => {
  const rows = [s("a", "needs_input"), s("b", "working"), s("c", "merged")];

  it("returns the list untouched for the empty filter", () => {
    expect(triaged(rows, "")).toBe(rows);
  });

  it("keeps only the rows in the named column", () => {
    expect(triaged(rows, "Needs You").map((r) => r.id)).toEqual(["a"]);
    expect(triaged(rows, "Done").map((r) => r.id)).toEqual(["c"]);
  });

  it("can filter to nothing", () => {
    expect(triaged(rows, "In Review")).toEqual([]);
  });
});

function axes(id: string, agentState: string, delivery: string): SessionInfo {
  return { id, agentState, delivery, status: "" } as SessionInfo;
}

describe("triageOf", () => {
  it("buckets by BOTH axes when the session carries them", () => {
    expect(triageOf(axes("a", "waiting_input", "review_pending"))).toBe("Needs You");
    expect(triageOf(axes("b", "working", "ci_failed"))).toBe("Fixing");
    expect(triageOf(axes("c", "idle", "approved"))).toBe("In Review");
    expect(triageOf(axes("d", "exited", "ci_failed"))).toBe("Done");
  });

  it("falls back to the collapsed status word when there is no agent axis", () => {
    // Both axes are optional on the wire. Without the fallback every row of a
    // pre-split push buckets as "Working" (kanbanKey's deliberate default) and
    // four of the five columns come up empty.
    expect(triageOf({ status: "needs_input" } as SessionInfo)).toBe("Needs You");
    expect(triageOf({ status: "merged" } as SessionInfo)).toBe("Done");
  });
});

describe("matchesTriageFor", () => {
  it('treats "" as everything', () => {
    expect(matchesTriageFor(axes("a", "dead", "merged"), "")).toBe(true);
  });

  it("is the predicate the list and the sidebar counts share", () => {
    // A row filtered out of the list while its bucket still counts it is a
    // filter that shows an empty screen over a non-zero number, which is what
    // two different predicates would produce.
    const row = axes("a", "waiting_input", "review_pending");
    expect(matchesTriageFor(row, "Needs You")).toBe(true);
    expect(matchesTriageFor(row, "In Review")).toBe(false);
    expect(triaged([row], "Needs You")).toHaveLength(1);
    expect(triaged([row], "In Review")).toHaveLength(0);
  });
});
