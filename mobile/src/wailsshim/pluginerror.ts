// Turning the native plugin's two failure channels into ONE wire-level refusal.
//
// WHY THIS FILE EXISTS AT ALL. The daemon distinguishes "nothing answered" from
// "I answered and said no", and those two have completely different fixes — a
// wrong WiFi versus a wrong access key. That distinction survives the socket,
// survives Swift, and then used to die on the bridge: `LolaTransport.connect`
// rejected with a Capacitor error carrying a transport-level code, the
// structured `daemonCode` rode a `state` event that arrives one bridge hop
// LATER, and `ChannelTransport` recorded only `status.error`. `diagnose` reads
// `status.refusal` for the refusal branches, found none, fell through to its
// silence branch, and told the user their phone was not on the Mac's network —
// on a screen where the address, the port and the pin were all correct and only
// the key was wrong.
//
// So both channels are normalised here, into `WireRefusalError`, which is what
// `ChannelTransport` already turns into `status.refusal`.
//
// THE ORDERING IS THE WHOLE REASON THE REJECTION PATH IS PRIMARY. Swift's
// `terminate` settles the pending connect BEFORE it emits the state event, and
// the two cross the bridge as separate evaluations — so at the moment
// `openPluginChannel`'s catch runs, the state event has provably not arrived
// yet. Waiting for it would mean putting a timer on every connect failure.
// Reading the rejection instead needs no timer, which is why the plugin puts
// the daemon's own code in the rejection's `data` dictionary (Capacitor merges
// that dictionary into the JS error object). The state-event path stays as the
// route for a refusal that lands AFTER connect resolved.
//
// NOTHING HERE MAY CARRY KEY MATERIAL. The strings it reads are the plugin's
// own reason lines, which are written to name a phase and a code and never a
// credential.

import { WireRefusalError } from "../wire";

/** As much of `LolaStateEvent` as this module reads. */
export interface PluginStateLike {
  phase?: string;
  code?: string;
  reason?: string;
  daemonCode?: string;
  minV?: number;
  maxV?: number;
}

/** As much of a Capacitor rejection as this module reads. */
export interface PluginErrorLike {
  message?: unknown;
  code?: unknown;
  daemonCode?: unknown;
  minV?: unknown;
  maxV?: unknown;
  /**
   * WHERE CAPACITOR ACTUALLY PUTS THE REJECTION DATA, and the reason this
   * interface has two shapes rather than one.
   *
   * `CAPPluginCall.reject(message, code, error, data)` does NOT merge `data`
   * into the error object: `CAPPluginCallError.init` stores
   * `.dictionary(["data": data])`, so the JS side receives
   * `{ message, errorMessage, code, data: { … } }`. Reading only the top level
   * looked right, typechecked, unit-tested green against a hand-built error —
   * and on the device found nothing, so a refused key still rendered as a dead
   * network. Both levels are read now: the nesting is Capacitor's private
   * detail, and a plugin that ever hands the fields over flat should keep
   * working.
   */
  data?: unknown;
}

function num(v: unknown): number | undefined {
  return typeof v === "number" && Number.isFinite(v) ? v : undefined;
}

function text(v: unknown): string {
  return typeof v === "string" ? v : "";
}

/**
 * The refusal a failed `connect` carried, or undefined when the failure was not
 * one the daemon spoke.
 *
 * `daemonCode` is the only field consulted, and its absence is meaningful: a
 * timeout, a pin mismatch and an unreachable host all reject with a transport
 * code and no daemon code, and every one of them is correctly diagnosed from
 * `error` alone. Inventing a refusal for those would replace one wrong sentence
 * with another.
 */
export function refusalFromPluginError(err: unknown): WireRefusalError | undefined {
  if (!err || typeof err !== "object") return undefined;
  const e = err as PluginErrorLike;
  const nested = (e.data && typeof e.data === "object" ? e.data : {}) as PluginErrorLike;
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
 * One `state` event as an Error for the close path.
 *
 * A refusal becomes a `WireRefusalError` so the transport can record it as
 * `status.refusal`; everything else stays a plain Error naming the transport
 * code, which is what the message-sniffing half of `diagnose` reads.
 */
export function stateError(e: PluginStateLike): Error {
  const refusal = refusalFromPluginError(e);
  if (refusal) return refusal;
  const why = e.code ?? e.phase;
  return new Error(e.reason ? `${why}: ${e.reason}` : String(why));
}
