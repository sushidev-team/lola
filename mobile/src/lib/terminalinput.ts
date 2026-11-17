// What the human's fingers and keys become on the terminal: typed bytes with
// the accessory bar's latched modifiers, the touch gesture machine that turns a
// drag into a pan, a scroll or a pinch, and the scroll queue that batches a
// drag into line counts. No DOM, no runes: the terminal component feeds it
// touch points and applies what it answers.
//
// Invariants:
//  - The axis of a one-finger drag is locked once per gesture (`lockAxis`), so
//    a curved swipe never alternates between panning and scrolling.
//  - A pinch is computed from its origin (`pinchFont(base, scale)`), never
//    accumulated per frame.
//  - A bracketed paste passes through untouched: a latched modifier would
//    otherwise land on xterm's wrapper instead of the pasted text.
//  - Scrolling is whole lines handed to the daemon's scroll RPC, never keys:
//    the daemon decides between wheel events and copy mode.

import { isBracketedPaste, textBytes, type KeyModifiers } from "@mobile/lib/keybytes";
import {
  NO_SCROLL,
  accumulateScroll,
  lockAxis,
  pinchFont,
  takeScroll,
  touchDistance,
  touchMidpoint,
  type Axis,
  type ScrollAccum,
} from "@mobile/lib/viewport";

/** Shift+Enter is `ESC CR`, the encoding both surfaces share. */
export const SHIFT_ENTER = "\x1b\r";

/** The slice of a keyboard event that identifies an unmodified Shift+Enter. */
export interface KeyLike {
  type: string;
  key: string;
  shiftKey: boolean;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
}

export function isShiftEnter(e: KeyLike): boolean {
  return (
    e.type === "keydown" &&
    e.key === "Enter" &&
    e.shiftKey &&
    !e.ctrlKey &&
    !e.metaKey &&
    !e.altKey
  );
}

/**
 * What soft-keyboard text becomes under the accessory bar's latched modifiers.
 * The latch is only peeked: it is consumed by the write that used it.
 */
export function typedBytes(data: string, latched: KeyModifiers): string {
  return isBracketedPaste(data) ? data : textBytes(data, latched);
}

interface Point {
  clientX: number;
  clientY: number;
}

/** What one touch event asks the terminal to do. */
export interface TouchEffect {
  /** The event is ours: the page must not scroll or zoom. */
  claim: boolean;
  /** Move the window over the grid. */
  pan?: { dx: number; dy: number };
  /** Scroll the pane by this many pixels (positive is toward older output). */
  scrollPx?: number;
  /** A new font size from a pinch. */
  font?: number;
}

const NONE: TouchEffect = { claim: false };

export class TouchGestures {
  private start = { x: 0, y: 0 };
  private last = { x: 0, y: 0 };
  private axis: Axis = null;
  private moved = false;
  private pinchStart = 0;
  private pinchBase = 0;
  private fingers = 0;

  begin(touches: ArrayLike<Point>, fontSize: number): void {
    this.fingers = touches.length;
    this.moved = false;
    this.axis = null;
    if (this.fingers === 1) {
      const t = touches[0];
      this.start = this.last = { x: t.clientX, y: t.clientY };
    } else if (this.fingers >= 2) {
      this.pinchStart = touchDistance(touches[0], touches[1]);
      this.pinchBase = fontSize;
      this.last = touchMidpoint(touches[0], touches[1]);
    }
  }

  move(touches: ArrayLike<Point>): TouchEffect {
    if (touches.length >= 2) {
      this.moved = true;
      const d = touchDistance(touches[0], touches[1]);
      const mid = touchMidpoint(touches[0], touches[1]);
      const effect: TouchEffect = {
        claim: true,
        pan: { dx: mid.x - this.last.x, dy: mid.y - this.last.y },
      };
      if (this.pinchStart > 0) effect.font = pinchFont(this.pinchBase, d / this.pinchStart);
      this.last = mid;
      return effect;
    }

    const t = touches[0];
    if (!t) return NONE;
    this.axis ??= lockAxis(t.clientX - this.start.x, t.clientY - this.start.y);
    if (this.axis === null) return NONE;

    this.moved = true;
    const effect: TouchEffect =
      this.axis === "x"
        ? { claim: true, pan: { dx: t.clientX - this.last.x, dy: 0 } }
        : { claim: true, scrollPx: -(t.clientY - this.last.y) };
    this.last = { x: t.clientX, y: t.clientY };
    return effect;
  }

  /** True when the lift was a tap, which is what raises the keyboard. */
  end(remaining: number): boolean {
    const tap = !this.moved && this.fingers === 1;
    if (remaining === 0) {
      this.fingers = 0;
      this.axis = null;
      this.pinchStart = 0;
    }
    return tap;
  }
}

/** One frame: a drag's pixels are folded into lines at most this often. */
export const SCROLL_FLUSH_MS = 16;

/** Pixels in, whole lines out to `send`, at most once per SCROLL_FLUSH_MS. */
export class ScrollQueue {
  private acc: ScrollAccum = NO_SCROLL;
  private timer: ReturnType<typeof setTimeout> | undefined;

  constructor(private readonly send: (lines: number) => void) {}

  add(px: number, cellHeight: number): void {
    this.acc = accumulateScroll(this.acc, px, cellHeight);
    this.timer ??= setTimeout(() => this.flush(), SCROLL_FLUSH_MS);
  }

  dispose(): void {
    clearTimeout(this.timer);
    this.timer = undefined;
  }

  private flush(): void {
    this.timer = undefined;
    const { lines, rest } = takeScroll(this.acc);
    this.acc = rest;
    if (lines !== 0) this.send(lines);
  }
}
