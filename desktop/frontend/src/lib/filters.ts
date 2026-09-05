// Triage buckets for the sidebar filter. DERIVED from theme.ts's KANBAN_COLUMNS
// rather than invented, so the sidebar filter and the kanban lens are the same
// partition by construction and the labels match Go's state.KanbanColumns().
//
// This lives OUTSIDE theme.ts on purpose: desktop/state_parity_test.go parses
// theme.ts's ALL_DISPLAYS, ALL_STATUSES and KANBAN_COLUMNS literals by regexp,
// and theme.ts is a port of internal/state. Neither may be edited freely.
//
// It also lives outside store.svelte.ts on purpose: `scopedSessions` is called
// from five components and adding a required parameter there would break every
// call site at once. Callers compose instead —
// `triaged(scopedSessions(store.sessions, nav.scoped, nav.project), nav.triage)`.
import { KANBAN_COLUMNS, kanbanColumn, kanbanTitle } from "./theme";
import type { SessionInfo } from "./store.svelte";

export const TRIAGE_FILTERS: string[] = KANBAN_COLUMNS.map((c) => c.title);

/**
 * The bucket a session belongs to, over BOTH axes (state.KanbanKeyFor).
 *
 * Falls back to the collapsed status word when the session carries no agent
 * axis — a daemon predating the split, or a snapshot written by one. Both axes
 * are optional on the wire, and bucketing every such session as "Working"
 * (which is what kanbanKey("", "") answers, by design) would empty four of the
 * five columns on that push.
 */
export function triageOf(s: Pick<SessionInfo, "status" | "agentState" | "delivery">): string {
  return s.agentState ? kanbanTitle(s.agentState, s.delivery ?? "") : kanbanColumn(s.status ?? "");
}

/**
 * Does a session fall in `triage`? "" means everything.
 *
 * This is the predicate the LIST and the sidebar's COUNTS both use, and they
 * must use the same one: a row filtered out of the list while its bucket still
 * counts it is a filter that shows an empty screen over a non-zero number.
 */
export function matchesTriageFor(
  s: Pick<SessionInfo, "status" | "agentState" | "delivery">,
  triage: string,
): boolean {
  return triage === "" || triageOf(s) === triage;
}

/**
 * The status-string form, kept for callers that hold a word rather than a
 * session. Answers over the LEGACY partition, so it agrees with
 * `matchesTriageFor` only for a session whose axes agree with its rollup —
 * which is most of them, but not the ones the split exists to separate.
 */
export function matchesTriage(status: string, triage: string): boolean {
  return triage === "" || kanbanColumn(status) === triage;
}

/** Apply a triage filter to an already-scoped, already-sorted list. */
export function triaged(list: SessionInfo[], triage: string): SessionInfo[] {
  return triage === "" ? list : list.filter((s) => matchesTriageFor(s, triage));
}
