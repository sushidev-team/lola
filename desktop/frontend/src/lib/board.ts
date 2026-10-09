// The agent's self-report (`lola report …`, SessionInfo.board): pure helpers
// shared by every surface that draws it — the session sidebar, the list's Plan
// column, the kanban cards and AgentActivity (which mobile renders verbatim).
//
// The board is the agent's CLAIM about itself, display-only daemon-side and
// display-only here: nothing in this file may feed attention(), sorting, triage
// or any action. Its strings arrive sanitized (one line, no control characters),
// and are rendered as TEXT — never as markup, never as a link.

import type { BoardInfo } from "@bindings/internal/protocol";

export type { BoardInfo };

// A report older than this stops reading as current and fades. Agents forget to
// report; a confident stale "testing" is worse than none. Mirrors the TUI's
// boardStaleAfter.
export const BOARD_STALE_MS = 15 * 60 * 1000;

export function boardStale(b: BoardInfo | null | undefined, now: number = Date.now()): boolean {
  if (!b?.updatedAt) return true;
  const t = Date.parse(b.updatedAt);
  return Number.isNaN(t) || now - t > BOARD_STALE_MS;
}

// Phase → chip colours. Every class is a LITERAL so Tailwind's scanner sees it;
// an unknown phase (a daemon newer than this build) gets the neutral chip.
const PHASE_CHIP: Record<string, string> = {
  planning: "bg-pill-grey text-pill-grey-fg",
  investigating: "bg-pill-grey text-pill-grey-fg",
  implementing: "bg-pill-work text-pill-work-fg",
  testing: "bg-pill-work text-pill-work-fg",
  reviewing: "bg-pill-work text-pill-work-fg",
  polishing: "bg-pill-work text-pill-work-fg",
  done: "bg-pill-done text-pill-done-fg",
};

export function phaseChip(phase: string): string {
  return PHASE_CHIP[phase] ?? "bg-pill-grey text-pill-grey-fg";
}

// The count beside a bar: "2/5" for a plan-derived bar, "40%" for an explicit one.
export function progressText(b: BoardInfo): string {
  return b.progressDerived ? `${b.done}/${b.total}` : `${b.percent}%`;
}

// Whether the board has anything worth a compact chip (list cell, kanban card).
export function hasChip(b: BoardInfo | null | undefined): b is BoardInfo {
  return !!b && (!!b.blocked || !!b.hasProgress || !!b.phase);
}

export const CHECK_GLYPH: Record<string, string> = {
  pass: "✓",
  fail: "✗",
  running: "…",
};
export const CHECK_TEXT: Record<string, string> = {
  pass: "text-good",
  fail: "text-bad",
  running: "text-faint",
};
