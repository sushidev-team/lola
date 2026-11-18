// A STACK session's apps (protocol.SessionApp): one worktree per member
// repository, each with its own PR. The row's PR fields already describe
// `focusApp` — the app whose PR needs attention most — so these helpers only
// name it and summarize the rest. No collapsible rows: the other apps live in a
// "+N" chip's hover card and, for the focused session, in an app strip.

import type { SessionApp } from "@bindings/internal/protocol";

type StackSession = { stack?: boolean; apps?: SessionApp[] | null; focusApp?: string };

export type AppState = "pr" | "changes" | "untouched";

export function appLabel(a: SessionApp): string {
  return a.label || a.name;
}

export function appState(a: SessionApp): AppState {
  if ((a.number ?? 0) > 0) return "pr";
  return a.touched ? "changes" : "untouched";
}

/** The focus app, or undefined for a non-stack session (or one not yet observed). */
export function focusApp(s: StackSession): SessionApp | undefined {
  if (!s.stack) return undefined;
  return (s.apps ?? []).find((a) => a.name === s.focusApp);
}

/** The label the row's PR badge is prefixed with ("web" in "web #9"), or "". */
export function focusLabel(s: StackSession): string {
  const a = focusApp(s);
  return a ? appLabel(a) : "";
}

/**
 * How many OTHER apps have something to show — a PR or local changes — which
 * is the "+N" beside the badge. An untouched app is not counted: it is listed
 * in the card, but it is not news.
 */
export function otherActive(s: StackSession): number {
  if (!s.stack) return 0;
  return (s.apps ?? []).filter((a) => a.name !== s.focusApp && appState(a) !== "untouched").length;
}

/** An app's PR in the shape PrBadge reads. */
export function appPr(a: SessionApp): { prNumber: number; prUrl: string; checks: string; review: string } {
  return { prNumber: a.number ?? 0, prUrl: a.url ?? "", checks: a.checks ?? "", review: a.review ?? "" };
}

// --- Which repositories a stack session gets ------------------------------
// The issue's labels pick them at spawn. A missing routing label leaves a
// repository out, so the ticket picker asks before such a start, and the
// session menu can add one to a session that already runs.

type Member = { name: string; label?: string; always?: boolean };
type Ticket = { apps?: string[] | null };
type MemberProject = { name: string; label?: string; stack?: string };

/**
 * True when no label on the issue routes it to a repository: the stack has
 * members that labels select, and the issue selects none of them (it gets at
 * most the members every issue gets). That is the missed "app" label.
 */
export function unrouted(t: Ticket, members: Member[] | null | undefined): boolean {
  const labelled = (members ?? []).filter((m) => !m.always);
  if (labelled.length === 0) return false;
  const picked = new Set(t.apps ?? []);
  return !labelled.some((m) => picked.has(m.name));
}

/** The stack's members a session has NOT checked out, in config order. */
export function missingMembers(
  s: { project: string; stack?: boolean; apps?: { name: string }[] | null },
  projects: MemberProject[],
): { name: string; label: string }[] {
  if (!s.stack) return [];
  const have = new Set((s.apps ?? []).map((a) => a.name));
  return projects
    .filter((p) => p.stack === s.project && !have.has(p.name))
    .map((p) => ({ name: p.name, label: p.label || p.name }));
}
