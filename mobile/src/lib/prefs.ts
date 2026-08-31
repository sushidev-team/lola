// Per-device display preferences: small, non-secret, and remembered so the phone
// does not forget a setting the moment a screen unmounts.
//
// WHY A SEPARATE MODULE, and why not either of the two places that already
// persist something:
//
//   * secretstore.ts remembers the endpoint, but its whole subject is what may
//     and may not be written in the clear. A font size does not belong in a file
//     whose opening paragraph is about the access key never touching
//     localStorage, and putting it there invites the next reader to assume the
//     rules are the same for both.
//   * viewport.ts owns the arithmetic (FONT_MIN/MAX/DEFAULT and clampFont), and
//     its header promises "pure functions, no DOM" so the trickiest part of the
//     terminal can be exercised in Node. Reaching for a browser global there
//     would cost exactly that property.
//
// So the validator is imported from viewport.ts and the storage lives here. The
// pattern is the one loadEndpoint/saveEndpoint and theme-runtime's flavor cache
// already use, and it is deliberate in three ways: a namespaced key, try/catch
// on BOTH sides because a WKWebView can have storage disabled or partitioned,
// and a tolerant read that validates rather than trusting what it finds. Nothing
// here is worth failing a screen over — every failure degrades to the default.

import { FONT_DEFAULT, clampFont } from "./viewport";

const FONT_KEY = "lola.mobile.termFont";

/**
 * The remembered terminal font size, or the default.
 *
 * CLAMPED on the way out, not just on the way in. The stored value outlives the
 * build that wrote it: a future version that widens the range, a hand-edited
 * entry, or another origin writing this key would otherwise render a terminal at
 * a size this build has no way to display legibly, with no control on screen
 * able to reach it again. clampFont also folds NaN back to FONT_DEFAULT, which
 * covers a key holding something that is not a number at all.
 */
export function loadFontSize(): number {
  try {
    const raw = globalThis.localStorage?.getItem(FONT_KEY);
    if (raw === null || raw === undefined || raw === "") return FONT_DEFAULT;
    return clampFont(Number(raw));
  } catch {
    return FONT_DEFAULT; // storage disabled or partitioned
  }
}

/** Remember a terminal font size. Clamped, so only a legible value is stored. */
export function saveFontSize(size: number): void {
  try {
    globalThis.localStorage?.setItem(FONT_KEY, String(clampFont(size)));
  } catch {
    /* a font size is not worth failing a screen over */
  }
}

/** Forget it, so the next open uses the default. Used by tests. */
export function clearFontSize(): void {
  try {
    globalThis.localStorage?.removeItem(FONT_KEY);
  } catch {
    /* nothing to do */
  }
}
