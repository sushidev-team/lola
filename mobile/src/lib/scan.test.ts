import { afterEach, describe, expect, it, vi } from "vitest";

import { scanCapability, scanErrorToOutcome, scanOnce } from "./scan";

const held = vi.hoisted(() => ({ native: null as unknown }));
vi.mock("../native", () => ({
  get native() {
    return held.native;
  },
}));

afterEach(() => {
  held.native = null;
});

describe("scanCapability", () => {
  it("says no when there is no native transport", async () => {
    expect(await scanCapability()).toEqual({ available: false, reason: "unsupported" });
  });

  it("relays the plugin's answer and reason", async () => {
    held.native = {
      scanCapability: async () => ({ available: false, authorization: "denied", reason: "no_camera" }),
    };
    expect(await scanCapability()).toEqual({ available: false, reason: "no_camera" });
  });

  it("counts notDetermined as available so the permission prompt can show", async () => {
    held.native = {
      scanCapability: async () => ({ available: true, authorization: "notDetermined" }),
    };
    expect(await scanCapability()).toMatchObject({ available: true });
  });

  it("fails closed when the probe throws", async () => {
    held.native = {
      scanCapability: async () => {
        throw new Error("boom");
      },
    };
    expect(await scanCapability()).toEqual({ available: false });
  });
});

describe("scanErrorToOutcome", () => {
  it("reads the plugin's own code first", () => {
    expect(scanErrorToOutcome({ code: "camera_denied" })).toEqual({ kind: "denied" });
    expect(scanErrorToOutcome({ code: "denied" })).toEqual({ kind: "denied" });
    expect(scanErrorToOutcome({ code: "camera_restricted" })).toEqual({ kind: "restricted" });
    expect(scanErrorToOutcome({ code: "cancelled" })).toEqual({ kind: "cancelled" });
    expect(scanErrorToOutcome({ code: "canceled" })).toEqual({ kind: "cancelled" });
    expect(scanErrorToOutcome({ code: "no_camera" })).toEqual({ kind: "unavailable", reason: "no_camera" });
    expect(scanErrorToOutcome({ code: "unimplemented" })).toEqual({
      kind: "unavailable",
      reason: "unsupported",
    });
  });

  it("prefers the code even when the message says something else", () => {
    expect(scanErrorToOutcome({ code: "camera_denied", message: "user cancelled" })).toEqual({
      kind: "denied",
    });
  });

  it("falls back to a narrow reading of the message", () => {
    expect(scanErrorToOutcome(new Error("Camera permission denied"))).toEqual({ kind: "denied" });
    expect(scanErrorToOutcome(new Error("User cancelled the scan"))).toEqual({ kind: "cancelled" });
    expect(scanErrorToOutcome(new Error("no camera on this device"))).toMatchObject({
      kind: "unavailable",
    });
    expect(scanErrorToOutcome(new Error("no native transport"))).toMatchObject({
      kind: "unavailable",
    });
  });

  it("does not guess from an unrelated message", () => {
    expect(scanErrorToOutcome(new Error("AVFoundation session interrupted"))).toEqual({
      kind: "failed",
      reason: "AVFoundation session interrupted",
    });
    expect(scanErrorToOutcome(undefined)).toEqual({ kind: "failed", reason: undefined });
    expect(scanErrorToOutcome("a bare string")).toMatchObject({ kind: "failed" });
  });
});

describe("scanOnce", () => {
  it("returns only the public half of a scanned payload", async () => {
    held.native = {
      scanPairing: async () => ({
        cancelled: false,
        pairRef: "ref-1",
        daemonId: "d1",
        addrs: 2,
        relay: true,
      }),
    };
    expect(await scanOnce()).toEqual({
      kind: "scanned",
      pairRef: "ref-1",
      daemonId: "d1",
      addrs: 2,
      relay: true,
    });
  });

  it("passes the prompt through only when given", async () => {
    const scanPairing = vi.fn(async () => ({ cancelled: true as const }));
    held.native = { scanPairing };
    await scanOnce();
    await scanOnce("Scan the code");
    expect(scanPairing.mock.calls).toEqual([[undefined], [{ prompt: "Scan the code" }]]);
  });

  it("reports a cancel as an outcome", async () => {
    held.native = { scanPairing: async () => ({ cancelled: true }) };
    expect(await scanOnce()).toEqual({ kind: "cancelled" });
  });

  it("reports a code that is not a pairing code", async () => {
    held.native = { scanPairing: async () => ({ cancelled: false, invalid: "old_format" }) };
    expect(await scanOnce()).toEqual({ kind: "invalid", why: "old_format" });
  });

  it("never throws, whatever the plugin does", async () => {
    held.native = {
      scanPairing: async () => {
        throw { code: "camera_denied" };
      },
    };
    expect(await scanOnce()).toEqual({ kind: "denied" });
  });

  it("reports the absent transport rather than pretending to scan", async () => {
    expect(await scanOnce()).toEqual({ kind: "unavailable", reason: "unsupported" });
  });
});
