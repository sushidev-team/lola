import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ShimBridge } from "./bridge";
import { ChannelTransport } from "./channeltransport";
import { On, OffAll } from "./events";
import {
  FRAME_PTY,
  FRAME_RESP,
  FRAME_RESYNC,
  FakeChannel,
  type Endpoint,
  type Frame,
} from "../wire";

const PANE = "lola-fe-42";
const endpoint: Endpoint = { host: "127.0.0.1", spkiPin: "pin" };

/** A scripted daemon: answers `sessions`/`projects`/`status` and a pane sub. */
function daemon(ch: FakeChannel, answers: Record<string, unknown>, fails = new Set<string>()) {
  ch.onSend = (f: Frame) => {
    if (f.type === "req") {
      const cmd = f.cmd ?? "";
      if (fails.has(cmd)) {
        ch.deliver({ v: 1, type: FRAME_RESP, id: f.id, payload: { ok: false, error: `${cmd} failed` } });
        return;
      }
      ch.deliver({ v: 1, type: FRAME_RESP, id: f.id, payload: { ok: true, data: answers[cmd] } });
      return;
    }
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
}

async function wired(answers: Record<string, unknown> = {}, fails?: Set<string>) {
  const ch = new FakeChannel();
  daemon(ch, answers, fails);
  const t = new ChannelTransport({ open: async () => ch });
  await t.connect(endpoint);
  const b = new ShimBridge({ pauseWhenHidden: false });
  b.installTransport(t);
  return { b, ch, t };
}

beforeEach(() => OffAll());
afterEach(() => OffAll());

describe("the synthesised push loop", () => {
  it("turns polled answers into the daemon:* events store.svelte.ts subscribes to", async () => {
    // There is no server push in the remote protocol: no session frame, no
    // status frame. The event SHAPE is defined by desktop/main.go's pushLoop,
    // and this is the port of it.
    const { b } = await wired({
      sessions: { sessions: [{ id: "s1" }], events: [] },
      projects: { projects: [{ name: "p" }], groups: [] },
      status: { polls: [] },
    });
    const got: Record<string, unknown> = {};
    On("daemon:sessions", (e) => (got.sessions = e.data));
    On("daemon:projects", (e) => (got.projects = e.data));
    On("daemon:status", (e) => (got.status = e.data));

    await b.pollOnce();

    expect(got.sessions).toEqual({ sessions: [{ id: "s1" }], events: [] });
    expect(got.projects).toEqual({ projects: [{ name: "p" }], groups: [] });
    expect(got.status).toEqual({ polls: [] });
  });

  it("polls projects and status on every OTHER tick, as the desktop does", async () => {
    const { b, ch } = await wired({ sessions: {}, projects: {}, status: {} });
    await b.pollOnce();
    await b.pollOnce();
    const cmds = ch.sent.filter((f) => f.type === "req").map((f) => f.cmd);
    expect(cmds.filter((c) => c === "sessions")).toHaveLength(2);
    expect(cmds.filter((c) => c === "projects")).toHaveLength(1);
  });

  it("emits daemon:alive only on a CHANGE", async () => {
    const { b, t } = await wired({ sessions: {}, projects: {}, status: {} });
    const seen: boolean[] = [];
    On<boolean>("daemon:alive", (e) => seen.push(e.data));
    await b.pollOnce();
    await b.pollOnce();
    await t.disconnect();
    await b.pollOnce();
    expect(seen).toEqual([false]); // installTransport already announced `true`
  });

  it("emits daemon:pusherr only on a change, recovery included", async () => {
    // The banner it raises is dismissible, so re-emitting the same failure
    // every two seconds would resurrect a banner the user just dismissed.
    const fails = new Set(["sessions"]);
    const { b } = await wired({ sessions: {}, projects: {}, status: {} }, fails);
    const seen: { cmd: string; msg: string }[] = [];
    On<{ cmd: string; msg: string }>("daemon:pusherr", (e) => seen.push(e.data));

    await b.pollOnce();
    await b.pollOnce();
    expect(seen).toHaveLength(1);
    expect(seen[0].cmd).toBe("sessions");
    expect(seen[0].msg).toMatch(/sessions failed/);

    fails.delete("sessions");
    await b.pollOnce();
    expect(seen).toHaveLength(2);
    expect(seen[1]).toEqual({ cmd: "sessions", msg: "" }); // recovery
  });

  it("reports the daemon unreachable, and emits no error, with no transport", async () => {
    const b = new ShimBridge({ pauseWhenHidden: false });
    const seen: unknown[] = [];
    On("daemon:pusherr", (e) => seen.push(e.data));
    On<boolean>("daemon:alive", (e) => seen.push(e.data));
    await b.pollOnce();
    expect(b.alive()).toBe(false);
    expect(seen).toEqual([false]); // alive:false, and no pusherr — see pollOnce
  });

  it("drops an overlapping tick rather than stacking requests", async () => {
    // The daemon answers a fifth concurrent request with a rate_limited
    // refusal, so a slow link must not be able to stack polls into one.
    const ch = new FakeChannel();
    const held: Frame[] = [];
    ch.onSend = (f) => held.push(f);
    const t = new ChannelTransport({ open: async () => ch });
    await t.connect(endpoint);
    const b = new ShimBridge({ pauseWhenHidden: false });
    b.installTransport(t);

    void b.pollOnce();
    await b.pollOnce();
    expect(held.filter((f) => f.cmd === "sessions")).toHaveLength(1);
  });
});

describe("the pane bridge", () => {
  it("republishes the acknowledging resync on pty:<name> as a repaint", async () => {
    // LiveTerminal.svelte only ever calls term.write(bytes). The initial screen
    // arrives as a STRUCTURED frame over this protocol, so it is rendered back
    // into an escape sequence and pushed down the same channel — which is what
    // lets the component be reused without an edit.
    const { b } = await wired();
    const chunks: string[] = [];
    On<string>(`pty:${PANE}`, (e) => chunks.push(e.data));
    await b.attachPane(PANE, 55, 30);
    expect(chunks).toHaveLength(1);
    const painted = new TextDecoder().decode(
      Uint8Array.from(atob(chunks[0]), (c) => c.charCodeAt(0)),
    );
    expect(painted).toContain("\x1b[?1049h"); // the pane is on the alternate screen
    expect(painted).toContain("ready");
  });

  it("republishes pty output as base64 on the same channel", async () => {
    const { b, ch } = await wired();
    await b.attachPane(PANE, 55, 30);
    const chunks: string[] = [];
    On<string>(`pty:${PANE}`, (e) => chunks.push(e.data));
    ch.deliver({ v: 1, type: FRAME_PTY, pane: PANE, seq: 2, payload: { data: "aGk=" } });
    expect(chunks).toEqual(["aGk="]);
  });

  it("swallows an empty flush instead of writing zero bytes to the terminal", async () => {
    const { b, ch } = await wired();
    await b.attachPane(PANE, 55, 30);
    const chunks: string[] = [];
    On<string>(`pty:${PANE}`, (e) => chunks.push(e.data));
    ch.deliver({ v: 1, type: FRAME_PTY, pane: PANE, seq: 2, payload: { data: null } });
    expect(chunks).toEqual([]);
  });

  it("emits pty:<name>:exit when the pane dies, after painting its last screen", async () => {
    const { b, ch } = await wired();
    await b.attachPane(PANE, 55, 30);
    const events: string[] = [];
    On(`pty:${PANE}`, () => events.push("paint"));
    On(`pty:${PANE}:exit`, () => events.push("exit"));
    ch.deliver({
      v: 1,
      type: FRAME_RESYNC,
      pane: PANE,
      seq: 2,
      payload: { cols: 80, rows: 24, lines: ["done"], cursorX: 0, cursorY: 0, exited: true },
    });
    expect(events).toEqual(["paint", "exit"]);
    expect(b.paneSubscription(PANE)).toBeUndefined();
  });

  it("re-subscribes rather than rendering a torn stream", async () => {
    // A gap on a PTY frame means the bus dropped output for a subscriber that
    // fell behind, and the panebus contract says it cannot be replayed. The
    // component on the other end of pty:<name> has no idea the remote protocol
    // exists, so this layer has to recover for it: a fresh subscription, whose
    // acknowledging resync repaints the whole screen.
    const { b, ch } = await wired();
    await b.attachPane(PANE, 55, 30);
    const first = b.paneSubscription(PANE);

    const chunks: string[] = [];
    On<string>(`pty:${PANE}`, (e) => chunks.push(e.data));
    const subs: Frame[] = [];
    const prior = ch.onSend!;
    ch.onSend = (f, c) => {
      if (f.type === "sub") subs.push(f);
      prior(f, c);
    };

    // seq 7 against a lastSeq of 1: five frames the bus dropped.
    ch.deliver({ v: 1, type: FRAME_PTY, pane: PANE, seq: 7, payload: { data: "aGk=" } });
    // The recovery is a chain of awaits (unsub, subscribe, ack), so wait for its
    // last observable step rather than counting microtasks.
    await vi.waitFor(() => {
      expect(subs).toHaveLength(1); // it re-subscribed
      expect(chunks.length).toBeGreaterThan(0); // and repainted from the new ack
    });

    expect(b.paneSubscription(PANE)).toBeDefined();
    expect(b.paneSubscription(PANE)).not.toBe(first);
    // The torn bytes were dropped; what reached the terminal is a full repaint.
    expect(chunks).not.toContain("aGk=");
    const painted = chunks.map((c) => new TextDecoder().decode(Uint8Array.from(atob(c), (x) => x.charCodeAt(0))));
    expect(painted.some((p) => p.includes("ready"))).toBe(true);
  });

  it("stops republishing once the pane is detached", async () => {
    const { b, ch } = await wired();
    await b.attachPane(PANE, 55, 30);
    const chunks: string[] = [];
    On<string>(`pty:${PANE}`, (e) => chunks.push(e.data));
    await b.detachPane(PANE);
    ch.deliver({ v: 1, type: FRAME_PTY, pane: PANE, seq: 2, payload: { data: "aGk=" } });
    expect(chunks).toEqual([]);
  });

  it("drops every pane binding when the transport is replaced", async () => {
    // Sequence numbers, subscriptions and correlation ids are all properties of
    // one connection; none of them survives a reconnect.
    const { b } = await wired();
    await b.attachPane(PANE, 55, 30);
    expect(b.paneSubscription(PANE)).toBeDefined();
    b.installTransport(null);
    expect(b.paneSubscription(PANE)).toBeUndefined();
  });

  it("rejects an attach with no transport, naming the call the component made", async () => {
    const b = new ShimBridge({ pauseWhenHidden: false });
    await expect(b.attachPane(PANE, 80, 24)).rejects.toMatchObject({
      name: "ShimNotConnectedError",
    });
  });
});

describe("polling lifecycle", () => {
  it("starts, kicks immediately, and stops cleanly", async () => {
    vi.useFakeTimers();
    try {
      const { b, ch } = await wired({ sessions: {}, projects: {}, status: {} });
      b.startPolling();
      await Promise.resolve();
      expect(ch.sent.some((f) => f.cmd === "sessions")).toBe(true);
      b.stopPolling();
      const n = ch.sent.length;
      vi.advanceTimersByTime(10_000);
      expect(ch.sent.length).toBe(n);
    } finally {
      vi.useRealTimers();
    }
  });
});
