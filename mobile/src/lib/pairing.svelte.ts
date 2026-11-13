// The hand-off inbox: one pending payload, and who offered it.
//
// WHY A MODULE-LEVEL INBOX RATHER THAN A CALL. A payload can arrive from two
// places and only one of them is the connect screen. An in-app scan happens
// inside `Connect.svelte` and could apply itself directly; a payload from the
// OS URL router arrives whenever iOS decides to deliver it, which may be while
// the user is on Sessions, or during a cold launch before any screen has
// mounted. Both therefore drop the payload HERE, and the connect screen drains
// it — which is what makes a scan and a link converge on one piece of applying
// code instead of two that drift.
//
// `source` IS A SECURITY FIELD, not bookkeeping. A scan is something the user
// did one second ago with the camera in their hand, so it may dial on its own.
// A link is something an arbitrary app asked the OS to deliver, so it may only
// FILL THE FORM and wait for a tap — PLAN.md's objection to URL-routed pairing
// is exactly that anyone can send one, and silently dialling whatever arrives
// would import that problem after going to the trouble of avoiding it.

import type { DevLinkTarget } from "./devlink";
import type { PairPayload, PairSource } from "./pairpayload";

export interface PairOffer {
  payload: PairPayload;
  source: PairSource;
  /**
   * Where the offer asks the app to land once connected, or null for the list.
   *
   * Only a development link ever sets one, and it is applied by whichever
   * connect actually succeeded — never by the offer itself — for the same
   * reason `devLinkActive` is: a flag a link could set for itself would
   * describe nothing.
   */
  target?: DevLinkTarget | null;
}

class Pairing {
  /** The payload waiting to be applied, if any. */
  pending = $state<PairOffer | null>(null);

  /**
   * True while the LIVE connection is one a development URL set up.
   *
   * This is the third fence around `lola-dev://` and it is the app's half of
   * it: the plugin's contract requires a PERSISTENT banner for as long as such
   * a connection is up, which is what makes the scheme a labelled test fixture
   * rather than a hidden back door. `App.svelte` renders it above every screen.
   * It is set by whichever connect actually succeeded, never by the URL — a
   * flag a link could set for itself would label nothing.
   */
  devLinkActive = $state(false);

  /** Hand a payload to whichever screen is in a position to use it. */
  offer(payload: PairPayload, source: PairSource, target: DevLinkTarget | null = null): void {
    this.pending = { payload, source, target };
  }

  /** Take the pending payload, leaving the inbox empty. */
  take(): PairOffer | null {
    const p = this.pending;
    this.pending = null;
    return p;
  }
}

export const pairing = new Pairing();
