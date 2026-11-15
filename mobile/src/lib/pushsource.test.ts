import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mobilePush } from "./pushsource";
import { ChannelTransport } from "../wailsshim/channeltransport";
import { UNWATCH_DELAY_MS } from "../wailsshim/watch";
import { bridge } from "../wailsshim/bridge";
import { FRAME_RESP, FakeChannel, type Frame } from "../wire";

let ch: FakeChannel;
const stops: (() => void)[] = [];
const ids = new Map<string, string>();
const results = new Map<string, { ok: boolean; code?: string }>();
/** sessionTimeline replies keyed by `after`. */
const pages = new Map<number, unknown>();

beforeEach(async () => {
  vi.useFakeTimers();
  ids.clear();
  results.clear();
  pages.clear();
  ch = new FakeChannel();
  ch.onSend = (f: Frame) => {
    if (f.cmd === "sessionTimeline") {
      const after = (f.payload as { args: { after?: number } }).args.after ?? 0;
      ch.deliver({ v: 2, type: FRAME_RESP, id: f.id, payload: { ok: true, data: pages.get(after) } });
      return;
    }
    if (f.cmd !== "watch") return;
    const topics = (f.payload as { args: { topics: string[] } }).args.topics;
    for (const t of topics) ids.set(t, f.id!);
    ch.deliver({
      v: 2,
      type: FRAME_RESP,
      id: f.id,
      payload: { ok: true, data: { topics: topics.map((topic) => ({ topic, ...(results.get(topic) ?? { ok: true }) })) } },
    });
  };
  const t = new ChannelTransport({ open: async () => ch });
  await t.connect({ daemonId: "d" });
  bridge.installTransport(t);
});

afterEach(() => {
  for (const stop of stops.splice(0)) stop();
  bridge.installTransport(null);
  vi.useRealTimers();
});

const tick = () => vi.advanceTimersByTimeAsync(0);

describe("talk", () => {
  it("watches talk:<id> and passes the state through", async () => {
    const got: unknown[] = [];
    stops.push(mobilePush.talk("s1", (d) => got.push(d), () => {}));
    await tick();
    ch.deliverEvent(ids.get("talk:s1")!, "talk:s1", 1, { session: "s1", feedSeq: 4 });
    expect(got).toEqual([{ session: "s1", feedSeq: 4 }]);
  });
});

describe("feed", () => {
  it("maps a FeedEvent to a FeedPush", async () => {
    const got: unknown[] = [];
    stops.push(mobilePush.feed("s1", (p) => got.push(p), () => {}));
    await tick();
    const id = ids.get("feed:s1")!;
    ch.deliverEvent(id, "feed:s1", 1, { reset: true, older: true, session: "s1", epoch: "e1", lastSeq: 9, health: "ok", items: [{ seq: 9 }] });
    ch.deliverEvent(id, "feed:s1", 2, { session: "s1", epoch: "e1", lastSeq: 10, scanning: true, health: "scanning", items: [] });
    expect(got).toEqual([
      { reset: true, older: true, epoch: "e1", lastSeq: 9, items: [{ seq: 9 }], scanning: false, health: "ok" },
      { reset: false, older: false, epoch: "e1", lastSeq: 10, items: [], scanning: true, health: "scanning" },
    ]);
  });
});

describe("timeline", () => {
  it("passes an event that omits connections as unchanged (null)", async () => {
    const got: { d: { connections: unknown }; reset: boolean }[] = [];
    stops.push(mobilePush.timeline("s1", (d, reset) => got.push({ d: d as never, reset }), () => {}));
    await tick();
    const id = ids.get("timeline:s1")!;
    ch.deliverEvent(id, "timeline:s1", 1, { reset: true, session: "s1", lastSeq: 3, items: [{ id: "a" }], connections: [{ id: "c1" }] });
    ch.deliverEvent(id, "timeline:s1", 2, { session: "s1", lastSeq: 4, items: [{ id: "b" }] });
    expect(got.map((g) => g.reset)).toEqual([true, false]);
    expect(got[0].d.connections).toEqual([{ id: "c1" }]);
    expect(got[1].d).toEqual({ session: "s1", items: [{ id: "b" }], lastSeq: 4, connections: null });
  });

  it("delivers a split reset as one whole snapshot", async () => {
    const got: { d: { items: unknown[] }; reset: boolean }[] = [];
    stops.push(mobilePush.timeline("s1", (d, reset) => got.push({ d: d as never, reset }), () => {}));
    await tick();
    const id = ids.get("timeline:s1")!;
    ch.deliverEvent(id, "timeline:s1", 1, { reset: true, more: true, session: "s1", lastSeq: 1, items: [{ id: "a" }], connections: [] });
    ch.deliverEvent(id, "timeline:s1", 2, { more: false, session: "s1", lastSeq: 2, items: [{ id: "b" }] });
    expect(got).toHaveLength(1);
    expect(got[0].reset).toBe(true);
    expect(got[0].d.items).toHaveLength(2);
  });
});

describe("timeline older history", () => {
  it("pages sessionTimeline from 0 up to the reset's lastSeq, as deltas", async () => {
    pages.set(0, { session: "s1", items: [{ id: "o1" }], lastSeq: 2, connections: [], more: true });
    pages.set(2, { session: "s1", items: [{ id: "o2" }], lastSeq: 4, connections: [], more: false });
    const got: { d: { items: { id: string }[]; connections: unknown }; reset: boolean }[] = [];
    stops.push(mobilePush.timeline("s1", (d, reset) => got.push({ d: d as never, reset }), () => {}));
    await tick();
    ch.deliverEvent(ids.get("timeline:s1")!, "timeline:s1", 1, { reset: true, older: true, session: "s1", lastSeq: 9, items: [{ id: "n" }], connections: [{ id: "c1" }] });
    await tick();
    await tick();
    expect(got.map((g) => [g.reset, g.d.items.map((i) => i.id)])).toEqual([
      [true, ["n"]],
      [false, ["o1"]],
      [false, ["o2"]],
    ]);
    expect(got[1].d.connections).toBeNull();
    expect(ch.sent.filter((f) => f.cmd === "sessionTimeline")).toHaveLength(2);
  });

  it("stops paging once unsubscribed", async () => {
    pages.set(0, { session: "s1", items: [{ id: "o1" }], lastSeq: 2, connections: [], more: true });
    const got: unknown[] = [];
    const stop = mobilePush.timeline("s1", (d) => got.push(d), () => {});
    await tick();
    ch.deliverEvent(ids.get("timeline:s1")!, "timeline:s1", 1, { reset: true, older: true, session: "s1", lastSeq: 9, items: [], connections: [] });
    stop();
    await tick();
    expect(got).toHaveLength(1);
  });
});

describe("diff", () => {
  it("passes the revision counter", async () => {
    const revs: number[] = [];
    stops.push(mobilePush.diff("s1", (r) => revs.push(r), () => {}));
    await tick();
    ch.deliverEvent(ids.get("diff:s1")!, "diff:s1", 1, { rev: 7 });
    expect(revs).toEqual([7]);
  });
});

describe("refusals and teardown", () => {
  it("reports a refused topic to the store so it can poll", async () => {
    results.set("diff:s1", { ok: false, code: "unknown_session" });
    const errs: string[] = [];
    stops.push(mobilePush.diff("s1", () => {}, (e) => errs.push(e)));
    await tick();
    expect(errs).toEqual(["unknown_session"]);
  });

  it("stops watching once the store lets go", async () => {
    const stop = mobilePush.talk("s1", () => {}, () => {});
    await tick();
    stop();
    await vi.advanceTimersByTimeAsync(UNWATCH_DELAY_MS + 1);
    expect(ch.sent.filter((f) => f.cmd === "unwatch")).toHaveLength(1);
  });
});
