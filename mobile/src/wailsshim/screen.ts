// A resync frame rendered back into bytes a terminal can write.
//
// Invariant: the initial screen of a pane arrives as a structured snapshot, not
// as bytes, and the reused LiveTerminal only ever writes bytes, so the snapshot is
// rendered here and pushed down the same `pty:<name>` channel as output. The
// escape sequence itself lives in `@mobile/lib/resync`, shared with
// MobileTerminal so the two renderers cannot drift.

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
