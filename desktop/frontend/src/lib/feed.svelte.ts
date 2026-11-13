// The agent's log as a client sees it, per session: what has been fetched, what a person opened.
//
// Every string in here is a claim, because the agent can write that file. It is display-only and
// nothing here feeds anything else: the only thing that leaves is a row on screen.
//
// Expansion lives on this object rather than in a row, so a refetch, an epoch reset, an off-screen
// turn becoming a placeholder and a trip to the terminal all leave what a person opened where it was.

import { SvelteMap, SvelteSet } from "svelte/reactivity";
import type { AgentFeedItem } from "@bindings/internal/protocol";
import { isTalkUnsupported, talk } from "./talk.svelte";

/** One page of older history. A client never loops on More: a person asks for each page. */
export const FEED_PAGE = 300;
/** Asking again while the scanner has not reached the end of the file. */
export const FEED_SCAN_POLL_MS = 1000;

export type FeedOutput = { text: string; truncated: boolean; loading: boolean; failed: boolean };
export type FeedThread = { items: AgentFeedItem[]; loading: boolean; failed: boolean };

/** Upsert by id, ordered by the reader's Order. A tool item changes in place when its result lands. */
export function mergeFeed(current: readonly AgentFeedItem[], incoming: readonly AgentFeedItem[] | null | undefined): AgentFeedItem[] {
  if (!incoming?.length) return current as AgentFeedItem[];
  const byId = new Map(current.map((i) => [i.id, i]));
  for (const it of incoming) byId.set(it.id, it);
  return [...byId.values()].sort((a, b) => a.order - b.order);
}

export class SessionFeed {
  items = $state.raw<AgentFeedItem[]>([]);
  more = $state(false);
  scanning = $state(false);
  /** "" until the first answer. Display only: agent liveness never reads this. */
  health = $state("");
  loaded = $state(false);
  error = $state("");
  loadingOlder = $state(false);
  /** Tool rows, run lines and folded turns a person opened, by stable id. */
  expanded = new SvelteSet<string>();
  /** Rows already presented on their own stay there as new activity arrives. */
  standing = new SvelteSet<string>();
  outputs = new SvelteMap<string, FeedOutput>();
  threads = new SvelteMap<string, FeedThread>();

  private id: string;
  private epoch = "";
  private lastSeq = 0;
  private target = 0;
  private inflight = false;
  private again = false;

  constructor(id: string) {
    this.id = id;
  }

  /**
   * talkState moved the sequence, or a view just mounted and has nothing yet. A reader rebuilt by
   * a daemon restart, an eviction or an agent switch starts its count over and can come back
   * lower, so growth alone would stop asking for as long as it takes to catch up. The fetch side
   * already copes: a stale epoch is answered with the tail, and the reset below adopts it.
   */
  sync(seq: number | undefined, epoch?: string) {
    const rebuilt = (!!epoch && !!this.epoch && epoch !== this.epoch) || (typeof seq === "number" && seq < this.lastSeq);
    if (typeof seq === "number") this.target = rebuilt ? seq : Math.max(this.target, seq);
    if (!this.loaded || this.scanning || rebuilt || this.target > this.lastSeq) void this.fetch();
  }

  async fetch(): Promise<void> {
    if (this.inflight) {
      this.again = true;
      return;
    }
    this.inflight = true;
    try {
      // While the scanner is still reading, its items are the file's start and not its tail. A
      // merge would leave a hole, so the tail is asked for again until it has one.
      const tail = !this.epoch || this.scanning || !this.loaded;
      const r = await talk.feed({ session: this.id, epoch: this.epoch, ...(tail ? {} : { after: this.lastSeq }) } as never);
      if (!r) return;
      const reset = !!this.epoch && r.epoch !== this.epoch;
      this.epoch = r.epoch ?? "";
      this.scanning = !!r.scanning;
      this.health = r.health ?? "";
      this.error = "";
      if (tail || reset) {
        // A rescan changes the epoch and keeps ids, which is why what a person opened survives.
        this.items = r.items ?? [];
        this.more = !!r.more;
        this.lastSeq = r.lastSeq ?? 0;
        this.target = reset ? this.lastSeq : Math.max(this.target, this.lastSeq);
      } else {
        this.items = mergeFeed(this.items, r.items);
        // A backlog over the byte budget answers the last sequence it included, so asking again
        // resumes. An empty answer means there is nothing more, whatever the target claims.
        const before = this.lastSeq;
        this.lastSeq = Math.max(this.lastSeq, r.lastSeq ?? 0);
        if (this.lastSeq > before && this.lastSeq < this.target) this.again = true;
        else if (this.lastSeq <= before) this.target = this.lastSeq;
      }
    } catch (err) {
      if (isTalkUnsupported(err)) {
        this.health = "unsupported";
        this.error = "";
      } else {
        this.error = err instanceof Error ? err.message : String(err ?? "");
      }
    } finally {
      this.loaded = true;
      this.inflight = false;
      if (this.again) {
        this.again = false;
        void this.fetch();
      }
    }
  }

  /** One page further back. Its `lastSeq` is 0 on purpose, so it is never adopted as a position. */
  async older(): Promise<void> {
    const first = this.items[0];
    if (!first || this.loadingOlder) return;
    this.loadingOlder = true;
    try {
      const r = await talk.feed({ session: this.id, epoch: this.epoch, before: first.order, limit: FEED_PAGE } as never);
      if (r && r.epoch === this.epoch) {
        this.items = mergeFeed(this.items, r.items);
        this.more = !!r.more;
      }
    } catch (err) {
      this.error = err instanceof Error ? err.message : String(err ?? "");
    } finally {
      this.loadingOlder = false;
    }
  }

  /** The bounded output of one tool call, read once when current or first opened. */
  async output(item: string): Promise<void> {
    const had = this.outputs.get(item);
    if (had && (had.loading || !had.failed)) return;
    this.outputs.set(item, { text: "", truncated: false, loading: true, failed: false });
    try {
      const r = await talk.feedOutput(this.id, item);
      this.outputs.set(item, { text: r?.text ?? "", truncated: !!r?.truncated, loading: false, failed: false });
    } catch {
      this.outputs.set(item, { text: "", truncated: false, loading: false, failed: true });
    }
  }

  /** A subagent's own thread, read when a person opens its row. */
  async thread(parent: string): Promise<void> {
    const had = this.threads.get(parent);
    if (had && (had.loading || !had.failed)) return;
    this.threads.set(parent, { items: [], loading: true, failed: false });
    try {
      const r = await talk.feed({ session: this.id, parent } as never);
      this.threads.set(parent, { items: r?.items ?? [], loading: false, failed: false });
    } catch {
      this.threads.set(parent, { items: [], loading: false, failed: true });
    }
  }
}

class FeedStore {
  // A plain Map for the same reason as ssh.view: this creates on first read, inside $derived.
  private views = new Map<string, SessionFeed>();

  view(id: string): SessionFeed {
    let v = this.views.get(id);
    if (!v) {
      v = new SessionFeed(id);
      this.views.set(id, v);
    }
    return v;
  }
}

export const feed = new FeedStore();
