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
import { waitingQueue } from "./queue";

/** The title of the column whose filter also takes an SSH session waiting on a human. */
const NEEDS_YOU = KANBAN_COLUMNS.find((c) => c.key === "needs")?.title ?? "Needs You";

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
  s: Pick<SessionInfo, "status" | "agentState" | "delivery"> & { ssh?: SessionInfo["ssh"] },
  triage: string,
): boolean {
  // A go, a sign-in or an approval waiting on a human ORs into "Needs You" without
  // moving the session's kanban column, which stays a function of the two axes.
  // needsHuman is only ever set by daemon facts, never by anything an agent posted.
  if (triage === NEEDS_YOU && s.ssh?.needsHuman) return true;
  // One predicate for the list, the counts and the top bar: the roster's own "waiting on you" test
  // (which also holds a waiting session whose PR has merged), or work that regressed.
  if (triage === ATTENTION) return s.ssh?.needsHuman === true || waitingQueue([s as SessionInfo]).length > 0 || triageOf(s) === NEEDS_YOU || triageOf(s) === "Fixing";
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

/** Everything that wants a human soon: a session waiting on you, or delivered work that regressed. */
export const ATTENTION = "Needs attention";

/** The rows the session filter offers, in the order worth scanning: what needs you first. */
export const STATUS_OPTIONS: { id: string; label: string; hint: string; key?: string }[] = [
  { id: "", label: "All sessions", hint: "" },
  { id: ATTENTION, label: "Needs attention", hint: "Waiting on you, or failing CI", key: "needs" },
  { id: "Needs You", label: "Waiting on you", hint: "An agent is blocked on an answer", key: "needs" },
  { id: "Fixing", label: "Fixing", hint: "CI red, changes requested or a conflict", key: "fixing" },
  { id: "In Review", label: "In review", hint: "Pull request open, waiting on reviewers", key: "review" },
  { id: "Working", label: "Working", hint: "An agent is busy", key: "working" },
  { id: "Done", label: "Done", hint: "Merged, closed or finished", key: "done" },
  { id: "Ended", label: "Ended", hint: "Sessions that have been closed", key: "done" },
];
