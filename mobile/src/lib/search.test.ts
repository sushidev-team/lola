import { describe, it, expect } from "vitest";
import { matchesQuery, searchSessions, type Searchable } from "./search";

const s = (over: Partial<Searchable> = {}): Searchable => ({
  id: "lola-fe-42",
  issue: "ENG-412",
  title: "Fix the accessory bar",
  project: "lola-fe",
  branch: "feature/eng-412-bar",
  ...over,
});

describe("matchesQuery", () => {
  it("matches everything on an empty query", () => {
    expect(matchesQuery(s(), "")).toBe(true);
    expect(matchesQuery(s(), "   ")).toBe(true);
  });

  it("matches the issue key however it is typed", () => {
    expect(matchesQuery(s(), "eng-412")).toBe(true);
    expect(matchesQuery(s(), "ENG")).toBe(true);
    expect(matchesQuery(s(), "412")).toBe(true);
  });

  it("matches the title, the project and the branch", () => {
    expect(matchesQuery(s(), "accessory")).toBe(true);
    expect(matchesQuery(s(), "lola-fe")).toBe(true);
    expect(matchesQuery(s(), "feature/")).toBe(true);
  });

  it("matches a project's DISPLAY label too", () => {
    // A project has two names - Name is identity, Label is display - and a
    // person may remember either.
    expect(matchesQuery(s({ project: "okane" }), "Money App", "Money App")).toBe(true);
  });

  it("does not let a match straddle two fields", () => {
    // "bar" ends the title and "lola-fe" starts the project; joined naively they
    // would answer a search for "barlola".
    expect(matchesQuery(s(), "barlola")).toBe(false);
    expect(matchesQuery(s(), "bar lola")).toBe(false);
  });

  it("misses what it should miss", () => {
    expect(matchesQuery(s(), "zzz")).toBe(false);
  });
});

describe("searchSessions", () => {
  const list = [s(), s({ id: "b", issue: "ENG-9", title: "Something else", project: "okane" })];

  it("preserves order, because the order already means something", () => {
    // sortSessions puts what needs a human first; re-ranking by match quality
    // would move rows under a thumb while it is being read.
    expect(searchSessions(list, "eng").map((x) => x.id)).toEqual(["lola-fe-42", "b"]);
  });

  it("passes everything through for an empty query", () => {
    expect(searchSessions(list, "")).toHaveLength(2);
  });

  it("uses the label resolver it was given", () => {
    const found = searchSessions(list, "money", (p) => (p === "okane" ? "Money App" : ""));
    expect(found.map((x) => x.id)).toEqual(["b"]);
  });
});
