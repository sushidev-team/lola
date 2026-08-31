import { describe, expect, it } from "vitest";
import { SHEETS, isSheetName } from "./sheets";

describe("sheet vocabulary", () => {
  it("names every addressable sheet", () => {
    expect([...SHEETS]).toEqual(["filter", "connection", "view", "pane"]);
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
