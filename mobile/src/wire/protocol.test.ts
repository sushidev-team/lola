import { describe, expect, it } from "vitest";

import {
  DEFAULT_REMOTE_PORT,
  DENIED_COMMANDS,
  FRAME_HEADER_BYTES,
  FRAME_VERSION_CURRENT,
  FRAME_VERSION_MIN,
  HELLO_CMD,
  INSECURE_MIN_KEY_LEN,
  MAX_FRAME_BYTES,
  MAX_PANE_NAME,
  MAX_REQUESTS_IN_FLIGHT,
  MAX_SCROLL_LINES,
  classifyGap,
  clientAcceptsFrame,
  commandDenied,
  daemonAcceptsFrame,
  errorFrame,
  helloFrame,
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

// See the note at the top of codec.test.ts: these tests are written but were not
// executed here, because mobile/ has no node_modules yet.

describe("constants that must equal the Go side", () => {
  // Each of these is a number the daemon enforces. Getting one wrong is not a
  // rejected frame, it is a dropped socket or a refused connection, so they are
  // asserted literally here rather than derived from anything.
  it("mirrors internal/protocol", () => {
    expect(MAX_FRAME_BYTES).toBe(1 << 20); // protocol.MaxFrameBytes
    expect(FRAME_HEADER_BYTES).toBe(4); // protocol.FrameHeaderBytes
    expect(FRAME_VERSION_CURRENT).toBe(1); // protocol.FrameVersionCurrent
    expect(FRAME_VERSION_MIN).toBe(1); // protocol.FrameVersionMin
  });

  it("mirrors internal/remote and internal/config", () => {
    expect(MAX_REQUESTS_IN_FLIGHT).toBe(4); // remote.reqConcurrency
    expect(MAX_PANE_NAME).toBe(128); // remote.maxPaneName
    expect(MAX_SCROLL_LINES).toBe(500); // tmux.MaxScrollLines
    expect(DEFAULT_REMOTE_PORT).toBe(7717); // config.DefaultRemotePort
    expect(INSECURE_MIN_KEY_LEN).toBe(16); // remote.insecureMinKeyLen
    expect(HELLO_CMD).toBe("remote.hello"); // remote.helloCmd
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
  ];

  it.each(table)("%s: toDaemon=%s toClient=%s", (typ, toDaemon, toClient) => {
    expect(daemonAcceptsFrame(typ)).toBe(toDaemon);
    expect(clientAcceptsFrame(typ)).toBe(toClient);
    expect(knownFrameType(typ)).toBe(true);
  });

  it.each([[""], ["REQ"], ["event"], ["pair.begin"]])("%s fails closed in both directions", (typ) => {
    expect(knownFrameType(typ)).toBe(false);
    expect(daemonAcceptsFrame(typ)).toBe(false);
    expect(clientAcceptsFrame(typ)).toBe(false);
  });
});

describe("the version window", () => {
  it("is a closed interval with no v0", () => {
    expect(supportedFrameVersion(1)).toBe(true);
    for (const v of [-1, 0, 2, 99]) expect(supportedFrameVersion(v)).toBe(false);
  });

  it("refuses a non-integer", () => {
    expect(supportedFrameVersion(1.5)).toBe(false);
    expect(supportedFrameVersion(NaN)).toBe(false);
  });
});

describe("command policy", () => {
  // Mirrored from internal/remote/policy.go for ONE reason: the daemon answers a
  // denied cmd with unknown_cmd and then closes, so a single mistyped command
  // costs a full reconnect and a re-subscribe of every pane.
  it.each(DENIED_COMMANDS.map((c) => [c]))("denies %s", (cmd) => {
    expect(commandDenied(cmd)).toBe(true);
  });

  it("denies an empty command", () => {
    // A req frame naming nothing has nothing to authorize.
    expect(commandDenied("")).toBe(true);
  });

  it("denies the whole remote namespace, the hello included", () => {
    expect(commandDenied("remote.hello")).toBe(true);
    expect(commandDenied("remote.anything")).toBe(true);
  });

  it("permits the commands the app actually uses", () => {
    for (const cmd of [
      "status",
      "sessions",
      "projects",
      "prs",
      "tickets",
      "pane",
      "answer",
      "resolveConflict",
      "review",
      "coderabbit",
      "dev",
      "devFreePort",
      "revive",
      "pollOnce",
      "enable",
      "disable",
      "open",
      "openPr",
      "openManual",
      "openTicket",
      "switchAgent",
      "openURL",
      "kill",
    ]) {
      expect(commandDenied(cmd)).toBe(false);
    }
  });

  it("does not deny kill, whose FIELD is denied instead", () => {
    // Discarding a dirty worktree is the one gate teardown has, so Force is
    // cleared by the daemon rather than the command being refused — and the
    // client's request builder has no force field at all.
    expect(commandDenied("kill")).toBe(false);
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
    // match if this order does: v, type, id, cmd, pane, seq, payload.
    const f: Frame = { v: 1, type: "pty", id: "w1", pane: "lola-fe-42", seq: 3, payload: {} };
    const json = encodeFrameJSON(f);
    const order = ["v", "type", "id", "cmd", "pane", "seq", "payload"]
      .map((k) => json.indexOf('"' + k + '"'))
      .filter((i) => i >= 0);
    expect(order).toEqual([...order].sort((a, b) => a - b));
  });

  it("builds a request payload in protocol.Request's declaration order", () => {
    const p = requestPayload("answer", { text: "yes", session: "lola-fe-42", poll: "fe" });
    expect(Object.keys(p)).toEqual(["cmd", "poll", "session", "text"]);
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

  it("builds the bearer hello with the key outside any Request field", () => {
    // The key is not a command argument, and putting it in one would make it
    // something a future handler could read.
    const f = helloFrame("h1", "replace-me-with-a-real-key");
    expect(f.cmd).toBe(HELLO_CMD);
    expect(f.payload).toEqual({ key: "replace-me-with-a-real-key" });
    expect(Object.keys(f.payload as object)).not.toContain("cmd");
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
