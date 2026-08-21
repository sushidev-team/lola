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
    // `dead` used to return Tailwind's built-in white here, on the premise that
    // the caller always paints it on a bad-colored fill. Only pillClasses does —
    // Rail and SessionsKanban print statusText bare on a panel, where white was
    // 1.14:1 on latte. It is a bad-family status, so it takes the bad-family
    // color; the pill supplies its own on-fill foreground.
    case "dead":
      return "text-bad";
    case "approved":
      return "text-good";
    case "needs_input":
      return "text-orange";
    case "merged":
    case "session_ended":
    case "idle":
    case "closed":
    case "shell":
    case "orphaned":
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
      // `dead` is the one plain status that still gets a solid fill, so it is
      // the one plain status that needs an on-fill foreground. --color-on-bad
      // is onFill(f, red), the same measured rule the urgent/broken pills use.
      // What it replaces was Tailwind's built-in white — the one foreground no
      // flavor could override, and 2.32:1 on the default Mocha.
      return status === "dead" ? "bg-bad text-on-bad" : statusText(status);
  }
}

/**
 * Human label for a status (or agent-axis / interpreted) word — statusLabel
 * in the TUI, kept identical. Every raw identifier gets a readable spelling
 * (a rendered "ci_failed" reads like a translation placeholder, not a badge);
 * the fallback de-underscores so an unmapped future word can never leak an
 * identifier into the UI. Display-only: control flow keys on the raw string.
 */
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
      return "ci running";
    case "ci_failed":
      return "ci failed";
    case "needs_input":
      return "needs you";
    case "waiting_input": // agent axis / interpreted overlay
      return "waiting";
    case "quota_limited":
      return "usage limit";
    default:
      return status.replaceAll("_", " ");
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
    merged: "mg",
    dead: "xx",
    session_ended: "en",
    idle: "..",
    draft: "df",
    closed: "cl",
    shell: "sh",
    orphaned: "or",
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
  { title: "Working", statuses: ["working", "ci_pending", "idle", "draft"] },
  { title: "Fixing", statuses: ["ci_failed", "changes_requested", "merge_conflict"] },
  { title: "In Review", statuses: ["review_pending", "approved"] },
  { title: "Done", statuses: ["merged", "closed", "dead", "session_ended"] },
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

/**
 * The complete rolled-up status vocabulary, mirroring Go's
 * internal/state.AllStatuses(). desktop/state_parity_test.go parses this
 * array and fails the build when the two lists drift — keep order identical.
 */
export const ALL_STATUSES: string[] = [
  "working", "idle", "needs_input", "session_ended", "dead",
  "shell", "orphaned",
  "draft", "ci_pending", "ci_failed", "merge_conflict",
  "changes_requested", "review_pending", "approved",
  "merged", "closed",
];

/**
 * ≤2-char glyph for the AGENT axis (agentBadge in the TUI). "" = no badge.
 * idle deliberately gets NONE: an idle agent under an open PR is the routine
 * resting state (turn done, PR in review) — badging it would stamp "·.." noise
 * on every parked row. Only the informative divergences show: still working,
 * or exited, while the PR state suggests otherwise.
 */
export function agentBadge(agentState: string): string {
  switch (agentState) {
    case "working":
    case "starting":
      return "wk";
    case "waiting_input":
      return "?!";
    case "exited":
      return "en";
    default:
      return "";
  }
}

/**
 * Whether to show the agent-axis badge next to the status pill: only when the
 * axes DIVERGE under an open PR (statusPillFor in the TUI) — "ci_pending ·wk"
 * says CI is running AND the agent is still typing.
 */
export function showAgentBadge(status: string, agentState?: string, delivery?: string): boolean {
  if (!agentState || !delivery || delivery === "none") return false;
  if (status === "merged" || status === "closed" || status === "dead" || status === "needs_input") return false;
  if (status === agentState) return false;
  return agentBadge(agentState) !== "";
}

export interface Attentionish {
  status: string;
}

/** Count of sessions needing a human (AttentionCount in the TUI). */
export function attentionCount(sessions: Attentionish[]): number {
  return sessions.reduce((n, s) => (isAttention(s.status) ? n + 1 : n), 0);
}
