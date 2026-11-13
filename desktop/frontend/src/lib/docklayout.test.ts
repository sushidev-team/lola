import { describe, it, expect } from "vitest";
import {
  MAIN_ID,
  MAX_DEPTH,
  MAX_LIVE_FRAMES,
  MIN_GROUP,
  MIN_MAIN,
  SASH,
  activeTabs,
  hitTest,
  layout,
  groupOf,
  emptyLayout,
  deserialize,
  resizeSplit,
  normalize,
  evenSplit,
  clampToDock,
  DROP_OVERSHOOT,
  addTab,
  allTabs,
  closeTab,
  depth,
  fits,
  groups,
  liveFrames,
  minSize,
  moveTab,
  newPanelTarget,
  nextPanelTarget,
  panelSlot,
  retainTabs,
  selectTab,
  serialize,
  shiftTab,
  tabIndexAt,
  type DockNode,
  type DropTarget,
  type Edge,
  type Split,
  type TabGroup,
} from "./docklayout";

const B = { w: 1600, h: 900 };
const group = (id: string, ...tabs: string[]): TabGroup => ({ kind: "group", id, tabs, active: tabs[0] ?? "" });
const main: DockNode = { kind: "main" };
const split = (id: string, dir: "row" | "col", children: DockNode[], sizes?: number[]): Split => ({ kind: "split", id, dir, sizes: sizes ?? children.map(() => 1 / children.length), children });
const open = (...tabs: string[]) => tabs.reduce((n, t) => addTab(n, t), emptyLayout());
const shape = (n: DockNode): unknown => (n.kind === "main" ? "M" : n.kind === "group" ? n.tabs.join("+") : { [n.dir]: n.children.map(shape) });

/** Everything that must hold of any tree the module hands out. */
function checkInvariants(root: DockNode) {
  const seen = new Set<string>();
  let mains = 0;
  const walk = (n: DockNode) => {
    if (n.kind === "main") {
      mains++;
      return;
    }
    expect(seen.has(n.id), `duplicate id ${n.id}`).toBe(false);
    seen.add(n.id);
    if (n.kind === "group") {
      expect(n.tabs.length).toBeGreaterThan(0);
      expect(n.tabs).toContain(n.active);
      return;
    }
    expect(n.children.length).toBeGreaterThanOrEqual(2);
    expect(n.sizes.length).toBe(n.children.length);
    expect(n.sizes.every((s) => s > 0)).toBe(true);
    expect(Math.abs(n.sizes.reduce((a, b) => a + b, 0) - 1)).toBeLessThan(1e-6);
    for (const c of n.children) {
      if (c.kind === "split") expect(c.dir).not.toBe(n.dir);
      walk(c);
    }
  };
  walk(root);
  expect(mains).toBe(1);
  expect(depth(root)).toBeLessThanOrEqual(MAX_DEPTH);
  const tabs = allTabs(root);
  expect(new Set(tabs).size).toBe(tabs.length);
}

/** The panels tile the bounds exactly: no gap, no overlap, whatever the proportions. */
function checkTiling(root: DockNode, b = B) {
  const { nodes, sashes } = layout(root, b);
  const leaves = [MAIN_ID, ...groups(root).map((g) => g.id)].map((id) => nodes[id]);
  const area = leaves.reduce((a, r) => a + r.w * r.h, 0) + sashes.reduce((a, s) => a + s.rect.w * s.rect.h, 0);
  expect(area).toBe(b.w * b.h);
  for (let i = 0; i < leaves.length; i++) {
    for (let j = i + 1; j < leaves.length; j++) {
      const p = leaves[i];
      const q = leaves[j];
      expect(p.x + p.w <= q.x || q.x + q.w <= p.x || p.y + p.h <= q.y || q.y + q.h <= p.y).toBe(true);
    }
  }
}

describe("normalize", () => {
  it("drops an empty group and collapses the split it leaves", () => {
    const n = normalize(split("s1", "row", [main, group("g1")]));
    expect(n).toEqual({ kind: "main" });
  });
  it("flattens a row in a row, scaling its fractions", () => {
    const n = normalize(split("s1", "row", [split("s2", "row", [main, group("g1", "a")], [0.5, 0.5]), group("g2", "b")], [0.5, 0.5])) as Split;
    expect(shape(n)).toEqual({ row: ["M", "a", "b"] });
    expect(n.sizes).toEqual([0.25, 0.25, 0.5]);
  });
  it("gives back an emptied child's space to its siblings", () => {
    const n = normalize(split("s1", "row", [main, group("g1"), group("g2", "b")], [0.5, 0.25, 0.25])) as Split;
    expect(shape(n)).toEqual({ row: ["M", "b"] });
    expect(n.sizes[0]).toBeCloseTo(2 / 3);
    expect(n.sizes[1]).toBeCloseTo(1 / 3);
  });
  it("collapses through several levels at once", () => {
    const n = normalize(split("s1", "row", [split("s2", "col", [split("s3", "row", [group("g1", "a")])]), main]));
    expect(shape(n)).toEqual({ row: ["a", "M"] });
  });
  it("repairs what a damaged file could say", () => {
    const n = normalize(split("x", "row", [group("x", "a", "a"), group("x", "a", "b"), main, main], [NaN, -1, 0, 3]));
    checkInvariants(n);
    expect(allTabs(n)).toEqual(["a", "b"]);
    expect(groupOf(n, "a")!.active).toBe("a");
  });
  it("puts the timeline back in a tree without one", () => {
    const n = normalize(group("g1", "a"));
    checkInvariants(n);
    expect(shape(n)).toEqual({ row: ["M", "a"] });
  });
  it("fixes an active tab that is not there", () => {
    const g: TabGroup = { kind: "group", id: "g1", tabs: ["a", "b"], active: "zzz" };
    expect(groupOf(normalize(split("s", "row", [main, g])), "a")!.active).toBe("a");
  });
  it("is idempotent, down to the last bit of every fraction", () => {
    const n = normalize(split("s1", "row", [main, split("s2", "col", [group("g1", "a"), group("g2", "b"), group("g3")], [0.1, 0.7, 0.13]), group("g4", "c")], [0.3, 0.3, 0.7]));
    expect(normalize(n)).toEqual(n);
  });
});

describe("tabs", () => {
  it("opens the first one in a new panel right of the timeline, narrower than it", () => {
    const n = open("a") as Split;
    expect(shape(n)).toEqual({ row: ["M", "a"] });
    expect(n.sizes[0]).toBeGreaterThan(n.sizes[1]);
  });
  it("adds the next ones as tabs, each becoming active", () => {
    const n = open("a", "b");
    expect(shape(n)).toEqual({ row: ["M", "a+b"] });
    expect(activeTabs(n)).toEqual(["b"]);
  });
  it("ignores a tab it already has", () => {
    const n = open("a", "b");
    expect(addTab(n, "a")).toBe(n);
  });
  it("selects", () => {
    const n = selectTab(open("a", "b"), "a");
    expect(activeTabs(n)).toEqual(["a"]);
  });
  it("closes, handing the panel to the neighbor", () => {
    let n = open("a", "b", "c");
    n = selectTab(n, "b");
    n = closeTab(n, "b");
    expect(activeTabs(n)).toEqual(["c"]);
    n = closeTab(n, "c");
    expect(activeTabs(n)).toEqual(["a"]);
  });
  it("gives the whole width back when the last tab closes", () => {
    expect(closeTab(open("a"), "a")).toEqual({ kind: "main" });
  });
  it("drops stale tabs and keeps the rest of the arrangement", () => {
    let n = open("a", "b");
    n = moveTab(n, "b", { kind: "edge", node: groupOf(n, "a")!.id, edge: "bottom" });
    expect(shape(n)).toEqual({ row: ["M", { col: ["a", "b"] }] });
    const pruned = retainTabs(n, new Set(["b"]));
    expect(shape(pruned)).toEqual({ row: ["M", "b"] });
    expect(retainTabs(pruned, new Set(["b"]))).toBe(pruned);
  });
});

describe("moving", () => {
  const twoPanels = () => {
    let n = open("a", "b", "c");
    n = moveTab(n, "c", { kind: "outer", edge: "right" });
    return n;
  };
  const edges: Edge[] = ["left", "right", "top", "bottom"];

  it("reorders within a strip", () => {
    const n = open("a", "b", "c");
    const g = groupOf(n, "a")!.id;
    expect(shape(moveTab(n, "c", { kind: "tabs", group: g, index: 0 }))).toEqual({ row: ["M", "c+a+b"] });
    expect(shape(moveTab(n, "a", { kind: "tabs", group: g, index: 2 }))).toEqual({ row: ["M", "b+c+a"] });
    expect(shape(shiftTab(n, "a", 1))).toEqual({ row: ["M", "b+a+c"] });
    expect(shiftTab(n, "a", -1)).toBe(n);
  });
  it("moves to another panel's strip, at a position, and empties the old one", () => {
    const n = twoPanels();
    const right = groupOf(n, "c")!.id;
    const m = moveTab(n, "b", { kind: "tabs", group: right, index: 0 });
    expect(shape(m)).toEqual({ row: ["M", "a", "b+c"] });
    expect(activeTabs(m)).toContain("b");
    const empty = moveTab(m, "a", { kind: "tabs", group: right });
    expect(shape(empty)).toEqual({ row: ["M", "b+c+a"] });
  });
  it("splits a panel on each of its four edges", () => {
    const n = open("a", "b");
    const g = groupOf(n, "a")!.id;
    expect(shape(moveTab(n, "b", { kind: "edge", node: g, edge: "left" }))).toEqual({ row: ["M", "b", "a"] });
    expect(shape(moveTab(n, "b", { kind: "edge", node: g, edge: "right" }))).toEqual({ row: ["M", "a", "b"] });
    expect(shape(moveTab(n, "b", { kind: "edge", node: g, edge: "top" }))).toEqual({ row: ["M", { col: ["b", "a"] }] });
    expect(shape(moveTab(n, "b", { kind: "edge", node: g, edge: "bottom" }))).toEqual({ row: ["M", { col: ["a", "b"] }] });
  });
  it("goes beside or below the timeline", () => {
    const n = open("a", "b");
    expect(shape(moveTab(n, "b", { kind: "edge", node: MAIN_ID, edge: "bottom" }))).toEqual({ row: [{ col: ["M", "b"] }, "a"] });
    expect(shape(moveTab(n, "b", { kind: "edge", node: MAIN_ID, edge: "left" }))).toEqual({ row: ["b", "M", "a"] });
  });
  it("goes to the outer edges of the whole workspace", () => {
    const n = open("a", "b");
    expect(shape(moveTab(n, "b", { kind: "outer", edge: "bottom" }))).toEqual({ col: [{ row: ["M", "a"] }, "b"] });
    expect(shape(moveTab(n, "b", { kind: "outer", edge: "top" }))).toEqual({ col: ["b", { row: ["M", "a"] }] });
    expect(shape(moveTab(n, "b", { kind: "outer", edge: "left" }))).toEqual({ row: ["b", "M", "a"] });
  });
  it("treats a panel's only tab dropped on its own edge as nothing", () => {
    const n = open("a");
    for (const edge of edges) expect(moveTab(n, "a", { kind: "edge", node: groupOf(n, "a")!.id, edge })).toBe(n);
    expect(moveTab(n, "a", { kind: "tabs", group: groupOf(n, "a")!.id })).toBe(n);
  });
  it("treats a move that changes nothing as nothing", () => {
    const n = open("a");
    expect(moveTab(n, "a", { kind: "outer", edge: "right" })).toBe(n);
    expect(moveTab(n, "a", { kind: "edge", node: MAIN_ID, edge: "right" })).toBe(n);
  });
  it("ignores an unknown tab or target", () => {
    const n = open("a", "b");
    expect(moveTab(n, "zzz", { kind: "outer", edge: "left" })).toBe(n);
    expect(moveTab(n, "a", { kind: "tabs", group: "nope" })).toBe(n);
    expect(moveTab(n, "a", { kind: "edge", node: "nope", edge: "left" })).toBe(n);
  });
  it("refuses to nest past the depth limit", () => {
    let n = open("a", "b", "c", "d", "e", "f", "g");
    const dirs: Edge[] = ["bottom", "right", "bottom", "right", "bottom", "right"];
    let last = "a";
    ["b", "c", "d", "e", "f", "g"].forEach((t, i) => {
      n = moveTab(n, t, { kind: "edge", node: groupOf(n, last)!.id, edge: dirs[i] });
      last = t;
    });
    checkInvariants(n);
    expect(depth(n)).toBe(MAX_DEPTH);
    expect(allTabs(n).length).toBe(7);
  });
  it("offers a keyboard route to a new panel and the next one", () => {
    const n = open("a", "b");
    const t = newPanelTarget(n, "b", "right", B)!;
    expect(shape(moveTab(n, "b", t))).toEqual({ row: ["M", "a", "b"] });
    const m = moveTab(n, "b", t);
    // A lone tab cannot split its own panel, so it goes to the edge. Already there is nothing.
    expect(newPanelTarget(m, "b", "right", B)).toBeNull();
    expect(newPanelTarget(m, "b", "bottom", B)).toEqual({ kind: "outer", edge: "bottom" });
    expect(shape(moveTab(m, "b", nextPanelTarget(m, "b")!))).toEqual({ row: ["M", "a+b"] });
    expect(nextPanelTarget(n, "b")).toBeNull();
  });
  it("offers no new panel when it would not fit", () => {
    const tight = { w: MIN_MAIN.w + MIN_GROUP.w + SASH + 10, h: 900 };
    expect(newPanelTarget(open("a", "b"), "b", "right", tight)).toBeNull();
    expect(newPanelTarget(open("a", "b"), "b", "bottom", tight)).not.toBeNull();
  });
});

describe("geometry", () => {
  it("fills the bounds with the timeline when the dock is empty", () => {
    expect(layout(emptyLayout(), B).nodes[MAIN_ID]).toEqual({ x: 0, y: 0, w: B.w, h: B.h });
    expect(layout(emptyLayout(), B).sashes).toEqual([]);
  });
  it("tiles exactly, on awkward sizes too", () => {
    let n = open("a", "b", "c");
    n = moveTab(n, "b", { kind: "outer", edge: "bottom" });
    n = moveTab(n, "c", { kind: "edge", node: groupOf(n, "b")!.id, edge: "right" });
    for (const b of [B, { w: 1234, h: 777 }, { w: 1001, h: 603 }]) checkTiling(n, b);
  });
  it("honours minimums before proportions", () => {
    const n = normalize(split("s", "row", [main, group("g1", "a")], [0.01, 0.99]));
    const { nodes } = layout(n, B);
    expect(nodes[MAIN_ID].w).toBe(MIN_MAIN.w);
    const m = normalize(split("s", "row", [main, group("g1", "a")], [0.99, 0.01]));
    expect(layout(m, B).nodes.g1.w).toBe(MIN_GROUP.w);
  });
  it("adds minimums up the tree", () => {
    const n = normalize(split("s", "row", [main, split("c", "col", [group("g1", "a"), group("g2", "b")])]));
    expect(minSize(n)).toEqual({ w: MIN_MAIN.w + MIN_GROUP.w + SASH, h: Math.max(MIN_MAIN.h, 2 * MIN_GROUP.h + SASH) });
    expect(fits(n, { w: MIN_MAIN.w + MIN_GROUP.w, h: 900 })).toBe(false);
    expect(fits(n, { w: MIN_MAIN.w + MIN_GROUP.w + SASH, h: 900 })).toBe(true);
  });
  it("keeps the arithmetic sane when it cannot fit", () => {
    const n = open("a");
    const { nodes } = layout(n, { w: 300, h: 100 });
    expect(nodes[MAIN_ID].w + nodes[(n as Split).children[1] && groupOf(n, "a")!.id].w + SASH).toBe(300);
    expect(layout(n, { w: 0, h: 0 }).nodes[MAIN_ID].w).toBeGreaterThanOrEqual(0);
  });
  it("fills a panel edge to edge below its tab strip", () => {
    const s = panelSlot({ x: 100, y: 50, w: 400, h: 300 });
    expect(s).toEqual({ x: 100, y: 82, w: 400, h: 268 });
    expect(panelSlot({ x: 0, y: 0, w: 2, h: 2 }).h).toBe(0);
  });
});

describe("resizing", () => {
  it("moves only the two children either side of the line", () => {
    const n = moveTab(open("a", "b"), "b", { kind: "outer", edge: "right" });
    const s = n as Split;
    const before = layout(n, B).nodes;
    const m = resizeSplit(n, s.id, 0, 100, B);
    const after = layout(m, B).nodes;
    expect(after[MAIN_ID].w).toBe(before[MAIN_ID].w + 100);
    expect(after[groupOf(n, "a")!.id].w).toBe(before[groupOf(n, "a")!.id].w - 100);
    expect(after[groupOf(n, "b")!.id].w).toBe(before[groupOf(n, "b")!.id].w);
  });
  it("stops at each side's minimum", () => {
    const n = open("a");
    const s = n as Split;
    const g = groupOf(n, "a")!.id;
    expect(layout(resizeSplit(n, s.id, 0, 99999, B), B).nodes[g].w).toBe(MIN_GROUP.w);
    expect(layout(resizeSplit(n, s.id, 0, -99999, B), B).nodes[MAIN_ID].w).toBe(MIN_MAIN.w);
  });
  it("counts a nested split's minimum, not just a leaf's", () => {
    let n = open("a", "b");
    n = moveTab(n, "b", { kind: "edge", node: groupOf(n, "a")!.id, edge: "right" });
    n = moveTab(n, "b", { kind: "edge", node: MAIN_ID, edge: "bottom" });
    const col = (n as Split).children[0] as Split;
    const r = resizeSplit(n, col.id, 0, 99999, B);
    expect(layout(r, B).nodes[groupOf(n, "b")!.id].h).toBe(MIN_GROUP.h);
  });
  it("ignores a split that is not there and a drag of nothing", () => {
    const n = open("a");
    expect(resizeSplit(n, "nope", 0, 10, B)).toBe(n);
    expect(resizeSplit(n, (n as Split).id, 5, 10, B)).toBe(n);
    expect(resizeSplit(n, (n as Split).id, 0, 0, B)).toBe(n);
  });
});

describe("hit testing", () => {
  const setup = () => open("a", "b"); // [main | a+b]
  const rectOf = (n: DockNode, id: string) => layout(n, B).nodes[id];
  const hit = (n: DockNode, x: number, y: number, tab = "b") => hitTest(n, B, tab, x, y);

  it("returns nothing outside the bounds", () => {
    expect(hit(setup(), -1, 100)).toBeNull();
    expect(hit(setup(), B.w, 100)).toBeNull();
    expect(hit(setup(), 100, B.h + 3)).toBeNull();
  });
  it("finds the panel's four edges, and the preview is the exact result", () => {
    const n = setup();
    const g = groupOf(n, "a")!.id;
    const r = rectOf(n, g);
    const body = (fx: number, fy: number) => hit(n, r.x + r.w * fx, r.y + 40 + (r.h - 40) * fy);
    const left = body(0.1, 0.5)!;
    expect(left.target).toEqual({ kind: "edge", node: g, edge: "left" });
    const applied = moveTab(n, "b", left.target);
    expect(left.preview).toEqual(layout(applied, B).nodes[groupOf(applied, "b")!.id]);
    expect(body(0.9, 0.5)!.target).toEqual({ kind: "edge", node: g, edge: "right" });
    expect(body(0.5, 0.05)!.target).toEqual({ kind: "edge", node: g, edge: "top" });
    expect(body(0.5, 0.95)!.target).toEqual({ kind: "edge", node: g, edge: "bottom" });
  });
  it("treats the strip as joining, with the DOM's answer for where", () => {
    const n = setup();
    const g = groupOf(n, "a")!.id;
    const r = rectOf(n, g);
    const h = hitTest(n, B, "b", r.x + 40, r.y + 10, () => 0)!;
    expect(h.target).toEqual({ kind: "tabs", group: g, index: 0 });
    expect(h.index).toBe(0);
    expect(h.preview.h).toBeLessThan(r.h);
  });
  it("calls a drop in a tab's own panel centre nothing", () => {
    const n = setup();
    const r = rectOf(n, groupOf(n, "a")!.id);
    expect(hit(n, r.x + r.w / 2, r.y + r.h / 2)).toBeNull();
  });
  it("joins another panel through its centre", () => {
    const n = moveTab(setup(), "b", { kind: "outer", edge: "right" });
    const a = groupOf(n, "a")!.id;
    const r = rectOf(n, a);
    const h = hit(n, r.x + r.w / 2, r.y + r.h / 2)!;
    expect(h.target).toEqual({ kind: "tabs", group: a });
    expect(h.preview).toEqual(r);
  });
  it("offers the timeline's edges but not its middle", () => {
    const n = setup();
    const r = rectOf(n, MAIN_ID);
    expect(hit(n, r.w / 2, r.h / 2)).toBeNull();
    expect(hit(n, r.w / 2, r.h * 0.9)!.target).toEqual({ kind: "edge", node: MAIN_ID, edge: "bottom" });
    expect(hit(n, r.w * 0.15, r.h / 2)!.target).toEqual({ kind: "edge", node: MAIN_ID, edge: "left" });
  });
  it("finds the workspace's outer edges first", () => {
    const n = setup();
    expect(hit(n, 3, 400)!.target).toEqual({ kind: "outer", edge: "left" });
    expect(hit(n, B.w - 3, 400)!.target).toEqual({ kind: "outer", edge: "right" });
    expect(hit(n, 400, 2)!.target).toEqual({ kind: "outer", edge: "top" });
    expect(hit(n, 400, B.h - 3)!.target).toEqual({ kind: "outer", edge: "bottom" });
  });
  it("refuses what would squeeze the timeline below its minimum", () => {
    const tight = { w: MIN_MAIN.w + MIN_GROUP.w + SASH + 20, h: 900 };
    const n = setup();
    const out = hitTest(n, tight, "b", 3, 400);
    expect(out?.refused).toBe(true);
    expect(hitTest(n, tight, "b", 400, 2)?.refused).toBeFalsy();
  });
  it("counts a point just past the top or left as that edge", () => {
    const b = { w: 1000, h: 800 };
    expect(clampToDock(b, 500, -4)).toEqual({ x: 500, y: 0 });
    expect(clampToDock(b, -30, 400)).toEqual({ x: 0, y: 400 });
    expect(clampToDock(b, 1020, 400)).toEqual({ x: 999, y: 400 });
    expect(clampToDock(b, 500, -DROP_OVERSHOOT - 1)).toBeNull();
    expect(clampToDock(b, 500, 400)).toEqual({ x: 500, y: 400 });
    const p = clampToDock(b, 500, -4)!;
    expect((hitTest(setup(), b, "b", p.x, p.y)?.target as { edge?: string }).edge).toBe("top");
  });
  it("computes where in a strip a tab lands, leaving the dragged one out", () => {
    const boxes = [{ left: 0, right: 100 }, { left: 100, right: 200 }];
    expect(tabIndexAt(boxes, 10)).toBe(0);
    expect(tabIndexAt(boxes, 60)).toBe(1);
    expect(tabIndexAt(boxes, 500)).toBe(2);
  });
});

describe("frames", () => {
  const none = new Map<string, number>();
  it("appends and filters, and never reorders what is there", () => {
    expect(liveFrames(["a", "b", "c"], ["c", "a", "b", "d"], ["d"], none)).toEqual(["a", "b", "c", "d"]);
    expect(liveFrames(["a", "b", "c"], ["c", "a"], ["c"], none)).toEqual(["a", "c"]);
  });
  it("returns the same array when nothing changed, so nothing re-renders", () => {
    const p = ["a", "b"];
    expect(liveFrames(p, ["a", "b"], ["b"], none)).toBe(p);
  });
  it("starts a background tab only when it is first shown", () => {
    expect(liveFrames([], ["a", "b", "c"], ["c"], none)).toEqual(["c"]);
  });
  it("caps the count, dropping the background tab shown longest ago", () => {
    const shown = new Map([["a", 5], ["b", 1], ["c", 3], ["d", 9]]);
    expect(liveFrames(["a", "b", "c"], ["a", "b", "c", "d"], ["d"], shown, 3)).toEqual(["a", "c", "d"]);
  });
  it("never drops what is on show, even past the cap", () => {
    expect(liveFrames(["a", "b", "c"], ["a", "b", "c"], ["a", "b", "c"], none, 2)).toEqual(["a", "b", "c"]);
  });
  it("has a cap that leaves room for a real layout", () => {
    expect(MAX_LIVE_FRAMES).toBeGreaterThanOrEqual(4);
  });
});

describe("saving", () => {
  it("round-trips through JSON", () => {
    let n = open("a", "b", "c");
    n = moveTab(n, "c", { kind: "edge", node: MAIN_ID, edge: "bottom" });
    n = resizeSplit(n, (n as Split).id, 0, 77, B);
    expect(deserialize(JSON.parse(JSON.stringify(serialize(n))))).toEqual(n);
  });
  it("gives an empty dock for anything unreadable", () => {
    for (const bad of [null, 3, "x", {}, { kind: "nope" }, { kind: "group", id: 3, tabs: [] }, { kind: "group", id: "g", tabs: [1] }, { kind: "split", id: "s", dir: "diagonal", children: [] }, { kind: "split", id: "s", dir: "row", children: [{ kind: "group" }] }]) {
      expect(deserialize(bad)).toEqual(emptyLayout());
    }
  });
  it("repairs odd sizes rather than refusing the layout", () => {
    const n = deserialize({ kind: "split", id: "s", dir: "row", sizes: "x", children: [{ kind: "main" }, { kind: "group", id: "g", tabs: ["a"] }] });
    checkInvariants(n);
    expect(allTabs(n)).toEqual(["a"]);
  });
  it("keeps the tabs of a tree too deep to draw, in one panel", () => {
    let deep: DockNode = group("g0", "t0");
    for (let i = 1; i <= MAX_DEPTH + 3; i++) deep = split(`s${i}`, i % 2 ? "row" : "col", [deep, group(`g${i}`, `t${i}`)]);
    const n = deserialize(split("top", "col", [main, deep]));
    checkInvariants(n);
    expect(allTabs(n).length).toBe(MAX_DEPTH + 4);
    expect(groups(n).length).toBe(1);
  });
});

describe("any sequence of operations", () => {
  // A small seeded generator, so a failure names a seed that reproduces it.
  const rng = (seed: number) => () => {
    seed = (seed * 1664525 + 1013904223) >>> 0;
    return seed / 2 ** 32;
  };
  const EDGES: Edge[] = ["left", "right", "top", "bottom"];

  function run(seed: number) {
    const r = rng(seed);
    const pick = <T>(a: readonly T[]) => a[Math.floor(r() * a.length)];
    let n = emptyLayout();
    const expected = new Set<string>();
    let next = 0;
    for (let step = 0; step < 120; step++) {
      const tabs = allTabs(n);
      const op = r();
      if (op < 0.2 || !tabs.length) {
        const t = `t${next++}`;
        const all = groups(n);
        n = addTab(n, t, all.length ? pick(all).id : undefined);
        expected.add(t);
      } else if (op < 0.3) {
        const t = pick(tabs);
        n = closeTab(n, t);
        expected.delete(t);
      } else if (op < 0.75) {
        const nodes = [MAIN_ID, ...groups(n).map((g) => g.id)];
        const kind = r();
        const target: DropTarget = kind < 0.3 ? { kind: "tabs", group: pick(groups(n)).id, index: Math.floor(r() * 5) } : kind < 0.8 ? { kind: "edge", node: pick(nodes), edge: pick(EDGES) } : { kind: "outer", edge: pick(EDGES) };
        n = moveTab(n, pick(tabs), target);
      } else if (op < 0.85) {
        const hits = hitTest(n, B, pick(tabs), Math.floor(r() * B.w), Math.floor(r() * B.h));
        if (hits) n = moveTab(n, pick(tabs), hits.target);
      } else if (op < 0.95) {
        const { sashes } = layout(n, B);
        if (sashes.length) {
          const s = pick(sashes);
          n = resizeSplit(n, s.split, s.index, Math.floor(r() * 600 - 300), B);
        }
      } else {
        n = selectTab(n, pick(tabs));
      }
      checkInvariants(n);
      expect(new Set(allTabs(n))).toEqual(expected);
      expect(normalize(n)).toEqual(n);
      expect(deserialize(JSON.parse(JSON.stringify(serialize(n))))).toEqual(n);
      checkTiling(n, { w: 8000, h: 6000 });
    }
  }

  for (const seed of [1, 2, 3, 7, 11, 42, 99, 1234, 5555, 90210]) {
    it(`never loses or duplicates a tab (seed ${seed})`, () => run(seed));
  }
});

describe("a sash with no room right beside it", () => {
  const g = (id: string, tabs: string[]): TabGroup => ({ kind: "group", id, tabs, active: tabs[0] });
  const wide = { w: 1500, h: 900 };
  // Two panels at their minimums and a wide timeline: what the first split of a panel leaves.
  const pair = (): Split => ({ kind: "split", id: "s", dir: "row", sizes: [1000, 1, 1], children: [{ kind: "main" }, g("a", ["a"]), g("b", ["b"])] });
  const widths = (n: DockNode) => {
    const { nodes } = layout(n, wide);
    return [nodes.main.w, nodes.a.w, nodes.b.w];
  };
  it("borrows from the next one along, as the first split would otherwise be a dead line", () => {
    const n = normalize(pair());
    expect(widths(n)[1]).toBe(MIN_GROUP.w);
    const moved = widths(resizeSplit(n, "s", 1, -100, wide));
    expect(moved[1]).toBe(MIN_GROUP.w);
    expect(moved[2]).toBe(MIN_GROUP.w + 100);
    expect(moved[0]).toBe(widths(n)[0] - 100);
  });
  it("still changes only the two beside it while they have room", () => {
    const n: DockNode = { kind: "split", id: "s", dir: "row", sizes: [0.4, 0.3, 0.3], children: [{ kind: "main" }, g("a", ["a"]), g("b", ["b"])] };
    const before = widths(n);
    const after = widths(resizeSplit(n, "s", 1, -50, wide));
    expect(after[0]).toBe(before[0]);
    expect(after[1]).toBe(before[1] - 50);
    expect(after[2]).toBe(before[2] + 50);
  });
  it("is not offered as draggable when neither side can give anything", () => {
    const tight = { w: MIN_MAIN.w + 2 * MIN_GROUP.w + 2 * SASH, h: 900 };
    expect(layout(normalize(pair()), tight).sashes.every((s) => !s.movable)).toBe(true);
    expect(layout(normalize(pair()), wide).sashes.every((s) => s.movable)).toBe(true);
  });
  it("evens the two sides on a double click, within their minimums", () => {
    const n: DockNode = { kind: "split", id: "s", dir: "row", sizes: [0.4, 0.45, 0.15], children: [{ kind: "main" }, g("a", ["a"]), g("b", ["b"])] };
    const after = widths(evenSplit(n, "s", 1, wide));
    expect(Math.abs(after[1] - after[2])).toBeLessThanOrEqual(1);
    const tight = { w: 1000, h: 900 };
    const squeezed: DockNode = { kind: "split", id: "s", dir: "row", sizes: [0.9, 0.05, 0.05], children: [{ kind: "main" }, g("a", ["a"]), g("b", ["b"])] };
    const { nodes } = layout(evenSplit(squeezed, "s", 0, tight), tight);
    expect(nodes.main.w).toBeGreaterThanOrEqual(MIN_MAIN.w);
    expect(nodes.a.w).toBeGreaterThanOrEqual(MIN_GROUP.w);
  });
});

describe("adding a tab", () => {
  it("joins behind the one on show when asked", () => {
    const one = addTab(emptyLayout(), "a");
    expect(groupOf(addTab(one, "b", undefined, false), "b")!.active).toBe("a");
    expect(groupOf(addTab(one, "b"), "b")!.active).toBe("b");
    expect(groupOf(addTab(emptyLayout(), "a", undefined, false), "a")!.active).toBe("a");
  });
});

describe("a saved layout too deep to read", () => {
  it("gives an empty dock rather than running out of stack", () => {
    let deep: unknown = { kind: "main" };
    for (let i = 0; i < 40000; i++) deep = { kind: "split", id: "s", dir: "row", sizes: [1], children: [deep] };
    expect(deserialize(deep)).toEqual(emptyLayout());
  });
});
