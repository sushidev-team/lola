import { describe, expect, it } from "vitest";
import { LinkError, pluginFailure, refusalFromPluginError, stateError, toFailureCode } from "./pluginerror";
import { WireRefusalError } from "../wire";

// A daemon refusal must reach the screen as a refusal, never as a network
// failure: the two have different fixes and look identical at the socket.

describe("refusalFromPluginError", () => {
  it("reads the daemon's code nested under data, where Capacitor puts it", () => {
    const err = Object.assign(new Error("refused"), { code: "protocol", data: { daemonCode: "denied" } });
    const refusal = refusalFromPluginError(err);
    expect(refusal).toBeInstanceOf(WireRefusalError);
    expect(refusal?.code).toBe("denied");
  });

  it("also reads a flat daemonCode", () => {
    const err = Object.assign(new Error("refused"), { code: "protocol", daemonCode: "not_paired" });
    expect(refusalFromPluginError(err)?.code).toBe("not_paired");
  });

  it("carries the version bounds", () => {
    const err = Object.assign(new Error("refused"), {
      code: "unsupported_version",
      data: { daemonCode: "unsupported_version", minV: 2, maxV: 3 },
    });
    const refusal = refusalFromPluginError(err);
    expect(refusal?.minV).toBe(2);
    expect(refusal?.maxV).toBe(3);
  });

  it("invents nothing for a transport failure", () => {
    for (const code of ["timeout", "unreachable", "key_changed"]) {
      expect(refusalFromPluginError(Object.assign(new Error("x"), { code }))).toBeUndefined();
      expect(refusalFromPluginError(Object.assign(new Error("x"), { code, data: {} }))).toBeUndefined();
    }
    expect(refusalFromPluginError(undefined)).toBeUndefined();
    expect(refusalFromPluginError("not an object")).toBeUndefined();
  });
});

describe("stateError", () => {
  it("turns a daemon refusal into a WireRefusalError", () => {
    const err = stateError({ phase: "failed", code: "not_paired", daemonCode: "not_paired" });
    expect(err).toBeInstanceOf(WireRefusalError);
    expect((err as WireRefusalError).code).toBe("not_paired");
  });

  it("types a transport failure by its failure code", () => {
    const err = stateError({ phase: "failed", code: "timeout", reason: "budget elapsed" });
    expect(err).toBeInstanceOf(LinkError);
    expect((err as LinkError).code).toBe("timeout");
    expect(err.message).toBe("timeout: budget elapsed");
  });

  it("treats a code it does not know as internal", () => {
    expect((stateError({ phase: "failed", code: "from_the_future" }) as LinkError).code).toBe("internal");
    expect(toFailureCode(undefined)).toBe("internal");
  });
});

describe("pluginFailure", () => {
  it("prefers the refusal, else the failure code", () => {
    expect(pluginFailure(Object.assign(new Error("x"), { data: { daemonCode: "denied" } }))).toBeInstanceOf(
      WireRefusalError,
    );
    const link = pluginFailure(Object.assign(new Error("no route"), { code: "unreachable" })) as LinkError;
    expect(link.code).toBe("unreachable");
    expect((pluginFailure("boom") as LinkError).code).toBe("internal");
  });
});
