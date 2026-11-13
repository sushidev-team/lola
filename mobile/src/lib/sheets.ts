// The app's modal surfaces, as a vocabulary.
//
// WHY A LIST AND NOT A BOOLEAN PER SHEET. The filter overlay, the connection
// settings and the terminal's view settings were each a local
// `let open = $state(false)` inside the screen that drew them, which is the
// obvious shape and has one consequence nobody notices until a screenshot is
// needed: a sheet that only a tap can open is a sheet no script can reach, and
// `simctl` has no gesture API. Those three were exactly the surfaces the last
// review could not photograph, so three of the five changes it was asked to
// judge were judged from unit tests alone.
//
// THE RULE IS "EVERY SHEET", NOT "EVERY SHEET THAT IS HARD TO OPEN". The
// terminal's session menu arrived as a local because it opens from an ordinary
// tap on a 44-point button, unlike the pane menu's long press — which is a fair
// reading of the paragraph above and still the wrong answer. What the
// vocabulary buys is a script's ability to PHOTOGRAPH a surface, and a plain
// tap is exactly as unreachable to `simctl` as a long press is; the session
// menu now holds most of this screen's actions, so leaving it out meant the
// richest overlay in the app was the one nobody could picture. Naming it also
// makes `nav.toSessions()` close it for free, which the local needed two
// hand-written resets to achieve.
//
// Naming them makes the open sheet a piece of NAVIGATION — a place the app can
// be in — which a development link can then ask for, in the same way and behind
// the same fence as the pane target it already carries. See devlink.ts for that
// fence; it grants nothing, because opening a sheet is something the person
// holding the phone could do with one tap.
//
// It is a plain module with no runes on purpose: `nav` owns the state and
// `devlink` validates against the vocabulary, and neither should have to import
// the other.

/** Every sheet that can be addressed by name. */
//
// TWO NAMES HAVE BEEN RETIRED, both because the sheet behind them stopped
// existing rather than because anything became unaddressable:
//
//   "view"        the terminal's view settings had their own header button and
//                 their own sheet. The header carried that glyph beside the menu
//                 glyph — 88 points of chrome on the screen with the least to
//                 give, on the row where the issue key was the only item allowed
//                 to shorten — so the sections moved into "menu".
//   "connection"  the sessions header's Mac sheet held the connected-to line,
//                 disconnect, forget and the nickname. All four live on the
//                 Settings TAB, which a link reaches as `tab=settings`; a place
//                 does not need a shortcut from another screen's header.
export const SHEETS = ["filter", "pane", "menu"] as const;

/** An addressable sheet, or "" for none open. */
export type SheetName = (typeof SHEETS)[number] | "";

/**
 * Narrow a string to a sheet name, FAILING CLOSED.
 *
 * Anything unrecognised — a typo, a sheet a later build added, a value from a
 * link written against a different version of the app — is not a sheet, and the
 * caller lands with nothing open rather than with something unexpected in front
 * of the screen it asked for.
 */
export function isSheetName(v: string): v is Exclude<SheetName, ""> {
  return (SHEETS as readonly string[]).includes(v);
}
