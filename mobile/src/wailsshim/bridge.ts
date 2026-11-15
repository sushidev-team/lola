// The connection the shim's service modules run on, and the push events the
// shared components subscribe to.
//
// Invariants:
//  - The transport is INJECTED (the native plugin in the app, a `ChannelTransport`
//    over `FakeChannel` in tests). Until one is installed every call rejects with
//    `ShimNotConnectedError`, a state the connect screen renders.
//  - Nothing is polled while a watch is live. `sessions`, `projects` and `status`
//    arrive as events and are republished as the `daemon:*` events the desktop
//    store already consumes; a topic the daemon refuses is polled every
//    FALLBACK_POLL_MS, foreground and connected only, until the topic delivers
//    again or the next epoch re-watches it.
//  - Sessions are keyed. A reset replaces the map, a delta upserts and removes;
//    `events` is the held array so a delta never clears Activity. Rows keep their
//    object identity unless they changed, and one `daemon:sessions` goes out per
//    animation frame.
//  - While the page is hidden every request rejects at once: nothing queues
//    behind a socket the OS has suspended.
//  - Pane bindings outlive an epoch. A lost connection drops the subscription but
//    keeps the binding, and `onEpoch` re-subscribes it with its current viewport;
//    only a pane's own death or refusal ends a binding.

import type { ProjectsData, SessionInfo, SessionsData, StatusData, Event as ActivityEvent } from "@bindings/internal/protocol";
import {
  bytesToBase64,
  type PaneSubscription,
  type RequestFields,
  type SessionsEvent,
  type TransportRequestOptions,
  type Unsubscribe,
} from "../wire";
import type { ShimTransport } from "./channeltransport";
import { refusalText } from "@mobile/lib/refusal";
import { ShimNotConnectedError } from "./errors";
import { emit } from "./events";
import { renderResyncBase64 } from "./screen";
import { watch, watches, type Topic } from "./watch";

export const FALLBACK_POLL_MS = 2000;

export interface BridgeOptions {
  fallbackPollMs?: number;
}

type PaneBinding = {
  sub: PaneSubscription;
  offEvent: Unsubscribe;
  offError: Unsubscribe;
};

type Viewport = { cols: number; rows: number };

const GLOBAL_TOPICS = [
  { topic: "sessions", event: "daemon:sessions" },
  { topic: "projects", event: "daemon:projects" },
  { topic: "status", event: "daemon:status" },
] as const;

function nextFrame(fn: () => void): void {
  if (typeof requestAnimationFrame === "function") requestAnimationFrame(() => fn());
  else setTimeout(fn, 16);
}

const sentenced = new WeakSet<object>();

/**
 * Make a rejection speak a sentence. The SAME error object is returned, so `instanceof` and `.code`
 * keep working; only what a person reads changes: `message` and `String(err)` become the app's
 * sentence (`refusalText`), never a class name or a bare wire code. Done once per error.
 */
function sentence(err: unknown): unknown {
  if (!(err instanceof Error) || sentenced.has(err)) return err;
  sentenced.add(err);
  const text = refusalText(err);
  Object.defineProperty(err, "message", { value: text, writable: true, configurable: true });
  Object.defineProperty(err, "toString", { value: () => err.message, writable: true, configurable: true });
  return err;
}

export class ShimBridge {
  private transport: ShimTransport | null = null;
  private lastAlive: boolean | null = null;
  private offStatus: Unsubscribe | null = null;

  private readonly panes = new Map<string, PaneBinding>();
  private readonly viewports = new Map<string, Viewport>();

  private offWatches: Unsubscribe[] = [];
  private readonly fallbacks = new Map<string, ReturnType<typeof setInterval>>();
  private readonly rows = new Map<string, SessionInfo>();
  private lastEvents: ActivityEvent[] = [];
  private framePending = false;

  constructor(private readonly opts: BridgeOptions = {}) {}

  // --- transport ownership -------------------------------------------------

  /** Install (or with `null` remove) the transport. A replaced transport takes its panes' subscriptions with it. */
  installTransport(t: ShimTransport | null): void {
    this.offStatus?.();
    this.offStatus = null;
    this.dropSubscriptions();
    this.viewports.clear();
    this.stopFallbacks();
    this.transport = t;
    watches.bind(t);
    if (!t) {
      this.announceAlive(false);
      return;
    }
    this.offStatus = t.onStatus((s) => {
      this.announceAlive(s.phase === "ready");
      if (s.phase !== "ready") this.dropSubscriptions();
    });
    this.announceAlive(t.status.phase === "ready");
  }

  get installed(): ShimTransport | null {
    return this.transport;
  }

  private require(method: string): ShimTransport {
    if (!this.transport) throw new ShimNotConnectedError(method);
    return this.transport;
  }

  /**
   * One daemon command. `method` is the shim-facing name and appears in the
   * not-connected rejection, so a failure names the call a component made.
   */
  request<T>(
    method: string,
    cmd: string,
    fields: RequestFields = {},
    opts?: TransportRequestOptions,
  ): Promise<T> {
    if (typeof document !== "undefined" && document.visibilityState === "hidden") {
      return Promise.reject(sentence(new ShimNotConnectedError(method)));
    }
    let t: ShimTransport;
    try {
      t = this.require(method);
    } catch (err) {
      return Promise.reject(sentence(err));
    }
    return t.request<T>(cmd, fields, opts).catch((err: unknown) => {
      throw sentence(err);
    });
  }

  /** Whether a daemon is reachable right now. Never throws. */
  alive(): boolean {
    return this.transport?.status.phase === "ready";
  }

  private announceAlive(alive: boolean): void {
    if (this.lastAlive === alive) return;
    this.lastAlive = alive;
    emit("daemon:alive", alive);
  }

  // --- pushed global state -------------------------------------------------

  /** Watch `sessions`, `projects` and `status` and republish them. Idempotent. */
  start(): void {
    if (this.offWatches.length > 0) return;
    for (const { topic, event } of GLOBAL_TOPICS) {
      this.offWatches.push(
        watch(topic, {
          on: (payload, reset) => {
            this.stopFallback(topic);
            if (topic === "sessions") this.onSessions(payload as SessionsEvent, reset);
            else emit(event, payload as ProjectsData | StatusData);
          },
          onError: () => this.startFallback(topic, event),
        }),
      );
    }
  }

  stop(): void {
    for (const off of this.offWatches) off();
    this.offWatches = [];
    this.stopFallbacks();
  }

  private onSessions(e: SessionsEvent, reset: boolean): void {
    if (reset) this.rows.clear();
    for (const row of e.upsert ?? []) this.rows.set(row.id, Object.freeze(row));
    for (const id of e.remove ?? []) this.rows.delete(id);
    if (e.events) this.lastEvents = e.events;
    if (e.omitted?.length) void this.fetchOmitted(e.omitted);
    this.scheduleSessions();
  }

  /** Rows too large to stream are fetched whole with one `sessions` request. */
  private async fetchOmitted(ids: string[]): Promise<void> {
    try {
      const data = await this.request<SessionsData>("sessions", "sessions");
      const want = new Set(ids);
      for (const row of data.sessions ?? []) if (want.has(row.id)) this.rows.set(row.id, Object.freeze(row));
      this.scheduleSessions();
    } catch {
      // The rows stay absent; the next delta or epoch repairs the list.
    }
  }

  private scheduleSessions(): void {
    if (this.framePending) return;
    this.framePending = true;
    nextFrame(() => {
      this.framePending = false;
      emit("daemon:sessions", { sessions: [...this.rows.values()], events: this.lastEvents });
    });
  }

  private startFallback(topic: Topic, event: string): void {
    if (this.fallbacks.has(topic)) return;
    const poll = async () => {
      if (!this.alive() || (typeof document !== "undefined" && document.hidden)) return;
      try {
        emit(event, await this.request(topic, topic));
      } catch {
        // The next tick retries; a dead connection is `daemon:alive`'s to report.
      }
    };
    this.fallbacks.set(topic, setInterval(() => void poll(), this.opts.fallbackPollMs ?? FALLBACK_POLL_MS));
    void poll();
  }

  private stopFallback(topic: Topic): void {
    clearInterval(this.fallbacks.get(topic));
    this.fallbacks.delete(topic);
  }

  private stopFallbacks(): void {
    for (const timer of this.fallbacks.values()) clearInterval(timer);
    this.fallbacks.clear();
  }

  /** A new epoch is ready: fallbacks end (the watches are re-sent) and every bound pane re-subscribes. */
  async onEpoch(): Promise<void> {
    this.stopFallbacks();
    for (const [name, vp] of [...this.viewports]) {
      if (this.panes.has(name)) continue;
      try {
        await this.attachPane(name, vp.cols, vp.rows);
      } catch (err) {
        console.warn(`wailsshim: pane ${name} could not be re-attached`, err);
        this.endPane(name);
      }
    }
  }

  // --- panes ---------------------------------------------------------------

  /**
   * Subscribe to a pane and republish it on the `pty:<name>` channel
   * LiveTerminal already listens on. The resync snapshot is rendered to an
   * escape sequence and pushed down the same channel as output, so the
   * component needs no knowledge of the remote protocol.
   */
  async attachPane(name: string, cols: number, rows: number): Promise<string> {
    const t = this.require(`TermService.Attach(${name})`);
    await this.detachPane(name);
    const sub = await t.subscribe(name, { cols, rows });
    this.viewports.set(name, { cols, rows });

    const offEvent = sub.onEvent((e) => {
      if (e.kind === "output") {
        if (e.gap) {
          // Dropped output cannot be replayed; re-subscribing sends a fresh full screen.
          const vp = this.viewports.get(name) ?? { cols, rows };
          void this.repane(name, vp.cols, vp.rows);
          return;
        }
        if (e.data.length > 0) emit(`pty:${name}`, bytesToBase64(e.data));
        return;
      }
      if (e.kind === "resync") {
        emit(`pty:${name}`, renderResyncBase64(e.screen));
        return;
      }
      // A pane death arrives as a resync carrying `exited`: paint its last screen, then report it.
      if (e.screen.lines?.length) emit(`pty:${name}`, renderResyncBase64(e.screen));
      this.endPane(name);
    });

    const offError = sub.onError((err) => {
      // A lost connection keeps the binding for `onEpoch`; a refusal ends it.
      if (err.code === "connection_closed") {
        this.dropPane(name);
        return;
      }
      console.warn(`wailsshim: pane ${name}: ${err.message}`);
      this.endPane(name);
    });

    if (sub.screen) emit(`pty:${name}`, renderResyncBase64(sub.screen));
    this.panes.set(name, { sub, offEvent, offError });
    return name;
  }

  private async repane(name: string, cols: number, rows: number): Promise<void> {
    try {
      await this.detachPane(name);
      await this.attachPane(name, cols, rows);
    } catch (err) {
      console.warn(`wailsshim: pane ${name} could not be re-attached after a gap`, err);
      this.endPane(name);
    }
  }

  async detachPane(name: string): Promise<void> {
    const bound = this.panes.get(name);
    this.viewports.delete(name);
    if (!bound) return;
    this.dropPane(name);
    await bound.sub.close().catch(() => {});
  }

  /** Resize a bound pane; the size is kept for its re-subscribes, also while it waits for the next epoch. */
  resizePane(name: string, cols: number, rows: number): Promise<void> {
    if (this.viewports.has(name)) this.viewports.set(name, { cols, rows });
    return this.panes.get(name)?.sub.resize(cols, rows) ?? Promise.resolve();
  }

  paneSubscription(name: string): PaneSubscription | undefined {
    return this.panes.get(name)?.sub;
  }

  /** The pane is over: forget its binding and tell its terminal. */
  private endPane(name: string): void {
    this.viewports.delete(name);
    this.dropPane(name);
    emit(`pty:${name}:exit`, undefined);
  }

  private dropPane(name: string): void {
    const bound = this.panes.get(name);
    if (!bound) return;
    bound.offEvent();
    bound.offError();
    this.panes.delete(name);
  }

  private dropSubscriptions(): void {
    for (const name of [...this.panes.keys()]) this.dropPane(name);
  }
}

/** The process-wide bridge: the shared components import the service namespaces at module scope and never receive a context. */
export const bridge = new ShimBridge();

/** Hand the app's single transport to the shim. */
export function useTransport(t: ShimTransport | null): void {
  bridge.installTransport(t);
}


