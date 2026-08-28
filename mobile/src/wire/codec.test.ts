import { describe, expect, it } from "vitest";

import vectorFile from "./testdata/frames.json";
import {
  FrameDecoder,
  FrameEmptyError,
  FrameMalformedError,
  FrameTooLargeError,
  base64ToBytes,
  bytesToBase64,
  decodeFrame,
  decodeFrameBody,
  encodeFrame,
  encodeFrameBody,
  encodeFrameJSON,
  goJSONString,
} from "./codec";
import { FRAME_HEADER_BYTES, MAX_FRAME_BYTES, type Frame } from "./protocol";

// NOTE ON HOW THIS FILE IS RUN. There is no node_modules under mobile/ yet — the
// human runs the toolchain once, from the recipe this milestone produces — so
// these tests were WRITTEN but not executed here. Their subject was nonetheless
// verified: every golden vector below was produced by the Go codec (which the
// author did run, via internal/protocol/goldenvectors_test.go) and the encoder
// was checked against all twenty of them out of band. What has not run is
// vitest itself.
//
// NOTE ON CHARACTER LITERALS. Backslash and control characters are built from
// char codes rather than written as escapes. It looks odd and it is deliberate:
// this file's whole subject is the difference between a character and its
// six-byte escape text, and a nested "\\\\u003c" in the source is exactly the
// thing a reader (or a tool that rewrites the file) gets wrong. fromCharCode
// says which bytes are meant and cannot be misread.

const BACKSLASH = String.fromCharCode(0x5c);
const ESC = String.fromCharCode(0x1b);
const LF = String.fromCharCode(0x0a);
const CR = String.fromCharCode(0x0d);
const TAB = String.fromCharCode(0x09);
const BACKSPACE = String.fromCharCode(0x08);
const FORMFEED = String.fromCharCode(0x0c);
const LINE_SEP = String.fromCharCode(0x2028);
const PARA_SEP = String.fromCharCode(0x2029);

interface GoldenCase {
  name: string;
  why: string;
  frame: Frame;
  hex: string;
}

const vectors = (vectorFile as unknown as { note: string; cases: GoldenCase[] }).cases;

function toHex(b: Uint8Array): string {
  let s = "";
  for (const byte of b) s += byte.toString(16).padStart(2, "0");
  return s;
}

function fromHex(s: string): Uint8Array {
  const out = new Uint8Array(s.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(s.slice(i * 2, i * 2 + 2), 16);
  return out;
}

// ---------------------------------------------------------------------------
// The golden vectors. This is the reason the package exists in this shape.
// ---------------------------------------------------------------------------

describe("golden vectors", () => {
  it("has cases to check", () => {
    expect(vectors.length).toBeGreaterThan(0);
  });

  it.each(vectors.map((c) => [c.name, c] as const))("%s encodes to the stated bytes", (_n, c) => {
    // The SAME file drives internal/protocol/goldenvectors_test.go, which asserts
    // that the Go codec produces these bytes. Both sides passing means both sides
    // agree, which is the only cross-language check this protocol has.
    expect(toHex(encodeFrame(c.frame))).toBe(c.hex);
  });

  it.each(vectors.map((c) => [c.name, c] as const))("%s round-trips", (_n, c) => {
    const raw = fromHex(c.hex);
    const decoded = new FrameDecoder().pushAll(raw);
    expect(decoded).toHaveLength(1);
    expect(toHex(encodeFrame(decoded[0]))).toBe(c.hex);
  });

  it("covers every frame kind", () => {
    const kinds = new Set(vectors.map((c) => c.frame.type));
    for (const k of ["req", "resp", "sub", "unsub", "resync", "pty", "err"]) {
      expect(kinds.has(k)).toBe(true);
    }
  });
});

// ---------------------------------------------------------------------------
// The shapes a client gets wrong, named individually so deleting one is
// deliberate rather than accidental.
// ---------------------------------------------------------------------------

describe("the shapes a client gets wrong", () => {
  const byName = new Map(vectors.map((c) => [c.name, c]));
  const body = (name: string) => {
    const c = byName.get(name);
    if (!c) throw new Error(`the ${name} vector is gone`);
    return new TextDecoder().decode(fromHex(c.hex).subarray(FRAME_HEADER_BYTES));
  };

  it("states cursor visibility in the negative", () => {
    // Absent means VISIBLE. A client that inverts this paints no caret on any
    // pane, which reads as an agent that is not waiting for input when it is.
    expect(body("resync_cursor_visible")).not.toContain("cursorHidden");
    expect(body("resync_cursor_hidden")).toContain('"cursorHidden":true');

    const visible = byName.get("resync_cursor_visible")!.frame.payload as {
      cursorHidden?: boolean;
    };
    expect(visible.cursorHidden ?? false).toBe(false);
  });

  it("always writes pty output data, including as null", () => {
    // PTYOutputPayload.Data carries no omitempty, and Go marshals a nil slice as
    // null rather than "". Handing null to a base64 decoder throws.
    expect(body("pty_output_empty_string")).toContain('"data":""');
    expect(body("pty_output_null_data")).toContain('"data":null');

    const nul = byName.get("pty_output_null_data")!.frame.payload as { data: string | null };
    expect(base64ToBytes(nul.data)).toHaveLength(0);
  });

  it("always writes a resync's cols, rows and cursor, even as zeros", () => {
    const exit = body("resync_exit_without_screen");
    expect(exit).toContain('"cols":0');
    expect(exit).toContain('"rows":0');
    expect(exit).toContain('"cursorX":0');
    expect(exit).toContain('"cursorY":0');
    expect(exit).toContain('"exited":true');
  });

  it("escapes html characters the way encoding/json does", () => {
    const escaped = body("resync_html_escaped_lines");
    expect(escaped).toContain(BACKSLASH + "u003c");
    expect(escaped).toContain(BACKSLASH + "u003e");
    expect(escaped).toContain(BACKSLASH + "u0026");
    expect(escaped).not.toContain("<");
    expect(escaped).not.toContain(">");
    expect(escaped).not.toContain("&");
  });

  it("keeps a req's cmd on the envelope AND in the payload", () => {
    // Authorization reads the ENVELOPE's copy without unmarshalling anything, and
    // normalizeRequest then overwrites the payload's from it. A payload naming a
    // different command would be authorized as one thing and run as another.
    const c = byName.get("req_sessions")!;
    expect(c.frame.cmd).toBe("sessions");
    expect((c.frame.payload as { cmd: string }).cmd).toBe("sessions");
  });

  it("omits a zero seq and an empty id", () => {
    expect(body("unsub_no_payload")).not.toContain("seq");
    expect(body("unsub_no_payload")).not.toContain('"id"');
    expect(body("pty_scroll_back")).not.toContain('"id"');
  });
});

// ---------------------------------------------------------------------------
// Go-compatible JSON string escaping
// ---------------------------------------------------------------------------

describe("goJSONString", () => {
  it("uses the short forms Go uses", () => {
    expect(goJSONString(LF)).toBe('"' + BACKSLASH + 'n"');
    expect(goJSONString(CR)).toBe('"' + BACKSLASH + 'r"');
    expect(goJSONString(TAB)).toBe('"' + BACKSLASH + 't"');
    expect(goJSONString('"')).toBe('"' + BACKSLASH + '""');
    expect(goJSONString(BACKSLASH)).toBe('"' + BACKSLASH + BACKSLASH + '"');
  });

  it("uses u00XX where JSON.stringify would use a short form", () => {
    // The one difference that is pure trivia until a pane prints a backspace.
    expect(goJSONString(BACKSPACE)).toBe('"' + BACKSLASH + 'u0008"');
    expect(goJSONString(FORMFEED)).toBe('"' + BACKSLASH + 'u000c"');
    expect(JSON.stringify(BACKSPACE)).not.toBe(goJSONString(BACKSPACE));
  });

  it("escapes html characters and the two line separators", () => {
    expect(goJSONString("<")).toBe('"' + BACKSLASH + 'u003c"');
    expect(goJSONString(">")).toBe('"' + BACKSLASH + 'u003e"');
    expect(goJSONString("&")).toBe('"' + BACKSLASH + 'u0026"');
    expect(goJSONString(LINE_SEP)).toBe('"' + BACKSLASH + 'u2028"');
    expect(goJSONString(PARA_SEP)).toBe('"' + BACKSLASH + 'u2029"');
  });

  it("uses lowercase hex, as Go does", () => {
    expect(goJSONString(String.fromCharCode(0x1f))).toBe('"' + BACKSLASH + 'u001f"');
  });

  it("passes non-ascii through as raw utf-8", () => {
    // Go does not \u-escape anything above 0x7f, and neither does this.
    expect(goJSONString("❯ ✻")).toBe('"❯ ✻"');
  });

  it("escapes an SGR sequence the way a resync line carries one", () => {
    expect(goJSONString(ESC + "[1m")).toBe('"' + BACKSLASH + 'u001b[1m"');
  });

  it("keeps a valid surrogate pair intact", () => {
    // A pane can legitimately print an emoji, which is a surrogate pair in
    // UTF-16 and four bytes in UTF-8. Nothing may be escaped here.
    expect(goJSONString("\u{1f680}")).toBe('"\u{1f680}"');
    expect(goJSONString("a\u{1f680}b")).toBe('"a\u{1f680}b"');
  });

  it("writes Go's replacement ESCAPE for a lone surrogate, not the character", () => {
    // Go emits the six characters \ufffd for a byte it cannot decode. Letting
    // the surrogate fall through would have TextEncoder emit ef bf bd instead:
    // the same character, different bytes, and bytes are the whole contract.
    const lone = String.fromCharCode(0xd800);
    expect(goJSONString(lone)).toBe('"' + BACKSLASH + 'ufffd"');
    expect(goJSONString("a" + String.fromCharCode(0xdfff) + "b")).toBe(
      '"a' + BACKSLASH + 'ufffdb"',
    );
    // A high surrogate at the very end of the string has nothing to pair with.
    expect(goJSONString("x" + String.fromCharCode(0xd83d))).toBe('"x' + BACKSLASH + 'ufffd"');
  });
});

// ---------------------------------------------------------------------------
// Framing
// ---------------------------------------------------------------------------

describe("framing", () => {
  const frame: Frame = { v: 1, type: "req", id: "r1", cmd: "status", payload: { cmd: "status" } };

  it("writes a four-byte big-endian length prefix", () => {
    const wire = encodeFrame(frame);
    const body = encodeFrameBody(frame);
    expect(wire.length).toBe(FRAME_HEADER_BYTES + body.length);
    // Stated as arithmetic rather than via a DataView, so the test says which
    // byte order it means instead of asking the codec.
    const n = (wire[0] << 24) | (wire[1] << 16) | (wire[2] << 8) | wire[3];
    expect(n).toBe(body.length);
    expect(wire[0]).toBe(0); // a small frame's high bytes are the zeros
    expect(wire[1]).toBe(0);
  });

  it("has no delimiter", () => {
    const wire = encodeFrame(frame);
    expect(wire[wire.length - 1]).not.toBe(0x0a);
  });

  it("refuses a body over the cap and emits nothing", () => {
    const huge: Frame = { v: 1, type: "resync", pane: "lola-x", payload: { lines: ["y".repeat(MAX_FRAME_BYTES)] } };
    expect(() => encodeFrame(huge)).toThrow(FrameTooLargeError);
  });

  it("refuses an impossible inbound length before reading a body", () => {
    const zero = new Uint8Array([0, 0, 0, 0]);
    expect(() => decodeFrame(zero)).toThrow(FrameEmptyError);

    // One over the cap, with no body at all behind it: a decoder that tried to
    // read the body would block or report a truncation instead of the refusal.
    const over = new Uint8Array(4);
    new DataView(over.buffer).setUint32(0, MAX_FRAME_BYTES + 1, false);
    expect(() => decodeFrame(over)).toThrow(FrameTooLargeError);
  });

  it("accepts a body of exactly the cap", () => {
    // The cap is on the body, not on the body plus its prefix.
    const at = new Uint8Array(FRAME_HEADER_BYTES + MAX_FRAME_BYTES);
    new DataView(at.buffer).setUint32(0, MAX_FRAME_BYTES, false);
    const pad = "z".repeat(MAX_FRAME_BYTES - '{"v":1,"type":"req","cmd":""}'.length);
    const body = new TextEncoder().encode('{"v":1,"type":"req","cmd":"' + pad + '"}');
    expect(body.length).toBe(MAX_FRAME_BYTES);
    at.set(body, FRAME_HEADER_BYTES);
    const got = decodeFrame(at);
    expect(got?.frame.cmd).toHaveLength(pad.length);
  });

  it("reports a malformed body without losing the frame boundary", () => {
    const bad = new Uint8Array(FRAME_HEADER_BYTES + 2);
    new DataView(bad.buffer).setUint32(0, 2, false);
    bad.set(new TextEncoder().encode("}{"), FRAME_HEADER_BYTES);
    expect(() => decodeFrame(bad)).toThrow(FrameMalformedError);
  });

  it("rejects a v that is not a number rather than reading it as zero", () => {
    // A client that silently read a string v as 0 would refuse a perfectly good
    // daemon with "unsupported version".
    const body = new TextEncoder().encode('{"v":"one","type":"req"}');
    expect(() => decodeFrameBody(body)).toThrow(FrameMalformedError);
  });

  it("tolerates unknown fields, on the envelope and in the payload", () => {
    // A newer peer's additive field must never be a parse failure; that is what
    // keeps the version number from having to move for every addition.
    const json = '{"v":1,"type":"sub","id":"s3","pane":"lola-fe-42","device":"ignored","payload":{"cols":55,"rows":34,"dpr":3}}';
    const f = decodeFrameBody(new TextEncoder().encode(json));
    expect(f.type).toBe("sub");
    expect(f.pane).toBe("lola-fe-42");
    expect(f.payload).toMatchObject({ cols: 55, rows: 34 });
  });
});

describe("FrameDecoder", () => {
  const a: Frame = { v: 1, type: "pty", pane: "lola-fe-42", seq: 1, payload: { data: "" } };
  const b: Frame = { v: 1, type: "pty", pane: "lola-fe-42", seq: 2, payload: { data: "AA==" } };

  it("reads several frames out of one chunk", () => {
    const wire = new Uint8Array([...encodeFrame(a), ...encodeFrame(b)]);
    expect(new FrameDecoder().pushAll(wire).map((f) => f.seq)).toEqual([1, 2]);
  });

  it("reassembles a frame split across chunks, including mid-prefix", () => {
    const wire = new Uint8Array([...encodeFrame(a), ...encodeFrame(b)]);
    const d = new FrameDecoder();
    const seen: Frame[] = [];
    // Two bytes at a time, so every split point including the middle of a
    // length prefix is exercised.
    for (let i = 0; i < wire.length; i += 2) {
      d.push(wire.subarray(i, Math.min(i + 2, wire.length)), (f) => seen.push(f));
    }
    expect(seen.map((f) => f.seq)).toEqual([1, 2]);
    expect(d.buffered).toBe(0);
  });

  it("delivers what it parsed before it throws", () => {
    const bad = new Uint8Array(FRAME_HEADER_BYTES + 2);
    new DataView(bad.buffer).setUint32(0, 2, false);
    bad.set(new TextEncoder().encode("}{"), FRAME_HEADER_BYTES);
    const wire = new Uint8Array([...encodeFrame(a), ...bad]);

    const d = new FrameDecoder();
    const seen: Frame[] = [];
    expect(() => d.push(wire, (f) => seen.push(f))).toThrow(FrameMalformedError);
    expect(seen.map((f) => f.seq)).toEqual([1]);
    // A malformed body left the boundary intact, so the decoder is still usable.
    expect(d.poisoned).toBe(false);
    expect(d.pushAll(encodeFrame(b)).map((f) => f.seq)).toEqual([2]);
  });

  it("is poisoned by a length prefix it cannot honour", () => {
    // Skipping such a frame would mean reading the very bytes the reader just
    // refused to read, so the stream cannot be resynchronized.
    const d = new FrameDecoder();
    expect(() => d.pushAll(new Uint8Array([0, 0, 0, 0]))).toThrow(FrameEmptyError);
    expect(d.poisoned).toBe(true);
    expect(() => d.pushAll(encodeFrame(a))).toThrow(FrameEmptyError);
    d.reset();
    expect(d.poisoned).toBe(false);
    expect(d.pushAll(encodeFrame(a))).toHaveLength(1);
  });
});

// ---------------------------------------------------------------------------
// base64
// ---------------------------------------------------------------------------

describe("base64", () => {
  it("matches Go's standard encoding with padding", () => {
    // These two are the exact byte strings the golden vectors carry.
    expect(bytesToBase64(new Uint8Array([0x1b, 0x5b, 0x5a, 0x00, 0xff]))).toBe("G1taAP8=");
    expect(bytesToBase64(new Uint8Array([0x79, 0x0d]))).toBe("eQ0=");
    expect(bytesToBase64(new Uint8Array([]))).toBe("");
  });

  it("round-trips every byte value, including the ones btoa mangles", () => {
    const all = new Uint8Array(256);
    for (let i = 0; i < 256; i++) all[i] = i;
    expect(base64ToBytes(bytesToBase64(all))).toEqual(all);
  });

  it("treats null, undefined and empty as no bytes", () => {
    // Not defensive programming: a nil Go []byte reaches the wire as null.
    expect(base64ToBytes(null)).toHaveLength(0);
    expect(base64ToBytes(undefined)).toHaveLength(0);
    expect(base64ToBytes("")).toHaveLength(0);
  });

  it("encodes a Uint8Array payload field without the caller base64ing it", () => {
    const f: Frame = {
      v: 1,
      type: "pty",
      pane: "lola-fe-42",
      payload: { action: "write", data: new Uint8Array([0x79, 0x0d]) },
    };
    expect(encodeFrameJSON(f)).toContain('"data":"eQ0="');
  });
});
