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

// Whether a compact surface (list row, kanban card) should still draw the chip.
// The plan is the agent's road TO a pull request: once one exists, the PR badge
// is the more advanced — and factual — answer to "how far along is this", and a
// finished 5/5 bar beside it only repeats the past. A blocker is the exception:
// it is the one part of the report a human must act on, at any stage.
export function chipRelevant(b: BoardInfo | null | undefined, prNumber: number): b is BoardInfo {
  return hasChip(b) && (!!b.blocked || !(prNumber > 0));
}

// A check's state as a BoardMarker kind and as words. Unknown states (a daemon
// newer than this build) read as running: still in flight, never a false pass.
export function checkKind(state: string): "pass" | "fail" | "running" {
  return state === "pass" || state === "fail" ? state : "running";
}
export const CHECK_WORD: Record<string, string> = { pass: "passed", fail: "failed", running: "running" };

// Phase → the dot inside the phase chip. Literal classes, same reason as above.
const PHASE_DOT: Record<string, string> = {
  planning: "bg-faint",
  investigating: "bg-magenta",
  implementing: "bg-info",
  testing: "bg-warn",
  reviewing: "bg-accent",
  polishing: "bg-accent",
  done: "bg-good",
};

export function phaseDot(phase: string): string {
  return PHASE_DOT[phase] ?? "bg-faint";
}

// A compact "how long ago" for a wire timestamp ("", "now", "4m", "2h", "3d").
// Client-side because blockedAt has no daemon-formatted twin, and a blocker's
// age is the part that tells a human how long the agent has been waiting.
export function agoShort(iso: string | undefined, now: number = Date.now()): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 60) return "now";
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

// The claim audit (internal/claimaudit): lola's own comparison of the board's
// pass claims with the agent's transcript and the PR's CI. Display-only like
// the board — a mismatch is a warning for a human, never an input to
// attention(), sorting or triage. `mismatches` are lola-authored sentences.
export function hasMismatch(b: BoardInfo | null | undefined): b is BoardInfo {
  return !!b?.mismatches?.length;
}

// A check's audit verdict as a short tag, "" when there is nothing worth saying
// (not audited, or an unknown verdict from a daemon newer than this build).
export const EVIDENCE_TAG: Record<string, { word: string; cls: string }> = {
  verified: { word: "verified", cls: "text-good" },
  ran: { word: "ran", cls: "text-faint" },
  unverified: { word: "no run found", cls: "text-warn" },
  contradicted: { word: "last run failed", cls: "text-bad" },
};
