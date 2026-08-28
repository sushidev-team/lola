// A resync frame, turned back into the escape sequences that repaint it.
//
// WHY A RESYNC FRAME EXISTS AT ALL. An agent runs full-screen, i.e. on the
// terminal's ALTERNATE screen, where a fresh `tmux attach` replays nothing. A
// subscriber that received only the live byte stream would therefore stare at a
// blank pane until the agent happened to repaint — which, for a parked agent
// waiting on a yes/no, is never. So `internal/panebus` keeps a shadow emulator
// per pane and sends every new subscriber its rendered screen first. That frame
// is the whole reason the phone can show a stopped agent at all.
//
// WHY IT IS NOT `capture-pane`. The rendered frame carries the CURSOR, and the
// cursor is precisely what a human needs in order to answer a prompt. It is also
// always coherent: a raw byte ring handed to a waking phone can begin in the
// middle of an escape sequence, and a screen cannot.
//
// This module is pure text in and text out, which is the point: the trickiest
// part of the terminal screen is testable without a Terminal, a socket or a
// device.
//
// THIS IS THE ONLY RENDERER, and it has two consumers:
//
//   MobileTerminal.svelte  the phone's own pane view. It holds a
//                          `PaneSubscription` directly and writes this string
//                          into xterm itself.
//   wailsshim/screen.ts    the reuse path. `TermService.Attach` republishes a
//                          resync as base64 on a `pty:<name>` event so that
//                          desktop/frontend's own LiveTerminal.svelte — which
//                          knows nothing about resync frames and only ever calls
//                          term.write(bytes) — works unmodified.
//
// There were briefly TWO implementations, written independently for those two
// consumers, agreeing on every decision that mattered and differing in how they
// erased, where they put the per-row SGR reset, and whether they clipped `lines`
// to `rows`. Nothing pinned them together, so a fix applied to one would
// silently not have reached the other. This is the survivor; the shim delegates.
//
// The dependency therefore runs shim -> lib for this one module, which is the
// unusual direction. It is deliberate: the repaint is pure text with no
// platform, no Svelte and no runes, it belongs beside the viewport logic
// (`geometryChanged`) that only a panning client needs, and the shim's actual
// job in the pane path is the base64 wrapper, not the escape sequence.

import type { ResyncPayload } from "@mobile/wire/protocol";

/** ESC, then CSI. Local rather than imported from keybytes: this module has no
 *  other reason to pull in the whole key table, and the shim imports it. */
const ESC = "\x1b";
const CSI = `${ESC}[`;

/**
 * The bytes that repaint a terminal from a resync frame.
 *
 * The caller must `reset()` the terminal immediately before writing these. That
 * is deliberate rather than incidental: a repaint of a plain shell pane would
 * otherwise push the previous screen into xterm's scrollback, so the history a
 * user scrolls through would fill with duplicate copies of the same screen — and
 * the scrollback that matters is the DAEMON's (reached by the scroll RPC), never
 * xterm's own, which on an agent pane is correctly empty.
 *
 * The sequence, and why each part is in this order:
 *
 *  1. Alternate screen first. Switching screens after painting would throw the
 *     paint away, since the two screens have separate buffers.
 *  2. Autowrap OFF for the duration. A line that is exactly `cols` wide wraps at
 *     the last cell and scrolls the whole screen up by one, which shifts every
 *     subsequent absolutely-positioned line by one row and puts the cursor in
 *     the wrong place. With DECAWM off the overflow is simply discarded, which
 *     is what the daemon's own emulator did to produce the line.
 *  3. Erase, then paint each line at an ABSOLUTE position. Absolute positioning
 *     rather than CR/LF means a short line cannot pull the next one up and a
 *     long one cannot push it down, so a malformed line costs one row rather
 *     than the whole frame.
 *  4. SGR reset after every line. `lines` carry their own colour and a line that
 *     ends mid-attribute would bleed into the next.
 *  5. Autowrap back ON. This is a GUESS, and the only one here: DECAWM is on by
 *     default and virtually every program leaves it that way, but the resync
 *     payload does not carry the real state, so a program that had turned it off
 *     gets it back until it says otherwise. Leaving it off instead would break
 *     ordinary shell wrapping permanently, which is the worse failure. Same
 *     class of gap as the missing DECCKM state — both belong in the payload.
 *  6. Cursor position, then cursor visibility. Position first so a visible
 *     cursor never appears at the origin for a frame.
 */
export function resyncToBytes(screen: ResyncPayload): string {
  const out: string[] = [];

  out.push(screen.altScreen ? `${CSI}?1049h` : `${CSI}?1049l`);
  out.push(`${CSI}?7l`);
  out.push(`${CSI}0m`);
  out.push(`${CSI}2J`);

  const rows = screen.rows > 0 ? screen.rows : Number.MAX_SAFE_INTEGER;
  // `lines` has its trailing blank rows trimmed daemon-side, so it is normally
  // shorter than `rows` and the remainder is simply the erase above. Clipping
  // the other way protects against a frame that claims more rows than it says
  // it has.
  const lines = (screen.lines ?? []).slice(0, rows);
  for (let i = 0; i < lines.length; i++) {
    out.push(`${CSI}${i + 1};1H`);
    out.push(lines[i]);
    out.push(`${CSI}0m`);
  }

  out.push(`${CSI}?7h`);
  out.push(`${CSI}${clampCell(screen.cursorY) + 1};${clampCell(screen.cursorX) + 1}H`);
  // cursorHidden is stated in the NEGATIVE on the wire, so an absent field means
  // visible — which is both the common case and the safe default against an
  // older daemon that does not send it at all.
  out.push(screen.cursorHidden ? `${CSI}?25l` : `${CSI}?25h`);

  return out.join("");
}

/**
 * A cursor coordinate that can be turned into a CSI parameter.
 *
 * Cursor coordinates are zero-based cells on the wire and one-based in CSI, so
 * they are incremented by the caller. A negative or non-integer value would
 * produce a malformed sequence that the emulator discards along with whatever
 * follows it in the same write, so it is clamped rather than trusted: this
 * number crossed a network.
 */
function clampCell(n: number): number {
  if (!Number.isFinite(n) || n < 0) return 0;
  return Math.floor(n);
}

/**
 * Whether a new frame's geometry differs from the terminal's current grid.
 *
 * The phone never reflows: it renders the daemon's full grid and pans over it,
 * so this is the only thing that ever changes `cols`/`rows`, and it changes them
 * to whatever the developer's tmux window happens to be. A desktop client
 * attaching, detaching or resizing moves that window under the phone's feet, and
 * the daemon answers with a fresh resync — which is exactly the case this
 * predicate exists to catch.
 */
export function geometryChanged(
  current: { cols: number; rows: number },
  screen: ResyncPayload,
): boolean {
  return (
    screen.cols > 0 &&
    screen.rows > 0 &&
    (current.cols !== screen.cols || current.rows !== screen.rows)
  );
}
