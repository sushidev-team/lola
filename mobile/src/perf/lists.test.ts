// The list budgets of spec §8.1: a keyed delta costs one row, and a long list
// mounts a window of rows. Both are measured on the real push path, from the
// bridge's `daemon:sessions` event to the DOM.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render } from "@testing-library/svelte";
import { flushSync, tick } from "svelte";

vi.mock("@mobile/native", () => ({ transport: { haptic: async () => {}, addListener: async () => ({ remove() {} }) } }));
vi.mock("$lib/talk.svelte", () => ({ talk: { state: () => null, poll: () => () => {}, choose: vi.fn() } }));
// metaLine is the first thing a mounted InboxRow derives from its model, so its
// call count is the number of rows that re-rendered.
vi.mock("@mobile/lib/inbox.svelte", async (orig) => {
  const m = (await orig()) as Record<string, unknown>;
  const real = m.metaLine as (...a: unknown[]) => string;
  const rendered = vi.fn((...a: unknown[]) => real(...a));
  return { ...m, metaLine: rendered, __rendered: rendered };
});

import Inbox from "../screens/Inbox.svelte";
import * as inboxModule from "../lib/inbox.svelte";
import { InboxModel, attachInbox, inbox, inboxView, rowModel, type RowModel } from "../lib/inbox.svelte";
import { router } from "../lib/router.svelte";
import { ESTIMATE, OVERSCAN, VIRTUALIZE_AT } from "../lib/components/VirtualList.svelte";
import { ShimBridge } from "../wailsshim/bridge";
import { ChannelTransport } from "../wailsshim/channeltransport";
import { OffAll, On, emit } from "../wailsshim/events";
import { FRAME_RESP, FakeChannel, type Frame } from "../wire";
import type { SessionInfo } from "$lib/store.svelte";
import { mutationsDuring, ownerOf, report } from "./harness";

const rendered = (inboxModule as unknown as { __rendered: ReturnType<typeof vi.fn> }).__rendered;

const VIEWPORT = 560;
const ROWS = 200;

const session = (n: number, over: Partial<SessionInfo> = {}): SessionInfo =>
  ({
    id: `s${n}`,
    project: "p",
    issue: `NOR-${n}`,
    title: `title ${n}`,
    branch: `b${n}`,
    agentState: "working",
    delivery: "none",
    age: "1m",
    prNumber: 0,
    ...over,
  }) as SessionInfo;

const rowsOf = (n: number) => Array.from({ length: n }, (_, i) => session(i));

/** A daemon that acknowledges every watch and records the stream ids. */
async function wire() {
  const ch = new FakeChannel();
  const ids = new Map<string, string>();
  ch.onSend = (f: Frame) => {
    if (f.type !== "req" || f.cmd !== "watch") return;
    const topics = (f.payload as { args: { topics: string[] } }).args.topics;
    for (const t of topics) ids.set(t, f.id!);
    ch.deliver({
      v: 2,
      type: FRAME_RESP,
      id: f.id,
      payload: { ok: true, data: { topics: topics.map((topic) => ({ topic, ok: true })) } },
    });
  };
  const t = new ChannelTransport({ open: async () => ch });
  await t.connect({ daemonId: "d" });
  const bridge = new ShimBridge();
  bridge.installTransport(t);
  bridge.start();
  return { bridge, ch, ids };
}

const settle = () => new Promise((r) => setTimeout(r, 60));

beforeEach(() => OffAll());
afterEach(() => {
  OffAll();
  cleanup();
  delete (HTMLElement.prototype as { clientHeight?: number }).clientHeight;
});

describe("keyed deltas", () => {
  it("runs rowModel once when a delta upserts one of 200 sessions, and keeps every other row object", async () => {
    const { bridge, ch, ids } = await wire();
    const modelOf = vi.fn((s: SessionInfo): RowModel => rowModel(s));
    const model = new InboxModel(modelOf);
    const off = attachInbox(model);
    await settle();

    const id = ids.get("sessions")!;
    ch.deliverEvent(id, "sessions", 1, { reset: true, upsert: rowsOf(ROWS) });
    await settle();
    expect(model.rows.size).toBe(ROWS);
    expect(modelOf).toHaveBeenCalledTimes(ROWS);

    const before = new Map(model.models);
    modelOf.mockClear();
    ch.deliverEvent(id, "sessions", 2, { upsert: [session(7, { agentState: "waiting_input" })] });
    await settle();

    expect(modelOf).toHaveBeenCalledTimes(1);
    expect(modelOf.mock.calls[0][0].id).toBe("s7");
    expect(model.models.get("s7")).not.toBe(before.get("s7"));
    let same = 0;
    for (const [k, m] of before) if (k !== "s7" && model.models.get(k) === m) same++;
    expect(same).toBe(ROWS - 1);

    off();
    bridge.stop();
  });

  it("does no work for a push that carries the same rows again", async () => {
    const { bridge, ch, ids } = await wire();
    const modelOf = vi.fn((s: SessionInfo): RowModel => rowModel(s));
    const model = new InboxModel(modelOf);
    const off = attachInbox(model);
    await settle();
    const id = ids.get("sessions")!;
    ch.deliverEvent(id, "sessions", 1, { reset: true, upsert: rowsOf(ROWS) });
    await settle();
    modelOf.mockClear();
    // A delta that touches no session (events only) republishes the same objects.
    ch.deliverEvent(id, "sessions", 2, { events: [{ id: "s1", to: "idle" }] });
    await settle();
    expect(modelOf).not.toHaveBeenCalled();
    off();
    bridge.stop();
  });

  it("publishes unchanged rows with the identity they had", async () => {
    const { bridge, ch, ids } = await wire();
    const seen: { sessions: SessionInfo[] }[] = [];
    On("daemon:sessions", (e) => seen.push(e.data));
    await settle();
    const id = ids.get("sessions")!;
    ch.deliverEvent(id, "sessions", 1, { reset: true, upsert: rowsOf(ROWS) });
    await settle();
    ch.deliverEvent(id, "sessions", 2, { upsert: [session(3, { title: "renamed" })] });
    await settle();
    const [a, b] = seen.slice(-2).map((d) => new Map(d.sessions.map((s) => [s.id, s])));
    expect(b.get("s3")).not.toBe(a.get("s3"));
    expect([...a].filter(([k, v]) => k !== "s3" && b.get(k) === v)).toHaveLength(ROWS - 1);
    bridge.stop();
  });
});

describe("rows re-rendered per push", () => {
  // The Inbox mounted with a list, then the list again with ONE session changed, as
  // the bridge publishes it (unchanged rows keep their object). Counted twice: the
  // rows that re-derived what they draw, and the rows whose DOM was written.
  for (const [n, windowed] of [
    [ROWS, true],
    [40, false],
  ] as const) {
    it(`re-renders 1 row of ${n} (${windowed ? "windowed" : "every row mounted"})`, async () => {
      Object.defineProperty(HTMLElement.prototype, "clientHeight", { configurable: true, get: () => VIEWPORT });
      router.stack = [{ name: "tab", tab: "inbox" }];
      router.sheet = "";
      inbox.ingest([]);
      render(Inbox);
      const list = rowsOf(n).map((s) => Object.freeze(s));
      const t0 = performance.now();
      emit("daemon:sessions", { sessions: list, events: [] });
      flushSync();
      const first = performance.now() - t0;
      const mounted = document.querySelectorAll("[data-vkey]").length;
      expect(document.querySelector("[role=list]")).toHaveAttribute("data-virtual", windowed ? "on" : "off");

      rendered.mockClear();
      const next = list.slice();
      // s1 sorts second (NOR-1), so it is on screen in both lists.
      next[1] = Object.freeze(session(1, { title: "renamed" }));
      let delta = 0;
      const records = await mutationsDuring(document.querySelector("[role=list]")!, async () => {
        const t1 = performance.now();
        emit("daemon:sessions", { sessions: next, events: [] });
        flushSync();
        delta = performance.now() - t1;
        await tick();
      });
      const rows = new Set(records.map((r) => ownerOf(r, "[data-vkey]")?.getAttribute("data-vkey") ?? "(list)"));
      report(`inbox sessions=${n} mounted=${mounted} first=${first.toFixed(1)}ms delta=${delta.toFixed(1)}ms rerendered=${rendered.mock.calls.length} domRows=${[...rows].join(",")}`);
      expect(rendered).toHaveBeenCalledTimes(1);
      expect([...rows]).toEqual(["row:s1"]);
    });
  }
});

describe("virtualization", () => {
  it("renders a window of the 300-row Inbox, not the list", async () => {
    Object.defineProperty(HTMLElement.prototype, "clientHeight", { configurable: true, get: () => VIEWPORT });
    router.stack = [{ name: "tab", tab: "inbox" }];
    router.sheet = "";
    inbox.ingest([]);
    render(Inbox);
    // The one `working` section header plus 300 rows: 301 list items.
    inbox.ingest(rowsOf(300));
    await tick();
    await tick();

    expect(document.querySelector("[role=list]")).toHaveAttribute("data-virtual", "on");
    const mounted = document.querySelectorAll("[data-vkey]").length;
    const visible = Math.ceil(VIEWPORT / ESTIMATE.row);
    expect(300).toBeGreaterThan(VIRTUALIZE_AT);
    expect(mounted).toBeGreaterThanOrEqual(visible);
    expect(mounted).toBeLessThanOrEqual(visible + 2 * OVERSCAN + 1);
    expect(document.querySelector('[data-vkey="row:s299"]')).toBeNull();
  });

  it("windows a 300-row Inbox grouped by project too", async () => {
    Object.defineProperty(HTMLElement.prototype, "clientHeight", { configurable: true, get: () => VIEWPORT });
    router.stack = [{ name: "tab", tab: "inbox" }];
    router.sheet = "";
    inbox.ingest([]);
    inboxView.set("project");
    try {
      render(Inbox);
      // Three project headers plus 300 rows.
      inbox.ingest(rowsOf(300).map((r, i) => ({ ...r, project: `p${i % 3}` }) as SessionInfo));
      await tick();
      await tick();
      expect(document.querySelector("[role=list]")).toHaveAttribute("data-virtual", "on");
      const mounted = document.querySelectorAll("[data-vkey]").length;
      expect(mounted).toBeLessThanOrEqual(Math.ceil(VIEWPORT / ESTIMATE.row) + 2 * OVERSCAN + 1);
      expect(document.querySelector('[data-vkey="group:p0"]')).not.toBeNull();
      expect(document.querySelector('[data-vkey="row:s299"]')).toBeNull();
    } finally {
      inboxView.set("status");
    }
  });
});
