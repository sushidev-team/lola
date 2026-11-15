// The length-prefixed frame codec, byte-compatible with internal/protocol.
//
// A frame is a 4-byte big-endian length followed by that many bytes of UTF-8
// JSON; the frame's kind lives inside the JSON. The encoder reproduces
// encoding/json byte for byte (./codecjson.ts) because the golden vectors in
// ./testdata/*.json are the only cross-language check the protocol has.

import {
  FRAME_ERR,
  FRAME_HEADER_BYTES,
  FRAME_VERSION_CURRENT,
  clientAcceptsFrame,
  type Frame,
} from "./protocol";
import { MAX_FRAME_BYTES } from "./limits";
import { WireError, bytesToBase64, base64ToBytes, goJSONString, goJSONValue, goNumber } from "./codecjson";

export { WireError, bytesToBase64, base64ToBytes, goJSONString, goJSONValue };

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

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
 *   topic    omitted when ""
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
  if (f.topic) out += ',"topic":' + goJSONString(f.topic);
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
  for (const k of ["id", "cmd", "pane", "topic"] as const) {
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
  if (typeof o.topic === "string" && o.topic !== "") f.topic = o.topic;
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

