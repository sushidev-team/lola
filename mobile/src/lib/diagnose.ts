// Turning a failed connection into a sentence a human can act on.
//
// This module is small and it is the difference between an app that can be set
// up and one that cannot. There are four genuinely different reasons a phone
// does not reach the daemon, they are indistinguishable at the socket, and three
// of them have completely different fixes:
//
//   1. The key is wrong.        The daemon answered. Retype the key.
//   2. The pin is wrong.        TLS failed. Re-copy it, or the key was rotated.
//   3. Not on the network.      Wrong WiFi, VPN down, Mac asleep, daemon off.
//   4. Local network DENIED.    iOS refuses the connection with the SAME error
//                               as (3), the prompt never returns, and there is
//                               no API to ask. Only Settings can fix it.
//
// (4) is the one that makes this module mandatory rather than nice. If the app
// reports "could not connect" the user will check their WiFi forever, because
// nothing they can see is wrong. So the rule below is: on a private or
// unresolved address, a connection that never got as far as TLS names the
// permission as a POSSIBILITY, alongside the ordinary reasons, and says where
// the switch is. It never asserts the permission is the cause, because the app
// genuinely cannot know.
//
// PLAN.md's other requirement lands here too: a phone that is off the network
// must say "not on <daemon>'s network" rather than showing a raw connect error,
// and must never show the pairing screen — that is what revocation looks like,
// and the two must stay distinguishable.

import {
  CODE_DENIED,
  CODE_FRAME_TOO_LARGE,
  CODE_RATE_LIMITED,
  CODE_UNKNOWN_CMD,
  CODE_UNSUPPORTED_VERSION,
  type ErrPayload,
} from "@mobile/wire/protocol";
import { needsLocalNetwork } from "./endpoint";

export type DiagnosisKind =
  /** The daemon spoke and said no. Nothing about the network is wrong. */
  | "rejected"
  /** TLS or the pin. The host is there; the identity does not match. */
  | "identity"
  /** Client and daemon disagree about the protocol version. */
  | "version"
  /** Nothing answered. Includes the denied-permission case. */
  | "unreachable"
  /** The daemon closed us for a client-side bug. Not the user's problem. */
  | "client"
  /** Connected and working. */
  | "ok";

export interface Diagnosis {
  kind: DiagnosisKind;
  /** One short line. This is what the screen shows large. */
  title: string;
  /** One or two sentences saying what it means. */
  detail: string;
  /** The concrete next step, when there is exactly one. */
  hint?: string;
  /** Whether retrying could plausibly help. Drives the Retry button. */
  retryable: boolean;
}

export interface DiagnoseInput {
  phase: "idle" | "connecting" | "handshaking" | "ready" | "closed";
  error?: { message?: string } | null;
  refusal?: ErrPayload | null;
  host?: string;
  /** How the connection was named to the user, e.g. "marsmac.local". */
  label?: string;
}

/**
 * The one classifier. Exported and pure so every one of its branches is pinned
 * by a test — the branches are exactly the four confusions above, and a
 * regression in any of them costs a user their evening rather than a pixel.
 */
export function diagnose(input: DiagnoseInput): Diagnosis {
  const where = input.label || input.host || "the daemon";

  if (input.phase === "ready") {
    return { kind: "ok", title: "Connected", detail: `Talking to ${where}.`, retryable: false };
  }

  // A refusal is the most informative thing that can happen, because it means
  // the daemon was reached, authenticated the frame far enough to answer, and
  // then declined. Handle it first and never fold it into a network error.
  const code = input.refusal?.code;
  if (code) {
    switch (code) {
      case CODE_DENIED:
        return {
          kind: "rejected",
          title: "The daemon refused this key",
          detail:
            `${where} answered, so the address and the pin are right. The access key does not match.`,
          hint: "Check LOLA_REMOTE_INSECURE_KEY in the environment the daemon was started from.",
          retryable: false,
        };

      case CODE_UNSUPPORTED_VERSION: {
        const min = input.refusal?.minV;
        const max = input.refusal?.maxV;
        const behind =
          typeof max === "number" && max < 1
            ? "The daemon is older than this app."
            : "This app is older than the daemon.";
        return {
          kind: "version",
          title: "Protocol versions do not match",
          detail:
            `${behind} The daemon speaks version ${min ?? "?"} to ${max ?? "?"}; this app speaks 1.`,
          hint: "Update whichever is behind. Nothing else will make this connect.",
          retryable: false,
        };
      }

      case CODE_UNKNOWN_CMD:
        return {
          kind: "client",
          title: "The daemon closed the connection",
          detail:
            "This app sent a command the remote listener does not serve, and a refused command " +
            "closes the whole connection.",
          hint: "This is a bug in the app, not a setting. Reconnecting will work until it repeats.",
          retryable: true,
        };

      case CODE_FRAME_TOO_LARGE:
        return {
          kind: "client",
          title: "The daemon closed the connection",
          detail: "This app sent a frame larger than the protocol allows.",
          hint: "This is a bug in the app, not a setting.",
          retryable: true,
        };

      case CODE_RATE_LIMITED:
        return {
          kind: "client",
          title: "Too many requests at once",
          detail: "The daemon is refusing further work on this connection until it catches up.",
          retryable: true,
        };

      default:
        // Never interpret a code this build does not know. Saying something
        // specific about an unrecognised refusal is how a wrong fix gets
        // suggested confidently.
        return {
          kind: "rejected",
          title: "The daemon refused the connection",
          detail: input.refusal?.message
            ? `It said: ${input.refusal.message}`
            : `${where} declined without saying why.`,
          retryable: true,
        };
    }
  }

  const msg = (input.error?.message ?? "").toLowerCase();

  // TLS and the pin. The host answered — something is listening — but its
  // identity is not the one that was pinned.
  if (
    msg.includes("pin") ||
    msg.includes("certificate") ||
    msg.includes("tls") ||
    msg.includes("handshake") ||
    msg.includes("badcert") ||
    msg.includes("-9807")
  ) {
    return {
      kind: "identity",
      title: "That is not the daemon this app was pinned to",
      detail:
        `Something is listening on ${where}, but its certificate does not match the SPKI pin.`,
      hint:
        "Re-copy the pin from the daemon's startup log. It changes if ~/.lola/device.key was " +
        "regenerated.",
      retryable: false,
    };
  }

  // Everything else is silence, and silence has two very different causes on a
  // phone. Name both rather than guessing between them.
  const maybePermission = needsLocalNetwork(input.host ?? "");
  return {
    kind: "unreachable",
    title: `Not on ${where}'s network`,
    detail: maybePermission
      ? "Nothing answered. Either this phone cannot reach that address — different WiFi, VPN " +
        "down, Mac asleep, daemon not running — or iOS is blocking local network access for " +
        "this app."
      : "Nothing answered. The address may be wrong, or the daemon is not running.",
    hint: maybePermission
      ? "If the WiFi is right, check Settings → Privacy & Security → Local Network and make " +
        "sure Lola is switched on. iOS only asks once, and a refusal looks exactly like this."
      : undefined,
    retryable: true,
  };
}
