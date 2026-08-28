import { describe, it, expect } from "vitest";
import {
  BAR_ROW_PRIMARY,
  BAR_ROW_SECONDARY,
  DEFAULT_MODES,
  ESC,
  KEY_TABLE,
  barKeyBytes,
  bytesFor,
  controlByte,
  isBracketedPaste,
  modifierParam,
  pasteBytes,
  textBytes,
  type KeyId,
  type TerminalModes,
} from "./keybytes";

// These tests are the only mechanism that keeps the accessory bar honest before
// it reaches a device. A wrong byte here produces no error anywhere in the
// stack: the key just does not do what its label says, eight layers away from
// anything observable. So every assertion below names the exact string rather
// than a property of it, and the escape sequences are written out with explicit
// \x1b so a diff shows the actual bytes.

const APP: TerminalModes = { applicationCursorKeysMode: true, bracketedPasteMode: false };
const PASTE: TerminalModes = { applicationCursorKeysMode: false, bracketedPasteMode: true };

describe("the fixed sequences", () => {
  // Each of these is quoted in PLAN.md's "The exact bytes, because getting any
  // of them wrong is a silent failure" paragraph. They are pinned literally.
  const cases: [KeyId, string][] = [
    ["escape", "\x1b"],
    ["tab", "\t"],
    ["shiftTab", "\x1b[Z"],
    ["enter", "\r"],
    ["shiftEnter", "\x1b\r"],
    ["backspace", "\x7f"],
    ["ctrlC", "\x03"],
    ["ctrlD", "\x04"],
    ["ctrlZ", "\x1a"],
    ["ctrlR", "\x12"],
  ];

  for (const [id, want] of cases) {
    it(`${id} is ${JSON.stringify(want)}`, () => {
      expect(bytesFor(id, DEFAULT_MODES)).toBe(want);
    });
  }

  it("sends Enter as CR and never LF", () => {
    // A cooked shell maps CR to NL itself; a full-screen agent reads a bare LF
    // as Ctrl-J, a different key. This is the one assertion that catches a
    // "\n" typo, which otherwise looks entirely reasonable in review.
    expect(bytesFor("enter")).toBe("\r");
    expect(bytesFor("enter")).not.toContain("\n");
  });

  it("sends Backspace as DEL and never BS", () => {
    expect(bytesFor("backspace")).toBe("\x7f");
    expect(bytesFor("backspace")).not.toBe("\b");
  });

  it("sends Shift+Enter as the ESC CR pair, in that order", () => {
    const b = bytesFor("shiftEnter");
    expect(b).toHaveLength(2);
    expect(b.charCodeAt(0)).toBe(0x1b);
    expect(b.charCodeAt(1)).toBe(0x0d);
  });

  it("sends Shift-Tab as CSI Z, which is how Claude Code cycles permission modes", () => {
    expect(bytesFor("shiftTab")).toBe("\x1b[Z");
  });

  it("leaves the fixed sequences alone whatever the terminal modes are", () => {
    for (const [id, want] of cases) {
      expect(bytesFor(id, APP)).toBe(want);
      expect(bytesFor(id, PASTE)).toBe(want);
    }
  });
});

describe("the arrows follow DECCKM", () => {
  it("uses the CSI form when the application has not set the mode", () => {
    expect(bytesFor("up", DEFAULT_MODES)).toBe("\x1b[A");
    expect(bytesFor("down", DEFAULT_MODES)).toBe("\x1b[B");
    expect(bytesFor("right", DEFAULT_MODES)).toBe("\x1b[C");
    expect(bytesFor("left", DEFAULT_MODES)).toBe("\x1b[D");
  });

  it("uses the SS3 form once the application HAS set it", () => {
    expect(bytesFor("up", APP)).toBe("\x1bOA");
    expect(bytesFor("down", APP)).toBe("\x1bOB");
    expect(bytesFor("right", APP)).toBe("\x1bOC");
    expect(bytesFor("left", APP)).toBe("\x1bOD");
  });

  it("switches Home and End with the same mode", () => {
    expect(bytesFor("home", DEFAULT_MODES)).toBe("\x1b[H");
    expect(bytesFor("end", DEFAULT_MODES)).toBe("\x1b[F");
    expect(bytesFor("home", APP)).toBe("\x1bOH");
    expect(bytesFor("end", APP)).toBe("\x1bOF");
  });

  it("does NOT switch Page Up and Page Down, which are mode-independent", () => {
    expect(bytesFor("pageUp", DEFAULT_MODES)).toBe("\x1b[5~");
    expect(bytesFor("pageDown", DEFAULT_MODES)).toBe("\x1b[6~");
    expect(bytesFor("pageUp", APP)).toBe("\x1b[5~");
    expect(bytesFor("pageDown", APP)).toBe("\x1b[6~");
  });

  it("hardcodes no arrow anywhere in the table", () => {
    // The regression this guards: someone "simplifies" a cursor entry into a
    // fixed one, the arrows keep working in a shell, and the AskUserQuestion
    // picker silently stops responding.
    for (const id of ["up", "down", "left", "right", "home", "end"] as KeyId[]) {
      expect(KEY_TABLE[id].kind).toBe("cursor");
    }
  });
});

describe("modified cursor keys", () => {
  it("computes xterm's modifier parameter", () => {
    expect(modifierParam({})).toBe(1);
    expect(modifierParam({ shift: true })).toBe(2);
    expect(modifierParam({ alt: true })).toBe(3);
    expect(modifierParam({ ctrl: true })).toBe(5);
    expect(modifierParam({ ctrl: true, alt: true, shift: true })).toBe(8);
  });

  it("uses the explicit CSI 1 ; m form and ignores DECCKM while modified", () => {
    // A modified cursor key has no SS3 form, in either mode. Word-wise movement
    // (Ctrl-Left / Ctrl-Right) is the case that matters on a phone.
    expect(bytesFor("left", DEFAULT_MODES, { ctrl: true })).toBe("\x1b[1;5D");
    expect(bytesFor("left", APP, { ctrl: true })).toBe("\x1b[1;5D");
    expect(bytesFor("up", DEFAULT_MODES, { shift: true })).toBe("\x1b[1;2A");
  });

  it("modifies the tilde keys in place", () => {
    expect(bytesFor("pageUp", DEFAULT_MODES, { ctrl: true })).toBe("\x1b[5;5~");
  });

  it("meta-prefixes a fixed key but never control-mangles one", () => {
    expect(bytesFor("enter", DEFAULT_MODES, { alt: true })).toBe("\x1b\r");
    // Ctrl on Escape has no meaning and must leave the byte alone rather than
    // inventing one.
    expect(bytesFor("escape", DEFAULT_MODES, { ctrl: true })).toBe("\x1b");
  });
});

describe("the Ctrl latch", () => {
  it("maps the letters case-insensitively, as a tty does", () => {
    expect(controlByte("c")).toBe("\x03");
    expect(controlByte("C")).toBe("\x03");
    expect(controlByte("a")).toBe("\x01");
    expect(controlByte("z")).toBe("\x1a");
  });

  it("maps the punctuation that carries a C0", () => {
    expect(controlByte("@")).toBe("\x00");
    expect(controlByte("[")).toBe("\x1b");
    expect(controlByte("\\")).toBe("\x1c");
    expect(controlByte("]")).toBe("\x1d");
    expect(controlByte("^")).toBe("\x1e");
    expect(controlByte("_")).toBe("\x1f");
    expect(controlByte(" ")).toBe("\x00");
    expect(controlByte("?")).toBe("\x7f");
  });

  it("maps the digit row the way xterm's keydown path does", () => {
    expect(controlByte("3")).toBe("\x1b");
    expect(controlByte("4")).toBe("\x1c");
    expect(controlByte("5")).toBe("\x1d");
    expect(controlByte("6")).toBe("\x1e");
    expect(controlByte("7")).toBe("\x1f");
    expect(controlByte("8")).toBe("\x7f");
  });

  it("returns null for a character with no control form", () => {
    expect(controlByte("é")).toBeNull();
    expect(controlByte("1")).toBeNull();
    expect(controlByte("")).toBeNull();
    expect(controlByte("ab")).toBeNull();
  });

  it("refuses a non-ASCII character whose uppercase form lands in the table", () => {
    // toUpperCase is Unicode-aware and both lengthens and remaps. Taking
    // charCodeAt(0) of the result turned the sharp s into Ctrl-S — which XOFFs
    // the tty, so the terminal appears to freeze — and the dotless i into Tab.
    // Neither byte was asked for, and a wrong control byte is worse than none:
    // with null, textBytes sends the character itself.
    expect(controlByte("\u00df")).toBeNull(); // uppercases to "SS"
    expect(controlByte("\u0131")).toBeNull(); // uppercases to "I"
    expect(textBytes("\u00df", { ctrl: true })).toBe("\u00df");
  });

  it("applies to the first character of typed text only", () => {
    expect(textBytes("c", { ctrl: true })).toBe("\x03");
    expect(textBytes("cat", { ctrl: true })).toBe("\x03at");
  });

  it("sends the character unchanged when it has no control form", () => {
    // Dropping it would make the latch feel broken; inventing a byte is worse.
    expect(textBytes("1", { ctrl: true })).toBe("1");
  });

  it("recognises a paste xterm has already bracketed", () => {
    // xterm.js wraps a paste in CoreService, so the transform sees the wrapper
    // and must not put an ESC in front of it or apply Ctrl to its first byte.
    expect(isBracketedPaste(`${ESC}[200~hello${ESC}[201~`)).toBe(true);
    expect(isBracketedPaste("hello")).toBe(false);
    expect(isBracketedPaste(`${ESC}[201~`)).toBe(false);
    expect(isBracketedPaste("")).toBe(false);
  });

  it("prefixes ESC for a latched Alt, and stacks with Ctrl", () => {
    expect(textBytes("f", { alt: true })).toBe("\x1bf");
    expect(textBytes("c", { alt: true, ctrl: true })).toBe("\x1b\x03");
  });

  it("sends nothing for empty text", () => {
    expect(textBytes("", { ctrl: true })).toBe("");
  });
});

describe("bracketed paste", () => {
  it("wraps only when the program set the mode", () => {
    expect(pasteBytes("a\nb", DEFAULT_MODES)).toBe("a\nb");
    expect(pasteBytes("a\nb", PASTE)).toBe("\x1b[200~a\nb\x1b[201~");
  });

  it("strips a terminator hidden inside the payload", () => {
    // Otherwise pasted text ends its own paste early and the remainder arrives
    // as keystrokes — which, in a composer, submits.
    const nasty = `x${ESC}[201~rm -rf /`;
    expect(pasteBytes(nasty, PASTE)).toBe("\x1b[200~xrm -rf /\x1b[201~");
  });

  it("sends nothing for an empty paste", () => {
    expect(pasteBytes("", PASTE)).toBe("");
  });
});

describe("the bar's own layout", () => {
  it("puts every key PLAN.md's row 1 names on row 1", () => {
    const ids = BAR_ROW_PRIMARY.map((k) => k.id);
    expect(ids).toEqual([
      "escape", "tab", "shiftTab",
      "up", "down", "left", "right",
      "enter", "shiftEnter",
    ]);
  });

  it("keeps the two interrupts exempt from the mid-turn confirmation", () => {
    // PLAN.md: "Ctrl-C and Escape are exempt, because interrupting is the
    // legitimate mid-turn action". If this ever flips, the friction guard makes
    // the app useless for the one thing it is carried for.
    const all = [...BAR_ROW_PRIMARY, ...BAR_ROW_SECONDARY];
    const exempt = all.filter((k) => k.interrupt).map((k) => k.id);
    expect(exempt.sort()).toEqual(["ctrlC", "escape"]);
  });

  it("marks only the keys worth repeating", () => {
    const all = [...BAR_ROW_PRIMARY, ...BAR_ROW_SECONDARY];
    const repeats = all.filter((k) => k.repeats).map((k) => k.id);
    expect(repeats.sort()).toEqual([
      "backspace", "down", "left", "pageDown", "pageUp", "right", "up",
    ]);
  });

  it("gives every key a screen-reader name, since most labels are glyphs", () => {
    for (const k of [...BAR_ROW_PRIMARY, ...BAR_ROW_SECONDARY]) {
      expect(k.aria.length).toBeGreaterThan(0);
      expect(k.label.length).toBeGreaterThan(0);
    }
  });

  it("routes every bar key through the one encoder", () => {
    expect(barKeyBytes(BAR_ROW_PRIMARY[0])).toBe("\x1b");
    const y = BAR_ROW_SECONDARY.find((k) => k.value === "y")!;
    expect(barKeyBytes(y)).toBe("y");
    expect(barKeyBytes(y, DEFAULT_MODES, { ctrl: true })).toBe("\x19");
  });

  it("makes a latch toggle send nothing at all", () => {
    const ctrl = BAR_ROW_SECONDARY.find((k) => k.kind === "latch")!;
    expect(barKeyBytes(ctrl)).toBe("");
  });
});
