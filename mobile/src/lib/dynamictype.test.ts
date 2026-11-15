import { describe, expect, it } from "vitest";
import {
  BODY_DEFAULT,
  ROOT_BASE,
  ROOT_MAX,
  ROOT_MIN,
  applyRootSize,
  rootSizeFor,
} from "./dynamictype";

// The app used to ignore Dynamic Type completely: a capture at
// `accessibility-extra-large` was pixel-identical to one at the default size,
// because every step of the type scale was pinned to an absolute px value.

describe("rootSizeFor", () => {
  it("leaves the designed scale alone at the system default", () => {
    expect(rootSizeFor(BODY_DEFAULT)).toBe(ROOT_BASE);
  });

  it("grows with the setting", () => {
    expect(rootSizeFor(23)).toBeGreaterThan(ROOT_BASE);
    expect(rootSizeFor(28)).toBeGreaterThan(rootSizeFor(23));
    // AX1 (body 28px) is honoured in full: 16 * 28 / 17.
    expect(rootSizeFor(28)).toBeCloseTo(26.35, 1);
    expect(rootSizeFor(28)).toBeLessThan(ROOT_MAX);
  });

  it("never shrinks below the phone scale", () => {
    // The scale was raised on purpose — 13px reads as a shrunken Mac window —
    // so a small Dynamic Type step must not undo that.
    expect(rootSizeFor(14)).toBe(ROOT_MIN);
    expect(rootSizeFor(11)).toBe(ROOT_MIN);
  });

  it("stops at the ceiling, which is a layout limit", () => {
    // ROOT_MAX covers AX1-AX2; past it the dense layout is not designed (spec 04 §6), so AX2 (a
    // body of about 33px) and AX5 (about 53px) both clamp.
    expect(ROOT_MAX).toBe(28);
    expect(rootSizeFor(33)).toBe(ROOT_MAX);
    expect(rootSizeFor(53)).toBe(ROOT_MAX);
  });

  it("falls back to the base for anything unmeasurable", () => {
    for (const v of [0, -1, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(rootSizeFor(v)).toBe(ROOT_BASE);
    }
  });
});

describe("applyRootSize", () => {
  it("writes the property the stylesheet reads", () => {
    applyRootSize(19);
    expect(document.documentElement.style.getPropertyValue("--lola-root-size")).toBe("19px");
  });
});
