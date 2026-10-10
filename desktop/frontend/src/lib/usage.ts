// Usage rendering for the daemon's token figures (internal/usage). The number
// shown is TOKENS — a subscription pays nothing per token, so a dollar amount
// would read as a bill nobody is sent; the list-price estimate survives only as
// a "~$" aside in tooltips. How HEAVY a session is comes from the daemon's
// ranking against the user's own finished sessions (a 4-step bar glyph), plus a
// flame while it burns tokens faster than past sessions ever did. Mirrors
// internal/tui/usage.go.
import type { UsageInfo, UsageStatus } from "@bindings/internal/protocol";

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
  return `heavier than ${Math.round(u.percentile ?? 0)}% of your last ${u.of} sessions`;
}

/** A session's token label; "" when nothing is known (absent is unknown, not 0). */
export function usageLabel(u: UsageInfo | null | undefined): string {
  return u ? fmtTokens(u.tokens) : "";
}

/** The tooltip that spells a session's figure out. */
export function usageTitle(u: UsageInfo): string {
  const lines = [
    `${fmtTokens(u.tokens)} tokens (${fmtTokens(u.todayTokens)} today)`,
    rankText(u),
    `~${fmtUSD(u.totalUsd)} at list price`,
  ];
  if (u.burning) lines.push(`Burning ${fmtTokens(u.tokensPerHour ?? 0)} tokens/h — faster than 90% of your sessions`);
  return lines.join("\n");
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

/** Its tooltip: per-project usage and limits, and why dispatch is held. */
export function spendTitle(u: UsageStatus): string {
  const lines = [
    `Tokens ${u.day}: ${fmtTokens(u.tokens)} (${fmtTokens(u.weighted)} weighted, ~${fmtUSD(u.todayUsd)} at list price)`,
  ];
  lines.push(
    u.budgetTokens
      ? `Daily limit ${fmtTokens(u.budgetTokens)} weighted tokens (budget.daily_tokens)`
      : "No daily limit",
  );
  for (const p of u.projects ?? []) {
    const pct = budgetPercent(p.weighted, p.budgetTokens);
    lines.push(`${p.name}: ${fmtTokens(p.tokens)}${pct >= 0 ? ` — ${pct}% of ${fmtTokens(p.budgetTokens ?? 0)}` : ""}`);
  }
  if (u.load?.busy) lines.push(`Dispatch held — ${u.load.busy}`);
  return lines.join("\n");
}
