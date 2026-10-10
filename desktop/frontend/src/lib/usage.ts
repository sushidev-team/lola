// Spend rendering for the daemon's usage figures (internal/usage). Every number
// is an ESTIMATE at list price — a subscription pays nothing per token — so
// each one carries a "~"; a bare "$18.33" would read as a bill. Mirrors
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

/** A session's cost cell; "" when nothing is known (absent is unknown, not $0). */
export function costLabel(u: UsageInfo | null | undefined): string {
  return u ? `~${fmtUSD(u.totalUsd)}` : "";
}

/** The tooltip that spells a session's figure out. */
export function costTitle(u: UsageInfo): string {
  return `estimated spend: ~${fmtUSD(u.totalUsd)} total, ~${fmtUSD(u.todayUsd)} today, ${fmtTokens(u.tokens)} tokens (list price)`;
}

export type SpendLevel = "ok" | "near" | "over";

/** Today's spend against the global limit: near from 80%, over once reached. */
export function spendLevel(u: UsageStatus): SpendLevel {
  if (!u.budgetUsd) return "ok";
  if (u.todayUsd >= u.budgetUsd) return "over";
  if (u.todayUsd >= 0.8 * u.budgetUsd) return "near";
  return "ok";
}

/** The top bar's daily chip: "~$12.34 today" or "~$12.34 / $50 today". */
export function spendLabel(u: UsageStatus): string {
  const limit = u.budgetUsd ? ` / ${fmtUSD(u.budgetUsd)}` : "";
  return `~${fmtUSD(u.todayUsd)}${limit} today`;
}

/** Its tooltip: per-project spend and limits, and why dispatch is held. */
export function spendTitle(u: UsageStatus): string {
  const lines = [`Estimated spend ${u.day} (list price): ~${fmtUSD(u.todayUsd)}, ${fmtTokens(u.tokens)} tokens`];
  lines.push(u.budgetUsd ? `Daily limit ${fmtUSD(u.budgetUsd)} (budget.daily_usd)` : "No daily limit");
  for (const p of u.projects ?? []) {
    lines.push(`${p.name}: ~${fmtUSD(p.todayUsd)}${p.budgetUsd ? ` / ${fmtUSD(p.budgetUsd)}` : ""}`);
  }
  if (u.load?.busy) lines.push(`Dispatch held — ${u.load.busy}`);
  return lines.join("\n");
}
