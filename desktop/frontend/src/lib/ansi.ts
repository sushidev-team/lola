// A tiny ANSI-SGR → HTML renderer for the sessions overview grid. tmux
// `capture-pane -p -e` emits the visible screen as text with SGR color/attribute
// escapes only (no cursor motion), so a small SGR state machine is enough to turn
// a snapshot into styled <span>s. This lets the grid show 40 live-ish terminal
// tiles as plain DOM — far cheaper than 40 xterm instances, and well under the
// ~16 WebGL-context ceiling we reserve for the one focused terminal.
//
// All text is HTML-escaped; only style attributes are emitted, so the result is
// safe to inject with {@html} even though pane content is agent-influenced.

// Catppuccin Mocha — matches the live terminal (LiveTerminal.svelte) so the grid
// snapshots and the focused terminal share one palette.
const ANSI_16 = [
  "#45475a", // 0 black (surface1)
  "#f38ba8", // 1 red
  "#a6e3a1", // 2 green
  "#f9e2af", // 3 yellow
  "#89b4fa", // 4 blue
  "#f5c2e7", // 5 magenta (pink)
  "#94e2d5", // 6 cyan (teal)
  "#bac2de", // 7 white (subtext1)
  "#585b70", // 8 bright black (surface2)
  "#f38ba8", // 9 bright red
  "#a6e3a1", // 10 bright green
  "#f9e2af", // 11 bright yellow
  "#89b4fa", // 12 bright blue
  "#f5c2e7", // 13 bright magenta
  "#94e2d5", // 14 bright cyan
  "#a6adc8", // 15 bright white (subtext0)
];

function esc(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

function xterm256(n: number): string {
  if (n < 16) return ANSI_16[n];
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

function spanStyle(s: SGR): string {
  let fg = s.fg;
  let bg = s.bg;
  if (s.inverse) [fg, bg] = [bg ?? "#0e1420", fg ?? "#c3cbd6"];
  const css: string[] = [];
  if (fg) css.push(`color:${fg}`);
  if (bg) css.push(`background:${bg}`);
  if (s.bold) css.push("font-weight:600");
  if (s.dim) css.push("opacity:.6");
  if (s.italic) css.push("font-style:italic");
  if (s.underline) css.push("text-decoration:underline");
  return css.join(";");
}

function applyCodes(s: SGR, codes: number[]): SGR {
  const next = { ...s };
  for (let i = 0; i < codes.length; i++) {
    const c = codes[i];
    switch (true) {
      case c === 0:
        return {};
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
        next.fg = ANSI_16[c - 30];
        break;
      case c === 38:
        if (codes[i + 1] === 5) {
          next.fg = xterm256(codes[i + 2]);
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
        next.bg = ANSI_16[c - 40];
        break;
      case c === 48:
        if (codes[i + 1] === 5) {
          next.bg = xterm256(codes[i + 2]);
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
        next.fg = ANSI_16[c - 90 + 8];
        break;
      case c >= 100 && c <= 107:
        next.bg = ANSI_16[c - 100 + 8];
        break;
    }
  }
  return next;
}

// eslint-disable-next-line no-control-regex
const CSI = /\x1b\[([0-9;]*)([A-Za-z])/g;

/** Render an ANSI (SGR-only) snapshot into safe styled HTML. */
export function ansiToHtml(input: string): string {
  let out = "";
  let state: SGR = {};
  let last = 0;
  const emit = (text: string) => {
    if (!text) return;
    const style = spanStyle(state);
    out += style ? `<span style="${style}">${esc(text)}</span>` : esc(text);
  };
  CSI.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = CSI.exec(input)) !== null) {
    emit(input.slice(last, m.index));
    last = CSI.lastIndex;
    if (m[2] === "m") {
      const codes = m[1] === "" ? [0] : m[1].split(";").map((n) => parseInt(n, 10) || 0);
      state = applyCodes(state, codes);
    }
    // non-'m' CSI (cursor moves etc.) are dropped — capture-pane rarely emits them.
  }
  emit(input.slice(last));
  return out;
}
