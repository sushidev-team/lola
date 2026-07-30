import { describe, it, expect } from "vitest";
import { TRIAGE_FILTERS, matchesTriage, triaged } from "./filters";
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
