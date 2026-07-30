// Triage buckets for the sidebar filter. DERIVED from theme.ts's KANBAN_COLUMNS
// rather than invented, so the sidebar filter and the kanban lens are the same
// partition by construction and the labels match Go's state.KanbanColumns().
//
// This lives OUTSIDE theme.ts on purpose: desktop/state_parity_test.go parses
// theme.ts's ALL_STATUSES and KANBAN_COLUMNS literals by regexp, and theme.ts is
// a verbatim port of internal/tui/theme.go. Neither may be edited.
//
// It also lives outside store.svelte.ts on purpose: `scopedSessions` is called
// from five components and adding a required parameter there would break every
// call site at once. Callers compose instead —
// `triaged(scopedSessions(store.sessions, nav.scoped, nav.project), nav.triage)`.
import { KANBAN_COLUMNS, kanbanColumn } from "./theme";
import type { SessionInfo } from "./store.svelte";

export const TRIAGE_FILTERS: string[] = KANBAN_COLUMNS.map((c) => c.title);

/** "" means everything. */
export function matchesTriage(status: string, triage: string): boolean {
  return triage === "" || kanbanColumn(status) === triage;
}

/** Apply a triage filter to an already-scoped, already-sorted list. */
export function triaged(list: SessionInfo[], triage: string): SessionInfo[] {
  return triage === "" ? list : list.filter((s) => matchesTriage(s.status, triage));
}
