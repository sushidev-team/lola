import { describe, expect, it } from "vitest";
import { SHEETS, isSheetName } from "./sheets";

describe("sheet vocabulary", () => {
  it("names every addressable sheet", () => {
    // The list is pinned rather than merely non-empty because its whole value
    // is that a development link can address each entry: a sheet dropped from
    // it still opens by tap and silently stops being photographable, which is
    // the failure this vocabulary exists to prevent and the one nothing else
    // would catch.
    expect([...SHEETS]).toEqual(["filter", "pane", "menu"]);
  });

  it("fails closed on anything else", () => {
    // A link written against a later build, or a typo, must open nothing at
    // all rather than something the caller did not ask for.
    expect(isSheetName("filter")).toBe(true);
    expect(isSheetName("")).toBe(false);
    expect(isSheetName("Filter")).toBe(false);
    expect(isSheetName("wharrgarbl")).toBe(false);
  });
});
