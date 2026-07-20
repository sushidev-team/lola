import { describe, it, expect } from "vitest";
import { linesToText, textToLines, splitLines, cleanLines } from "./lines";

describe("linesToText", () => {
  it("joins a config array one entry per line", () => {
    expect(linesToText([".env", "node_modules"])).toBe(".env\nnode_modules");
  });
  it("treats null/undefined/empty as an empty string", () => {
    expect(linesToText(undefined)).toBe("");
    expect(linesToText(null)).toBe("");
    expect(linesToText([])).toBe("");
  });
});

describe("textToLines", () => {
  it("splits on newlines, trims, and drops blank lines", () => {
    expect(textToLines("  npm install \n\n make build  \n")).toEqual(["npm install", "make build"]);
  });
  it("returns an empty array for blank input", () => {
    expect(textToLines("")).toEqual([]);
    expect(textToLines("   \n \n")).toEqual([]);
  });
  it("round-trips with linesToText", () => {
    const arr = ["API_URL=http://localhost", "KEY=value"];
    expect(textToLines(linesToText(arr))).toEqual(arr);
  });
});

describe("splitLines", () => {
  // The editing path must be lossless: joinLines(splitLines(v)) === v, so a
  // trailing newline mid-edit doesn't get eaten under the cursor.
  it("keeps blanks and whitespace so an in-progress edit survives", () => {
    expect(splitLines("a\n\n b ")).toEqual(["a", "", " b "]);
    expect(linesToText(splitLines("a\n\n b "))).toBe("a\n\n b ");
  });
});

describe("cleanLines", () => {
  it("trims and drops blanks on the way to config", () => {
    expect(cleanLines(["  .env ", "", "  ", "node_modules"])).toEqual([".env", "node_modules"]);
  });
  it("tolerates the null a Go nil slice arrives as", () => {
    expect(cleanLines(null)).toEqual([]);
  });
});
