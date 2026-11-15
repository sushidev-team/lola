import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ChannelTransport } from "../wailsshim/channeltransport";
import { FakeLink } from "../wailsshim/fakelink";
import { LinkError } from "../wailsshim/pluginerror";
import { FakeChannel, WireRefusalError } from "../wire";

const plugin = vi.hoisted(() => ({
  daemons: vi.fn(),
  forget: vi.fn(async () => {}),
  rename: vi.fn(async () => {}),
}));
vi.mock("../native", () => ({ transport: plugin }));

import { Connection } from "./connection.svelte";
import { tiers } from "./tiers.svelte";

const mac = (id: string, pairedAt: number, name = id) => ({
  daemonId: id,
  name,
  pairedAt,
  relay: "none" as const,
  secureEnclave: true,
});

beforeEach(() => {
  plugin.daemons.mockReset();
  plugin.forget.mockClear();
  plugin.rename.mockClear();
  plugin.daemons.mockResolvedValue({ daemons: [mac("old", 1), mac("new", 2, "Mars Mac")] });
});
afterEach(() => vi.useRealTimers());

function withTransport(open?: () => Promise<FakeChannel | FakeLink>) {
  const link = new FakeLink();
  const t = new ChannelTransport({ open: open ?? (async () => link) });
  const c = new Connection();
  c.useTransport(t);
  return { c, t, link };
}

describe("the paired daemon", () => {
  it("picks the most recently paired and names it", async () => {
    const { c } = withTransport();
    await c.refresh();
    expect(c.daemonId).toBe("new");
    expect(c.label).toBe("Mars Mac");
  });

  it("keeps a daemon the person already chose", async () => {
    const { c } = withTransport();
    c.daemonId = "old";
    await c.refresh();
    expect(c.daemonId).toBe("old");
  });

  it("survives a plugin that cannot list daemons", async () => {
    plugin.daemons.mockRejectedValue(new Error("no native transport"));
    const { c } = withTransport();
    await expect(c.refresh()).resolves.toEqual([]);
    expect(c.daemonId).toBe("");
    expect(await c.boot()).toBe(false);
  });
});

describe("connecting", () => {
  it("connects by id and mirrors phase, epoch, path and device", async () => {
    const { c, link } = withTransport();
    link.up = { epoch: 4, path: { kind: "relay" }, device: { deviceId: "x", name: "n", tiers: ["read"], host: "h" } };
    expect(await c.boot()).toBe(true);
    expect(c.ready).toBe(true);
    expect(c.epoch).toBe(4);
    expect(c.path).toEqual({ kind: "relay" });
    expect(c.device?.tiers).toEqual(["read"]);
    expect(c.diagnosis.kind).toBe("ok");
  });

  it("keeps the last path race's duration across a reconnect that reports none", async () => {
    const { c, link } = withTransport();
    expect(c.raceMs).toBeNull();
    link.up = { epoch: 1, path: { kind: "direct" }, raceMs: 37 };
    await c.boot();
    expect(c.raceMs).toBe(37);
    link.raiseDown(new LinkError("peer_closed", "peer_closed"));
    link.raiseUp({ epoch: 2 });
    expect(c.raceMs).toBe(37);
    link.raiseUp({ epoch: 3, raceMs: 120 });
    expect(c.raceMs).toBe(120);
  });

  it("sets the tiers store from the device connect() returned", async () => {
    tiers.clear();
    const { c, link } = withTransport();
    link.up = { epoch: 4, device: { deviceId: "x", name: "n", tiers: ["read", "answer"], host: "h" } };
    await c.boot();
    expect(tiers.can("agentSay")).toBe(true);
    expect(tiers.can("mergePR")).toBe(false);
  });

  it("follows the plugin's own reconnect through connecting back to ready", async () => {
    const { c, link } = withTransport();
    await c.boot();
    link.raiseDown(new LinkError("peer_closed", "peer_closed"));
    expect(c.phase).toBe("connecting");
    expect(c.diagnosis.kind).toBe("unreachable");
    link.raiseUp({ epoch: 2 });
    expect(c.ready).toBe(true);
    expect(c.epoch).toBe(2);
  });

  it("reports a refusal the daemon spoke and does not retry it", async () => {
    vi.useFakeTimers();
    const open = vi.fn(async () => {
      throw new WireRefusalError("not_paired", "not paired");
    });
    const { c } = withTransport(open);
    expect(await c.boot()).toBe(false);
    expect(c.diagnosis.kind).toBe("not_paired");
    await vi.advanceTimersByTimeAsync(60_000);
    expect(open).toHaveBeenCalledTimes(1);
  });

  it("walks a short ladder after a retryable first failure, then stops", async () => {
    vi.useFakeTimers();
    const open = vi.fn(async () => {
      throw new LinkError("unreachable", "unreachable");
    });
    const { c } = withTransport(open);
    await c.boot();
    expect(c.diagnosis.kind).toBe("unreachable");
    expect(open).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(2000);
    expect(open).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(5000);
    expect(open).toHaveBeenCalledTimes(3);
    await vi.advanceTimersByTimeAsync(15000);
    expect(open).toHaveBeenCalledTimes(4);
    await vi.advanceTimersByTimeAsync(120_000);
    expect(open).toHaveBeenCalledTimes(4);
  });

  it("never undoes a deliberate disconnect", async () => {
    vi.useFakeTimers();
    const open = vi.fn(async () => {
      throw new LinkError("unreachable", "unreachable");
    });
    const { c } = withTransport(open);
    await c.boot();
    await c.disconnect();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(open).toHaveBeenCalledTimes(1);
    expect(await c.reconnect()).toBe(false);
    expect(open).toHaveBeenCalledTimes(1);
  });

  it("leaves a live link to the plugin on foreground", async () => {
    const open = vi.fn(async () => new FakeLink());
    const { c } = withTransport(open);
    await c.boot();
    expect(await c.reconnect()).toBe(false);
    expect(open).toHaveBeenCalledTimes(1);
  });

  it("waits for a transport that arrives after the call, and says so when none does", async () => {
    vi.useFakeTimers();
    const c = new Connection();
    await c.refresh();
    const pending = c.connect("new");
    const link = new FakeLink();
    c.useTransport(new ChannelTransport({ open: async () => link }));
    expect(await pending).toBe(true);

    const none = new Connection();
    const lost = none.connect("new");
    await vi.advanceTimersByTimeAsync(4000);
    expect(await lost).toBe(false);
    expect(none.error?.message).toMatch(/No transport/);
  });
});

describe("forgetting and renaming", () => {
  it("unpairs through the plugin and refreshes the list", async () => {
    const { c } = withTransport();
    await c.boot();
    tiers.set(["read"]);
    plugin.daemons.mockResolvedValue({ daemons: [mac("old", 1)] });
    await c.forget();
    expect(plugin.forget).toHaveBeenCalledWith({ daemonId: "new" });
    expect(tiers.held).toEqual([]);
    expect(c.daemonId).toBe("old");
    expect(c.phase).toBe("closed");
  });

  it("renames on this phone only", async () => {
    const { c } = withTransport();
    await c.refresh();
    await c.rename("Desk");
    expect(plugin.rename).toHaveBeenCalledWith({ daemonId: "new", name: "Desk" });
  });
});

describe("requests", () => {
  it("rejects with 'not connected' when no transport is installed", async () => {
    const c = new Connection();
    await expect(c.request("sessions")).rejects.toThrow("not connected");
    await expect(c.subscribe("lola-x")).rejects.toThrow("not connected");
  });
});
