import { describe, expect, it } from "vitest";
import { FLAVORS, THEME_IDS, contrast, panelBg, toTokens } from "$lib/catppuccin";
import { DETENT, mobileTokens } from "./mobiletokens";

const flavors = THEME_IDS.map((id) => [id, FLAVORS[id]] as const);

describe("mobileTokens", () => {
  it.each(flavors)("%s: on-accent clears 4.5:1 on the accent fill", (_id, f) => {
    const t = { ...toTokens(f), ...mobileTokens(f) };
    expect(contrast(t["--color-on-accent"], t["--color-accent"])).toBeGreaterThanOrEqual(4.5);
  });

  it("labels the accent dark on dark flavors and light on GitHub Light", () => {
    const dark = mobileTokens(FLAVORS["lola-midnight"])["--color-on-accent"];
    const light = mobileTokens(FLAVORS["github-light"])["--color-on-accent"];
    expect(contrast(dark, "#000000")).toBeLessThan(contrast(dark, "#ffffff"));
    expect(contrast(light, "#ffffff")).toBeLessThan(contrast(light, "#000000"));
  });

  it.each(flavors)("%s: the inactive tab label clears 4.5:1 on the bar", (_id, f) => {
    const idle = mobileTokens(f)["--color-tab-idle"];
    expect(contrast(idle, f.mantle)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(idle, f.base)).toBeGreaterThanOrEqual(4.5);
  });

  it.each(flavors)("%s: a placeholder (full subtext) reads on a panel", (_id, f) => {
    const t = mobileTokens(f);
    expect(contrast(t["--color-subtext"], panelBg(f))).toBeGreaterThanOrEqual(4.5);
  });

  // The composer's placeholder is painted ink-2 (secondary text) on `sel`; prose on the same
  // ground is `subtext`. Both have to read on every flavor.
  it.each(flavors)("%s: ink-2 and subtext clear 4.5:1 on sel", (_id, f) => {
    const t = { ...toTokens(f), ...mobileTokens(f) };
    expect(contrast(t["--color-ink-2"], t["--color-sel"])).toBeGreaterThanOrEqual(4.5);
    expect(contrast(t["--color-subtext"], t["--color-sel"])).toBeGreaterThanOrEqual(4.5);
  });

  it("knows the fit detent", () => {
    expect(Object.keys(DETENT)).toEqual(["medium", "large"]);
  });
});

describe("primary fill and segment thumb tokens", () => {
  for (const [id, f] of flavors) {
    it(`${id}: the primary label clears 4.5:1 on the primary fill`, () => {
      const t = mobileTokens(f);
      expect(contrast(t["--color-on-primary"], t["--color-primary-fill"])).toBeGreaterThanOrEqual(4.5);
      if (!f.dark) expect(t["--color-on-primary"]).toBe(f.base);
    });
    it(`${id}: the segment thumb is ${f.dark ? "lighter than the track" : "the base ground"}`, () => {
      const t = mobileTokens(f);
      expect(t["--color-segment-thumb"]).toBe(f.dark ? f.surface1 : f.base);
    });
  }
});
