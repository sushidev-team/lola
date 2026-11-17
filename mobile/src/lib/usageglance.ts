// The usage and health glance on Settings: what the Mac's `status` topic says about today's
// tokens, subscription windows, machine load and the daemon's own health, as plain rows.
//
// Invariants:
//  - Tokens, never dollars (AGENTS.md usage rule): the desktop's `fmtTokens` and window words
//    are reused, and `todayUsd` is not read.
//  - Display only: nothing here feeds a send, a gate or a hold.
//  - Absent is unknown, not zero: an older daemon sends no `usage`, and then there are no usage
//    rows. A health row appears only on trouble, so a healthy Mac adds nothing to the screen.

import type { StatusData } from "@bindings/internal/protocol";
import { AGENT_NAMES, budgetPercent, fmtTokens, paceNote, untilShort, windowName, windowPace } from "$lib/usage";
import { copy, fill } from "./copy";

export interface GlanceRow {
  key: string;
  label: string;
  value: string;
  detail?: string;
  /** "danger" draws the row in the bad colour. */
  tone?: "default" | "danger";
}

export interface Glance {
  usage: GlanceRow[];
  health: GlanceRow[];
}

/** Today's tokens, each subscription window, and the load hold, in that order. */
function usageRows(status: StatusData, now: number): GlanceRow[] {
  const u = status.usage;
  if (!u) return [];
  const rows: GlanceRow[] = [];
  const pct = budgetPercent(u.weighted, u.budgetTokens);
  rows.push({
    key: "today",
    label: copy.usage.today,
    value: fill(copy.usage.tokens, { n: fmtTokens(u.tokens) }),
    detail: pct >= 0 ? fill(copy.usage.ofBudget, { pct }) : undefined,
    tone: pct >= 100 ? "danger" : "default",
  });
  for (const q of u.quotas ?? []) {
    const agent = AGENT_NAMES[q.agent] ?? q.agent;
    for (const w of q.windows ?? []) {
      const until = untilShort(w.resetsAt, now);
      const parts = [until ? fill(copy.usage.resets, { when: until }) : "", paceNote(windowPace(w, now)), q.stale ? copy.usage.stale : ""];
      rows.push({
        key: `quota:${q.agent}:${w.label}`,
        label: `${agent} ${windowName(w.label).toLowerCase()}`,
        value: `${Math.round(w.usedPercent)}%`,
        detail: parts.filter((p) => p !== "").join(" · ") || undefined,
        tone: windowPace(w, now).tone === "bad" ? "danger" : "default",
      });
    }
  }
  if (u.load?.busy) {
    rows.push({ key: "load", label: copy.usage.load, value: copy.usage.busy, detail: u.load.busy });
  }
  return rows;
}

/** One row per broken part of the daemon; none when it is healthy or its status is unknown. */
function healthRows(status: StatusData): GlanceRow[] {
  const rows: GlanceRow[] = [];
  if (!status.runtimeOk) {
    rows.push({
      key: "runtime",
      label: copy.usage.runtime,
      value: copy.usage.runtimeDown,
      detail: status.runtimeErr || undefined,
      tone: "danger",
    });
  }
  if (!status.linearOk) {
    rows.push({ key: "linear", label: copy.usage.linear, value: copy.usage.linearDown, tone: "danger" });
  }
  return rows;
}

/** The glance for a status; empty for an unknown one. */
export function usageGlance(status: StatusData | null | undefined, now: number = Date.now()): Glance {
  if (!status) return { usage: [], health: [] };
  return { usage: usageRows(status, now), health: healthRows(status) };
}
