// The connection the shim's service modules run on, and the loop that turns the
// daemon's request/response protocol back into the push events the shared
// components already subscribe to.
//
// Two asymmetries between the desktop and the phone live here, and everything
// else in the shim is a thin mapping on top of them.
//
// 1. THERE IS NO SERVER PUSH. The desktop's Go side polls the unix socket every
//    two seconds and emits `daemon:sessions` / `daemon:projects` /
//    `daemon:status` / `daemon:alive` / `daemon:pusherr` into the WebView; the
//    frontend subscribes once and never polls. The remote protocol has no
//    session or status event frame at all — only `req`/`resp` and the pane
//    stream — so the same events have to be SYNTHESISED by polling here.
//    `pollLoop` is a direct port of `desktop/main.go`'s `pushLoop`, cadence,
//    every-other-tick split and error-dedup included, because the event shape
//    those components expect is defined by that working code rather than by a
//    fresh invention.
//
// 2. THE INITIAL SCREEN IS A FRAME, NOT BYTES. See screen.ts.
//
// The transport is INJECTED rather than constructed. The only real transport is
// the native `LolaTransport` plugin (raw TLS 1.3, a pinned self-signed SPKI,
// four-byte length prefixes — none of which JavaScript can do), and tests drive
// the same seam with `FakeChannel`. Until one is installed every call rejects
// with `ShimNotConnectedError`, which is a state the connect screen renders
// rather than an error anybody has to debug.

import type {
  ProjectsData,
  SessionsData,
  StatusData,
} from "@bindings/internal/protocol";
import {
  ConnectionClosedError,
  bytesToBase64,
  type ConnectOptions,
  type ConnectionStatus,
  type Endpoint,
  type PaneSubscription,
  type RequestFields,
  type Transport,
  type TransportRequestOptions,
  type Unsubscribe,
} from "../wire";
import { ShimNotConnectedError } from "./errors";
import { emit } from "./events";
import { renderResyncBase64 } from "./screen";

/** The desktop's own cadence. Sessions every tick, projects/status every other. */
export const DEFAULT_POLL_INTERVAL_MS = 2000;

export interface BridgeOptions {
  pollIntervalMs?: number;
  /**
   * Stop polling while the WebView is hidden. On iOS a backgrounded app is
   * suspended anyway and the native side tears the connection down, but the
   * WebView is also hidden for a foreground overlay (the share sheet, a system
   * alert), and a poll that fires into a suspended queue only produces a
   * timeout to explain later.
   */
  pauseWhenHidden?: boolean;
}

type PaneBinding = {
  sub: PaneSubscription;
  offEvent: Unsubscribe;
  offError: Unsubscribe;
};

export class ShimBridge {
  private transport: Transport | null = null;
  private timer: ReturnType<typeof setInterval> | undefined;
  private tick = 0;
  private polling = false;
  private lastAlive: boolean | null = null;
  private readonly lastErr = new Map<string, string>();
  private readonly panes = new Map<string, PaneBinding>();
  private offStatus: Unsubscribe | null = null;
  private visibilityHandler: (() => void) | null = null;

  constructor(private readonly opts: BridgeOptions = {}) {}

  // --- transport ownership -------------------------------------------------

  /**
   * Install (or replace, or with `null` remove) the transport every service
   * call runs on. Replacing one drops every pane binding: sequence numbers,
   * subscriptions and correlation ids are all properties of a single
   * connection and none of them survives a reconnect.
   */
  installTransport(t: Transport | null): void {
    this.offStatus?.();
    this.offStatus = null;
    this.dropPanes();
    this.transport = t;
    if (!t) {
      this.announceAlive(false);
      return;
    }
    this.offStatus = t.onStatus((s) => this.onTransportStatus(s));
    this.announceAlive(t.status.phase === "ready");
  }

  get installed(): Transport | null {
    return this.transport;
  }

  /** Connect an already-installed transport. */
  async connect(endpoint: Endpoint, opts?: ConnectOptions): Promise<void> {
    const t = this.require("connect");
    await t.connect(endpoint, opts);
  }

  async disconnect(reason?: string): Promise<void> {
    this.dropPanes();
    await this.transport?.disconnect(reason);
  }

  private require(method: string): Transport {
    if (!this.transport) throw new ShimNotConnectedError(method);
    return this.transport;
  }

  private onTransportStatus(s: ConnectionStatus): void {
    if (s.phase === "ready") {
      this.announceAlive(true);
      return;
    }
    if (s.phase === "closed") {
      this.dropPanes();
      this.announceAlive(false);
    }
  }

  // --- requests ------------------------------------------------------------

  /**
   * One daemon command. `method` is the shim-facing name and appears in the
   * not-connected rejection, so a failure names the call a component made
   * rather than the wire command it would have become.
   */
  request<T>(
    method: string,
    cmd: string,
    fields: RequestFields = {},
    opts?: TransportRequestOptions,
  ): Promise<T> {
    let t: Transport;
    try {
      t = this.require(method);
    } catch (err) {
      return Promise.reject(err);
    }
    return t.request<T>(cmd, fields, opts);
  }

  /** Whether a daemon is reachable right now. Never throws. */
  alive(): boolean {
    return this.transport?.status.phase === "ready";
  }

  // --- the synthesised push loop -------------------------------------------

  /** Start emitting daemon:* events. Idempotent. */
  startPolling(): void {
    if (this.timer !== undefined) return;
    const every = this.opts.pollIntervalMs ?? DEFAULT_POLL_INTERVAL_MS;
    this.timer = setInterval(() => void this.pollOnce(), every);

    if ((this.opts.pauseWhenHidden ?? true) && typeof document !== "undefined") {
      this.visibilityHandler = () => {
        if (!document.hidden) void this.pollOnce();
      };
      document.addEventListener("visibilitychange", this.visibilityHandler);
    }

    // Kick immediately so the first paint is not empty for a whole interval —
    // the same reason store.start() calls refresh() by hand.
    void this.pollOnce();
  }

  stopPolling(): void {
    if (this.timer !== undefined) clearInterval(this.timer);
    this.timer = undefined;
    if (this.visibilityHandler && typeof document !== "undefined") {
      document.removeEventListener("visibilitychange", this.visibilityHandler);
    }
    this.visibilityHandler = null;
  }

  /**
   * One poll. Exported for tests and for a pull-to-refresh gesture, which is
   * the mobile equivalent of the desktop's "kick an immediate fetch".
   *
   * Overlapping ticks are dropped rather than queued: the daemon allows four
   * concurrent requests per connection and answers the fifth with a
   * `rate_limited` refusal, so a slow link must not be able to stack polls into
   * one.
   */
  async pollOnce(): Promise<void> {
    if (this.polling) return;
    if ((this.opts.pauseWhenHidden ?? true) && typeof document !== "undefined" && document.hidden) {
      return;
    }
    const t = this.transport;
    const alive = t?.status.phase === "ready";
    this.announceAlive(alive);
    if (!t || !alive) {
      // A daemon that is not reachable is not "out of date": the frontend's
      // offline state covers it. Reset the dedup so a still-out-of-date daemon
      // re-announces when it comes back.
      this.lastErr.clear();
      this.tick++;
      return;
    }

    this.polling = true;
    try {
      const wide = this.tick % 2 === 0;
      const jobs: Promise<void>[] = [
        this.pollInto<SessionsData>("sessions", "daemon:sessions", t),
      ];
      if (wide) {
        jobs.push(this.pollInto<ProjectsData>("projects", "daemon:projects", t));
        jobs.push(this.pollInto<StatusData>("status", "daemon:status", t));
      }
      await Promise.allSettled(jobs);
    } finally {
      this.polling = false;
      this.tick++;
    }
  }

  private async pollInto<T>(cmd: string, event: string, t: Transport): Promise<void> {
    try {
      const data = await t.request<T>(cmd);
      emit(event, data);
      this.announceErr(cmd, "");
    } catch (err) {
      if (err instanceof ConnectionClosedError) {
        this.announceAlive(false);
        return;
      }
      this.announceErr(cmd, String(err));
    }
  }

  /** Emit `daemon:alive` only on a change, as the desktop's push loop does. */
  private announceAlive(alive: boolean): void {
    if (this.lastAlive === alive) return;
    this.lastAlive = alive;
    emit("daemon:alive", alive);
  }

  /**
   * Emit `daemon:pusherr` only on a CHANGE, recovery included. The banner it
   * raises is dismissible, so re-emitting the same failure every two seconds
   * would resurrect a banner the user just dismissed.
   */
  private announceErr(cmd: string, msg: string): void {
    if ((this.lastErr.get(cmd) ?? "") === msg) return;
    if (msg) this.lastErr.set(cmd, msg);
    else this.lastErr.delete(cmd);
    emit("daemon:pusherr", { cmd, msg });
  }

  // --- panes ---------------------------------------------------------------

  /**
   * Subscribe to a pane and republish it on the `pty:<name>` channel
   * LiveTerminal.svelte already listens on.
   *
   * The resync frame is rendered to an escape sequence and pushed down the same
   * channel as ordinary output (see screen.ts), so the component needs no
   * knowledge of the remote protocol — which is the whole reason it can be
   * reused without being edited.
   */
  async attachPane(name: string, cols: number, rows: number): Promise<string> {
    const t = this.require(`TermService.Attach(${name})`);
    await this.detachPane(name); // idempotent re-attach, as the desktop's is
    const sub = await t.subscribe(name, { cols, rows });

    const offEvent = sub.onEvent((e) => {
      if (e.kind === "output") {
        if (e.gap) {
          // A gap on a PTY frame means the daemon's bus dropped output for a
          // subscriber that fell behind, and the panebus contract says those
          // bytes cannot be replayed: the defined recovery is to re-subscribe,
          // which replaces the subscription and sends a fresh full screen.
          // Writing the surviving bytes through instead would leave the pane
          // subtly wrong with nothing on screen to say so, and this republisher
          // feeds a component (the desktop's LiveTerminal) that has no idea the
          // remote protocol exists and so cannot recover on its own.
          //
          // MobileTerminal.handle does exactly the same thing for the pane it
          // owns. The two paths are separate because they render into different
          // consumers, but they must agree about what a torn stream means.
          void this.repane(name, cols, rows);
          return;
        }
        if (e.data.length > 0) emit(`pty:${name}`, bytesToBase64(e.data));
        return;
      }
      if (e.kind === "resync") {
        emit(`pty:${name}`, renderResyncBase64(e.screen));
        return;
      }
      // A pane death arrives as a resync carrying `exited`. Paint the final
      // screen first when there is one, so the last thing an agent said stays
      // readable, and only then report the exit.
      if (e.screen.lines?.length) emit(`pty:${name}`, renderResyncBase64(e.screen));
      emit(`pty:${name}:exit`, undefined);
      this.dropPane(name);
    });

    const offError = sub.onError((err) => {
      // A refusal on a pane is not distinguishable by design between "no such
      // pane", "not available" and "not subscribed", so the only correct
      // reaction is to stop claiming the pane is attached and let the session
      // list be re-fetched.
      console.warn(`wailsshim: pane ${name}: ${err.message}`);
      emit(`pty:${name}:exit`, undefined);
      this.dropPane(name);
    });

    // The first screen arrived as the subscription's acknowledgement, before
    // any listener existed. Paint it now.
    if (sub.screen) emit(`pty:${name}`, renderResyncBase64(sub.screen));

    this.panes.set(name, { sub, offEvent, offError });
    return name;
  }

  /**
   * Tear a pane binding down and build a new one, after a gap that cannot be
   * replayed.
   *
   * Failure is reported the way a refusal is — the pane stops claiming to be
   * attached — because the alternative is a live-looking terminal over a stream
   * nobody is reading.
   */
  private async repane(name: string, cols: number, rows: number): Promise<void> {
    try {
      await this.detachPane(name);
      await this.attachPane(name, cols, rows);
    } catch (err) {
      console.warn(`wailsshim: pane ${name} could not be re-attached after a gap`, err);
      emit(`pty:${name}:exit`, undefined);
      this.dropPane(name);
    }
  }

  async detachPane(name: string): Promise<void> {
    const bound = this.panes.get(name);
    if (!bound) return;
    this.dropPane(name);
    await bound.sub.close().catch(() => {});
  }

  paneSubscription(name: string): PaneSubscription | undefined {
    return this.panes.get(name)?.sub;
  }

  private dropPane(name: string): void {
    const bound = this.panes.get(name);
    if (!bound) return;
    bound.offEvent();
    bound.offError();
    this.panes.delete(name);
  }

  private dropPanes(): void {
    for (const name of [...this.panes.keys()]) this.dropPane(name);
  }
}

/**
 * The process-wide bridge. A module-level singleton because the shared
 * components import the service namespaces at module scope and never receive a
 * context — exactly as `@bindings/desktop` is a singleton on the desktop.
 */
export const bridge = new ShimBridge();

/**
 * Hand the app's single Transport to the shim.
 *
 * The same instance must also go to `connection.svelte.ts`, which drives the
 * connect screen and the pane streams: two transports would authenticate twice,
 * take two of the daemon's eight connection slots, and let one of them show a
 * connected UI over the other's dead pipe. `main.ts` is the one place that
 * creates it and the one place that distributes it.
 */
export function useTransport(t: Transport | null): void {
  bridge.installTransport(t);
}
