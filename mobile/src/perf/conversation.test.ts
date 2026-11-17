// A long Conversation under push (spec 04 §8.1, §8.2): a 2 000-item story, its
// window on screen, and what one pushed item costs. Measured on the real path:
// ConversationSegment → the desktop Workspace and Timeline → the feed and
// timeline views → the push source. IntersectionObserver is scripted so the
// phone's screen shows the newest three turns, as it does when a session opens.
//
// What is asserted:
//  - The DOM is a window: the turns near the screen are mounted, every other turn
//    (folded or not) is a placeholder of its height.
//  - One pushed item mutates the DOM of the turn it lands in only, builds the
//    story at most twice, and hands every other turn the object it already had.
//  - The activity map, a pointer scrubber the phone never draws, is not mounted.
// Timings are printed with LOLA_PERF_LOG=1 and never asserted (jsdom, dev build).

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render } from "@testing-library/svelte";
import { flushSync, tick } from "svelte";

vi.mock("@mobile/native", () => ({ native: null, keyboard: null, browser: null, transport: { haptic: async () => {}, addListener: async () => ({ remove() {} }), status: async () => null } }));
vi.mock("$lib/turns", async (orig) => {
  const m = (await orig()) as Record<string, unknown>;
  const build = m.buildStory as (...a: unknown[]) => unknown;
  const builds = vi.fn((...a: unknown[]) => build(...a));
  return { ...m, buildStory: builds, __builds: builds };
});

import * as turns from "$lib/turns";
import ConversationSegment from "../screens/session/ConversationSegment.svelte";
import { mobilePush } from "../lib/pushsource";
import { router } from "../lib/router.svelte";
import { bridge } from "../wailsshim/bridge";
import { OffAll } from "../wailsshim/events";
import { setPushSource, type FeedPush } from "$lib/pushsource";
import { setTouchSurface } from "$lib/touchsurface";
import { store, type SessionInfo } from "$lib/store.svelte";
import type { SSHTimelineData } from "@bindings/internal/protocol";
import { longStory, textItem, userItem } from "../fixtures/build";
import { ScriptedIntersectionObserver, daemon, mutationsDuring, ownerOf, report } from "./harness";

const builds = (turns as unknown as { __builds: ReturnType<typeof vi.fn> }).__builds;
const SESSION = "s1";
const N = 2000;
/** longStory opens a turn every 10 items. */
const TURNS = N / 10;
/** Turns the scripted screen shows. */
const NEAR = 3;

const session = {
  id: SESSION,
  project: "p",
  issue: "NOR-1",
  title: "T",
  branch: "b",
  agentState: "working",
  delivery: "none",
  age: "1m",
  prNumber: 0,
  worktree: "/w/s1",
  tmuxName: "t",
} as SessionInfo;

const settle = () => new Promise((r) => setTimeout(r, 30));

// The push source as installed, with its callbacks captured so a push is applied
// synchronously and its cost is the client's alone (the wire has its own tests).
let feedOn: (p: FeedPush) => void = () => {};
let timelineOn: (d: SSHTimelineData, reset: boolean) => void = () => {};
const pushFeed = (p: Partial<FeedPush>) => {
  feedOn({ reset: false, older: false, epoch: "e1", lastSeq: 0, items: [], scanning: false, health: "ok", ...p });
  flushSync();
};

beforeEach(async () => {
  OffAll();
  setTouchSurface({});
  (globalThis as { IntersectionObserver?: unknown }).IntersectionObserver = ScriptedIntersectionObserver;
  ScriptedIntersectionObserver.instances = [];
  ScriptedIntersectionObserver.near = (el) => Number(((el as HTMLElement).dataset.turn ?? "").replace(/^f/, "")) / 10 >= TURNS - NEAR;
  const d = await daemon();
  bridge.installTransport(d.transport);
  setPushSource({
    ...mobilePush,
    feed: (id, on, err) => {
      feedOn = on;
      return mobilePush.feed(id, on, err);
    },
    timeline: (id, on, err) => {
      timelineOn = on;
      return mobilePush.timeline(id, on, err);
    },
  });
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
  setTouchSurface(null);
  OffAll();
  delete (globalThis as { IntersectionObserver?: unknown }).IntersectionObserver;
});

function census() {
  const root = document.querySelector("[data-conversation]")!;
  let folded = 0;
  let full = 0;
  let placeholder = 0;
  for (const t of root.querySelectorAll("[data-turn]")) {
    if (t.querySelector("[data-turn-fold]")) folded++;
    else if (t.querySelector(".cv-turn")) full++;
    else placeholder++;
  }
  return { folded, full, placeholder, elements: root.querySelectorAll("*").length };
}

/** Opens the segment on a 2 000-item story and lets the observer settle the window. */
async function openLongStory(): Promise<number> {
  render(ConversationSegment, { props: { id: SESSION } });
  await settle();
  timelineOn({ session: SESSION, items: [], lastSeq: 0, connections: null }, true);
  const t0 = performance.now();
  pushFeed({ reset: true, lastSeq: N, items: longStory(N) });
  const ms = performance.now() - t0;
  await settle();
  ScriptedIntersectionObserver.flushAll();
  await settle();
  await tick();
  return ms;
}

describe("a 2 000-item conversation", () => {
  it("mounts the turns near the screen and placeholders for the rest", async () => {
    const ms = await openLongStory();
    const c = census();
    report(`conversation reset items=${N} turns=${TURNS} ${ms.toFixed(1)}ms ${JSON.stringify(c)}`);
    expect(c.full + c.folded + c.placeholder).toBe(TURNS);
    // The newest turns are near; the ones a jump or the tail pins are mounted too.
    expect(c.full).toBeGreaterThanOrEqual(NEAR);
    expect(c.full + c.folded).toBeLessThanOrEqual(NEAR + 1);
    expect(c.placeholder).toBeGreaterThanOrEqual(TURNS - NEAR - 1);
    expect(document.querySelector(".cv-activity-map")).toBeNull();
  }, 60_000);

  for (const [label, item] of [
    ["a reply in the newest turn", textItem(N, "One more line from the agent.", `t${TURNS - 1}`)],
    ["a message that opens a turn", userItem(N + 1, "Next step please.", `t${TURNS}`)],
  ] as const) {
    it(`applies ${label} to that turn only`, async () => {
      await openLongStory();
      const root = document.querySelector("[data-conversation]")!;
      const before = new Map([...root.querySelectorAll<HTMLElement>("[data-turn]")].map((el) => [el.dataset.turn!, el]));
      builds.mockClear();
      let ms = 0;
      const records = await mutationsDuring(root, async () => {
        const t0 = performance.now();
        pushFeed({ lastSeq: item.seq, items: [item] });
        ms = performance.now() - t0;
        await settle();
      });
      // Records inside a turn name it; a turn added to the list is a record on the list itself.
      const touched = new Set(records.map((r) => ownerOf(r, "[data-turn]")?.getAttribute("data-turn")).filter(Boolean));
      report(`conversation append "${label}" ${ms.toFixed(1)}ms builds=${builds.mock.calls.length} turnsTouched=${[...touched].join(",")} records=${records.length}`);
      expect([...touched]).toEqual([`f${(TURNS - 1) * 10}`]);
      expect(builds.mock.calls.length).toBeLessThanOrEqual(2);
      // Every turn that was on the page is the same element.
      for (const [id, el] of before) expect(root.querySelector(`[data-turn="${id}"]`)).toBe(el);
    }, 60_000);
  }

  it("applies a timeline delta (a send's echo) with one story build", async () => {
    await openLongStory();
    const human = (k: number) => ({ id: `h${k}`, kind: "human", order: k + 1, seq: k + 1, at: new Date(Date.now() + k * 1000).toISOString(), human: { text: `note ${k}`, delivered: true } });
    timelineOn({ session: SESSION, items: Array.from({ length: 200 }, (_, k) => human(k)), lastSeq: 200, connections: null }, true);
    flushSync();
    await settle();
    ScriptedIntersectionObserver.flushAll();
    await settle();
    const root = document.querySelector("[data-conversation]")!;
    const elements = root.querySelectorAll("*").length;
    builds.mockClear();
    let ms = 0;
    const records = await mutationsDuring(root, async () => {
      const t0 = performance.now();
      timelineOn({ session: SESSION, items: [human(200)], lastSeq: 201, connections: null }, false);
      flushSync();
      ms = performance.now() - t0;
      await settle();
    });
    const touched = new Set(records.map((r) => ownerOf(r, "[data-turn]")?.getAttribute("data-turn")).filter(Boolean));
    report(`timeline delta into ${N} feed + 200 timeline items ${ms.toFixed(1)}ms builds=${builds.mock.calls.length} turnsTouched=${touched.size} elements=${elements}`);
    expect(builds.mock.calls.length).toBe(1);
    expect(touched.size).toBeLessThanOrEqual(2);
  }, 60_000);
});
