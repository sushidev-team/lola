// What the sidebar beside a Conversation holds: the agent's standing account, gathered from its
// `lola report` board and its `lola post` elements. All of it is a claim, and the sidebar only
// exists when there is something in it. No DOM, so it can be tested.
import { deriveBrief, type Brief, type SSHElement, type SSHTimelineItem } from "./ssh";
import type { BoardInfo } from "./board";

export type SideRow = { id: string; element: SSHElement };

export type Side = {
  brief: Brief;
  /** Only when the board has no plan of its own: two plans would disagree. */
  plan?: SideRow;
  findings: SideRow[];
  theories: SideRow[];
  metrics?: SideRow;
  /** Newest first, at most NOTES_SHOWN. The story keeps the rest. */
  notes: SideRow[];
  widgets: SideRow[];
};

export const NOTES_SHOWN = 5;

const SEVERITY_RANK: Record<string, number> = { critical: 0, high: 1, medium: 2, low: 3 };

export function deriveSide(items: SSHTimelineItem[], board: BoardInfo | null | undefined, now: number = Date.now()): Side {
  const side: Side = { brief: deriveBrief(items, now), findings: [], theories: [], notes: [], widgets: [] };
  for (const i of items) {
    const e = i.element as SSHElement | undefined;
    if (!e) continue;
    const row = { id: i.id, element: e };
    if (e.kind === "plan" && !board?.todos?.length) side.plan = row;
    else if (e.kind === "finding") side.findings.push(row);
    else if (e.kind === "hypothesis") side.theories.push(row);
    else if (e.kind === "metric") side.metrics = row;
    else if (e.kind === "narration") side.notes.unshift(row);
    else if (e.kind === "widget") side.widgets.push(row);
  }
  side.findings.sort((a, b) => (SEVERITY_RANK[a.element.severity ?? ""] ?? 4) - (SEVERITY_RANK[b.element.severity ?? ""] ?? 4));
  side.notes = side.notes.slice(0, NOTES_SHOWN);
  return side;
}

/** A board worth a section: any fact the agent reported. An empty one draws nothing. */
export function boardHasContent(b: BoardInfo | null | undefined): b is BoardInfo {
  return !!b && (!!b.blocked || !!b.hasProgress || !!b.phase || !!b.note || !!b.todos?.length || !!b.checks?.length || !!b.mismatches?.length);
}

export function sideHasContent(side: Side, board: BoardInfo | null | undefined): boolean {
  return (
    boardHasContent(board) ||
    !!side.brief.summary ||
    !!side.plan ||
    !!side.metrics ||
    side.findings.length + side.theories.length + side.notes.length + side.widgets.length > 0
  );
}
