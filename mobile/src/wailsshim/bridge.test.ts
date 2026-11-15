import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ShimBridge, FALLBACK_POLL_MS } from "./bridge";

const POLL_MS = 50;
import { ChannelTransport } from "./channeltransport";
import { LinkError } from "./pluginerror";
import { copy } from "@mobile/lib/copy";
import { WireRefusalError } from "@mobile/wire/correlator";
import { On, OffAll } from "./events";
import { watches } from "./watch";
import {
  FRAME_PTY,
  FRAME_RESP,
  FRAME_RESYNC,
  FakeChannel,
  type Frame,
} from "../wire";
import { FakeLink } from "./fakelink";

const PANE = "lola-fe-42";

type Daemon = {
  answers: Record<string, unknown>;
  /** Commands whose `watch` result is refused. */
  refuseWatch: Set<string>;
  ids: Map<string, string>;
};

/** A scripted daemon: answers requests, watches and a pane sub. */
function script(ch: FakeChannel, d: Daemon) {
  ch.onSend = (f: Frame) => {
    if (f.type === "req" && f.cmd === "watch") {
      const topics = (f.payload as { args: { topics: string[] } }).args.topics;
      for (const t of topics) d.ids.set(t, f.id!);
      ch.deliver({
        v: 2,
        type: FRAME_RESP,
        id: f.id,
        payload: {
          ok: true,
          data: { topics: topics.map((topic) => (d.refuseWatch.has(topic) ? { topic, ok: false, code: "limit" } : { topic, ok: true })) },
        },
      });
      return;
    }
    if (f.type === "req") {
      ch.deliver({ v: 2, type: FRAME_RESP, id: f.id, payload: { ok: true, data: d.answers[f.cmd ?? ""] } });
      return;
    }
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
}

async function wired(answers: Record<string, unknown> = {}, refuseWatch: string[] = []) {
  const ch = new FakeChannel();
  const d: Daemon = { answers, refuseWatch: new Set(refuseWatch), ids: new Map() };
  script(ch, d);
  const t = new ChannelTransport({ open: async () => ch });
  await t.connect({ daemonId: "d" });
  const b = new ShimBridge({ fallbackPollMs: POLL_MS });
  b.installTransport(t);
  return { b, ch, t, d };
}

const row = (id: string, extra: Record<string, unknown> = {}) => ({ id, status: "working", ...extra });
const settle = () => new Promise((r) => setTimeout(r, 40));

beforeEach(() => OffAll());
afterEach(() => OffAll());

describe("pushed global state", () => {
  it("watches sessions, projects and status once connected", async () => {
    const { b, ch } = await wired();
    b.start();
    await settle();
    const watch = ch.sent.filter((f) => f.cmd === "watch");
    expect(watch).toHaveLength(1);
    expect((watch[0].payload as { args: { topics: string[] } }).args.topics).toEqual(["sessions", "projects", "status"]);
    b.stop();
  });

  it("republishes projects and status as the daemon:* events the store subscribes to", async () => {
    const { b, ch, d } = await wired();
    const seen: Record<string, unknown> = {};
    On("daemon:projects", (e) => (seen.projects = e.data));
    On("daemon:status", (e) => (seen.status = e.data));
    b.start();
    await settle();
    ch.deliverEvent(d.ids.get("projects")!, "projects", 1, { projects: [{ name: "p" }] });
    ch.deliverEvent(d.ids.get("status")!, "status", 1, { host: "mac" });
    expect(seen).toEqual({ projects: { projects: [{ name: "p" }] }, status: { host: "mac" } });
    b.stop();
  });

  it("keeps sessions keyed: a reset replaces, a delta upserts and removes, events persist", async () => {
    const { b, ch, d } = await wired();
    const out: { sessions: { id: string }[]; events: unknown[] }[] = [];
    On("daemon:sessions", (e) => out.push(e.data));
    b.start();
    await settle();
    const id = d.ids.get("sessions")!;
    ch.deliverEvent(id, "sessions", 1, { reset: true, upsert: [row("a"), row("b")], events: [{ id: "a", to: "idle" }] });
    await settle();
    ch.deliverEvent(id, "sessions", 2, { upsert: [row("c")], remove: ["a"] });
    await settle();
    expect(out.at(-1)!.sessions.map((s) => s.id)).toEqual(["b", "c"]);
    // A delta carries no `events`, and Activity must not be cleared by it.
    expect(out.at(-1)!.events).toEqual([{ id: "a", to: "idle" }]);
    b.stop();
  });

  it("keeps the identity of a row a delta did not touch", async () => {
    const { b, ch, d } = await wired();
    const out: { sessions: object[] }[] = [];
    On("daemon:sessions", (e) => out.push(e.data));
    b.start();
    await settle();
    const id = d.ids.get("sessions")!;
    ch.deliverEvent(id, "sessions", 1, { reset: true, upsert: [row("a"), row("b")] });
    await settle();
    ch.deliverEvent(id, "sessions", 2, { upsert: [row("b", { status: "idle" })] });
    await settle();
    expect(out.at(-1)!.sessions[0]).toBe(out.at(-2)!.sessions[0]);
    expect(out.at(-1)!.sessions[1]).not.toBe(out.at(-2)!.sessions[1]);
    b.stop();
  });

  it("emits one daemon:sessions per animation frame however many events arrive", async () => {
    const { b, ch, d } = await wired();
    let n = 0;
    On("daemon:sessions", () => n++);
    b.start();
    await settle();
    const id = d.ids.get("sessions")!;
    ch.deliverEvent(id, "sessions", 1, { reset: true, upsert: [row("a")] });
    ch.deliverEvent(id, "sessions", 2, { upsert: [row("b")] });
    ch.deliverEvent(id, "sessions", 3, { upsert: [row("c")] });
    await settle();
    expect(n).toBe(1);
    b.stop();
  });

  it("fetches an omitted row with one `sessions` request", async () => {
    const { b, ch, d } = await wired({ sessions: { sessions: [row("big", { big: true })] } });
    const out: { sessions: { id: string }[] }[] = [];
    On("daemon:sessions", (e) => out.push(e.data));
    b.start();
    await settle();
    ch.deliverEvent(d.ids.get("sessions")!, "sessions", 1, { reset: true, upsert: [row("a")], omitted: ["big"] });
    await settle();
    expect(ch.sent.filter((f) => f.cmd === "sessions")).toHaveLength(1);
    expect(out.at(-1)!.sessions.map((s) => s.id).sort()).toEqual(["a", "big"]);
    b.stop();
  });

  it("emits no daemon:pusherr, ever", async () => {
    const { b } = await wired({}, ["sessions"]);
    const errs: unknown[] = [];
    On("daemon:pusherr", (e) => errs.push(e.data));
    b.start();
    await settle();
    b.stop();
    expect(errs).toEqual([]);
  });
});

describe("a topic the daemon refuses", () => {
  it("is polled on the fallback interval while connected, until the next epoch", async () => {
    const { b, ch } = await wired({ projects: { projects: [] } }, ["projects"]);
    const seen: unknown[] = [];
    On("daemon:projects", (e) => seen.push(e.data));
    b.start();
    await new Promise((r) => setTimeout(r, POLL_MS * 4));
    expect(ch.sent.filter((f) => f.cmd === "projects").length).toBeGreaterThanOrEqual(2);
    expect(seen.length).toBeGreaterThanOrEqual(2);
    await b.onEpoch();
    const n = ch.sent.filter((f) => f.cmd === "projects").length;
    await new Promise((r) => setTimeout(r, POLL_MS * 4));
    expect(ch.sent.filter((f) => f.cmd === "projects").length).toBe(n);
    b.stop();
  });
});

describe("a refused topic that is watched again", () => {
  it("stops polling once the topic delivers", async () => {
    const { b, ch, d } = await wired({ projects: { projects: [] } }, ["projects"]);
    b.start();
    await new Promise((r) => setTimeout(r, POLL_MS * 2));
    expect(ch.sent.filter((f) => f.cmd === "projects").length).toBeGreaterThanOrEqual(1);
    d.refuseWatch.delete("projects");
    watches.resume();
    await settle();
    ch.deliverEvent(d.ids.get("projects")!, "projects", 1, { projects: [{ name: "p" }] });
    const n = ch.sent.filter((f) => f.cmd === "projects").length;
    await new Promise((r) => setTimeout(r, POLL_MS * 4));
    expect(ch.sent.filter((f) => f.cmd === "projects").length).toBe(n);
    b.stop();
  });
});

describe("defaults", () => {
  it("polls a refused topic every two seconds", () => {
    expect(FALLBACK_POLL_MS).toBe(2000);
  });
});

describe("requests", () => {
  it("rejects at once while the page is hidden", async () => {
    const { b, ch } = await wired({ status: {} });
    const spy = vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    try {
      await expect(b.request("DaemonService.Status", "status")).rejects.toMatchObject({ name: "ShimNotConnectedError" });
      expect(ch.sent.filter((f) => f.cmd === "status")).toHaveLength(0);
    } finally {
      spy.mockRestore();
    }
  });

  it("rejects with the not-connected error naming the call when no transport is installed", async () => {
    const b = new ShimBridge();
    await expect(b.request("DaemonService.Status", "status")).rejects.toMatchObject({ name: "ShimNotConnectedError" });
  });
});

describe("refusals speak a sentence", () => {
  async function rejected(err: Error): Promise<Error> {
    const { b, t } = await wired({});
    vi.spyOn(t, "request").mockRejectedValue(err);
    return (await b.request("X.Y", "x").catch((e: unknown) => e)) as Error;
  }

  it("keeps the SAME error object, its class and its code", async () => {
    const orig = new WireRefusalError("unknown_cmd", "");
    const got = await rejected(orig);
    expect(got).toBe(orig);
    expect(got).toBeInstanceOf(WireRefusalError);
    expect((got as WireRefusalError).code).toBe("unknown_cmd");
  });

  it("shows only the sentence in String(e) and e.message", async () => {
    const e = await rejected(new WireRefusalError("unknown_cmd", ""));
    expect(String(e)).toBe(copy.refusals.update);
    expect(e.message).toBe(copy.refusals.update);
  });

  it("denied with a message shows it; without, the no-tier sentence", async () => {
    const a = await rejected(new WireRefusalError("denied", "Ask on your Mac."));
    expect(String(a)).toBe("Ask on your Mac.");
    const b = await rejected(new WireRefusalError("denied", ""));
    expect(String(b)).toBe(copy.notices.noTier.body);
  });

  it("a generic code shows its message part, never the code", async () => {
    const e = await rejected(new WireRefusalError("bad_args", "Pick a project."));
    expect(String(e)).toBe("Pick a project.");
    expect(e.message).not.toContain("bad_args");
  });

  it("the not-connected rejections say offline too", async () => {
    const b = new ShimBridge();
    const e = (await b.request("X.Y", "x").catch((err: unknown) => err)) as Error;
    expect(e.name).toBe("ShimNotConnectedError");
    expect(String(e)).toBe(copy.refusals.offline);
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
    ch.deliver({ v: 2, type: FRAME_PTY, pane: PANE, seq: 2, payload: { data: "aGk=" } });
    expect(chunks).toEqual(["aGk="]);
  });

  it("swallows an empty flush instead of writing zero bytes to the terminal", async () => {
    const { b, ch } = await wired();
    await b.attachPane(PANE, 55, 30);
    const chunks: string[] = [];
    On<string>(`pty:${PANE}`, (e) => chunks.push(e.data));
    ch.deliver({ v: 2, type: FRAME_PTY, pane: PANE, seq: 2, payload: { data: null } });
    expect(chunks).toEqual([]);
  });

  it("emits pty:<name>:exit when the pane dies, after painting its last screen", async () => {
    const { b, ch } = await wired();
    await b.attachPane(PANE, 55, 30);
    const events: string[] = [];
    On(`pty:${PANE}`, () => events.push("paint"));
    On(`pty:${PANE}:exit`, () => events.push("exit"));
    ch.deliver({
      v: 2,
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
    ch.deliver({ v: 2, type: FRAME_PTY, pane: PANE, seq: 7, payload: { data: "aGk=" } });
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
    ch.deliver({ v: 2, type: FRAME_PTY, pane: PANE, seq: 2, payload: { data: "aGk=" } });
    expect(chunks).toEqual([]);
  });

  it("drops every pane binding when the transport is replaced", async () => {
    const { b } = await wired();
    await b.attachPane(PANE, 55, 30);
    expect(b.paneSubscription(PANE)).toBeDefined();
    b.installTransport(null);
    expect(b.paneSubscription(PANE)).toBeUndefined();
  });

  it("rejects an attach with no transport, naming the call the component made", async () => {
    const b = new ShimBridge();
    await expect(b.attachPane(PANE, 80, 24)).rejects.toMatchObject({
      name: "ShimNotConnectedError",
    });
  });
});


describe("pane bindings across epochs", () => {
  async function linkedBridge() {
    const ch = new FakeLink();
    const d: Daemon = { answers: {}, refuseWatch: new Set(), ids: new Map() };
    script(ch, d);
    const t = new ChannelTransport({ open: async () => ch });
    await t.connect({ daemonId: "d" });
    const b = new ShimBridge();
    b.installTransport(t);
    return { b, ch, t };
  }

  it("re-subscribes a bound pane with its viewport when the next epoch is ready, without reporting an exit", async () => {
    const { b, ch } = await linkedBridge();
    await b.attachPane(PANE, 55, 30);
    const exits: unknown[] = [];
    On(`pty:${PANE}:exit`, () => exits.push(1));
    ch.raiseDown(new LinkError("peer_closed", "x"));
    expect(b.paneSubscription(PANE)).toBeUndefined();
    ch.raiseUp({ epoch: 2 });
    await b.onEpoch();
    expect(b.paneSubscription(PANE)).toBeDefined();
    const subs = ch.sent.filter((f) => f.type === "sub");
    expect(subs).toHaveLength(2);
    expect(subs[1].payload).toEqual({ cols: 55, rows: 30 });
    expect(exits).toEqual([]);
  });

  it("re-subscribes at the size of the last resize, also one made between epochs", async () => {
    const { b, ch } = await linkedBridge();
    await b.attachPane(PANE, 55, 30);
    await b.resizePane(PANE, 60, 32);
    ch.raiseDown(new LinkError("peer_closed", "x"));
    await b.resizePane(PANE, 70, 40);
    ch.raiseUp({ epoch: 2 });
    await b.onEpoch();
    const subs = ch.sent.filter((f) => f.type === "sub");
    expect(subs).toHaveLength(2);
    expect(subs[1].payload).toEqual({ cols: 70, rows: 40 });
  });

  it("does not bring back a pane that was detached", async () => {
    const { b, ch } = await linkedBridge();
    await b.attachPane(PANE, 55, 30);
    await b.detachPane(PANE);
    ch.raiseDown(new LinkError("peer_closed", "x"));
    ch.raiseUp({ epoch: 2 });
    await b.onEpoch();
    expect(ch.sent.filter((f) => f.type === "sub")).toHaveLength(1);
  });
});
