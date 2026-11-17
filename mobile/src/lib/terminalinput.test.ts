import { describe, expect, it, vi } from "vitest";
import { SCROLL_FLUSH_MS, SHIFT_ENTER, ScrollQueue, TouchGestures, isShiftEnter, typedBytes } from "./terminalinput";

const key = (over: Partial<Parameters<typeof isShiftEnter>[0]> = {}) => ({
  type: "keydown",
  key: "Enter",
  shiftKey: true,
  ctrlKey: false,
  metaKey: false,
  altKey: false,
  ...over,
});

const pt = (x: number, y: number) => ({ clientX: x, clientY: y });

describe("shift+enter", () => {
  it("is ESC CR", () => {
    expect(SHIFT_ENTER).toBe("\x1b\r");
  });

  it("matches only an unmodified shift+enter keydown", () => {
    expect(isShiftEnter(key())).toBe(true);
    expect(isShiftEnter(key({ shiftKey: false }))).toBe(false);
    expect(isShiftEnter(key({ ctrlKey: true }))).toBe(false);
    expect(isShiftEnter(key({ metaKey: true }))).toBe(false);
    expect(isShiftEnter(key({ altKey: true }))).toBe(false);
    expect(isShiftEnter(key({ type: "keyup" }))).toBe(false);
    expect(isShiftEnter(key({ key: "a" }))).toBe(false);
  });
});

describe("typedBytes", () => {
  it("applies a latched ctrl to the first character", () => {
    expect(typedBytes("c", { ctrl: true })).toBe("\x03");
  });

  it("applies a latched alt as an ESC prefix", () => {
    expect(typedBytes("b", { alt: true })).toBe("\x1bb");
  });

  it("passes a bracketed paste through untouched", () => {
    const paste = "\x1b[200~ls\x1b[201~";
    expect(typedBytes(paste, { ctrl: true, alt: true })).toBe(paste);
  });

  it("leaves plain text alone with nothing latched", () => {
    expect(typedBytes("ls", {})).toBe("ls");
  });
});

describe("TouchGestures", () => {
  it("claims nothing until the finger clears the slop", () => {
    const g = new TouchGestures();
    g.begin([pt(100, 100)], 12);
    expect(g.move([pt(102, 101)])).toEqual({ claim: false });
  });

  it("pans on a sideways drag and scrolls on a vertical one, locked once", () => {
    const g = new TouchGestures();
    g.begin([pt(100, 100)], 12);
    expect(g.move([pt(120, 102)])).toEqual({ claim: true, pan: { dx: 20, dy: 0 } });
    // A later vertical wobble stays on the locked axis.
    expect(g.move([pt(125, 160)])).toEqual({ claim: true, pan: { dx: 5, dy: 0 } });

    const v = new TouchGestures();
    v.begin([pt(100, 100)], 12);
    expect(v.move([pt(101, 130)])).toEqual({ claim: true, scrollPx: -30 });
  });

  it("pinches from the origin distance and pans by the midpoint", () => {
    const g = new TouchGestures();
    g.begin([pt(100, 100), pt(200, 100)], 12);
    const e = g.move([pt(80, 100), pt(220, 100)]);
    expect(e.claim).toBe(true);
    expect(e.font).toBe(16); // 12 * 1.4 = 16.8 -> clamped to the ceiling
    expect(e.pan).toEqual({ dx: 0, dy: 0 });
  });

  it("reports a tap only when nothing moved and one finger was down", () => {
    const g = new TouchGestures();
    g.begin([pt(10, 10)], 12);
    expect(g.end(0)).toBe(true);

    g.begin([pt(10, 10)], 12);
    g.move([pt(60, 10)]);
    expect(g.end(0)).toBe(false);

    g.begin([pt(10, 10), pt(50, 10)], 12);
    expect(g.end(0)).toBe(false);
  });

  it("forgets the gesture once every finger is up", () => {
    const g = new TouchGestures();
    g.begin([pt(0, 0)], 12);
    g.move([pt(30, 0)]);
    g.end(0);
    g.begin([pt(0, 0)], 12);
    expect(g.move([pt(0, 30)])).toEqual({ claim: true, scrollPx: -30 });
  });
});

describe("ScrollQueue", () => {
  it("batches a drag into whole lines once per flush and keeps the remainder", () => {
    vi.useFakeTimers();
    try {
      const sent: number[] = [];
      const q = new ScrollQueue((n) => sent.push(n));
      q.add(10, 10);
      q.add(25, 10);
      expect(sent).toEqual([]);
      vi.advanceTimersByTime(SCROLL_FLUSH_MS);
      expect(sent).toEqual([-3]);
      q.add(5, 10);
      vi.advanceTimersByTime(SCROLL_FLUSH_MS);
      expect(sent).toEqual([-3, -1]);
    } finally {
      vi.useRealTimers();
    }
  });

  it("sends nothing after dispose", () => {
    vi.useFakeTimers();
    try {
      const send = vi.fn();
      const q = new ScrollQueue(send);
      q.add(40, 10);
      q.dispose();
      vi.advanceTimersByTime(SCROLL_FLUSH_MS * 4);
      expect(send).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });
});
