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
