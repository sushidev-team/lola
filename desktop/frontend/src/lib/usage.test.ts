import { describe, expect, it } from "vitest";
import type { UsageInfo } from "@bindings/internal/protocol";
import { budgetPercent, headerLabel, headerLevel, quotaLabel, untilShort, fmtTokens, fmtUSD, rankText, spendLabel, spendLevel, usageHeadline, usageLabel, usageRows } from "./usage";

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
    expect(usageHeadline(info())).toBe("46.7M tokens");
    const rows = usageRows(info({ burning: true, tokensPerHour: 12_300_000, percentile: 82, of: 40 }));
    expect(rows.map((r) => r.label)).toEqual(["Today", "Size", "Burning", "Estimate"]);
    expect(rows[1]).toMatchObject({ value: "heavier than 82%", note: "of your last 40 sessions" });
    expect(rows[2]).toMatchObject({ value: "12.3M tokens/h", tone: "hot" });
    expect(rows[3]).toMatchObject({ value: "~$18.33", note: "at list price" });
    const codex = usageRows(info({ agent: "codex", totalUsd: 0, percentile: 50, of: 12 }));
    expect(codex.find((r) => r.label === "Size")?.note).toBe("of your last 12 Codex sessions");
    expect(codex.some((r) => r.label === "Estimate")).toBe(false);
  });

  it("drops rows an older daemon does not send, never rendering undefined", () => {
    const old = { tokens: 48_200_000, totalUsd: 20.97, todayUsd: 1 } as unknown as UsageInfo;
    const rows = usageRows(old);
    expect(rows.map((r) => r.label)).toEqual(["Size", "Estimate"]);
    expect(JSON.stringify(rows)).not.toContain("undefined");
  });

  it("grades today's weighted usage against the limit", () => {
    const base = { day: "2026-10-09", tokens: 46_700_000, todayUsd: 3 };
    expect(budgetPercent(5, undefined)).toBe(-1);
    expect(spendLevel({ ...base, weighted: 10 })).toBe("ok");
    expect(spendLevel({ ...base, weighted: 80, budgetTokens: 100 })).toBe("near");
    expect(spendLevel({ ...base, weighted: 100, budgetTokens: 100 })).toBe("over");
    expect(spendLabel({ ...base, weighted: 38, budgetTokens: 100 })).toBe("46.7M today · 38% of budget");
    expect(spendLabel({ ...base, weighted: 38 })).toBe("46.7M today");
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

  it("formats the time until a reset", () => {
    expect(untilShort("2026-10-10T12:10:00Z", now)).toBe("in 2h 10m");
    expect(untilShort("2026-10-09T12:10:00Z", now)).toBe("");
  });
});
