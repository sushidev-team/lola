import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => {
  const t = {
    listener: null as null | ((e: unknown) => void),
    remove: vi.fn(),
    pairBegin: vi.fn(),
    pairCancel: vi.fn(async () => {}),
    setIdleTimerDisabled: vi.fn(async () => {}),
    addListener: vi.fn(),
  };
  t.addListener.mockImplementation(async (_n: string, f: (e: unknown) => void) => {
    t.listener = f;
    return { remove: t.remove };
  });
  return {
    t,
    cap: vi.fn(),
    scanOnce: vi.fn(),
    haptic: vi.fn(),
    refresh: vi.fn(async () => []),
    connect: vi.fn(async () => true),
    retryLater: vi.fn(),
    replace: vi.fn(),
    conn: { daemonId: "d1", diagnosis: { retryable: false } },
  };
});

vi.mock("@mobile/native", () => ({ transport: h.t }));
vi.mock("./scan", () => ({ scanCapability: h.cap, scanOnce: h.scanOnce }));
vi.mock("./haptics", () => ({ haptic: h.haptic }));
vi.mock("./connection.svelte", () => ({
  connection: {
    get daemonId() {
      return h.conn.daemonId;
    },
    get diagnosis() {
      return h.conn.diagnosis;
    },
    refresh: h.refresh,
    connect: h.connect,
    retryLater: h.retryLater,
  },
}));
vi.mock("./router.svelte", () => ({ router: { replace: h.replace } }));

import { deviceName, pairing } from "./pairing.svelte";

const scanned = { kind: "scanned", pairRef: "r1", daemonId: "d1", addrs: 1, relay: false };
const settle = () => new Promise((r) => setTimeout(r, 0));

beforeEach(() => {
  pairing.close();
  vi.clearAllMocks();
  h.t.listener = null;
  h.cap.mockResolvedValue({ available: true });
  h.scanOnce.mockResolvedValue(scanned);
  h.t.pairBegin.mockResolvedValue({ code: "123456", path: { kind: "direct" } });
  h.conn.diagnosis = { retryable: false };
});

describe("pairing", () => {
  it("scans on open, shows the code after the proof and holds the screen awake", async () => {
    pairing.open("scan");
    await settle();
    expect(h.t.pairBegin).toHaveBeenCalledWith({ pairRef: "r1", deviceName: "iPhone" });
    expect(pairing.view).toBe("code");
    expect(pairing.code).toBe("123456");
    expect(h.t.setIdleTimerDisabled).toHaveBeenCalledWith({ disabled: true });
  });

  it("does not scan for a state shown as given", () => {
    pairing.open("expired");
    expect(pairing.view).toBe("expired");
    expect(h.cap).not.toHaveBeenCalled();
  });

  it("leaves the scan alone after a cancel and waits for a tap", async () => {
    h.scanOnce.mockResolvedValue({ kind: "cancelled" });
    pairing.open("scan");
    await settle();
    expect(pairing.view).toBe("scan");
    expect(pairing.notice).toBeNull();
    expect(h.scanOnce).toHaveBeenCalledOnce();
  });

  it("explains a code that is not Lola's or is old", async () => {
    h.scanOnce.mockResolvedValue({ kind: "invalid", why: "not_lola" });
    pairing.open("scan");
    await settle();
    expect(pairing.notice).toBe("invalid");
    h.scanOnce.mockResolvedValue({ kind: "invalid", why: "old_format" });
    await pairing.rescan();
    expect(pairing.notice).toBe("oldCode");
  });

  it("separates a denied camera from no camera", async () => {
    h.cap.mockResolvedValue({ available: false, reason: "denied" });
    pairing.open("scan");
    await settle();
    expect(pairing.notice).toBe("cameraOff");
    expect(pairing.view).toBe("scan");

    h.cap.mockResolvedValue({ available: false, reason: "no_camera" });
    await pairing.rescan();
    expect(pairing.view).toBe("no-camera");
    expect(h.scanOnce).not.toHaveBeenCalled();
  });

  it("keeps the scan when pairBegin rejects before the proof was written", async () => {
    h.t.pairBegin.mockRejectedValue({ code: "unreachable" });
    pairing.open("scan");
    await settle();
    expect(pairing.view).toBe("scan");
    expect(pairing.notice).toBe("unreachable");
    expect(h.t.setIdleTimerDisabled).toHaveBeenLastCalledWith({ disabled: false });
  });

  it("shows expired when the hold lapsed", async () => {
    h.t.pairBegin.mockRejectedValue({ code: "expired" });
    pairing.open("scan");
    await settle();
    expect(pairing.view).toBe("expired");
  });

  it.each([
    ["refused", "refused"],
    ["expired", "expired"],
    ["failed", "failed"],
    ["dropped", "dropped"],
  ] as const)("ends in %s on that outcome and releases the screen", async (outcome, view) => {
    pairing.open("scan");
    await settle();
    h.t.listener?.({ pairRef: "r1", outcome });
    expect(pairing.view).toBe(view);
    expect(pairing.code).toBe("");
    expect(h.haptic).toHaveBeenCalledWith("refusal");
    expect(h.t.setIdleTimerDisabled).toHaveBeenLastCalledWith({ disabled: false });
    pairing.close();
    expect(h.t.pairCancel).not.toHaveBeenCalled();
  });

  it("ignores an outcome for another pairing", async () => {
    pairing.open("scan");
    await settle();
    h.t.listener?.({ pairRef: "other", outcome: "refused" });
    expect(pairing.view).toBe("code");
  });

  it("lands on the inbox and connects when paired", async () => {
    pairing.open("scan");
    await settle();
    h.t.listener?.({ pairRef: "r1", outcome: "paired", daemonId: "dX", name: "Mars" });
    await settle();
    expect(h.haptic).toHaveBeenCalledWith("success");
    expect(h.refresh).toHaveBeenCalled();
    expect(h.replace).toHaveBeenCalledWith({ name: "tab", tab: "inbox" });
    expect(h.connect).toHaveBeenCalledWith("dX");
    expect(h.retryLater).not.toHaveBeenCalled();
  });

  it("arms the retry ladder when the first connect fails retryably", async () => {
    h.connect.mockResolvedValueOnce(false);
    h.conn.diagnosis = { retryable: true };
    pairing.open("scan");
    await settle();
    h.t.listener?.({ pairRef: "r1", outcome: "paired", daemonId: "dX" });
    await settle();
    expect(h.retryLater).toHaveBeenCalledOnce();
  });

  it("takes an outcome the plugin ran itself (a development launch)", async () => {
    pairing.open("expired");
    await settle();
    h.t.listener?.({ pairRef: "dev", outcome: "paired", daemonId: "dD" });
    await settle();
    expect(h.connect).toHaveBeenCalledWith("dD");
  });

  it("cancels a waiting pairing when the screen goes", async () => {
    pairing.open("scan");
    await settle();
    pairing.close();
    expect(h.t.pairCancel).toHaveBeenCalledWith({ pairRef: "r1" });
    expect(h.t.remove).toHaveBeenCalled();
    expect(h.t.setIdleTimerDisabled).toHaveBeenLastCalledWith({ disabled: false });
  });

  it("scans again from a terminal state", async () => {
    pairing.open("refused");
    await pairing.rescan();
    expect(pairing.view).toBe("code");
  });

  it("names the device", () => {
    expect(deviceName("Mozilla/5.0 (iPad; CPU OS 18_0)")).toBe("iPad");
    expect(deviceName("Mozilla/5.0 (iPhone; CPU iPhone OS 18_0)")).toBe("iPhone");
  });
});
