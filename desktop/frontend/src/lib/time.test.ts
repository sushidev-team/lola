import { describe, expect, it } from "vitest";
import { agoPhrase, exactTime, fluidAgo, nextTickIn } from "./time";

const now = Date.parse("2026-10-11T12:00:00Z");
const ago = (ms: number) => new Date(now - ms).toISOString();
const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;

describe("fluidAgo", () => {
  it("uses one unit, as coarse as the age allows", () => {
    expect(fluidAgo(ago(30_000), now).text).toBe("now");
    expect(fluidAgo(ago(5 * MIN), now)).toEqual({ text: "5m", rel: true });
    // The screenshot's "10h51m": the minutes are dropped.
    expect(fluidAgo(ago(10 * HOUR + 51 * MIN), now)).toEqual({ text: "10h", rel: true });
    expect(fluidAgo(ago(2 * DAY + 3 * HOUR), now)).toEqual({ text: "2d", rel: true });
  });
  it("switches to a calendar date after a week, with the year only when it differs", () => {
    const f = fluidAgo(ago(10 * DAY), now);
    expect(f.rel).toBe(false);
    expect(f.text).not.toMatch(/\d{4}/);
    expect(fluidAgo("2025-03-04T10:00:00Z", now).text).toMatch(/2025/);
  });
  it("renders nothing for unknown or Go-zero times and clamps the future", () => {
    expect(fluidAgo(undefined, now).text).toBe("");
    expect(fluidAgo("garbage", now).text).toBe("");
    expect(fluidAgo("0001-01-01T00:00:00Z", now).text).toBe("");
    expect(fluidAgo(ago(-5 * MIN), now).text).toBe("now");
  });
});

describe("agoPhrase / exactTime / nextTickIn", () => {
  it("phrases relative units with ago, dates without", () => {
    expect(agoPhrase(ago(10), now)).toBe("just now");
    expect(agoPhrase(ago(3 * HOUR), now)).toBe("3h ago");
    expect(agoPhrase(ago(30 * DAY), now)).not.toMatch(/ago/);
    expect(agoPhrase(undefined, now)).toBe("");
  });
  it("keeps the exact time for the hover card", () => {
    expect(exactTime(ago(0))).toMatch(/2026/);
    expect(exactTime("")).toBe("");
  });
  it("wakes when the text can next change", () => {
    expect(nextTickIn(ago(90_000), now)).toBeLessThanOrEqual(MIN);
    expect(nextTickIn(ago(2 * HOUR + 10 * MIN), now)).toBeGreaterThan(40 * MIN);
  });
});
