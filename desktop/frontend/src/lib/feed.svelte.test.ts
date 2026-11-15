import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";

const svc = vi.hoisted(() => ({ State: vi.fn(), Feed: vi.fn(), FeedOutput: vi.fn() }));
vi.mock("@bindings/desktop", () => ({ TalkService: svc }));

const { setPushSource } = await import("./pushsource");
const { SessionFeed } = await import("./feed.svelte");
import type { FeedPush, PushError } from "./pushsource";

const item = (id: string, order: number) => ({ id, order, kind: "text", text: id }) as never;
const push = (o: Partial<FeedPush> = {}): FeedPush => ({ reset: false, older: false, epoch: "e1", lastSeq: 1, items: [], scanning: false, health: "ok", ...o });

let on: (p: FeedPush) => void;
let onError: (e: PushError) => void;
const stop = vi.fn();
let n = 0;

beforeEach(() => {
  svc.Feed.mockReset().mockResolvedValue({ session: "s", epoch: "e1", lastSeq: 0, items: [] });
  stop.mockReset();
  setPushSource({
    feed: (_s: string, a: (p: FeedPush) => void, b: (e: PushError) => void) => {
      on = a;
      onError = b;
      return stop;
    },
  } as never);
});
afterEach(() => setPushSource(null));

const view = () => new SessionFeed(`feed${++n}`);

describe("follow", () => {
  it("is a no-op without a source", () => {
    setPushSource(null);
    const f = view();
    f.follow()();
    expect(f.following).toBe(false);
  });

  it("adopts a snapshot, then merges deltas of the same epoch", () => {
    const f = view();
    f.follow();
    on(push({ reset: true, older: true, lastSeq: 2, scanning: true, health: "ok", items: [item("a", 1), item("b", 2)] }));
    expect(f.items.map((i) => i.id)).toEqual(["a", "b"]);
    expect(f.more).toBe(true);
    expect(f.scanning).toBe(true);
    expect(f.loaded).toBe(true);
    on(push({ lastSeq: 3, items: [item("c", 3)] }));
    expect(f.items.map((i) => i.id)).toEqual(["a", "b", "c"]);
    expect(f.more).toBe(true);
    on(push({ lastSeq: 1, items: [] }));
    expect(f.items).toHaveLength(3);
  });

  it("replaces on a new epoch", () => {
    const f = view();
    f.follow();
    on(push({ reset: true, lastSeq: 2, items: [item("a", 1), item("b", 2)] }));
    on(push({ epoch: "e2", lastSeq: 1, items: [item("z", 1)] }));
    expect(f.items.map((i) => i.id)).toEqual(["z"]);
  });

  it("makes no feed call when a talk event carries a newer seq", () => {
    const f = view();
    f.follow();
    on(push({ reset: true, lastSeq: 2, items: [item("a", 1)] }));
    f.sync(50, "e1");
    expect(svc.Feed).not.toHaveBeenCalled();
  });

  it("returns the view to fetch when the topic is refused", async () => {
    const f = view();
    f.follow();
    onError("limit");
    expect(f.following).toBe(false);
    await vi.waitFor(() => expect(svc.Feed).toHaveBeenCalledTimes(1));
    f.sync(5, "e1");
    expect(svc.Feed).toHaveBeenCalledTimes(2);
  });

  it("stops the subscription", () => {
    const f = view();
    f.follow()();
    expect(stop).toHaveBeenCalledOnce();
    expect(f.following).toBe(false);
  });
});

describe("retention", () => {
  it("trims the story of a view left unfollowed past the bound, keeping the object and what was opened", async () => {
    const { feed } = await import("./feed.svelte");
    const { IDLE_VIEWS_KEPT } = await import("./viewretention");
    const first = feed.view("ret0");
    const unfollow = first.follow();
    on(push({ reset: true, lastSeq: 1, items: [item("a", 1)] }));
    first.expanded.add("a");
    unfollow();
    expect(first.items).toHaveLength(1);
    for (let i = 1; i <= IDLE_VIEWS_KEPT; i++) feed.view(`ret${i}`).follow()();
    expect(feed.view("ret0")).toBe(first);
    expect(first.items).toEqual([]);
    expect(first.loaded).toBe(false);
    expect(first.expanded.has("a")).toBe(true);
    expect(feed.view(`ret${IDLE_VIEWS_KEPT}`).loaded).toBe(false);
  });

  it("never trims a view that is still followed", async () => {
    const { feed } = await import("./feed.svelte");
    const { IDLE_VIEWS_KEPT } = await import("./viewretention");
    const live = feed.view("live0");
    live.follow();
    on(push({ reset: true, lastSeq: 1, items: [item("a", 1)] }));
    for (let i = 1; i <= IDLE_VIEWS_KEPT + 2; i++) feed.view(`live${i}`).follow()();
    expect(live.items).toHaveLength(1);
  });
});
