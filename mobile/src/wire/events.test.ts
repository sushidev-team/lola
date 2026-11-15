import { describe, expect, it } from "vitest";

import {
  IDLE,
  isWatchId,
  onDisconnect,
  onEvent,
  onStreamErr,
  onWatchResult,
  parseTopic,
  retryOf,
  startWatch,
  toWatchCode,
  type ErrFrame,
  type EventFrame,
  type Topic,
  type TopicState,
} from "./events";
import eventVectors from "./testdata/events.json";

const ev = (id: string, topic: string, seq: number, payload: unknown): EventFrame => ({
  v: 2,
  type: "event",
  id,
  topic,
  seq,
  payload,
});
const stErr = (id: string, code: string, topic = "talk:a"): ErrFrame => ({
  v: 2,
  type: "err",
  id,
  topic,
  payload: { code },
});

/** Runs frames through the machine, returning the final state and every action. */
function run(topic: Topic, st: TopicState, frames: EventFrame[]) {
  const actions: unknown[] = [];
  for (const f of frames) {
    const [next, a] = onEvent(st, topic, f);
    st = next;
    actions.push(...a);
  }
  return { st, actions };
}

describe("parseTopic", () => {
  it.each([
    ["sessions", "sessions", ""],
    ["projects", "projects", ""],
    ["status", "status", ""],
    ["talk:lola-fe-42", "talk", "lola-fe-42"],
    ["feed:lola-fe-42", "feed", "lola-fe-42"],
    ["timeline:x", "timeline", "x"],
    ["diff:x.y", "diff", "x.y"],
  ])("accepts %s", (t, kind, session) => {
    expect(parseTopic(t)).toEqual({ kind, session });
  });

  it.each([
    [""],
    ["Sessions"],
    ["sessions:x"],
    ["talk"],
    ["talk:"],
    ["goal:x"],
    ["talk:.hidden"],
    ["talk:a/b"],
    ["talk:a\\b"],
    ["talk:lola-1~app"],
    ["talk:a\u0001b"],
    ["talk:" + "a".repeat(129)],
    [" talk:x"],
  ])("rejects %j", (t) => {
    expect(parseTopic(t)).toBeNull();
  });

  it("takes a 128-byte session and refuses a 129-byte one counted in utf-8", () => {
    expect(parseTopic("talk:" + "a".repeat(128))).not.toBeNull();
    expect(parseTopic("talk:" + "é".repeat(65))).toBeNull();
  });
});

describe("isWatchId", () => {
  it("matches ^[A-Za-z0-9_-]{1,64}$", () => {
    for (const ok of ["w7", "a", "A_b-9", "x".repeat(64)]) expect(isWatchId(ok)).toBe(true);
    for (const bad of ["", "x".repeat(65), "a b", "a:b", "é"]) expect(isWatchId(bad)).toBe(false);
  });
});

describe("the happy path", () => {
  it("goes awaiting, live on seq 1, and applies seq+1", () => {
    const t: Topic = "talk:a";
    let st = startWatch("w1");
    const r = run(t, st, [ev("w1", t, 1, { n: 1 }), ev("w1", t, 2, { n: 2 })]);
    expect(r.st).toEqual({ s: "live", watchId: "w1", seq: 2, pending: undefined });
    expect(r.actions).toEqual([
      { a: "deliver", payload: { n: 1 }, reset: false },
      { a: "deliver", payload: { n: 2 }, reset: false },
    ]);
  });

  it("requires reset on the first event of a delta topic", () => {
    const [st, a] = onEvent(startWatch("w1"), "sessions", ev("w1", "sessions", 1, { upsert: [] }));
    expect(st).toEqual(IDLE);
    expect(a).toEqual([{ a: "rewatch" }]);
  });

  it("accepts any first payload on a replace topic", () => {
    const [st] = onEvent(startWatch("w1"), "status", ev("w1", "status", 1, {}));
    expect(st.s).toBe("live");
  });
});

describe("the watch id", () => {
  it("drops an event of another stream while awaiting and while live", () => {
    const t: Topic = "talk:a";
    expect(onEvent(startWatch("w2"), t, ev("w1", t, 1, {}))).toEqual([startWatch("w2"), [{ a: "drop" }]]);
    const live: TopicState = { s: "live", watchId: "w2", seq: 4 };
    expect(onEvent(live, t, ev("w1", t, 5, {}))).toEqual([live, [{ a: "drop" }]]);
  });

  it("drops events while idle or failed", () => {
    const failed: TopicState = { s: "failed", code: "limit" };
    expect(onEvent(IDLE, "status", ev("w1", "status", 1, {}))[1]).toEqual([{ a: "drop" }]);
    expect(onEvent(failed, "status", ev("w1", "status", 1, {}))[1]).toEqual([{ a: "drop" }]);
  });

  it("lets a re-watch supersede the old stream without ordering against its events", () => {
    const t: Topic = "talk:a";
    const st = startWatch("w9");
    const r = run(t, st, [ev("w8", t, 7, {}), ev("w9", t, 1, { fresh: true })]);
    expect(r.actions).toEqual([{ a: "drop" }, { a: "deliver", payload: { fresh: true }, reset: false }]);
  });
});

describe("gaps", () => {
  it("rewatches on a skipped seq and goes idle", () => {
    const t: Topic = "talk:a";
    const r = run(t, startWatch("w1"), [ev("w1", t, 1, {}), ev("w1", t, 3, {})]);
    expect(r.st).toEqual(IDLE);
    expect(r.actions[1]).toEqual({ a: "rewatch" });
  });

  it("rewatches on a first event that is not seq 1", () => {
    expect(onEvent(startWatch("w1"), "talk:a", ev("w1", "talk:a", 2, {}))[1]).toEqual([{ a: "rewatch" }]);
  });

  it("rewatches on a repeated seq", () => {
    const live: TopicState = { s: "live", watchId: "w1", seq: 3 };
    expect(onEvent(live, "talk:a", ev("w1", "talk:a", 3, {}))[1]).toEqual([{ a: "rewatch" }]);
  });

  it("reads a seq past 2^32", () => {
    const live: TopicState = { s: "live", watchId: "w7", seq: 4294967296 };
    const [st] = onEvent(live, "diff:lola-fe-42", ev("w7", "diff:lola-fe-42", 4294967297, { rev: 5 }));
    expect(st).toMatchObject({ s: "live", seq: 4294967297 });
  });
});

describe("a split sessions reset", () => {
  const row = (id: string) => ({ id });

  it("assembles parts and delivers once, merged, as a reset", () => {
    const t: Topic = "sessions";
    const r = run(t, startWatch("w1"), [
      ev("w1", t, 1, { reset: true, more: true, upsert: [row("a")], events: [{ id: "e" }], omitted: ["big"] }),
      ev("w1", t, 2, { more: true, upsert: [row("b")] }),
      ev("w1", t, 3, { upsert: [row("c")], omitted: ["huge"] }),
    ]);
    expect(r.actions).toEqual([
      {
        a: "deliver",
        reset: true,
        payload: {
          reset: true,
          upsert: [row("a"), row("b"), row("c")],
          events: [{ id: "e" }],
          omitted: ["big", "huge"],
        },
      },
    ]);
    expect(r.st).toMatchObject({ s: "live", seq: 3, pending: undefined });
  });

  it("delivers nothing and holds the parts while more is set", () => {
    const [st, a] = onEvent(startWatch("w1"), "sessions", ev("w1", "sessions", 1, { reset: true, more: true }));
    expect(a).toEqual([]);
    expect(st).toMatchObject({ s: "live", pending: [{ reset: true, more: true }] });
  });

  it("treats a new reset inside an open reset as a gap", () => {
    const t: Topic = "sessions";
    const r = run(t, startWatch("w1"), [
      ev("w1", t, 1, { reset: true, more: true }),
      ev("w1", t, 2, { reset: true }),
    ]);
    expect(r.actions).toEqual([{ a: "rewatch" }]);
    expect(r.st).toEqual(IDLE);
  });

  it("delivers a delta after the reset as a plain apply", () => {
    const t: Topic = "sessions";
    const r = run(t, startWatch("w1"), [
      ev("w1", t, 1, { reset: true, upsert: [row("a")] }),
      ev("w1", t, 2, { upsert: [row("a")], remove: ["z"] }),
    ]);
    expect(r.actions.map((x) => (x as { reset: boolean }).reset)).toEqual([true, false]);
  });
});

describe("a split timeline reset", () => {
  it("concatenates items, keeps the first connections and older, and ends on the last lastSeq", () => {
    const t: Topic = "timeline:a";
    const r = run(t, startWatch("w1"), [
      ev("w1", t, 1, { reset: true, more: true, older: true, session: "a", lastSeq: 2, items: [{ id: "1" }], connections: [{ id: "c" }] }),
      ev("w1", t, 2, { more: true, session: "a", lastSeq: 4, items: [{ id: "2" }] }),
      ev("w1", t, 3, { session: "a", lastSeq: 9, items: [{ id: "3" }] }),
    ]);
    expect(r.actions).toEqual([
      {
        a: "deliver",
        reset: true,
        payload: {
          reset: true,
          older: true,
          session: "a",
          lastSeq: 9,
          items: [{ id: "1" }, { id: "2" }, { id: "3" }],
          connections: [{ id: "c" }],
        },
      },
    ]);
  });
});

describe("failures", () => {
  it("fails a topic the daemon refused, with the daemon's code", () => {
    const [st, a] = onWatchResult(startWatch("w1"), "w1", { ok: false, code: "unknown_session" });
    expect(st).toEqual({ s: "failed", code: "unknown_session" });
    expect(a).toEqual([{ a: "fail", code: "unknown_session" }]);
  });

  it("maps an unknown code to bad_request rather than interpreting it", () => {
    expect(toWatchCode("brand_new")).toBe("bad_request");
    expect(toWatchCode(undefined)).toBe("bad_request");
    const [st] = onWatchResult(startWatch("w1"), "w1", { ok: false, code: "brand_new" });
    expect(st).toEqual({ s: "failed", code: "bad_request" });
  });

  it("changes nothing on an ok result, and ignores a stale one", () => {
    const st = startWatch("w2");
    expect(onWatchResult(st, "w2", { ok: true })).toEqual([st, []]);
    expect(onWatchResult(st, "w1", { ok: false, code: "limit" })).toEqual([st, []]);
    expect(onWatchResult(IDLE, "w1", { ok: false, code: "limit" })).toEqual([IDLE, []]);
  });

  it("ends a live stream on an err carrying its watch id", () => {
    const live: TopicState = { s: "live", watchId: "w1", seq: 3 };
    const [st, a] = onStreamErr(live, stErr("w1", "frame_too_large"));
    expect(st).toEqual({ s: "failed", code: "frame_too_large" });
    expect(a).toEqual([{ a: "fail", code: "frame_too_large" }]);
  });

  it("ends an awaiting stream on an err too", () => {
    expect(onStreamErr(startWatch("w1"), stErr("w1", "denied"))[0]).toEqual({ s: "failed", code: "denied" });
  });

  it("ignores an err of another stream", () => {
    const live: TopicState = { s: "live", watchId: "w2", seq: 3 };
    expect(onStreamErr(live, stErr("w1", "rate_limited"))).toEqual([live, [{ a: "drop" }]]);
  });

  it("returns to idle on disconnect", () => {
    expect(onDisconnect()).toEqual(IDLE);
  });

  it.each([
    ["limit", "after_unwatch"],
    ["rate_limited", "after_delay"],
    ["unknown_session", "never"],
    ["frame_too_large", "never"],
    ["denied", "never"],
    ["invalid_topic", "next_epoch"],
    ["unavailable", "next_epoch"],
    ["bad_request", "next_epoch"],
  ] as const)("retries %s: %s", (code, retry) => {
    expect(retryOf(code)).toBe(retry);
  });
});

describe("the golden event vectors", () => {
  type Case = { name: string; frame: EventFrame };
  const cases = (eventVectors as unknown as { cases: Case[] }).cases;
  const byName = (n: string) => cases.find((c) => c.name === n)!.frame;

  it("accepts every event case as the first event of a fresh stream where it should", () => {
    for (const c of cases.filter((c) => c.frame.type === "event" && c.frame.seq === 1)) {
      const [st, a] = onEvent(startWatch(c.frame.id), c.frame.topic as Topic, c.frame);
      expect(a.some((x) => x.a === "rewatch"), c.name).toBe(false);
      expect(st.s, c.name).toBe("live");
    }
  });

  it("holds a split sessions reset until more clears", () => {
    const f = byName("event_sessions_reset_more");
    const [, a] = onEvent(startWatch(f.id), "sessions", f);
    expect(a).toEqual([]);
  });

  it("ends the stream on the stream-too-large err", () => {
    const f = byName("err_stream_too_large") as unknown as ErrFrame;
    const [st] = onStreamErr(startWatch(f.id!), f);
    expect(st).toEqual({ s: "failed", code: "frame_too_large" });
  });

  it("reports a partial watch result per topic", () => {
    const f = cases.find((c) => c.name === "resp_watch_partial")!.frame as unknown as {
      payload: { data: { topics: { topic: string; ok: boolean; code?: string }[] } };
    };
    const [ok, bad] = f.payload.data.topics;
    expect(onWatchResult(startWatch("w7"), "w7", ok)[1]).toEqual([]);
    expect(onWatchResult(startWatch("w7"), "w7", bad)[0]).toEqual({ s: "failed", code: "unknown_session" });
  });
});
