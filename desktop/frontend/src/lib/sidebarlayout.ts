// The sidebar's project ARRANGEMENT, as pure data.
//
// The panel draws ONE list. A row is either a project or a FOLDER, side by side
// in the same order — a folder is not a section below the projects, it is a row
// among them that other rows can be dropped onto.
//
// Both facts behind that list live in config.toml: `[[project]].group` files a
// project into a folder, and the order comes from the `[[project]]` array (which
// the TUI renders too) plus each `[[group]]`'s own `position`, since an EMPTY
// folder has no member to derive a place from.
//
// This module is deliberately free of Svelte, the store and the bindings:
// dragging is the part hardest to verify by hand (WKWebView vs Chrome, see
// LiveTerminal's history), so every decision worth testing is made here, where a
// test needs no DOM.

export type LayoutProject = { name: string; group?: string };
export type LayoutGroup = { name: string; label?: string; position?: number; collapsed?: boolean };

/** One top-level row: a loose project, or a folder with its members. */
export type Row<P extends LayoutProject = LayoutProject> =
  | { kind: "project"; project: P }
  | { kind: "group"; group: LayoutGroup; projects: P[] };

/**
 * Build the top-level rows: the ungrouped projects in `[[project]]` order, with
 * each folder spliced in at its own `position` (ascending, clamped, ties broken
 * by config order). A folder's members keep their `[[project]]` order.
 *
 * Round-trips with toLayout: rebuilding from the positions it writes reproduces
 * the same list.
 */
export function buildRows<P extends LayoutProject>(projects: P[], groups: LayoutGroup[]): Row<P>[] {
  const known = new Set(groups.map((g) => g.name));
  const members = new Map<string, P[]>(groups.map((g) => [g.name, [] as P[]]));
  const rows: Row<P>[] = [];
  for (const p of projects) {
    // A group the daemon does not list is treated as no group at all. Config
    // repairs a dangling reference on load, so this only covers the window
    // between a folder's removal and the next push — during which the project
    // must stay visible, never disappear into a folder nothing draws.
    const g = p.group && known.has(p.group) ? p.group : "";
    if (g) members.get(g)!.push(p);
    else rows.push({ kind: "project", project: p });
  }
  const ordered = groups.map((g, i) => ({ g, i })).sort((a, b) => (a.g.position ?? 0) - (b.g.position ?? 0) || a.i - b.i);
  // `last` keeps two folders claiming the SAME position in config order: the
  // second would otherwise splice in ahead of the first, so a hand-written
  // config (or a not-yet-canonical one) would draw them back to front.
  let last = -1;
  for (const { g } of ordered) {
    let at = clamp(g.position ?? 0, 0, rows.length);
    if (at <= last) at = clamp(last + 1, 0, rows.length);
    rows.splice(at, 0, { kind: "group", group: g, projects: members.get(g.name)! });
    last = at;
  }
  return rows;
}

/** The layout payload ConfigService.SetProjectLayout expects. */
export type Layout = {
  groups: { name: string; label: string; position: number; collapsed: boolean }[];
  projects: { name: string; group: string }[];
};

/**
 * Lower rows back to a layout. Projects are flattened in RENDER order — each
 * top-level row in turn, a folder contributing its members — so the
 * `[[project]]` array order and what the sidebar shows can never disagree, and
 * every folder records the index it was actually drawn at.
 */
export function toLayout(rows: Row[]): Layout {
  const groups: Layout["groups"] = [];
  const projects: Layout["projects"] = [];
  rows.forEach((row, i) => {
    if (row.kind === "project") {
      projects.push({ name: row.project.name, group: "" });
      return;
    }
    groups.push({
      name: row.group.name,
      label: row.group.label ?? "",
      position: i,
      collapsed: !!row.group.collapsed,
    });
    for (const p of row.projects) projects.push({ name: p.name, group: row.group.name });
  });
  return { groups, projects };
}

/** Where a dragged project is being dropped. */
export type ProjectTarget =
  /** Between top-level rows, at this index — i.e. ungrouped. */
  | { kind: "top"; index: number }
  /** Between a folder's members, at this index. */
  | { kind: "group"; group: string; index: number }
  /** ONTO a folder's row: file it there, at the end. */
  | { kind: "into"; group: string };

/**
 * Move a project, returning NEW rows. Indices count positions AFTER the project
 * has been lifted out of wherever it was, which is what a drop indicator drawn
 * between rows means.
 *
 * An unknown project or a folder that does not exist returns the input
 * unchanged: a drag against a snapshot that has since been reloaded should cost
 * the drag, not scramble the arrangement.
 */
export function moveProject<P extends LayoutProject>(rows: Row<P>[], name: string, to: ProjectTarget): Row<P>[] {
  const group = to.kind === "top" ? "" : to.group;
  if (group && !rows.some((r) => r.kind === "group" && r.group.name === group)) return rows;

  let moved: P | undefined;
  const lifted: Row<P>[] = [];
  for (const row of rows) {
    if (row.kind === "project") {
      if (row.project.name === name) moved = row.project;
      else lifted.push(row);
      continue;
    }
    const keep: P[] = [];
    for (const p of row.projects) {
      if (p.name === name) moved = p;
      else keep.push(p);
    }
    lifted.push({ kind: "group", group: row.group, projects: keep });
  }
  if (!moved) return rows;
  const project = { ...moved, group } as P;

  if (to.kind === "top") {
    lifted.splice(clamp(to.index, 0, lifted.length), 0, { kind: "project", project });
    return lifted;
  }
  const dest = lifted.find((r) => r.kind === "group" && r.group.name === group) as
    | { kind: "group"; group: LayoutGroup; projects: P[] }
    | undefined;
  if (!dest) return rows;
  const at = to.kind === "into" ? dest.projects.length : clamp(to.index, 0, dest.projects.length);
  dest.projects.splice(at, 0, project);
  return lifted;
}

/**
 * Move a folder to `index` among the top-level rows, returning NEW rows. Its
 * members travel with it — that is what makes it a folder rather than a section
 * header.
 */
export function moveGroup<P extends LayoutProject>(rows: Row<P>[], name: string, index: number): Row<P>[] {
  const from = rows.findIndex((r) => r.kind === "group" && r.group.name === name);
  if (from < 0) return rows;
  const out = [...rows];
  const [moved] = out.splice(from, 1);
  out.splice(clamp(index, 0, out.length), 0, moved);
  return out;
}

/**
 * Convert a gap index measured against the LIVE list — the one on screen, which
 * still contains the row being dragged — into the post-lift index moveProject
 * and moveGroup take. `sourceIndex` is the dragged row's own index in that same
 * list, or null when it is not in it (dragging into a folder it is not in yet).
 *
 * Without this a downward drag lands one place PAST the indicator: the rows
 * after the source shift up by one the moment it is lifted out. The live index
 * is still what draws the indicator, so the two coordinate systems have to be
 * converted between rather than merged.
 */
export function liftedIndex(gap: number, sourceIndex: number | null): number {
  return sourceIndex !== null && sourceIndex < gap ? gap - 1 : gap;
}

/**
 * Where a pointer at `y` lands within a list of row rectangles: the index of the
 * gap it is nearest, 0..rows.length. A row is split at its MIDPOINT, so the
 * indicator flips to the far side once the pointer passes the halfway mark —
 * the behaviour every list drag has, and the reason this is not just "the index
 * of the row under the cursor".
 */
export function dropIndex(rows: { top: number; height: number }[], y: number): number {
  for (let i = 0; i < rows.length; i++) {
    if (y < rows[i].top + rows[i].height / 2) return i;
  }
  return rows.length;
}

/**
 * How a folder's row reads a pointer at `y`: its middle band means "drop INTO
 * this folder", its top and bottom edges mean the gaps either side of it.
 *
 * The edge bands are what keep a folder from swallowing every drop meant for
 * the row above or below it — with a plain midpoint split there would be no way
 * to place a project BETWEEN two folders.
 */
export function groupRowZone(rect: { top: number; height: number }, y: number): "before" | "into" | "after" {
  const edge = rect.height * 0.3;
  if (y < rect.top + edge) return "before";
  if (y > rect.top + rect.height - edge) return "after";
  return "into";
}

function clamp(n: number, lo: number, hi: number): number {
  return n < lo ? lo : n > hi ? hi : n;
}
