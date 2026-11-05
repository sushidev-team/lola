// The dock's layout, as data: a tree of splits whose leaves are tab groups, plus exactly one
// "main" leaf that holds the timeline. No DOM in here, so every rule can be tested without a
// browser. Every operation returns a new, normalized tree and never edits its input.
//
// The timeline is a leaf rather than something the dock sits beside, because a panel may go
// on any side of it, or below it, and the tree then says so without a second layout system.

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}
export interface Size {
  w: number;
  h: number;
}
export type Edge = "left" | "right" | "top" | "bottom";
export type Dir = "row" | "col";

export interface MainLeaf {
  kind: "main";
}
export interface TabGroup {
  kind: "group";
  id: string;
  tabs: string[];
  active: string;
}
export interface Split {
  kind: "split";
  id: string;
  dir: Dir;
  /** One fraction per child, summing to 1. */
  sizes: number[];
  children: DockNode[];
}
export type DockNode = MainLeaf | TabGroup | Split;

/** Where a tab can go. `node` is a group id or MAIN_ID. */
export type DropTarget =
  | { kind: "tabs"; group: string; index?: number }
  | { kind: "edge"; node: string; edge: Edge }
  | { kind: "outer"; edge: Edge };

export const MAIN_ID = "main";
/** The width of the line between two panels. */
export const SASH = 1;
/** Below this the timeline is not usable, and the dock gives way rather than squeeze it. */
export const MIN_MAIN: Size = { w: 420, h: 260 };
export const MIN_GROUP: Size = { w: 180, h: 140 };
/** The gap around a panel's card, its border, and the height of its tab strip. */
export const PANEL_PAD = 4;
export const PANEL_BORDER = 1;
export const TAB_STRIP_H = 30;
/** Levels of nested split. A tree deeper than this is a layout nobody can read. */
export const MAX_DEPTH = 4;
/** How much of a leaf, from each side, counts as that side when dropping. */
export const EDGE_FRACTION = 0.25;
/** How close to the workspace's own edge counts as that edge. Thin at the top, where tabs are. */
export const OUTER_BAND = 14;
export const OUTER_BAND_TOP = 6;
/** Frames kept running, the visible ones included. Each one costs a document. */
export const MAX_LIVE_FRAMES = 8;
/** A sash's arrow keys. */
export const SASH_STEP = 16;
export const SASH_STEP_LARGE = 64;
/** How far outside the dock a dragged tab still counts: a fling at the header means the top. */
export const DROP_OVERSHOOT = 40;
/** A saved tree deeper than this is damage, and reading it must not run out of stack. */
const MAX_SAVED_DEPTH = 64;

// A new panel beside the timeline starts narrower than half, because the timeline is the work.
const MAIN_SHARE = 0.62;
const OUTER_SHARE = 0.3;

export function emptyLayout(): DockNode {
  return { kind: "main" };
}

// --- reading ------------------------------------------------------------------

export function nodeId(n: DockNode): string {
  return n.kind === "main" ? MAIN_ID : n.id;
}

export function groups(root: DockNode): TabGroup[] {
  if (root.kind === "group") return [root];
  if (root.kind === "split") return root.children.flatMap(groups);
  return [];
}

export function allTabs(root: DockNode): string[] {
  return groups(root).flatMap((g) => g.tabs);
}

export function groupOf(root: DockNode, tab: string): TabGroup | undefined {
  return groups(root).find((g) => g.tabs.includes(tab));
}

/** The tab on show in each panel. */
export function activeTabs(root: DockNode): string[] {
  return groups(root).map((g) => g.active);
}

export function depth(n: DockNode): number {
  return n.kind === "split" ? 1 + Math.max(...n.children.map(depth)) : 0;
}

function ids(root: DockNode, out = new Set<string>()): Set<string> {
  if (root.kind === "main") return out;
  out.add(root.id);
  if (root.kind === "split") root.children.forEach((c) => ids(c, out));
  return out;
}

function freshId(root: DockNode, prefix: string, taken = ids(root)): string {
  for (let i = 1; ; i++) {
    const id = `${prefix}${i}`;
    if (!taken.has(id)) return id;
  }
}

function hasNode(root: DockNode, id: string): boolean {
  if (nodeId(root) === id) return true;
  return root.kind === "split" && root.children.some((c) => hasNode(c, id));
}

function hasMain(n: DockNode): boolean {
  return n.kind === "main" || (n.kind === "split" && n.children.some(hasMain));
}

/** The same arrangement of the same tabs, whatever the ids and proportions. */
function sameShape(a: DockNode, b: DockNode): boolean {
  if (a.kind === "main" || b.kind === "main") return a.kind === b.kind;
  if (a.kind === "group") return b.kind === "group" && a.tabs.join("\n") === b.tabs.join("\n");
  if (b.kind !== "split" || a.dir !== b.dir || a.children.length !== b.children.length) return false;
  return a.children.every((c, i) => sameShape(c, b.children[i]));
}

// --- normalizing --------------------------------------------------------------

function cleanSizes(raw: number[], n: number): number[] {
  const given = raw.length === n && raw.every((s) => Number.isFinite(s) && s > 0);
  if (!given) return Array.from({ length: n }, () => 1 / n);
  const sum = raw.reduce((a, b) => a + b, 0);
  // Left alone when it already adds up, or dividing would walk the last bits on every pass
  // and normalizing would not be idempotent.
  return Math.abs(sum - 1) < 1e-9 ? raw : raw.map((s) => s / sum);
}

type Normalizing = { tabs: Set<string>; used: Set<string>; main: boolean };

function norm(n: DockNode, st: Normalizing): DockNode | null {
  if (n.kind === "main") {
    if (st.main) return null;
    st.main = true;
    return n;
  }
  if (n.kind === "group") {
    const tabs = [...new Set(n.tabs)].filter((t) => !st.tabs.has(t));
    if (!tabs.length) return null;
    tabs.forEach((t) => st.tabs.add(t));
    const id = st.used.has(n.id) || !n.id ? freshId(n, "g", st.used) : n.id;
    st.used.add(id);
    return { kind: "group", id, tabs, active: tabs.includes(n.active) ? n.active : tabs[0] };
  }
  const given = cleanSizes(n.sizes, n.children.length);
  const children: DockNode[] = [];
  const sizes: number[] = [];
  n.children.forEach((child, i) => {
    const c = norm(child, st);
    if (!c) return;
    // A row inside a row is the same row. Flattening keeps a sash from sitting inside a
    // nested split it has nothing to do with, and keeps the depth honest.
    if (c.kind === "split" && c.dir === n.dir) {
      c.children.forEach((cc, j) => {
        children.push(cc);
        sizes.push(c.sizes[j] * given[i]);
      });
    } else {
      children.push(c);
      sizes.push(given[i]);
    }
  });
  if (!children.length) return null;
  if (children.length === 1) return children[0];
  const id = st.used.has(n.id) || !n.id ? freshId(n, "s", st.used) : n.id;
  st.used.add(id);
  return { kind: "split", id, dir: n.dir, sizes: cleanSizes(sizes, children.length), children };
}

/**
 * The tree's one canonical form: no empty groups, no one-child splits, no split directly
 * inside one of the same direction, one main leaf, each tab once, fractions summing to 1.
 */
export function normalize(root: DockNode): DockNode {
  const st: Normalizing = { tabs: new Set(), used: new Set(), main: false };
  const out = norm(root, st);
  if (!out) return emptyLayout();
  if (st.main) return out;
  // A tree that lost its timeline is damaged, and the timeline is not optional.
  return normalize({ kind: "split", id: freshId(out, "s"), dir: "row", sizes: [MAIN_SHARE, 1 - MAIN_SHARE], children: [emptyLayout(), out] });
}

// --- tabs ---------------------------------------------------------------------

/**
 * A tab joins the first panel, or a new one to the timeline's right if there is none. With
 * `front` false it joins behind the tab on show, for one nobody asked to look at.
 */
export function addTab(root: DockNode, tab: string, into?: string, front = true): DockNode {
  if (groupOf(root, tab)) return root;
  const all = groups(root);
  const g = all.find((x) => x.id === into) ?? all[0];
  if (g) return replace(root, g.id, { ...g, tabs: [...g.tabs, tab], active: front ? tab : g.active });
  return moveInto(root, { kind: "group", id: freshId(root, "g"), tabs: [tab], active: tab }, { kind: "edge", node: MAIN_ID, edge: "right" });
}

export function selectTab(root: DockNode, tab: string): DockNode {
  const g = groupOf(root, tab);
  return !g || g.active === tab ? root : replace(root, g.id, { ...g, active: tab });
}

export function closeTab(root: DockNode, tab: string): DockNode {
  return groupOf(root, tab) ? normalize(withoutTab(root, tab)) : root;
}

/** Keeps only these tabs, for a saved layout naming widgets that no longer exist. */
export function retainTabs(root: DockNode, known: ReadonlySet<string>): DockNode {
  const stale = allTabs(root).filter((t) => !known.has(t));
  return stale.length ? normalize(stale.reduce(withoutTab, root)) : root;
}

function withoutTab(root: DockNode, tab: string): DockNode {
  if (root.kind === "split") return { ...root, children: root.children.map((c) => withoutTab(c, tab)) };
  if (root.kind !== "group" || !root.tabs.includes(tab)) return root;
  const at = root.tabs.indexOf(tab);
  const tabs = root.tabs.filter((t) => t !== tab);
  // The neighbor takes over, as it does in every tabbed editor.
  const active = root.active === tab ? (tabs[at] ?? tabs[at - 1] ?? "") : root.active;
  return { ...root, tabs, active };
}

function replace(root: DockNode, id: string, next: DockNode): DockNode {
  if (nodeId(root) === id) return next;
  if (root.kind !== "split") return root;
  return { ...root, children: root.children.map((c) => replace(c, id, next)) };
}

function moveInto(root: DockNode, group: TabGroup, target: DropTarget): DockNode {
  const before = (e: Edge) => e === "left" || e === "top";
  const dirOf = (e: Edge): Dir => (e === "left" || e === "right" ? "row" : "col");
  const pair = (a: DockNode, share: number, edge: Edge, id: string): Split => ({
    kind: "split",
    id,
    dir: dirOf(edge),
    sizes: before(edge) ? [1 - share, share] : [share, 1 - share],
    children: before(edge) ? [group, a] : [a, group],
  });
  const taken = ids(root);
  taken.add(group.id);
  const splitId = freshId(root, "s", taken);
  if (target.kind === "outer") return normalize(pair(root, 1 - OUTER_SHARE, target.edge, splitId));
  if (target.kind !== "edge") return root;
  const at = findNode(root, target.node);
  if (!at) return root;
  return normalize(replace(root, target.node, pair(at, at.kind === "main" ? MAIN_SHARE : 0.5, target.edge, splitId)));
}

function findNode(root: DockNode, id: string): DockNode | undefined {
  if (nodeId(root) === id) return root;
  if (root.kind !== "split") return undefined;
  for (const c of root.children) {
    const f = findNode(c, id);
    if (f) return f;
  }
  return undefined;
}

/**
 * Moves a tab. A target that cannot apply, or one that would nest deeper than MAX_DEPTH,
 * returns the tree it was given, so a caller can compare to learn nothing happened.
 */
export function moveTab(root: DockNode, tab: string, target: DropTarget): DockNode {
  const from = groupOf(root, tab);
  if (!from) return root;
  if (target.kind === "tabs") {
    const to = groups(root).find((g) => g.id === target.group);
    if (!to) return root;
    if (to.id === from.id && to.tabs.length === 1) return root;
    const stripped = withoutTab(root, tab);
    const list = groups(stripped).find((g) => g.id === to.id)!.tabs;
    const index = Math.max(0, Math.min(list.length, target.index ?? list.length));
    const tabs = [...list.slice(0, index), tab, ...list.slice(index)];
    if (to.id === from.id && tabs.join("\n") === from.tabs.join("\n") && from.active === tab) return root;
    return normalize(replace(stripped, to.id, { kind: "group", id: to.id, tabs, active: tab }));
  }
  if (target.kind === "edge") {
    if (!hasNode(root, target.node)) return root;
    // Splitting a panel by its only tab would just put the same panel back.
    if (target.node === from.id && from.tabs.length === 1) return root;
  }
  const stripped = withoutTab(root, tab);
  const fresh: TabGroup = { kind: "group", id: freshId(root, "g"), tabs: [tab], active: tab };
  const next = moveInto(stripped, fresh, target);
  if (depth(next) > MAX_DEPTH || sameShape(root, next)) return root;
  return next;
}

/** Reorders a tab within its strip, for the keyboard. */
export function shiftTab(root: DockNode, tab: string, delta: number): DockNode {
  const g = groupOf(root, tab);
  if (!g) return root;
  const at = g.tabs.indexOf(tab) + delta;
  if (at < 0 || at >= g.tabs.length) return root;
  return moveTab(root, tab, { kind: "tabs", group: g.id, index: at });
}

/**
 * The target for "Move to new panel". A panel with other tabs splits. A panel with only this
 * tab has nothing to split from, so the tab goes to the workspace's edge instead.
 */
export function newPanelTarget(root: DockNode, tab: string, edge: Edge, bounds: Size): DropTarget | null {
  const g = groupOf(root, tab);
  if (!g) return null;
  const target: DropTarget = g.tabs.length > 1 ? { kind: "edge", node: g.id, edge } : { kind: "outer", edge };
  const next = moveTab(root, tab, target);
  return next !== root && fits(next, bounds) ? target : null;
}

/** The target for "Move to next panel", or null when there is only one. */
export function nextPanelTarget(root: DockNode, tab: string): DropTarget | null {
  const all = groups(root);
  const at = all.findIndex((g) => g.tabs.includes(tab));
  if (at < 0 || all.length < 2) return null;
  return { kind: "tabs", group: all[(at + 1) % all.length].id };
}

// --- geometry -----------------------------------------------------------------

export function minSize(n: DockNode): Size {
  if (n.kind === "main") return MIN_MAIN;
  if (n.kind === "group") return MIN_GROUP;
  const mins = n.children.map(minSize);
  const gaps = SASH * (n.children.length - 1);
  return n.dir === "row"
    ? { w: mins.reduce((a, m) => a + m.w, gaps), h: Math.max(...mins.map((m) => m.h)) }
    : { h: mins.reduce((a, m) => a + m.h, gaps), w: Math.max(...mins.map((m) => m.w)) };
}

export function fits(root: DockNode, bounds: Size): boolean {
  const m = minSize(root);
  return bounds.w >= m.w && bounds.h >= m.h;
}

export interface Sash {
  split: string;
  /** Between children `index` and `index + 1`. */
  index: number;
  dir: Dir;
  rect: Rect;
  /** False when every panel on both sides is at its minimum, so dragging it does nothing. */
  movable: boolean;
}
export interface Layout {
  /** Every node's rect by id, MAIN_ID included. */
  nodes: Record<string, Rect>;
  sashes: Sash[];
}

function distribute(total: number, sizes: number[], mins: number[]): number[] {
  const minSum = mins.reduce((a, b) => a + b, 0);
  let exact: number[];
  if (total <= minSum) {
    // It cannot fit. The caller shows the drawer in this case, but the arithmetic stays sane.
    exact = mins.map((m) => (minSum ? (m * total) / minSum : 0));
  } else {
    const fixed = sizes.map(() => false);
    let left = total;
    let share = sizes.reduce((a, b) => a + b, 0);
    for (let again = true; again; ) {
      again = false;
      for (let i = 0; i < sizes.length; i++) {
        if (fixed[i] || (sizes[i] / share) * left >= mins[i]) continue;
        fixed[i] = true;
        left -= mins[i];
        share -= sizes[i];
        again = true;
      }
    }
    exact = sizes.map((s, i) => (fixed[i] ? mins[i] : (s / share) * left));
  }
  // Rounded on the running total, so whole pixels still add up and no seam opens.
  let run = 0;
  let prev = 0;
  return exact.map((v) => {
    run += v;
    const at = Math.round(run);
    const px = at - prev;
    prev = at;
    return px;
  });
}

export function layout(root: DockNode, bounds: Size): Layout {
  const out: Layout = { nodes: {}, sashes: [] };
  const place = (n: DockNode, r: Rect) => {
    out.nodes[nodeId(n)] = r;
    if (n.kind !== "split") return;
    const row = n.dir === "row";
    const total = Math.max(0, (row ? r.w : r.h) - SASH * (n.children.length - 1));
    const mins = n.children.map((c) => (row ? minSize(c).w : minSize(c).h));
    const px = distribute(total, n.sizes, mins);
    const spare = px.reduce((sum, p, i) => sum + Math.max(0, p - mins[i]), 0);
    let at = row ? r.x : r.y;
    n.children.forEach((c, i) => {
      place(c, row ? { x: at, y: r.y, w: px[i], h: r.h } : { x: r.x, y: at, w: r.w, h: px[i] });
      at += px[i];
      if (i < n.children.length - 1) {
        out.sashes.push({ split: n.id, index: i, dir: n.dir, rect: row ? { x: at, y: r.y, w: SASH, h: r.h } : { x: r.x, y: at, w: r.w, h: SASH }, movable: spare > 0 });
        at += SASH;
      }
    });
  };
  place(root, { x: 0, y: 0, w: Math.max(0, bounds.w), h: Math.max(0, bounds.h) });
  return out;
}

/** Where a panel's page goes: inside its card, below its tab strip. */
export function panelSlot(r: Rect): Rect {
  const inset = PANEL_PAD + PANEL_BORDER;
  return { x: r.x + inset, y: r.y + inset + TAB_STRIP_H, w: Math.max(0, r.w - 2 * inset), h: Math.max(0, r.h - 2 * inset - TAB_STRIP_H) };
}

/**
 * Drags the line between two children by `delta` pixels. The child on the far side of the line
 * grows. The one it pushes against shrinks to its minimum, and then the next one along gives
 * way, as in VS Code. A new panel starts at its minimum beside another, and without that its
 * line could not move even with a wide timeline just beyond it.
 */
export function resizeSplit(root: DockNode, split: string, index: number, delta: number, bounds: Size): DockNode {
  const s = findNode(root, split);
  if (!s || s.kind !== "split" || index < 0 || index >= s.children.length - 1) return root;
  const { nodes } = layout(root, bounds);
  const row = s.dir === "row";
  const px = s.children.map((c) => (row ? nodes[nodeId(c)].w : nodes[nodeId(c)].h));
  const room = (i: number) => Math.max(0, px[i] - (row ? minSize(s.children[i]).w : minSize(s.children[i]).h));
  const before = s.children.map((_, i) => i).filter((i) => i <= index).reverse();
  const after = s.children.map((_, i) => i).filter((i) => i > index);
  const roomOf = (list: number[]) => list.reduce((sum, i) => sum + room(i), 0);
  const d = Math.max(-roomOf(before), Math.min(roomOf(after), delta));
  if (!d) return root;
  let left = Math.abs(d);
  for (const i of d < 0 ? before : after) {
    const take = Math.min(room(i), left);
    px[i] -= take;
    left -= take;
  }
  px[d < 0 ? index + 1 : index] += Math.abs(d);
  const total = px.reduce((a, b) => a + b, 0);
  return replace(root, split, { ...s, sizes: cleanSizes(px.map((p) => p / total), px.length) });
}

/** Double-clicking a line shares its two sides equally, as far as their minimums allow. */
export function evenSplit(root: DockNode, split: string, index: number, bounds: Size): DockNode {
  const s = findNode(root, split);
  if (!s || s.kind !== "split" || index < 0 || index >= s.children.length - 1) return root;
  const { nodes } = layout(root, bounds);
  const row = s.dir === "row";
  const size = (n: DockNode) => (row ? nodes[nodeId(n)].w : nodes[nodeId(n)].h);
  const min = (n: DockNode) => (row ? minSize(n).w : minSize(n).h);
  const px = s.children.map(size);
  const [a, b] = [s.children[index], s.children[index + 1]];
  const both = px[index] + px[index + 1];
  const half = Math.max(min(a), Math.min(both - min(b), both / 2));
  if (Math.abs(half - px[index]) < 1) return root;
  px[index] = half;
  px[index + 1] = both - half;
  const total = px.reduce((sum, p) => sum + p, 0);
  return replace(root, split, { ...s, sizes: cleanSizes(px.map((p) => p / total), px.length) });
}

// --- hit testing --------------------------------------------------------------

/**
 * A point just outside the dock counts as its edge, so the header and the sidebar do not swallow
 * a drag that overshot. The window's frame stops the pointer at the right and bottom, but content
 * sits above and to the left, where the bands would otherwise be a few pixels to hit.
 */
export function clampToDock(bounds: Size, x: number, y: number, slack = DROP_OVERSHOOT): { x: number; y: number } | null {
  if (x < -slack || y < -slack || x >= bounds.w + slack || y >= bounds.h + slack) return null;
  return { x: Math.max(0, Math.min(bounds.w - 1, x)), y: Math.max(0, Math.min(bounds.h - 1, y)) };
}

export interface Hit {
  target: DropTarget;
  /** What the result will occupy: the new panel's exact rect, or the strip or panel it joins. */
  preview: Rect;
  /** A drop into a strip: where the insertion mark goes among its tabs. */
  index?: number;
  /** A place that means something, with no room for it. Dropping here does nothing. */
  refused?: boolean;
}

/** Which position a tab dropped at `x` takes, among a strip's other tabs. */
export function tabIndexAt(others: { left: number; right: number }[], x: number): number {
  return others.filter((b) => (b.left + b.right) / 2 < x).length;
}

const inRect = (r: Rect, x: number, y: number, slack = 0) => x >= r.x - slack && x < r.x + r.w + slack && y >= r.y - slack && y < r.y + r.h + slack;

function leafAt(root: DockNode, nodes: Record<string, Rect>, x: number, y: number): DockNode | null {
  if (root.kind !== "split") return inRect(nodes[nodeId(root)], x, y, SASH) ? root : null;
  for (const c of root.children) {
    const f = leafAt(c, nodes, x, y);
    if (f) return f;
  }
  return null;
}

function outerEdge(bounds: Size, x: number, y: number): Edge | null {
  if (y < OUTER_BAND_TOP) return "top";
  if (x < OUTER_BAND) return "left";
  if (x >= bounds.w - OUTER_BAND) return "right";
  if (y >= bounds.h - OUTER_BAND) return "bottom";
  return null;
}

function edgeOf(r: Rect, x: number, y: number): Edge | null {
  const sides: [Edge, number][] = [
    ["left", (x - r.x) / r.w],
    ["right", (r.x + r.w - x) / r.w],
    ["top", (y - r.y) / r.h],
    ["bottom", (r.y + r.h - y) / r.h],
  ];
  const [edge, near] = sides.reduce((a, b) => (b[1] < a[1] ? b : a));
  return near < EDGE_FRACTION ? edge : null;
}

/**
 * What dropping `tab` at this point does, or null when it means nothing. A move with no room says
 * so with `refused`, so the drag can tell the human why nothing would happen. The preview comes
 * from actually applying the move, so it is the exact area the panel will have.
 * `stripIndex` answers where in a strip the point falls, which only the DOM can know.
 */
export function hitTest(root: DockNode, bounds: Size, tab: string, x: number, y: number, stripIndex?: (group: string, x: number) => number): Hit | null {
  const from = groupOf(root, tab);
  if (!from || !inRect({ x: 0, y: 0, w: bounds.w, h: bounds.h }, x, y)) return null;

  const resulting = (target: DropTarget): Hit | null => {
    const next = moveTab(root, tab, target);
    if (next === root) return null;
    if (!fits(next, bounds)) return { target, preview: { x: 0, y: 0, w: 0, h: 0 }, refused: true };
    const g = groupOf(next, tab);
    return g ? { target, preview: layout(next, bounds).nodes[g.id] } : null;
  };

  const out = outerEdge(bounds, x, y);
  if (out) return resulting({ kind: "outer", edge: out });

  const { nodes } = layout(root, bounds);
  const leaf = leafAt(root, nodes, x, y);
  if (!leaf) return null;
  const r = nodes[nodeId(leaf)];
  if (leaf.kind === "main") {
    // The middle of the timeline is not a place for a tab: it has no strip, and a drop there
    // would have nothing to mean.
    const edge = edgeOf(r, x, y);
    return edge ? resulting({ kind: "edge", node: MAIN_ID, edge }) : null;
  }
  if (leaf.kind !== "group") return null;

  const stripEnd = r.y + PANEL_PAD + PANEL_BORDER + TAB_STRIP_H;
  if (y < stripEnd) {
    const index = stripIndex?.(leaf.id, x) ?? (leaf.id === from.id ? leaf.tabs.length - 1 : leaf.tabs.length);
    const target: DropTarget = { kind: "tabs", group: leaf.id, index };
    if (moveTab(root, tab, target) === root) return null;
    return { target, preview: { x: r.x, y: r.y, w: r.w, h: stripEnd - r.y }, index };
  }
  const body: Rect = { x: r.x, y: stripEnd, w: r.w, h: r.y + r.h - stripEnd };
  const edge = edgeOf(body, x, y);
  if (edge) return resulting({ kind: "edge", node: leaf.id, edge });
  if (leaf.id === from.id) return null;
  return { target: { kind: "tabs", group: leaf.id }, preview: r };
}

// --- frames -------------------------------------------------------------------

/**
 * Which frames run, in the order their nodes sit in the DOM. Only ever appended to and filtered,
 * never reordered, because moving an iframe in the DOM reloads it. Past the cap, the background
 * tab shown longest ago goes. What is on show always runs.
 */
export function liveFrames(prev: readonly string[], all: readonly string[], visible: readonly string[], lastShown: ReadonlyMap<string, number>, cap = MAX_LIVE_FRAMES): string[] {
  const exists = new Set(all);
  let live = prev.filter((t) => exists.has(t));
  for (const t of visible) if (exists.has(t) && !live.includes(t)) live.push(t);
  const showing = new Set(visible);
  const spare = live.filter((t) => !showing.has(t)).sort((a, b) => (lastShown.get(a) ?? 0) - (lastShown.get(b) ?? 0));
  const drop = new Set(spare.slice(0, Math.max(0, live.length - cap)));
  if (drop.size) live = live.filter((t) => !drop.has(t));
  return live.length === prev.length && live.every((t, i) => t === prev[i]) ? (prev as string[]) : live;
}

// --- saving -------------------------------------------------------------------

function parseNode(raw: unknown, level = 0): DockNode | null {
  if (!raw || typeof raw !== "object" || level > MAX_SAVED_DEPTH) return null;
  const n = raw as Record<string, unknown>;
  if (n.kind === "main") return { kind: "main" };
  if (typeof n.id !== "string") return null;
  if (n.kind === "group") {
    if (!Array.isArray(n.tabs) || !n.tabs.every((t) => typeof t === "string" && t)) return null;
    return { kind: "group", id: n.id, tabs: n.tabs as string[], active: typeof n.active === "string" ? n.active : "" };
  }
  if (n.kind !== "split" || (n.dir !== "row" && n.dir !== "col") || !Array.isArray(n.children)) return null;
  const children: DockNode[] = [];
  for (const c of n.children) {
    const p = parseNode(c, level + 1);
    if (!p) return null;
    children.push(p);
  }
  const sizes = Array.isArray(n.sizes) ? n.sizes.map(Number) : [];
  return { kind: "split", id: n.id, dir: n.dir, sizes, children };
}

export function serialize(root: DockNode): unknown {
  return normalize(root);
}

/**
 * Anything unreadable gives an empty dock, because a saved layout is a convenience and must
 * never stop a session from opening. A tree too deep to draw keeps its tabs in one panel.
 */
export function deserialize(raw: unknown): DockNode {
  const parsed = parseNode(raw);
  if (!parsed) return emptyLayout();
  const clean = normalize(parsed);
  if (depth(clean) <= MAX_DEPTH) return clean;
  return allTabs(clean).reduce((n, t) => addTab(n, t), emptyLayout());
}
