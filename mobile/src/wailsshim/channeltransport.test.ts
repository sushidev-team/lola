import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ChannelTransport, MIGRATE_DRAIN_MS } from "./channeltransport";
import { FakeLink } from "./fakelink";
import { LinkError } from "./pluginerror";
import {
  ConnectionClosedError,
  FRAME_ERR,
  FRAME_EVENT,
  FRAME_PTY,
  FRAME_RESP,
  FRAME_RESYNC,
  FRAME_VERSION_CURRENT,
  FakeChannel,
  WireRefusalError,
  base64ToBytes,
  type Frame,
  type PaneEvent,
} from "../wire";

const PANE = "lola-fe-42";
const target = { daemonId: "d1" };

function ok(id: string, data?: unknown): Frame {
  return { v: FRAME_VERSION_CURRENT, type: FRAME_RESP, id, payload: { ok: true, data } };
}

async function connected(): Promise<{ t: ChannelTransport; ch: FakeChannel }> {
  const ch = new FakeChannel();
  const t = new ChannelTransport({ open: async () => ch });
  await t.connect(target);
  return { t, ch };
}

async function linked(): Promise<{ t: ChannelTransport; ch: FakeLink }> {
  const ch = new FakeLink();
  const t = new ChannelTransport({ open: async () => ch });
  await t.connect(target);
  return { t, ch };
}

describe("connecting", () => {
  it("is ready with the plugin's epoch, path and device, and sends nothing", async () => {
    const ch = new FakeLink();
    ch.up = { epoch: 7, path: { kind: "relay" }, device: { deviceId: "x", name: "n", tiers: ["read"], host: "h" } };
    const t = new ChannelTransport({ open: async () => ch });
    await t.connect(target);
    expect(t.status).toMatchObject({ phase: "ready", epoch: 7, path: { kind: "relay" }, device: { deviceId: "x" } });
    expect(ch.sent).toHaveLength(0);
  });

  it("numbers epochs itself for a channel that reports none", async () => {
    const { t } = await connected();
    expect(t.status).toMatchObject({ phase: "ready", epoch: 1 });
  });

  it("closes and records the failure code when no path came up", async () => {
    const boom = new LinkError("unreachable", "unreachable: no path");
    const t = new ChannelTransport({
      open: async () => {
        throw boom;
      },
    });
    await expect(t.connect(target)).rejects.toBe(boom);
    expect(t.status).toMatchObject({ phase: "closed", failure: "unreachable" });
  });
});

describe("requests", () => {
  it("turns a command into a req frame carrying cmd on the envelope AND the payload", async () => {
    const { t, ch } = await connected();
    ch.onSend = (f) => {
      if (f.type === "req") ch.deliver(ok(f.id!, { sessions: [] }));
    };
    await expect(t.request("sessions")).resolves.toEqual({ sessions: [] });
    expect(ch.last).toMatchObject({ v: 2, type: "req", cmd: "sessions", payload: { cmd: "sessions" } });
  });

  it("sends whatever command it is given and lets the daemon refuse it", async () => {
    const { t, ch } = await connected();
    const p = t.request("reload");
    ch.deliver({ v: 2, type: FRAME_ERR, id: ch.last!.id, payload: { code: "denied" } });
    await expect(p).rejects.toMatchObject({ code: "denied" });
    expect(ch.sent).toHaveLength(1);
    expect(t.status.phase).toBe("ready");
  });

  it("rejects with the daemon's own error text on ok:false", async () => {
    const { t, ch } = await connected();
    ch.onSend = (f) => {
      if (f.type === "req") ch.deliver({ v: 2, type: FRAME_RESP, id: f.id, payload: { ok: false, error: "no such session" } });
    };
    await expect(t.request("kill", { session: "x" })).rejects.toThrow(/no such session/);
  });

  it("rejects at once when not connected", async () => {
    const t = new ChannelTransport({ open: async () => new FakeChannel() });
    await expect(t.request("sessions")).rejects.toBeInstanceOf(ConnectionClosedError);
  });

  it("begin() reports the request id before anything is answered", async () => {
    const { t, ch } = await connected();
    const { id, done } = t.begin("watch", { args: { topics: ["sessions"] } });
    expect(ch.last).toMatchObject({ id, cmd: "watch" });
    ch.deliver(ok(id, { topics: [] }));
    await expect(done).resolves.toEqual({ topics: [] });
  });
});

describe("epochs", () => {
  it("fails in-flight requests when the connection drops and goes back to ready with a new epoch", async () => {
    const { t, ch } = await linked();
    const seen: string[] = [];
    t.onStatus((s) => seen.push(`${s.phase}:${s.epoch}`));
    const p = t.request("kill", { session: "x" });
    const settled = p.catch((e) => e);
    ch.raiseDown(new LinkError("peer_closed", "peer_closed"));
    expect(await settled).toBeInstanceOf(ConnectionClosedError);
    expect(t.status).toMatchObject({ phase: "connecting", failure: "peer_closed" });
    ch.raiseUp({ epoch: 2 });
    expect(seen).toEqual(["connecting:1", "ready:2"]);
  });

  it("re-sends a read-tier request once on the next epoch", async () => {
    const { t, ch } = await linked();
    const p = t.request("sessions");
    ch.raiseDown(new LinkError("peer_closed", "x"));
    ch.raiseUp({ epoch: 2 });
    await Promise.resolve();
    await Promise.resolve();
    const again = ch.sent.filter((f) => f.cmd === "sessions");
    expect(again).toHaveLength(2);
    ch.deliver(ok(again[1].id!, { sessions: [] }));
    await expect(p).resolves.toEqual({ sessions: [] });
  });

  it("never re-sends a request that is not read-tier", async () => {
    const { t, ch } = await linked();
    const p = t.request("agentSay", { session: "s", text: "hi" });
    const settled = p.catch((e) => e);
    ch.raiseDown(new LinkError("peer_closed", "x"));
    ch.raiseUp({ epoch: 2 });
    expect(await settled).toBeInstanceOf(ConnectionClosedError);
    expect(ch.sent.filter((f) => f.cmd === "agentSay")).toHaveLength(1);
  });

  it("does not re-send after a deliberate disconnect", async () => {
    const { t, ch } = await linked();
    const p = t.request("sessions");
    const settled = p.catch((e) => e);
    await t.disconnect("by hand");
    expect(await settled).toBeInstanceOf(ConnectionClosedError);
    expect(ch.sent.filter((f) => f.cmd === "sessions")).toHaveLength(1);
  });

  it("fails every pane subscription with connection_closed when the epoch ends", async () => {
    const { t, ch } = await linked();
    ch.onSend = (f) => {
      if (f.type === "sub") ch.deliver({ v: 2, type: FRAME_RESYNC, id: f.id, pane: f.pane, seq: 1, payload: { cols: 80, rows: 24 } });
    };
    const sub = await t.subscribe(PANE);
    const errs: WireRefusalError[] = [];
    sub.onError((e) => errs.push(e));
    ch.raiseDown(new LinkError("peer_closed", "x"));
    expect(errs.map((e) => e.code)).toEqual(["connection_closed"]);
    expect(t.subscription(PANE)).toBeUndefined();
  });

  describe("a migration", () => {
    beforeEach(() => vi.useFakeTimers());
    afterEach(() => vi.useRealTimers());

    it("is adopted at once when no mutating request is in flight", async () => {
      const { ch } = await linked();
      ch.raiseMigrating({ epoch: 2 });
      await vi.advanceTimersByTimeAsync(0);
      expect(ch.adopted).toEqual([2]);
    });

    it("waits for a mutating request, then adopts", async () => {
      const { t, ch } = await linked();
      void t.request("agentSay", { session: "s", text: "x" }).catch(() => {});
      const id = ch.last!.id!;
      ch.raiseMigrating({ epoch: 2 });
      await vi.advanceTimersByTimeAsync(100);
      expect(ch.adopted).toEqual([]);
      ch.deliver(ok(id));
      await vi.advanceTimersByTimeAsync(0);
      expect(ch.adopted).toEqual([2]);
    });

    it("adopts after MIGRATE_DRAIN_MS whatever is still in flight", async () => {
      const { t, ch } = await linked();
      void t.request("agentSay", { session: "s", text: "x" }).catch(() => {});
      ch.raiseMigrating({ epoch: 2 });
      await vi.advanceTimersByTimeAsync(MIGRATE_DRAIN_MS - 1);
      expect(ch.adopted).toEqual([]);
      await vi.advanceTimersByTimeAsync(2);
      expect(ch.adopted).toEqual([2]);
    });

    it("does not wait for a read-tier request", async () => {
      const { t, ch } = await linked();
      void t.request("sessions").catch(() => {});
      ch.raiseMigrating({ epoch: 2 });
      await vi.advanceTimersByTimeAsync(0);
      expect(ch.adopted).toEqual([2]);
    });
  });
});

describe("pushed events", () => {
  it("routes event frames and the err that ends a stream to onEvent, not to the connection", async () => {
    const { t, ch } = await connected();
    const seen: Frame[] = [];
    const refusals: WireRefusalError[] = [];
    t.onEvent((f) => seen.push(f));
    t.onRefusal((e) => refusals.push(e));
    ch.deliverEvent("r1", "sessions", 1, { reset: true });
    ch.deliver({ v: 2, type: FRAME_ERR, id: "r1", topic: "sessions", payload: { code: "rate_limited" } });
    expect(seen.map((f) => f.type)).toEqual([FRAME_EVENT, FRAME_ERR]);
    expect(refusals).toHaveLength(0);
    expect(t.status.phase).toBe("ready");
  });
});

describe("pane subscriptions", () => {
  /** Connect, then subscribe with a scripted resync acknowledgement. */
  async function subscribed() {
    const { t, ch } = await connected();
    ch.onSend = (f) => {
      if (f.type === "sub") {
        ch.deliver({
          v: 2,
          type: FRAME_RESYNC,
          id: f.id,
          pane: f.pane,
          seq: 1,
          payload: { cols: 120, rows: 40, lines: ["ready"], cursorX: 0, cursorY: 0, altScreen: true },
        });
      }
    };
    const sub = await t.subscribe(PANE, { cols: 55, rows: 30 });
    return { t, ch, sub };
  }

  it("acknowledges a subscription with its FIRST resync, and adopts that screen", async () => {
    const { ch, sub } = await subscribed();
    const subFrame = ch.sent.find((f) => f.type === "sub");
    expect(subFrame).toMatchObject({ type: "sub", pane: PANE, payload: { cols: 55, rows: 30 } });
    expect(sub.screen?.cols).toBe(120);
    expect(sub.lastSeq).toBe(1);
  });

  it("replays frames that arrived before the first listener, in order", async () => {
    // THE FIRST-OPEN BLANK PANE. `subscribe()` resolves on the daemon's
    // acknowledging resync, and the caller only gets to call `onEvent` a
    // microtask later. tmux's initial paint of a freshly attached pane lands as
    // pty frames inside that hop; a plain fanout over an empty listener set
    // discards them, and an idle agent writes nothing else for minutes — so the
    // pane stays empty until something forces a repaint.
    const { ch, sub } = await subscribed();
    ch.deliver({ v: 2, type: FRAME_PTY, pane: PANE, seq: 2, payload: { data: "aGk=" } });
    ch.deliver({ v: 2, type: FRAME_PTY, pane: PANE, seq: 3, payload: { data: "IXo=" } });

    const events: PaneEvent[] = [];
    sub.onEvent((e) => events.push(e));

    const text = events.map((e) => new TextDecoder().decode((e as { data: Uint8Array }).data));
    expect(text).toEqual(["hi", "!z"]);
  });

  it("replays only to the FIRST listener, so a second reader does not double-render", async () => {
    const { ch, sub } = await subscribed();
    ch.deliver({ v: 2, type: FRAME_PTY, pane: PANE, seq: 2, payload: { data: "aGk=" } });

    const first: PaneEvent[] = [];
    const second: PaneEvent[] = [];
    sub.onEvent((e) => first.push(e));
    sub.onEvent((e) => second.push(e));

    expect(first).toHaveLength(1);
    expect(second).toHaveLength(0);
  });

  it("does not replay the acknowledging resync — the caller reads it off `screen`", async () => {
    const { sub } = await subscribed();
    const events: PaneEvent[] = [];
    sub.onEvent((e) => events.push(e));
    expect(events).toHaveLength(0);
    expect(sub.screen?.cols).toBe(120);
  });

  it("marks the replay torn when more arrived than the buffer holds", async () => {
    // A screen torn down mid-attach is the only way to reach this. Dropping the
    // oldest and announcing a gap makes the terminal re-subscribe, which is the
    // defined recovery: a byte range cannot be replayed from halfway through an
    // escape sequence.
    const { ch, sub } = await subscribed();
    for (let i = 0; i < 300; i++) {
      ch.deliver({ v: 2, type: FRAME_PTY, pane: PANE, seq: 2 + i, payload: { data: "eA==" } });
    }
    const events: PaneEvent[] = [];
    sub.onEvent((e) => events.push(e));
    expect(events).toHaveLength(256);
    expect(events[0]).toMatchObject({ kind: "output", gap: true });
    expect(events[1]).toMatchObject({ kind: "output", gap: false });
  });

  it("replays a pane death that landed before the listener did", async () => {
    // The ack is not replayed, but everything after it is — including the exit
    // resync. A caller that never hears about it waits forever on a pane that
    // will never write again.
    const { ch, sub } = await subscribed();
    ch.deliver({
      v: 2,
      type: FRAME_RESYNC,
      pane: PANE,
      seq: 2,
      payload: { cols: 120, rows: 40, cursorX: 0, cursorY: 0, exited: true },
    });
    const events: PaneEvent[] = [];
    sub.onEvent((e) => events.push(e));
    expect(events).toHaveLength(1);
    expect(events[0]).toMatchObject({ kind: "exit" });
  });

  it("refuses a pane name that fails the daemon's own shape gate", async () => {
    const { t } = await connected();
    await expect(t.subscribe("../etc/passwd")).rejects.toThrow(/not a lola pane name/);
  });

  it("decodes pty output, including the null data an empty flush produces", async () => {
    // PTYOutputPayload.Data carries no omitempty and a nil []byte marshals as
    // JSON null, so both "" and null are reachable and neither is malformed.
    const { ch, sub } = await subscribed();
    const events: PaneEvent[] = [];
    sub.onEvent((e) => events.push(e));
    ch.deliver({ v: 2, type: FRAME_PTY, pane: PANE, seq: 2, payload: { data: "aGk=" } });
    ch.deliver({ v: 2, type: FRAME_PTY, pane: PANE, seq: 3, payload: { data: null } });
    expect(events).toHaveLength(2);
    expect(new TextDecoder().decode((events[0] as { data: Uint8Array }).data)).toBe("hi");
    expect((events[1] as { data: Uint8Array }).data).toHaveLength(0);
    expect(sub.lastSeq).toBe(3);
  });

  it("treats a gap arriving on a resync as SELF-HEALING and one on pty as torn", async () => {
    // The bus drops a frame for an overflowing subscriber, keeps its sequence
    // advancing so the gap stays visible, then repairs with a full screen.
    // Only a gap arriving on a pty frame is a reason to re-subscribe.
    const { ch, sub } = await subscribed();
    const events: PaneEvent[] = [];
    sub.onEvent((e) => events.push(e));
    ch.deliver({
      v: 2,
      type: FRAME_RESYNC,
      pane: PANE,
      seq: 9,
      payload: { cols: 120, rows: 40, cursorX: 0, cursorY: 0 },
    });
    ch.deliver({ v: 2, type: FRAME_PTY, pane: PANE, seq: 40, payload: { data: "" } });
    expect(events[0]).toMatchObject({ kind: "resync", repaired: true });
    expect(events[1]).toMatchObject({ kind: "output", gap: true });
  });

  it("reports a pane death, which arrives as a resync rather than its own type", async () => {
    const { ch, sub } = await subscribed();
    const events: PaneEvent[] = [];
    sub.onEvent((e) => events.push(e));
    ch.deliver({
      v: 2,
      type: FRAME_RESYNC,
      pane: PANE,
      seq: 2,
      payload: { cols: 0, rows: 0, cursorX: 0, cursorY: 0, exited: true },
    });
    expect(events[0]).toMatchObject({ kind: "exit" });
    expect(sub.exited).toBe(true);
  });

  it("sends a write as an uncorrelated pty frame carrying base64 bytes", async () => {
    // No id, for the same reason unsub carries none: a successful pty write is
    // never acknowledged, so an id would leak a pending promise until its
    // deadline. A refusal echoes the PANE, which is all the routing needed.
    const { ch, sub } = await subscribed();
    await sub.write("y\r");
    expect(ch.last).toEqual({
      v: 2,
      type: "pty",
      pane: PANE,
      payload: { action: "write", data: "eQ0=" },
    });
    expect(ch.last?.id).toBeUndefined();
    expect(new TextDecoder().decode(base64ToBytes("eQ0="))).toBe("y\r");
  });

  it("clamps a scroll to the daemon's own MaxScrollLines", async () => {
    const { ch, sub } = await subscribed();
    await sub.scroll(-99999);
    expect(ch.last).toMatchObject({ type: "pty", payload: { action: "scroll", lines: -500 } });
  });

  it("sends unsub with no id and does not wait for an acknowledgement", async () => {
    const { ch, sub } = await subscribed();
    await sub.close(); // resolves; the daemon never replies to an unsub
    expect(ch.last).toEqual({ v: 2, type: "unsub", pane: PANE });
  });

  it("fails every pane and every pending request when the channel closes", async () => {
    const { t, ch, sub } = await subscribed();
    const errs: WireRefusalError[] = [];
    sub.onError((e) => errs.push(e));
    ch.close("socket dropped");
    expect(errs).toHaveLength(1);
    expect(t.status.phase).toBe("closed");
    expect(t.subscription(PANE)).toBeUndefined();
  });
});

describe("frames the client must not accept", () => {
  beforeEach(() => vi.spyOn(console, "error").mockImplementation(() => {}));
  afterEach(() => vi.restoreAllMocks());

  it("refuses a frame travelling the wrong way rather than rendering it", async () => {
    const { t, ch } = await connected();
    const refusals: WireRefusalError[] = [];
    t.onRefusal((e) => refusals.push(e));
    ch.deliver({ v: 2, type: "sub", pane: PANE }); // client -> daemon only
    expect(refusals[0]?.code).toBe("unknown_type");
  });

  it("refuses an envelope version outside the supported window", async () => {
    const { t, ch } = await connected();
    const refusals: WireRefusalError[] = [];
    t.onRefusal((e) => refusals.push(e));
    // An err frame is exempt: its shape is frozen at v1 forever so that a peer
    // which understands nothing else can still read `unsupported_version`.
    ch.deliver({ v: 99, type: FRAME_RESP, id: "zzz", payload: { ok: true } });
    expect(refusals[0]?.code).toBe("unsupported_version");
    ch.deliver({ v: 99, type: FRAME_ERR, payload: { code: "unsupported_version", minV: 2, maxV: 3 } });
    expect(refusals[1]).toMatchObject({ code: "unsupported_version", minV: 2, maxV: 3 });
  });

  it("tears the connection down on a code that is always fatal", async () => {
    const { t, ch } = await connected();
    ch.deliver({ v: 2, type: FRAME_ERR, payload: { code: "frame_too_large" } });
    expect(t.status.phase).toBe("closed");
  });

  it("tears down on a fatal code even when it settled a pending request", async () => {
    // The fatal code is read before the correlator settles the request, so the
    // request still rejects with its own refusal and the connection still dies
    // without waiting for the socket close to arrive.
    const { t, ch } = await connected();
    const p = t.request("sessions");
    ch.deliver({ v: 2, type: FRAME_ERR, id: ch.last!.id, payload: { code: "frame_too_large" } });
    await expect(p).rejects.toMatchObject({ code: "frame_too_large" });
    expect(t.status.phase).toBe("closed");
  });

  it("rejects one request and keeps the socket up for denied, unknown_cmd and rate_limited", async () => {
    const { t, ch } = await connected();
    for (const code of ["denied", "unknown_cmd", "rate_limited"]) {
      const p = t.request("sessions");
      ch.deliver({ v: 2, type: FRAME_ERR, id: ch.last!.id, payload: { code } });
      await expect(p).rejects.toMatchObject({ code });
      expect(t.status.phase).toBe("ready");
    }
  });
});

describe("a subscribe that does not complete", () => {
  beforeEach(() => vi.spyOn(console, "error").mockImplementation(() => {}));
  afterEach(() => vi.restoreAllMocks());

  it("unsubscribes after an abort, because the daemon may have registered one", async () => {
    // remote.conn.subscribe registers the subscription BEFORE its pump writes
    // the first resync, so giving up locally can leave the daemon pumping a pane
    // nothing routes — holding a panebus subscription and its tmux attach for
    // the life of the connection.
    const { t, ch } = await connected();
    const ac = new AbortController();
    const p = t.subscribe(PANE, undefined, { signal: ac.signal });
    ac.abort();
    await expect(p).rejects.toThrow();

    const undo = ch.sent.filter((f) => f.type === "unsub" && f.pane === PANE);
    expect(undo).toHaveLength(1);
    expect(undo[0].id).toBeUndefined(); // unacknowledged by design
    expect(t.subscription(PANE)).toBeUndefined();
  });

  it("does NOT unsubscribe after a refusal, which created nothing to undo", async () => {
    const ch = new FakeChannel();
    ch.onSend = (f) => {
      if (f.type === "sub") {
        ch.deliver({ v: 2, type: FRAME_ERR, id: f.id, pane: f.pane, payload: { code: "unknown_pane" } });
      }
    };
    const t = new ChannelTransport({ open: async () => ch });
    await t.connect(target);

    await expect(t.subscribe(PANE)).rejects.toMatchObject({ code: "unknown_pane" });
    expect(ch.sent.filter((f) => f.type === "unsub")).toHaveLength(0);
  });
});

describe("a refusal that arrives instead of a channel", () => {
  // The plugin authenticates itself, so a daemon refusal never reaches the frame
  // layer: `connect` rejects. It must land in `status.refusal`, not only in
  // `status.error`, or the screen blames the network for what the daemon said.

  it("records a WireRefusalError from the channel factory as status.refusal", async () => {
    const refusal = new WireRefusalError("denied", "the daemon refused the connection");
    const t = new ChannelTransport({
      open: async () => {
        throw refusal;
      },
    });

    await expect(t.connect(target)).rejects.toBe(refusal);
    expect(t.status.phase).toBe("closed");
    expect(t.status.refusal).toEqual({
      code: "denied",
      message: refusal.message,
      minV: undefined,
      maxV: undefined,
    });
  });

  it("carries the version bounds through, so the skew can name a side", async () => {
    const refusal = new WireRefusalError("unsupported_version", "v2..v3", undefined, 2, 3);
    const t = new ChannelTransport({
      open: async () => {
        throw refusal;
      },
    });
    await expect(t.connect(target)).rejects.toBe(refusal);
    expect(t.status.refusal?.minV).toBe(2);
    expect(t.status.refusal?.maxV).toBe(3);
  });

  it("leaves an ordinary transport failure without a refusal", async () => {
    const boom = new Error("the connection timed out");
    const t = new ChannelTransport({
      open: async () => {
        throw boom;
      },
    });
    await expect(t.connect(target)).rejects.toBe(boom);
    expect(t.status.error).toBe(boom);
    expect(t.status.refusal).toBeUndefined();
  });

  it("records a refusal that closes an already-open connection", async () => {
    // The other half of the same path: a refusal can also land AFTER connect
    // resolved, as a plugin `state` event that becomes a close carrying the
    // daemon's code. `FakeChannel.close` only ever produces a plain Error, so
    // the close is driven directly here.
    let onClose: ((err?: Error) => void) | undefined;
    const ch = new FakeChannel();
    const t = new ChannelTransport({
      open: async () => ({
        send: (f) => ch.send(f),
        onFrame: (l) => ch.onFrame(l),
        onClose: (l) => {
          onClose = l;
          return ch.onClose(l);
        },
        close: async () => ch.close(),
      }),
    });
    await t.connect(target);
    expect(t.status.phase).toBe("ready");

    onClose?.(new WireRefusalError("denied", "the daemon refused the connection"));
    expect(t.status.phase).toBe("closed");
    expect(t.status.refusal?.code).toBe("denied");
  });
});
