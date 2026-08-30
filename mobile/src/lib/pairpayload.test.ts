import { describe, it, expect } from "vitest";
import vector from "./testdata/connectcode.json";
import {
  MAX_PAYLOAD_CHARS,
  PAIR_PREFIX,
  chooseAddress,
  normalizePin,
  pairFailureMessage,
  parsePairing,
  rankAddresses,
  toDraft,
  type PairFailure,
  type PairPayload,
} from "./pairpayload";

// A real pin: 32 bytes of SHA-256 as standard base64, which is what
// `remote.DeviceKey.SPKIPin` produces and what the live daemon printed while
// this was written.
const PIN = "C4td4uyeJMSyxfoAsB3i98Kd6JhkpOTf3Oxipiq+sxI=";
const PIN_URL = "C4td4uyeJMSyxfoAsB3i98Kd6JhkpOTf3Oxipiq-sxI";
const KEY = "0123456789abcdef0123456789abcdef";

/** Encode a body the way the desktop is expected to. */
function encode(body: unknown, prefix = PAIR_PREFIX): string {
  const json = JSON.stringify(body);
  const b64 = btoa(String.fromCharCode(...new TextEncoder().encode(json)));
  return prefix + b64.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

// The hand-built payload the rest of this file mutates. It is the shared
// vector's own fields, so the unit tests and the golden token can never drift
// into describing two different formats.
const good = { addrs: vector.fields.addrs, port: vector.fields.port, pin: PIN, key: KEY };

describe("normalizePin", () => {
  it("leaves the standard-base64 form the daemon prints untouched", () => {
    expect(normalizePin(PIN)).toBe(PIN);
    expect(normalizePin(`  ${PIN}  `)).toBe(PIN);
  });

  it("converts the base64url form PLAN.md's M2 sketch uses to the same bytes", () => {
    // The two encodings are the same 32-byte hash. Rejecting one of them would
    // turn a cosmetic disagreement between the two ends into a dead feature.
    expect(normalizePin(PIN_URL)).toBe(PIN);
    expect(normalizePin(`${PIN_URL}=`)).toBe(PIN);
  });

  it("returns anything else untouched, so validateDraft writes the message", () => {
    expect(normalizePin("nonsense")).toBe("nonsense");
    expect(normalizePin("")).toBe("");
  });
});

describe("chooseAddress", () => {
  it("prefers a routable address over loopback", () => {
    expect(chooseAddress(["127.0.0.1", "192.168.1.5", "::1"])).toBe("192.168.1.5");
  });

  it("falls back to loopback, which is all the insecure build ever binds", () => {
    // A Simulator shares the Mac's loopback, so this is the working case there.
    expect(chooseAddress(["127.0.0.1", "::1"])).toBe("127.0.0.1");
  });

  it("drops a zone-scoped link-local, which names an interface on the Mac", () => {
    expect(chooseAddress(["fe80::1%en0", "10.0.0.4"])).toBe("10.0.0.4");
    expect(chooseAddress(["fe80::1%en0"])).toBe("");
  });

  it("ignores blanks and unparseable entries", () => {
    expect(chooseAddress(["", "   ", "not a host!", "10.0.0.4"])).toBe("10.0.0.4");
    expect(chooseAddress([])).toBe("");
  });
});

// The GOLDEN VECTOR: one token, in one file, read by BOTH ends of the hand-off.
//
// This suite and internal/remote/connectcode_test.go load the same
// mobile/src/lib/testdata/connectcode.json. The Go side asserts that
// EncodeConnectCode produces that exact token; this side asserts that
// parsePairing reads it back into the same values and picks the same address.
//
// GO IS THE SOURCE OF TRUTH. If these disagree, the daemon is right and this
// file is wrong — the same rule the wire vectors in src/wire/testdata run under,
// and for the same reason: nothing generates one side from the other, so an
// encoder that pads its base64 or a field that gets renamed is not a compile
// error anywhere. Without this, it surfaces as a phone refusing to scan a
// square, which is indistinguishable from a dirty lens or a dim room.
describe("the shared connect-code vector", () => {
  it("parses the exact token the Go encoder produces", () => {
    const r = parsePairing(vector.token);
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(r.payload.addrs).toEqual(vector.fields.addrs);
    expect(r.payload.port).toBe(vector.fields.port);
    expect(r.payload.pin).toBe(vector.fields.pin);
    expect(r.payload.key).toBe(vector.fields.key);
  });

  it("dials the address the vector names", () => {
    // Under `lola_insecure` the listener binds loopback only, so this is the
    // Simulator's working case — and the ranking that produces it (routable
    // first, loopback last, zone-scoped dropped) has to agree with the order
    // the daemon writes into `addrs`.
    const r = parsePairing(vector.token);
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(toDraft(r.payload).draft.host).toBe(vector.dialHost);
  });

  it("hands the form a draft the shared validator accepts", () => {
    // The whole point of the token: what comes out of it goes through the same
    // `validateDraft` the typed form runs. `parsePairing` returning ok is that
    // assertion, so this pins the values the form is actually left holding.
    const r = parsePairing(vector.token);
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(toDraft(r.payload)).toEqual({
      draft: {
        host: vector.dialHost,
        port: String(vector.fields.port),
        spkiPin: vector.fields.pin,
      },
      key: vector.fields.key,
      // Everything the daemon offered BESIDES the one shown, in rank order.
      // Connection#connect walks these when the first host does not route.
      alternates: rankAddresses(vector.fields.addrs).slice(1),
    });
  });

  it("refuses every token the vector says both ends must refuse", () => {
    // Only the cases both decoders agree on. This one is deliberately MORE
    // tolerant than Go's in places it documents — a bare `host`, a padded body,
    // a base64url pin — so a fixture asserting Go's extra strictness would fail
    // this side for being right.
    for (const c of vector.refused) {
      const r = parsePairing(c.token);
      expect(r.ok, `${c.name}: ${c.why}`).toBe(false);
    }
  });
});

describe("parsePairing", () => {
  it("reads a well-formed payload", () => {
    const r = parsePairing(encode(good));
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(r.payload.pin).toBe(PIN);
    expect(r.payload.port).toBe(7717);
    expect(r.payload.key).toBe(KEY);
    expect(r.payload.addrs).toEqual(["127.0.0.1", "::1"]);
  });

  it("accepts standard base64 in the body as well as base64url", () => {
    const json = JSON.stringify(good);
    const std = PAIR_PREFIX + btoa(String.fromCharCode(...new TextEncoder().encode(json)));
    expect(parsePairing(std).ok).toBe(true);
  });

  it("accepts a base64url pin, converting it on the way through", () => {
    const r = parsePairing(encode({ ...good, pin: PIN_URL }));
    expect(r.ok).toBe(true);
    if (r.ok) expect(r.payload.pin).toBe(PIN);
  });

  it("accepts a single `host` in place of `addrs`", () => {
    const r = parsePairing(encode({ host: "10.0.0.4", port: 7717, pin: PIN, key: KEY }));
    expect(r.ok).toBe(true);
    if (r.ok) expect(toDraft(r.payload).draft.host).toBe("10.0.0.4");
  });

  it("reports an empty scan as empty", () => {
    expect(parsePairing("")).toMatchObject({ ok: false, kind: "empty" });
    expect(parsePairing("   ")).toMatchObject({ ok: false, kind: "empty" });
  });

  it("reports anything without the prefix as not ours", () => {
    // The three codes a camera is most likely to meet by accident.
    expect(parsePairing("https://example.com")).toMatchObject({ kind: "not_lola" });
    expect(parsePairing("WIFI:S:home;T:WPA;P:hunter2;;")).toMatchObject({ kind: "not_lola" });
    expect(parsePairing("lola://connect?host=1.2.3.4")).toMatchObject({ kind: "not_lola" });
  });

  it("names a version mismatch rather than calling it corrupt", () => {
    const r = parsePairing(encode(good, "lola2."));
    expect(r).toMatchObject({ ok: false, kind: "wrong_version", version: "2" });
  });

  it("reports a damaged body as malformed", () => {
    expect(parsePairing("lola1.!!!!not base64!!!!")).toMatchObject({ kind: "malformed" });
    expect(parsePairing("lola1." + btoa("not json"))).toMatchObject({ kind: "malformed" });
    expect(parsePairing(encode([1, 2, 3]))).toMatchObject({ kind: "malformed" });
    expect(parsePairing(encode("a string"))).toMatchObject({ kind: "malformed" });
  });

  it("treats a missing field as structural, not as a field problem", () => {
    // There is no value to show and nothing the user could correct, so this is
    // "the code is damaged" rather than "the address in it is wrong".
    expect(parsePairing(encode({ port: 7717, pin: PIN, key: KEY }))).toMatchObject({
      kind: "malformed",
    });
    expect(parsePairing(encode({ addrs: ["10.0.0.4"], port: 7717, pin: PIN }))).toMatchObject({
      kind: "malformed",
    });
  });

  it("refuses a payload longer than a QR could plausibly carry", () => {
    const huge = PAIR_PREFIX + "A".repeat(MAX_PAYLOAD_CHARS);
    expect(parsePairing(huge)).toMatchObject({ kind: "malformed" });
  });

  // The case the task calls out: it parses, and then it is wrong. Each of these
  // must be `fields`, because "unreadable code" would send the user to clean a
  // lens that is working perfectly.
  it("reports a parsed payload with a bad pin as a field problem", () => {
    const r = parsePairing(encode({ ...good, pin: "too-short" }));
    expect(r).toMatchObject({ ok: false, kind: "fields" });
    if (r.ok || r.kind !== "fields") return;
    expect(r.problems[0].field).toBe("pin");
  });

  it("reports a parsed payload with a short key as a field problem", () => {
    const r = parsePairing(encode({ ...good, key: "short" }));
    expect(r).toMatchObject({ ok: false, kind: "fields" });
    if (r.ok || r.kind !== "fields") return;
    expect(r.problems[0].field).toBe("key");
  });

  it("reports a payload with no usable address as a field problem", () => {
    const r = parsePairing(encode({ ...good, addrs: ["fe80::1%en0"] }));
    expect(r).toMatchObject({ ok: false, kind: "fields" });
    if (r.ok || r.kind !== "fields") return;
    expect(r.problems[0].field).toBe("host");
  });

  it("reports an out-of-range port as a field problem", () => {
    const r = parsePairing(encode({ ...good, port: 99999 }));
    expect(r).toMatchObject({ ok: false, kind: "fields" });
    if (r.ok || r.kind !== "fields") return;
    expect(r.problems[0].field).toBe("port");
  });
});

describe("toDraft", () => {
  it("produces exactly what the typed form holds, so both converge", () => {
    const p: PairPayload = { addrs: ["10.0.0.4"], port: 7717, pin: PIN, key: KEY };
    expect(toDraft(p)).toEqual({
      draft: { host: "10.0.0.4", port: "7717", spkiPin: PIN },
      key: KEY,
      alternates: [],
    });
  });

  it("leaves the port empty when the payload names none, so the default applies", () => {
    const p: PairPayload = { addrs: ["10.0.0.4"], port: 0, pin: PIN, key: KEY };
    expect(toDraft(p).draft.port).toBe("");
  });
});

describe("pairFailureMessage", () => {
  it("never quotes the scanned payload, which carries the key", () => {
    // The one rule this module has. A message that echoed what it read would
    // print a bearer secret on screen the first time a code was truncated or a
    // prefix mistyped. Driven through the real parser so the guarantee is about
    // the actual pipeline rather than a hand-built failure value.
    const secret = "SUPERSECRETKEYMATERIAL0123456789";
    const payloads = [
      encode({ ...good, key: secret, pin: "not-a-pin" }), // fields
      encode({ ...good, key: secret }, "lola2."), // wrong_version
      encode({ ...good, key: secret }).slice(0, 20) + "%%%", // malformed
      "WIFI:S:home;T:WPA;P:" + secret + ";;", // not_lola
    ];
    for (const text of payloads) {
      const r = parsePairing(text);
      expect(r.ok).toBe(false);
      if (r.ok) continue;
      const m = pairFailureMessage(r);
      expect(`${m.title} ${m.detail} ${m.hint ?? ""}`).not.toContain(secret.slice(0, 12));
    }
  });

  it("gives every kind a title, a detail and a tone", () => {
    const failures: PairFailure[] = [
      { kind: "empty" },
      { kind: "not_lola" },
      { kind: "wrong_version", version: "9" },
      { kind: "malformed" },
      { kind: "fields", problems: [] },
    ];
    for (const f of failures) {
      const m = pairFailureMessage(f);
      expect(m.title.length).toBeGreaterThan(0);
      expect(m.detail.length).toBeGreaterThan(0);
      expect(["bad", "warn"]).toContain(m.tone);
    }
  });

  it("says a well-read code is a Mac-side problem, not a scanning one", () => {
    const m = pairFailureMessage({
      kind: "fields",
      problems: [{ field: "host", message: "Enter the Mac's address." }],
    });
    expect(m.detail).toContain("read correctly");
  });
});
