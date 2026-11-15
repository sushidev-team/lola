import { beforeEach, describe, expect, it } from "vitest";
import { INSET_MIN_PX, INSET_PX, TerminalView, fitCols, terminalInset, type GridReport } from "./terminalgeometry.svelte";
import { DEFAULT_MODES } from "@mobile/lib/keybytes";
import { rememberOwnShell } from "@mobile/lib/panepinstore";

const report = (over: Partial<GridReport> = {}): GridReport => ({
  modes: DEFAULT_MODES,
  cols: 200,
  rows: 50,
  shown: 55,
  shownRows: 30,
  capCols: 55,
  capRows: 30,
  first: 1,
  panning: true,
  font: 12,
  canFit: true,
  fitActive: false,
  fitSize: 8,
  ...over,
});

beforeEach(() => globalThis.localStorage?.clear());

describe("TerminalView geometry", () => {
  it("does not replace geom for an identical report", () => {
    const v = new TerminalView();
    expect(v.apply(report())).toBe(true);
    const first = v.geom;
    expect(v.apply(report())).toBe(false);
    expect(v.geom).toBe(first);
  });

  it("does not replace geom when only capacity, font or modes moved", () => {
    const v = new TerminalView();
    v.apply(report());
    const first = v.geom;
    expect(v.apply(report({ capCols: 40, font: 14 }))).toBe(false);
    expect(v.geom).toBe(first);
    expect(v.capacity).toEqual({ cols: 40, rows: 30 });
    expect(v.font).toBe(14);
  });

  it("replaces geom when a field differs", () => {
    const v = new TerminalView();
    v.apply(report());
    const first = v.geom;
    expect(v.apply(report({ first: 20 }))).toBe(true);
    expect(v.geom).not.toBe(first);
    expect(v.geom.first).toBe(20);
  });

  it("keeps the capacity object when the capacity did not move", () => {
    const v = new TerminalView();
    v.apply(report());
    const cap = v.capacity;
    v.apply(report({ first: 3 }));
    expect(v.capacity).toBe(cap);
  });

  it("does not carry the stored geometry fields of the report into geom", () => {
    const v = new TerminalView();
    v.apply(report());
    expect(Object.keys(v.geom).sort()).toEqual(
      ["canFit", "cols", "first", "fitActive", "fitSize", "panning", "rows", "shown", "shownRows"],
    );
  });
});

describe("TerminalView pin", () => {
  it("is off until turned on, and remembers the choice", () => {
    const v = new TerminalView();
    v.switchPane("a");
    expect(v.pinActive).toBe(false);
    v.setPin(true);
    expect(v.pinActive).toBe(true);
    expect(new TerminalView().pinEnabled).toBe(true);
  });

  it("is on for a shell this phone started, whatever the toggle says", () => {
    rememberOwnShell("s-shell-1");
    const v = new TerminalView();
    v.switchPane("s-shell-1");
    expect(v.pinEnabled).toBe(false);
    expect(v.pinActive).toBe(true);
  });

  it("turning it off also stops the auto-pin of that shell", () => {
    rememberOwnShell("s-shell-1");
    const v = new TerminalView();
    v.switchPane("s-shell-1");
    v.setPin(false);
    expect(v.pinActive).toBe(false);
  });

  it("clears the capacity on a pane switch", () => {
    const v = new TerminalView();
    v.apply(report());
    v.switchPane("other");
    expect(v.capacity).toEqual({ cols: 0, rows: 0 });
  });

  it("reset forgets the last terminal", () => {
    const v = new TerminalView();
    v.apply(report());
    v.switchPane("x");
    v.actions = { setFont() {}, toggleFit() {} };
    v.reset();
    expect(v.geom.cols).toBe(0);
    expect(v.pane).toBe("");
    expect(v.actions).toBeNull();
  });
});

describe("fitted columns", () => {
  it("floors the measured inner width over the cell, never wider", () => {
    // 390 less 12 each side is 366; at an 8px cell that is 45 columns, not 48.
    expect(fitCols(390, 8, INSET_PX)).toBe(45);
    expect(fitCols(390, 8, INSET_MIN_PX)).toBe(46);
    expect(fitCols(390, 7.8, INSET_PX) * 7.8).toBeLessThanOrEqual(390 - 2 * INSET_PX);
  });

  it("is 0 for anything unmeasured", () => {
    expect(fitCols(0, 8)).toBe(0);
    expect(fitCols(390, 0)).toBe(0);
    expect(fitCols(Number.NaN, 8)).toBe(0);
    expect(fitCols(10, 8)).toBe(0);
  });

  it("pads 12px, and 8px only when 12 would drop a column of the pinned grid", () => {
    expect(terminalInset(390, 8, 0)).toBe(12);
    expect(terminalInset(390, 8, 40)).toBe(12);
    expect(terminalInset(390, 8, 45)).toBe(12);
    // 46 columns fit at 8px but not at 12px.
    expect(terminalInset(390, 8, 46)).toBe(8);
    // 47 fit at neither: the grid pans, so the usual padding stands.
    expect(terminalInset(390, 8, 47)).toBe(12);
  });
});
