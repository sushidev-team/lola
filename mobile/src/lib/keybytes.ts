// THE key table. Every byte the phone ever sends to an agent's PTY is decided
// here and nowhere else.
//
// This module exists because a wrong byte is the worst bug this app can have.
// It is silent: nothing errors, nothing logs, the key simply does not do what
// its label says. And it is undiagnosable on a device, because the only place
// the mistake is visible is inside a coding agent's own input handling, eight
// layers away from the button that was tapped. So the encodings live in ONE
// table, in ONE module, with a comment per entry naming the program that reads
// the bytes, and the table is covered by tests that assert the exact string.
//
// Two rules shape everything below.
//
// RULE 1 — arrows are NOT constants. `\x1b[A` is correct only while the program
// has left DECCKM (DEC Cursor Key Mode, CSI ? 1 h) alone; once an application
// sets it, the same key is `\x1bOA`, and a real emulator learns which by
// watching the OUTPUT stream. So every cursor-ish key in the table stores its
// FINAL byte rather than a finished sequence, and `bytesFor` assembles it from
// the terminal's live mode. This is not a nicety: the AskUserQuestion picker is
// arrow-plus-Enter driven, and it is the second most important thing on this
// phone after a yes/no.
//
//   A DELIBERATE DEVIATION FROM PLAN.md, stated here so it can be overruled.
//   The plan prescribes synthesizing a KeyboardEvent into xterm.js so xterm
//   resolves the mode. This module reads the same mode off xterm instead
//   (`Terminal.modes.applicationCursorKeysMode`, public API since 5.x) and
//   encodes here. The mode is still learned from the output stream by xterm's
//   parser — that half is untouched, and it is the half the plan was protecting.
//   What changes is that the encoding is a pure function of (key, mode), so it
//   can be asserted byte for byte in vitest, whereas a synthetic event's journey
//   through xterm's internals cannot be. Given that wrong bytes here are the
//   most likely bug in the app and the hardest to see, testability won.
//   The forms below are transcribed from the same source xterm's own soft
//   keyboard path uses (`@xterm/xterm` src/common/input/Keyboard.ts), so the two
//   input routes into one pane cannot disagree.
//
// RULE 2 — a fixed entry is fixed. Where a sequence genuinely does not depend on
// terminal state (Escape, Tab, Shift-Tab, the control characters) it is written
// out in full and never assembled, because assembling an invariant is how a
// typo gets somewhere a test is not looking.
//
// KNOWN M1 GAP, and it is the daemon's, not this table's: `ResyncPayload`
// carries no DECCKM state. After a fresh subscribe or a repair the terminal is
// reset, so `applicationCursorKeysMode` reads false and the arrows go out in
// their normal form until the agent next emits the mode. A pane repainted
// mid-picker can therefore take one wrong-form arrow. The fix belongs in the
// resync payload; nothing on this side can infer it.

// ---------------------------------------------------------------------------
// C0 and friends
// ---------------------------------------------------------------------------

/** ESC, 0x1b. The first byte of every escape sequence below. */
export const ESC = "\x1b";
/** CR, 0x0d. What Enter sends — never LF. See the `enter` entry. */
export const CR = "\r";
/** HT, 0x09. Tab. */
export const HT = "\t";
/** DEL, 0x7f. What Backspace sends — never BS (0x08). See `backspace`. */
export const DEL = "\x7f";
/** NUL, 0x00. Ctrl-Space / Ctrl-@. */
export const NUL = "\x00";

// ---------------------------------------------------------------------------
// Terminal state the encoding depends on
// ---------------------------------------------------------------------------

/**
 * The two modes an encoder has to know about, named exactly as xterm.js's
 * public `Terminal.modes` names them so a caller can pass `term.modes` straight
 * in. Both are learned from the program's OUTPUT; neither can be guessed.
 */
export interface TerminalModes {
  /** DECCKM. Set by full-screen applications; changes what the arrows send. */
  applicationCursorKeysMode: boolean;
  /** DECSET 2004. Set by shells and composers that want pastes delimited. */
  bracketedPasteMode: boolean;
}

/**
 * What a terminal that has said nothing is in. Also what a freshly reset
 * terminal is in, which is the state every resync repaint leaves behind.
 */
export const DEFAULT_MODES: TerminalModes = {
  applicationCursorKeysMode: false,
  bracketedPasteMode: false,
};

/**
 * Modifiers held (or, on this phone, LATCHED) while a key is pressed.
 *
 * A modifier you must hold down is unusable on glass, so the accessory bar
 * latches instead: tap Ctrl, then c, and the next ordinary key consumes the
 * modifier and clears it. The latch state arrives here as this record.
 */
export interface KeyModifiers {
  ctrl?: boolean;
  alt?: boolean;
  shift?: boolean;
}

// ---------------------------------------------------------------------------
// The table
// ---------------------------------------------------------------------------

/**
 * How one key turns into bytes.
 *
 *   fixed   the sequence is a constant, whatever the terminal is doing.
 *   cursor  a CSI/SS3 key whose final byte is stored and whose PREFIX depends
 *           on DECCKM (and on any held modifier). Arrows, Home and End.
 *   tilde   a CSI <n> ~ key. Page Up and Page Down.
 */
export type Encoding =
  | { readonly kind: "fixed"; readonly bytes: string }
  | { readonly kind: "cursor"; readonly final: "A" | "B" | "C" | "D" | "H" | "F" }
  | { readonly kind: "tilde"; readonly num: number };

export type KeyId =
  | "escape"
  | "tab"
  | "shiftTab"
  | "enter"
  | "shiftEnter"
  | "backspace"
  | "up"
  | "down"
  | "left"
  | "right"
  | "home"
  | "end"
  | "pageUp"
  | "pageDown"
  | "ctrlC"
  | "ctrlD"
  | "ctrlZ"
  | "ctrlR";

/**
 * The table. One entry per named key the accessory bar can send, each with the
 * program that reads it.
 *
 * Anything a human can also type on the soft keyboard (letters, digits,
 * punctuation, "y", "n") is NOT here: those go through `textBytes`, which is the
 * same path a latched modifier applies to. The table is for keys a soft keyboard
 * does not have.
 */
export const KEY_TABLE: Readonly<Record<KeyId, Encoding>> = {
  // Read by every full-screen TUI as "cancel / close / back". In Claude Code it
  // dismisses the modal overlay (the `▔` rule the daemon's pane classifier
  // reports as ActivityBlocked) and interrupts a picker. It is also the first
  // byte of every sequence below, which is why a bare Escape is followed by
  // nothing and must not be coalesced with a key that arrives right after it.
  escape: { kind: "fixed", bytes: ESC },

  // Read by the agent's completion and by readline. Plain HT, no escape.
  tab: { kind: "fixed", bytes: HT },

  // CSI Z, "cursor backward tabulation". Read by Claude Code to CYCLE PERMISSION
  // MODES, which is the whole reason it is on the first row of the bar and not
  // hidden behind the collapsible one. No soft keyboard can produce it.
  shiftTab: { kind: "fixed", bytes: ESC + "[Z" },

  // CR, not LF. A cooked-mode shell has ICRNL and maps CR to NL itself; a
  // full-screen agent reads the CR directly and treats a bare LF as Ctrl-J,
  // which is a different key. Sending "\n" here submits nothing and inserts a
  // stray control character.
  enter: { kind: "fixed", bytes: CR },

  // ESC CR — meta+Enter. Read by Claude Code's composer as "insert a line break,
  // do not submit". This is the pair Claude Code's own `/terminal-setup` teaches
  // a native terminal to send for Shift+Enter, and the desktop's LiveTerminal
  // writes exactly these two bytes for the same reason. Nothing in the stack
  // consults shift for Enter on its own, so if this key is not on the bar the
  // capability does not exist on a phone at all.
  shiftEnter: { kind: "fixed", bytes: ESC + CR },

  // DEL (0x7f), not BS (0x08). Read by readline and by every agent composer as
  // "erase the character behind the cursor". BS is Ctrl-H, which readline binds
  // to the same action but agents generally do not, so the two are not
  // interchangeable and the wrong one deletes nothing.
  backspace: { kind: "fixed", bytes: DEL },

  // The four arrows. Read by the AskUserQuestion picker to move the selection,
  // and by every composer for history and cursor movement. Stored as final bytes
  // ONLY: see RULE 1. Up/Down are also what walks an agent's input history, which
  // is why xterm's alternate-screen wheel fallback (which converts a wheel into
  // these) is disabled in the terminal component — scrolling must never look like
  // pressing these.
  up: { kind: "cursor", final: "A" },
  down: { kind: "cursor", final: "B" },
  right: { kind: "cursor", final: "C" },
  left: { kind: "cursor", final: "D" },

  // Read by readline as beginning-of-line / end-of-line, and by TUI list widgets
  // as first/last. Mode-dependent in exactly the same way as the arrows.
  home: { kind: "cursor", final: "H" },
  end: { kind: "cursor", final: "F" },

  // CSI 5 ~ / CSI 6 ~. Read by pagers and by scrollable TUI panes. NOT affected
  // by DECCKM — the tilde forms are the same in both modes.
  pageUp: { kind: "tilde", num: 5 },
  pageDown: { kind: "tilde", num: 6 },

  // ETX, 0x03. Read by the tty line discipline as INTR: it raises SIGINT on the
  // foreground process group. This is the one action that is legitimate
  // mid-turn, and it is the reason the phone's live terminal deliberately
  // bypasses lola's AtPrompt gate — a phone that could not interrupt a running
  // agent would not be worth carrying.
  ctrlC: { kind: "fixed", bytes: "\x03" },

  // EOT, 0x04. Read by the line discipline as EOF at a cooked prompt, and by
  // most REPLs as "quit". Destructive enough to live on the collapsible row.
  ctrlD: { kind: "fixed", bytes: "\x04" },

  // SUB, 0x1a. Read by the line discipline as SUSP: SIGTSTP, which stops the
  // foreground job. On a phone there is no `fg` to type afterwards without the
  // shell tab, so this is a row-two key by intent.
  ctrlZ: { kind: "fixed", bytes: "\x1a" },

  // DC2, 0x12. Read by readline as reverse-i-search, and by several TUIs as
  // "refresh". Included because searching back through a long command is
  // otherwise impossible without arrow-key spam.
  ctrlR: { kind: "fixed", bytes: "\x12" },
} as const;

// ---------------------------------------------------------------------------
// Encoding
// ---------------------------------------------------------------------------

/**
 * xterm's modifier parameter: 1 plus a bitmask of shift(1), alt(2), ctrl(4).
 * A value of 1 means "no modifier", and in that case the UNMODIFIED form is
 * sent rather than an explicit `;1`.
 */
export function modifierParam(mods: KeyModifiers = {}): number {
  return 1 + (mods.shift ? 1 : 0) + (mods.alt ? 2 : 0) + (mods.ctrl ? 4 : 0);
}

/**
 * The bytes for one named key, given the terminal's live modes and any latched
 * modifiers.
 *
 * The modified forms (`CSI 1 ; 5 A` for Ctrl-Left, and so on) are the standard
 * ones and match xterm.js's own encoder, with one deliberate omission: xterm
 * rewrites Alt+Left/Right into `ESC b` / `ESC f` on macOS. That rewrite exists
 * because macOS terminals conventionally send meta that way; it is a HOST
 * platform decision and this bar runs on a phone, so the plain modified form is
 * sent instead. No soft keyboard can produce Alt+arrow, so only this table can
 * reach that branch at all.
 */
export function bytesFor(
  id: KeyId,
  modes: TerminalModes = DEFAULT_MODES,
  mods: KeyModifiers = {},
): string {
  const entry = KEY_TABLE[id];
  const m = modifierParam(mods);

  switch (entry.kind) {
    case "cursor":
      if (m !== 1) return `${ESC}[1;${m}${entry.final}`;
      // SS3 rather than CSI once the application has asked for it.
      return modes.applicationCursorKeysMode
        ? `${ESC}O${entry.final}`
        : `${ESC}[${entry.final}`;

    case "tilde":
      return m !== 1 ? `${ESC}[${entry.num};${m}~` : `${ESC}[${entry.num}~`;

    case "fixed":
      // A latched ALT means meta, and meta is an ESC prefix. A latched CTRL is
      // ignored: a fixed entry already names its exact bytes, and there is no
      // general "control form" of a sequence — Ctrl-Escape is not a thing, and
      // the control characters that DO exist have their own entries above.
      return mods.alt ? ESC + entry.bytes : entry.bytes;
  }
}

/**
 * The control character for one ordinary character, or null when it has none.
 *
 * Transcribed from xterm.js's own keydown path so the bar's Ctrl latch and a
 * hardware keyboard's Ctrl produce identical bytes into the same pane:
 *
 *   @ A-Z [ \ ] ^ _   ->  code - 0x40      (so Ctrl-C is 0x03, Ctrl-[ is ESC)
 *   a-z               ->  upper - 0x40     (case-insensitive, as every tty is)
 *   space             ->  NUL
 *   ?                 ->  DEL
 *   3 4 5 6 7         ->  0x1b..0x1f       (the shifted punctuation row's C0s)
 *   8                 ->  DEL
 *
 * Anything else returns null, and the caller sends the character unmodified —
 * dropping it would make the latch feel broken, and inventing a byte for it
 * would be worse than sending the letter.
 */
export function controlByte(ch: string): string | null {
  if (ch.length !== 1) return null;
  // ASCII only, checked BEFORE the case fold. `toUpperCase` is Unicode-aware and
  // both lengthens and remaps: "\u00df" (sharp s) uppercases to "SS", whose first
  // char yields 0x13 — Ctrl-S, which XOFFs the tty and looks to the user like the
  // terminal froze — and "\u0131" (dotless i) uppercases to "I", yielding Tab.
  // Neither is a control the human asked for, and a wrong byte is worse here than
  // no byte: the caller sends the character unmodified when this returns null.
  if (ch.charCodeAt(0) >= 0x80) return null;
  if (ch === " ") return NUL;
  if (ch === "?") return DEL;
  if (ch === "8") return DEL;
  if (ch >= "3" && ch <= "7") {
    return String.fromCharCode(ch.charCodeAt(0) - 0x33 + 0x1b);
  }
  const code = ch.toUpperCase().charCodeAt(0);
  if (code >= 0x40 && code <= 0x5f) return String.fromCharCode(code - 0x40);
  return null;
}

/**
 * Whether a payload is already a bracketed paste.
 *
 * xterm.js wraps a paste itself, in CoreService, so anything reaching the app's
 * own transform through `onData` is already `CSI 200~ ... CSI 201~`. A latched
 * alt would put ESC in front of that wrapper and a latched ctrl would apply to
 * the wrapper's first byte rather than to the pasted text, so the transform has
 * to recognise one and leave it alone.
 */
export function isBracketedPaste(data: string): boolean {
  return data.startsWith(`${ESC}[200~`);
}

/**
 * The bytes for literal text the human produced — a soft-keyboard character, a
 * "y"/"n" key on the bar — with any latched modifiers applied.
 *
 * Ctrl applies to the FIRST character only, which is what a tty does: Ctrl is a
 * property of one keystroke, not of a string. Alt prefixes the whole thing with
 * ESC, which is how meta has always been transmitted on a 7-bit line.
 */
export function textBytes(text: string, mods: KeyModifiers = {}): string {
  if (text === "") return "";
  let out = text;
  if (mods.ctrl) {
    const c = controlByte(text[0]);
    if (c !== null) out = c + text.slice(1);
  }
  if (mods.alt) out = ESC + out;
  return out;
}

/**
 * Bracketed-paste wrapper: `CSI 200 ~` text `CSI 201 ~`.
 *
 * NOT on the app's paste path: xterm.js does its own bracketing in CoreService,
 * so a paste arrives at `onData` already wrapped and `isBracketedPaste` is what
 * the transform uses to leave it alone. This is here for a caller that writes to
 * a pane WITHOUT going through xterm — the shim's `TermService.Write` shape, and
 * anything M2 adds that pushes text at a pane directly — where nothing else
 * would wrap it.
 *
 * Applied ONLY when the program asked for it. A composer that set the mode reads
 * the wrapper as "this is one block, do not act on the newlines inside", which
 * is what stops a pasted stack trace from submitting at its first line. A program
 * that did NOT set the mode has no idea what those sequences are and would print
 * them, so wrapping unconditionally is worse than not wrapping at all.
 *
 * The markers are also stripped from the payload before wrapping: text
 * containing its own terminator would otherwise end the paste early and hand the
 * remainder to the program as keystrokes.
 */
export function pasteBytes(text: string, modes: TerminalModes = DEFAULT_MODES): string {
  if (text === "") return "";
  if (!modes.bracketedPasteMode) return text;
  const clean = text.split(`${ESC}[200~`).join("").split(`${ESC}[201~`).join("");
  return `${ESC}[200~${clean}${ESC}[201~`;
}

// ---------------------------------------------------------------------------
// What the bar draws
// ---------------------------------------------------------------------------

/** One key as the accessory bar renders it. */
export interface BarKey {
  /** A table key, or "text" for a literal, or "ctrl"/"alt" for a latch toggle. */
  readonly kind: "key" | "text" | "latch";
  /** For kind "key". */
  readonly id?: KeyId;
  /** For kind "text": the literal character. For "latch": "ctrl" | "alt". */
  readonly value?: string;
  /** What the button shows. */
  readonly label: string;
  /** Screen-reader name, since most labels are glyphs. */
  readonly aria: string;
  /** Whether holding the key repeats it. Arrows and backspace only. */
  readonly repeats?: boolean;
  /**
   * Exempt from the mid-turn "send anyway" confirmation. Interrupting is the
   * legitimate mid-turn action, and putting friction in front of it would
   * recreate exactly the uselessness that friction is trying to avoid.
   */
  readonly interrupt?: boolean;
}

/**
 * Row one: the keys you cannot answer a prompt without. Always visible.
 * Matches PLAN.md's accessory-bar layout.
 */
export const BAR_ROW_PRIMARY: readonly BarKey[] = [
  { kind: "key", id: "escape", label: "esc", aria: "Escape", interrupt: true },
  { kind: "key", id: "tab", label: "tab", aria: "Tab" },
  { kind: "key", id: "shiftTab", label: "⇧tab", aria: "Shift Tab" },
  { kind: "key", id: "up", label: "↑", aria: "Up arrow", repeats: true },
  { kind: "key", id: "down", label: "↓", aria: "Down arrow", repeats: true },
  { kind: "key", id: "left", label: "←", aria: "Left arrow", repeats: true },
  { kind: "key", id: "right", label: "→", aria: "Right arrow", repeats: true },
  { kind: "key", id: "enter", label: "⏎", aria: "Enter" },
  { kind: "key", id: "shiftEnter", label: "⇧⏎", aria: "Shift Enter, insert a line break" },
];

/**
 * Row two: everything else. Collapsible, because two permanent rows over a soft
 * keyboard leave almost no pane visible on a 390-point screen.
 */
export const BAR_ROW_SECONDARY: readonly BarKey[] = [
  { kind: "latch", value: "ctrl", label: "ctrl", aria: "Control modifier" },
  { kind: "latch", value: "alt", label: "alt", aria: "Alt modifier" },
  { kind: "key", id: "ctrlC", label: "^C", aria: "Control C, interrupt", interrupt: true },
  { kind: "key", id: "ctrlD", label: "^D", aria: "Control D, end of input" },
  { kind: "key", id: "ctrlZ", label: "^Z", aria: "Control Z, suspend" },
  { kind: "key", id: "ctrlR", label: "^R", aria: "Control R, search history" },
  { kind: "key", id: "backspace", label: "⌫", aria: "Backspace", repeats: true },
  { kind: "key", id: "home", label: "home", aria: "Home" },
  { kind: "key", id: "end", label: "end", aria: "End" },
  { kind: "key", id: "pageUp", label: "pgup", aria: "Page up", repeats: true },
  { kind: "key", id: "pageDown", label: "pgdn", aria: "Page down", repeats: true },
  { kind: "text", value: "y", label: "y", aria: "Letter y" },
  { kind: "text", value: "n", label: "n", aria: "Letter n" },
];

/** Long-press: time to the FIRST repeat, then the interval between repeats. */
export const REPEAT_DELAY_MS = 200;
export const REPEAT_INTERVAL_MS = 80;

/**
 * The bytes one bar key produces. The single entry point the UI calls, so the UI
 * never touches KEY_TABLE and can never assemble a sequence of its own.
 */
export function barKeyBytes(
  key: BarKey,
  modes: TerminalModes = DEFAULT_MODES,
  mods: KeyModifiers = {},
): string {
  if (key.kind === "key" && key.id) return bytesFor(key.id, modes, mods);
  if (key.kind === "text" && key.value !== undefined) return textBytes(key.value, mods);
  return ""; // a latch toggle sends nothing; it changes `mods` for the NEXT key
}
