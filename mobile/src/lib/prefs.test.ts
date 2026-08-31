import { describe, it, expect, beforeEach } from "vitest";
import {
  PANE_LABEL_MAX,
  clearFontSize,
  clearPaneLabel,
  loadFontSize,
  loadPaneLabels,
  normalizePaneLabel,
  prunePaneLabels,
  saveFontSize,
  savePaneLabel,
} from "./prefs";
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

const LABELS = "lola.mobile.paneLabels";

/** A literal control or format character, spelled rather than pasted. */
const ch = (code: number) => String.fromCharCode(code);

describe("per-pane display labels", () => {
  it("round-trips a nickname, keyed by tmux pane name", () => {
    savePaneLabel("lola-fe-42-shell-2", "notes");
    expect(loadPaneLabels()).toEqual({ "lola-fe-42-shell-2": "notes" });
  });

  it("holds several panes in ONE entry", () => {
    // One key rather than one per pane, so a prune is a single read and a
    // single write rather than a walk of the whole storage area.
    savePaneLabel("lola-fe-42-shell-1", "logs");
    savePaneLabel("lola-fe-42-shell-2", "notes");
    const mine = Object.keys(globalThis.localStorage ?? {}).filter((k) =>
      k.startsWith("lola.mobile.pane"),
    );
    expect(mine).toEqual([LABELS]);
    expect(loadPaneLabels()).toEqual({
      "lola-fe-42-shell-1": "logs",
      "lola-fe-42-shell-2": "notes",
    });
  });

  it("treats an empty label as FORGET rather than as a stored empty string", () => {
    // "Use the default name" and "clear the field" are the same gesture, and a
    // stored "" would render as a nameless tab.
    savePaneLabel("lola-fe-42-shell-2", "notes");
    savePaneLabel("lola-fe-42-shell-2", "   ");
    expect(loadPaneLabels()).toEqual({});
    // ...and the key goes with the last entry, rather than leaving "{}" behind.
    expect(globalThis.localStorage?.getItem(LABELS)).toBeNull();
  });

  it("clears one pane's nickname and leaves the others", () => {
    savePaneLabel("lola-fe-42-shell-1", "logs");
    savePaneLabel("lola-fe-42-shell-2", "notes");
    clearPaneLabel("lola-fe-42-shell-1");
    expect(loadPaneLabels()).toEqual({ "lola-fe-42-shell-2": "notes" });
  });

  it("normalizes to one trimmed, clipped line", () => {
    expect(normalizePaneLabel("  spaced  out  ")).toBe("spaced out");
    expect(normalizePaneLabel("two\nlines")).toBe("two lines");
    expect(normalizePaneLabel("tab\tsep")).toBe("tab sep");
    // A tab is one line in a horizontal scroller: a label long enough to fill
    // the strip is a strip in which no other tab can be found.
    expect(normalizePaneLabel("x".repeat(200))).toHaveLength(PANE_LABEL_MAX);
  });

  it("folds control and format characters, so a tab stays one line", () => {
    // Svelte escapes the text on the way to the DOM, so this is about LAYOUT --
    // and about a bidi override not being able to reorder the strip around it.
    expect(normalizePaneLabel("a" + ch(0x07) + "b")).toBe("a b");
    expect(normalizePaneLabel("a" + ch(0x202e) + "b")).toBe("a b");
  });

  it("clips on the way IN as well as out", () => {
    savePaneLabel("p", "y".repeat(100));
    expect(loadPaneLabels().p).toHaveLength(PANE_LABEL_MAX);
  });

  it("prunes the panes that are gone and returns what survived", () => {
    // THE WHOLE REASON THIS EXISTS: the daemon allocates the lowest free shell
    // index, so the next `shellCreate` after a close reuses the name that just
    // disappeared. Without the prune, a shell somebody opens tomorrow inherits
    // a stranger's nickname with nothing on screen to explain it.
    savePaneLabel("lola-fe-42-shell-1", "logs");
    savePaneLabel("lola-fe-42-shell-2", "notes");
    const left = prunePaneLabels(["lola-fe-42", "lola-fe-42-shell-1"]);
    expect(left).toEqual({ "lola-fe-42-shell-1": "logs" });
    expect(loadPaneLabels()).toEqual({ "lola-fe-42-shell-1": "logs" });
  });

  it("prunes everything when nothing is known, and says so in its answer", () => {
    savePaneLabel("lola-fe-42-shell-1", "logs");
    expect(prunePaneLabels([])).toEqual({});
    expect(loadPaneLabels()).toEqual({});
  });

  it("validates on the way OUT, so a value from another build cannot reach a tab", () => {
    // The stored value outlives the build that wrote it, exactly as the font
    // size does -- and a hand-edited entry, or another origin writing this key,
    // must not put a multi-line or unbounded string into the strip.
    globalThis.localStorage?.setItem(
      LABELS,
      JSON.stringify({
        good: "fine",
        "": "no pane",
        nested: { not: "a string" },
        numeric: 7,
        blank: "   ",
        long: "z".repeat(90),
        wrapped: "two\nlines",
      }),
    );
    expect(loadPaneLabels()).toEqual({
      good: "fine",
      long: "z".repeat(PANE_LABEL_MAX),
      wrapped: "two lines",
    });
  });

  it("shrugs off a key that is not JSON, or is JSON of the wrong shape", () => {
    globalThis.localStorage?.setItem(LABELS, "{not json");
    expect(loadPaneLabels()).toEqual({});
    globalThis.localStorage?.setItem(LABELS, JSON.stringify(["an", "array"]));
    expect(loadPaneLabels()).toEqual({});
    globalThis.localStorage?.setItem(LABELS, JSON.stringify(null));
    expect(loadPaneLabels()).toEqual({});
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
      expect(loadPaneLabels()).toEqual({});
      expect(() => savePaneLabel("p", "x")).not.toThrow();
      expect(() => clearPaneLabel("p")).not.toThrow();
      expect(prunePaneLabels(["p"])).toEqual({});
    } finally {
      if (real) Object.defineProperty(globalThis, "localStorage", real);
    }
  });
});
