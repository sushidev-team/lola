import { describe, expect, it, vi } from "vitest";
import { IDLE_VIEWS_KEPT, ViewRetention } from "./viewretention";

describe("ViewRetention", () => {
  it("trims the least recently released idle views past the bound, never a held one", () => {
    const trim = vi.fn();
    const r = new ViewRetention(trim, 2);
    const held = r.hold("h");
    for (const id of ["a", "b", "c"]) r.hold(id)();
    expect(trim.mock.calls).toEqual([["a"]]);
    r.hold("b")();
    r.hold("d")();
    expect(trim.mock.calls).toEqual([["a"], ["c"]]);
    held();
    expect(trim.mock.calls).toEqual([["a"], ["c"], ["b"]]);
  });

  it("keeps a view two followers share until both let go, and counts a release once", () => {
    const trim = vi.fn();
    const r = new ViewRetention(trim, 0);
    const one = r.hold("x");
    const two = r.hold("x");
    one();
    one();
    expect(trim).not.toHaveBeenCalled();
    two();
    expect(trim).toHaveBeenCalledExactlyOnceWith("x");
  });

  it("defaults to a small bound", () => {
    expect(IDLE_VIEWS_KEPT).toBe(8);
  });
});
