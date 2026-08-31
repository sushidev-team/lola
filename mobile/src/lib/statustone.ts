// The status word's colour on a phone, where the pill that used to carry it is
// gone.
//
// WHY THIS EXISTS. `$lib/theme`'s statusText is the shared vocabulary — a port
// of Go's internal/state that desktop/state_parity_test.go pins byte-identical
// across the three surfaces — and it answers `text-ink` for every status it
// does not name: review_pending, draft, ci_pending, and anything a newer daemon
// invents. On the desktop and in the TUI that is correct, because those
// surfaces draw the word inside a pill whose SHAPE says "this is a status" and
// whose fill supplies the contrast. The phone dropped the pill deliberately
// (badges did not feel right, and a chip parked at the far right of a row put
// the one word that says what is happening as far from the sentence it modifies
// as the row is wide) — and with the shape gone, `text-ink` prints the status in
// exactly the ink of the row's heading, one size smaller. It stops reading as a
// state and starts reading as emphasis: on a review row the word "review" was
// the second-brightest thing on the line.
//
// So the DEFAULT FAMILY, and only the default family, is stepped down to
// `text-faint` here. That is the same tier the row's other secondary facts take
// — the project, the age — which is what the quiet statuses are: true, and not
// news. Every status theme.ts actually names keeps its own colour untouched, so
// needs_input is still orange, the broken family still bad, working still info.
//
// WHAT THIS IS NOT. It is not a second palette and it must never become one. It
// adds no colour, renames nothing, and has exactly one rule: the fallback tier
// is faint rather than ink. A status that needs a NEW colour needs it in
// theme.ts, where all three surfaces read it and the parity test guards it.

import { statusText } from "$lib/theme";

/**
 * The Tailwind text-colour utility for a status word drawn WITHOUT a pill.
 *
 * Identical to `statusText` for every status theme.ts names; `text-faint`
 * instead of `text-ink` for the ones it does not.
 */
export function statusTone(status: string): string {
  const tone = statusText(status);
  return tone === "text-ink" ? "text-faint" : tone;
}
