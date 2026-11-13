// The hand-off payload: what a QR code from the Mac carries, and how much of it
// is allowed to be believed.
//
// WHY AN OPAQUE TOKEN AND NOT A URL. PLAN.md settles this for M2's pairing QR
// and the argument transfers to M1 unchanged: custom URL schemes cannot be
// claimed exclusively on either platform, resolution is roughly
// first-installed-wins, and people routinely scan codes with the SYSTEM camera
// rather than an in-app scanner — at which point the OS hands the secret to
// whichever app registered the scheme. M1's bearer key is a LONGER-lived
// credential than M2's `qr_secret` (no 90-second window, not single-use, not
// zeroed after one handshake), so routing it through the OS URL router would be
// strictly worse than the case PLAN.md already rejects. The payload is
// therefore `lola1.<base64url(JSON)>`: the system camera produces nothing
// useful from it, and only this module knows how to read it.
//
// THE PAYLOAD IS UNTRUSTED INPUT. It arrives from a camera pointed at an
// arbitrary printed square, or from the OS URL router on behalf of an
// arbitrary app. Every field is therefore re-validated here against the SAME
// `validateDraft` the typed form uses — a scanned host that skipped validation
// would be the one input path that could hand the transport something the form
// would have refused.
//
// AND IT CARRIES A SECRET. The `key` field is the daemon's bearer key, so
// nothing in this module ever puts the payload — or any slice of it — into a
// message, an error or a log line. `not_lola` in particular says only that the
// code was not a lola code: echoing the scanned text back would print the key
// on screen the first time somebody mistypes the version prefix.

import { classifyHost, validateDraft, type EndpointDraft, type EndpointProblem } from "./endpoint";
import { INSECURE_MIN_KEY_LEN } from "@mobile/wire/protocol";

/** The version prefix this build understands. */
export const PAIR_PREFIX = "lola1.";

/**
 * An upper bound on a scanned string before any of it is decoded.
 *
 * A QR code in the densest standard version tops out well under this, so the
 * only thing that reaches it is a code carrying something else entirely. It
 * exists because `atob` on an unbounded string is the one place a scan could
 * cost real time on the main thread.
 */
export const MAX_PAYLOAD_CHARS = 4096;

/** What the Mac put in the code. Addresses in preference order. */
export interface PairPayload {
  /** Every address the daemon is listening on, most useful first. */
  addrs: string[];
  port: number;
  /** SPKI pin, normalised to the standard-base64 form the transport wants. */
  pin: string;
  /** The M1 bearer key. Never rendered, never logged. */
  key: string;
}

/** Where a payload came from, which decides whether it may dial on its own. */
export type PairSource =
  /** Lola's own camera. The user aimed it at this code one second ago. */
  | "scan"
  /** The OS URL router, on behalf of some app. Shown, never auto-dialled. */
  | "link"
  /**
   * This process's own launch environment or argv, in a debug build.
   *
   * Split from `link` because they are not the same door, however identical
   * the URL in them looks. Anybody on the device can ask iOS to open a
   * `lola-dev://` URL — that is the whole of PLAN.md's objection to URL-routed
   * pairing, and why `link` may only fill the form. Nobody can set another
   * process's launch environment without being the thing that starts it, which
   * on a device means a debugger and in CI means already owning the machine.
   * So this one may dial, and the app says so on a banner for as long as the
   * connection is up.
   *
   * Without the split the scriptable path was not scriptable: from iOS 26 the
   * system draws an untappable "Open in Lola?" confirmation over every
   * `simctl openurl`, and `simctl` has no gesture API — so an agent could fill
   * the form and had no way to submit it.
   */
  | "launch";

export type PairFailure =
  /** Nothing was scanned, or the code was empty. */
  | { kind: "empty" }
  /** A readable code that is not a lola hand-off at all. */
  | { kind: "not_lola" }
  /** A lola code from a different version of the app. */
  | { kind: "wrong_version"; version: string }
  /** The right prefix, but the body is not decodable JSON of the right shape. */
  | { kind: "malformed" }
  /** Decoded cleanly, and then said something that cannot be connected to. */
  | { kind: "fields"; problems: EndpointProblem[] };

export type PairResult = { ok: true; payload: PairPayload } | ({ ok: false } & PairFailure);

/** One rendered message. `tone` picks the banner colour from the theme tokens. */
export interface PairNotice {
  tone: "bad" | "warn";
  title: string;
  detail: string;
  hint?: string;
}

/**
 * base64 or base64url, with or without padding, to a UTF-8 string.
 *
 * Both alphabets are accepted because the two ends of this hand-off are written
 * by different people at different times and the bytes are identical either
 * way; refusing one of them would turn a cosmetic disagreement into a feature
 * that does not work. Anything genuinely undecodable throws, and the caller
 * turns that into `malformed`.
 */
function decodeBase64(s: string): string {
  const norm = s.replace(/-/g, "+").replace(/_/g, "/").replace(/=+$/, "");
  const padded = norm + "=".repeat((4 - (norm.length % 4)) % 4);
  const bin = atob(padded);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  // `fatal` so a body that is not UTF-8 fails loudly here rather than arriving
  // downstream as replacement characters inside a host name.
  return new TextDecoder("utf-8", { fatal: true }).decode(bytes);
}

/**
 * Put an SPKI pin in the one shape the rest of the app expects.
 *
 * `remote.DeviceKey.SPKIPin` produces STANDARD base64 with padding — 44
 * characters ending in "=" — and that is what `isPinShaped`, the connect form
 * and the native transport all compare against. PLAN.md's M2 sketch writes the
 * same 32 bytes as 43 characters of base64url. They are the same hash, so a
 * payload carrying either is accepted and converted here rather than rejected
 * for its alphabet. Anything else is returned untouched, so `validateDraft`
 * gets to produce the message.
 */
export function normalizePin(pin: string): string {
  const t = pin.trim();
  if (/^[A-Za-z0-9+/]{43}=$/.test(t)) return t;
  if (/^[A-Za-z0-9\-_]{43}=?$/.test(t)) {
    return t.replace(/=+$/, "").replace(/-/g, "+").replace(/_/g, "/") + "=";
  }
  return t;
}

/**
 * Every address worth dialling, best first.
 *
 * Two rules, both learned from what the addresses actually look like. A
 * ZONE-SCOPED link-local (`fe80::1%en0`) names an interface on the MAC and
 * means nothing on a phone, so it is dropped rather than ranked. And a
 * loopback address is kept but ranked LAST: it is useless to a physical device
 * and exactly right for a Simulator, which shares the Mac's loopback — and
 * under the `lola_insecure` build without the LAN opt-in the listener binds
 * loopback only, so it is routinely the only address there is.
 *
 * The whole LIST matters, not only its head. A Mac commonly has several private
 * addresses at once — Wi-Fi, a wired dock, a VM bridge — and the daemon reports
 * all of them because it cannot know which one the phone shares a network with.
 * Only the phone can find that out, and it finds out by trying.
 */
export function rankAddresses(addrs: readonly string[]): string[] {
  const clean = addrs
    .map((a) => (typeof a === "string" ? a.trim() : ""))
    .filter((a) => a !== "" && !a.includes("%"));
  const usable = clean.filter((a) => classifyHost(a) !== "invalid");
  const ordered = [
    ...usable.filter((a) => classifyHost(a) !== "loopback"),
    ...usable.filter((a) => classifyHost(a) === "loopback"),
  ];
  const seen = new Set<string>();
  return ordered.filter((a) => {
    if (seen.has(a)) return false;
    seen.add(a);
    return true;
  });
}

/**
 * The one address to SHOW in the form, out of everything the daemon reported.
 *
 * The form has a single host field and a human has to read something, so this
 * is the best candidate rather than the whole list. Connecting uses
 * `rankAddresses` and tries them in turn — see Connection#connect — because the
 * best guess and the one that actually routes are not always the same.
 */
export function chooseAddress(addrs: readonly string[]): string {
  return rankAddresses(addrs)[0] ?? "";
}

/**
 * The payload as the connect form holds it. The one conversion, used by both paths.
 *
 * `alternates` is every OTHER address the daemon offered, best first. The form
 * shows one host because a human reads one, but connecting walks the whole list
 * — the daemon lists several because it cannot know which of its networks the
 * phone is on, and only the phone can find out. See Connection#connect.
 */
export function toDraft(p: PairPayload): {
  draft: EndpointDraft;
  key: string;
  alternates: string[];
} {
  const ranked = rankAddresses(p.addrs);
  return {
    draft: {
      host: ranked[0] ?? "",
      port: p.port > 0 ? String(p.port) : "",
      spkiPin: p.pin,
    },
    key: p.key,
    alternates: ranked.slice(1),
  };
}

/** Read `addrs`, tolerating a single `host` string from a terser encoder. */
function readAddrs(o: Record<string, unknown>): string[] | null {
  const a = o.addrs;
  if (Array.isArray(a)) return a.filter((x): x is string => typeof x === "string");
  if (typeof o.host === "string") return [o.host];
  return null;
}

/**
 * Parse a scanned string into something connectable, or say precisely why not.
 *
 * The failure kinds are the distinction the screen needs: a code that is not
 * ours, a code from another version, a corrupted one, and — the one that is
 * easy to fold in and must not be — a perfectly well-formed payload whose
 * fields cannot be connected to. The last one is the daemon's fault or the
 * desktop's, and telling the user "unreadable code" would send them to clean
 * their camera lens instead.
 */
export function parsePairing(text: string): PairResult {
  const t = (text ?? "").trim();
  if (t === "") return { ok: false, kind: "empty" };
  if (t.length > MAX_PAYLOAD_CHARS) return { ok: false, kind: "malformed" };

  const m = /^lola(\d+)\./.exec(t);
  if (!m) return { ok: false, kind: "not_lola" };
  if (m[1] !== "1") return { ok: false, kind: "wrong_version", version: m[1] };

  let parsed: unknown;
  try {
    parsed = JSON.parse(decodeBase64(t.slice(m[0].length)));
  } catch {
    return { ok: false, kind: "malformed" };
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    return { ok: false, kind: "malformed" };
  }

  const o = parsed as Record<string, unknown>;
  const addrs = readAddrs(o);
  // A missing or wrongly-typed field is a STRUCTURAL failure, not a field one:
  // there is no value to show the user and nothing they could correct.
  if (!addrs || typeof o.key !== "string") return { ok: false, kind: "malformed" };
  const pin = typeof o.pin === "string" ? o.pin : "";
  const port = typeof o.port === "number" && Number.isFinite(o.port) ? Math.trunc(o.port) : 0;

  const payload: PairPayload = { addrs, port, pin: normalizePin(pin), key: o.key };

  // The same validator the typed form runs, on the same draft shape. A scanned
  // endpoint gets no shortcut past it.
  const { draft, key } = toDraft(payload);
  const problems = validateDraft(draft, key, INSECURE_MIN_KEY_LEN);
  if (problems.length > 0) return { ok: false, kind: "fields", problems };

  return { ok: true, payload };
}

/**
 * A failure, as a banner.
 *
 * Nothing here quotes the scanned text. The payload carries a bearer key, and a
 * message that echoed what it read would put that key on screen the first time
 * a code was truncated or a prefix mistyped.
 */
export function pairFailureMessage(f: PairFailure): PairNotice {
  switch (f.kind) {
    case "empty":
      return {
        tone: "warn",
        title: "Nothing in that code",
        detail: "The scanner read a code, but it was empty.",
      };

    case "not_lola":
      return {
        tone: "warn",
        title: "That is not a lola code",
        detail:
          "The scanner read something, but it was not a connection hand-off — a WiFi code or a " +
          "web address, most likely.",
        hint: "On the Mac, open Lola’s settings and pick Remote to show the right one.",
      };

    case "wrong_version":
      return {
        tone: "bad",
        title: "That code is from a different version",
        detail:
          `It is a lola code, but a version ${f.version} one and this app reads version 1.`,
        hint: "Update whichever of the two is behind. Nothing else will make this code work.",
      };

    case "malformed":
      return {
        tone: "bad",
        title: "That code did not survive the trip",
        detail:
          "It starts like a lola hand-off but the rest of it is damaged, so there is nothing " +
          "safe to read out of it.",
        hint: "Scan it again, straight on and in good light. If it fails twice, show a fresh one.",
      };

    case "fields": {
      // The code was read perfectly. Something on the Mac side is wrong, and
      // saying "unreadable" here would send the user to clean their lens.
      const first = f.problems[0];
      return {
        tone: "bad",
        title: "That code cannot be connected to",
        detail: `It was read correctly, but ${describeProblem(first)}`,
        hint: "Check the daemon is running on the Mac, then show the code again.",
      };
    }
  }
}

/** One field problem, lower-cased into the middle of a sentence. */
function describeProblem(p: EndpointProblem | undefined): string {
  if (!p) return "one of the values in it is not usable.";
  const where =
    p.field === "host"
      ? "the address in it"
      : p.field === "port"
        ? "the port in it"
        : p.field === "pin"
          ? "the certificate pin in it"
          : "the access key in it";
  return `${where} is not usable: ${p.message.charAt(0).toLowerCase()}${p.message.slice(1)}`;
}
