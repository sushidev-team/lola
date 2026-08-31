// The app's modal surfaces, as a vocabulary.
//
// WHY A LIST AND NOT A BOOLEAN PER SHEET. Three sheets — the filter overlay,
// the connection settings, the terminal's view settings — were each a local
// `let open = $state(false)` inside the screen that drew them, which is the
// obvious shape and has one consequence nobody notices until a screenshot is
// needed: a sheet that only a tap can open is a sheet no script can reach, and
// `simctl` has no gesture API. Those three were exactly the surfaces the last
// review could not photograph, so three of the five changes it was asked to
// judge were judged from unit tests alone.
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
export const SHEETS = ["filter", "connection", "view", "pane"] as const;

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
