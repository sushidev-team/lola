import { describe, it, expect, afterEach } from "vitest";
import { scanCapability, scanErrorToOutcome, scanForPairing, scanMessage } from "./scan";

const PIN = "C4td4uyeJMSyxfoAsB3i98Kd6JhkpOTf3Oxipiq+sxI=";
const KEY = "0123456789abcdef0123456789abcdef";

function encode(body: unknown): string {
  const json = JSON.stringify(body);
  const b64 = btoa(String.fromCharCode(...new TextEncoder().encode(json)));
  return "lola1." + b64.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/** Install a fake plugin on the Capacitor global, the way the real one arrives. */
function install(p: Record<string, unknown> | undefined): void {
  (globalThis as Record<string, unknown>).Capacitor = p
    ? { Plugins: { LolaTransport: p } }
    : undefined;
}

afterEach(() => install(undefined));

describe("scanCapability", () => {
  it("says no when there is no plugin at all", async () => {
    // The state this build is actually in: LolaTransportPlugin.swift declares
    // connect/disconnect/send/status and nothing else.
    install(undefined);
    expect(await scanCapability()).toMatchObject({ available: false });
  });

  it("says no when the plugin has no scanQR", async () => {
    install({ connect: () => {} });
    expect(await scanCapability()).toMatchObject({ available: false });
  });

  it("assumes yes when scanQR exists but the probe does not", async () => {
    install({ scanQR: async () => ({ value: "" }) });
    expect(await scanCapability()).toEqual({ available: true });
  });

  it("turns the probe's reason enum into a sentence", async () => {
    install({
      scanQR: async () => ({ value: "" }),
      scanCapability: async () => ({ available: false, reason: "no_camera" }),
    });
    const c = await scanCapability();
    expect(c.available).toBe(false);
    expect(c.reason).toContain("Simulator");
  });

  it("says nothing rather than guessing at a reason it does not know", async () => {
    install({
      scanQR: async () => ({ value: "" }),
      scanCapability: async () => ({ available: false, reason: "something-new" }),
    });
    expect(await scanCapability()).toEqual({ available: false, reason: undefined });
  });

  it("fails closed when the probe throws", async () => {
    // An offered control that cannot work is worse than an absent one: the
    // absent one sends the user straight to the form that does.
    install({
      scanQR: async () => ({ value: "" }),
      scanCapability: async () => {
        throw new Error("boom");
      },
    });
    expect(await scanCapability()).toMatchObject({ available: false });
  });
});

describe("scanErrorToOutcome", () => {
  it("reads the plugin's own code first", () => {
    // The vocabulary is LolaScanErrorCode's, not one invented here.
    expect(scanErrorToOutcome({ code: "camera_denied" })).toEqual({ kind: "denied" });
    expect(scanErrorToOutcome({ code: "camera_restricted" })).toEqual({ kind: "restricted" });
    expect(scanErrorToOutcome({ code: "denied" })).toEqual({ kind: "denied" });
    expect(scanErrorToOutcome({ code: "cancelled" })).toEqual({ kind: "cancelled" });
    expect(scanErrorToOutcome({ code: "canceled" })).toEqual({ kind: "cancelled" });
    expect(scanErrorToOutcome({ code: "no_camera" })).toMatchObject({ kind: "unavailable" });
    expect(scanErrorToOutcome({ code: "unimplemented" })).toMatchObject({ kind: "unavailable" });
  });

  it("prefers the code even when the message says something else", () => {
    expect(scanErrorToOutcome({ code: "camera_denied", message: "user cancelled" })).toEqual({
      kind: "denied",
    });
  });

  it("falls back to the message for a plugin that rejects without a code", () => {
    expect(scanErrorToOutcome(new Error("Camera permission denied"))).toEqual({ kind: "denied" });
    expect(scanErrorToOutcome(new Error("not authorized"))).toEqual({ kind: "denied" });
    expect(scanErrorToOutcome(new Error("User cancelled the scan"))).toEqual({
      kind: "cancelled",
    });
    expect(scanErrorToOutcome(new Error("no camera on this device"))).toMatchObject({
      kind: "unavailable",
    });
    expect(scanErrorToOutcome(new Error("not implemented"))).toMatchObject({
      kind: "unavailable",
    });
  });

  it("does not guess from an unrelated message", () => {
    // Matching a bare "error" or "camera" against arbitrary text is how a
    // confident wrong diagnosis gets shown to somebody.
    expect(scanErrorToOutcome(new Error("AVFoundation session interrupted"))).toMatchObject({
      kind: "failed",
    });
    expect(scanErrorToOutcome(undefined)).toMatchObject({ kind: "failed" });
    expect(scanErrorToOutcome("a bare string")).toMatchObject({ kind: "failed" });
  });
});

describe("scanMessage", () => {
  it("says nothing at all about a cancel", () => {
    // The user pressed Cancel one moment ago. A banner explaining their own
    // decision back to them is the kind of noise that makes an app feel argumentative.
    expect(scanMessage({ kind: "cancelled" })).toBeNull();
    expect(scanMessage({ kind: "value", text: "x" })).toBeNull();
  });

  it("sends a denied camera to Settings, because nothing in the app can undo it", () => {
    const m = scanMessage({ kind: "denied" });
    expect(m?.hint).toContain("Settings");
    expect(m?.hint).toContain("Camera");
  });

  it("points every dead end at the form, which does the same job", () => {
    for (const o of [
      { kind: "unavailable" } as const,
      { kind: "failed" } as const,
      { kind: "denied" } as const,
      { kind: "restricted" } as const,
    ]) {
      const m = scanMessage(o);
      expect(m).not.toBeNull();
      expect(`${m!.detail} ${m!.hint ?? ""}`.toLowerCase()).toContain("below");
    }
  });

  it("relays a reason the plugin gave rather than inventing one", () => {
    expect(scanMessage({ kind: "unavailable", reason: "No camera here." })?.detail).toBe(
      "No camera here.",
    );
  });

  it("offers no Settings switch for a restricted camera, because there is none", () => {
    const m = scanMessage({ kind: "restricted" });
    expect(m?.hint ?? "").not.toContain("Settings");
  });
});

describe("scanForPairing", () => {
  const good = { addrs: ["127.0.0.1"], port: 7717, pin: PIN, key: KEY };

  it("returns a parsed payload for a good code", async () => {
    install({ scanQR: async () => ({ value: encode(good) }) });
    const r = await scanForPairing();
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(r.result.ok).toBe(true);
    if (!r.result.ok) return;
    expect(r.result.payload.key).toBe(KEY);
  });

  it("returns a parse failure as a result, not as a scan failure", async () => {
    // The scanner worked perfectly; the code was wrong. Those are different
    // sentences and the screen shows different ones.
    install({ scanQR: async () => ({ value: "https://example.com" }) });
    const r = await scanForPairing();
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(r.result).toMatchObject({ ok: false, kind: "not_lola" });
  });

  it("treats an explicit cancel and an empty value alike, and says nothing", async () => {
    install({ scanQR: async () => ({ cancelled: true }) });
    expect(await scanForPairing()).toMatchObject({ ok: false, notice: null });

    install({ scanQR: async () => ({ value: "" }) });
    expect(await scanForPairing()).toMatchObject({ ok: false, notice: null });
  });

  it("never throws, whatever the plugin does", async () => {
    install({
      scanQR: async () => {
        throw { code: "camera_denied" };
      },
    });
    const r = await scanForPairing();
    expect(r.ok).toBe(false);
    if (r.ok) return;
    expect(r.notice?.title).toContain("camera");
  });

  it("reports the absent plugin rather than pretending to scan", async () => {
    install(undefined);
    const r = await scanForPairing();
    expect(r.ok).toBe(false);
    if (r.ok) return;
    // The KIND is what the connect screen keys off to stop offering a Scan
    // button, so it travels beside the rendered message.
    expect(r.outcome.kind).toBe("unavailable");
    expect(r.notice?.title).toBe("No camera to scan with");
  });
});
