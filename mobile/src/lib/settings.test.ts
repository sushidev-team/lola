import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@mobile/native", () => ({
  transport: {
    status: vi.fn(),
    rename: vi.fn(async () => {}),
    forget: vi.fn(async () => {}),
    daemons: vi.fn(async () => ({ daemons: [] })),
    addListener: async () => ({ remove: async () => {} }),
  },
}));

import { confirm } from "$lib/confirm.svelte";
import { transport } from "@mobile/native";
import { connection } from "./connection.svelte";
import {
  bootMs,
  confirmUnpair,
  detailRows,
  diagnosticRows,
  heroGlyph,
  heroLine,
  pathLabel,
  pathWord,
  renameMac,
  viaLabel,
} from "./settings.svelte";

beforeEach(() => {
  confirm.cancel();
  vi.clearAllMocks();
  connection.daemonId = "d1";
  connection.daemons = [{ daemonId: "d1", name: "Studio", pairedAt: 1, relay: "none", secureEnclave: false }];
});

describe("pathLabel", () => {
  it("names direct paths by interface, relay and offline plainly", () => {
    expect(pathLabel(true, { kind: "direct", interface: "wifi" })).toBe("Direct · Wi-Fi");
    expect(pathLabel(true, { kind: "direct" })).toBe("Direct");
    expect(pathLabel(true, { kind: "relay" })).toBe("Relay");
    expect(pathLabel(false, { kind: "direct" })).toBe("Offline");
    expect(pathLabel(true, null)).toBe("Offline");
  });
});

describe("detailRows", () => {
  it("lists path, address, epoch, relay state and counters", () => {
    const rows = detailRows(
      { epoch: 3, phase: "ready", relay: "backoff", framesIn: 1, framesOut: 2, bytesIn: 3, bytesOut: 4, launchedAt: 0 },
      3,
      { kind: "direct", host: "10.0.0.2", port: 7717, interface: "wifi" },
      42,
    );
    const by = Object.fromEntries(rows.map((r) => [r.label, r.value]));
    expect(by).toMatchObject({
      Path: "Direct · Wi-Fi",
      Address: "10.0.0.2:7717",
      Epoch: "3",
      "Last race": "42 ms",
      Relay: "Retrying",
      "Frames out": "2",
    });
  });

  it("still answers when the plugin cannot", () => {
    expect(detailRows(null, 0, null).map((r) => r.label)).toEqual(["Path", "Epoch"]);
  });
});

describe("bootMs", () => {
  it("is null until the Inbox mark and the launch time exist", () => {
    expect(bootMs(undefined)).toBeNull();
    expect(bootMs(Date.now())).toBeNull();
  });

  it("measures launch to the inbox-interactive mark", () => {
    const launched = performance.timeOrigin - 400;
    performance.mark("inbox-interactive", { startTime: 600 });
    const ms = bootMs(launched, performance.timeOrigin + 5000);
    performance.clearMarks("inbox-interactive");
    expect(ms).toBe(1000);
  });
});

describe("renameMac", () => {
  it("renames on this phone only, and refuses an empty or unchanged name", async () => {
    expect(await renameMac("   ")).toBe(false);
    expect(await renameMac("Studio")).toBe(false);
    expect(await renameMac("Work\u0000 Mac")).toBe(true);
    expect(transport.rename).toHaveBeenCalledWith({ daemonId: "d1", name: "Work Mac" });
  });
});

describe("confirmUnpair", () => {
  it("asks first and forgets only on accept", async () => {
    confirmUnpair();
    expect(confirm.request?.title).toBe("Unpair phone?");
    expect(transport.forget).not.toHaveBeenCalled();
    confirm.accept();
    await vi.waitFor(() => expect(transport.forget).toHaveBeenCalledWith({ daemonId: "d1" }));
  });
});

describe("connection sheet words", () => {
  it("never calls a relay path anything but Relay, with the race when known", () => {
    expect(pathWord(true, { kind: "relay" })).toBe("Relay");
    expect(heroLine(true, { kind: "relay" }, 180)).toBe("Relay · 180 ms");
    expect(heroLine(true, { kind: "relay" }, null)).toBe("Relay");
    expect(heroLine(false, { kind: "relay" }, 180)).toBe("Offline");
    expect(heroGlyph(true, { kind: "relay" })).toBe("globe");
    expect(heroGlyph(true, { kind: "direct" })).toBe("smartphone");
    expect(viaLabel(true, { kind: "relay" })).toBe("Relay");
    expect(viaLabel(true, { kind: "direct", interface: "wifi" })).toBe("Wi-Fi");
    expect(viaLabel(false, null)).toBe("—");
  });

  it("folds traffic into one row and omits the relay state", () => {
    const rows = diagnosticRows(
      { epoch: 1, phase: "ready", relay: "none", framesIn: 2, framesOut: 3, bytesIn: 2048, bytesOut: 5, launchedAt: 0 },
      7,
      null,
    );
    expect(rows.map((r) => r.label)).toEqual(["Epoch", "Traffic"]);
    expect(rows[1].value).toBe("2.0 KB, 2 frames in · 5 B, 3 frames out");
  });
});
