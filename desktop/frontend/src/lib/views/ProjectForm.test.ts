import { describe, it, expect } from "vitest";
import { linesToText, textToLines } from "./ProjectForm.svelte";

describe("linesToText", () => {
  it("joins a config array one entry per line", () => {
    expect(linesToText([".env", "node_modules"])).toBe(".env\nnode_modules");
  });
  it("treats undefined/empty as an empty string", () => {
    expect(linesToText(undefined)).toBe("");
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
