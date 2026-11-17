import { describe, expect, it } from "vitest";

import { copy } from "./copy";
import {
  connectGlyph,
  connectTone,
  groupCode,
  invalidNotice,
  noticeWords,
  outcomeState,
  pairError,
  SCAN_AGAIN,
  stateWords,
} from "./paircopy";
import { CONNECT_STATES } from "./router.svelte";

describe("paircopy", () => {
  it("has words for every connect state, from the copy table", () => {
    for (const s of CONNECT_STATES) expect(stateWords(s).title).not.toBe("");
    expect(stateWords("refused")).toEqual(copy.connect.refused);
    expect(stateWords("not-paired").action).toBe("Pair again");
    expect(SCAN_AGAIN).toBe("Scan again");
  });

  it("maps every non-paired native outcome to its state", () => {
    expect(outcomeState("refused")).toBe("refused");
    expect(outcomeState("expired")).toBe("expired");
    expect(outcomeState("failed")).toBe("failed");
    expect(outcomeState("dropped")).toBe("dropped");
  });

  it("maps an unusable scan to its sentence", () => {
    expect(noticeWords(invalidNotice("not_lola"))).toEqual(copy.connect.invalidScan);
    expect(noticeWords(invalidNotice("malformed"))).toEqual(copy.connect.invalidScan);
    expect(noticeWords(invalidNotice("old_format"))).toEqual(copy.connect.oldCode);
  });

  it("keeps the scan for a pairing that never wrote its proof", () => {
    for (const code of ["unreachable", "timeout", "offline"]) {
      expect(pairError({ code })).toEqual({ kind: "notice", notice: "unreachable" });
    }
    expect(noticeWords("unreachable").body).toBe("Can't reach your Mac.");
  });

  it("names which side must update", () => {
    expect(pairError({ code: "unsupported_version", data: { maxV: 1 } })).toEqual({
      kind: "notice",
      notice: "versionMac",
    });
    expect(pairError({ code: "unsupported_version", maxV: 99 })).toEqual({
      kind: "notice",
      notice: "versionApp",
    });
    expect(pairError({ code: "unsupported_version" })).toEqual({ kind: "notice", notice: "versionApp" });
  });

  it("treats a lapsed hold as expired, a cancel as nothing and the rest as failed", () => {
    expect(pairError({ code: "expired" })).toEqual({ kind: "state", state: "expired" });
    expect(pairError({ code: "client_closed" })).toEqual({ kind: "cancelled" });
    expect(pairError({ code: "internal" })).toEqual({ kind: "state", state: "failed" });
    expect(pairError(null)).toEqual({ kind: "state", state: "failed" });
    expect(pairError("boom")).toEqual({ kind: "state", state: "failed" });
  });

  it("groups six digits and leaves anything else alone", () => {
    expect(groupCode("123456")).toBe("123 456");
    expect(groupCode("12345")).toBe("12345");
    expect(groupCode("")).toBe("");
  });
});

describe("connectGlyph", () => {
  it("gives every state its own glyph", () => {
    expect(connectGlyph("no-camera", null)).toBe("camera-off");
    expect(connectGlyph("scan", "cameraOff")).toBe("camera-off");
    expect(connectGlyph("refused", null)).toBe("shield-x");
    expect(connectGlyph("expired", null)).toBe("timer-off");
    expect(connectGlyph("failed", null)).toBe("triangle-alert");
    expect(connectGlyph("dropped", null)).toBe("wifi-off");
    expect(connectGlyph("not-paired", null)).toBe("unplug");
    expect(connectGlyph("code", null)).toBe("laptop");
    expect(connectGlyph("scan", null)).toBe("camera");
  });

  it("colours them by meaning", () => {
    expect(connectTone("shield-x")).toBe("text-bad");
    expect(connectTone("triangle-alert")).toBe("text-bad");
    expect(connectTone("timer-off")).toBe("text-warn");
    expect(connectTone("wifi-off")).toBe("text-warn");
    expect(connectTone("laptop")).toBe("text-accent");
    expect(connectTone("unplug")).toBe("text-faint");
    expect(connectTone("camera-off")).toBe("text-faint");
  });
});
