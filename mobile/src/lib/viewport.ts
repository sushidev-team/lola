// The arithmetic behind panning, pinching and scrolling a 200-column grid on a
// 390-point screen. Pure functions, no DOM, so the part of the terminal screen
// most likely to be subtly wrong is the part that can be tested in Node.
//
// THE MODEL. The phone renders the daemon's WHOLE grid and moves a window over
// it. It does not reflow: `attach-session -f ignore-size` means the phone cannot
// shrink the developer's tmux window, so the grid is whatever the Mac's window
// is — commonly 200 columns — and about 55 of them fit. Panning is therefore
// purely client-side and costs no network at all, which is also what keeps it
// responsive under a finger.
//
// THREE GESTURES, and the split between them is the one design decision here:
//
//   two fingers dragging   pan the window over the grid, both axes.
//   one finger, sideways   pan horizontally. There is no horizontal scroll to
//                          confuse it with, and at 55 visible columns this is
//                          the most-used gesture in the app.
//   one finger, vertical   SCROLL — an RPC to the daemon, not a pan. The grid
//                          is one screen; what is above it lives in the
//                          program's own transcript or in tmux's copy mode, and
//                          only the daemon knows which.
//   two fingers pinching   font size, 8 to 16 points.
//
// The axis of a one-finger drag is locked once, from the first movement that
// clears the slop threshold, so a diagonal smudge does not scroll and pan at the
// same time.

/** The pane's own viewport, in device-independent pixels. */
export interface PanBox {
  /** Pixel width and height of the rendered grid (cols x cellW, rows x cellH). */
  contentWidth: number;
  contentHeight: number;
  /** Pixel width and height of the window the phone shows it through. */
  viewWidth: number;
  viewHeight: number;
}

export interface Pan {
  x: number;
  y: number;
}

/**
 * How far the window may travel on each axis.
 *
 * Never negative: content narrower than the view (a small grid, or a zoomed-out
 * font) pins the window at the origin rather than allowing it to drift off the
 * only content there is.
 */
export function panLimits(box: PanBox): Pan {
  return {
    x: Math.max(0, box.contentWidth - box.viewWidth),
    y: Math.max(0, box.contentHeight - box.viewHeight),
  };
}

/** Hold a pan inside its limits. Non-finite values collapse to the origin. */
export function clampPan(pan: Pan, box: PanBox): Pan {
  const lim = panLimits(box);
  const fix = (v: number, max: number) => {
    if (!Number.isFinite(v)) return 0;
    return Math.min(max, Math.max(0, v));
  };
  return { x: fix(pan.x, lim.x), y: fix(pan.y, lim.y) };
}

/**
 * Apply a finger movement to the pan.
 *
 * The sign is inverted because the finger drags the CONTENT, not the window:
 * pulling left reveals what is to the right, which is an increase in the
 * window's offset. Getting this backwards is not subtle on a device, but it is
 * exactly the kind of thing that gets flipped during a refactor.
 */
export function panBy(pan: Pan, dx: number, dy: number, box: PanBox): Pan {
  return clampPan({ x: pan.x - dx, y: pan.y - dy }, box);
}

// ---------------------------------------------------------------------------
// Font size
// ---------------------------------------------------------------------------

/**
 * The legible floor and the practical ceiling.
 *
 * 200 columns across 390 points is roughly a 2-point cell, which is not small
 * text but a grey smear — so "zoom out until it fits" is not on offer. MIN is
 * where a Hack glyph is still resolvable on a retina phone and is useful for
 * ORIENTATION (where am I in this screen), not for reading; DEFAULT is the
 * reading size; MAX is for answering a prompt one-handed.
 */
export const FONT_MIN = 8;
export const FONT_MAX = 16;
export const FONT_DEFAULT = 12;

/** Hold a font size inside the range, rounded to a whole point. */
export function clampFont(size: number): number {
  if (!Number.isFinite(size)) return FONT_DEFAULT;
  return Math.min(FONT_MAX, Math.max(FONT_MIN, Math.round(size)));
}

/**
 * The font size a pinch has arrived at.
 *
 * `scale` is the live ratio of the current two-finger distance to the distance
 * at gesture start, and `base` is the size when the gesture started — so the
 * whole gesture is computed from its origin rather than accumulated per frame.
 * Accumulating drifts: rounding to a whole point every frame turns a pinch out
 * and back into a net change of several points.
 */
export function pinchFont(base: number, scale: number): number {
  if (!Number.isFinite(scale) || scale <= 0) return clampFont(base);
  return clampFont(base * scale);
}

/** One step of the +/- buttons, which exist because a pinch is imprecise. */
export function stepFont(size: number, delta: number): number {
  return clampFont(size + delta);
}

/**
 * The result of asking "make the whole grid fit across the screen".
 *
 * `complete` is the honest half. A 200-column grid on a 390-point phone needs
 * roughly a 2-point cell, and FONT_MIN is 8, so for a wide grid there IS no size
 * that fits and the answer is "8 points, and you are still panning". Reporting
 * that as a plain size would let a caller label a button "fit" over a screen
 * that is still two thirds off-stage.
 */
export interface FitWidth {
  /** The size to use. Always inside [FONT_MIN, FONT_MAX]. */
  size: number;
  /** Whether the whole grid is actually on screen at that size. */
  complete: boolean;
}

/**
 * The font size at which the grid's full width would be visible.
 *
 * THIS IS A ZOOM AND NOTHING ELSE. It does not touch tmux, does not resize the
 * pane, and cannot affect any other client — it scales the phone's own view of a
 * grid whose width the daemon owns. The tmux-side "fit to phone" that PLAN.md
 * describes (dropping `-f ignore-size` so the phone's dimensions drive the
 * window) is a different, daemon-side feature; do not conflate the two, and do
 * not label this one with its name.
 *
 * `box` is a real measurement of the grid as currently rendered at `current`
 * points, so the cell width comes from the browser rather than from an assumed
 * aspect ratio. Cell width is linear in font size, which makes the answer one
 * multiplication — floored, because rounding up leaves it still overflowing by a
 * pixel and the chip would then claim a fit that is not there.
 */
export function fitWidth(box: PanBox, current: number): FitWidth {
  const size = clampFont(current);
  if (
    !Number.isFinite(box.contentWidth) ||
    !Number.isFinite(box.viewWidth) ||
    box.contentWidth <= 0 ||
    box.viewWidth <= 0
  ) {
    return { size, complete: !isPanning(box) };
  }
  if (box.contentWidth <= box.viewWidth) return { size, complete: true };
  const want = Math.floor(size * (box.viewWidth / box.contentWidth));
  const next = clampFont(want);
  return { size: next, complete: next <= want };
}

// ---------------------------------------------------------------------------
// Scroll
// ---------------------------------------------------------------------------

/**
 * POSITIVE SCROLLS BACK INTO HISTORY. This is the daemon's convention and it is
 * the opposite of a wheel delta.
 *
 * The authority is `internal/tmux/client.go`'s ScrollPane, whose first act is
 * `up := lines > 0`: a positive count becomes `scroll-up` in copy mode, or an
 * upward SGR wheel sequence written to a full-screen program. The desktop's
 * LiveTerminal flips the browser's `deltaY` for the same reason.
 *
 * HISTORY, kept because the mistake is easy to make again: `wire/transport.ts`,
 * `wire/protocol.ts` and the golden vector `pty_scroll_back` all originally
 * documented the opposite ("negative scrolls back"). The wire FORMAT was right
 * in every one of them — an integer in a `pty` frame — but the sentence had the
 * sign inverted relative to the Go, so a reader who trusted the prose would have
 * scrolled the wrong way. All four now say what ScrollPane does.
 */
export const SCROLL_BACK = 1;

/** The daemon clamps to this anyway (tmux.MaxScrollLines); clamped here so a
 *  flick cannot send an absurd number and read as a bug at the listener. */
export const MAX_SCROLL_LINES = 500;

/**
 * A drag or a wheel, accumulated into whole lines.
 *
 * `pixels` is the sub-line remainder that must survive between events: one flick
 * is dozens of events and a slow drag is many small ones, so rounding each in
 * isolation rounds every one of them to zero and the pane never moves.
 * `lines` is what has not been sent yet.
 */
export interface ScrollAccum {
  pixels: number;
  lines: number;
}

export const NO_SCROLL: ScrollAccum = { pixels: 0, lines: 0 };

/**
 * Fold a movement in pixels into the accumulator.
 *
 * `deltaPixels` follows the SCREEN convention — positive means the content moved
 * up, i.e. the user is going forward toward the newest output — and the sign is
 * flipped here, once, into the daemon's convention. That flip lives in exactly
 * one place on purpose.
 */
export function accumulateScroll(
  acc: ScrollAccum,
  deltaPixels: number,
  cellHeight: number,
): ScrollAccum {
  if (!Number.isFinite(deltaPixels) || !Number.isFinite(cellHeight) || cellHeight <= 0) {
    return acc;
  }
  const pixels = acc.pixels + deltaPixels;
  const whole = Math.trunc(pixels / cellHeight);
  return { pixels: pixels - whole * cellHeight, lines: acc.lines - whole };
}

/**
 * Take whatever whole lines have accumulated, clamped, and leave the sub-line
 * remainder behind for the next event.
 */
export function takeScroll(acc: ScrollAccum): { lines: number; rest: ScrollAccum } {
  const clamped = Math.max(-MAX_SCROLL_LINES, Math.min(MAX_SCROLL_LINES, acc.lines));
  return { lines: clamped, rest: { pixels: acc.pixels, lines: 0 } };
}

/**
 * A browser wheel delta in the units the element actually uses.
 *
 * Transcribed from the desktop's LiveTerminal.onWheel: DOM_DELTA_LINE is
 * measured in rows and DOM_DELTA_PAGE in screens, and a trackpad reports pixels.
 * A Magic Keyboard trackpad on an iPad reaches this; a finger never does.
 */
export function wheelPixels(deltaY: number, deltaMode: number, cellHeight: number, rows: number): number {
  if (deltaMode === 1) return deltaY * cellHeight;
  if (deltaMode === 2) return deltaY * cellHeight * Math.max(1, rows);
  return deltaY;
}

// ---------------------------------------------------------------------------
// Gestures
// ---------------------------------------------------------------------------

/** How far a finger must travel before the app decides what the gesture is. */
export const AXIS_LOCK_SLOP = 8;

export type Axis = "x" | "y" | null;

/**
 * Which axis a one-finger drag has committed to, or null while it is still
 * ambiguous.
 *
 * Decided ONCE per gesture and then held, which is why the caller stores the
 * answer: re-deciding per frame lets a curved swipe alternate between panning
 * and scrolling, and a scroll is a network round trip.
 */
export function lockAxis(dx: number, dy: number, slop = AXIS_LOCK_SLOP): Axis {
  const ax = Math.abs(dx);
  const ay = Math.abs(dy);
  if (Math.max(ax, ay) < slop) return null;
  return ax > ay ? "x" : "y";
}

/** Distance between two touch points, for a pinch. */
export function touchDistance(
  a: { clientX: number; clientY: number },
  b: { clientX: number; clientY: number },
): number {
  return Math.hypot(a.clientX - b.clientX, a.clientY - b.clientY);
}

/** Midpoint of two touch points, which is what a two-finger drag pans by. */
export function touchMidpoint(
  a: { clientX: number; clientY: number },
  b: { clientX: number; clientY: number },
): { x: number; y: number } {
  return { x: (a.clientX + b.clientX) / 2, y: (a.clientY + b.clientY) / 2 };
}

/**
 * Whether the visible window is narrower than the grid, i.e. whether the "N cols
 * · panning" chip should be shown.
 *
 * Truncation must never be a mystery: a phone showing 55 of 200 columns with no
 * indication looks like an agent that stopped writing halfway across.
 */
export function isPanning(box: PanBox): boolean {
  return box.contentWidth > box.viewWidth + 1;
}

/** How many of the grid's columns are actually on screen, for that chip. */
export function visibleColumns(box: PanBox, cols: number): number {
  if (box.contentWidth <= 0 || cols <= 0) return 0;
  const cell = box.contentWidth / cols;
  if (cell <= 0) return 0;
  return Math.min(cols, Math.max(1, Math.floor(box.viewWidth / cell)));
}

/**
 * The FIRST grid column visible at a given horizontal pan, 1-based.
 *
 * WHY A RANGE AND NOT A COUNT. `visibleColumns` answers "how much of the grid is
 * on screen", which is the truncation warning. It cannot answer "where am I",
 * and on a 211-column grid seen 43 columns at a time that is five screens with
 * nothing to distinguish them: the far left and the middle rendered identically,
 * chip and header included. The pair (first, first + shown - 1) is the whole
 * fix, and it is arithmetic rather than DOM so it is pinned here.
 *
 * Clamped into the grid at both ends, because `pan.x` is clamped against a box
 * that is re-measured a frame later than the font that changed it.
 */
export function firstVisibleColumn(box: PanBox, cols: number, panX: number): number {
  if (box.contentWidth <= 0 || cols <= 0) return 1;
  const cell = box.contentWidth / cols;
  if (cell <= 0) return 1;
  const shown = visibleColumns(box, cols);
  const first = Math.floor(Math.max(0, panX) / cell) + 1;
  return Math.min(Math.max(1, first), Math.max(1, cols - shown + 1));
}

/**
 * The height, in pixels, of the PARTIAL text row at the bottom of the viewport.
 *
 * The grid is a fixed number of rows at a fixed cell height and the frame is
 * whatever is left over after the header and the accessory bar, so the two
 * almost never divide. Opening the bar's second key row shrinks the frame by
 * about 44 points and the last visible line is then cut horizontally THROUGH
 * its glyphs — which reads as a rendering fault rather than as more content
 * below.
 *
 * Masking that strip in the pane's own background turns a bisected line into an
 * absent one, which is the truth. Returns 0 when the rows happen to divide
 * evenly, and 0 for any unmeasured input, so the mask costs nothing until there
 * is something to hide.
 */
export function partialRowHeight(panY: number, viewHeight: number, cellHeight: number): number {
  if (!(cellHeight > 0) || !(viewHeight > 0)) return 0;
  const bottom = Math.max(0, panY) + viewHeight;
  const rest = bottom % cellHeight;
  // A hair under a whole row is a rounding artefact of a fractional cell, not a
  // sliced glyph; masking it would eat a row that is fully drawn.
  return rest < 0.5 || cellHeight - rest < 0.5 ? 0 : rest;
}
