// What a pin leaves in storage: the toggle, the shells this device started and
// the breadcrumb of pins not yet confirmed released. `panepin.ts` owns the wire
// lifecycle and writes the breadcrumb in lockstep with its requests.
//
// Invariants:
//  - Every read validates and fails closed (a pin is somebody else's screen:
//    unreadable storage means "off" and "nothing outstanding").
//  - Every write is try/catch: a WKWebView can have storage disabled, and a
//    preference or breadcrumb is not worth failing a screen over.
//  - The breadcrumb is a bounded list, read-modify-write, never overwritten
//    wholesale: an older run's unreleased pin must survive this run's pins.
//  - No DOM, no runes, no transport.

/** Which pane is pinned. The breadcrumb, and the identity half of a target. */
export interface PinRecord {
  session: string;
  pane: string;
}

// ---------------------------------------------------------------------------
// Storage
// ---------------------------------------------------------------------------

const ENABLED_KEY = "lola.mobile.pinPaneSize";
const CRUMB_KEY = "lola.mobile.pinnedPane";

/**
 * Whether the human has turned the pin on. OFF unless the stored value is
 * exactly the string this module writes.
 *
 * FAILS CLOSED, and that is not the usual tolerant-read politeness: the default
 * has to be the one that leaves somebody else's screen alone. A key holding
 * anything unexpected — another build's encoding, a hand edit, a partitioned
 * store — means "off", because the cost of a wrong "off" is a feature the user
 * turns on again and the cost of a wrong "on" is a stranger's window resized
 * without them asking.
 */
export function loadPinEnabled(): boolean {
  try {
    return globalThis.localStorage?.getItem(ENABLED_KEY) === "1";
  } catch {
    return false; // storage disabled or partitioned
  }
}

/** Remember the toggle. */
export function savePinEnabled(on: boolean): void {
  try {
    if (on) globalThis.localStorage?.setItem(ENABLED_KEY, "1");
    else globalThis.localStorage?.removeItem(ENABLED_KEY);
  } catch {
    /* a preference is not worth failing a screen over */
  }
}

/**
 * Panes this device STARTED, which are pinned without anyone asking.
 *
 * WHY A SHELL IS DIFFERENT FROM AN AGENT PANE. The pin reshapes somebody else's
 * screen, which is why it is a deliberate, per-device toggle that defaults to
 * off: an agent pane belongs to the work, a developer may be watching it on the
 * Mac, and narrowing it to phone width without being asked is rude at best. A
 * shell the PHONE created has none of that history. It exists because somebody
 * tapped `+` on a phone, nothing on the Mac is looking at it, and its tmux
 * window is born at whatever size tmux picks — commonly 157x37 — so it opens
 * showing a prompt on row 0 with a void beneath it and no obvious way to make
 * it behave like a terminal. Sizing that one to the phone takes nothing from
 * anybody.
 *
 * Stored per DEVICE and by pane NAME, because that is the only identity a pane
 * has (see the hand-off: a tmux session name is a pane's identity, and a rename
 * is a local label). Bounded, oldest dropped, and validated on read like every
 * other key here — a hand-edited value must not be able to make this phone pin
 * panes it never created.
 */
const OWN_SHELLS_KEY = "lola.mobile.ownShells";

/** How many self-started shells are remembered. Oldest is dropped. */
export const OWN_SHELLS_MAX = 64;

/** Every pane this device started, oldest first. */
export function loadOwnShells(): string[] {
  try {
    const raw = globalThis.localStorage?.getItem(OWN_SHELLS_KEY);
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    const out: string[] = [];
    for (const item of parsed) {
      if (typeof item !== "string" || item === "") continue;
      if (out.includes(item)) continue;
      out.push(item);
      if (out.length >= OWN_SHELLS_MAX) break;
    }
    return out;
  } catch {
    return [];
  }
}

/** Remember that this device started `pane`. Idempotent. */
export function rememberOwnShell(pane: string): void {
  if (pane === "") return;
  const all = loadOwnShells();
  if (all.includes(pane)) return;
  const next = [...all, pane].slice(-OWN_SHELLS_MAX);
  try {
    globalThis.localStorage?.setItem(OWN_SHELLS_KEY, JSON.stringify(next));
  } catch {
    /* a convenience is not worth failing a screen over */
  }
}

/**
 * Forget one — the pane was closed, or the user turned its pin off.
 *
 * Turning the pin off has to reach this list or the switch would be a lie: the
 * next visit would pin the pane again and the toggle would appear to have
 * flipped itself back on.
 */
export function forgetOwnShell(pane: string): void {
  const all = loadOwnShells();
  const kept = all.filter((p) => p !== pane);
  if (kept.length === all.length) return;
  try {
    if (kept.length === 0) globalThis.localStorage?.removeItem(OWN_SHELLS_KEY);
    else globalThis.localStorage?.setItem(OWN_SHELLS_KEY, JSON.stringify(kept));
  } catch {
    /* as above */
  }
}

/** Whether this device started that pane, and should size it to itself. */
export function isOwnShell(pane: string): boolean {
  return pane !== "" && loadOwnShells().includes(pane);
}

/**
 * How many outstanding pins the breadcrumb will hold.
 *
 * There is no legitimate way to reach it: a stray record is only ever created
 * by a release that failed, and the sweep retries every one of them on every
 * apply, every reconnect and every return to a terminal. The cap exists so a
 * daemon that refuses releases indefinitely grows a bounded list rather than an
 * unbounded one, and so a hand-edited key cannot make the sweep walk forever.
 * Reaching it is a bug; the bound just decides how the bug behaves.
 */
export const PIN_CRUMB_MAX = 16;

/**
 * Every pane this device has asked to pin and has not yet confirmed released.
 *
 * A LIST RATHER THAN ONE RECORD, and the plural is the whole point. A release
 * can fail while the pin that follows it succeeds — the socket blinked, the
 * daemon answered one request and not the next — and with one slot the second
 * pin OVERWROTE the first pane's record. Nothing on the phone then knew a
 * developer's window was still squashed, and nothing would ever release it: the
 * single failure this module exists to prevent, arriving through the mechanism
 * meant to prevent it. So a pane is recorded when it is pinned and removed only
 * when its release has actually resolved, however many are outstanding.
 *
 * Validated rather than trusted, item by item: both halves of a record must be
 * non-empty strings, because the only thing done with one is naming a pane in a
 * `paneResize` request, and a half-formed record would spend a round trip to be
 * refused. A LEGACY single object is still read, so a phone updating from the
 * one-slot build does not lose the pin it is holding across the upgrade.
 */
export function loadPinnedPanes(): PinRecord[] {
  let parsed: unknown;
  try {
    const raw = globalThis.localStorage?.getItem(CRUMB_KEY);
    if (!raw) return [];
    parsed = JSON.parse(raw);
  } catch {
    return []; // unreadable storage, or something that is not JSON
  }
  const items = Array.isArray(parsed) ? parsed : [parsed];
  const out: PinRecord[] = [];
  for (const item of items) {
    const v = item as Partial<PinRecord> | null;
    const session = typeof v?.session === "string" ? v.session : "";
    const pane = typeof v?.pane === "string" ? v.pane : "";
    if (session === "" || pane === "") continue;
    if (out.some((r) => r.session === session && r.pane === pane)) continue;
    out.push({ session, pane });
    if (out.length >= PIN_CRUMB_MAX) break;
  }
  return out;
}

/** Write the breadcrumb, or remove the key when nothing is outstanding. */
export function savePinnedPanes(rs: readonly PinRecord[]): void {
  try {
    if (rs.length === 0) {
      globalThis.localStorage?.removeItem(CRUMB_KEY);
      return;
    }
    globalThis.localStorage?.setItem(
      CRUMB_KEY,
      JSON.stringify(
        rs
          .slice(0, PIN_CRUMB_MAX)
          .map((r) => ({ session: r.session, pane: r.pane })),
      ),
    );
  } catch {
    /* a breadcrumb that cannot be written costs the recovery, not the app */
  }
}

/**
 * Record one more outstanding pin, KEEPING everything already recorded.
 *
 * Read-modify-write rather than a write of what this run believes, and the
 * difference is a whole class of lost window. The breadcrumb outlives the
 * process: a run that was force-quit while pinned leaves its record for the
 * NEXT run to sweep, and that sweep (`PanePin.recover`) cannot happen until
 * there is a connection. Writing this run's belief wholesale erased the older
 * record the moment this run pinned anything at all — which is before the sweep
 * on every screen that opens with the toggle already on. The pane from the
 * previous run then stayed squashed forever, one line of code away from the
 * mechanism meant to rescue it.
 */
export function addPinnedPane(r: PinRecord): void {
  const all = loadPinnedPanes();
  if (all.some((x) => x.session === r.session && x.pane === r.pane)) return;
  savePinnedPanes([...all, r]);
}

/** Forget one, leaving every other outstanding pin recorded. */
export function dropPinnedPane(r: PinRecord): void {
  const all = loadPinnedPanes();
  const kept = all.filter((x) => x.session !== r.session || x.pane !== r.pane);
  if (kept.length !== all.length) savePinnedPanes(kept);
}

/** Forget both, so the next launch starts clean. Used by tests. */
export function clearPinState(): void {
  savePinEnabled(false);
  savePinnedPanes([]);
  for (const pane of loadOwnShells()) forgetOwnShell(pane);
}
