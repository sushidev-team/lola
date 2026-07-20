// A tiny ANSI-SGR → HTML renderer for the sessions overview grid. tmux
// `capture-pane -p -e` emits the visible screen as text with SGR color/attribute
// escapes only (no cursor motion), so a small SGR state machine is enough to turn
// a snapshot into styled <span>s. This lets the grid show 40 live-ish terminal
// tiles as plain DOM — far cheaper than 40 xterm instances, and well under the
// ~16 WebGL-context ceiling we reserve for the one focused terminal.
//
// All text is HTML-escaped; only style attributes are emitted, so the result is
// safe to inject with {@html} even though pane content is agent-influenced.
//
// THE PALETTE IS A PARAMETER, not an import of the live theme. This module has
// to stay pure — no DOM, no Svelte runes — so it can be unit-tested as a plain
// function and so a renderer is never coupled to whichever flavor happens to be
// mounted. It takes an AnsiPalette and defaults to the default flavor's, which
// keeps every existing call site working. `catppuccin.ts` is safe to import
// because it is pure data + pure functions itself.
//
// Reactivity is therefore the CONSUMER's job: SnapshotTile reads
// `appearance.ansi` inside a $derived, so reading the rune there is what makes
// all the grid tiles re-render when the flavor changes. Nothing in here knows
// that runes exist.

import { DEFAULT_THEME_ID, FLAVORS, toAnsi, type AnsiPalette } from "./catppuccin";

export type { AnsiPalette };

/** The default flavor's palette, so ansiToHtml(text) alone still renders. */
const DEFAULT_PALETTE: AnsiPalette = toAnsi(FLAVORS[DEFAULT_THEME_ID]);

function esc(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

function xterm256(n: number, pal: AnsiPalette): string {
  if (n < 16) return pal.ansi16[n];
  if (n >= 232) {
    const v = 8 + (n - 232) * 10;
    return rgb(v, v, v);
  }
  const c = n - 16;
  const r = Math.floor(c / 36);
  const g = Math.floor((c % 36) / 6);
  const b = c % 6;
  const conv = (x: number) => (x === 0 ? 0 : 55 + x * 40);
  return rgb(conv(r), conv(g), conv(b));
}

function rgb(r: number, g: number, b: number): string {
  return `#${[r, g, b].map((x) => x.toString(16).padStart(2, "0")).join("")}`;
}

interface SGR {
  fg?: string;
  bg?: string;
  bold?: boolean;
  dim?: boolean;
  italic?: boolean;
  underline?: boolean;
  inverse?: boolean;
}

function spanStyle(s: SGR, pal: AnsiPalette): string {
  let fg = s.fg;
  let bg = s.bg;
  // Inverse video (SGR 7) swaps fg and bg; where either is still "default" it
  // has to be materialised, and the defaults come from the flavor — pal.bg is
  // the terminal background and pal.fg the default foreground, the same two
  // colours the live terminal uses. These used to be lola's navy literals,
  // which inverted to the wrong pair on any other palette (and to a dark-on-
  // dark smudge on Latte).
  if (s.inverse) [fg, bg] = [bg ?? pal.bg, fg ?? pal.fg];
  const css: string[] = [];
  if (fg) css.push(`color:${fg}`);
  if (bg) css.push(`background:${bg}`);
  if (s.bold) css.push("font-weight:600");
  if (s.dim) css.push("opacity:.6");
  if (s.italic) css.push("font-style:italic");
  if (s.underline) css.push("text-decoration:underline");
  return css.join(";");
}

function applyCodes(s: SGR, codes: number[], pal: AnsiPalette): SGR {
  let next: SGR = { ...s };
  for (let i = 0; i < codes.length; i++) {
    const c = codes[i];
    switch (true) {
      case c === 0:
        // Reset all attributes IN PLACE and keep going: a combined sequence
        // like ESC[0;31m must apply the 31 (red) after the reset, not return
        // early and drop it.
        next = {};
        break;
      case c === 1:
        next.bold = true;
        break;
      case c === 2:
        next.dim = true;
        break;
      case c === 3:
        next.italic = true;
        break;
      case c === 4:
        next.underline = true;
        break;
      case c === 7:
        next.inverse = true;
        break;
      case c === 22:
        next.bold = false;
        next.dim = false;
        break;
      case c === 23:
        next.italic = false;
        break;
      case c === 24:
        next.underline = false;
        break;
      case c === 27:
        next.inverse = false;
        break;
      case c >= 30 && c <= 37:
        next.fg = pal.ansi16[c - 30];
        break;
      case c === 38:
        if (codes[i + 1] === 5) {
          next.fg = xterm256(codes[i + 2], pal);
          i += 2;
        } else if (codes[i + 1] === 2) {
          next.fg = rgb(codes[i + 2], codes[i + 3], codes[i + 4]);
          i += 4;
        }
        break;
      case c === 39:
        next.fg = undefined;
        break;
      case c >= 40 && c <= 47:
        next.bg = pal.ansi16[c - 40];
        break;
      case c === 48:
        if (codes[i + 1] === 5) {
          next.bg = xterm256(codes[i + 2], pal);
          i += 2;
        } else if (codes[i + 1] === 2) {
          next.bg = rgb(codes[i + 2], codes[i + 3], codes[i + 4]);
          i += 4;
        }
        break;
      case c === 49:
        next.bg = undefined;
        break;
      case c >= 90 && c <= 97:
        next.fg = pal.ansi16[c - 90 + 8];
        break;
      case c >= 100 && c <= 107:
        next.bg = pal.ansi16[c - 100 + 8];
        break;
    }
  }
  return next;
}

// eslint-disable-next-line no-control-regex
const CSI = /\x1b\[([0-9;]*)([A-Za-z])/g;

/**
 * Render an ANSI (SGR-only) snapshot into safe styled HTML.
 *
 * @param pal the flavor's terminal palette — pass `appearance.ansi` from a
 *   component to make the output follow the live theme. Defaults to the default
 *   flavor so this stays callable as a pure one-argument function.
 */
export function ansiToHtml(input: string, pal: AnsiPalette = DEFAULT_PALETTE): string {
  let out = "";
  let state: SGR = {};
  let last = 0;
  const emit = (text: string) => {
    if (!text) return;
    const style = spanStyle(state, pal);
    out += style ? `<span style="${style}">${esc(text)}</span>` : esc(text);
  };
  CSI.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = CSI.exec(input)) !== null) {
    emit(input.slice(last, m.index));
    last = CSI.lastIndex;
    if (m[2] === "m") {
      const codes = m[1] === "" ? [0] : m[1].split(";").map((n) => parseInt(n, 10) || 0);
      state = applyCodes(state, codes, pal);
    }
    // non-'m' CSI (cursor moves etc.) are dropped — capture-pane rarely emits them.
  }
  emit(input.slice(last));
  return out;
}
