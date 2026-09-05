import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/svelte";
import { KANBAN_COLUMNS } from "$lib/theme";
import FilterRail from "./FilterRail.svelte";
import type { SessionInfo } from "$lib/store.svelte";

// The rail's JUDGEMENTS: which buckets it offers, what each count means, what a
// second tap on the selected chip does, and what a screen reader is told.
//
// THE BUCKETS ARE NOT ASSERTED BY NAME. They come from `KANBAN_COLUMNS` in
// $lib/theme, which is a port of Go's state.KanbanColumns() pinned by
// desktop/state_parity_test.go, so a list spelled out here would be a third
// mirror of something the repository deliberately keeps in exactly two — and it
// would start failing the day a column is renamed on the Go side, which is
// precisely the change that should flow through untouched. What is pinned is
// that the rail offers ALL of them plus "All", in order.
//
// The geometry (the 50pt strip, `py-[7px]`, the lopsided `pl-3 pr-2`) is
// transcribed from Figma and is not a decision this component makes, so it is
// not pinned. The one class assertion below is about the count badge's GROUND,
// which is a decision: the attention bucket's badge is the only loud one.

// A session's bucket is decided by the AXIS PAIR and not by the rolled-up
// status word — `triageOf` in $lib/filters prefers `kanbanTitle(agentState,
// delivery)` and only falls back to the legacy partition for a record with no
// agent state. So the fixtures below set the pair, and the status string is
// carried along only because the row components print it.
//
// Getting this wrong is quiet rather than loud, which is worth a fixture that
// states it: an earlier version of this file gave every session
// `agentState: "working"` and varied only the status, so all six landed in
// Working and the counts it asserted were nonsense the component had computed
// perfectly correctly.
function s(
  id: string,
  pair: { agentState: string; delivery: string },
  status: string,
): SessionInfo {
  return {
    id,
    project: "nori-app",
    issue: id.toUpperCase(),
    title: "A session",
    status,
    interpretedState: "",
    age: "1m",
    prNumber: 0,
    reacting: "",
    devActive: false,
    ...pair,
  } as unknown as SessionInfo;
}

/** One session in each of the five buckets, plus a second that needs a human. */
const SESSIONS = [
  s("a", { agentState: "waiting_input", delivery: "" }, "needs_input"),
  s("b", { agentState: "waiting_input", delivery: "" }, "needs_input"),
  s("c", { agentState: "working", delivery: "" }, "working"),
  s("d", { agentState: "working", delivery: "ci_failed" }, "ci_failed"),
  s("e", { agentState: "idle", delivery: "review_pending" }, "review_pending"),
  s("f", { agentState: "dead", delivery: "merged" }, "merged"),
];

function mount(props: Partial<{ value: string; sessions: SessionInfo[] }> = {}) {
  return render(FilterRail, {
    props: { value: "", sessions: SESSIONS, ...props },
  });
}

/** A chip, by the visible label the design draws on it. */
function chip(name: string): HTMLElement {
  return screen.getByRole("button", { name: new RegExp(`^${name}`) });
}

describe("FilterRail", () => {
  it("offers every shared bucket, in the shared order, behind an All", () => {
    mount();
    const labels = screen
      .getAllByRole("button")
      .map((b) => b.textContent!.replace(/\s+/g, " ").trim());
    expect(labels[0]).toBe("All");
    // Each remaining chip leads with its column's title; the count follows.
    for (const [i, column] of KANBAN_COLUMNS.entries()) {
      expect(labels[i + 1]).toContain(column.title);
    }
    expect(labels).toHaveLength(KANBAN_COLUMNS.length + 1);
  });

  it("counts each bucket from the sessions it is given", () => {
    mount();
    // Two sessions need a human; one sits in each of the other four.
    const needs = KANBAN_COLUMNS.find((c) => c.key === "needs")!;
    expect(chip(needs.title).textContent).toMatch(/2\s*$/);
    for (const column of KANBAN_COLUMNS.filter((c) => c.key !== "needs")) {
      expect(chip(column.title).textContent).toMatch(/1\s*$/);
    }
  });

  it("draws a zero rather than hiding an empty bucket's chip", () => {
    // A 0 says the bucket is empty; a missing chip says the filter is broken.
    // With the list now partitioned by these same buckets it does a second job:
    // it is the only thing on screen saying a SECTION is absent because it
    // holds nothing rather than because something went wrong.
    const { container } = mount({ sessions: [] });
    for (const column of KANBAN_COLUMNS) {
      expect(chip(column.title).textContent).toMatch(/0\s*$/);
    }
    expect(container.querySelectorAll("button")).toHaveLength(KANBAN_COLUMNS.length + 1);
  });

  it("gives only the attention bucket a loud count badge", () => {
    // Every other bucket's badge is the quiet grey. Five orange badges in a row
    // is five alarms, which is the same failure the hero card's rail avoids by
    // being drawn for one status rather than for the whole family.
    mount();
    const needs = KANBAN_COLUMNS.find((c) => c.key === "needs")!;
    const badge = chip(needs.title).querySelector(".num")!;
    expect(badge.className).toContain("bg-pill-urgent");

    for (const column of KANBAN_COLUMNS.filter((c) => c.key !== "needs")) {
      expect(chip(column.title).querySelector(".num")!.className).toContain("bg-pill-grey");
    }
  });

  it("reports its state through aria-pressed, not just a filled ground", () => {
    // The chips are toggles. A screen reader gets no signal at all from the
    // ground, and these are visually identical to the ones still inside
    // <FilterSheet>, so the role has to carry the difference.
    const { container } = mount({ value: KANBAN_COLUMNS[0].title });
    expect(chip(KANBAN_COLUMNS[0].title).getAttribute("aria-pressed")).toBe("true");
    expect(chip("All").getAttribute("aria-pressed")).toBe("false");
    // The selected chip is marked for the scroll-into-view effect, which is the
    // only thing keeping it on screen once the strip is scrolled.
    expect(container.querySelectorAll("[data-rail-selected='true']")).toHaveLength(1);
  });

  it("keeps every chip at the 44pt floor even though the chip itself is 31", () => {
    // The design's chip is below the tap minimum, so the BUTTON owns the rail's
    // full height and the chip sits at the drawn offset inside it. Growing the
    // chip instead would make the filter row taller than the card titles under
    // it.
    mount();
    for (const b of screen.getAllByRole("button")) {
      expect(b.className).toContain("min-w-11");
      expect(b.className).toContain("h-full");
    }
  });
});
