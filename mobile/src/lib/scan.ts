// The camera, as the connect screen sees it.
//
// Invariants:
//  - The payload never reaches JS. `scanPairing` parses a `lola2.` QR natively
//    and hands back only its public half plus an opaque `pairRef`; this module
//    forwards that and nothing else.
//  - A cancel RESOLVES. A human changing their mind is the ordinary way a
//    scanner ends, so it is an outcome, not a rejection a call site could
//    render as a red banner.
//  - Every path comes back as a `ScanOutcome`; `scanOnce` never throws, and the
//    probe fails CLOSED: no plugin or a throwing probe means no Scan button.
//    `notDetermined` counts as available (hiding the button would guarantee the
//    permission prompt never shows).

import type { DaemonId, ScanInvalid, ScanUnavailableReason } from "lola-transport";

import { native } from "../native";

/** What one attempt to scan produced. */
export type ScanOutcome =
  /** A `lola2.` payload was read and is held natively under `pairRef`. */
  | { kind: "scanned"; pairRef: string; daemonId: DaemonId; addrs: number; relay: boolean }
  /** The user backed out. Not a failure. */
  | { kind: "cancelled" }
  /** The QR is not a pairing code this build can use. */
  | { kind: "invalid"; why: ScanInvalid }
  /** Camera permission was refused. Only Settings can undo it. */
  | { kind: "denied" }
  /** Policy forbids the camera. There is no toggle to send anyone to. */
  | { kind: "restricted" }
  /** There is no scanner here: no plugin, no camera, or a Simulator. */
  | { kind: "unavailable"; reason?: ScanUnavailableReason }
  /** Something else broke inside the scanner. */
  | { kind: "failed"; reason?: string };

/** Whether to offer a Scan button at all, and why not when the answer is no. */
export interface ScanCapability {
  available: boolean;
  reason?: ScanUnavailableReason;
}

/** Can this build scan? */
export async function scanCapability(): Promise<ScanCapability> {
  if (!native) return { available: false, reason: "unsupported" };
  try {
    const r = await native.scanCapability();
    return { available: r.available === true, reason: r.reason };
  } catch {
    return { available: false };
  }
}

/**
 * A rejected `scanPairing`, as an outcome. Pure and exported so every branch is
 * pinned by a test. The plugin's `code` is authoritative; the message fallback
 * is deliberately narrow, because matching a bare "error" against arbitrary text
 * is how a confident wrong diagnosis gets shown.
 */
export function scanErrorToOutcome(e: unknown): ScanOutcome {
  const err = e as { code?: unknown; message?: unknown } | null | undefined;
  const code = typeof err?.code === "string" ? err.code : "";
  const message = typeof err?.message === "string" ? err.message : "";

  switch (code) {
    case "camera_denied":
    case "denied":
      return { kind: "denied" };
    case "camera_restricted":
    case "restricted":
      return { kind: "restricted" };
    case "cancelled":
    case "canceled":
      return { kind: "cancelled" };
    case "no_camera":
      return { kind: "unavailable", reason: "no_camera" };
    case "unavailable":
    case "unimplemented":
      return { kind: "unavailable", reason: "unsupported" };
  }

  const m = message.toLowerCase();
  if (m.includes("denied") || m.includes("not authorized")) return { kind: "denied" };
  if (m.includes("cancel")) return { kind: "cancelled" };
  if (m.includes("no camera")) return { kind: "unavailable", reason: "no_camera" };
  if (m.includes("not implemented") || m.includes("no native transport")) {
    return { kind: "unavailable", reason: "unsupported" };
  }
  return { kind: "failed", reason: message || undefined };
}

/** Open the scanner and wait for one code. Never throws. */
export async function scanOnce(prompt?: string): Promise<ScanOutcome> {
  if (!native) return { kind: "unavailable", reason: "unsupported" };
  try {
    const r = await native.scanPairing(prompt === undefined ? undefined : { prompt });
    if (r.cancelled) return { kind: "cancelled" };
    if ("invalid" in r) return { kind: "invalid", why: r.invalid };
    return { kind: "scanned", pairRef: r.pairRef, daemonId: r.daemonId, addrs: r.addrs, relay: r.relay };
  } catch (e) {
    return scanErrorToOutcome(e);
  }
}
