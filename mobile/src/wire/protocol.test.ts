import { describe, expect, it } from "vitest";

import {
  FRAME_HEADER_BYTES,
  FRAME_VERSION_CURRENT,
  FRAME_VERSION_MIN,
  FRAME_EVENT,
  alwaysFatalCode,
  classifyGap,
  clientAcceptsFrame,
  daemonAcceptsFrame,
  errorFrame,
  knownFrameType,
  ptyResizeFrame,
  ptyScrollFrame,
  ptyWriteFrame,
  requestFrame,
  requestPayload,
  subFrame,
  supportedFrameVersion,
  unsubFrame,
  validPaneName,
  type Frame,
} from "./protocol";
import { encodeFrameJSON } from "./codec";
import { MAX_PANE_NAME, MAX_SCROLL_LINES } from "./limits";

describe("the envelope version", () => {
  it("is 2 and only 2: the daemon refuses every other version", () => {
    expect(FRAME_VERSION_CURRENT).toBe(2); // protocol.FrameVersionCurrent
    expect(FRAME_VERSION_MIN).toBe(2); // protocol.FrameVersionMin
    expect(FRAME_HEADER_BYTES).toBe(4); // protocol.FrameHeaderBytes
  });

  it("stamps every builder with it", () => {
    for (const f of [requestFrame("a", "status"), subFrame("b", "lola-x"), unsubFrame("lola-x"), ptyWriteFrame("lola-x", "AA=="), errorFrame("c", "internal")]) {
      expect(f.v).toBe(2);
    }
  });
});

describe("the direction table", () => {
  // Direction is part of the contract: a daemon receiving a daemon-to-client
  // type refuses the frame and CLOSES, so a client that sends one loses every
  // live pane subscription with it.
  const table: Array<[string, boolean, boolean]> = [
    ["req", true, false],
    ["sub", true, false],
    ["unsub", true, false],
    ["resp", false, true],
    ["resync", false, true],
    ["pty", true, true],
    ["err", true, true],
    [FRAME_EVENT, false, true],
  ];

  it.each(table)("%s: toDaemon=%s toClient=%s", (typ, toDaemon, toClient) => {
    expect(daemonAcceptsFrame(typ)).toBe(toDaemon);
    expect(clientAcceptsFrame(typ)).toBe(toClient);
    expect(knownFrameType(typ)).toBe(true);
  });

  it.each([[""], ["REQ"], ["EVENT"], ["pair.begin"]])("%s fails closed in both directions", (typ) => {
    expect(knownFrameType(typ)).toBe(false);
    expect(daemonAcceptsFrame(typ)).toBe(false);
    expect(clientAcceptsFrame(typ)).toBe(false);
  });
});

describe("the version window", () => {
  it("is a closed interval with no v0", () => {
    expect(supportedFrameVersion(2)).toBe(true);
    for (const v of [-1, 0, 1, 3, 99]) expect(supportedFrameVersion(v)).toBe(false);
  });

  it("refuses a non-integer", () => {
    expect(supportedFrameVersion(1.5)).toBe(false);
    expect(supportedFrameVersion(NaN)).toBe(false);
  });
});

describe("request payload", () => {
  it("has no force field for kill: the daemon clears it", () => {
    // Discarding a dirty worktree is the one gate teardown has.
    expect(Object.keys(requestPayload("kill", { session: "lola-fe-42" }))).not.toContain("force");
  });
});

describe("pane names", () => {
  it.each([
    ["lola-fe-42"],
    ["lola-fe-42-shell-1"],
    ["lola-fe-42-review"],
    ["lola-fe-42-dev-2"],
    ["lola-a.b_c-1"],
  ])("accepts %s", (n) => {
    expect(validPaneName(n)).toBe(true);
  });

  it.each([[""], ["fe-42"], ["lola-"], ["lola-fe-42;kill"], ["LOLA-fe-42"], ["lola-fe-42 "]])(
    "refuses %s",
    (n) => {
      expect(validPaneName(n)).toBe(false);
    },
  );

  it("refuses a name past the byte cap, counted in utf-8", () => {
    // "lola-" is five characters, so the longest legal tail is MAX_PANE_NAME-5.
    expect(validPaneName("lola-" + "a".repeat(MAX_PANE_NAME - 5))).toBe(true);
    expect(validPaneName("lola-" + "a".repeat(MAX_PANE_NAME - 4))).toBe(false);
    // Multi-byte characters are not valid in a lola name anyway, but the length
    // must be counted in BYTES because that is what the daemon bounds.
    expect(validPaneName("lola-" + "é".repeat(MAX_PANE_NAME))).toBe(false);
  });
});

describe("frame builders", () => {
  it("puts the envelope fields in the daemon's declaration order", () => {
    // encoding/json emits struct fields in declaration order, so the bytes only
    // match if this order does: v, type, id, cmd, pane, topic, seq, payload.
    const f: Frame = { v: 2, type: "event", id: "w1", cmd: "c", pane: "p", topic: "talk:x", seq: 3, payload: {} };
    const json = encodeFrameJSON(f);
    const order = ["v", "type", "id", "cmd", "pane", "topic", "seq", "payload"]
      .map((k) => json.indexOf('"' + k + '"'))
      .filter((i) => i >= 0);
    expect(order).toHaveLength(8);
    expect(order).toEqual([...order].sort((a, b) => a - b));
  });

  it("builds a request payload in protocol.Request's declaration order", () => {
    const p = requestPayload("answer", { text: "yes", session: "lola-fe-42", poll: "fe" });
    expect(Object.keys(p)).toEqual(["cmd", "poll", "session", "text"]);
  });

  it("places keepWorktree after session and drops it when false", () => {
    expect(Object.keys(requestPayload("kill", { text: "t", keepWorktree: true, session: "s" }))).toEqual([
      "cmd",
      "session",
      "keepWorktree",
      "text",
    ]);
    expect(requestPayload("kill", { session: "s", keepWorktree: false })).toEqual({ cmd: "kill", session: "s" });
  });

  it("writes cmd to both the envelope and the payload", () => {
    const f = requestFrame("r1", "sessions");
    expect(f.cmd).toBe("sessions");
    expect(f.payload).toEqual({ cmd: "sessions" });
  });

  it("drops a zero viewport from a sub rather than sending zeros", () => {
    expect(subFrame("s1", "lola-fe-42").payload).toBeUndefined();
    expect(subFrame("s1", "lola-fe-42", { cols: 0, rows: 0 }).payload).toBeUndefined();
    expect(subFrame("s1", "lola-fe-42", { cols: 55, rows: 34 }).payload).toEqual({
      cols: 55,
      rows: 34,
    });
  });

  it("gives an unsub no id and no payload", () => {
    // It is unacknowledged, so there is nothing for an id to correlate.
    const f = unsubFrame("lola-fe-42");
    expect(f.id).toBeUndefined();
    expect(f.payload).toBeUndefined();
  });

  it("clamps a scroll to the daemon's own limit", () => {
    expect((ptyScrollFrame("lola-fe-42", -10_000).payload as { lines: number }).lines).toBe(
      -MAX_SCROLL_LINES,
    );
    expect((ptyScrollFrame("lola-fe-42", 10_000).payload as { lines: number }).lines).toBe(
      MAX_SCROLL_LINES,
    );
    expect((ptyScrollFrame("lola-fe-42", -3).payload as { lines: number }).lines).toBe(-3);
  });

  it("builds the pty actions the daemon's closed vocabulary names", () => {
    expect((ptyWriteFrame("lola-fe-42", "eQ0=").payload as { action: string }).action).toBe("write");
    expect((ptyScrollFrame("lola-fe-42", -1).payload as { action: string }).action).toBe("scroll");
    expect((ptyResizeFrame("lola-fe-42", 55, 34).payload as { action: string }).action).toBe(
      "resize",
    );
  });

  it("omits a zero pty field, exactly as Go's omitempty does", () => {
    // These builders are held to the same byte-for-byte contract as the request
    // builder: `PTYInputPayload` marks Data, Lines, Cols and Rows omitempty, so
    // a zero value is ABSENT from the daemon's own encoding of the same struct.
    // Nothing on the wire breaks if it is written — the daemon parses JSON, it
    // does not compare bytes — but this file's whole premise is reproducing that
    // encoding, and a divergence with no test is one nobody finds.
    expect(ptyWriteFrame("lola-fe-42", "").payload).toEqual({ action: "write" });
    expect(ptyScrollFrame("lola-fe-42", 0).payload).toEqual({ action: "scroll" });
    expect(ptyResizeFrame("lola-fe-42", 0, 0).payload).toEqual({ action: "resize" });
    // ...and they are omitted independently.
    expect(ptyResizeFrame("lola-fe-42", 0, 24).payload).toEqual({ action: "resize", rows: 24 });
    expect(ptyResizeFrame("lola-fe-42", 80, 0).payload).toEqual({ action: "resize", cols: 80 });
  });

  it("keeps a non-zero pty field", () => {
    expect(ptyWriteFrame("lola-fe-42", "eQ0=").payload).toEqual({ action: "write", data: "eQ0=" });
    expect(ptyScrollFrame("lola-fe-42", -3).payload).toEqual({ action: "scroll", lines: -3 });
    expect(ptyResizeFrame("lola-fe-42", 55, 34).payload).toEqual({
      action: "resize",
      cols: 55,
      rows: 34,
    });
  });

  it("omits an empty error message", () => {
    expect(errorFrame("x", "internal").payload).toEqual({ code: "internal" });
  });
});

describe("sequence gaps", () => {
  const at = (type: string, seq: number): Frame => ({ v: 1, type, pane: "lola-fe-42", seq });

  it("treats the first frame of a subscription as contiguous", () => {
    expect(classifyGap(0, at("resync", 1))).toBe("ok");
    expect(classifyGap(0, at("resync", 97))).toBe("ok");
  });

  it("treats a contiguous frame as contiguous", () => {
    expect(classifyGap(8, at("pty", 9))).toBe("ok");
  });

  it("calls a gap arriving on a resync self-healing", () => {
    // The daemon marks a slow subscriber desynced, withholds output while the
    // counter advances, then sends a fresh full screen. Repaint and adopt.
    expect(classifyGap(9, at("resync", 40))).toBe("repaired");
  });

  it("calls a gap arriving on pty output torn", () => {
    // A byte range cannot resume from halfway through an escape sequence, so
    // there is nothing to do but subscribe again.
    expect(classifyGap(9, at("pty", 40))).toBe("torn");
  });

  it("ignores an unnumbered frame and a number that went backwards", () => {
    expect(classifyGap(9, { v: 1, type: "pty", pane: "lola-fe-42" })).toBe("ok");
    expect(classifyGap(40, at("pty", 9))).toBe("ok");
  });
});

describe("refusals", () => {
  it("treats a refused command as that request's problem, not the socket's", () => {
    for (const code of ["denied", "unknown_cmd", "rate_limited", "unknown_pane", "internal"]) {
      expect(alwaysFatalCode(code)).toBe(false);
    }
  });

  it("still treats a version refusal and an oversize frame as the end of the socket", () => {
    expect(alwaysFatalCode("unsupported_version")).toBe(true);
    expect(alwaysFatalCode("frame_too_large")).toBe(true);
  });
});
