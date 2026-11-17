// The pairing flow behind the connect screen (spec §1.5, §5.2).
//
// Invariants:
//  - Nothing secret is here. The scanned payload is held natively under an
//    opaque `pairRef`; this model holds the ref, the display code and the view.
//  - The code is shown only after `pairBegin` resolved, i.e. after the proof was
//    written; the Mac, not this phone, confirms it.
//  - Every native outcome ends in exactly one view, and a finished pairing
//    forgets its ref so leaving the screen never cancels a pairing that ended.
//  - The idle timer is held off only while a pairing waits (auto-lock would
//    background the app and fail it as `phone_left`), and is always released.
//  - A pairing outcome for a ref this screen did not start (a development
//    launch the plugin ran itself) is applied like our own.

import type { PairingEvent } from "lola-transport";
import { transport } from "@mobile/native";
import { connection } from "./connection.svelte";
import { haptic } from "./haptics";
import {
  invalidNotice,
  outcomeState,
  pairError,
  type PairNotice,
} from "./paircopy";
import { router, type ConnectState } from "./router.svelte";
import { scanCapability, scanOnce } from "./scan";

/** The name the Mac shows for this phone. */
export function deviceName(userAgent: string = globalThis.navigator?.userAgent ?? ""): string {
  return /iPad/i.test(userAgent) ? "iPad" : "iPhone";
}

class Pairing {
  view = $state<ConnectState>("scan");
  /** A message over the scan view; null when the view says its own words. */
  notice = $state<PairNotice | null>(null);
  /** The six digits to type on the Mac, once the proof is written. */
  code = $state("");
  /** True from the scan until the code is shown or the attempt ended. */
  busy = $state(false);

  #ref = "";
  #awake = false;
  #live = false;
  #handle: { remove(): unknown } | undefined;

  /** Starts the screen: a scan state presents the scanner, any other state is shown as given. */
  open(initial: ConnectState = "scan"): void {
    this.close();
    this.view = initial;
    this.notice = null;
    this.code = "";
    this.#live = true;
    void transport
      .addListener("pairing", (e) => this.#outcome(e))
      .then((h) => {
        if (this.#live) this.#handle = h;
        else void h.remove();
      })
      .catch(() => {});
    if (initial === "scan") void this.scan();
  }

  /** Leaves the screen: abandons a pairing still waiting and releases everything. */
  close(): void {
    if (this.#ref !== "") void transport.pairCancel({ pairRef: this.#ref }).catch(() => {});
    this.#ref = "";
    this.#live = false;
    void this.#handle?.remove();
    this.#handle = undefined;
    this.#hold(false);
    this.busy = false;
  }

  /** Back to the scan and presents the scanner. */
  async rescan(): Promise<void> {
    if (this.busy) return;
    if (this.#ref !== "") {
      void transport.pairCancel({ pairRef: this.#ref }).catch(() => {});
      this.#ref = "";
      this.#hold(false);
    }
    this.view = "scan";
    this.notice = null;
    this.code = "";
    await this.scan();
  }

  /** One scan; a readable code goes straight on to the pairing. */
  async scan(): Promise<void> {
    if (this.busy || this.view === "code") return;
    this.busy = true;
    this.notice = null;
    try {
      const cap = await scanCapability();
      if (!cap.available) {
        if (cap.reason === "denied") this.notice = "cameraOff";
        else this.view = "no-camera";
        return;
      }
      const r = await scanOnce();
      switch (r.kind) {
        case "scanned":
          await this.#begin(r.pairRef);
          return;
        case "invalid":
          this.notice = invalidNotice(r.why);
          return;
        case "denied":
          this.notice = "cameraOff";
          return;
        case "restricted":
        case "unavailable":
          this.view = "no-camera";
          return;
        case "failed":
          this.view = "failed";
          return;
        case "cancelled":
          return;
      }
    } finally {
      this.busy = false;
    }
  }

  async #begin(pairRef: string): Promise<void> {
    this.#ref = pairRef;
    this.#hold(true);
    try {
      const r = await transport.pairBegin({ pairRef, deviceName: deviceName() });
      // An outcome event can overtake this reply; it has already decided the view.
      if (this.#ref !== pairRef) return;
      this.code = r.code;
      this.view = "code";
    } catch (e) {
      if (this.#ref !== pairRef) return;
      this.#ref = "";
      this.#hold(false);
      const out = pairError(e);
      if (out.kind === "notice") this.notice = out.notice;
      else if (out.kind === "state") this.view = out.state;
    }
  }

  #outcome(e: PairingEvent): void {
    if (this.#ref !== "" && e.pairRef !== this.#ref) return;
    this.#ref = "";
    this.#hold(false);
    this.busy = false;
    if (e.outcome === "paired") {
      void this.#paired(e.daemonId);
      return;
    }
    haptic("refusal");
    this.code = "";
    this.notice = null;
    this.view = outcomeState(e.outcome);
  }

  async #paired(daemonId: string | undefined): Promise<void> {
    haptic("success");
    await connection.refresh();
    router.replace({ name: "tab", tab: "inbox" });
    const ok = await connection.connect(daemonId || connection.daemonId);
    if (!ok && connection.diagnosis.retryable) connection.retryLater();
  }

  #hold(on: boolean): void {
    if (this.#awake === on) return;
    this.#awake = on;
    void transport.setIdleTimerDisabled({ disabled: on }).catch(() => {});
  }
}

export const pairing = new Pairing();
