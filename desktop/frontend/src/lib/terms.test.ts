import { describe, it, expect, vi, beforeEach } from "vitest";

// TermService is only reached by the async paths (refresh / openShell / close);
// these tests drive the pure view logic, so the binding is stubbed away.
vi.mock("@bindings/desktop", () => ({
  TermService: { Shells: vi.fn(async () => []), Shell: vi.fn(async () => ""), CloseShell: vi.fn(async () => {}) },
}));

const { terms, AGENT, isReviewTab } = await import("./terms.svelte");

// The store is a singleton; give each test its own session id instead of
// resetting it, so ordering can never matter.
let n = 0;
const newId = () => `s${++n}`;

// seed installs a discovered tab list the way refresh() would.
function seed(id: string, names: string[]) {
  (terms as unknown as { shells: Map<string, string[]> }).shells.set(id, names);
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("review pane tabs", () => {
  it("recognises a review pane by its suffix", () => {
    expect(isReviewTab("lola-nori-app-nor-357-review")).toBe(true);
    expect(isReviewTab("lola-nori-app-nor-357-shell-1")).toBe(false);
    expect(isReviewTab("lola-nori-app-nor-357")).toBe(false);
  });

  it("labels the review pane 'Review' and leaves shell numbering alone", () => {
    const id = newId();
    seed(id, [`${id}-shell-1`, `${id}-shell-2`, `${id}-review`]);
    expect(terms.labelFor(id, `${id}-shell-1`)).toBe("sh 1");
    expect(terms.labelFor(id, `${id}-shell-2`)).toBe("sh 2");
    expect(terms.labelFor(id, `${id}-review`)).toBe("Review");
  });

  it("numbers a new shell past the existing shells, ignoring the review pane", async () => {
    const id = newId();
    seed(id, [`${id}-shell-1`, `${id}-review`]);
    const { TermService } = await import("@bindings/desktop");
    await terms.openShell(id, "/tmp/wt");
    expect(TermService.Shell).toHaveBeenCalledWith(`${id}-shell-2`, "/tmp/wt");
  });

  it("cycles through the review pane like any other tab", () => {
    const id = newId();
    seed(id, [`${id}-shell-1`, `${id}-review`]);
    terms.select(id, AGENT);
    terms.cycleTab(id, 1);
    expect(terms.activeTab(id)).toBe(`${id}-shell-1`);
    terms.cycleTab(id, 1);
    expect(terms.activeTab(id)).toBe(`${id}-review`);
    terms.cycleTab(id, 1);
    expect(terms.activeTab(id)).toBe(AGENT);
  });

  it("falls back off a review tab that has gone away", async () => {
    const id = newId();
    seed(id, [`${id}-review`]);
    terms.select(id, `${id}-review`);
    expect(terms.activeTab(id)).toBe(`${id}-review`);
    seed(id, []);
    expect(terms.activeTab(id)).toBe(AGENT);
  });
});

describe("drag-to-sort tabs", () => {
  it("reorders the tabs and renumbers the labels by position", () => {
    const id = newId();
    seed(id, [`${id}-shell-1`, `${id}-shell-2`, `${id}-shell-3`]);
    terms.moveTab(id, 2, 0);
    expect(terms.shellsFor(id)).toEqual([`${id}-shell-3`, `${id}-shell-1`, `${id}-shell-2`]);
    expect(terms.labelFor(id, `${id}-shell-3`)).toBe("sh 1");
    expect(terms.labelFor(id, `${id}-shell-1`)).toBe("sh 2");
  });

  it("ignores a no-op or out-of-range move", () => {
    const id = newId();
    const names = [`${id}-shell-1`, `${id}-shell-2`];
    seed(id, names);
    terms.moveTab(id, 0, 0);
    terms.moveTab(id, 1, 5);
    terms.moveTab(id, -1, 0);
    expect(terms.shellsFor(id)).toEqual(names);
  });

  it("survives a refresh, and appends a shell discovered after the sort", () => {
    const id = newId();
    seed(id, [`${id}-shell-1`, `${id}-shell-2`]);
    terms.moveTab(id, 1, 0);
    seed(id, [`${id}-shell-1`, `${id}-shell-2`, `${id}-shell-3`]); // discovery order, as refresh writes it
    expect(terms.shellsFor(id)).toEqual([`${id}-shell-2`, `${id}-shell-1`, `${id}-shell-3`]);
  });

  it("drops a closed tab from the sorted order", () => {
    const id = newId();
    seed(id, [`${id}-shell-1`, `${id}-shell-2`, `${id}-review`]);
    terms.moveTab(id, 2, 0);
    seed(id, [`${id}-shell-1`, `${id}-shell-2`]);
    expect(terms.shellsFor(id)).toEqual([`${id}-shell-1`, `${id}-shell-2`]);
  });

  it("cycles in the sorted order", () => {
    const id = newId();
    seed(id, [`${id}-shell-1`, `${id}-shell-2`]);
    terms.moveTab(id, 1, 0);
    terms.select(id, AGENT);
    terms.cycleTab(id, 1);
    expect(terms.activeTab(id)).toBe(`${id}-shell-2`);
  });
});
