// What the connect screen says for each pairing outcome (spec §1.5, §5.2, §5.5).
//
// Invariants:
//  - Pure: no plugin, no store. Every sentence comes from `copy`; nothing here
//    invents text, and a code this build does not know is `failed`, never
//    interpreted.
//  - A pairing that never wrote the proof (unreachable, timeout) keeps the
//    scan: the secret was not presented, so the person may scan again.
//  - Parsing a QR is native. The only TS-side knowledge of a rejected payload is
//    which sentence its `invalid` reason earns.

import type { PairingOutcome, ScanInvalid } from "lola-transport";
import { FRAME_VERSION_CURRENT } from "@mobile/wire";
import { copy, fill } from "./copy";
import type { ConnectState } from "./router.svelte";

/** A message shown over the scan state: the scan stays the screen. */
export type PairNotice = "invalid" | "oldCode" | "cameraOff" | "unreachable" | "versionMac" | "versionApp";

export interface Words {
  title: string;
  body: string;
  action?: string;
}

/** The label that starts a scan again. */
export const SCAN_AGAIN: string = copy.connect.dropped.action;

const NOTICES: Record<PairNotice, Words> = {
  invalid: copy.connect.invalidScan,
  oldCode: copy.connect.oldCode,
  cameraOff: copy.connect.cameraOff,
  unreachable: {
    title: copy.connect.dropped.title,
    body: fill(copy.banner.unreachable.body, { mac: "your Mac" }),
  },
  versionMac: copy.connect.versionMac,
  versionApp: copy.connect.versionApp,
};

export function noticeWords(n: PairNotice): Words {
  return NOTICES[n];
}

const STATES: Record<ConnectState, Words> = {
  scan: copy.connect.scan,
  "no-camera": copy.connect.noCamera,
  code: copy.connect.code,
  refused: copy.connect.refused,
  expired: copy.connect.expired,
  failed: copy.connect.failed,
  dropped: copy.connect.dropped,
  "not-paired": copy.connect.notPaired,
};

export function stateWords(s: ConnectState): Words {
  return STATES[s];
}

/** The state a native pairing outcome other than `paired` leads to. */
export function outcomeState(o: Exclude<PairingOutcome, "paired">): ConnectState {
  switch (o) {
    case "refused":
      return "refused";
    case "expired":
      return "expired";
    case "dropped":
      return "dropped";
    case "failed":
      return "failed";
  }
}

/** The notice an unusable scan earns. */
export function invalidNotice(why: ScanInvalid): PairNotice {
  return why === "old_format" ? "oldCode" : "invalid";
}

/** What a rejected `pairBegin` means for the screen. */
export type PairError =
  | { kind: "notice"; notice: PairNotice }
  | { kind: "state"; state: ConnectState }
  | { kind: "cancelled" };

function maxVersionOf(e: { maxV?: unknown; data?: unknown }): number | undefined {
  const nested = e.data && typeof e.data === "object" ? (e.data as { maxV?: unknown }) : {};
  const v = e.maxV ?? nested.maxV;
  return typeof v === "number" && Number.isFinite(v) ? v : undefined;
}

export function pairError(err: unknown): PairError {
  const e = (err && typeof err === "object" ? err : {}) as { code?: unknown; maxV?: unknown; data?: unknown };
  switch (e.code) {
    case "client_closed":
      return { kind: "cancelled" };
    case "expired":
      return { kind: "state", state: "expired" };
    case "unreachable":
    case "timeout":
    case "offline":
      return { kind: "notice", notice: "unreachable" };
    case "unsupported_version": {
      const maxV = maxVersionOf(e);
      const macBehind = maxV !== undefined && maxV < FRAME_VERSION_CURRENT;
      return { kind: "notice", notice: macBehind ? "versionMac" : "versionApp" };
    }
    default:
      return { kind: "state", state: "failed" };
  }
}

/** The 56px glyph a state or notice carries: one per outcome, so a failure reads before its words do. */
export type ConnectGlyph =
  | "shield-x"
  | "timer-off"
  | "triangle-alert"
  | "wifi-off"
  | "unplug"
  | "laptop"
  | "camera-off"
  | "camera";

export function connectGlyph(view: ConnectState, notice: PairNotice | null): ConnectGlyph {
  if (notice === "cameraOff" || view === "no-camera") return "camera-off";
  if (notice === "unreachable") return "wifi-off";
  if (notice === "versionMac" || notice === "versionApp") return "triangle-alert";
  if (notice) return "camera";
  switch (view) {
    case "refused":
      return "shield-x";
    case "expired":
      return "timer-off";
    case "failed":
      return "triangle-alert";
    case "dropped":
      return "wifi-off";
    case "not-paired":
      return "unplug";
    case "code":
      return "laptop";
    default:
      return "camera";
  }
}

/** The glyph's literal text colour: bad for a refusal or failure, warn for a lapse, accent for the code, faint otherwise. */
export function connectTone(glyph: ConnectGlyph): string {
  switch (glyph) {
    case "shield-x":
    case "triangle-alert":
      return "text-bad";
    case "timer-off":
    case "wifi-off":
      return "text-warn";
    case "laptop":
      return "text-accent";
    default:
      return "text-faint";
  }
}

/** `123456` as `123 456`; anything that is not six digits is shown as given. */
export function groupCode(code: string): string {
  return /^\d{6}$/.test(code) ? `${code.slice(0, 3)} ${code.slice(3)}` : code;
}
