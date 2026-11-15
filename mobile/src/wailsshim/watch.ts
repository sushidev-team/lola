// The watch registry: topics with subscribers, over one transport.
//
// Invariants:
//  - A topic is watched while it has a subscriber. The first subscriber starts
//    the stream; the last one's removal stops it after UNWATCH_DELAY_MS, so a
//    segment flip does not churn the daemon.
//  - Every subscriber starts from a reset. One that joins a stream already past
//    its reset (a second consumer, or one returning inside the delay, after
//    deltas went to nobody) re-watches the topic; the daemon's fresh reset goes
//    to every subscriber and replaces their state.
//  - Every `watch` or `unwatch` queued within one macrotask goes out as one
//    request per MAX_WATCH_TOPICS_PER_REQUEST topics.
//  - The watch id is the request id the correlator assigned, known before the
//    request is sent; the stream state machine (wire/events.ts) decides what an
//    event means.
//  - Nothing survives an epoch. `reset` returns every topic to idle when the
//    connection goes away and `resume` watches every topic that still has a
//    subscriber, in one request, when the next one is ready. Resets replace
//    consumer state; nothing is merged across epochs.
//  - A failed topic tells every subscriber once (`onError`) so it can fall back
//    to polling; retry follows `retryOf` and never happens on a timer for
//    `unknown_session`, `frame_too_large` or `denied`.

import {
  ConnectionClosedError,
  IDLE,
  MAX_WATCH_TOPICS_PER_REQUEST,
  WATCH_RETRY_AFTER_MS,
  onEvent,
  onStreamErr,
  onWatchResult,
  retryOf,
  startWatch,
  toWatchCode,
  type ErrFrame,
  type EventFrame,
  type StreamAction,
  type Topic,
  type TopicState,
  type Unsubscribe,
  type WatchCode,
  type WatchData,
} from "../wire";
import type { Frame } from "../wire";
import type { ShimTransport } from "./channeltransport";

export type { Topic } from "../wire";
export type { WatchCode } from "../wire";

export const UNWATCH_DELAY_MS = 1500;

export interface Sub {
  on(payload: unknown, reset: boolean): void;
  onError(code: WatchCode): void;
}

interface Entry {
  subs: Set<Sub>;
  st: TopicState;
  /** Pending delayed unwatch, set while the last subscriber is gone. */
  unwatch?: ReturnType<typeof setTimeout>;
  /** Pending timed retry of a rate-limited topic. */
  retry?: ReturnType<typeof setTimeout>;
}

export class WatchRegistry {
  private t: ShimTransport | null = null;
  private offEvent: Unsubscribe | null = null;
  private readonly topics = new Map<Topic, Entry>();
  private toWatch = new Set<Topic>();
  private toUnwatch = new Set<Topic>();
  private flushTimer: ReturnType<typeof setTimeout> | undefined;

  /** Route this transport's pushed events here. `null` detaches. */
  bind(t: ShimTransport | null): void {
    this.offEvent?.();
    this.offEvent = null;
    this.t = t;
    this.reset();
    if (t) this.offEvent = t.onEvent((f) => this.onFrame(f));
  }

  watch(topic: Topic, sub: Sub): Unsubscribe {
    let e = this.topics.get(topic);
    if (!e) {
      e = { subs: new Set(), st: IDLE };
      this.topics.set(topic, e);
    }
    e.subs.add(sub);
    clearTimeout(e.unwatch);
    e.unwatch = undefined;
    if (e.st.s === "failed") sub.onError(e.st.code);
    else if (e.st.s === "idle") this.queueWatch(topic);
    else if (e.st.s === "live") {
      e.st = IDLE;
      this.queueWatch(topic);
    }

    let live = true;
    return () => {
      if (!live) return;
      live = false;
      e.subs.delete(sub);
      if (e.subs.size === 0) this.scheduleUnwatch(topic, e);
    };
  }

  /** The connection is gone: every stream with it. Subscribers stay. */
  reset(): void {
    clearTimeout(this.flushTimer);
    this.flushTimer = undefined;
    this.toWatch.clear();
    this.toUnwatch.clear();
    for (const [topic, e] of [...this.topics]) {
      clearTimeout(e.retry);
      e.retry = undefined;
      e.st = IDLE;
      if (e.subs.size === 0) {
        clearTimeout(e.unwatch);
        this.topics.delete(topic);
      }
    }
  }

  /** A new epoch is ready: watch every topic that has a subscriber, in one request. */
  resume(): void {
    for (const [topic, e] of this.topics) {
      clearTimeout(e.retry);
      e.retry = undefined;
      e.st = IDLE;
      if (e.subs.size > 0) this.toWatch.add(topic);
    }
    this.schedule();
  }

  // --- sending -------------------------------------------------------------

  private queueWatch(topic: Topic): void {
    this.toUnwatch.delete(topic);
    this.toWatch.add(topic);
    this.schedule();
  }

  private schedule(): void {
    if (this.flushTimer !== undefined || (this.toWatch.size === 0 && this.toUnwatch.size === 0)) return;
    this.flushTimer = setTimeout(() => {
      this.flushTimer = undefined;
      this.flush();
    }, 0);
  }

  private flush(): void {
    const t = this.t;
    if (!t || t.status.phase !== "ready") {
      // `resume` re-queues everything when the next epoch is ready.
      this.toWatch.clear();
      this.toUnwatch.clear();
      return;
    }
    const unwatch = [...this.toUnwatch];
    this.toUnwatch.clear();
    const watch = [...this.toWatch].filter((topic) => {
      const e = this.topics.get(topic);
      return e !== undefined && e.subs.size > 0 && e.st.s === "idle";
    });
    this.toWatch.clear();

    for (let i = 0; i < unwatch.length; i += MAX_WATCH_TOPICS_PER_REQUEST) {
      const topics = unwatch.slice(i, i + MAX_WATCH_TOPICS_PER_REQUEST);
      // Best effort: the daemon drops the streams with the connection anyway.
      t.request("unwatch", { args: { topics } }).catch(() => {});
    }
    for (let i = 0; i < watch.length; i += MAX_WATCH_TOPICS_PER_REQUEST) {
      this.send(t, watch.slice(i, i + MAX_WATCH_TOPICS_PER_REQUEST));
    }
  }

  private send(t: ShimTransport, topics: Topic[]): void {
    let begun: { id: string; done: Promise<WatchData> };
    try {
      begun = t.begin<WatchData>("watch", { args: { topics } });
    } catch {
      return;
    }
    const { id } = begun;
    for (const topic of topics) this.topics.get(topic)!.st = startWatch(id);
    begun.done.then(
      (data) => this.onResults(id, topics, data),
      (err) => {
        // An ended epoch is `resume`'s to repair; any other refusal fails the whole request.
        if (err instanceof ConnectionClosedError) return;
        for (const topic of topics) this.apply(topic, onWatchResult(this.state(topic), id, { ok: false, code: "bad_request" }));
      },
    );
  }

  private onResults(id: string, topics: Topic[], data: WatchData | undefined): void {
    const byTopic = new Map((data?.topics ?? []).map((r) => [r.topic, r]));
    for (const topic of topics) {
      const r = byTopic.get(topic) ?? { ok: false, code: "bad_request" };
      this.apply(topic, onWatchResult(this.state(topic), id, r));
    }
  }

  // --- receiving -----------------------------------------------------------

  private state(topic: Topic): TopicState {
    return this.topics.get(topic)?.st ?? IDLE;
  }

  private onFrame(f: Frame): void {
    const topic = f.topic as Topic | undefined;
    if (!topic) return;
    const e = this.topics.get(topic);
    if (!e) return;
    if (f.type === "err") this.apply(topic, onStreamErr(e.st, f as ErrFrame));
    else this.apply(topic, onEvent(e.st, topic, f as EventFrame));
  }

  private apply(topic: Topic, [st, actions]: [TopicState, StreamAction[]]): void {
    const e = this.topics.get(topic);
    if (!e) return;
    e.st = st;
    for (const a of actions) {
      switch (a.a) {
        case "deliver":
          for (const s of [...e.subs]) this.call(() => s.on(a.payload, a.reset));
          break;
        case "rewatch":
          this.queueWatch(topic);
          break;
        case "fail":
          this.fail(topic, e, a.code);
          break;
        case "drop":
          break;
      }
    }
  }

  private fail(topic: Topic, e: Entry, code: WatchCode): void {
    for (const s of [...e.subs]) this.call(() => s.onError(code));
    if (retryOf(toWatchCode(code)) !== "after_delay") return;
    clearTimeout(e.retry);
    e.retry = setTimeout(() => {
      e.retry = undefined;
      if (e.st.s === "failed" && e.subs.size > 0) {
        e.st = IDLE;
        this.queueWatch(topic);
      }
    }, WATCH_RETRY_AFTER_MS);
  }

  private call(fn: () => void): void {
    try {
      fn();
    } catch (err) {
      console.error("wailsshim: watch subscriber threw", err);
    }
  }

  // --- unsubscribing -------------------------------------------------------

  private scheduleUnwatch(topic: Topic, e: Entry): void {
    clearTimeout(e.unwatch);
    e.unwatch = setTimeout(() => {
      e.unwatch = undefined;
      if (e.subs.size > 0 || this.topics.get(topic) !== e) return;
      const held = e.st.s === "awaiting" || e.st.s === "live";
      clearTimeout(e.retry);
      this.topics.delete(topic);
      this.toWatch.delete(topic);
      if (held) {
        this.toUnwatch.add(topic);
        this.retryLimited();
        this.schedule();
      }
    }, UNWATCH_DELAY_MS);
  }

  /** A stream slot was freed: a topic the daemon refused with `limit` may try again. */
  private retryLimited(): void {
    for (const [topic, e] of this.topics) {
      if (e.st.s === "failed" && e.st.code === "limit" && e.subs.size > 0) {
        e.st = IDLE;
        this.toWatch.add(topic);
      }
    }
  }
}

/** The app's one registry; `main.ts` binds it to the app's one transport. */
export const watches = new WatchRegistry();

/** Subscribe to a topic; the returned function unsubscribes. */
export function watch(topic: Topic, sub: Sub): Unsubscribe {
  return watches.watch(topic, sub);
}
