// The native plugin's two failure channels, normalised into one error.
//
// Invariant: a refusal the DAEMON spoke (`daemonCode`) becomes a
// `WireRefusalError`; every other failure becomes a `LinkError` carrying the
// plugin's `FailureCode`. Nothing here is a secret: the strings read are the
// plugin's own reason lines, which name a phase and a code, never a credential.
//
// A connect rejection is read first and synchronously. The plugin settles the
// pending connect before it emits the matching `state` event and the two cross
// the bridge separately, so waiting for the event would put a timer on every
// failure. Capacitor nests a rejection's data under `data`, so both levels are
// read.

import type { FailureCode } from "lola-transport";
import { WireRefusalError } from "../wire";

/** As much of `StateEvent` as this module reads. */
export interface PluginStateLike {
  phase?: string;
  code?: string;
  daemonCode?: string;
  minV?: number;
  maxV?: number;
  reason?: string;
}

/** As much of a Capacitor rejection as this module reads. */
export interface PluginErrorLike {
  message?: unknown;
  code?: unknown;
  daemonCode?: unknown;
  minV?: unknown;
  maxV?: unknown;
  data?: unknown;
}

/** A failure the transport reported without the daemon speaking. */
export class LinkError extends Error {
  readonly code: FailureCode;

  constructor(code: FailureCode, message: string) {
    super(message);
    this.name = "LinkError";
    this.code = code;
  }
}

const FAILURE_CODES: ReadonlySet<string> = new Set<FailureCode>([
  "offline",
  "unreachable",
  "timeout",
  "key_changed",
  "not_paired",
  "unsupported_version",
  "relay_unavailable",
  "protocol",
  "peer_closed",
  "client_closed",
  "backgrounded",
  "internal",
]);

/** A closed list: a code this build does not know is `internal`, never interpreted. */
export function toFailureCode(code: unknown): FailureCode {
  return typeof code === "string" && FAILURE_CODES.has(code) ? (code as FailureCode) : "internal";
}

function num(v: unknown): number | undefined {
  return typeof v === "number" && Number.isFinite(v) ? v : undefined;
}

function text(v: unknown): string {
  return typeof v === "string" ? v : "";
}

function nestedOf(e: PluginErrorLike): PluginErrorLike {
  return (e.data && typeof e.data === "object" ? e.data : {}) as PluginErrorLike;
}

/**
 * The refusal a failed `connect` carried, or undefined when the daemon did not
 * speak. A timeout, an unreachable host and a key change reject with a failure
 * code and no daemon code; inventing a refusal for them would swap one wrong
 * sentence for another.
 */
export function refusalFromPluginError(err: unknown): WireRefusalError | undefined {
  if (!err || typeof err !== "object") return undefined;
  const e = err as PluginErrorLike;
  const nested = nestedOf(e);
  const code = text(e.daemonCode) || text(nested.daemonCode);
  if (code === "") return undefined;
  return new WireRefusalError(
    code,
    text(e.message),
    undefined,
    num(e.minV) ?? num(nested.minV),
    num(e.maxV) ?? num(nested.maxV),
  );
}

/**
 * Any rejection of a plugin call as a typed error: the daemon's refusal when it
 * spoke, else a `LinkError` naming the plugin's failure code.
 */
export function pluginFailure(err: unknown): Error {
  const refusal = refusalFromPluginError(err);
  if (refusal) return refusal;
  if (err && typeof err === "object") {
    const e = err as PluginErrorLike;
    return new LinkError(toFailureCode(e.code), text(e.message) || "connection failed");
  }
  return new LinkError("internal", String(err));
}

/** One `state` event as the Error for the close path. */
export function stateError(e: PluginStateLike): Error {
  const refusal = refusalFromPluginError(e);
  if (refusal) return refusal;
  const code = toFailureCode(e.code);
  return new LinkError(code, e.reason ? `${code}: ${e.reason}` : code);
}
