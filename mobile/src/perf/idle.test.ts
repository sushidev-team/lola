// Idle cost (spec §8.1): with the Inbox and a Conversation mounted, the pushed
// streams delivered their resets and nothing changing, the phone sends no
// request for two minutes. Every poll the desktop stores own is replaced by the
// push source, so a single `req` frame here is a poll that survived.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render } from "@testing-library/svelte";

vi.mock("@mobile/native", () => ({ native: null, keyboard: null, browser: null, transport: { haptic: async () => {}, addListener: async () => ({ remove() {} }) } }));

import Inbox from "../screens/Inbox.svelte";
import ConversationSegment from "../screens/session/ConversationSegment.svelte";
import { inbox } from "../lib/inbox.svelte";
import { mobilePush } from "../lib/pushsource";
import { router } from "../lib/router.svelte";
import { bridge } from "../wailsshim/bridge";
import { ChannelTransport } from "../wailsshim/channeltransport";
import { OffAll } from "../wailsshim/events";
import { FRAME_RESP, FakeChannel, type Frame } from "../wire";
import { setPushSource } from "$lib/pushsource";
import { store, type SessionInfo } from "$lib/store.svelte";
import NavHarness from "./NavHarness.test.svelte";
import type { Seg } from "../lib/router.svelte";
import { tiers } from "../lib/tiers.svelte";
import { report } from "./harness";

const SESSION = "s1";
const IDLE_MS = 120_000;

const session = {
  id: SESSION,
  project: "p",
  issue: "NOR-1",
  title: "Email ingest",
  branch: "b",
  agentState: "idle",
  delivery: "none",
  age: "1m",
  prNumber: 0,
  worktree: "/w/s1",
  tmuxName: "t",
} as SessionInfo;

let ch: FakeChannel;
const ids = new Map<string, string>();

beforeEach(async () => {
  vi.useFakeTimers();
  OffAll();
  ids.clear();
  ch = new FakeChannel();
  ch.onSend = (f: Frame) => {
    if (f.type !== "req") return;
    if (f.cmd === "watch") {
      const topics = (f.payload as { args: { topics: string[] } }).args.topics;
      for (const t of topics) ids.set(t, f.id!);
      ch.deliver({
        v: 2,
        type: FRAME_RESP,
        id: f.id,
        payload: { ok: true, data: { topics: topics.map((topic) => ({ topic, ok: true })) } },
      });
      return;
    }
    // A diff answer has its required fields, or DiffView throws on a shape no daemon sends.
    const data = f.cmd === "diff" ? { base: "main", mergeBase: "0123456789ab", files: [] } : {};
    ch.deliver({ v: 2, type: FRAME_RESP, id: f.id, payload: { ok: true, data } });
  };
  const t = new ChannelTransport({ open: async () => ch });
  await t.connect({ daemonId: "d" });
  // The shared bridge is the one the services and the push source use.
  bridge.installTransport(t);
  setPushSource(mobilePush);
  bridge.start();
  store.sessions = [session];
  inbox.ingest([session]);
  router.stack = [{ name: "tab", tab: "inbox" }, { name: "session", id: SESSION, seg: "conversation" }];
  router.sheet = "";
});

afterEach(() => {
  cleanup();
  bridge.stop();
  bridge.installTransport(null);
  setPushSource(null);
  OffAll();
  vi.useRealTimers();
});

const reqs = () => ch.sent.filter((f) => f.type === "req");

describe("idle cost", () => {
  it("sends no request in 120 s with the Inbox and a Conversation mounted", async () => {
    render(Inbox);
    render(ConversationSegment, { props: { id: SESSION } });
    await vi.advanceTimersByTimeAsync(2000);

    // Every stream the mounted screens follow is acknowledged and has delivered
    // its first state.
    for (const topic of [`talk:${SESSION}`, `feed:${SESSION}`, `timeline:${SESSION}`]) {
      expect(ids.has(topic), topic).toBe(true);
    }
    ch.deliverEvent(ids.get("sessions")!, "sessions", 1, { reset: true, upsert: [session] });
    ch.deliverEvent(ids.get(`talk:${SESSION}`)!, `talk:${SESSION}`, 1, {
      session: SESSION,
      caps: { feed: true, send: true, stop: true, cards: true },
      composer: "resting",
      feedSeq: 1,
      health: "ok",
    });
    ch.deliverEvent(ids.get(`feed:${SESSION}`)!, `feed:${SESSION}`, 1, {
      reset: true,
      older: false,
      session: SESSION,
      epoch: "e1",
      lastSeq: 1,
      health: "ok",
      items: [],
    });
    ch.deliverEvent(ids.get(`timeline:${SESSION}`)!, `timeline:${SESSION}`, 1, {
      reset: true,
      session: SESSION,
      items: [],
      lastSeq: 0,
    });
    await vi.advanceTimersByTimeAsync(2000);

    const quiet = reqs().length;
    await vi.advanceTimersByTimeAsync(IDLE_MS);
    expect(reqs().slice(quiet)).toEqual([]);
  });
});

// The same budget for the whole Session screen as the App mounts it (Inbox under,
// Session with its header and segment bar over), on every segment, over the
// 30 s of the plan's budget (docs/mobile-rethink.md §8). The Changes, Review and
// Terminal segments read their data once on arrival; after that a quiet session
// must cost nothing.

describe("idle cost, Session screen", () => {
  const busy = { ...session, prNumber: 7 } as SessionInfo;
  for (const seg of ["conversation", "changes", "review", "terminal"] as Seg[]) {
    it(`sends no request in 30 s on the ${seg} segment`, async () => {
      class NoopResizeObserver {
        observe(): void {}
        unobserve(): void {}
        disconnect(): void {}
      }
      (globalThis as { ResizeObserver?: unknown }).ResizeObserver ??= NoopResizeObserver;
      tiers.set(["read", "answer", "control", "shell"]);
      store.sessions = [busy];
      store.alive = true;
      inbox.ingest([busy]);
      router.stack = [{ name: "tab", tab: "inbox" }, { name: "session", id: SESSION, seg }];
      render(NavHarness);
      await vi.advanceTimersByTimeAsync(2000);
      if (ids.has("sessions")) ch.deliverEvent(ids.get("sessions")!, "sessions", 1, { reset: true, upsert: [busy] });
      for (const topic of [`talk:${SESSION}`, `feed:${SESSION}`, `timeline:${SESSION}`, `diff:${SESSION}`]) {
        if (!ids.has(topic)) continue;
        const reset =
          topic.startsWith("talk:")
            ? { session: SESSION, caps: { feed: true, send: true, stop: true, cards: true }, composer: "resting", feedSeq: 1, health: "ok" }
            : topic.startsWith("feed:")
              ? { reset: true, older: false, session: SESSION, epoch: "e1", lastSeq: 1, health: "ok", items: [] }
              : topic.startsWith("diff:")
                ? { session: SESSION, rev: 1 }
                : { reset: true, session: SESSION, items: [], lastSeq: 0 };
        ch.deliverEvent(ids.get(topic)!, topic, 1, reset);
      }
      await vi.advanceTimersByTimeAsync(5000);
      const quiet = reqs().length;
      await vi.advanceTimersByTimeAsync(30_000);
      const extra = reqs().slice(quiet).map((f) => f.cmd);
      report(`idle seg=${seg} warmup=${quiet} (${reqs().slice(0, quiet).map((f) => f.cmd).join(",")}) idle30s=${extra.length} ${extra.join(",")}`);
      expect(extra).toEqual([]);
    });
  }
});
