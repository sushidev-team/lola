import { afterEach, describe, expect, it, vi } from "vitest";
import { setTouchSurface, touchHaptic, touchSurface } from "./touchsurface";

afterEach(() => setTouchSurface(null));

describe("touchsurface", () => {
  it("is off, and haptics are a no-op, unless a surface is set", () => {
    expect(touchSurface()).toBe(false);
    expect(() => touchHaptic("light")).not.toThrow();
  });

  it("routes a haptic to the surface that set itself", () => {
    const haptic = vi.fn();
    setTouchSurface({ haptic });
    expect(touchSurface()).toBe(true);
    touchHaptic("decision");
    expect(haptic).toHaveBeenCalledWith("decision");
    setTouchSurface(null);
    expect(touchSurface()).toBe(false);
  });

  it("never lets a failing haptic surface", () => {
    setTouchSurface({ haptic: () => { throw new Error("no engine"); } });
    expect(() => touchHaptic("rigid")).not.toThrow();
  });
});
