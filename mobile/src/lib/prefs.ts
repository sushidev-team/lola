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

// ---------------------------------------------------------------------------
// Per-pane display labels
// ---------------------------------------------------------------------------
//
// A RENAME IN THIS APP IS A NICKNAME, and the distinction is the whole design.
// The tmux session name IS the pane's identity: the daemon anchors on it
// (resolvePaneName), parses a shell's index out of it (shellIndex), and matches
// its suffix when it tears a session down. Renaming it on the Mac would break
// all three, so nothing here ever reaches a wire field - `paneClose`,
// `paneResize` and every subscribe keep sending `PaneInfo.name`, and the label
// is drawn over the top of the daemon's own "shell 2" on this device only.
//
// KEYED BY PANE NAME, in one JSON entry rather than one entry per pane. The
// name is unique within a daemon (`lola-<project>-<issue>-shell-2`), which is
// all that is needed for as long as this app pairs with exactly one daemon --
// PLAN.md settles that it does. The day a daemon switcher lands, this key needs
// the daemon's identity in it, or two Macs' panes will share nicknames.
//
// A LABEL IS CLEARED WHEN ITS PANE GOES, which is not tidiness: the daemon
// allocates the lowest free shell index, so the next `shellCreate` after a close
// reuses the name that just disappeared. Without the prune, a shell somebody
// opens tomorrow inherits a stranger's nickname and there is nothing on screen
// to explain where it came from. `prunePaneLabels` is called on every SUCCESSFUL
// inventory load and never on a failed one -- a refused or unsupported
// `cmd=panes` means the inventory is unknown, not empty, and pruning against
// nothing would wipe every label the moment the Mac's daemon was too old.

const PANE_LABEL_KEY = "lola.mobile.paneLabels";

/**
 * How long a nickname may be.
 *
 * A tab is `px-3 whitespace-nowrap` inside a horizontal scroller, so a long
 * label neither wraps nor truncates -- it just makes the strip longer, and a
 * strip one tab can fill is a strip in which no other tab can be found.
 */
export const PANE_LABEL_MAX = 24;

/**
 * A label as it will be stored and drawn: one line, trimmed, clipped.
 *
 * Control and format characters are folded to a space rather than dropped, so a
 * pasted newline separates the words either side of it instead of joining them,
 * and a bidi override cannot reorder the strip around it. Svelte escapes the
 * text on the way to the DOM, so this is about LAYOUT rather than safety -- a
 * tab is a single line and anything claiming otherwise breaks the row.
 */
export function normalizePaneLabel(v: string): string {
  return v
    .replace(/[\p{Cc}\p{Cf}]+/gu, " ")
    .replace(/\s+/g, " ")
    .trim()
    .slice(0, PANE_LABEL_MAX);
}

/** Read and VALIDATE the stored map. Anything unrecognised is dropped. */
function readLabels(): Record<string, string> {
  const out: Record<string, string> = {};
  let raw: string | null | undefined;
  try {
    raw = globalThis.localStorage?.getItem(PANE_LABEL_KEY);
  } catch {
    return out; // storage disabled or partitioned
  }
  if (raw === null || raw === undefined || raw === "") return out;

  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return out; // hand-edited, truncated, or written by something else
  }
  if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) return out;

  for (const [pane, label] of Object.entries(parsed as Record<string, unknown>)) {
    if (pane === "" || typeof label !== "string") continue;
    const clean = normalizePaneLabel(label);
    if (clean !== "") out[pane] = clean;
  }
  return out;
}

/** Write the map back, removing the key entirely when nothing is left. */
function writeLabels(m: Record<string, string>): void {
  try {
    if (Object.keys(m).length === 0) globalThis.localStorage?.removeItem(PANE_LABEL_KEY);
    else globalThis.localStorage?.setItem(PANE_LABEL_KEY, JSON.stringify(m));
  } catch {
    /* a nickname is not worth failing a screen over */
  }
}

/**
 * Every nickname this device holds, keyed by tmux pane name.
 *
 * VALIDATED on the way out for the same reason `loadFontSize` clamps there: the
 * stored value outlives the build that wrote it, and a future version that
 * widens the length, or a hand-edited entry, would otherwise reach the strip
 * unchecked.
 */
export function loadPaneLabels(): Record<string, string> {
  return readLabels();
}

/**
 * Give a pane a nickname. An empty or all-whitespace label FORGETS it rather
 * than storing "", so "use the default name" and "clear this field" are the same
 * gesture and the map never holds an entry that renders as nothing.
 */
export function savePaneLabel(pane: string, label: string): void {
  if (pane === "") return;
  const m = readLabels();
  const clean = normalizePaneLabel(label);
  if (clean === "") delete m[pane];
  else m[pane] = clean;
  writeLabels(m);
}

/** Forget one pane's nickname. */
export function clearPaneLabel(pane: string): void {
  savePaneLabel(pane, "");
}

/**
 * Drop every nickname whose pane is no longer listed, and return what survived.
 *
 * It returns the surviving map rather than a boolean so a caller can assign the
 * result straight into its render state: one call, one read, and no second trip
 * through storage to find out what is left.
 */
export function prunePaneLabels(known: readonly string[]): Record<string, string> {
  const live = new Set(known);
  const m = readLabels();
  let changed = false;
  for (const pane of Object.keys(m)) {
    if (live.has(pane)) continue;
    delete m[pane];
    changed = true;
  }
  if (changed) writeLabels(m);
  return m;
}
