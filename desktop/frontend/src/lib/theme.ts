// Status → presentation, ported verbatim from the TUI (internal/tui/theme.go,
// sessions.go, sessionview.go). This is the single source of truth for how a
// derived session status renders — colors, pills, badges, sort rank, kanban
// bucket — so the desktop app and the TUI agree pixel-for-semantic.

export type PillKind = "urgent" | "broken" | "work" | "done" | "grey" | "plain";

/** Tailwind text-color utility for a status label (statusStyle in the TUI). */
export function statusText(status: string): string {
  switch (status) {
    case "working":
      return "text-info";
    case "ci_failed":
    case "changes_requested":
    case "merge_conflict":
      return "text-bad";
    case "approved":
      return "text-good";
    case "needs_input":
    case "no_signal":
      return "text-orange";
    case "dead":
      return "text-white"; // rendered on a bad-colored fill by the caller
    case "merged":
    case "session_ended":
    case "idle":
      return "text-faint";
    default:
      return "text-ink";
  }
}

/** The pill kind for a status (statusPill in the TUI). */
export function pillKind(status: string): PillKind {
  switch (status) {
    case "needs_input":
      return "urgent";
    case "ci_failed":
    case "changes_requested":
    case "merge_conflict":
      return "broken";
    case "working":
    case "ci_pending":
    case "draft":
      return "work";
    case "approved":
    case "pr_open":
      return "done";
    case "review_pending":
      return "grey";
    default:
      return "plain"; // merged/dead/session_ended/idle/unknown → plain text
  }
}

/** Tailwind classes for a status pill fill. `plain` returns the text color. */
export function pillClasses(status: string): string {
  switch (pillKind(status)) {
    case "urgent":
      return "bg-pill-urgent text-pill-urgent-fg font-semibold";
    case "broken":
      return "bg-pill-broken text-pill-broken-fg font-semibold";
    case "work":
      return "bg-pill-work text-pill-work-fg";
    case "done":
      return "bg-pill-done text-pill-done-fg";
    case "grey":
      return "bg-pill-grey text-pill-grey-fg";
    default:
      return status === "dead" ? "bg-bad text-white" : statusText(status);
  }
}

/** Short human label for a status (statusLabel in the TUI). */
export function statusLabel(status: string): string {
  switch (status) {
    case "changes_requested":
      return "changes";
    case "review_pending":
      return "review";
    case "merge_conflict":
      return "conflict";
    case "session_ended":
      return "ended";
    case "ci_pending":
      return "pending";
    case "needs_input":
      return "needs you";
    default:
      return status;
  }
}

/** ≤2-char glyph for a status (statusBadge in the TUI). */
export function statusBadge(status: string): string {
  const m: Record<string, string> = {
    working: "wk",
    ci_pending: "ci",
    needs_input: "!!",
    ci_failed: "!x",
    changes_requested: "cr",
    merge_conflict: "mc",
    review_pending: "rv",
    approved: "ok",
    pr_open: "pr",
    merged: "mg",
    dead: "xx",
    session_ended: "en",
    idle: "..",
    draft: "df",
  };
  return m[status] ?? "??";
}

/** Attention-first sort tier (sortRank in the TUI). Lower sorts first. */
export function sortRank(status: string): number {
  switch (status) {
    case "needs_input":
      return 0;
    case "ci_failed":
    case "changes_requested":
    case "merge_conflict":
      return 1;
    case "working":
    case "ci_pending":
    case "draft":
      return 2;
    case "review_pending":
    case "approved":
    case "pr_open":
      return 3;
    case "merged":
    case "dead":
    case "session_ended":
    case "closed":
      return 5;
    default:
      return 4;
  }
}

/** Statuses that need a human (attentionStatuses in the TUI). */
export const ATTENTION_STATUSES = new Set([
  "needs_input",
  "ci_failed",
  "changes_requested",
  "merge_conflict",
]);

export function isAttention(status: string): boolean {
  return ATTENTION_STATUSES.has(status);
}

/** Kanban columns and the statuses they bucket (KanbanColumns in the TUI). */
export const KANBAN_COLUMNS: { title: string; statuses: string[] }[] = [
  { title: "Needs You", statuses: ["needs_input"] },
  { title: "Working", statuses: ["working", "ci_pending", "idle"] },
  { title: "Fixing", statuses: ["ci_failed", "changes_requested", "merge_conflict"] },
  { title: "In Review", statuses: ["review_pending", "approved", "pr_open"] },
  { title: "Done", statuses: ["merged", "dead", "session_ended"] },
];

/** Which kanban column a status falls in; unmapped → Working (the TUI fallback). */
export function kanbanColumn(status: string): string {
  for (const c of KANBAN_COLUMNS) if (c.statuses.includes(status)) return c.title;
  return "Working";
}

/** Activity-feed phrase for a status transition (eventPhrase in the TUI). */
export function eventPhrase(from: string, to: string): string {
  if (from === "") return "spawned";
  const m: Record<string, string> = {
    working: "resumed",
    needs_input: "needs you",
    draft: "PR opened",
    review_pending: "in review",
    ci_pending: "CI running",
    ci_failed: "CI failed",
    changes_requested: "changes req",
    merge_conflict: "conflict",
    approved: "approved",
    merged: "merged",
    closed: "PR closed",
    session_ended: "ended",
    dead: "died",
  };
  return m[to] ?? to;
}

/** Tailwind text color for a "reacting" phrase (reactingStyle in the TUI). */
export function reactingText(reacting: string): string {
  if (reacting === "escalated") return "text-bad";
  if (reacting === "ready to merge") return "text-good";
  if (
    reacting.startsWith("ci retry") ||
    reacting === "addressing review" ||
    reacting === "rebasing"
  )
    return "text-warn";
  return "text-ink";
}

export interface Attentionish {
  status: string;
}

/** Count of sessions needing a human (AttentionCount in the TUI). */
export function attentionCount(sessions: Attentionish[]): number {
  return sessions.reduce((n, s) => (isAttention(s.status) ? n + 1 : n), 0);
}
