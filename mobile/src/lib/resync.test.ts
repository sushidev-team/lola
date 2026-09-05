import { describe, it, expect } from "vitest";
import { geometryChanged, resyncToBytes } from "./resync";
import type { ResyncPayload } from "@mobile/wire/protocol";

const base = (over: Partial<ResyncPayload> = {}): ResyncPayload => ({
  cols: 80,
  rows: 24,
  cursorX: 0,
  cursorY: 0,
  ...over,
});

// These are the ONLY tests of the repaint sequence: `wailsshim/screen.ts`
// delegates here rather than keeping a second implementation, and its own test
// file pins that delegation byte for byte.

describe("resyncToBytes", () => {
  it("switches to the alternate screen BEFORE painting", () => {
    // The two screens have separate buffers, so a switch after the paint would
    // throw the paint away and leave the pane blank — the exact failure the
    // resync frame exists to prevent.
    const out = resyncToBytes(base({ altScreen: true, lines: ["hi"] }));
    expect(out.indexOf("\x1b[?1049h")).toBe(0);
    expect(out.indexOf("\x1b[?1049h")).toBeLessThan(out.indexOf("hi"));
  });

  it("leaves the alternate screen for a plain shell pane", () => {
    expect(resyncToBytes(base({ altScreen: false }))).toContain("\x1b[?1049l");
  });

  it("turns autowrap off for the paint and back on after it", () => {
    // A line exactly `cols` wide wraps at the last cell and scrolls the screen,
    // which shifts every absolutely-positioned line after it by one row.
    const out = resyncToBytes(base({ lines: ["x"] }));
    expect(out.indexOf("\x1b[?7l")).toBeGreaterThan(-1);
    expect(out.indexOf("\x1b[?7l")).toBeLessThan(out.indexOf("x"));
    expect(out.indexOf("\x1b[?7h")).toBeGreaterThan(out.indexOf("x"));
  });

  it("positions every line absolutely, one-based", () => {
    const out = resyncToBytes(base({ lines: ["a", "b", "c"] }));
    expect(out).toContain("\x1b[1;1Ha");
    expect(out).toContain("\x1b[2;1Hb");
    expect(out).toContain("\x1b[3;1Hc");
    // No CR/LF anywhere: a short line must not pull the next one up.
    expect(out).not.toContain("\n");
  });

  it("resets SGR after each line so colour cannot bleed", () => {
    const out = resyncToBytes(base({ lines: ["\x1b[31mred"] }));
    expect(out).toContain("\x1b[31mred\x1b[0m");
  });

  it("starts the FIRST line with no inherited attributes either", () => {
    // Every line but the first is protected by the reset that follows its
    // predecessor. The first is protected by the reset before the erase, which
    // is why that one is not decoration: the terminal being repainted may be
    // sitting inside whatever attribute the previous screen left open.
    const out = resyncToBytes(base({ lines: ["plain"] }));
    expect(out.indexOf("\x1b[0m")).toBeLessThan(out.indexOf("\x1b[2J"));
    expect(out.indexOf("\x1b[0m")).toBeLessThan(out.indexOf("plain"));
  });

  it("converts the zero-based cursor to a one-based CSI position", () => {
    const out = resyncToBytes(base({ cursorX: 2, cursorY: 1 }));
    expect(out).toContain("\x1b[2;3H");
  });

  it("shows the cursor when cursorHidden is absent", () => {
    // The field is stated in the negative on the wire on purpose: absent means
    // visible, which is the common case AND the safe default against a daemon
    // too old to send it.
    expect(resyncToBytes(base())).toContain("\x1b[?25h");
    expect(resyncToBytes(base())).not.toContain("\x1b[?25l");
  });

  it("hides it when the frame says so", () => {
    expect(resyncToBytes(base({ cursorHidden: true }))).toContain("\x1b[?25l");
  });

  it("positions the cursor before revealing it", () => {
    // Otherwise a visible cursor flashes at the origin for one frame.
    const out = resyncToBytes(base({ cursorX: 5, cursorY: 5 }));
    expect(out.indexOf("\x1b[6;6H")).toBeLessThan(out.indexOf("\x1b[?25h"));
  });

  it("survives a frame with no lines at all", () => {
    // A pane that exited without a final screen is literally
    // {cols:0,rows:0,cursorX:0,cursorY:0,exited:true}.
    const out = resyncToBytes({ cols: 0, rows: 0, cursorX: 0, cursorY: 0, exited: true });
    expect(out).toContain("\x1b[2J");
    expect(out).toContain("\x1b[1;1H");
  });

  it("clips lines to the frame's own row count", () => {
    const out = resyncToBytes(base({ rows: 2, lines: ["a", "b", "c"] }));
    expect(out).toContain("\x1b[2;1Hb");
    expect(out).not.toContain("\x1b[3;1Hc");
  });

  it("clamps a cursor coordinate that crossed the network as nonsense", () => {
    // A malformed CSI parameter makes the emulator discard the sequence AND
    // whatever follows it in the same write, so this cannot be left to chance.
    const out = resyncToBytes(base({ cursorX: -4, cursorY: 1.7 }));
    expect(out).toContain("\x1b[2;1H");
  });
});

describe("geometryChanged", () => {
  it("is true when the developer's tmux window moved under us", () => {
    expect(geometryChanged({ cols: 80, rows: 24 }, base({ cols: 200, rows: 50 }))).toBe(true);
  });

  it("is false for the same geometry", () => {
    expect(geometryChanged({ cols: 80, rows: 24 }, base())).toBe(false);
  });

  it("refuses to resize to a degenerate frame", () => {
    // An exit frame carries 0x0; resizing a terminal to it would blank the last
    // screen the user is still reading.
    expect(geometryChanged({ cols: 80, rows: 24 }, base({ cols: 0, rows: 0 }))).toBe(false);
  });
});
