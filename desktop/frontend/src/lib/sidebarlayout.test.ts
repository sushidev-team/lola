import { describe, it, expect } from "vitest";
import { buildSections, moveProject, moveGroup, toLayout, dropIndex, type Section } from "./sidebarlayout";

type P = { name: string; group?: string };

const groups = [
  { name: "clients", label: "Clients" },
  { name: "internal" },
];
const projects: P[] = [
  { name: "lola" },
  { name: "okane", group: "clients" },
  { name: "nori" },
  { name: "tools", group: "internal" },
];

function names(s: Section<P>[]): string[][] {
  return s.map((x) => x.projects.map((p) => p.name));
}

describe("buildSections", () => {
  it("puts ungrouped projects first, then every group in config order", () => {
    const s = buildSections(projects, groups);
    expect(s.map((x) => x.group?.name ?? "")).toEqual(["", "clients", "internal"]);
    expect(names(s)).toEqual([["lola", "nori"], ["okane"], ["tools"]]);
  });

  it("keeps an EMPTY group — a folder exists before anything is dragged in", () => {
    const s = buildSections([{ name: "lola" }], groups);
    expect(s.map((x) => x.group?.name ?? "")).toEqual(["", "clients", "internal"]);
    expect(names(s)).toEqual([["lola"], [], []]);
  });

  it("keeps the top-level section even when every project is grouped", () => {
    // It is the drop target for dragging a project OUT of a folder, so it can
    // never be absent.
    const s = buildSections([{ name: "okane", group: "clients" }], groups);
    expect(s[0].group).toBeNull();
    expect(s[0].projects).toEqual([]);
  });

  it("shows a project whose group is not listed at the top level", () => {
    // The window between a group's removal and the next daemon push: the
    // project must stay visible, never vanish into a folder nothing draws.
    const s = buildSections([{ name: "ghosted", group: "gone" }], groups);
    expect(names(s)).toEqual([["ghosted"], [], []]);
  });
});

describe("toLayout", () => {
  it("flattens projects in RENDER order and carries each one's group", () => {
    const l = toLayout(buildSections(projects, groups));
    expect(l.projects).toEqual([
      { name: "lola", group: "" },
      { name: "nori", group: "" },
      { name: "okane", group: "clients" },
      { name: "tools", group: "internal" },
    ]);
    expect(l.groups).toEqual([
      { name: "clients", label: "Clients", collapsed: false },
      { name: "internal", label: "", collapsed: false },
    ]);
  });
});

describe("moveProject", () => {
  const base = buildSections(projects, groups);

  it("reorders within a section", () => {
    expect(names(moveProject(base, "nori", "", 0))).toEqual([["nori", "lola"], ["okane"], ["tools"]]);
  });

  it("moves a project into a group at the given slot", () => {
    expect(names(moveProject(base, "lola", "clients", 0))).toEqual([["nori"], ["lola", "okane"], ["tools"]]);
    expect(names(moveProject(base, "lola", "clients", 1))).toEqual([["nori"], ["okane", "lola"], ["tools"]]);
  });

  it("moves a project back out to the top level", () => {
    const out = moveProject(base, "okane", "", 1);
    expect(names(out)).toEqual([["lola", "okane", "nori"], [], ["tools"]]);
    expect(out[0].projects.find((p) => p.name === "okane")?.group).toBe("");
  });

  it("counts the index AFTER the project is lifted out", () => {
    // Dragging the first row to the last gap yields last, not second-to-last.
    expect(names(moveProject(base, "lola", "", 2))).toEqual([["nori", "lola"], ["okane"], ["tools"]]);
  });

  it("clamps an out-of-range index instead of dropping the project", () => {
    expect(names(moveProject(base, "lola", "", 99))).toEqual([["nori", "lola"], ["okane"], ["tools"]]);
    expect(names(moveProject(base, "lola", "", -5))).toEqual([["lola", "nori"], ["okane"], ["tools"]]);
  });

  it("returns the input untouched for an unknown project or target", () => {
    // A drag against a snapshot that has since reloaded costs the drag, never
    // the arrangement.
    expect(moveProject(base, "ghost", "", 0)).toBe(base);
    expect(moveProject(base, "lola", "gone", 0)).toBe(base);
  });
});

describe("moveGroup", () => {
  const base = buildSections(projects, groups);

  it("reorders groups, leaving the top-level section first", () => {
    const out = moveGroup(base, "internal", 0);
    expect(out.map((s) => s.group?.name ?? "")).toEqual(["", "internal", "clients"]);
  });

  it("cannot move a group above the loose projects", () => {
    const out = moveGroup(base, "internal", -3);
    expect(out[0].group).toBeNull();
  });

  it("returns the input untouched for an unknown group", () => {
    expect(moveGroup(base, "gone", 0)).toBe(base);
  });
});

describe("dropIndex", () => {
  const rows = [
    { top: 0, height: 20 },
    { top: 20, height: 20 },
    { top: 40, height: 20 },
  ];

  it("splits each row at its midpoint", () => {
    expect(dropIndex(rows, 5)).toBe(0);
    expect(dropIndex(rows, 15)).toBe(1); // past the first row's middle
    expect(dropIndex(rows, 25)).toBe(1);
    expect(dropIndex(rows, 35)).toBe(2);
  });

  it("lands past the end below the last row", () => {
    expect(dropIndex(rows, 500)).toBe(3);
  });

  it("is 0 for an empty section", () => {
    expect(dropIndex([], 42)).toBe(0);
  });
});
