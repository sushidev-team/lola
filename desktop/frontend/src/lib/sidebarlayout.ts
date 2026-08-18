// The sidebar's project ARRANGEMENT, as pure data.
//
// A project's place is two facts, both in config.toml: which [[group]] it is
// filed under and where it sits in the [[project]] array — the order every
// surface renders, the TUI included. This module turns those two facts into the
// sections the sidebar draws, applies a drag to them, and lowers the result back
// into the layout ConfigService.SetProjectLayout takes.
//
// It is deliberately free of Svelte, the store and the bindings: dragging is the
// part of this feature that is hardest to verify by hand (WKWebView vs Chrome,
// see LiveTerminal's history), so the decisions worth testing are made here,
// where a test needs no DOM.

export type LayoutProject = { name: string; group?: string };
export type LayoutGroup = { name: string; label?: string; collapsed?: boolean };

/** One rendered block: the top-level section (group === null) or a folder. */
export type Section<P extends LayoutProject = LayoutProject> = {
  group: LayoutGroup | null;
  projects: P[];
};

/**
 * Split projects into render sections: the ungrouped ones first, then every
 * configured group in its own order — EMPTY groups included, because an empty
 * folder is a real thing here (it is created before anything is dragged in).
 *
 * The top-level section is always present, even when empty: it is the drop
 * target for dragging a project OUT of a folder, so it cannot vanish just
 * because every project currently sits in one.
 */
export function buildSections<P extends LayoutProject>(projects: P[], groups: LayoutGroup[]): Section<P>[] {
  const known = new Set(groups.map((g) => g.name));
  const sections: Section<P>[] = [{ group: null, projects: [] }];
  const byGroup = new Map<string, P[]>();
  for (const g of groups) {
    const bucket: P[] = [];
    byGroup.set(g.name, bucket);
    sections.push({ group: g, projects: bucket });
  }
  for (const p of projects) {
    // A group the daemon does not list is treated as no group at all. Config
    // repairs a dangling reference on load, so this only covers the window
    // between a group's removal and the next push — during which the project
    // must stay visible, never disappear into a folder nothing draws.
    const g = p.group && known.has(p.group) ? p.group : "";
    if (g) byGroup.get(g)!.push(p);
    else sections[0].projects.push(p);
  }
  return sections;
}

/** The layout payload ConfigService.SetProjectLayout expects. */
export type Layout = {
  groups: { name: string; label: string; collapsed: boolean }[];
  projects: { name: string; group: string }[];
};

/**
 * Lower sections back to a layout. Projects are flattened in RENDER order —
 * top level first, then each group's members — so the [[project]] array order
 * and what the sidebar shows can never disagree.
 */
export function toLayout(sections: Section[]): Layout {
  const groups: Layout["groups"] = [];
  const projects: Layout["projects"] = [];
  for (const s of sections) {
    if (s.group) groups.push({ name: s.group.name, label: s.group.label ?? "", collapsed: !!s.group.collapsed });
    for (const p of s.projects) projects.push({ name: p.name, group: s.group?.name ?? "" });
  }
  return { groups, projects };
}

/**
 * Move a project to `index` within the section identified by `group` ("" = top
 * level), returning NEW sections. `index` counts positions in the target
 * section AFTER the project has been lifted out of wherever it was, which is
 * what a drop indicator drawn between rows means.
 *
 * An unknown project or target returns the input unchanged: a drag against a
 * snapshot that has since been reloaded should cost the drag, not scramble the
 * arrangement.
 */
export function moveProject<P extends LayoutProject>(sections: Section<P>[], name: string, group: string, index: number): Section<P>[] {
  const target = sections.find((s) => (s.group?.name ?? "") === group);
  if (!target) return sections;
  let moved: P | undefined;
  const lifted = sections.map((s) => {
    const keep: P[] = [];
    for (const p of s.projects) {
      if (p.name === name) moved = p;
      else keep.push(p);
    }
    return { group: s.group, projects: keep };
  });
  if (!moved) return sections;
  const dest = lifted.find((s) => (s.group?.name ?? "") === group)!;
  const at = clamp(index, 0, dest.projects.length);
  dest.projects.splice(at, 0, { ...moved, group } as P);
  return lifted;
}

/**
 * Move a group to `index` among the groups, returning NEW sections. `index` is
 * a position among GROUPS only — the top-level section is not one and always
 * stays first, so a folder can never be dragged above the loose projects.
 */
export function moveGroup<P extends LayoutProject>(sections: Section<P>[], name: string, index: number): Section<P>[] {
  const top = sections.filter((s) => !s.group);
  const groups = sections.filter((s) => !!s.group);
  const from = groups.findIndex((s) => s.group!.name === name);
  if (from < 0) return sections;
  const [moved] = groups.splice(from, 1);
  groups.splice(clamp(index, 0, groups.length), 0, moved);
  return [...top, ...groups];
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

function clamp(n: number, lo: number, hi: number): number {
  return n < lo ? lo : n > hi ? hi : n;
}
