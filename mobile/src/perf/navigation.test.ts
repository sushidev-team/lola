// Watch lifecycle across navigation (spec §8: "no watcher, no work"; 04 §8.1
// idle cost). Fifty trips Inbox → Session → each segment → back leave exactly the
// streams the Inbox needs: every session topic a segment watched is unwatched on
// the wire, the registry holds nothing for it, no bus listener and no pane
// subscription is left behind. Measured on the real path: the screens, the
// desktop stores, the push source, the watch registry and the transport.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render } from "@testing-library/svelte";
import { tick } from "svelte";

vi.mock("@mobile/native", () => ({ native: null, keyboard: null, browser: null, transport: { haptic: async () => {}, addListener: async () => ({ remove() {} }), status: async () => null } }));

import NavHarness from "./NavHarness.test.svelte";
import { mobilePush } from "../lib/pushsource";
import { router } from "../lib/router.svelte";
import { tiers } from "../lib/tiers.svelte";
import { bridge } from "../wailsshim/bridge";
import { OffAll, listenerCount } from "../wailsshim/events";
import { UNWATCH_DELAY_MS, watches } from "../wailsshim/watch";
import { setPushSource } from "$lib/pushsource";
import { setTouchSurface } from "$lib/touchsurface";
import { store, type SessionInfo } from "$lib/store.svelte";
import { daemon, report, type Daemon } from "./harness";
import type { Seg } from "../lib/router.svelte";

class NoopResizeObserver {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}
(globalThis as { ResizeObserver?: unknown }).ResizeObserver ??= NoopResizeObserver;

const TRIPS = Number(process.env.LOLA_PERF_TRIPS ?? 50);
const SESSIONS = 10;
const GLOBAL = ["projects", "sessions", "status"];
const EVENTS = ["daemon:sessions", "daemon:projects", "daemon:status", "daemon:alive"];

const session = (n: number): SessionInfo =>
  ({
    id: `s${n}`,
    project: "p",
    issue: `NOR-${n}`,
    title: `title ${n}`,
    branch: `b${n}`,
    agentState: "working",
    delivery: "none",
    age: "1m",
    prNumber: 10 + n,
    worktree: `/w/s${n}`,
    tmuxName: `lola-s${n}`,
  }) as SessionInfo;

/** Time per navigation step; LOLA_PERF_STEP_MS=300 measures the peak at a person's pace. */
const STEP_MS = Number(process.env.LOLA_PERF_STEP_MS ?? 15);
const settle = () => new Promise((r) => setTimeout(r, STEP_MS));
async function step(): Promise<void> {
  await settle();
  await tick();
}

/** The topics the daemon believes this connection watches, replayed from the frames sent. */
function wireTopics(d: Daemon): string[] {
  const live = new Set<string>();
  for (const f of d.reqs()) {
    const topics = ((f.payload as { args?: { topics?: string[] } }).args?.topics ?? []) as string[];
    if (f.cmd === "watch") for (const t of topics) live.add(t);
    if (f.cmd === "unwatch") for (const t of topics) live.delete(t);
  }
  return [...live].sort();
}

const registryTopics = () => [...(watches as unknown as { topics: Map<string, unknown> }).topics.keys()].sort();
const paneFrames = (d: Daemon) => {
  const n: Record<string, number> = {};
  for (const f of d.ch.sent) if (f.type !== "req" || /pane|sub/i.test(f.cmd ?? "")) n[f.type === "req" ? f.cmd! : f.type] = (n[f.type === "req" ? f.cmd! : f.type] ?? 0) + 1;
  return n;
};
const paneSubs = (d: Daemon) => (d.transport as unknown as { subs: Map<string, unknown> }).subs.size;

let d: Daemon;
beforeEach(async () => {
  OffAll();
  setTouchSurface({});
  tiers.set(["read", "answer", "control", "shell"]);
  const list = Array.from({ length: SESSIONS }, (_, i) => session(i));
  d = await daemon((f) => (f.cmd === "sessions" ? { sessions: list } : f.cmd === "pane" ? { lines: [] } : f.cmd === "diff" ? { base: "main", mergeBase: "0123456789ab", files: [] } : {}));
  bridge.installTransport(d.transport);
  setPushSource(mobilePush);
  store.start();
  bridge.start();
  store.sessions = Array.from({ length: SESSIONS }, (_, i) => session(i));
  store.alive = true;
  router.stack = [{ name: "tab", tab: "inbox" }];
  router.sheet = "";
});

afterEach(() => {
  cleanup();
  bridge.stop();
  bridge.installTransport(null);
  setPushSource(null);
  setTouchSurface(null);
  OffAll();
});

describe("navigation", () => {
  it(`leaves no topic, listener or pane behind after ${TRIPS} trips through every segment`, async () => {
    render(NavHarness);
    await step();
    d.push("sessions", 1, { reset: true, upsert: Array.from({ length: SESSIONS }, (_, i) => session(i)) });
    await step();
    await new Promise((r) => setTimeout(r, UNWATCH_DELAY_MS + 100));
    const baseline = { wire: wireTopics(d), registry: registryTopics(), events: EVENTS.map(listenerCount) };
    expect(baseline.wire).toEqual(GLOBAL);

    let peak = 0;
    for (let i = 0; i < TRIPS; i++) {
      const id = `s${i % SESSIONS}`;
      router.push({ name: "session", id, seg: "conversation" });
      await step();
      for (const seg of ["changes", "review", "terminal", "conversation"] as Seg[]) {
        router.replace({ name: "session", id, seg });
        await step();
        peak = Math.max(peak, wireTopics(d).length);
      }
      router.back();
      await step();
    }
    await new Promise((r) => setTimeout(r, UNWATCH_DELAY_MS + 100));
    await step();

    const reqs = d.reqs();
    const watchFrames = reqs.filter((f) => f.cmd === "watch").length;
    const unwatchFrames = reqs.filter((f) => f.cmd === "unwatch").length;
    report(
      `navigation trips=${TRIPS} stepMs=${STEP_MS} peakWireTopics=${peak} watchReqs=${watchFrames} unwatchReqs=${unwatchFrames} ` +
        `wire=${JSON.stringify(wireTopics(d))} registry=${JSON.stringify(registryTopics())} ` +
        `events=${JSON.stringify(EVENTS.map(listenerCount))} (baseline ${JSON.stringify(baseline.events)}) panes=${paneSubs(d)} paneFrames=${JSON.stringify(paneFrames(d))}`,
    );

    expect(wireTopics(d)).toEqual(GLOBAL);
    expect(registryTopics()).toEqual(baseline.registry);
    expect(EVENTS.map(listenerCount)).toEqual(baseline.events);
    expect(paneSubs(d)).toBe(0);
  }, 60_000);
});
