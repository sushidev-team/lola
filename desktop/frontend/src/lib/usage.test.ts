import { describe, expect, it } from "vitest";
import type { UsageInfo } from "@bindings/internal/protocol";
import { budgetPercent, headerLabel, headerLevel, headerTitle, quotaLabel, untilShort, fmtTokens, fmtUSD, rankText, spendLabel, spendLevel, spendTitle, usageLabel, usageTitle } from "./usage";

const info = (o: Partial<UsageInfo> = {}): UsageInfo => ({
  tokens: 46_700_420,
  todayTokens: 2_100_000,
  totalUsd: 18.333,
  todayUsd: 1,
  level: 2,
  ...o,
});

describe("usage formatting", () => {
  it("formats dollars and tokens", () => {
    expect(fmtUSD(18.333)).toBe("$18.33");
    expect(fmtUSD(250.4)).toBe("$250");
    expect(fmtTokens(950)).toBe("950");
    expect(fmtTokens(12_300)).toBe("12.3k");
    expect(fmtTokens(46_700_420)).toBe("46.7M");
  });

  it("renders unknown usage as nothing, never 0", () => {
    expect(usageLabel(null)).toBe("");
    expect(usageLabel(info())).toBe("46.7M");
  });

  it("says the rank in words, against history or the fallback", () => {
    expect(rankText(info({ percentile: 82, of: 40 }))).toBe("heavier than 82% of your last 40 sessions");
    expect(rankText(info({ level: 3 }))).toContain("very heavy (too little history");
    const t = usageTitle(info({ burning: true, tokensPerHour: 12_300_000 }));
    expect(t).toContain("~$18.33 at list price");
    expect(t).toContain("Burning 12.3M tokens/h");
    const codex = usageTitle(info({ agent: "codex", totalUsd: 0, percentile: 50, of: 12 }));
    expect(codex).toContain("of your last 12 codex sessions");
    expect(codex).not.toContain("$");
  });

  it("grades today's weighted usage against the limit", () => {
    const base = { day: "2026-10-09", tokens: 46_700_000, todayUsd: 3 };
    expect(budgetPercent(5, undefined)).toBe(-1);
    expect(spendLevel({ ...base, weighted: 10 })).toBe("ok");
    expect(spendLevel({ ...base, weighted: 80, budgetTokens: 100 })).toBe("near");
    expect(spendLevel({ ...base, weighted: 100, budgetTokens: 100 })).toBe("over");
    expect(spendLabel({ ...base, weighted: 38, budgetTokens: 100 })).toBe("46.7M today · 38% of budget");
    expect(spendLabel({ ...base, weighted: 38 })).toBe("46.7M today");
    const title = spendTitle({
      ...base,
      weighted: 1_000_000,
      projects: [{ name: "p", tokens: 3_000_000, weighted: 1_000_000, budgetTokens: 4_000_000 }],
      load: { load1: 20, cpus: 8, freeMemPercent: 50, busy: "machine busy: load 20" },
    });
    expect(title).toContain("p: 3.0M — 25% of 4.0M");
    expect(title).toContain("Dispatch held — machine busy");
  });
});

describe("subscription limits", () => {
  const now = Date.parse("2026-10-10T10:00:00Z");
  const claude = {
    agent: "claude",
    at: "2026-10-10T09:48:00Z",
    windows: [
      { label: "5h", usedPercent: 42.4, resetsAt: "2026-10-10T12:10:00Z" },
      { label: "7d", usedPercent: 18, resetsAt: "2026-10-13T14:00:00Z" },
    ],
  };
  const codex = { agent: "codex", plan: "pro", at: "2026-10-10T08:14:34Z", windows: [{ label: "7d", usedPercent: 4, resetsAt: "2026-10-15T20:29:36Z" }] };
  const base = { day: "2026-10-10", tokens: 46_700_000, weighted: 0, todayUsd: 3 };

  it("leads the header with the limits, falling back to tokens", () => {
    expect(quotaLabel(claude)).toBe("Claude 5h 42% · 7d 18%");
    expect(headerLabel({ ...base, quotas: [claude, codex] })).toBe("Claude 5h 42% · 7d 18% · Codex 7d 4%");
    expect(headerLabel({ ...base, quotas: [codex], weighted: 50, budgetTokens: 100 })).toBe("Codex 7d 4% · 50% of budget");
    expect(headerLabel(base)).toBe("46.7M today");
  });

  it("grades by the fullest limit or the budget", () => {
    expect(headerLevel({ ...base, quotas: [claude] })).toBe("ok");
    const full = { ...claude, windows: [{ ...claude.windows[0], usedPercent: 85 }] };
    expect(headerLevel({ ...base, quotas: [full] })).toBe("near");
    expect(headerLevel({ ...base, quotas: [claude], weighted: 100, budgetTokens: 100 })).toBe("over");
  });

  it("explains resets and freshness in the tooltip", () => {
    expect(untilShort("2026-10-10T12:10:00Z", now)).toBe("in 2h 10m");
    expect(untilShort("2026-10-09T12:10:00Z", now)).toBe("");
    const t = headerTitle({ ...base, quotas: [claude, codex] }, now);
    expect(t).toContain("Claude — as of 12m ago");
    expect(t).toContain("5h: 42% used, resets in 2h 10m");
    expect(t).toContain("Codex (pro)");
    expect(t).toContain("Tokens 2026-10-10: 46.7M");
  });
});
