import { describe, expect, it } from "vitest";
import { matchesSearch, searchMatch } from "./search";

describe("matchesSearch", () => {
  it("matches consecutive text in names, repositories, and paths", () => {
    expect(matchesSearch(" NORI ", "Nori Website")).toBe(true);
    expect(matchesSearch("nori", "Website", "team/nori-web")).toBe(true);
    expect(matchesSearch("nori", "Website", undefined, "/projects/nori")).toBe(true);
    expect(matchesSearch("nori website", "Nori", "team/website")).toBe(true);
  });

  it("keeps fuzzy matches and reports the letters that explain them", () => {
    expect(searchMatch("nori", "Northern River")).toEqual([[0, 1, 2, 10]]);
    expect(matchesSearch("zzz", "Northern River")).toBe(false);
  });

  it("never combines letters from different fields into a word", () => {
    expect(searchMatch("nori", "no", "ri")).toBeNull();
    expect(searchMatch("nori", "Cargopartner", "sushidev-team/cust-cargopartner-website", "/Volumes/Git/sushi/customers/cargopartner")).toBeNull();
    expect(searchMatch("nori", "Other project", "team/northern-river")).toEqual([[], [5, 6, 7, 15]]);
  });

  it("prefers a consecutive match and handles multiple words and Unicode", () => {
    expect(searchMatch("nori", "Northern River", "team/nori")).toEqual([[], [5, 6, 7, 8]]);
    expect(searchMatch("web nori", "Nori Website")).toEqual([[0, 1, 2, 3, 5, 6, 7]]);
    expect(searchMatch("nori", "🦊 Nori")).toEqual([[2, 3, 4, 5]]);
  });

  it("shows all results for an empty query", () => {
    expect(matchesSearch("  ", "Cargopartner")).toBe(true);
    expect(matchesSearch("", undefined)).toBe(true);
  });
});
