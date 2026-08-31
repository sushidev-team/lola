import { describe, it, expect } from "vitest";
import {
  AXIS_LOCK_SLOP,
  FONT_DEFAULT,
  FONT_MAX,
  FONT_MIN,
  MAX_SCROLL_LINES,
  NO_SCROLL,
  accumulateScroll,
  clampFont,
  clampPan,
  firstVisibleColumn,
  fitWidth,
  isPanning,
  lockAxis,
  panBy,
  panLimits,
  partialRowHeight,
  pinchFont,
  stepFont,
  takeScroll,
  touchDistance,
  touchMidpoint,
  visibleColumns,
  visibleRows,
  wheelPixels,
  type PanBox,
} from "./viewport";

// A 200-column pane on a phone: the case the whole module exists for.
const wide: PanBox = { contentWidth: 1600, contentHeight: 700, viewWidth: 390, viewHeight: 480 };
// A grid smaller than its window, which happens after a pinch out.
const small: PanBox = { contentWidth: 300, contentHeight: 200, viewWidth: 390, viewHeight: 480 };

describe("pan limits", () => {
  it("is the overflow on each axis", () => {
    expect(panLimits(wide)).toEqual({ x: 1210, y: 220 });
  });

  it("is never negative, so a small grid pins at the origin", () => {
    expect(panLimits(small)).toEqual({ x: 0, y: 0 });
    expect(clampPan({ x: 50, y: 50 }, small)).toEqual({ x: 0, y: 0 });
  });

  it("clamps at both ends", () => {
    expect(clampPan({ x: -30, y: 9999 }, wide)).toEqual({ x: 0, y: 220 });
  });

  it("collapses a non-finite pan to the origin rather than propagating it", () => {
    // ONE rule for both, deliberately: a NaN would reach a CSS transform and
    // blank the pane, and an Infinity is not a position anyone asked for. The
    // origin is the one value that is always valid and always recoverable.
    expect(clampPan({ x: NaN, y: Infinity }, wide)).toEqual({ x: 0, y: 0 });
  });
});

describe("panBy", () => {
  it("moves the window opposite to the finger, because the finger drags content", () => {
    // Pulling left (dx negative) reveals what is to the RIGHT, so the window's
    // offset grows.
    expect(panBy({ x: 100, y: 0 }, -40, 0, wide).x).toBe(140);
    expect(panBy({ x: 100, y: 0 }, 40, 0, wide).x).toBe(60);
  });

  it("clamps as it goes, so a fast flick cannot leave the grid", () => {
    expect(panBy({ x: 0, y: 0 }, 500, 500, wide)).toEqual({ x: 0, y: 0 });
    expect(panBy({ x: 1200, y: 0 }, -500, 0, wide).x).toBe(1210);
  });
});

describe("font size", () => {
  it("holds the 8-to-16 range PLAN.md names", () => {
    expect(FONT_MIN).toBe(8);
    expect(FONT_MAX).toBe(16);
    expect(clampFont(2)).toBe(8);
    expect(clampFont(40)).toBe(16);
    expect(clampFont(11.4)).toBe(11);
  });

  it("falls back to the reading size for nonsense", () => {
    expect(clampFont(NaN)).toBe(FONT_DEFAULT);
  });

  it("computes a pinch from the size the gesture STARTED at", () => {
    // Accumulating per frame drifts: rounding to a whole point each frame turns
    // a pinch out and back into a net change of several points.
    expect(pinchFont(12, 1)).toBe(12);
    expect(pinchFont(12, 1.5)).toBe(16); // clamped at the ceiling
    expect(pinchFont(12, 0.5)).toBe(8);
    expect(pinchFont(10, 1.2)).toBe(12);
  });

  it("ignores a degenerate scale rather than collapsing the font", () => {
    expect(pinchFont(13, 0)).toBe(13);
    expect(pinchFont(13, NaN)).toBe(13);
  });

  it("steps by whole points for the buttons", () => {
    expect(stepFont(12, 1)).toBe(13);
    expect(stepFont(FONT_MAX, 1)).toBe(FONT_MAX);
    expect(stepFont(FONT_MIN, -1)).toBe(FONT_MIN);
  });
});

describe("scroll accumulation", () => {
  const cell = 17;

  it("keeps the sub-line remainder, so a slow drag eventually moves", () => {
    // Rounding each event in isolation rounds every one of them to zero.
    let acc = NO_SCROLL;
    for (let i = 0; i < 4; i++) acc = accumulateScroll(acc, 5, cell);
    expect(acc.lines).toBe(-1); // 20px of forward movement is one line forward
    expect(Math.abs(acc.pixels)).toBeLessThan(cell);
  });

  it("flips the screen convention into the daemon's, once", () => {
    // Positive pixels means the content moved up (going forward), which is a
    // NEGATIVE line count for the daemon, whose positive means back in history.
    // See SCROLL_BACK in viewport.ts: internal/tmux/client.go's `up := lines > 0`
    // is the authority, NOT the wire package's prose.
    expect(accumulateScroll(NO_SCROLL, 3 * cell, cell).lines).toBe(-3);
    expect(accumulateScroll(NO_SCROLL, -3 * cell, cell).lines).toBe(3);
  });

  it("ignores a zero or nonsense cell height instead of dividing by it", () => {
    expect(accumulateScroll(NO_SCROLL, 100, 0)).toEqual(NO_SCROLL);
    expect(accumulateScroll(NO_SCROLL, 100, NaN)).toEqual(NO_SCROLL);
    expect(accumulateScroll(NO_SCROLL, NaN, cell)).toEqual(NO_SCROLL);
  });

  it("takes whole lines and leaves the remainder behind", () => {
    const acc = accumulateScroll(NO_SCROLL, 2.5 * cell, cell);
    const { lines, rest } = takeScroll(acc);
    expect(lines).toBe(-2);
    expect(rest.lines).toBe(0);
    expect(rest.pixels).toBeCloseTo(0.5 * cell);
  });

  it("clamps to the daemon's own ceiling", () => {
    const { lines } = takeScroll({ pixels: 0, lines: 9000 });
    expect(lines).toBe(MAX_SCROLL_LINES);
    expect(takeScroll({ pixels: 0, lines: -9000 }).lines).toBe(-MAX_SCROLL_LINES);
  });
});

describe("wheel deltas", () => {
  it("passes pixels through", () => {
    expect(wheelPixels(30, 0, 17, 40)).toBe(30);
  });
  it("converts lines and pages", () => {
    expect(wheelPixels(2, 1, 17, 40)).toBe(34);
    expect(wheelPixels(1, 2, 17, 40)).toBe(680);
  });
});

describe("axis lock", () => {
  it("stays undecided inside the slop", () => {
    expect(lockAxis(3, 4)).toBeNull();
    expect(lockAxis(AXIS_LOCK_SLOP - 1, 0)).toBeNull();
  });

  it("commits to the dominant axis once the slop is cleared", () => {
    expect(lockAxis(20, 3)).toBe("x");
    expect(lockAxis(3, 20)).toBe("y");
    expect(lockAxis(-20, 3)).toBe("x");
  });

  it("breaks a perfect diagonal toward the vertical, which is the scroll", () => {
    // Arbitrary, but it must be DETERMINISTIC: an undecided gesture that keeps
    // re-deciding alternates between a free pan and a network round trip.
    expect(lockAxis(20, 20)).toBe("y");
  });
});

describe("pinch geometry", () => {
  const a = { clientX: 0, clientY: 0 };
  const b = { clientX: 30, clientY: 40 };

  it("measures the two-finger distance", () => {
    expect(touchDistance(a, b)).toBe(50);
  });

  it("finds the midpoint a two-finger drag pans by", () => {
    expect(touchMidpoint(a, b)).toEqual({ x: 15, y: 20 });
  });
});

describe("the truncation chip", () => {
  it("reports panning only when the grid is genuinely wider", () => {
    expect(isPanning(wide)).toBe(true);
    expect(isPanning(small)).toBe(false);
  });

  it("counts the columns actually on screen", () => {
    // 1600px of 200 columns is an 8px cell; 390px of window shows 48 of them.
    expect(visibleColumns(wide, 200)).toBe(48);
  });

  it("never claims more columns than the grid has", () => {
    expect(visibleColumns(small, 40)).toBe(40);
  });

  it("answers zero rather than dividing by zero", () => {
    expect(visibleColumns({ ...wide, contentWidth: 0 }, 200)).toBe(0);
    expect(visibleColumns(wide, 0)).toBe(0);
  });
});

// The other half of the same question, and the reason it exists: the size pin
// names this phone's capacity as a PAIR, and until this there was a count for
// the columns and only a pixel offcut for the rows.
describe("visible rows", () => {
  it("counts the rows actually on screen", () => {
    // 700px of 50 rows is a 14px cell; a 480px window shows 34 of them.
    expect(visibleRows(wide, 50)).toBe(34);
  });

  it("never claims more rows than the grid has", () => {
    // A window taller than the content is not a view onto rows that do not
    // exist, and a pin asking for them would hold a window open on blank lines.
    expect(visibleRows(small, 10)).toBe(10);
  });

  it("answers zero rather than dividing by zero", () => {
    expect(visibleRows({ ...wide, contentHeight: 0 }, 50)).toBe(0);
    expect(visibleRows(wide, 0)).toBe(0);
  });

  it("never answers zero for a measured box, because zero is the release", () => {
    // panepin.ts treats a zero as "let go", so a frame too short for one whole
    // row must still report one rather than silently unpinning the pane.
    expect(visibleRows({ ...wide, viewHeight: 3 }, 50)).toBe(1);
  });
});

describe("fit width", () => {
  it("finds the size at which a wide grid fits, when one exists", () => {
    // 100 columns at 12pt measures 800px in a 390px window; 390/800 of 12 is
    // 5.85, which floors to 5 and is below the floor -- so this box is the one
    // that CANNOT fit. A 40-column grid can.
    const narrow: PanBox = { contentWidth: 640, contentHeight: 300, viewWidth: 390, viewHeight: 480 };
    const r = fitWidth(narrow, 16);
    expect(r.size).toBe(9); // floor(16 * 390/640) = 9
    expect(r.complete).toBe(true);
  });

  it("floors rather than rounds, so it never claims a fit it does not have", () => {
    // A round would give 10 here, which still overflows by a pixel.
    const box: PanBox = { contentWidth: 640, contentHeight: 300, viewWidth: 399, viewHeight: 480 };
    expect(fitWidth(box, 16).size).toBe(9); // floor(9.975), not round
  });

  it("reports INCOMPLETE for a 200-column grid, because no legible size fits", () => {
    const r = fitWidth(wide, FONT_DEFAULT);
    expect(r.size).toBe(FONT_MIN);
    expect(r.complete).toBe(false);
  });

  it("is a no-op when the grid already fits", () => {
    const r = fitWidth(small, FONT_DEFAULT);
    expect(r).toEqual({ size: FONT_DEFAULT, complete: true });
  });

  it("has nothing to offer at the floor, which is how the chip knows to stay quiet", () => {
    expect(fitWidth(wide, FONT_MIN).size).toBe(FONT_MIN);
  });

  it("clamps the size it is given and survives an unmeasured box", () => {
    const zero: PanBox = { contentWidth: 0, contentHeight: 0, viewWidth: 0, viewHeight: 0 };
    expect(fitWidth(zero, 99).size).toBe(FONT_MAX);
    expect(fitWidth(zero, Number.NaN).size).toBe(FONT_DEFAULT);
  });
});

describe("firstVisibleColumn", () => {
  // 211 columns of 8px in a 344px viewport: 43 columns on screen.
  const box = { contentWidth: 1688, contentHeight: 748, viewWidth: 344, viewHeight: 600 };

  it("is column 1 at the left edge", () => {
    expect(firstVisibleColumn(box, 211, 0)).toBe(1);
  });

  it("advances one column per cell of pan", () => {
    expect(firstVisibleColumn(box, 211, 8)).toBe(2);
    expect(firstVisibleColumn(box, 211, 800)).toBe(101);
  });

  it("never reports a first column that would run the window past the grid", () => {
    // Panned to the far right, the window is columns 169..211, not 212..254.
    expect(firstVisibleColumn(box, 211, 99999)).toBe(211 - 43 + 1);
  });

  it("is 1 for an unmeasured box, rather than NaN", () => {
    expect(firstVisibleColumn({ contentWidth: 0, contentHeight: 0, viewWidth: 0, viewHeight: 0 }, 211, 40)).toBe(1);
    expect(firstVisibleColumn(box, 0, 40)).toBe(1);
  });
});

describe("partialRowHeight", () => {
  it("is the leftover when the rows do not divide the viewport", () => {
    expect(partialRowHeight(0, 500, 17)).toBeCloseTo(500 % 17);
  });

  it("counts from the panned top, not from the grid's", () => {
    expect(partialRowHeight(4, 500, 17)).toBeCloseTo(504 % 17);
  });

  it("is 0 when the rows divide evenly", () => {
    expect(partialRowHeight(0, 510, 17)).toBe(0);
    expect(partialRowHeight(34, 510, 17)).toBe(0);
  });

  it("ignores a sub-pixel remainder, which is a fractional cell rather than a sliced glyph", () => {
    expect(partialRowHeight(0, 500.2, 100.04)).toBe(0);
  });

  it("is 0 for anything unmeasured", () => {
    expect(partialRowHeight(0, 0, 17)).toBe(0);
    expect(partialRowHeight(0, 500, 0)).toBe(0);
  });
});
