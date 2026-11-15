// Send-to-echo (spec §8.1): pressing Send in the Conversation costs one round
// trip. The only request is `agentSay`; the daemon publishes the echo itself on
// `timeline:`/`talk:`, so the phone asks for neither state again.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";

vi.mock("@mobile/native", () => ({ transport: { haptic: async () => {}, addListener: async () => ({ remove() {} }) } }));

import ConversationSegment from "../screens/session/ConversationSegment.svelte";
import { mobilePush } from "../lib/pushsource";
import { router } from "../lib/router.svelte";
import { bridge } from "../wailsshim/bridge";
import { ChannelTransport } from "../wailsshim/channeltransport";
import { OffAll } from "../wailsshim/events";
import { FRAME_RESP, FakeChannel, type Frame } from "../wire";
import { setPushSource } from "$lib/pushsource";
import { store, type SessionInfo } from "$lib/store.svelte";

const SESSION = "s1";
const TEXT = "run the tests";

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
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval", "Date"] });
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
    const data = f.cmd === "agentSay" ? { delivered: true } : {};
    ch.deliver({ v: 2, type: FRAME_RESP, id: f.id, payload: { ok: true, data } });
  };
  const t = new ChannelTransport({ open: async () => ch });
  await t.connect({ daemonId: "d" });
  bridge.installTransport(t);
  setPushSource(mobilePush);
  bridge.start();
  store.sessions = [session];
  store.alive = true;
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

const cmds = () => ch.sent.filter((f) => f.type === "req").map((f) => f.cmd);

describe("send to echo", () => {
  it("sends exactly one agentSay and reads no state back; the echo arrives as events", async () => {
    render(ConversationSegment, { props: { id: SESSION } });
    await vi.advanceTimersByTimeAsync(2000);

    const talk = `talk:${SESSION}`;
    ch.deliverEvent(ids.get(talk)!, talk, 1, {
      session: SESSION,
      caps: { feed: true, send: true, stop: true, cards: true },
      composer: "resting",
      feedSeq: 1,
      health: "ok",
    });
    await vi.advanceTimersByTimeAsync(100);

    const before = cmds().length;
    const field = screen.getByRole("textbox");
    await fireEvent.input(field, { target: { value: TEXT } });
    await vi.advanceTimersByTimeAsync(50);
    const send = document.querySelector<HTMLButtonElement>('[data-footer-primary="send"]');
    expect(send).not.toBeNull();
    await fireEvent.click(send!);
    await vi.advanceTimersByTimeAsync(100);
    await waitFor(() => expect(cmds().slice(before)).toContain("agentSay"));

    // The echo: the daemon pushes the new timeline and talk state.
    ch.deliverEvent(ids.get(`timeline:${SESSION}`)!, `timeline:${SESSION}`, 1, {
      reset: true,
      session: SESSION,
      items: [],
      lastSeq: 1,
    });
    ch.deliverEvent(ids.get(talk)!, talk, 2, {
      session: SESSION,
      caps: { feed: true, send: true, stop: true, cards: true },
      composer: "resting",
      feedSeq: 2,
      health: "ok",
    });
    await vi.advanceTimersByTimeAsync(5000);

    const sent = cmds().slice(before);
    expect(sent.filter((c) => c === "agentSay")).toHaveLength(1);
    expect(sent.filter((c) => c === "talkState" || c === "sessionTimeline")).toEqual([]);
    expect(sent).toEqual(["agentSay"]);
    const say = ch.sent.find((f) => f.cmd === "agentSay")!;
    expect((say.payload as { args: { text: string } }).args.text).toBe(TEXT);
  });
});
