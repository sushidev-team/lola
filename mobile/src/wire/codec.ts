// The length-prefixed frame codec, byte-compatible with internal/protocol.
//
// Mirrors internal/protocol/framecodec.go. The framing is a 4-byte big-endian
// uint32 length prefix followed by exactly that many bytes of UTF-8 JSON. There
// is no magic number, no type byte and no trailing newline; the frame's kind
// lives inside the JSON, so there is exactly one place it is written down.
//
// WHY THIS EXISTS IN TYPESCRIPT AT ALL, given that the native LolaTransport
// plugin owns the real socket and does its own framing in Swift. Three reasons,
// and the third is the important one:
//
//   1. A pure-TypeScript transport (an in-memory fake, a development bridge)
//      needs a codec, and every test in this package is written against one.
//   2. The plugin may hand the WebView an already-decoded envelope OR a raw
//      frame body depending on how much of the protocol it chooses to own; the
//      shim should not care which.
//   3. There are now THREE implementations of one wire format — Go, Swift,
//      TypeScript — and nothing but agreement holds them together. The golden
//      vectors in ./testdata/frames.json are that agreement written down, and
//      they are only meaningful if this side can produce exact bytes. So the
//      encoder reproduces encoding/json's output byte for byte rather than
//      calling JSON.stringify and hoping.
//
// THE FOUR PLACES JSON.stringify DISAGREES WITH encoding/json, all of which this
// file handles explicitly:
//
//   - HTML escaping. Go's default encoder escapes `<`, `>` and `&` as
//     \u003c, \u003e and \u0026, in every string, and it does so a SECOND
//     time when a json.RawMessage payload is compacted into the envelope.
//     JSON.stringify escapes none of them. A resync line containing an
//     ampersand, an agent echoing a shell command say, diverges without this.
//   - U+2028 and U+2029. Go escapes both as \u2028 and \u2029; JSON.stringify
//     leaves them raw.
//   - Backspace and formfeed. JSON.stringify emits the short forms \b and \f;
//     Go emits \u0008 and \u000c, because its encoder special-cases only \\,
//     \", \n, \r and \t and sends every other control byte down the
//     \u00XX path.
//   - Field order and omitempty. Go emits struct fields in DECLARATION order and
//     drops zero values for tagged fields. A JavaScript object reproduces that
//     only if it is built in the right order with the right fields left out,
//     which is what the builders in ./protocol.ts do.
//
// None of these divergences is a correctness problem for the daemon, which
// parses JSON rather than comparing bytes. They are a problem for the golden
// vectors, which are the only cross-language check this protocol has.

import {
  FRAME_ERR,
  FRAME_HEADER_BYTES,
  FRAME_VERSION_CURRENT,
  MAX_FRAME_BYTES,
  clientAcceptsFrame,
  type Frame,
} from "./protocol";

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

/** Base class for every framing failure, so a caller can catch one thing. */
export class WireError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "WireError";
  }
}

/**
 * A zero-length prefix.
 *
 * Mirrors `protocol.ErrFrameEmpty`. There is no empty frame in this protocol —
 * the envelope always carries at least `v` and `type` — so a zero is a bug or a
 * probe. FATAL: nothing downstream can be trusted to be a frame boundary, so the
 * stream cannot be resynchronized and the connection must close.
 */
export class FrameEmptyError extends WireError {
  readonly recoverable = false;
  constructor() {
    super("wire: zero-length frame");
    this.name = "FrameEmptyError";
  }
}

/**
 * A length prefix (inbound) or an encoded body (outbound) over MAX_FRAME_BYTES.
 *
 * Mirrors `protocol.ErrFrameTooLarge`. Inbound it is FATAL for the same reason
 * as an empty frame: skipping it would mean reading the very bytes the reader
 * just refused to read. Outbound it is a bug in the client, and the frame is
 * dropped entirely rather than truncated — half a frame is worse than none.
 */
export class FrameTooLargeError extends WireError {
  readonly recoverable = false;
  readonly size: number;
  readonly max: number;
  constructor(size: number, max: number = MAX_FRAME_BYTES) {
    super(`wire: frame exceeds the maximum frame size: ${size} bytes (max ${max})`);
    this.name = "FrameTooLargeError";
    this.size = size;
    this.max = max;
  }
}

/**
 * A body that is not a decodable envelope.
 *
 * Mirrors `protocol.ErrFrameMalformed`. The framing itself was intact, so the
 * stream position is still known and the decoder can carry on; whether the
 * CONNECTION carries on is the caller's decision, and the daemon's own answer is
 * that a malformed envelope has no id to answer on and therefore closes.
 */
export class FrameMalformedError extends WireError {
  readonly recoverable = true;
  constructor(detail: string) {
    super(`wire: malformed frame: ${detail}`);
    this.name = "FrameMalformedError";
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
function goNumber(n: number): string {
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
// Encoding
// ---------------------------------------------------------------------------

const encoder = new TextEncoder();
const decoder = new TextDecoder("utf-8", { fatal: false });

/**
 * Serialize one frame's envelope to JSON text, in `protocol.Frame`'s declaration
 * order and with its omitempty rules applied:
 *
 *   v        always
 *   type     always
 *   id       omitted when ""
 *   cmd      omitted when ""
 *   pane     omitted when ""
 *   seq      omitted when 0
 *   payload  omitted when absent
 *
 * The payload is serialized in place. Go keeps it as a json.RawMessage and
 * compacts it into the envelope, which HTML-escapes it a second time; goJSONValue
 * escapes it once, to the same result, because the escaping is idempotent.
 */
export function encodeFrameJSON(f: Frame): string {
  let out = "{";
  out += '"v":' + goNumber(f.v ?? FRAME_VERSION_CURRENT);
  out += ',"type":' + goJSONString(f.type);
  if (f.id) out += ',"id":' + goJSONString(f.id);
  if (f.cmd) out += ',"cmd":' + goJSONString(f.cmd);
  if (f.pane) out += ',"pane":' + goJSONString(f.pane);
  if (f.seq) out += ',"seq":' + goNumber(f.seq);
  if (f.payload !== undefined && f.payload !== null) {
    out += ',"payload":' + goJSONValue(f.payload);
  }
  return out + "}";
}

/**
 * The frame's BODY as bytes, with no length prefix.
 *
 * This is the form that survives a move to a message transport: on a WebSocket
 * each frame is one binary message and the transport frames it already. Keeping
 * the body identical either way is what lets one codec serve both.
 *
 * Throws FrameTooLargeError and emits NOTHING when the body is over the cap.
 * Truncating is never an option: half a resync paints a wrong screen.
 */
export function encodeFrameBody(f: Frame): Uint8Array {
  const body = encoder.encode(encodeFrameJSON(f));
  if (body.length > MAX_FRAME_BYTES) throw new FrameTooLargeError(body.length);
  return body;
}

/**
 * One complete wire frame: the 4-byte big-endian length prefix followed by the
 * JSON body, in a single buffer.
 *
 * The prefix is copied in front of the body rather than written separately for
 * the same reason the Go writer does it: a separate 4-byte write puts a header
 * in its own TCP segment and interacts badly with Nagle on the exact stream that
 * carries keystrokes.
 */
export function encodeFrame(f: Frame): Uint8Array {
  const body = encodeFrameBody(f);
  const out = new Uint8Array(FRAME_HEADER_BYTES + body.length);
  new DataView(out.buffer).setUint32(0, body.length, false); // false = big-endian
  out.set(body, FRAME_HEADER_BYTES);
  return out;
}

// ---------------------------------------------------------------------------
// Decoding
// ---------------------------------------------------------------------------

/**
 * Decode one frame BODY (no length prefix).
 *
 * Applies only the ENVELOPE shape rules, and deliberately nothing else: an
 * unknown `type` and an unsupported `v` both decode fine, because refusing them
 * is dispatch policy and the dispatcher needs the decoded envelope in order to
 * refuse usefully — the id to answer on, the version to report. Mirrors the
 * split in framecodec.go, where ReadFrame validates neither.
 */
export function decodeFrameBody(body: Uint8Array): Frame {
  // Decoding is non-fatal, so invalid UTF-8 becomes U+FFFD rather than throwing;
  // inside a JSON string that is harmless, and anywhere else JSON.parse below
  // rejects it, which is the same outcome Go reaches by a different route.
  const text = decoder.decode(body);
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch (e) {
    throw new FrameMalformedError(e instanceof Error ? e.message : String(e));
  }
  if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new FrameMalformedError("body is not a JSON object");
  }
  const o = parsed as Record<string, unknown>;
  // Go's json.Unmarshal fails on a type mismatch for a concrete field, which is
  // what makes {"v":"one"} malformed rather than a v of 0. Mirror that, because
  // a client that silently reads a string v as 0 refuses a perfectly good
  // daemon with "unsupported version".
  if (o.v !== undefined && typeof o.v !== "number") {
    throw new FrameMalformedError("v is not a number");
  }
  if (o.type !== undefined && typeof o.type !== "string") {
    throw new FrameMalformedError("type is not a string");
  }
  for (const k of ["id", "cmd", "pane"] as const) {
    if (o[k] !== undefined && typeof o[k] !== "string") {
      throw new FrameMalformedError(`${k} is not a string`);
    }
  }
  if (o.seq !== undefined && typeof o.seq !== "number") {
    throw new FrameMalformedError("seq is not a number");
  }
  const f: Frame = { v: (o.v as number) ?? 0, type: (o.type as string) ?? "" };
  if (typeof o.id === "string" && o.id !== "") f.id = o.id;
  if (typeof o.cmd === "string" && o.cmd !== "") f.cmd = o.cmd;
  if (typeof o.pane === "string" && o.pane !== "") f.pane = o.pane;
  if (typeof o.seq === "number" && o.seq !== 0) f.seq = o.seq;
  if (o.payload !== undefined) f.payload = o.payload;
  // Unknown fields are TOLERATED and dropped, exactly as the Go side tolerates
  // them (no DisallowUnknownFields): a newer peer's additive field must never
  // turn into a parse failure.
  return f;
}

/**
 * Decode one complete wire frame — prefix and body — from the start of `bytes`.
 * Returns the frame and how many bytes it consumed, or null when `bytes` does
 * not yet hold a whole frame.
 *
 * Refuses an impossible length BEFORE touching the body, which is the whole
 * reason the protocol uses a prefix rather than a delimiter.
 */
export function decodeFrame(bytes: Uint8Array): { frame: Frame; consumed: number } | null {
  if (bytes.length < FRAME_HEADER_BYTES) return null;
  const n = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength).getUint32(0, false);
  if (n === 0) throw new FrameEmptyError();
  if (n > MAX_FRAME_BYTES) throw new FrameTooLargeError(n);
  if (bytes.length < FRAME_HEADER_BYTES + n) return null;
  const body = bytes.subarray(FRAME_HEADER_BYTES, FRAME_HEADER_BYTES + n);
  return { frame: decodeFrameBody(body), consumed: FRAME_HEADER_BYTES + n };
}

/**
 * A streaming reader over a byte stream that arrives in arbitrary chunks.
 *
 * Mirrors `protocol.FrameReader`, including its posture: ONE reader, no
 * timeouts of its own (the right deadline differs between a request stream and
 * an attached pane, and an attached pane is legitimately silent for minutes), and
 * a fatal length prefix poisons it rather than being skipped.
 */
export class FrameDecoder {
  private buf: Uint8Array = new Uint8Array(0);
  private poison: WireError | null = null;

  /** Bytes held but not yet forming a whole frame. */
  get buffered(): number {
    return this.buf.length;
  }

  /** True once an unrecoverable framing error has been seen. */
  get poisoned(): boolean {
    return this.poison !== null;
  }

  /**
   * Feed a chunk. Every complete frame is handed to `sink` IN ORDER, as it is
   * parsed, and only then is a framing error thrown — so a caller never loses
   * frames that arrived before a bad one.
   *
   * A FrameMalformedError is recoverable: the boundary was intact, so the
   * decoder stays usable and the caller decides whether the connection does. A
   * FrameEmptyError or FrameTooLargeError poisons the decoder, because the
   * stream position is no longer known.
   */
  push(chunk: Uint8Array, sink: (f: Frame) => void): void {
    if (this.poison) throw this.poison;
    this.buf = concat(this.buf, chunk);
    for (;;) {
      let got: { frame: Frame; consumed: number } | null;
      try {
        got = decodeFrame(this.buf);
      } catch (e) {
        if (e instanceof FrameMalformedError) {
          // The length was honoured, so the boundary is known: drop this frame
          // and let the caller decide about the connection.
          const n = new DataView(
            this.buf.buffer,
            this.buf.byteOffset,
            this.buf.byteLength,
          ).getUint32(0, false);
          this.buf = this.buf.subarray(FRAME_HEADER_BYTES + n);
          throw e;
        }
        this.poison = e as WireError;
        throw e;
      }
      if (!got) return;
      this.buf = this.buf.subarray(got.consumed);
      sink(got.frame);
    }
  }

  /** Convenience for tests and for a transport that gets whole frames at once. */
  pushAll(chunk: Uint8Array): Frame[] {
    const out: Frame[] = [];
    this.push(chunk, (f) => out.push(f));
    return out;
  }

  /** Forget everything buffered, and clear a poison. Used on reconnect. */
  reset(): void {
    this.buf = new Uint8Array(0);
    this.poison = null;
  }
}

function concat(a: Uint8Array, b: Uint8Array): Uint8Array {
  if (a.length === 0) return b.slice();
  if (b.length === 0) return a;
  const out = new Uint8Array(a.length + b.length);
  out.set(a, 0);
  out.set(b, a.length);
  return out;
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

// ---------------------------------------------------------------------------
// Envelope policy helpers
// ---------------------------------------------------------------------------

/**
 * Whether a decoded frame is one this client may act on: a known type that a
 * client is allowed to RECEIVE.
 *
 * The version check is deliberately NOT folded in. A frame carrying an
 * unsupported `v` still has to be READ, because the one frame a peer that
 * understands nothing else must still parse is the `unsupported_version` err —
 * that is the whole reason its shape is frozen at v1 forever.
 */
export function clientMayAccept(f: Frame): boolean {
  return f.type === FRAME_ERR || clientAcceptsFrame(f.type);
}
