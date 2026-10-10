import { describe, expect, it } from "vitest";
import { costLabel, fmtTokens, fmtUSD, spendLabel, spendLevel, spendTitle } from "./usage";

describe("usage formatting", () => {
  it("formats dollars and tokens", () => {
    expect(fmtUSD(18.333)).toBe("$18.33");
    expect(fmtUSD(250.4)).toBe("$250");
    expect(fmtTokens(950)).toBe("950");
    expect(fmtTokens(12_300)).toBe("12.3k");
    expect(fmtTokens(46_700_420)).toBe("46.7M");
  });

  it("renders an unknown cost as nothing, never $0", () => {
    expect(costLabel(null)).toBe("");
    expect(costLabel({ totalUsd: 1.5, todayUsd: 1, tokens: 10 })).toBe("~$1.50");
  });

  it("grades today's spend against the limit", () => {
    const base = { day: "2026-10-09", tokens: 0 };
    expect(spendLevel({ ...base, todayUsd: 10 })).toBe("ok");
    expect(spendLevel({ ...base, todayUsd: 41, budgetUsd: 50 })).toBe("near");
    expect(spendLevel({ ...base, todayUsd: 50, budgetUsd: 50 })).toBe("over");
    expect(spendLabel({ ...base, todayUsd: 12.34, budgetUsd: 50 })).toBe("~$12.34 / $50.00 today");
    const title = spendTitle({
      ...base,
      todayUsd: 3,
      projects: [{ name: "p", todayUsd: 3, budgetUsd: 5 }],
      load: { load1: 20, cpus: 8, freeMemPercent: 50, busy: "machine busy: load 20" },
    });
    expect(title).toContain("p: ~$3.00 / $5.00");
    expect(title).toContain("Dispatch held — machine busy");
  });
});
