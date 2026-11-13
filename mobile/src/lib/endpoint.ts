// Where the daemon is, and what kind of address that is.
//
// The "what kind" half is not cosmetic. On iOS, a connection to a private
// address is gated by the local-network permission, and a DENIED permission is
// reported by Network.framework as an ordinary unreachable host — the same error
// as a wrong IP, a sleeping Mac or a daemon that is not running. The prompt is
// one-shot and never returns, so the app can never ask again and the user has no
// way to discover what happened. That is why "first launch where local network
// is denied shows an actionable explanation rather than a connect error" is in
// M1's definition of done, and why this module can tell a LAN address from a
// loopback one: only the LAN case can be a permission problem.

import { DEFAULT_REMOTE_PORT } from "@mobile/wire/protocol";

export { DEFAULT_REMOTE_PORT };

/** What the connect form holds while it is being typed. */
export interface EndpointDraft {
  host: string;
  /** As typed. Empty means "the default". */
  port: string;
  spkiPin: string;
}

export type AddressKind =
  /** 127.0.0.0/8, ::1, or the name "localhost". An SSH forward lands here, and
   *  it prompts for nothing at all. */
  | "loopback"
  /** RFC1918, CGNAT, link-local, ULA. The local-network permission applies. */
  | "private"
  /** A hostname, or a public address. Behind a VPN this is where a tailnet
   *  address lands, and the permission may or may not apply. */
  | "other"
  | "invalid";

const V4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/;

/**
 * Classify a host string. Never throws; an unparseable host is "invalid" so the
 * form can refuse it rather than a socket failing later with less context.
 */
export function classifyHost(host: string): AddressKind {
  const h = host.trim().toLowerCase().replace(/^\[|\]$/g, "");
  if (h === "") return "invalid";

  if (h === "localhost" || h === "::1" || h === "0:0:0:0:0:0:0:1") return "loopback";

  const m = V4.exec(h);
  if (m) {
    const o = m.slice(1).map(Number);
    if (o.some((n) => n > 255)) return "invalid";
    if (o[0] === 127) return "loopback";
    if (o[0] === 10) return "private";
    if (o[0] === 172 && o[1] >= 16 && o[1] <= 31) return "private";
    if (o[0] === 192 && o[1] === 168) return "private";
    if (o[0] === 100 && o[1] >= 64 && o[1] <= 127) return "private"; // CGNAT, incl. tailnets
    if (o[0] === 169 && o[1] === 254) return "private"; // link-local
    return "other";
  }

  if (h.includes(":")) {
    // IPv6. fc00::/7 is unique-local and fe80::/10 is link-local; both are
    // "private" for permission purposes.
    if (/^f[cd]/.test(h) || /^fe[89ab]/.test(h)) return "private";
    return /^[0-9a-f:.]+$/.test(h) ? "other" : "invalid";
  }

  // A hostname. `.local` is mDNS, which is squarely inside the permission.
  if (!/^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$/.test(h)) {
    return "invalid";
  }
  return h.endsWith(".local") ? "private" : "other";
}

/** Whether iOS's local-network permission can plausibly be what is refusing. */
export function needsLocalNetwork(host: string): boolean {
  const kind = classifyHost(host);
  return kind === "private" || kind === "other";
}

/**
 * Parse a typed port. Empty means the default, which is what the daemon binds
 * when `[remote].port` is unset.
 */
export function parsePort(text: string): number | null {
  const t = text.trim();
  if (t === "") return DEFAULT_REMOTE_PORT;
  if (!/^\d{1,5}$/.test(t)) return null;
  const n = Number(t);
  return n >= 1 && n <= 65535 ? n : null;
}

/**
 * Is this pin the shape the daemon prints?
 *
 * `remote.DeviceKey.SPKIPin` is base64(SHA-256(SubjectPublicKeyInfo)) in the
 * standard alphabet WITH padding — 32 bytes, so always 44 characters ending in
 * "=". Checking the shape here turns "a typo in a 44-character string copied out
 * of a log" from an opaque TLS failure into a form error, which is the whole
 * difference between a two-minute setup and a lost evening.
 */
export function isPinShaped(pin: string): boolean {
  return /^[A-Za-z0-9+/]{43}=$/.test(pin.trim());
}

export interface EndpointProblem {
  field: "host" | "port" | "pin" | "key";
  message: string;
}

/**
 * Everything wrong with a draft, in the order the form shows its fields.
 *
 * The bearer key's minimum is the daemon's own: `remote.insecureMinKeyLen` is 16
 * and a listener refuses to START below it, so a shorter key is not a rejection
 * at connect time but a daemon that was never listening — a failure that looks
 * exactly like the wrong host.
 */
export function validateDraft(
  draft: EndpointDraft,
  key: string,
  minKeyLen: number,
  /**
   * A key is held natively and was deliberately not handed to this caller.
   *
   * The plaintext has no way back across the Capacitor bridge — a resolved
   * payload is logged, so `secretGet` was removed — and the reconnect passes a
   * `keyRef` instead. Validating the empty string it was given would refuse
   * every automatic reconnect with "Enter the daemon's access key", on a phone
   * that is correctly paired.
   */
  keyStored = false,
): EndpointProblem[] {
  const out: EndpointProblem[] = [];

  const kind = classifyHost(draft.host);
  if (kind === "invalid") {
    out.push({
      field: "host",
      message: draft.host.trim() === "" ? "Enter the Mac's address." : "That is not a host or IP address.",
    });
  }

  if (parsePort(draft.port) === null) {
    out.push({ field: "port", message: "Port must be a number between 1 and 65535." });
  }

  const pin = draft.spkiPin.trim();
  if (pin === "") {
    out.push({ field: "pin", message: "Paste the SPKI pin the daemon logged at startup." });
  } else if (!isPinShaped(pin)) {
    out.push({
      field: "pin",
      message: "That is not an SPKI pin: expected 44 base64 characters ending in “=”.",
    });
  }

  if (keyStored && key.length === 0) {
    // Nothing to check: the plugin holds it and the daemon is the only thing
    // that can say whether it is still right.
  } else if (key.length === 0) {
    out.push({ field: "key", message: "Enter the daemon's access key." });
  } else if (key.length < minKeyLen) {
    out.push({
      field: "key",
      message: `The key must be at least ${minKeyLen} characters — a shorter one and the daemon never starts listening.`,
    });
  }

  return out;
}

/**
 * A short, stable identity for an endpoint. Used as the key the bearer secret is
 * filed under, so moving between two daemons does not silently reuse one key
 * against the other.
 *
 * The pin is deliberately NOT part of it: rotating `~/.lola/device.key` changes
 * the pin while the same machine keeps the same key, and losing the stored
 * secret on a pin rotation would be a puzzle rather than a prompt.
 */
export function endpointId(host: string, port: number): string {
  return `${host.trim().toLowerCase()}:${port}`;
}
