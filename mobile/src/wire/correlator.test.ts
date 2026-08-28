import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  ConnectionClosedError,
  Correlator,
  DaemonError,
  RequestTimeoutError,
  WireRefusalError,
} from "./correlator";
import { FakeChannel } from "./fake";
import { requestFrame, subFrame, type Frame } from "./protocol";

// See the note at the top of codec.test.ts: written but not executed here,
// because mobile/ has no node_modules yet.

/**
 * A correlator wired to a FakeChannel, with fake timers so a deadline can be
 * reached without waiting for one.
 */
function harness(opts: { maxInFlight?: number; timeoutMs?: number } = {}) {
  const ch = new FakeChannel();
  const c = new Correlator({
    send: (f) => ch.send(f),
    timeoutMs: opts.timeoutMs ?? 15_000,
    maxInFlight: opts.maxInFlight,
  });
  return { ch, c };
}

const resp = (id: string, data: unknown): Frame => ({
  v: 1,
  type: "resp",
  id,
  payload: { ok: true, data },
});

const errFrame = (id: string, code: string, message = "", extra: Partial<Frame> = {}): Frame => ({
  v: 1,
  type: "err",
  id,
  payload: { code, message },
  ...extra,
});

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("multiplexing", () => {
  it("settles replies that arrive out of order", () => {
    // The daemon runs req frames concurrently behind a semaphore and answers
    // them in whatever order the handlers finish, which is the entire reason
    // Frame.id exists.
    const { ch, c } = harness();
    const a = c.request(requestFrame("", "sessions"));
    const b = c.request(requestFrame("", "status"));
    const ids = ch.sent.map((f) => f.id!);
    expect(new Set(ids).size).toBe(2);

    c.accept(resp(ids[1], { runtimeOk: true }));
    c.accept(resp(ids[0], { sessions: [] }));

    return Promise.all([
      expect(a).resolves.toEqual({ sessions: [] }),
      expect(b).resolves.toEqual({ runtimeOk: true }),
    ]);
  });

  it("never reuses a correlation id", () => {
    // A pane subscription holds its id for as long as the subscription lives and
    // every resync on that pane echoes it. A reused id would take the next
    // resync as some later request's reply.
    const { c } = harness();
    const ids = new Set<string>();
    for (let i = 0; i < 200; i++) ids.add(c.nextID());
    expect(ids.size).toBe(200);
  });

  it("refuses a duplicate id rather than losing the first request", () => {
    const { c } = harness();
    const first = c.request(requestFrame("dup", "status"));
    const second = c.request(requestFrame("dup", "status"));
    void expect(second).rejects.toThrow(/already in flight/);
    c.accept(resp("dup", { ok: 1 }));
    return expect(first).resolves.toEqual({ ok: 1 });
  });

  it("ignores a reply for an id nobody is waiting on", () => {
    const { c } = harness();
    expect(c.accept(resp("nobody", {}))).toBe(false);
  });
});

describe("failure surfaces", () => {
  it("rejects on an err frame carrying the request's id", () => {
    const { ch, c } = harness();
    const p = c.request(requestFrame("", "kill", { session: "lola-fe-42" }));
    const id = ch.last!.id!;
    c.accept(errFrame(id, "unknown_cmd", "command is not available remotely"));
    return expect(p).rejects.toBeInstanceOf(WireRefusalError);
  });

  it("carries the version bounds off an unsupported_version refusal", () => {
    // The one refusal a client can act on precisely: it names which side is
    // behind rather than showing a connect error.
    const { ch, c } = harness();
    const p = c.request(requestFrame("", "status"));
    const id = ch.last!.id!;
    c.accept({
      v: 1,
      type: "err",
      id,
      payload: { code: "unsupported_version", message: "daemon speaks envelope v1", minV: 1, maxV: 1 },
    });
    return p.catch((e: WireRefusalError) => {
      expect(e.isVersionSkew).toBe(true);
      expect(e.minV).toBe(1);
      expect(e.maxV).toBe(1);
    });
  });

  it("rejects an ok:false response, which is not an err frame", () => {
    const { ch, c } = harness();
    const p = c.request(requestFrame("", "answer", { session: "lola-x", text: "hi" }));
    const id = ch.last!.id!;
    c.accept({ v: 1, type: "resp", id, payload: { ok: false, error: "session is not parked" } });
    return expect(p).rejects.toBeInstanceOf(DaemonError);
  });

  it("rejects on the deadline and stops holding the id", () => {
    const { ch, c } = harness({ timeoutMs: 1000 });
    const p = c.request(requestFrame("", "sessions"));
    const id = ch.last!.id!;
    vi.advanceTimersByTime(1001);
    expect(c.pendingIds).not.toContain(id);
    return expect(p).rejects.toBeInstanceOf(RequestTimeoutError);
  });

  it("does not settle twice when a reply races the deadline", () => {
    const { ch, c } = harness({ timeoutMs: 1000 });
    const p = c.request(requestFrame("", "sessions"));
    const id = ch.last!.id!;
    c.accept(resp(id, { sessions: [] }));
    vi.advanceTimersByTime(5000);
    expect(c.accept(resp(id, { sessions: [1] }))).toBe(false);
    return expect(p).resolves.toEqual({ sessions: [] });
  });

  it("rejects when the send itself throws", () => {
    const { ch, c } = harness();
    ch.sendError = new Error("socket is gone");
    return expect(c.request(requestFrame("", "status"))).rejects.toThrow("socket is gone");
  });
});

describe("no pending promise survives a disconnect", () => {
  it("rejects everything in flight and everything queued", async () => {
    // This is the property that matters most on a phone: the app is
    // backgrounded, the OS SIGSTOPs the queue, the peer resets, and nothing
    // will ever answer. A hung promise there is a spinner that never stops.
    const { c } = harness({ maxInFlight: 2 });
    const all = [
      c.request(requestFrame("", "sessions")),
      c.request(requestFrame("", "projects")),
      c.request(requestFrame("", "status")), // queued behind the cap
      c.request(requestFrame("", "prs")), // queued behind the cap
    ];
    expect(c.inFlight).toBe(2);
    expect(c.queued).toBe(2);

    c.failAll(new ConnectionClosedError("peer reset"));

    const settled = await Promise.allSettled(all);
    expect(settled.every((s) => s.status === "rejected")).toBe(true);
    expect(c.pendingIds).toHaveLength(0);
    expect(c.inFlight).toBe(0);
    expect(c.queued).toBe(0);
  });

  it("refuses a new request after the connection is gone", () => {
    const { c } = harness();
    c.failAll(new ConnectionClosedError());
    return expect(c.request(requestFrame("", "status"))).rejects.toBeInstanceOf(
      ConnectionClosedError,
    );
  });

  it("leaves no timer behind", async () => {
    const { c } = harness({ timeoutMs: 1000 });
    const p = c.request(requestFrame("", "status"));
    c.failAll(new ConnectionClosedError());
    await expect(p).rejects.toBeInstanceOf(ConnectionClosedError);
    // If failAll had left the deadline armed it would fire into an entry that no
    // longer exists, which is harmless here but is a leak on a long session.
    expect(vi.getTimerCount()).toBe(0);
  });
});

describe("the in-flight cap", () => {
  it("queues past the daemon's limit instead of being refused there", () => {
    // More than four concurrent req frames on one connection is answered with a
    // non-fatal rate_limited refusal. Queueing here means the burst costs
    // latency rather than errors.
    const { ch, c } = harness({ maxInFlight: 2 });
    const a = c.request(requestFrame("", "sessions"));
    void c.request(requestFrame("", "projects"));
    void c.request(requestFrame("", "status"));
    expect(ch.sent).toHaveLength(2);

    c.accept(resp(ch.sent[0].id!, {}));
    expect(ch.sent).toHaveLength(3);
    return expect(a).resolves.toEqual({});
  });

  it("does not send a queued request that was already settled", async () => {
    // Putting it on the wire then would count against the daemon's cap for a
    // reply nobody is waiting for, and the slot it took would come back only
    // when the daemon answered a request the client had given up on.
    const { ch, c } = harness({ maxInFlight: 1 });
    const first = c.request(requestFrame("", "sessions"));
    const ac = new AbortController();
    const queued = c.request(requestFrame("", "projects"), { signal: ac.signal });
    expect(ch.sent).toHaveLength(1);

    ac.abort(); // settles while it is still waiting for a slot
    await expect(queued).rejects.toThrow();

    c.accept(resp(ch.sent[0].id!, {})); // releases the slot
    await expect(first).resolves.toEqual({});

    expect(ch.sent).toHaveLength(1);
    expect(c.inFlight).toBe(0);
    expect(c.queued).toBe(0);
  });

  // A failed send is the one path where the slot bookkeeping can go wrong
  // silently. `dispatch` marks the entry slotted BEFORE calling send, so
  // `settle` already hands the slot back on the failure paths; releasing it a
  // second time by hand does not merely double-decrement a counter, it runs
  // `releaseSlot`'s queue pump twice, so one failed send puts an extra request
  // on the wire while the counter records fewer than are really in flight.
  // Against the daemon that means a fifth concurrent `req` frame and a
  // `rate_limited` refusal — the exact outcome the cap exists to prevent.
  //
  // Both tests drive `send` directly rather than through FakeChannel, because
  // the interesting failure is ONE send failing while others succeed and
  // `FakeChannel.sendError` is a single latched flag.
  function slotHarness(maxInFlight: number) {
    const sent: Frame[] = [];
    let failNext = false;
    let asyncFailNext = false;
    const c = new Correlator({
      maxInFlight,
      send: (f) => {
        if (failNext) {
          failNext = false;
          throw new Error("write failed");
        }
        sent.push(f);
        if (asyncFailNext) {
          asyncFailNext = false;
          return Promise.reject(new Error("write failed later"));
        }
        return undefined;
      },
    });
    return {
      c,
      sent,
      failSyncOnce: () => (failNext = true),
      failAsyncOnce: () => (asyncFailNext = true),
    };
  }

  it("releases exactly one slot when a send throws from the queue", async () => {
    const h = slotHarness(1);
    const a = h.c.request(requestFrame("", "sessions"));
    const b = h.c.request(requestFrame("", "projects"));
    const d = h.c.request(requestFrame("", "status"));
    expect(h.sent).toHaveLength(1);

    h.failSyncOnce(); // b's send throws when the queue hands it a slot
    h.c.accept(resp(h.sent[0].id!, {}));

    await expect(a).resolves.toEqual({});
    await expect(b).rejects.toThrow(/write failed/);

    // b's slot went to d, and d is genuinely in flight. A second release would
    // have reported the connection idle while d's reply was still outstanding.
    expect(h.sent).toHaveLength(2);
    expect(h.c.inFlight).toBe(1);
    expect(h.c.queued).toBe(0);

    // The proof that the count is not merely cosmetic: a further request must
    // WAIT, not join d on the wire.
    const e = h.c.request(requestFrame("", "doctor"));
    expect(h.sent).toHaveLength(2);
    expect(h.c.queued).toBe(1);

    h.c.accept(resp(h.sent[1].id!, {}));
    await expect(d).resolves.toEqual({});
    h.c.accept(resp(h.sent[2].id!, {}));
    await expect(e).resolves.toEqual({});
    expect(h.c.inFlight).toBe(0);
  });

  it("releases exactly one slot when a send rejects asynchronously", async () => {
    const h = slotHarness(1);
    h.failAsyncOnce();
    const a = h.c.request(requestFrame("", "sessions"));
    const b = h.c.request(requestFrame("", "projects"));
    expect(h.sent).toHaveLength(1);

    await expect(a).rejects.toThrow(/write failed later/);

    // a's slot went to b exactly once.
    expect(h.sent).toHaveLength(2);
    expect(h.c.inFlight).toBe(1);

    const d = h.c.request(requestFrame("", "status"));
    expect(h.sent).toHaveLength(2);
    expect(h.c.queued).toBe(1);

    h.c.accept(resp(h.sent[1].id!, {}));
    await expect(b).resolves.toEqual({});
    h.c.accept(resp(h.sent[2].id!, {}));
    await expect(d).resolves.toEqual({});
  });

  it("narrows its own cap when the daemon refuses one anyway", () => {
    const { ch, c } = harness({ maxInFlight: 4 });
    const p = c.request(requestFrame("", "sessions"));
    c.accept(errFrame(ch.last!.id!, "rate_limited", "too many requests in flight on this connection"));
    void expect(p).rejects.toBeInstanceOf(WireRefusalError);

    // The next burst stays one below what was just refused.
    for (let i = 0; i < 6; i++) void c.request(requestFrame("", "status"));
    expect(c.inFlight).toBe(3);
  });
});

describe("the pane subscription ack", () => {
  it("is the first resync on the sub's id", () => {
    // There is no other acknowledgement: a sub is answered by a resync or by an
    // err, and nothing else.
    const { ch, c } = harness();
    const p = c.request(subFrame("", "lola-fe-42", { cols: 55, rows: 34 }), { expect: "resync" });
    const id = ch.last!.id!;
    c.accept({
      v: 1,
      type: "resync",
      id,
      pane: "lola-fe-42",
      seq: 1,
      payload: { cols: 200, rows: 50, cursorX: 0, cursorY: 0 },
    });
    return expect(p).resolves.toMatchObject({ cols: 200, rows: 50 });
  });

  it("stops correlating once the subscription is up", () => {
    // Every LATER resync on that pane echoes the same id. They are not stray
    // frames — they are the normal case — and only the pane router consumes them.
    const { ch, c } = harness();
    void c.request(subFrame("", "lola-fe-42"), { expect: "resync" });
    const id = ch.last!.id!;
    const frame: Frame = {
      v: 1,
      type: "resync",
      id,
      pane: "lola-fe-42",
      seq: 1,
      payload: { cols: 80, rows: 24, cursorX: 0, cursorY: 0 },
    };
    expect(c.accept(frame)).toBe(true);
    expect(c.accept({ ...frame, seq: 9 })).toBe(false);
  });

  it("rejects a subscribe the daemon refused", () => {
    const { ch, c } = harness();
    const p = c.request(subFrame("", "lola-nope"), { expect: "resync" });
    c.accept(errFrame(ch.last!.id!, "unknown_pane", "pane is not available", { pane: "lola-nope" }));
    return p.catch((e: WireRefusalError) => {
      expect(e.code).toBe("unknown_pane");
      expect(e.pane).toBe("lola-nope");
    });
  });

  it("refuses a reply of the wrong type", () => {
    const { ch, c } = harness();
    const p = c.request(subFrame("", "lola-fe-42"), { expect: "resync" });
    c.accept(resp(ch.last!.id!, {}));
    return expect(p).rejects.toThrow(/expected a resync/);
  });
});

describe("abort", () => {
  it("rejects immediately for an already-aborted signal and sends nothing", () => {
    const { ch, c } = harness();
    const ac = new AbortController();
    ac.abort();
    void expect(c.request(requestFrame("", "status"), { signal: ac.signal })).rejects.toThrow();
    expect(ch.sent).toHaveLength(0);
  });

  it("rejects an in-flight request when its signal fires", () => {
    // The daemon is NOT told: there is no cancel frame in this protocol, so a
    // late reply simply finds no pending id and is dropped.
    const { ch, c } = harness();
    const ac = new AbortController();
    const p = c.request(requestFrame("", "sessions"), { signal: ac.signal });
    const id = ch.last!.id!;
    ac.abort();
    void expect(p).rejects.toThrow();
    expect(c.accept(resp(id, {}))).toBe(false);
  });
});
