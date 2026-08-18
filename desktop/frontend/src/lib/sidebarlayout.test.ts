import { describe, it, expect } from "vitest";
import {
  buildRows,
  moveProject,
  moveGroup,
  toLayout,
  dropIndex,
  groupRowZone,
  liftedIndex,
  type Row,
} from "./sidebarlayout";

type P = { name: string; group?: string };

const groups = [
  { name: "clients", label: "Clients", position: 1 },
  { name: "internal", position: 3 },
];
const projects: P[] = [
  { name: "lola" },
  { name: "okane", group: "clients" },
  { name: "nori" },
  { name: "tools", group: "internal" },
];

/** The rendered list, as "project" / "group[member, …]" strings. */
function shape(rows: Row<P>[]): string[] {
  return rows.map((r) => (r.kind === "project" ? r.project.name : `${r.group.name}[${r.projects.map((p) => p.name)}]`));
}

describe("buildRows", () => {
  it("draws folders BESIDE the projects, each at its own position", () => {
    expect(shape(buildRows(projects, groups))).toEqual(["lola", "clients[okane]", "nori", "internal[tools]"]);
  });

  it("keeps an EMPTY folder — one exists before anything is dragged in", () => {
    expect(shape(buildRows([{ name: "lola" }], groups))).toEqual(["lola", "clients[]", "internal[]"]);
  });

  it("clamps a position past the end and breaks ties by config order", () => {
    const gs = [
      { name: "a", position: 99 },
      { name: "b", position: 0 },
      { name: "c", position: 0 },
    ];
    expect(shape(buildRows([{ name: "lola" }], gs))).toEqual(["b[]", "c[]", "lola", "a[]"]);
  });

  it("shows a project whose group is not listed at the top level", () => {
    // The window between a folder's removal and the next daemon push: the
    // project must stay visible, never vanish into a folder nothing draws.
    expect(shape(buildRows([{ name: "ghosted", group: "gone" }], groups))).toEqual([
      "ghosted",
      "clients[]",
      "internal[]",
    ]);
  });
});

describe("toLayout", () => {
  it("flattens in RENDER order and records where each folder was drawn", () => {
    const l = toLayout(buildRows(projects, groups));
    expect(l.projects).toEqual([
      { name: "lola", group: "" },
      { name: "okane", group: "clients" },
      { name: "nori", group: "" },
      { name: "tools", group: "internal" },
    ]);
    expect(l.groups).toEqual([
      { name: "clients", label: "Clients", position: 1, collapsed: false },
      { name: "internal", label: "", position: 3, collapsed: false },
    ]);
  });

  it("round-trips: rebuilding from what it writes reproduces the same list", () => {
    const rows = moveProject(buildRows(projects, groups), "lola", { kind: "into", group: "internal" });
    const l = toLayout(rows);
    expect(shape(buildRows(l.projects, l.groups))).toEqual(shape(rows));
  });
});

describe("moveProject", () => {
  const base = buildRows(projects, groups);

  it("files a project INTO a folder it is dropped on, at the end", () => {
    expect(shape(moveProject(base, "lola", { kind: "into", group: "clients" }))).toEqual([
      "clients[okane,lola]",
      "nori",
      "internal[tools]",
    ]);
  });

  it("places a project among a folder's members", () => {
    expect(shape(moveProject(base, "lola", { kind: "group", group: "clients", index: 0 }))).toEqual([
      "clients[lola,okane]",
      "nori",
      "internal[tools]",
    ]);
  });

  it("takes a project back out to the top level", () => {
    const out = moveProject(base, "okane", { kind: "top", index: 0 });
    expect(shape(out)).toEqual(["okane", "lola", "clients[]", "nori", "internal[tools]"]);
    expect((out[0] as { project: P }).project.group).toBe("");
  });

  it("reorders at the top level, counting the index AFTER the lift", () => {
    expect(shape(moveProject(base, "lola", { kind: "top", index: 2 }))).toEqual([
      "clients[okane]",
      "nori",
      "lola",
      "internal[tools]",
    ]);
  });

  it("clamps an out-of-range index instead of dropping the project", () => {
    expect(shape(moveProject(base, "lola", { kind: "top", index: 99 }))).toEqual([
      "clients[okane]",
      "nori",
      "internal[tools]",
      "lola",
    ]);
    expect(shape(moveProject(base, "nori", { kind: "top", index: -5 }))).toEqual([
      "nori",
      "lola",
      "clients[okane]",
      "internal[tools]",
    ]);
  });

  it("returns the input untouched for an unknown project or folder", () => {
    // A drag against a snapshot that has since reloaded costs the drag, never
    // the arrangement.
    expect(moveProject(base, "ghost", { kind: "top", index: 0 })).toBe(base);
    expect(moveProject(base, "lola", { kind: "into", group: "gone" })).toBe(base);
    expect(moveProject(base, "lola", { kind: "group", group: "gone", index: 0 })).toBe(base);
  });
});

describe("moveGroup", () => {
  const base = buildRows(projects, groups);

  it("moves a folder among the top-level rows, members and all", () => {
    expect(shape(moveGroup(base, "internal", 0))).toEqual(["internal[tools]", "lola", "clients[okane]", "nori"]);
  });

  it("clamps rather than dropping the folder", () => {
    expect(shape(moveGroup(base, "clients", 99))).toEqual(["lola", "nori", "internal[tools]", "clients[okane]"]);
  });

  it("returns the input untouched for an unknown folder", () => {
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

  it("is 0 for an empty list", () => {
    expect(dropIndex([], 42)).toBe(0);
  });
});

describe("liftedIndex", () => {
  it("shifts a DOWNWARD move back by one", () => {
    // The indicator is drawn against the list still holding the dragged row;
    // lifting it out shifts everything below up by one, so without this a
    // downward drag lands one place past where it was aimed.
    expect(liftedIndex(2, 0)).toBe(1);
    expect(liftedIndex(3, 1)).toBe(2);
  });

  it("leaves an upward move, and a move from another list, alone", () => {
    expect(liftedIndex(0, 2)).toBe(0);
    expect(liftedIndex(1, 1)).toBe(1); // the gap just above itself
    expect(liftedIndex(2, null)).toBe(2); // dragged in from a folder
  });

  it("lands a drag where the indicator was drawn", () => {
    // The end-to-end statement of the two above: A dragged into the gap between
    // B and C (live gap 2) must end up between them.
    const rows = buildRows([{ name: "a" }, { name: "b" }, { name: "c" }], []);
    const out = moveProject(rows, "a", { kind: "top", index: liftedIndex(2, 0) });
    expect(shape(out)).toEqual(["b", "a", "c"]);
  });
});

describe("groupRowZone", () => {
  const rect = { top: 100, height: 28 };

  it("reads the middle of a folder row as 'file it in here'", () => {
    expect(groupRowZone(rect, 114)).toBe("into");
  });

  it("keeps the edges as the gaps either side", () => {
    // Without them a folder would swallow every drop meant for its neighbours,
    // and nothing could be placed BETWEEN two folders.
    expect(groupRowZone(rect, 101)).toBe("before");
    expect(groupRowZone(rect, 127)).toBe("after");
  });
});
