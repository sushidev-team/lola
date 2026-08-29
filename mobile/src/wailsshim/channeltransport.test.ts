import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ChannelTransport } from "./channeltransport";
import {
  FRAME_ERR,
  FRAME_PTY,
  FRAME_RESP,
  FRAME_RESYNC,
  FRAME_VERSION_CURRENT,
  FakeChannel,
  WireRefusalError,
  base64ToBytes,
  type Endpoint,
  type Frame,
  type PaneEvent,
} from "../wire";

const PANE = "lola-fe-42";
const endpoint: Endpoint = { host: "127.0.0.1", spkiPin: "pin", insecureKey: "0123456789abcdef" };

function ok(id: string, data?: unknown): Frame {
  return { v: FRAME_VERSION_CURRENT, type: FRAME_RESP, id, payload: { ok: true, data } };
}

/** A transport whose channel answers the bearer hello and nothing else. */
async function connected(): Promise<{ t: ChannelTransport; ch: FakeChannel }> {
  const ch = new FakeChannel();
  ch.onSend = (f) => {
    if (f.cmd === "remote.hello") ch.deliver(ok(f.id!));
  };
  const t = new ChannelTransport({ open: async () => ch });
  await t.connect(endpoint);
  return { t, ch };
}

describe("connecting", () => {
  it("sends the bearer hello as the FIRST frame, outside the request path", async () => {
    // remote.hello is on the daemon's denial list (the whole `remote.` prefix
    // is), so it cannot go through request(); and its payload is {key}, not a
    // protocol.Request. It is also required to be the connection's first frame.
    const { ch } = await connected();
    expect(ch.sent).toHaveLength(1);
    expect(ch.sent[0]).toMatchObject({
      v: 1,
      type: "req",
      cmd: "remote.hello",
      payload: { key: "0123456789abcdef" },
    });
  });

  it("reaches `ready` only after the hello is acknowledged", async () => {
    const { t } = await connected();
    expect(t.status.phase).toBe("ready");
  });

  it("closes and reports the refusal when the key is wrong", async () => {
    const ch = new FakeChannel();
    ch.onSend = (f) =>
      ch.deliver({
        v: 1,
        type: FRAME_ERR,
        id: f.id,
        payload: { code: "denied", message: "authenticate first" },
      });
    const t = new ChannelTransport({ open: async () => ch });
    await expect(t.connect(endpoint)).rejects.toBeInstanceOf(WireRefusalError);
    expect(t.status.phase).toBe("closed");
  });

  it("sends NO hello when the channel already authenticated (the native plugin's case)", async () => {
    // The plugin's own connect() only resolves once the daemon accepted the
    // key. A second hello would arrive as an ordinary req naming a `remote.`
    // command on an authenticated connection, which is denied — and a denied
    // command is fatal: one err frame and the socket closes.
    const ch = new FakeChannel();
    const t = new ChannelTransport({ open: async () => ch, handshake: "channel" });
    await t.connect(endpoint); // endpoint DOES carry an insecureKey
    expect(ch.sent).toHaveLength(0);
    expect(t.status.phase).toBe("ready");
  });

  it("never sends the bearer key when the endpoint carries none", async () => {
    const ch = new FakeChannel();
    const t = new ChannelTransport({ open: async () => ch });
    await t.connect({ host: "h", spkiPin: "p" });
    expect(ch.sent).toHaveLength(0);
    expect(t.status.phase).toBe("ready");
  });
});

describe("requests", () => {
  it("turns a command into a req frame carrying cmd on the envelope AND the payload", async () => {
    const { t, ch } = await connected();
    ch.onSend = (f) => {
      if (f.type === "req") ch.deliver(ok(f.id!, { sessions: [] }));
    };
    await expect(t.request("sessions")).resolves.toEqual({ sessions: [] });
    expect(ch.last).toMatchObject({
      v: 1,
      type: "req",
      cmd: "sessions",
      payload: { cmd: "sessions" },
    });
  });

  it("refuses a DENIED command locally instead of putting it on the wire", async () => {
    // A denied command is FATAL daemon-side: one err frame and the socket
    // closes, taking every live pane subscription with it. One mistyped
    // command would otherwise cost a full reconnect and re-subscribe.
    const { t, ch } = await connected();
    const before = ch.sent.length;
    await expect(t.request("reload")).rejects.toMatchObject({ code: "unknown_cmd" });
    await expect(t.request("hookEvent")).rejects.toMatchObject({ code: "unknown_cmd" });
    await expect(t.request("")).rejects.toMatchObject({ code: "unknown_cmd" });
    expect(ch.sent.length).toBe(before);
    expect(t.status.phase).toBe("ready"); // the connection is untouched
  });

  it("rejects with the daemon's own error text on ok:false", async () => {
    const { t, ch } = await connected();
    ch.onSend = (f) => {
      if (f.type === "req") {
        ch.deliver({ v: 1, type: FRAME_RESP, id: f.id, payload: { ok: false, error: "no such session" } });
      }
    };
    await expect(t.request("kill", { session: "x" })).rejects.toThrow(/no such session/);
  });
});

describe("pane subscriptions", () => {
  /** Connect, then subscribe with a scripted resync acknowledgement. */
  async function subscribed() {
    const { t, ch } = await connected();
    ch.onSend = (f) => {
      if (f.type === "sub") {
        ch.deliver({
          v: 1,
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
    ch.deliver({ v: 1, type: FRAME_PTY, pane: PANE, seq: 2, payload: { data: "aGk=" } });
    ch.deliver({ v: 1, type: FRAME_PTY, pane: PANE, seq: 3, payload: { data: null } });
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
      v: 1,
      type: FRAME_RESYNC,
      pane: PANE,
      seq: 9,
      payload: { cols: 120, rows: 40, cursorX: 0, cursorY: 0 },
    });
    ch.deliver({ v: 1, type: FRAME_PTY, pane: PANE, seq: 40, payload: { data: "" } });
    expect(events[0]).toMatchObject({ kind: "resync", repaired: true });
    expect(events[1]).toMatchObject({ kind: "output", gap: true });
  });

  it("reports a pane death, which arrives as a resync rather than its own type", async () => {
    const { ch, sub } = await subscribed();
    const events: PaneEvent[] = [];
    sub.onEvent((e) => events.push(e));
    ch.deliver({
      v: 1,
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
      v: 1,
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
    expect(ch.last).toEqual({ v: 1, type: "unsub", pane: PANE });
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
    ch.deliver({ v: 1, type: "sub", pane: PANE }); // client -> daemon only
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
    ch.deliver({ v: 1, type: FRAME_ERR, payload: { code: "frame_too_large" } });
    expect(t.status.phase).toBe("closed");
  });

  it("tears down on a fatal code even when it settled a pending request", async () => {
    // Every fatal refusal the daemon actually produces — `denied`,
    // `unknown_cmd`, `unsupported_version` — is written in reply to a frame the
    // client sent, so it carries that frame's id and the correlator settles it.
    // Checking `alwaysFatalCode` only for the uncorrelated remainder made the
    // check dead for exactly the codes it exists to catch, and the client then
    // learned the socket was gone only when the close arrived.
    const { t, ch } = await connected();
    const p = t.request("sessions");
    ch.deliver({ v: 1, type: FRAME_ERR, id: ch.last!.id, payload: { code: "denied" } });

    // The pending request still rejects with its own specific refusal...
    await expect(p).rejects.toMatchObject({ code: "denied" });
    // ...and the connection is gone, without waiting for the socket to notice.
    expect(t.status.phase).toBe("closed");
  });

  it("leaves the connection up for a non-fatal refusal on a request", async () => {
    const { t, ch } = await connected();
    const p = t.request("sessions");
    ch.deliver({ v: 1, type: FRAME_ERR, id: ch.last!.id, payload: { code: "rate_limited" } });
    await expect(p).rejects.toMatchObject({ code: "rate_limited" });
    expect(t.status.phase).toBe("ready");
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
      if (f.cmd === "remote.hello") ch.deliver(ok(f.id!));
      if (f.type === "sub") {
        ch.deliver({ v: 1, type: FRAME_ERR, id: f.id, pane: f.pane, payload: { code: "unknown_pane" } });
      }
    };
    const t = new ChannelTransport({ open: async () => ch });
    await t.connect(endpoint);

    await expect(t.subscribe(PANE)).rejects.toMatchObject({ code: "unknown_pane" });
    expect(ch.sent.filter((f) => f.type === "unsub")).toHaveLength(0);
  });
});

describe("a refusal that arrives instead of a channel", () => {
  // The native plugin performs the bearer handshake itself, so a wrong key
  // never reaches the frame layer: `connect` simply rejects. If that rejection
  // only lands in `status.error`, `diagnose` finds no refusal, falls through to
  // its silence branch and tells the user their phone is not on the Mac's
  // network — with the address, the port and the pin all correct.

  it("records a WireRefusalError from the channel factory as status.refusal", async () => {
    const refusal = new WireRefusalError("denied", "the daemon refused the connection");
    const t = new ChannelTransport({
      open: async () => {
        throw refusal;
      },
      handshake: "channel",
    });

    await expect(t.connect(endpoint)).rejects.toBe(refusal);
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
      handshake: "channel",
    });
    await expect(t.connect(endpoint)).rejects.toBe(refusal);
    expect(t.status.refusal?.minV).toBe(2);
    expect(t.status.refusal?.maxV).toBe(3);
  });

  it("leaves an ordinary transport failure without a refusal", async () => {
    const boom = new Error("the connection timed out");
    const t = new ChannelTransport({
      open: async () => {
        throw boom;
      },
      handshake: "channel",
    });
    await expect(t.connect(endpoint)).rejects.toBe(boom);
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
      handshake: "channel",
    });
    await t.connect(endpoint);
    expect(t.status.phase).toBe("ready");

    onClose?.(new WireRefusalError("denied", "the daemon refused the connection"));
    expect(t.status.phase).toBe("closed");
    expect(t.status.refusal?.code).toBe("denied");
  });
});
