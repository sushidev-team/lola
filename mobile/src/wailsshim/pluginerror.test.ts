import { describe, expect, it } from "vitest";
import { refusalFromPluginError, stateError } from "./pluginerror";
import { WireRefusalError } from "../wire";

// THE BUG THIS FILE EXISTS FOR, in one sentence: a wrong access key was
// reported to the user as "Not on <host>'s network".
//
// The daemon answers a bad bearer key with an `err` frame carrying `denied` and
// then closes, the plugin classifies that as a refusal, and `diagnose` has a
// branch for it that names the field to retype. None of that reached the
// screen, because the structured code rode a `state` event that arrives a
// bridge hop AFTER the connect rejection, and the transport recorded only
// `status.error`. So the one failure with a one-field fix was rendered as the
// one with no fix at all — identical, pixel for pixel, to pointing the app at a
// dead port.

describe("refusalFromPluginError", () => {
  it("reads the daemon's code out of the connect rejection", () => {
    // THE SHAPE CAPACITOR ACTUALLY PRODUCES, which is not the obvious one and
    // is what made the first fix look correct and still fail on the device:
    // `CAPPluginCallError` stores `.dictionary(["data": data])`, so the fields
    // a plugin passes to `reject(…, data)` arrive NESTED under `data` rather
    // than flat on the error. The first version of this test hand-built a flat
    // error, passed, and pinned nothing.
    const err = Object.assign(new Error("the daemon refused the connection (denied)"), {
      code: "rejected",
      errorMessage: "the daemon refused the connection (denied)",
      data: { daemonCode: "denied" },
    });
    const refusal = refusalFromPluginError(err);
    expect(refusal).toBeInstanceOf(WireRefusalError);
    expect(refusal?.code).toBe("denied");
  });

  it("also reads a flat daemonCode, so the plugin may hand it over either way", () => {
    const err = Object.assign(new Error("refused"), { code: "rejected", daemonCode: "denied" });
    expect(refusalFromPluginError(err)?.code).toBe("denied");
  });

  it("carries the version bounds, which are the whole of a skew message", () => {
    const err = Object.assign(new Error("refused"), {
      code: "protocol",
      data: { daemonCode: "unsupported_version", minV: 2, maxV: 3 },
    });
    const refusal = refusalFromPluginError(err);
    expect(refusal?.code).toBe("unsupported_version");
    expect(refusal?.minV).toBe(2);
    expect(refusal?.maxV).toBe(3);
  });

  it("invents nothing for a transport failure", () => {
    // A timeout, a pin mismatch and an unreachable host all reject with a
    // transport code and NO daemon code, and every one of them is diagnosed
    // correctly from the error alone. Manufacturing a refusal for those would
    // swap one wrong sentence for another.
    for (const code of ["timeout", "pin_mismatch", "network"]) {
      expect(refusalFromPluginError(Object.assign(new Error("x"), { code }))).toBeUndefined();
      // A rejection with a data bag that carries no daemon code is still not a
      // refusal.
      expect(
        refusalFromPluginError(Object.assign(new Error("x"), { code, data: {} })),
      ).toBeUndefined();
    }
    expect(refusalFromPluginError(undefined)).toBeUndefined();
    expect(refusalFromPluginError("not an object")).toBeUndefined();
  });
});

describe("stateError", () => {
  it("turns a refusal state event into a WireRefusalError", () => {
    const err = stateError({
      phase: "failed",
      code: "rejected",
      reason: "the daemon refused the connection (denied)",
      daemonCode: "denied",
    });
    expect(err).toBeInstanceOf(WireRefusalError);
    expect((err as WireRefusalError).code).toBe("denied");
  });

  it("leaves a transport failure a plain Error naming its code", () => {
    const err = stateError({ phase: "failed", code: "timeout", reason: "budget elapsed" });
    expect(err).not.toBeInstanceOf(WireRefusalError);
    expect(err.message).toBe("timeout: budget elapsed");
  });
});
