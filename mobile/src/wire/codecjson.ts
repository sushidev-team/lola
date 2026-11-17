// Go-compatible JSON encoding and base64, the byte-level half of the codec.
//
// The golden vectors compare BYTES, so every value is serialised the way
// encoding/json does it: HTML characters and U+2028/2029 escaped, control bytes
// as lowercase \u00XX (never \b or \f), object keys in insertion order
// (= Go's declaration order), and []byte as padded standard base64.

/** Base class for every framing failure, so a caller can catch one thing. */
export class WireError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "WireError";
  }
}


// ---------------------------------------------------------------------------
// Go-compatible JSON encoding
// ---------------------------------------------------------------------------

const HEX = "0123456789abcdef";

/**
 * Encode one string as a JSON string literal, including the surrounding quotes,
 * exactly as Go's encoding/json does with escapeHTML enabled (the default).
 *
 * Go's rules, from encodeState.string:
 *   quote -> \"   backslash -> \\   newline -> \n   return -> \r   tab -> \t
 *   any other byte below 0x20 -> \u00XX with LOWERCASE hex
 *   `<` `>` `&` -> \u003c \u003e \u0026
 *   U+2028, U+2029 -> \u2028, \u2029
 *   everything else -> raw UTF-8
 *
 * A LONE SURROGATE — invalid UTF-16, the JavaScript analogue of Go's invalid
 * UTF-8 — is written as the six-character escape `\ufffd`, which is what Go
 * emits for an undecodable byte (`encodeState.string` appends the literal
 * `\ufffd`, not the replacement character itself). Passing the surrogate through
 * would have TextEncoder emit the three raw bytes ef bf bd instead: the same
 * character, different bytes, and this file's entire premise is the bytes.
 *
 * It is not reachable from a golden vector (JSON.parse cannot produce a lone
 * surrogate) and effectively unreachable on the wire, but the exemption used to
 * be documented as an agreement that did not exist, and a false note in the one
 * place a reader checks is worse than no note.
 */
export function goJSONString(s: string): string {
  let out = '"';
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    const ch = s[i];
    if (c < 0x20) {
      switch (c) {
        case 0x0a:
          out += "\\n";
          break;
        case 0x0d:
          out += "\\r";
          break;
        case 0x09:
          out += "\\t";
          break;
        default:
          // NOTE: this covers 0x08 and 0x0c, where JSON.stringify would have
          // emitted the short forms \b and \f. Go does not.
          out += "\\u00" + HEX[(c >> 4) & 0xf] + HEX[c & 0xf];
      }
      continue;
    }
    // A surrogate is only valid as half of a pair. An unpaired one cannot be
    // encoded as UTF-8 at all, so Go's `\ufffd` escape is what goes out.
    if (c >= 0xd800 && c <= 0xdfff) {
      const paired =
        c <= 0xdbff && i + 1 < s.length && s.charCodeAt(i + 1) >= 0xdc00 && s.charCodeAt(i + 1) <= 0xdfff;
      if (!paired) {
        out += "\\ufffd";
        continue;
      }
      out += ch + s[i + 1];
      i++;
      continue;
    }

    switch (c) {
      case 0x22: // "
        out += '\\"';
        continue;
      case 0x5c: // backslash
        out += "\\\\";
        continue;
      case 0x3c: // <
        out += "\\u003c";
        continue;
      case 0x3e: // >
        out += "\\u003e";
        continue;
      case 0x26: // &
        out += "\\u0026";
        continue;
      case 0x2028:
        out += "\\u2028";
        continue;
      case 0x2029:
        out += "\\u2029";
        continue;
    }
    out += ch;
  }
  return out + '"';
}

/**
 * Encode a number the way Go encodes an int, a uint64 or a float64.
 *
 * The protocol carries only integers, so the interesting case is the boring
 * one. A non-integer is still encoded rather than rejected — a hand-built
 * payload should not throw at the codec — but it is a sign that something has
 * drifted, because there is no float anywhere in internal/protocol.
 *
 * `seq` is a Go uint64 and a JavaScript number is exact only to 2^53. A pane
 * would have to produce nine quadrillion frames to reach that, so it is noted
 * rather than defended against.
 */
export function goNumber(n: number): string {
  if (!Number.isFinite(n)) {
    // encoding/json refuses NaN and the infinities outright, and a frame
    // carrying one would be rejected by the daemon as a malformed payload.
    throw new WireError(`wire: cannot encode a non-finite number (${n})`);
  }
  return String(n);
}

/**
 * Serialize any JSON value the way encoding/json would, honouring insertion
 * order for objects (which is Go's declaration order, because the builders in
 * ./protocol.ts insert in declaration order).
 *
 * `undefined` object properties are skipped, matching both JSON.stringify and
 * the effect of Go's omitempty on a field the builders left out. A top-level
 * `undefined` is an error rather than a silent empty string.
 */
export function goJSONValue(v: unknown): string {
  if (v === null) return "null";
  switch (typeof v) {
    case "boolean":
      return v ? "true" : "false";
    case "number":
      return goNumber(v);
    case "string":
      return goJSONString(v);
    case "object":
      break;
    default:
      throw new WireError(`wire: cannot encode a value of type ${typeof v}`);
  }
  if (Array.isArray(v)) {
    let out = "[";
    for (let i = 0; i < v.length; i++) {
      if (i > 0) out += ",";
      // A hole or an explicit undefined inside an array is null in JSON, as in
      // both JSON.stringify and a Go []any carrying a nil.
      out += v[i] === undefined ? "null" : goJSONValue(v[i]);
    }
    return out + "]";
  }
  if (v instanceof Uint8Array) {
    // Go marshals []byte as standard base64 with padding. Doing it here means a
    // caller may hand raw bytes to a payload builder without base64ing by hand,
    // which is the same convenience the Go side gets for free.
    return goJSONString(bytesToBase64(v));
  }
  let out = "{";
  let first = true;
  for (const [k, val] of Object.entries(v as Record<string, unknown>)) {
    if (val === undefined) continue;
    if (!first) out += ",";
    first = false;
    out += goJSONString(k) + ":" + goJSONValue(val);
  }
  return out + "}";
}


// ---------------------------------------------------------------------------
// base64, matching Go's base64.StdEncoding
// ---------------------------------------------------------------------------

const B64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

const B64_REV = (() => {
  const t = new Int16Array(128).fill(-1);
  for (let i = 0; i < B64.length; i++) t[B64.charCodeAt(i)] = i;
  return t;
})();

/**
 * Standard base64 with padding, which is what encoding/json produces for a Go
 * []byte. Written out rather than delegating to btoa, which takes a latin1
 * string and mangles anything above 0xff on the way in.
 */
export function bytesToBase64(b: Uint8Array): string {
  let out = "";
  let i = 0;
  for (; i + 2 < b.length; i += 3) {
    const n = (b[i] << 16) | (b[i + 1] << 8) | b[i + 2];
    out += B64[(n >> 18) & 63] + B64[(n >> 12) & 63] + B64[(n >> 6) & 63] + B64[n & 63];
  }
  const rest = b.length - i;
  if (rest === 1) {
    const n = b[i] << 16;
    out += B64[(n >> 18) & 63] + B64[(n >> 12) & 63] + "==";
  } else if (rest === 2) {
    const n = (b[i] << 16) | (b[i + 1] << 8);
    out += B64[(n >> 18) & 63] + B64[(n >> 12) & 63] + B64[(n >> 6) & 63] + "=";
  }
  return out;
}

/**
 * Decode standard base64.
 *
 * NULL AND UNDEFINED ARE EMPTY, and that is not defensive programming, it is the
 * wire. `PTYOutputPayload.Data` carries no `omitempty`, so the field is always
 * written — and Go marshals a nil []byte as `null`, not as `""`. Every pane
 * output frame therefore has to survive `{"data":null}`.
 */
export function base64ToBytes(s: string | null | undefined): Uint8Array {
  if (!s) return new Uint8Array(0);
  let end = s.length;
  while (end > 0 && s[end - 1] === "=") end--;
  const out = new Uint8Array(Math.floor((end * 3) / 4));
  let o = 0;
  let acc = 0;
  let bits = 0;
  for (let i = 0; i < end; i++) {
    const c = s.charCodeAt(i);
    const v = c < 128 ? B64_REV[c] : -1;
    if (v < 0) throw new WireError(`wire: invalid base64 character at index ${i}`);
    acc = (acc << 6) | v;
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      out[o++] = (acc >> bits) & 0xff;
    }
  }
  return o === out.length ? out : out.subarray(0, o);
}

