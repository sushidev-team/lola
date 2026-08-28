// Turning a resync frame back into bytes a terminal can render.
//
// This is the one adaptation the reuse bet actually required, and it is worth
// being precise about why. On the desktop, `TermService.Attach` runs a real
// `tmux attach` in a PTY and streams its raw bytes to `pty:<name>`; the initial
// screen arrives as bytes like everything else, so LiveTerminal.svelte only
// ever calls `term.write(bytes)`. Over the remote protocol the initial screen
// arrives as a RESYNC frame instead — a structured snapshot of the daemon's
// shadow emulator (cols, rows, rendered lines with SGR intact, cursor position,
// alternate-screen flag, cursor visibility).
//
// The frame exists because an agent runs on the alternate screen, where a fresh
// attach replays nothing: without it a subscriber stares at a blank pane until
// the agent next repaints itself, which for a parked Claude Code session is
// never. A raw byte ring could not replace it either — resuming a ring can
// begin mid-escape-sequence, whereas a rendered frame is parseable from its
// first byte.
//
// So the shim renders that snapshot back into an escape sequence and feeds it
// down the same `pty:<name>` channel as everything else, and LiveTerminal needs
// no change at all. The alternative — teaching the component about resync
// frames — would have meant editing desktop/frontend, which this project does
// not do.
//
// THE ESCAPE SEQUENCE ITSELF IS NOT HERE. It lives in `@mobile/lib/resync`,
// which `MobileTerminal.svelte` also uses, and this module is only the base64
// wrapper the `pty:<name>` event needs.
//
// That is a deliberate correction. There were briefly two independent renderers,
// one per consumer. They agreed on every decision that mattered — alternate
// screen first, auto-wrap off for the repaint, absolute row placement, a per-row
// SGR reset, cursor visibility written in both directions — and differed in how
// they erased, where the per-row reset went, and whether `lines` was clipped to
// `rows`. Nothing pinned them together, so a fix to one would silently not have
// reached the other, and the difference would first show up as "the phone
// repaints correctly and the reused LiveTerminal does not".

import { resyncToBytes } from "@mobile/lib/resync";
import { bytesToBase64, type ResyncPayload } from "../wire";

/**
 * Render a resync snapshot as the escape sequence that reproduces it.
 *
 * A thin alias for `resyncToBytes`, kept because this module's callers speak in
 * terms of the shim's pane path rather than of the app's terminal. See that
 * function for every decision the sequence encodes.
 */
export function renderResync(screen: ResyncPayload): string {
  return resyncToBytes(screen);
}

/**
 * The base64 form of `renderResync`, which is what the `pty:<name>` event
 * carries — LiveTerminal.svelte base64-decodes every payload it receives, so a
 * resync has to arrive in the same encoding as ordinary pane output.
 *
 * The repaint is pure ASCII by construction except for the rendered rows, which
 * may hold any UTF-8 the agent printed, so it is encoded through TextEncoder
 * rather than through `btoa` (which throws above U+00FF).
 */
export function renderResyncBase64(screen: ResyncPayload): string {
  return utf8ToBase64(renderResync(screen));
}

/**
 * UTF-8 string to standard, padded base64, through the wire package's encoder
 * rather than a second copy of one. `btoa` is not an option: the rendered rows
 * carry whatever UTF-8 the agent printed, and `btoa` throws above U+00FF.
 */
export function utf8ToBase64(s: string): string {
  return bytesToBase64(new TextEncoder().encode(s));
}
