import { describe, it, expect } from "vitest";
import { deepEqual } from "./deepEqual";

describe("deepEqual", () => {
  it("compares primitives", () => {
    expect(deepEqual(1, 1)).toBe(true);
    expect(deepEqual("a", "a")).toBe(true);
    expect(deepEqual(true, false)).toBe(false);
    expect(deepEqual(1, "1")).toBe(false); // type mismatch, no coercion
    expect(deepEqual(null, null)).toBe(true);
    expect(deepEqual(null, {})).toBe(false);
  });

  it("compares nested objects regardless of key order", () => {
    expect(deepEqual({ a: 1, b: { c: [1, 2] } }, { b: { c: [1, 2] }, a: 1 })).toBe(true);
    expect(deepEqual({ a: 1 }, { a: 1, b: 2 })).toBe(false); // extra key
    expect(deepEqual({ a: 1, b: 2 }, { a: 1 })).toBe(false); // missing key
    expect(deepEqual({ a: 1 }, { a: 2 })).toBe(false);
  });

  it("treats array order as significant", () => {
    expect(deepEqual(["priority", "createdAt"], ["priority", "createdAt"])).toBe(true);
    // A reordered priority-sort chain is a different value, not an equal one.
    expect(deepEqual(["priority", "createdAt"], ["createdAt", "priority"])).toBe(false);
    expect(deepEqual([1, 2], [1, 2, 3])).toBe(false);
    expect(deepEqual([], {})).toBe(false); // array vs object
  });

  it("mirrors a form DTO edit — equal on load, unequal after a change", () => {
    const loaded = { name: "acme", labels: ["a"], inherits: { symlinks: true } };
    expect(deepEqual({ ...loaded, labels: [...loaded.labels] }, loaded)).toBe(true);
    expect(deepEqual({ ...loaded, name: "acme2" }, loaded)).toBe(false);
    expect(deepEqual({ ...loaded, inherits: { symlinks: false } }, loaded)).toBe(false);
    expect(deepEqual({ ...loaded, labels: ["a", "b"] }, loaded)).toBe(false);
  });
});
