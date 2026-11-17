import { beforeEach, describe, expect, it, vi } from "vitest";

const { Status, nativeStatus } = vi.hoisted(() => ({ Status: vi.fn(), nativeStatus: vi.fn() }));

vi.mock("@mobile/wailsshim", () => ({ HubService: { Status } }));
vi.mock("@mobile/native", () => ({
  transport: {
    status: nativeStatus,
    daemons: vi.fn(async () => ({ daemons: [] })),
    addListener: async () => ({ remove: async () => {} }),
  },
}));

import type { RelayState } from "lola-transport";
import { connection } from "./connection.svelte";
import { copy } from "./copy";
import { phoneRelay, remoteAccess, remoteAccessKey, remoteAccessRow, type RemoteAccessKey } from "./remoteaccess.svelte";
import { tiers } from "./tiers.svelte";

const mac = (state: string, reason?: string) => ({ state, reason, activeConns: 0, rendezvous: 0 });
const plugin = (phase: string, relay: RelayState) =>
  ({ epoch: 1, phase, relay, framesIn: 0, framesOut: 0, bytesIn: 0, bytesOut: 0, launchedAt: 0 }) as never;

describe("remoteAccessKey", () => {
  it("lets the Mac's answer explain first", () => {
    const cases: [ReturnType<typeof mac>, RemoteAccessKey][] = [
      [mac("connected"), "available"],
      [mac("connecting"), "connecting"],
      [mac("syncing"), "connecting"],
      [mac("backoff", "error"), "retrying"],
      [mac("unsupported", "no_relay"), "hubUpdate"],
      [mac("unsupported", "version"), "hubUpdate"],
      [mac("refused", "revoked"), "refused"],
      [mac("off", "not_joined"), "notJoined"],
      [mac("off", "hub_off"), "hubOff"],
      [mac("off", "config_error"), "configError"],
      [mac("off", "revoked"), "refused"],
      [mac("off", "disabled"), "turnedOff"],
      [mac("off", "no_phones"), "pairAgain"],
      [mac("off", "brand_new_reason"), "turnedOff"],
      [mac("off", "stopped"), "turnedOff"],
    ];
    for (const [m, want] of cases) expect(remoteAccessKey("ok", m), JSON.stringify(m)).toBe(want);
  });

  it("does not say to pair again while the Mac issues this phone its credential", () => {
    expect(remoteAccessKey("none", mac("connected"))).toBe("settingUp");
    expect(remoteAccessKey("none", mac("off", "no_phones"))).toBe("settingUp");
    expect(remoteAccessKey(undefined, mac("off", "no_phones"))).toBe("settingUp");
    expect(remoteAccessRow("settingUp")).toEqual({ value: "Setting up", detail: "Your Mac is setting this up." });
    expect(remoteAccessRow("hubOff").detail).toBe("Team hub is off on your Mac.");
    expect(remoteAccessRow("configError").detail).toBe("Your Mac's settings file has an error.");
    // The Mac vouching for an upgraded hub outranks this phone's old pause.
    expect(remoteAccessKey("hub_too_old", mac("connected"))).toBe("available");
  });

  it("falls back to this phone's own state when the Mac did not answer", () => {
    const cases: [RelayState | undefined, RemoteAccessKey][] = [
      ["ok", "ready"],
      ["backoff", "retrying"],
      ["hub_too_old", "hubUpdate"],
      ["credential_dead", "expired"],
      ["none", "notSetUp"],
      [undefined, "notSetUp"],
    ];
    for (const [p, want] of cases) {
      expect(remoteAccessKey(p, undefined)).toBe(want);
      expect(remoteAccessKey(p, null)).toBe(want);
    }
  });

  it("gives every key a short value and one sentence", () => {
    for (const key of Object.keys(copy.remoteAccess).filter((k) => k !== "label") as RemoteAccessKey[]) {
      const row = remoteAccessRow(key);
      expect(row.value.length, key).toBeGreaterThan(0);
      expect(row.detail, key).toMatch(/^[A-Z].*\.$/);
    }
    expect(remoteAccessRow("notJoined").detail).toBe("Your Mac is not in a team hub.");
    expect(remoteAccessRow("hubUpdate").detail).toBe("Team hub needs an update.");
    expect(remoteAccessRow("turnedOff").detail).toBe("Turned off on your Mac.");
    expect(remoteAccessRow("pairAgain").detail).toBe("Pair again to set it up.");
    expect(remoteAccessRow("available").value).toBe("Available");
  });
});

describe("phoneRelay", () => {
  it("prefers the live session's state, else the pairing record's", () => {
    expect(phoneRelay(plugin("ready", "ok"), "none")).toBe("ok");
    expect(phoneRelay(plugin("closed", "none"), "hub_too_old")).toBe("hub_too_old");
    expect(phoneRelay(null, "ok")).toBe("ok");
  });
});

describe("remoteAccess.load", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    connection.daemonId = "d1";
    connection.daemons = [{ daemonId: "d1", name: "Studio", pairedAt: 1, relay: "hub_too_old", secureEnclave: false }];
    nativeStatus.mockResolvedValue(plugin("closed", "none"));
  });

  it("reads only this phone's state while not connected", async () => {
    connection.phase = "closed";
    await remoteAccess.load();
    expect(Status).not.toHaveBeenCalled();
    expect(remoteAccess.row.detail).toBe("Team hub needs an update.");
  });

  it("asks the Mac while connected with read", async () => {
    connection.phase = "ready";
    tiers.set(["read"]);
    nativeStatus.mockResolvedValue(plugin("ready", "ok"));
    Status.mockResolvedValue({ enabled: false, joined: false, relay: mac("off", "not_joined") });
    await remoteAccess.load();
    expect(Status).toHaveBeenCalledOnce();
    expect(remoteAccess.row).toEqual({ value: "Off", detail: "Your Mac is not in a team hub." });
  });

  it("ignores the Mac's last answer once the connection is no longer ready", async () => {
    connection.phase = "ready";
    tiers.set(["read"]);
    nativeStatus.mockResolvedValue(plugin("ready", "ok"));
    Status.mockResolvedValue({ enabled: true, joined: true, relay: mac("connected") });
    await remoteAccess.load();
    expect(remoteAccess.key).toBe("available");
    connection.phase = "closed";
    expect(remoteAccess.key).toBe("ready");
    expect(remoteAccess.row.value).not.toBe("Available");
  });

  it("treats a refused hubStatus as unknown, not as off", async () => {
    connection.phase = "ready";
    tiers.set(["read"]);
    nativeStatus.mockResolvedValue(plugin("ready", "ok"));
    Status.mockRejectedValue(new Error("unknown cmd"));
    await remoteAccess.load();
    expect(remoteAccess.key).toBe("ready");
  });

  it("never asks without the read tier", async () => {
    connection.phase = "ready";
    tiers.set([]);
    await remoteAccess.load();
    expect(Status).not.toHaveBeenCalled();
  });
});
