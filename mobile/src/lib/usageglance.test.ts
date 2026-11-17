import { describe, expect, it } from "vitest";
import type { StatusData } from "@bindings/internal/protocol";
import { usageGlance } from "./usageglance";

const NOW = Date.parse("2026-11-15T12:00:00Z");
const inHours = (h: number): string => new Date(NOW + h * 3_600_000).toISOString();

const ok = (over: Partial<StatusData> = {}): StatusData => ({ runtimeOk: true, linearOk: true, polls: [], ...over });

describe("usageGlance", () => {
  it("is empty for an unknown status and for a daemon that sends no usage", () => {
    expect(usageGlance(null)).toEqual({ usage: [], health: [] });
    expect(usageGlance(ok(), NOW)).toEqual({ usage: [], health: [] });
  });

  it("shows today's tokens against the budget, in tokens", () => {
    const g = usageGlance(ok({ usage: { day: "d", tokens: 46_700_000, weighted: 38, todayUsd: 99, budgetTokens: 100 } }), NOW);
    expect(g.usage).toHaveLength(1);
    expect(g.usage[0]).toMatchObject({ label: "Today", value: "46.7M tokens", detail: "38% of budget", tone: "default" });
    expect(JSON.stringify(g)).not.toContain("$");
  });

  it("marks a spent budget", () => {
    const g = usageGlance(ok({ usage: { day: "d", tokens: 1000, weighted: 120, todayUsd: 0, budgetTokens: 100 } }), NOW);
    expect(g.usage[0].tone).toBe("danger");
    expect(usageGlance(ok({ usage: { day: "d", tokens: 1000, weighted: 120, todayUsd: 0 } }), NOW).usage[0].detail).toBeUndefined();
  });

  it("lists each subscription window with its reset time, and flags a stale reading", () => {
    const g = usageGlance(
      ok({
        usage: {
          day: "d",
          tokens: 5,
          weighted: 5,
          todayUsd: 0,
          quotas: [
            {
              agent: "claude",
              at: "x",
              stale: true,
              windows: [
                { label: "5h", usedPercent: 42.4, resetsAt: inHours(2) },
                { label: "7d", usedPercent: 97, resetsAt: inHours(48) },
              ],
            },
          ],
        },
      }),
      NOW,
    );
    const [, five, week] = g.usage;
    expect(five).toMatchObject({ label: "Claude 5-hour", value: "42%", tone: "default" });
    expect(five.detail).toContain("Resets in 2h 0m");
    expect(five.detail).toContain("Stale reading");
    expect(week).toMatchObject({ label: "Claude weekly", value: "97%", tone: "danger" });
  });

  it("adds a load row only while the Mac holds work back", () => {
    const base = { day: "d", tokens: 1, weighted: 1, todayUsd: 0 };
    const calm = usageGlance(ok({ usage: { ...base, load: { load1: 1, cpus: 8, freeMemPercent: 50 } } }), NOW);
    expect(calm.usage.map((r) => r.key)).toEqual(["today"]);
    const busy = usageGlance(ok({ usage: { ...base, load: { load1: 20, cpus: 8, freeMemPercent: 5, busy: "load 20 over 8 cores" } } }), NOW);
    expect(busy.usage.at(-1)).toMatchObject({ key: "load", value: "Busy", detail: "load 20 over 8 cores" });
  });

  it("reports runtime and Linear trouble, and nothing while healthy", () => {
    const g = usageGlance(ok({ runtimeOk: false, runtimeErr: "tmux not found", linearOk: false }), NOW);
    expect(g.health.map((r) => [r.key, r.value, r.detail, r.tone])).toEqual([
      ["runtime", "Not working", "tmux not found", "danger"],
      ["linear", "Not reachable", undefined, "danger"],
    ]);
    expect(usageGlance(ok(), NOW).health).toEqual([]);
  });
});
