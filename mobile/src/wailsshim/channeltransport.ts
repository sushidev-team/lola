// The app's one transport above the frames: correlation, pane subscriptions,
// pushed events, and the life of the connection across native epochs.
//
// Invariants:
//  - The native plugin owns TLS, pinning, pairing, framing and every retry; this
//    class owns what a frame means. A channel is one connection of the plugin
//    (one epoch). A channel that also reports `onUp`/`onDown` is long-lived: the
//    plugin reconnects and migrates paths on its own, and each new epoch gets a
//    fresh Correlator, because ids, pane sequence numbers and watches are
//    properties of one connection and none of them survives it.
//  - A request in flight when its epoch ends rejects with `ConnectionClosedError`.
//    It is re-sent once on the next epoch only if `isRead(cmd)`; anything else
//    may already have been applied, so its caller decides.
//  - A refused command (`denied`, `unknown_cmd`, `rate_limited`) rejects that
//    request and leaves the socket up. Only `unsupported_version` and
//    `frame_too_large` are fatal.
//  - A migrated socket is adopted once no mutating request is in flight, or
//    after `MIGRATE_DRAIN_MS`.
//  - `status.epoch` changes exactly when a new connection became ready, so
//    "ready and the epoch changed" is the signal to resume.

import type { DeviceInfo, FailureCode, PathInfo } from "lola-transport";
import {
  CODE_UNSUPPORTED_VERSION,
  ConnectionClosedError,
  Correlator,
  FRAME_ERR,
  FRAME_EVENT,
  FRAME_RESYNC,
  MAX_PANE_NAME,
  RequestTimeoutError,
  WireError,
  WireRefusalError,
  alwaysFatalCode,
  clientMayAccept,
  isRead,
  requestFrame,
  subFrame,
  supportedFrameVersion,
  unsubFrame,
  validPaneName,
  type ConnectOptions,
  type ConnectionStatus,
  type Frame,
  type FrameChannel,
  type PaneSubscription,
  type RequestFields,
  type ResyncPayload,
  type TransportRequestOptions,
  type Unsubscribe,
  type Viewport,
} from "../wire";
import { LinkError } from "./pluginerror";
import { ChannelPaneSubscription, fanout, type Listener } from "./panesubs";

/** What is being connected to: a paired daemon, by id. Nothing secret. */
export interface LinkTarget {
  daemonId: string;
}

/** How long a migrated socket waits for in-flight mutating requests before it is adopted. */
export const MIGRATE_DRAIN_MS = 2000;

export type LinkPhase = ConnectionStatus["phase"];

/** The connection as the UI and the stores see it. */
export interface LinkStatus extends ConnectionStatus {
  /** Changes exactly when a new connection became ready; 0 before the first. */
  epoch: number;
  path?: PathInfo;
  device?: DeviceInfo;
  /** The plugin's failure code when the failure was not the daemon speaking. */
  failure?: FailureCode;
  /** How long the path race that made this connection took, when the plugin said. */
  raceMs?: number;
}

export interface LinkUp {
  epoch: number;
  path?: PathInfo;
  device?: DeviceInfo;
  raceMs?: number;
}

/**
 * A channel that outlives one epoch. `onClose` stays the terminal signal;
 * `onDown` is a drop the plugin will retry, `onUp` its recovery or a migration
 * it already adopted.
 */
export interface LinkChannel extends FrameChannel {
  readonly up?: LinkUp;
  onUp?(l: Listener<LinkUp>): Unsubscribe;
  onDown?(l: Listener<Error>): Unsubscribe;
  onMigrating?(l: Listener<LinkUp>): Unsubscribe;
  /** The device record of the CURRENT epoch arrived (or changed) after `onUp`. */
  onDevice?(l: Listener<DeviceInfo>): Unsubscribe;
  adopt?(epoch: number): Promise<void>;
}

export type ChannelFactory = (target: LinkTarget, opts?: ConnectOptions) => Promise<FrameChannel | LinkChannel>;

export interface ChannelTransportOptions {
  open: ChannelFactory;
  /** Per-request deadline. Defaults to the correlator's own 15 s. */
  timeoutMs?: number;
}

/** The seam the bridge, the watch registry and the connection store code against. */
export interface ShimTransport {
  readonly status: LinkStatus;
  connect(target: LinkTarget, opts?: ConnectOptions): Promise<void>;
  disconnect(reason?: string): Promise<void>;
  request<T = unknown>(cmd: string, fields?: RequestFields, opts?: TransportRequestOptions): Promise<T>;
  /** One request whose id is known before it is sent; never re-sent. */
  begin<T = unknown>(
    cmd: string,
    fields?: RequestFields,
    opts?: TransportRequestOptions,
  ): { id: string; done: Promise<T> };
  subscribe(pane: string, viewport?: Viewport, opts?: TransportRequestOptions): Promise<PaneSubscription>;
  subscription(pane: string): PaneSubscription | undefined;
  onFrame(l: Listener<Frame>): Unsubscribe;
  onStatus(l: Listener<LinkStatus>): Unsubscribe;
  onRefusal(l: Listener<WireRefusalError>): Unsubscribe;
  /** `event` frames and the `err` frames that end one event stream. */
  onEvent(l: Listener<Frame>): Unsubscribe;
}

function refusalOf(err: Error | undefined): ConnectionStatus["refusal"] {
  if (!(err instanceof WireRefusalError)) return undefined;
  return { code: err.code, message: err.message, minV: err.minV, maxV: err.maxV };
}

function failureOf(err: Error | undefined): FailureCode | undefined {
  return err instanceof LinkError ? err.code : undefined;
}

export class ChannelTransport implements ShimTransport {
  status: LinkStatus = { phase: "idle", epoch: 0 };

  private channel: FrameChannel | null = null;
  private correlator: Correlator | null = null;
  private connecting: Promise<void> | null = null;
  private userClosed = false;
  private offChannel: Unsubscribe[] = [];

  private readonly subs = new Map<string, ChannelPaneSubscription>();
  private readonly mutating = new Set<string>();
  private quiet: Array<() => void> = [];
  private readonly frameListeners = new Set<Listener<Frame>>();
  private readonly statusListeners = new Set<Listener<LinkStatus>>();
  private readonly refusalListeners = new Set<Listener<WireRefusalError>>();
  private readonly eventListeners = new Set<Listener<Frame>>();

  constructor(private readonly opts: ChannelTransportOptions) {}

  // --- lifecycle -----------------------------------------------------------

  async connect(target: LinkTarget, opts?: ConnectOptions): Promise<void> {
    if (this.connecting) return this.connecting;
    if (this.status.phase === "ready") return;
    this.connecting = this.doConnect(target, opts).finally(() => {
      this.connecting = null;
    });
    return this.connecting;
  }

  private async doConnect(target: LinkTarget, opts?: ConnectOptions): Promise<void> {
    this.userClosed = false;
    this.setStatus({ phase: "connecting", epoch: this.status.epoch });
    let channel: FrameChannel | LinkChannel;
    try {
      channel = await this.opts.open(target, opts);
    } catch (err) {
      const e = err as Error;
      this.setStatus({
        phase: "closed",
        epoch: this.status.epoch,
        error: e,
        refusal: refusalOf(e),
        failure: failureOf(e),
      });
      throw err;
    }

    this.channel = channel;
    this.offChannel = [channel.onFrame((f) => this.route(f)), channel.onClose((err) => this.onClosed(err))];
    const link = channel as LinkChannel;
    if (link.onUp) this.offChannel.push(link.onUp((u) => this.bringUp(u)));
    if (link.onDown) this.offChannel.push(link.onDown((err) => this.drop(err)));
    if (link.onMigrating) this.offChannel.push(link.onMigrating((u) => void this.adopt(link, u)));
    if (link.onDevice) {
      this.offChannel.push(link.onDevice((device) => this.setStatus({ ...this.status, device })));
    }
    this.bringUp(link.up);
  }

  /** A new connection is ready: fresh correlator, no panes, no pending requests. */
  private bringUp(up?: LinkUp): void {
    const channel = this.channel;
    if (!channel) return;
    this.endEpoch(new ConnectionClosedError("epoch ended"));
    this.correlator = new Correlator({ send: (f) => channel.send(f), timeoutMs: this.opts.timeoutMs });
    this.setStatus({
      phase: "ready",
      epoch: up?.epoch ?? this.status.epoch + 1,
      path: up?.path,
      device: up?.device,
      raceMs: up?.raceMs,
    });
  }

  private async adopt(link: LinkChannel, up: LinkUp): Promise<void> {
    await this.drained(MIGRATE_DRAIN_MS);
    try {
      await link.adopt?.(up.epoch);
    } catch {
      // The plugin adopts on its own after MIGRATE_DRAIN_MS; a refused adopt is not a failure.
    }
  }

  async disconnect(reason = "client disconnected"): Promise<void> {
    this.userClosed = true;
    await this.teardown(new ConnectionClosedError(reason));
  }

  /** Fail what belongs to the ended connection: requests, panes, mutating bookkeeping. */
  private endEpoch(cause: Error): void {
    // Whatever ended it, an in-flight request sees a closed connection (which is
    // what makes a read re-sendable); a daemon refusal keeps its own identity.
    const err = cause instanceof WireRefusalError || cause instanceof ConnectionClosedError
      ? cause
      : new ConnectionClosedError(cause.message);
    this.correlator?.failAll(err);
    this.correlator = null;
    const refusal = err instanceof WireRefusalError ? err : new WireRefusalError("connection_closed", cause.message);
    for (const sub of this.subs.values()) sub.fail(refusal);
    this.subs.clear();
    this.mutating.clear();
    this.releaseQuiet();
  }

  /** The plugin lost the connection and is retrying: not terminal. */
  private drop(err: Error): void {
    if (!this.channel) return;
    this.endEpoch(err);
    this.setStatus({
      phase: "connecting",
      epoch: this.status.epoch,
      error: err,
      refusal: refusalOf(err),
      failure: failureOf(err),
    });
  }

  private async teardown(err: Error): Promise<void> {
    const ch = this.channel;
    this.channel = null;
    for (const off of this.offChannel) off();
    this.offChannel = [];
    this.endEpoch(err);
    this.setStatus({
      phase: "closed",
      epoch: this.status.epoch,
      error: err,
      refusal: refusalOf(err),
      failure: failureOf(err),
    });
    try {
      await ch?.close(err.message);
    } catch {
      // Closing a socket that is already gone is not a failure worth reporting.
    }
  }

  private onClosed(err?: Error): void {
    if (this.status.phase === "closed") return;
    void this.teardown(err ?? new ConnectionClosedError());
  }

  // --- frame routing -------------------------------------------------------

  private route(f: Frame): void {
    fanout(this.frameListeners, f, "frame");

    if (!clientMayAccept(f)) {
      this.refuse(
        new WireRefusalError(
          "unknown_type",
          `client received an unacceptable frame (type ${String(f.type)}, v ${String(f.v)})`,
        ),
      );
      return;
    }
    // An `err` frame is readable at any version so a skew can still be named.
    if (f.type !== FRAME_ERR && !supportedFrameVersion(f.v)) {
      this.refuse(
        new WireRefusalError(
          CODE_UNSUPPORTED_VERSION,
          `the daemon sent envelope version ${String(f.v)}, which this client does not speak`,
        ),
      );
      return;
    }

    // Pushed events, and the err that ends one event stream, belong to the watch registry.
    if (f.type === FRAME_EVENT || (f.type === FRAME_ERR && f.topic)) {
      fanout(this.eventListeners, f, "event");
      return;
    }

    if (f.pane) this.subs.get(f.pane)?.handle(f);

    // Read the code before `accept` settles the request: the request still gets
    // its specific refusal and the connection still dies on a fatal one.
    const fatal = f.type === FRAME_ERR ? WireRefusalError.fromFrame(f) : undefined;

    if (this.correlator?.accept(f)) {
      if (fatal && alwaysFatalCode(fatal.code)) {
        this.refuse(fatal);
        void this.teardown(fatal);
      }
      return;
    }

    if (fatal) {
      if (!f.pane) this.refuse(fatal);
      if (alwaysFatalCode(fatal.code)) void this.teardown(fatal);
    }
  }

  private refuse(err: WireRefusalError): void {
    this.status = { ...this.status, refusal: { code: err.code, message: err.message } };
    fanout(this.refusalListeners, err, "refusal");
  }

  // --- requests ------------------------------------------------------------

  begin<T = unknown>(
    cmd: string,
    fields: RequestFields = {},
    opts: TransportRequestOptions = {},
  ): { id: string; done: Promise<T> } {
    const correlator = this.requireCorrelator(cmd);
    const id = correlator.nextID();
    const track = !isRead(cmd);
    if (track) this.mutating.add(id);
    const done = correlator.request<T>(requestFrame(id, cmd, fields), {
      timeoutMs: opts.timeoutMs,
      signal: opts.signal,
    });
    if (track) {
      const settled = () => {
        this.mutating.delete(id);
        if (this.mutating.size === 0) this.releaseQuiet();
      };
      done.then(settled, settled);
    }
    return { id, done };
  }

  async request<T = unknown>(
    cmd: string,
    fields: RequestFields = {},
    opts: TransportRequestOptions = {},
  ): Promise<T> {
    const resendable = isRead(cmd);
    for (let attempt = 0; ; attempt++) {
      // Not connected throws here, synchronously: nothing was sent, nothing is re-sent.
      const { done } = this.begin<T>(cmd, fields, opts);
      try {
        return await done;
      } catch (err) {
        if (!resendable || attempt > 0 || this.userClosed || !(err instanceof ConnectionClosedError)) throw err;
        await this.untilReady(err, opts.timeoutMs);
      }
    }
  }

  /** Resolves on the next ready epoch; rejects `cause` when none comes. */
  private untilReady(cause: Error, timeoutMs = 15_000): Promise<void> {
    if (this.status.phase === "ready") return Promise.resolve();
    return new Promise<void>((resolve, reject) => {
      const done = (fn: () => void) => {
        off();
        clearTimeout(timer);
        fn();
      };
      const off = this.onStatus((s) => {
        if (s.phase === "ready") done(resolve);
        else if (s.phase === "closed") done(() => reject(cause));
      });
      const timer = setTimeout(() => done(() => reject(new RequestTimeoutError("", timeoutMs))), timeoutMs);
    });
  }

  private drained(ms: number): Promise<void> {
    if (this.mutating.size === 0) return Promise.resolve();
    return new Promise<void>((resolve) => {
      const timer = setTimeout(resolve, ms);
      this.quiet.push(() => {
        clearTimeout(timer);
        resolve();
      });
    });
  }

  private releaseQuiet(): void {
    const waiting = this.quiet;
    this.quiet = [];
    for (const w of waiting) w();
  }

  // --- panes ---------------------------------------------------------------

  async subscribe(
    pane: string,
    viewport?: Viewport,
    opts: TransportRequestOptions = {},
  ): Promise<PaneSubscription> {
    if (!validPaneName(pane)) {
      throw new WireError(
        `wire: ${pane.length > MAX_PANE_NAME ? "pane name too long" : `not a lola pane name: ${pane}`}`,
      );
    }
    const existing = this.subs.get(pane);
    if (existing && !existing.exited) return existing;

    const correlator = this.requireCorrelator(`sub ${pane}`);
    const id = correlator.nextID();
    const sub = new ChannelPaneSubscription(pane, id, this);
    this.subs.set(pane, sub);
    try {
      // The acknowledgement IS the first resync, correlated on the sub's id;
      // later resyncs echo the same id, so it must never be reused.
      await correlator.request<ResyncPayload>(subFrame(id, pane, viewport), {
        expect: FRAME_RESYNC,
        timeoutMs: opts.timeoutMs,
        signal: opts.signal,
      });
    } catch (err) {
      this.subs.delete(pane);
      // A refusal created nothing. A timeout or abort may have left the daemon
      // pumping a pane nothing routes, so it is told, unacknowledged.
      if (!(err instanceof WireRefusalError)) {
        await this.sendFrame(unsubFrame(pane)).catch(() => {});
      }
      throw err;
    }
    return sub;
  }

  subscription(pane: string): PaneSubscription | undefined {
    return this.subs.get(pane);
  }

  retire(pane: string): void {
    this.subs.delete(pane);
  }

  async sendFrame(f: Frame): Promise<void> {
    const ch = this.channel;
    if (!ch || this.status.phase !== "ready") throw new ConnectionClosedError();
    await ch.send(f);
  }

  // --- observers -----------------------------------------------------------

  onFrame(l: Listener<Frame>): Unsubscribe {
    this.frameListeners.add(l);
    return () => this.frameListeners.delete(l);
  }

  onStatus(l: Listener<LinkStatus>): Unsubscribe {
    this.statusListeners.add(l);
    return () => this.statusListeners.delete(l);
  }

  onRefusal(l: Listener<WireRefusalError>): Unsubscribe {
    this.refusalListeners.add(l);
    return () => this.refusalListeners.delete(l);
  }

  onEvent(l: Listener<Frame>): Unsubscribe {
    this.eventListeners.add(l);
    return () => this.eventListeners.delete(l);
  }

  private requireCorrelator(what: string): Correlator {
    if (!this.correlator || this.status.phase !== "ready") {
      throw new ConnectionClosedError(`${what}: not connected`);
    }
    return this.correlator;
  }

  private setStatus(s: LinkStatus): void {
    this.status = s;
    fanout(this.statusListeners, s, "status");
  }
}
