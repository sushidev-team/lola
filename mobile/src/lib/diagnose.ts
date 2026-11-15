// A connection's state as one sentence a person can act on.
//
// Invariants: a refusal the daemon spoke outranks every transport failure, and a
// code this build does not know is never interpreted (a confident wrong fix is
// worse than a plain failure). Copy follows the phone budget: a title of at most
// three words, one short sentence for the rest.

import type { FailureCode } from "lola-transport";
import { FRAME_VERSION_CURRENT } from "@mobile/wire";

export type DiagnosisKind =
  | "ok"
  | "idle"
  | "racing"
  | "offline"
  | "unreachable"
  | "key_changed"
  | "not_paired"
  | "version"
  | "failed";

export interface Diagnosis {
  kind: DiagnosisKind;
  title: string;
  detail: string;
  /** Whether trying again could plausibly help. */
  retryable: boolean;
}

export interface DiagnoseInput {
  phase: "idle" | "connecting" | "handshaking" | "ready" | "closed";
  failure?: FailureCode | null;
  refusal?: { code: string; message?: string; minV?: number; maxV?: number } | null;
  /** How the daemon is named to the person, e.g. "Mars Mac". */
  label?: string;
}

const IDLE: Diagnosis = { kind: "idle", title: "Not connected", detail: "Connect to your Mac.", retryable: false };
const RACING: Diagnosis = { kind: "racing", title: "Connecting", detail: "Reconnecting…", retryable: true };

function version(minV?: number, maxV?: number): Diagnosis {
  // The daemon's window is [minV, maxV]; this app speaks FRAME_VERSION_CURRENT.
  const daemonBehind = typeof maxV === "number" && maxV < FRAME_VERSION_CURRENT;
  return {
    kind: "version",
    title: "Update needed",
    detail: daemonBehind ? "Update Lola on your Mac." : "Update this app.",
    retryable: false,
  };
}

const NOT_PAIRED: Diagnosis = {
  kind: "not_paired",
  title: "Phone removed",
  detail: "Pair it again from your Mac.",
  retryable: false,
};

export function diagnose(input: DiagnoseInput): Diagnosis {
  const where = input.label || "your Mac";
  if (input.phase === "ready") return { kind: "ok", title: "Connected", detail: `Talking to ${where}.`, retryable: false };

  const code = input.refusal?.code;
  if (code === "not_paired") return NOT_PAIRED;
  if (code === "unsupported_version") return version(input.refusal?.minV, input.refusal?.maxV);
  if (code) return { kind: "failed", title: "Connection refused", detail: "Try again in a moment.", retryable: true };

  switch (input.failure) {
    case "not_paired":
      return NOT_PAIRED;
    case "unsupported_version":
      return version();
    case "key_changed":
      return {
        kind: "key_changed",
        title: "Key changed",
        detail: "Your Mac's key changed; pair again if you expected this.",
        retryable: true,
      };
    case "offline":
      return { kind: "offline", title: "Offline", detail: "No network.", retryable: true };
    case "unreachable":
    case "timeout":
    case "relay_unavailable":
    case "peer_closed":
      return { kind: "unreachable", title: "Unreachable", detail: `Can't reach ${where}.`, retryable: true };
    case "backgrounded":
      return RACING;
    case "client_closed":
      return IDLE;
    case undefined:
    case null:
      return input.phase === "connecting" || input.phase === "handshaking" ? RACING : IDLE;
    default:
      return { kind: "failed", title: "Connection failed", detail: "Try again in a moment.", retryable: true };
  }
}
