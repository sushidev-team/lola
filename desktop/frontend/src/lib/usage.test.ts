import { describe, expect, it } from "vitest";
import type { UsageInfo } from "@bindings/internal/protocol";
import { budgetPercent, fmtTokens, fmtUSD, rankText, spendLabel, spendLevel, spendTitle, usageLabel, usageTitle } from "./usage";

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
