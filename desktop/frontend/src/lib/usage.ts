// Usage rendering for the daemon's token figures (internal/usage). The number
// shown is TOKENS — a subscription pays nothing per token, so a dollar amount
// would read as a bill nobody is sent; the list-price estimate survives only as
// a "~$" aside in tooltips. How HEAVY a session is comes from the daemon's
// ranking against the user's own finished sessions (a 4-step bar glyph), plus a
// flame while it burns tokens faster than past sessions ever did. Mirrors
// internal/tui/usage.go.
import type { QuotaInfo, QuotaWindow, UsageInfo, UsageStatus } from "@bindings/internal/protocol";

/** Compact dollars: cents below $100, whole dollars above. */
export function fmtUSD(v: number): string {
  return v >= 100 ? `$${Math.round(v)}` : `$${v.toFixed(2)}`;
}

/** Token count as 950, 12.3k, 4.5M. */
export function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1e6).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1e3).toFixed(1)}k`;
  return String(n);
}

/** The size glyph's step, clamped to 0..3 (usage.LevelLight … LevelTop). */
export function usageLevel(u: UsageInfo): number {
  return Math.max(0, Math.min(3, u.level ?? 0));
}

const LEVEL_WORDS = ["light", "moderate", "heavy", "very heavy"];

/** The rank in words: against history, or the fallback's word without it. */
export function rankText(u: UsageInfo): string {
  if (!u.of) return `${LEVEL_WORDS[usageLevel(u)]} (too little history to compare yet)`;
  const kind = u.agent === "codex" ? "codex " : ""; // ranked against codex sessions only
  return `heavier than ${Math.round(u.percentile ?? 0)}% of your last ${u.of} ${kind}sessions`;
}

/** A session's token label; "" when nothing is known (absent is unknown, not 0). */
export function usageLabel(u: UsageInfo | null | undefined): string {
  return u ? fmtTokens(u.tokens) : "";
}

/** The hover card's headline: "48.2M tokens". */
export function usageHeadline(u: UsageInfo): string {
  return `${fmtTokens(u.tokens)} tokens`;
}

export interface UsageRow {
  label: string;
  value: string;
  note?: string;
  tone?: "hot";
}

const AGENT_LABEL: Record<string, string> = { claude: "Claude", codex: "Codex", opencode: "OpenCode" };

/**
 * The hover card's rows. Every field is optional on the wire — an older
 * daemon sends none of the newer ones — so a missing value drops its row
 * rather than rendering "undefined".
 */
export function usageRows(u: UsageInfo): UsageRow[] {
  const rows: UsageRow[] = [];
  if (typeof u.todayTokens === "number") rows.push({ label: "Today", value: fmtTokens(u.todayTokens) });
  if (u.of) {
    const kind = u.agent && u.agent !== "claude" ? `${AGENT_LABEL[u.agent] ?? u.agent} ` : "";
    rows.push({
      label: "Size",
      value: `heavier than ${Math.round(u.percentile ?? 0)}%`,
      note: `of your last ${u.of} ${kind}sessions`,
      tone: usageLevel(u) === 3 ? "hot" : undefined,
    });
  } else {
    rows.push({ label: "Size", value: LEVEL_WORDS[usageLevel(u)], note: "· too little history to compare", tone: usageLevel(u) === 3 ? "hot" : undefined });
  }
  if (u.burning) {
    rows.push({ label: "Burning", value: `${fmtTokens(u.tokensPerHour ?? 0)} tokens/h`, note: "faster than 90% of your sessions", tone: "hot" });
  }
  // codex has no price lola trusts, and opencode's is 0 for free models.
  if (u.totalUsd > 0) rows.push({ label: "Estimate", value: `~${fmtUSD(u.totalUsd)}`, note: u.agent === "opencode" ? "opencode's own price" : "at list price" });
  if (u.agent) rows.push({ label: "Agent", value: AGENT_LABEL[u.agent] ?? u.agent });
  return rows;
}

/** used/budget as a whole percentage; -1 without a budget. */
export function budgetPercent(used: number, budget: number | undefined): number {
  return budget && budget > 0 ? Math.floor((100 * used) / budget) : -1;
}

export type SpendLevel = "ok" | "near" | "over";

/** Today's weighted usage against the global limit: near from 80%, over once reached. */
export function spendLevel(u: UsageStatus): SpendLevel {
  const pct = budgetPercent(u.weighted, u.budgetTokens);
  if (pct >= 100) return "over";
  if (pct >= 80) return "near";
  return "ok";
}

/**
 * The top bar's daily chip: "46.7M today", plus "· 38% of budget" with a
 * limit. A PERCENTAGE because the budget counts weighted tokens, a different
 * number from the raw count beside it.
 */
export function spendLabel(u: UsageStatus): string {
  const pct = budgetPercent(u.weighted, u.budgetTokens);
  return `${fmtTokens(u.tokens)} today${pct >= 0 ? ` · ${pct}% of budget` : ""}`;
}

// ---- subscription limits (internal/quota) ---------------------------------
// The header leads with how much of each agent's SUBSCRIPTION is used — the
// number a subscriber actually budgets by — and falls back to today's tokens
// only when no agent has reported one (an API-key user, or nothing ran yet).

export const AGENT_NAMES: Record<string, string> = { claude: "Claude", codex: "Codex" };

/** The text beside the bars: the budget share, "" without a budget. */
export function budgetLabel(u: UsageStatus): string {
  const pct = budgetPercent(u.weighted, u.budgetTokens);
  return pct >= 0 ? `${pct}% of budget` : "";
}

/** "Claude 5h 42% · 7d 18%". */
export function quotaLabel(q: QuotaInfo): string {
  const name = AGENT_NAMES[q.agent] ?? q.agent;
  return `${name} ${(q.windows ?? []).map((w) => `${w.label} ${Math.round(w.usedPercent)}%`).join(" · ")}`;
}

/** A compact "in 2h 10m" / "in 3d 4h" until iso; "" when unknown or past. */
export function untilShort(iso: string, now: number = Date.now()): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t) || t <= now) return "";
  return `in ${durationShort(t - now)}`;
}

/** The header chip: subscription limits when known, else today's tokens; a budget always shows. */
export function headerLabel(u: UsageStatus): string {
  if (!u.quotas?.length) return spendLabel(u);
  const pct = budgetPercent(u.weighted, u.budgetTokens);
  return u.quotas.map(quotaLabel).join(" · ") + (pct >= 0 ? ` · ${pct}% of budget` : "");
}

/** The header chip's level: the worse of the worst limit (by pace) and the budget. */
export function headerLevel(u: UsageStatus, now: number = Date.now()): SpendLevel {
  let q: SpendLevel = "ok";
  for (const x of u.quotas ?? [])
    for (const w of x.windows ?? []) {
      const t = windowPace(w, now).tone;
      if (t === "bad") q = "over";
      else if (t === "warn" && q === "ok") q = "near";
    }
  const b = spendLevel(u);
  return q === "over" || b === "over" ? "over" : q === "near" || b === "near" ? "near" : "ok";
}

// ---- pace -----------------------------------------------------------------
// A percentage alone misleads: 30% of a weekly limit one day in is a problem,
// 30% six days in is not. So each window is also judged by PACE — usage
// against the share of the window already elapsed — and projected to its
// reset. The tone then reads "will this run out before it resets?", not
// merely "is it nearly full?".

/** A window label's length in minutes ("5h" → 300, "7d" → 10080); null if unknown. */
export function windowMinutes(label: string): number | null {
  const m = /^(\d+)([mhd])$/.exec(label);
  if (!m) return null;
  return Number(m[1]) * ({ m: 1, h: 60, d: 1440 } as const)[m[2] as "m" | "h" | "d"];
}

/** A human name for a window: "5h" → "5-hour", "7d" → "Weekly". */
export function windowName(label: string): string {
  const named: Record<string, string> = { "7d": "Weekly", "1d": "Daily", "30d": "Monthly", spend: "Spend limit" };
  if (named[label]) return named[label];
  const m = /^(\d+)([mhd])$/.exec(label);
  return m ? `${m[1]}-${{ m: "minute", h: "hour", d: "day" }[m[2] as "m" | "h" | "d"]}` : label;
}

export interface Pace {
  /** Share of the window already elapsed, 0..1; null when unknown. */
  elapsed: number | null;
  /** Usage projected to the reset at the current pace; null too early to tell. */
  projected: number | null;
  /** Time until the limit is hit at the current pace, ms; null unless before the reset. */
  fullIn: number | null;
  tone: "ok" | "warn" | "bad";
}

/** Below this share of a window elapsed, a projection is noise. */
const MIN_ELAPSED = 0.1;

export function windowPace(w: QuotaWindow, now: number = Date.now()): Pace {
  const mins = windowMinutes(w.label);
  const reset = Date.parse(w.resetsAt);
  let elapsed: number | null = null;
  let projected: number | null = null;
  let fullIn: number | null = null;
  if (mins && !Number.isNaN(reset)) {
    const len = mins * 60000;
    elapsed = Math.min(1, Math.max(0, (now - (reset - len)) / len));
    if (elapsed >= MIN_ELAPSED && w.usedPercent > 0) {
      projected = w.usedPercent / elapsed;
      const rate = w.usedPercent / (elapsed * len); // % per ms
      const t = (100 - w.usedPercent) / rate;
      if (w.usedPercent < 100 && now + t < reset) fullIn = t;
    }
  }
  const tone = w.usedPercent >= 95 ? "bad" : w.usedPercent >= 80 || fullIn !== null ? "warn" : "ok";
  return { elapsed, projected, fullIn, tone };
}

/** A compact duration: "45m", "2h 10m", "3d 4h". */
export function durationShort(ms: number): string {
  const m = Math.max(0, Math.round(ms / 60000));
  if (m < 60) return `${m}m`;
  if (m < 1440) return `${Math.floor(m / 60)}h ${m % 60}m`;
  return `${Math.floor(m / 1440)}d ${Math.floor((m % 1440) / 60)}h`;
}

/** The card's pace note: "full in ~2d 3h at this pace" or "on pace for ~18%". */
export function paceNote(p: Pace): string {
  if (p.fullIn !== null) return `full in ~${durationShort(p.fullIn)} at this pace`;
  if (p.projected !== null) return `on pace for ~${Math.round(p.projected)}%`;
  return "";
}
