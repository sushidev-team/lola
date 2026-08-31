import { describe, it, expect, beforeEach } from "vitest";
import { clearFontSize, loadFontSize, saveFontSize } from "./prefs";
import { FONT_DEFAULT, FONT_MAX, FONT_MIN } from "./viewport";

const KEY = "lola.mobile.termFont";

beforeEach(() => {
  globalThis.localStorage?.clear();
});

describe("terminal font size", () => {
  it("defaults when nothing is stored", () => {
    expect(loadFontSize()).toBe(FONT_DEFAULT);
  });

  it("round-trips a pinch, which is the whole point", () => {
    saveFontSize(15);
    expect(loadFontSize()).toBe(15);
  });

  it("clears back to the default", () => {
    saveFontSize(9);
    clearFontSize();
    expect(loadFontSize()).toBe(FONT_DEFAULT);
  });

  it("clamps on the way OUT, so a value from another build stays legible", () => {
    // The stored value outlives the build that wrote it. A 2 here would be a
    // grey smear with no on-screen control able to climb back out of it.
    globalThis.localStorage?.setItem(KEY, "2");
    expect(loadFontSize()).toBe(FONT_MIN);
    globalThis.localStorage?.setItem(KEY, "72");
    expect(loadFontSize()).toBe(FONT_MAX);
  });

  it("clamps on the way in too", () => {
    saveFontSize(1000);
    expect(globalThis.localStorage?.getItem(KEY)).toBe(String(FONT_MAX));
  });

  it("falls back rather than throwing on something that is not a number", () => {
    globalThis.localStorage?.setItem(KEY, "not a size");
    expect(loadFontSize()).toBe(FONT_DEFAULT);
    globalThis.localStorage?.setItem(KEY, "");
    expect(loadFontSize()).toBe(FONT_DEFAULT);
  });

  it("survives storage being unavailable", () => {
    const real = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
    Object.defineProperty(globalThis, "localStorage", {
      configurable: true,
      get() {
        throw new Error("storage is disabled in this WebView");
      },
    });
    try {
      expect(loadFontSize()).toBe(FONT_DEFAULT);
      expect(() => saveFontSize(14)).not.toThrow();
      expect(() => clearFontSize()).not.toThrow();
    } finally {
      if (real) Object.defineProperty(globalThis, "localStorage", real);
    }
  });
});
