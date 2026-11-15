import { describe, expect, it } from "vitest";
import { copy } from "./copy";
import { refusalText } from "./refusal";
import { ConnectionClosedError, WireRefusalError } from "@mobile/wire/correlator";
import { ShimNotConnectedError, UnsupportedOnMobileError } from "@mobile/wailsshim/errors";

describe("refusalText", () => {
  it("says to update for unknown_cmd, whatever the daemon wrote", () => {
    expect(refusalText(new WireRefusalError("unknown_cmd", ""))).toBe(copy.refusals.update);
    expect(refusalText(new WireRefusalError("unknown_cmd", 'unknown cmd "feedback"'))).toBe(copy.refusals.update);
  });

  it("uses the daemon's message for denied, else the no-tier sentence", () => {
    expect(refusalText(new WireRefusalError("denied", "Ask on your Mac."))).toBe("Ask on your Mac.");
    expect(refusalText(new WireRefusalError("denied", ""))).toBe(copy.notices.noTier.body);
  });

  it("rate limits get their own sentence", () => {
    expect(refusalText(new WireRefusalError("rate_limited", "slow down"))).toBe(copy.refusals.rateLimited);
  });

  it("any other code shows the daemon's message part, never the code", () => {
    expect(refusalText(new WireRefusalError("bad_args", "Pick a project."))).toBe("Pick a project.");
    expect(refusalText(new WireRefusalError("bad_args", ""))).toBe(copy.refusals.generic);
    expect(refusalText(new WireRefusalError("bad_args", ""))).not.toContain("bad_args");
  });

  it("maps connection loss, the Mac-only wall and plain errors", () => {
    expect(refusalText(new ConnectionClosedError("socket"))).toBe(copy.refusals.offline);
    expect(refusalText(new ShimNotConnectedError("X.Y"))).toBe(copy.refusals.offline);
    expect(refusalText(new UnsupportedOnMobileError("DaemonService.Start", "no"))).toBe(copy.notices.macOnly.body);
    expect(refusalText(new Error("boom"))).toBe("boom");
    expect(refusalText("what")).toBe(copy.refusals.generic);
  });
});
