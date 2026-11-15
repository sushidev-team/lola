import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ChannelTransport } from "./channeltransport";
import { UNWATCH_DELAY_MS, WatchRegistry, type Sub, type Topic, type WatchCode } from "./watch";
import {
  FRAME_ERR,
  FRAME_RESP,
  FRAME_VERSION_CURRENT,
  FakeChannel,
  MAX_WATCH_TOPICS_PER_REQUEST,
  WATCH_RETRY_AFTER_MS,
  type Frame,
} from "../wire";

type Daemon = { results: Record<string, { ok: boolean; code?: string }>; refuse?: string };

/** A daemon that answers `watch`/`unwatch` per topic from `results` (default ok). */
async function setup(daemon: Daemon = { results: {} }) {
  const ch = new FakeChannel();
  const ids = new Map<string, string>();
  ch.onSend = (f: Frame) => {
    if (f.cmd === "unwatch") {
      ch.deliver({ v: FRAME_VERSION_CURRENT, type: FRAME_RESP, id: f.id, payload: { ok: true, data: { topics: [] } } });
      return;
    }
    if (f.cmd !== "watch") return;
    const topics = (f.payload as { args: { topics: string[] } }).args.topics;
    for (const topic of topics) ids.set(topic, f.id!);
    const payload = daemon.refuse
      ? { ok: false, error: daemon.refuse }
      : { ok: true, data: { topics: topics.map((topic) => ({ topic, ...(daemon.results[topic] ?? { ok: true }) })) } };
    ch.deliver({ v: FRAME_VERSION_CURRENT, type: FRAME_RESP, id: f.id, payload });
  };
  const t = new ChannelTransport({ open: async () => ch });
  await t.connect({ daemonId: "d" });
  const reg = new WatchRegistry();
  reg.bind(t);
  return { ch, t, reg, ids };
}

function sub(): Sub & { events: { payload: unknown; reset: boolean }[]; errors: WatchCode[] } {
  const events: { payload: unknown; reset: boolean }[] = [];
  const errors: WatchCode[] = [];
  return { events, errors, on: (payload, reset) => events.push({ payload, reset }), onError: (c) => errors.push(c) };
}

const sent = (ch: FakeChannel, cmd: string) =>
  ch.sent.filter((f) => f.cmd === cmd).map((f) => (f.payload as { args: { topics?: string[] } }).args.topics);

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

const tick = () => vi.advanceTimersByTimeAsync(0);

describe("starting and stopping a watch", () => {
  it("watches on the first subscriber and shares the stream with the second", async () => {
    const { ch, reg, ids } = await setup();
    const a = sub();
    const b = sub();
    reg.watch("sessions", a);
    await tick();
    reg.watch("sessions", b);
    await tick();
    expect(sent(ch, "watch")).toEqual([["sessions"]]);

    ch.deliverEvent(ids.get("sessions")!, "sessions", 1, { reset: true, upsert: [] });
    expect(a.events).toHaveLength(1);
    expect(b.events).toHaveLength(1);
    expect(a.events[0].reset).toBe(true);
  });

  it("sends every topic queued in one macrotask as one request", async () => {
    const { ch, reg } = await setup();
    reg.watch("sessions", sub());
    reg.watch("projects", sub());
    reg.watch("talk:s1" as Topic, sub());
    await tick();
    expect(sent(ch, "watch")).toEqual([["sessions", "projects", "talk:s1"]]);
  });

  it("splits more than the per-request cap into several requests", async () => {
    const { ch, reg } = await setup();
    for (let i = 0; i < MAX_WATCH_TOPICS_PER_REQUEST + 1; i++) reg.watch(`talk:s${i}` as Topic, sub());
    await tick();
    const reqs = sent(ch, "watch");
    expect(reqs).toHaveLength(2);
    expect(reqs[0]).toHaveLength(MAX_WATCH_TOPICS_PER_REQUEST);
    expect(reqs[1]).toHaveLength(1);
  });

  it("unwatches only after the last subscriber has been gone UNWATCH_DELAY_MS", async () => {
    const { ch, reg } = await setup();
    const off = reg.watch("feed:s1" as Topic, sub());
    await tick();
    off();
    await vi.advanceTimersByTimeAsync(UNWATCH_DELAY_MS - 1);
    expect(sent(ch, "unwatch")).toEqual([]);
    await vi.advanceTimersByTimeAsync(2);
    expect(sent(ch, "unwatch")).toEqual([["feed:s1"]]);
  });

  it("does not churn when a subscriber returns inside the delay", async () => {
    const { ch, reg } = await setup();
    const off = reg.watch("feed:s1" as Topic, sub());
    await tick();
    off();
    await vi.advanceTimersByTimeAsync(500);
    reg.watch("feed:s1" as Topic, sub());
    await vi.advanceTimersByTimeAsync(UNWATCH_DELAY_MS * 2);
    expect(sent(ch, "unwatch")).toEqual([]);
    expect(sent(ch, "watch")).toEqual([["feed:s1"]]);
  });
});

describe("joining a live stream", () => {
  it("re-watches for a fresh reset when a subscriber returns after deltas went to nobody", async () => {
    const { ch, reg, ids } = await setup();
    const first = sub();
    const off = reg.watch("feed:s1" as Topic, first);
    await tick();
    ch.deliverEvent(ids.get("feed:s1")!, "feed:s1", 1, { reset: true, items: ["a"] });
    off();
    ch.deliverEvent(ids.get("feed:s1")!, "feed:s1", 2, { items: ["missed"] });
    await vi.advanceTimersByTimeAsync(500);

    const back = sub();
    reg.watch("feed:s1" as Topic, back);
    await tick();
    expect(sent(ch, "watch")).toEqual([["feed:s1"], ["feed:s1"]]);
    expect(sent(ch, "unwatch")).toEqual([]);

    // A delta of the replaced stream is dropped; the new stream's reset arrives.
    ch.deliverEvent(ch.sent.filter((f) => f.cmd === "watch")[0].id!, "feed:s1", 3, { items: ["stale"] });
    ch.deliverEvent(ids.get("feed:s1")!, "feed:s1", 1, { reset: true, items: ["a", "missed"] });
    expect(back.events).toEqual([{ payload: { reset: true, items: ["a", "missed"] }, reset: true }]);
  });

  it("gives a second consumer of a live stream its own reset", async () => {
    const { ch, reg, ids } = await setup();
    const a = sub();
    reg.watch("talk:s1" as Topic, a);
    await tick();
    ch.deliverEvent(ids.get("talk:s1")!, "talk:s1", 1, { state: "idle" });
    const b = sub();
    reg.watch("talk:s1" as Topic, b);
    await tick();
    expect(sent(ch, "watch")).toHaveLength(2);
    ch.deliverEvent(ids.get("talk:s1")!, "talk:s1", 1, { state: "working" });
    expect(b.events.map((e) => e.payload)).toEqual([{ state: "working" }]);
    expect(a.events.map((e) => e.payload)).toEqual([{ state: "idle" }, { state: "working" }]);
  });
});

describe("the stream", () => {
  it("watches again when an event arrives out of sequence", async () => {
    const { ch, reg, ids } = await setup();
    reg.watch("sessions", sub());
    await tick();
    ch.deliverEvent(ids.get("sessions")!, "sessions", 1, { reset: true });
    ch.deliverEvent(ids.get("sessions")!, "sessions", 3, { upsert: [] });
    await tick();
    expect(sent(ch, "watch")).toHaveLength(2);
  });

  it("delivers a split reset once, whole", async () => {
    const { ch, reg, ids } = await setup();
    const s = sub();
    reg.watch("sessions", s);
    await tick();
    const id = ids.get("sessions")!;
    ch.deliverEvent(id, "sessions", 1, { reset: true, more: true, upsert: [{ id: "a" }] });
    expect(s.events).toHaveLength(0);
    ch.deliverEvent(id, "sessions", 2, { more: false, upsert: [{ id: "b" }] });
    expect(s.events).toHaveLength(1);
    expect((s.events[0].payload as { upsert: unknown[] }).upsert).toHaveLength(2);
  });

  it("isolates a throwing subscriber", async () => {
    const { ch, reg, ids } = await setup();
    vi.spyOn(console, "error").mockImplementation(() => {});
    const good = sub();
    reg.watch("status", { on: () => { throw new Error("boom"); }, onError: () => {} });
    reg.watch("status", good);
    await tick();
    ch.deliverEvent(ids.get("status")!, "status", 1, {});
    expect(good.events).toHaveLength(1);
  });
});

describe("a topic that fails", () => {
  it("tells every subscriber once, and a late one at once", async () => {
    const { reg } = await setup({ results: { "diff:s1": { ok: false, code: "unknown_session" } } });
    const a = sub();
    reg.watch("diff:s1" as Topic, a);
    await tick();
    expect(a.errors).toEqual(["unknown_session"]);
    const late = sub();
    reg.watch("diff:s1" as Topic, late);
    expect(late.errors).toEqual(["unknown_session"]);
    expect(a.errors).toHaveLength(1);
  });

  it("never retries unknown_session on a timer", async () => {
    const { ch, reg } = await setup({ results: { "diff:s1": { ok: false, code: "unknown_session" } } });
    reg.watch("diff:s1" as Topic, sub());
    await vi.advanceTimersByTimeAsync(WATCH_RETRY_AFTER_MS * 3);
    expect(sent(ch, "watch")).toHaveLength(1);
  });

  it("treats a refused request as bad_request for every topic in it", async () => {
    const { reg } = await setup({ results: {}, refuse: "nope" });
    const a = sub();
    const b = sub();
    reg.watch("sessions", a);
    reg.watch("projects", b);
    await tick();
    expect(a.errors).toEqual(["bad_request"]);
    expect(b.errors).toEqual(["bad_request"]);
  });

  it("retries a rate-limited stream after WATCH_RETRY_AFTER_MS", async () => {
    const { ch, reg, ids } = await setup();
    const s = sub();
    reg.watch("feed:s1" as Topic, s);
    await tick();
    ch.deliver({
      v: FRAME_VERSION_CURRENT,
      type: FRAME_ERR,
      id: ids.get("feed:s1"),
      topic: "feed:s1",
      payload: { code: "rate_limited" },
    });
    expect(s.errors).toEqual(["rate_limited"]);
    await vi.advanceTimersByTimeAsync(WATCH_RETRY_AFTER_MS - 1);
    expect(sent(ch, "watch")).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(2);
    expect(sent(ch, "watch")).toHaveLength(2);
  });

  it("retries a limit refusal once this client freed a stream", async () => {
    const results: Daemon["results"] = { "talk:s2": { ok: false, code: "limit" } };
    const { ch, reg } = await setup({ results });
    const offFirst = reg.watch("talk:s1" as Topic, sub());
    const s2 = sub();
    reg.watch("talk:s2" as Topic, s2);
    await tick();
    expect(s2.errors).toEqual(["limit"]);
    delete results["talk:s2"];
    offFirst();
    await vi.advanceTimersByTimeAsync(UNWATCH_DELAY_MS + 1);
    expect(sent(ch, "watch").at(-1)).toEqual(["talk:s2"]);
  });
});

describe("epochs", () => {
  it("returns to idle on reset and watches every subscribed topic in one request on resume", async () => {
    const { ch, reg } = await setup();
    reg.watch("sessions", sub());
    reg.watch("projects", sub());
    const gone = reg.watch("status", sub());
    await tick();
    gone();
    reg.reset();
    await vi.advanceTimersByTimeAsync(UNWATCH_DELAY_MS * 2);
    reg.resume();
    await tick();
    expect(sent(ch, "watch")).toEqual([["sessions", "projects", "status"], ["sessions", "projects"]]);
  });

  it("retries a failed topic on the next epoch", async () => {
    const results: Daemon["results"] = { "diff:s1": { ok: false, code: "denied" } };
    const { ch, reg } = await setup({ results });
    reg.watch("diff:s1" as Topic, sub());
    await tick();
    delete results["diff:s1"];
    reg.resume();
    await tick();
    expect(sent(ch, "watch")).toHaveLength(2);
  });

  it("sends nothing while the connection is down and watches on resume", async () => {
    const { ch, t, reg } = await setup();
    await t.disconnect("test");
    reg.watch("sessions", sub());
    await tick();
    expect(sent(ch, "watch")).toEqual([]);
  });
});
