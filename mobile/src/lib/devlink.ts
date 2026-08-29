// The development URL, and the banner it obliges the app to show.
//
// WHAT THIS IS FOR. A Simulator has no camera, so the QR hand-off — the whole
// point of the connect screen's primary action — is exactly the path that
// cannot be exercised on the one device an agent or a CI job can drive. The
// plugin therefore accepts a `lola-dev://connect?...` URL and turns it into a
// `devLink` event, so a script can put the app in front of a live daemon with
// `xcrun simctl openurl`. It is a test fixture.
//
// WHAT IT IS NOT. It is not pairing, and it never becomes pairing. PLAN.md
// settles that the pairing payload is an opaque `lola1.` token and deliberately
// NOT a URI, because a custom scheme cannot be claimed exclusively and the
// system camera would hand the secret to whichever app registered it. That
// argument applies here with MORE force, since M1's bearer key is longer-lived
// than M2's `qr_secret`. Three things fence it, and the third one is this
// file's:
//
//   1. the scheme is `lola-dev`, not the app's own;
//   2. every line of the Swift that turns a URL into a connection is compiled
//      out of a release build, so the event simply never fires there;
//   3. THE APP MUST SHOW A PERSISTENT BANNER for as long as a connection that
//      arrived this way is up. That is what makes it a labelled test fixture
//      rather than a hidden back door.
//
// (3) is `devLinkActive` below and the bar `App.svelte` renders from it. Do not
// make that banner dismissible and do not let it be derived from anything the
// URL itself supplies — it is the app's own statement about its own state.
//
// A LINK NEVER DIALS ON ITS OWN, which is the other half of not being a back
// door. The payload lands in the same inbox a scan uses, tagged `link`, and the
// connect screen fills the form and waits for a tap. Anyone can ask iOS to open
// a URL; nobody can make this app connect without somebody looking at what it
// is about to connect to.

import { DEFAULT_REMOTE_PORT } from "./endpoint";
import { normalizePin, type PairPayload, type PairSource } from "./pairpayload";

/** `LolaDevLinkEvent`, as much of it as this module reads. */
export interface DevLinkEvent {
  source?: string;
  host?: string;
  port?: number;
  spkiPin?: string;
  insecureKey?: string;
  /** A tmux pane to open once connected. See `DevLinkTarget`. */
  pane?: string;
  /** The session that pane belongs to. Defaults to `pane`. */
  session?: string;
}

/**
 * Where a link asks the app to land, once it is connected.
 *
 * It exists because the terminal is the screen this app is a bet on and it was
 * the one screen nobody — including a reviewer — could produce a screenshot of:
 * it is reachable only by tapping a session row, `simctl` has no gesture API,
 * and the Simulator's device window is absent from the accessibility tree, so
 * synthetic clicks do not reach it either. A pane name in the launch link makes
 * the app's core surface screenshottable and regression-testable by a script.
 *
 * It grants NOTHING. A destination is only useful to a link that already
 * connected, so it rides inside the fence rather than widening it, and a routed
 * `link` still only fills the form — the target is applied by whichever connect
 * actually succeeded, exactly like the banner flag.
 */
export interface DevLinkTarget {
  pane: string;
  session: string;
}

interface DevLinkCapablePlugin {
  addListener?(
    event: string,
    cb: (e: DevLinkEvent) => void,
  ): Promise<{ remove(): unknown }> | { remove(): unknown };
}

interface CapacitorGlobal {
  Plugins?: { LolaTransport?: DevLinkCapablePlugin };
}

/**
 * One event, as a payload — or null when there is nothing connectable in it.
 *
 * Only the STRUCTURE is checked here. A short key or a malformed pin is left to
 * `validateDraft` on the connect screen, which is the same validator the typed
 * form runs and the only one that can show the problem against the field it
 * belongs to. Refusing here instead would trade a labelled form error for a
 * link that silently does nothing.
 */
/**
 * Which door a link came through, as the source the connect screen acts on.
 *
 * FAILS CLOSED, and that is the whole of this function: only the exact string
 * the plugin uses for its launch route unlocks the one that may dial. Anything
 * else — the URL router's own stamp, a field the plugin did not set, a build
 * whose plugin is older than this app — is `link`, which fills the form and
 * waits for a human. An unrecognised value must never be read as "probably
 * fine".
 */
export function devLinkSource(e: DevLinkEvent | null | undefined): PairSource {
  return e?.source === "dev-launch" ? "launch" : "link";
}

/**
 * The pane a link asks for, or null.
 *
 * Kept OUT of `PairPayload` on purpose: that type is the shape a scanned QR
 * carries, it is shared with the pairing path, and a navigation hint has no
 * business in a credential envelope. The target travels beside the payload on
 * the offer instead.
 */
export function devLinkTarget(e: DevLinkEvent | null | undefined): DevLinkTarget | null {
  const pane = typeof e?.pane === "string" ? e.pane.trim() : "";
  if (pane === "") return null;
  const session = typeof e?.session === "string" ? e.session.trim() : "";
  return { pane, session: session || pane };
}

export function devLinkToPayload(e: DevLinkEvent | null | undefined): PairPayload | null {
  const host = typeof e?.host === "string" ? e.host.trim() : "";
  if (host === "") return null;
  const port =
    typeof e?.port === "number" && Number.isFinite(e.port) && e.port > 0
      ? Math.trunc(e.port)
      : DEFAULT_REMOTE_PORT;
  return {
    addrs: [host],
    port,
    pin: normalizePin(typeof e?.spkiPin === "string" ? e.spkiPin : ""),
    key: typeof e?.insecureKey === "string" ? e.insecureKey : "",
  };
}

function plugin(): DevLinkCapablePlugin | undefined {
  const cap = (globalThis as { Capacitor?: CapacitorGlobal }).Capacitor;
  const p = cap?.Plugins?.LolaTransport;
  return p && typeof p.addListener === "function" ? p : undefined;
}

/**
 * Listen for development links. Returns a teardown; never throws.
 *
 * Registered during startup rather than on the connect screen, because the
 * plugin RETAINS a pending link until something consumes it: a cold launch
 * delivers the URL while the WebView is still loading, and a listener that
 * waited for a screen to mount would be handed a link nobody could use.
 */
export function installDevLink(
  onPayload: (p: PairPayload, source: PairSource, target: DevLinkTarget | null) => void,
): () => void {
  let live = true;
  let handle: { remove(): unknown } | undefined;

  void (async () => {
    const p = plugin();
    if (!p?.addListener) return;
    try {
      const h = await p.addListener("devLink", (e) => {
        if (!live) return;
        const payload = devLinkToPayload(e);
        if (payload) onPayload(payload, devLinkSource(e), devLinkTarget(e));
      });
      if (!live) {
        try {
          void h.remove();
        } catch {
          /* a handle that will not detach is not worth failing a launch over */
        }
      } else {
        handle = h;
      }
    } catch {
      // A release build compiles the whole path out, so the event does not
      // exist and registering for it is expected to be a no-op rather than an
      // error worth reporting.
    }
  })();

  return () => {
    if (!live) return;
    live = false;
    try {
      void handle?.remove();
    } catch {
      /* nothing useful to do on teardown */
    }
  };
}
