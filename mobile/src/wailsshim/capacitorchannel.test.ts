import { beforeEach, describe, expect, it, vi } from "vitest";
import { FRAME_VERSION_CURRENT, WireRefusalError, type Frame } from "../wire";
import { LinkError } from "./pluginerror";

type Handler = (e: never) => void;

const plugin = vi.hoisted(() => {
  const handlers = new Map<string, Set<Handler>>();
  return {
    handlers,
    emit(event: string, payload: unknown) {
      for (const h of [...(handlers.get(event) ?? [])]) (h as (e: unknown) => void)(payload);
    },
    connect: vi.fn(),
    send: vi.fn(async () => {}),
    adopt: vi.fn(async () => {}),
    disconnect: vi.fn(async () => {}),
    addListener: vi.fn(async (event: string, h: Handler) => {
      if (!handlers.has(event)) handlers.set(event, new Set());
      handlers.get(event)!.add(h);
      return { remove: async () => void handlers.get(event)?.delete(h) };
    }),
  };
});

vi.mock("../native", () => ({ transport: plugin }));

import { makeTransport, openPluginChannel } from "./capacitorchannel";

const DEVICE = { deviceId: "dev", name: "Phone", tiers: ["read"], host: "mac" };
const body = (f: Partial<Frame>) => JSON.stringify({ v: FRAME_VERSION_CURRENT, ...f });
const frames = (epoch: number, ...fs: Partial<Frame>[]) => plugin.emit("frames", { epoch, frames: fs.map(body) });
const resp = (id: string) => ({ type: "resp", id, payload: { ok: true, data: {} } });

beforeEach(() => {
  plugin.handlers.clear();
  plugin.connect.mockReset();
  plugin.send.mockClear();
  plugin.adopt.mockClear();
  plugin.disconnect.mockClear();
  plugin.connect.mockResolvedValue({ epoch: 1, path: { kind: "direct" }, device: DEVICE, raceMs: 5 });
});

async function open() {
  const ch = await openPluginChannel({ daemonId: "d1" });
  const got: Frame[] = [];
  const ups: number[] = [];
  const downs: Error[] = [];
  const closes: (Error | undefined)[] = [];
  const migrating: number[] = [];
  ch.onFrame((f) => got.push(f));
  ch.onUp?.((u) => ups.push(u.epoch));
  ch.onDown?.((e) => downs.push(e));
  ch.onClose((e) => closes.push(e));
  ch.onMigrating?.((u) => migrating.push(u.epoch));
  return { ch, got, ups, downs, closes, migrating };
}

describe("connecting", () => {
  it("connects by daemon id only and reports the path and device", async () => {
    const { ch } = await open();
    expect(plugin.connect).toHaveBeenCalledWith({ daemonId: "d1" });
    expect(ch.up).toMatchObject({ epoch: 1, path: { kind: "direct" }, device: { deviceId: "dev" } });
  });

  it("turns a daemon refusal in the rejection into a WireRefusalError", async () => {
    plugin.connect.mockRejectedValue(Object.assign(new Error("x"), { code: "not_paired", data: { daemonCode: "not_paired" } }));
    await expect(openPluginChannel({ daemonId: "d1" })).rejects.toBeInstanceOf(WireRefusalError);
    expect(plugin.handlers.get("frames")?.size).toBe(0);
  });

  it("turns any other rejection into a LinkError carrying the failure code", async () => {
    plugin.connect.mockRejectedValue(Object.assign(new Error("no route"), { code: "unreachable" }));
    await expect(openPluginChannel({ daemonId: "d1" })).rejects.toMatchObject({ name: "LinkError", code: "unreachable" });
  });

  it("fails the connect when the bound epoch died inside the listener window", async () => {
    plugin.connect.mockImplementation(async () => {
      plugin.emit("state", { epoch: 1, phase: "failed", code: "peer_closed" });
      return { epoch: 1, path: { kind: "direct" }, device: DEVICE, raceMs: 1 };
    });
    await expect(openPluginChannel({ daemonId: "d1" })).rejects.toBeInstanceOf(LinkError);
  });

  it("ignores a terminal state of some other epoch seen before it resolved", async () => {
    plugin.connect.mockImplementation(async () => {
      plugin.emit("state", { epoch: 9, phase: "failed", code: "timeout" });
      return { epoch: 1, path: { kind: "direct" }, device: DEVICE, raceMs: 1 };
    });
    await expect(openPluginChannel({ daemonId: "d1" })).resolves.toBeDefined();
  });
});

describe("frames", () => {
  it("delivers decoded frames of the current epoch and drops other epochs", async () => {
    const { got } = await open();
    frames(1, resp("a"));
    frames(0, resp("old"));
    expect(got.map((f) => f.id)).toEqual(["a"]);
  });

  it("holds frames that arrive before the first listener and replays them", async () => {
    const ch = await openPluginChannel({ daemonId: "d1" });
    frames(1, resp("early"));
    const got: Frame[] = [];
    ch.onFrame((f) => got.push(f));
    expect(got.map((f) => f.id)).toEqual(["early"]);
  });

  it("drops an undecodable body and keeps the rest of the batch", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const { got } = await open();
    plugin.emit("frames", { epoch: 1, frames: ["not json", body(resp("ok"))] });
    expect(got.map((f) => f.id)).toEqual(["ok"]);
  });

  it("sends one body per call on the current epoch", async () => {
    const { ch } = await open();
    await ch.send({ v: FRAME_VERSION_CURRENT, type: "req", id: "r1", cmd: "sessions", payload: { cmd: "sessions" } });
    expect(plugin.send).toHaveBeenCalledTimes(1);
    const arg = (plugin.send.mock.calls as unknown as [{ epoch: number; frames: string[] }][])[0][0];
    expect(arg.epoch).toBe(1);
    expect(JSON.parse(arg.frames[0])).toMatchObject({ id: "r1", cmd: "sessions" });
  });
});

describe("epochs", () => {
  it("is down on a retryable closed state and up again on the next ready epoch, replaying its held frames", async () => {
    const { got, downs, ups, closes } = await open();
    plugin.emit("state", { epoch: 1, phase: "closed", code: "backgrounded" });
    expect(downs).toHaveLength(1);
    expect(closes).toEqual([]);
    frames(2, resp("held"));
    expect(got).toEqual([]);
    plugin.emit("device", { epoch: 2, device: { ...DEVICE, tiers: ["read", "answer"] } });
    plugin.emit("state", { epoch: 2, phase: "ready", path: { kind: "relay" } });
    expect(ups).toEqual([2]);
    expect(got.map((f) => f.id)).toEqual(["held"]);
  });

  it("reports the device of the new epoch with the up, and a late device event separately", async () => {
    const ch = await openPluginChannel({ daemonId: "d1" });
    const devices: string[][] = [];
    ch.onDevice?.((d) => devices.push(d.tiers));
    plugin.emit("state", { epoch: 1, phase: "racing" });
    plugin.emit("state", { epoch: 2, phase: "ready" });
    plugin.emit("device", { epoch: 2, device: { ...DEVICE, tiers: ["read", "control"] } });
    expect(devices).toEqual([["read", "control"]]);
  });

  it("treats racing while live as a drop, and a racing after the drop as nothing", async () => {
    const { downs } = await open();
    plugin.emit("state", { epoch: 1, phase: "racing" });
    plugin.emit("state", { epoch: 2, phase: "racing" });
    expect(downs.map((e) => (e as LinkError).code)).toEqual(["peer_closed"]);
  });

  it("names the code of every failed reconnect race while down", async () => {
    const { downs, closes } = await open();
    plugin.emit("state", { epoch: 1, phase: "closed", code: "peer_closed" });
    plugin.emit("state", { epoch: 2, phase: "racing" });
    plugin.emit("state", { epoch: 2, phase: "failed", code: "key_changed" });
    plugin.emit("state", { epoch: 3, phase: "racing" });
    plugin.emit("state", { epoch: 3, phase: "failed", code: "unreachable" });
    expect(downs.map((e) => (e as LinkError).code)).toEqual(["peer_closed", "key_changed", "unreachable"]);
    expect(closes).toEqual([]);
  });

  it("closes for good when a reconnect race after a drop ends terminally", async () => {
    const t = makeTransport();
    await t.connect({ daemonId: "d1" });
    plugin.emit("state", { epoch: 1, phase: "closed", code: "peer_closed" });
    plugin.emit("state", { epoch: 2, phase: "racing" });
    plugin.emit("state", { epoch: 2, phase: "failed", code: "not_paired", daemonCode: "not_paired" });
    expect(t.status.phase).toBe("closed");
    expect(t.status.refusal?.code).toBe("not_paired");
    expect(plugin.handlers.get("state")?.size).toBe(0);
  });

  it("ignores an older epoch's failure once a newer one is current", async () => {
    const { downs, closes } = await open();
    plugin.emit("state", { epoch: 1, phase: "closed", code: "peer_closed" });
    plugin.emit("state", { epoch: 2, phase: "ready" });
    plugin.emit("state", { epoch: 1, phase: "failed", code: "not_paired", daemonCode: "not_paired" });
    expect(downs).toHaveLength(1);
    expect(closes).toEqual([]);
  });

  it("closes for good on a terminal failure and keeps the daemon's refusal", async () => {
    const { closes } = await open();
    plugin.emit("state", { epoch: 1, phase: "failed", code: "not_paired", daemonCode: "not_paired" });
    expect(closes).toHaveLength(1);
    expect(closes[0]).toBeInstanceOf(WireRefusalError);
    expect(plugin.handlers.get("frames")?.size).toBe(0);
  });

  it("ignores the old epoch closing after a migration was adopted", async () => {
    const { ch, closes, downs, ups } = await open();
    plugin.emit("state", { epoch: 2, phase: "migrating", path: { kind: "direct" } });
    await ch.adopt!(2);
    plugin.emit("state", { epoch: 2, phase: "ready", path: { kind: "direct" } });
    expect(ups).toEqual([2]);
    plugin.emit("state", { epoch: 1, phase: "closed", code: "client_closed" });
    expect(closes).toEqual([]);
    expect(downs).toEqual([]);
  });

  it("announces a migration, adopts it through the plugin and moves to the new epoch", async () => {
    const { ch, migrating, ups } = await open();
    plugin.emit("state", { epoch: 2, phase: "migrating", path: { kind: "direct" } });
    expect(migrating).toEqual([2]);
    await ch.adopt!(2);
    expect(plugin.adopt).toHaveBeenCalledWith({ epoch: 2 });
    expect(ups).toEqual([]);
    plugin.emit("state", { epoch: 2, phase: "ready", path: { kind: "direct" } });
    expect(ups).toEqual([2]);
    await ch.send({ v: FRAME_VERSION_CURRENT, type: "req", id: "r", cmd: "status" });
    expect((plugin.send.mock.calls as unknown as [{ epoch: number }][]).at(-1)![0].epoch).toBe(2);
  });

  it("stays on the live epoch when the migrated socket died before it was adopted", async () => {
    const { ch, got, ups, downs, closes } = await open();
    plugin.emit("state", { epoch: 2, phase: "migrating", path: { kind: "direct" } });
    plugin.emit("state", { epoch: 2, phase: "closed", code: "peer_closed" });
    plugin.adopt.mockRejectedValueOnce(Object.assign(new Error("that migration is gone"), { code: "client_closed" }));
    await expect(ch.adopt!(2)).rejects.toThrow("gone");
    expect(ups).toEqual([]);
    expect(downs).toEqual([]);
    expect(closes).toEqual([]);
    frames(1, resp("still"));
    expect(got.map((f) => f.id)).toEqual(["still"]);
    await ch.send({ v: FRAME_VERSION_CURRENT, type: "req", id: "r", cmd: "status" });
    expect((plugin.send.mock.calls as unknown as [{ epoch: number }][]).at(-1)![0].epoch).toBe(1);
    plugin.emit("state", { epoch: 1, phase: "closed", code: "peer_closed" });
    expect(downs).toHaveLength(1);
  });

  it("follows a migration the plugin adopted on its own", async () => {
    const { ups } = await open();
    plugin.emit("state", { epoch: 2, phase: "migrating" });
    plugin.emit("state", { epoch: 2, phase: "ready" });
    expect(ups).toEqual([2]);
  });
});

describe("the transport over the plugin", () => {
  it("carries each ready epoch's path race time into the status", async () => {
    const t = makeTransport();
    await t.connect({ daemonId: "d1" });
    expect(t.status.raceMs).toBe(5);
    plugin.emit("state", { epoch: 1, phase: "closed", code: "peer_closed" });
    plugin.emit("state", { epoch: 2, phase: "ready", raceMs: 80 });
    expect(t.status).toMatchObject({ phase: "ready", epoch: 2, raceMs: 80 });
  });

  it("connects by id, answers a request on the plugin's epoch and is ready", async () => {
    const t = makeTransport();
    await t.connect({ daemonId: "d1" });
    expect(t.status).toMatchObject({ phase: "ready", epoch: 1, path: { kind: "direct" } });
    const p = t.request("sessions");
    await Promise.resolve();
    const sent = JSON.parse((plugin.send.mock.calls as unknown as [{ frames: string[] }][])[0][0].frames[0]);
    frames(1, resp(sent.id));
    await expect(p).resolves.toEqual({});
  });

  it("follows the plugin's own reconnect without a connect() call", async () => {
    const t = makeTransport();
    await t.connect({ daemonId: "d1" });
    plugin.emit("state", { epoch: 1, phase: "closed", code: "peer_closed" });
    expect(t.status.phase).toBe("connecting");
    plugin.emit("state", { epoch: 2, phase: "ready" });
    expect(t.status).toMatchObject({ phase: "ready", epoch: 2 });
  });

  it("disconnect asks the plugin to close", async () => {
    const t = makeTransport();
    await t.connect({ daemonId: "d1" });
    await t.disconnect("bye");
    expect(plugin.disconnect).toHaveBeenCalledWith({ reason: "connection closed: bye" });
    expect(t.status.phase).toBe("closed");
  });
});
