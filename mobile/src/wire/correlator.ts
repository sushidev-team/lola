// Request/response correlation over one multiplexed connection.
//
// The daemon runs `req` frames CONCURRENTLY and answers them out of order —
// that is the entire point of `Frame.id` (internal/remote/conn.go dispatchReq
// hands each one to a goroutine behind a semaphore, and only `sub`/`unsub`/`pty`
// are ordered against each other on a single worker). So a client that assumes
// replies arrive in the order it asked is wrong, and one that keys nothing on
// the id can only ever have one request outstanding.
//
// Four properties this file is responsible for, in the order they cost you if
// they are missing:
//
//   1. NEVER LEAK A PENDING PROMISE. A request whose reply never comes — the
//      socket dropped, the daemon closed on a denied cmd, the phone went into
//      the background and the OS SIGSTOPped the queue — must reject, not hang.
//      Every pending entry is therefore reachable from `failAll`, every entry
//      carries a total deadline, and both paths clear the timer.
//   2. An `err` frame on a request's id is a REJECTION, not a silent drop.
//   3. An `ok: false` Response is a rejection too. The daemon distinguishes a
//      protocol refusal from an application failure; every caller in the desktop
//      store treats them identically, and so does this.
//   4. Stay under the daemon's in-flight cap. More than four concurrent `req`
//      frames on one connection is answered with a non-fatal `rate_limited`
//      refusal, so a burst is QUEUED here rather than sent and refused.
//
// A pane subscription's ack is also correlated, and it is the one asymmetric
// case: a `sub` frame is acknowledged by the FIRST `resync` carrying its id, and
// every LATER resync on that pane carries the same id (the daemon's pump sets
// `out.ID = s.id` on all of them). So `accept` settles the first and reports
// itself unmatched for the rest, and the caller keeps routing resyncs to the
// pane either way. See the note on `accept`.

import {
  CODE_RATE_LIMITED,
  FRAME_ERR,
  FRAME_RESP,
  FRAME_RESYNC,
  MAX_REQUESTS_IN_FLIGHT,
  type ErrPayload,
  type Frame,
  type Response,
} from "./protocol";
import { WireError } from "./codec";

/** A refusal the daemon sent as an `err` frame. */
export class WireRefusalError extends WireError {
  readonly code: string;
  readonly pane?: string;
  readonly minV?: number;
  readonly maxV?: number;

  constructor(code: string, message: string, pane?: string, minV?: number, maxV?: number) {
    super(message ? `${code}: ${message}` : code);
    this.name = "WireRefusalError";
    this.code = code;
    this.pane = pane;
    this.minV = minV;
    this.maxV = maxV;
  }

  static fromFrame(f: Frame): WireRefusalError {
    const p = (f.payload ?? {}) as Partial<ErrPayload>;
    return new WireRefusalError(
      typeof p.code === "string" ? p.code : "unknown",
      typeof p.message === "string" ? p.message : "",
      f.pane,
      typeof p.minV === "number" ? p.minV : undefined,
      typeof p.maxV === "number" ? p.maxV : undefined,
    );
  }

  /** True for the one refusal a client can act on precisely: a version skew. */
  get isVersionSkew(): boolean {
    return this.minV !== undefined && this.maxV !== undefined;
  }
}

/** A `resp` frame whose Response carried `ok: false`. */
export class DaemonError extends WireError {
  readonly cmd: string;
  constructor(cmd: string, message: string) {
    super(message || `${cmd} failed`);
    this.name = "DaemonError";
    this.cmd = cmd;
  }
}

/** A request that reached its deadline with no reply. */
export class RequestTimeoutError extends WireError {
  readonly cmd: string;
  readonly timeoutMs: number;
  constructor(cmd: string, timeoutMs: number) {
    super(`${cmd || "request"} timed out after ${timeoutMs}ms`);
    this.name = "RequestTimeoutError";
    this.cmd = cmd;
    this.timeoutMs = timeoutMs;
  }
}

/** Every pending request is rejected with this when the connection goes away. */
export class ConnectionClosedError extends WireError {
  constructor(reason?: string) {
    super(reason ? `connection closed: ${reason}` : "connection closed");
    this.name = "ConnectionClosedError";
  }
}

/** Which frame type settles a given correlation id. */
export type ExpectKind = typeof FRAME_RESP | typeof FRAME_RESYNC;

export interface CorrelatorOptions {
  /**
   * Put one frame on the wire. It may return a promise; a rejection rejects the
   * request rather than escaping as an unhandled rejection.
   */
  send(frame: Frame): void | Promise<void>;

  /**
   * The default TOTAL deadline for a request, covering both the time it spends
   * queued behind the in-flight cap and the time it spends on the wire.
   *
   * A total deadline rather than a flight deadline is a deliberate choice: it is
   * the only shape under which "never leak a pending promise" holds without
   * qualification, since a request that never gets a slot would otherwise wait
   * forever. Callers with a genuinely slow command pass a larger one.
   */
  timeoutMs?: number;

  /**
   * How many `req` frames may be outstanding. Defaults to the daemon's own cap
   * so a burst waits here instead of being refused there.
   */
  maxInFlight?: number;

  /**
   * Timer seams, so tests can drive the clock without waiting. They default to
   * the globals and exist for the same reason every exec call in the Go daemon
   * is a struct field.
   */
  setTimer?: (fn: () => void, ms: number) => unknown;
  clearTimer?: (handle: unknown) => void;

  /** Prefix for generated correlation ids; useful when reading a packet log. */
  idPrefix?: string;
}

export interface RequestOptions {
  /** Overrides the correlator's default total deadline. */
  timeoutMs?: number;
  /** Which frame type settles this id. Defaults to `resp`. */
  expect?: ExpectKind;
  /** Cancels the request locally. The daemon is NOT told; there is no cancel frame. */
  signal?: AbortSignal;
  /**
   * Bypass the in-flight queue. Reserved for the handshake, which must be the
   * FIRST frame on the connection and is answered before any other request may
   * legitimately be sent.
   */
  immediate?: boolean;
}

interface Pending {
  id: string;
  cmd: string;
  expect: ExpectKind;
  resolve(value: unknown): void;
  reject(err: Error): void;
  timer: unknown;
  onAbort?: () => void;
  signal?: AbortSignal;
  /**
   * Whether this request has taken one of the in-flight slots. A request
   * settled while it is still QUEUED never took one, and releasing a slot it
   * does not hold is how an in-flight counter drifts below zero and then lets
   * the connection exceed the daemon's cap.
   */
  slotted: boolean;
}

/**
 * The multiplexer. One per connection; a reconnect builds a new one, because
 * nothing is resumed across a reconnect — ids restart, pane sequence numbers
 * restart, and every subscription is made again from scratch.
 */
export class Correlator {
  private readonly opts: Required<
    Pick<CorrelatorOptions, "timeoutMs" | "maxInFlight" | "idPrefix">
  > &
    CorrelatorOptions;
  private readonly pending = new Map<string, Pending>();
  private readonly queue: Array<() => void> = [];
  private inFlightCount = 0;
  private counter = 0;
  private closed: Error | null = null;

  constructor(opts: CorrelatorOptions) {
    // Filled field by field rather than by spreading over defaults. A spread
    // lets an EXPLICIT undefined win — which is exactly what an options
    // forwarder produces (`{ maxInFlight: opts.maxInFlight }` with nothing
    // set) — and a maxInFlight of undefined makes `count < cap` false forever,
    // so every request queues and none is ever sent. That is a hang, not an
    // error, which is the worst shape this class can fail in.
    this.opts = {
      ...opts,
      timeoutMs: opts.timeoutMs ?? 15_000,
      maxInFlight: opts.maxInFlight ?? MAX_REQUESTS_IN_FLIGHT,
      idPrefix: opts.idPrefix ?? "r",
    };
  }

  /** How many requests are awaiting a reply right now. */
  get inFlight(): number {
    return this.inFlightCount;
  }

  /** How many requests are waiting for a slot. */
  get queued(): number {
    return this.queue.length;
  }

  /** Every id awaiting settlement, in-flight or not. Diagnostics only. */
  get pendingIds(): string[] {
    return [...this.pending.keys()];
  }

  /**
   * A fresh correlation id, unique for the lifetime of this correlator.
   *
   * Uniqueness matters beyond tidiness: a pane subscription holds its id for as
   * long as the subscription lives, and every resync on that pane echoes it. An
   * id reused for a later request would take the next resync as its reply.
   */
  nextID(): string {
    this.counter++;
    return `${this.opts.idPrefix}${this.counter}`;
  }

  /**
   * Send `frame` and await the reply correlated to its id. An id is generated
   * when the frame carries none.
   *
   * Resolves with the Response's `data` for a `resp`, and with the ResyncPayload
   * for a `resync`. Rejects with WireRefusalError (an `err` frame on this id),
   * DaemonError (`ok: false`), RequestTimeoutError, or ConnectionClosedError.
   */
  request<T = unknown>(frame: Frame, opts: RequestOptions = {}): Promise<T> {
    if (this.closed) return Promise.reject(this.closed);

    // `||` and not `??`: a caller that builds a frame with an empty id means
    // "assign one", and nullish coalescing would keep the empty string — after
    // which every such request shares one correlation slot.
    const id = frame.id || this.nextID();
    const withID: Frame = frame.id ? frame : { ...frame, id };
    const cmd = frame.cmd || frame.type;
    const expect: ExpectKind = opts.expect ?? FRAME_RESP;
    const timeoutMs = opts.timeoutMs ?? this.opts.timeoutMs;

    if (this.pending.has(id)) {
      return Promise.reject(new WireError(`wire: correlation id ${id} is already in flight`));
    }

    return new Promise<T>((resolve, reject) => {
      if (opts.signal?.aborted) {
        reject(abortError(opts.signal));
        return;
      }

      const entry: Pending = {
        id,
        cmd,
        expect,
        resolve: resolve as (v: unknown) => void,
        reject,
        timer: undefined,
        signal: opts.signal,
        slotted: false,
      };

      // The deadline is armed BEFORE the slot is taken, so a request that never
      // gets one still rejects. This is the whole of property 1.
      entry.timer = this.setTimer(() => {
        this.settle(id, () => reject(new RequestTimeoutError(cmd, timeoutMs)));
      }, timeoutMs);

      if (opts.signal) {
        entry.onAbort = () => this.settle(id, () => reject(abortError(opts.signal!)));
        opts.signal.addEventListener("abort", entry.onAbort, { once: true });
      }

      this.pending.set(id, entry);

      const dispatch = () => {
        // The request may have been settled while it sat in the queue — a
        // timeout, an abort, a disconnect. Sending it then would put a frame on
        // the wire whose reply nobody is waiting for, and the daemon would count
        // it against the in-flight cap for nothing.
        const live = this.pending.get(id);
        if (!live) {
          this.releaseSlot();
          return;
        }
        // Marked slotted BEFORE the send, so `settle` owns the slot from here
        // on — including on the failure paths below. Releasing it a second time
        // by hand is not a harmless double-decrement: `releaseSlot` shifts a
        // queued request onto the wire each time it runs, so one failed send
        // would dispatch TWO queued requests while `inFlightCount` recorded
        // one, and the connection would carry five concurrent `req` frames
        // against the daemon's cap of four. The daemon answers the fifth with
        // `rate_limited`, which is the exact outcome this class exists to
        // prevent.
        live.slotted = true;
        try {
          const r = this.opts.send(withID);
          if (r && typeof (r as Promise<void>).then === "function") {
            (r as Promise<void>).catch((e: unknown) => {
              this.settle(id, () => reject(toError(e)));
            });
          }
        } catch (e) {
          this.settle(id, () => reject(toError(e)));
        }
      };

      if (opts.immediate) {
        // The handshake bypasses the queue entirely and never takes a slot: it
        // is the first frame on the connection and nothing else may be in
        // flight beside it.
        try {
          const r = this.opts.send(withID);
          if (r && typeof (r as Promise<void>).then === "function") {
            (r as Promise<void>).catch((e: unknown) => this.settle(id, () => reject(toError(e))));
          }
        } catch (e) {
          this.settle(id, () => reject(toError(e)));
        }
        return;
      }
      if (this.inFlightCount < this.opts.maxInFlight) {
        this.inFlightCount++;
        dispatch();
      } else {
        this.queue.push(() => {
          this.inFlightCount++;
          dispatch();
        });
      }
    });
  }

  /**
   * Offer one inbound frame for correlation.
   *
   * Returns true when the frame SETTLED a pending request. It does NOT mean the
   * caller may stop routing the frame:
   *
   *   resp    settled means fully handled; nothing else consumes a resp.
   *   resync  settled means this was a subscription's ACK — and it still carries
   *           the pane's first screen, so the caller must deliver it to the pane
   *           as well. Every LATER resync on that pane echoes the same id and
   *           returns false here; that is not a stray frame, it is the normal
   *           case, and the pane router is its only consumer.
   *   err     settled means the refusal belonged to a request. An err with no id,
   *           or with an id nobody is waiting on, returns false and is a
   *           CONNECTION-level event the caller must surface — several of them
   *           are followed immediately by a close.
   */
  accept(f: Frame): boolean {
    if (f.type !== FRAME_RESP && f.type !== FRAME_RESYNC && f.type !== FRAME_ERR) return false;
    const id = f.id;
    if (!id) return false;
    const entry = this.pending.get(id);
    if (!entry) return false;

    if (f.type === FRAME_ERR) {
      const err = WireRefusalError.fromFrame(f);
      this.settle(id, () => entry.reject(err));
      // A rate_limited refusal on a request means the daemon's in-flight cap was
      // exceeded, which this correlator is supposed to prevent. It survives the
      // connection, so it is worth noticing rather than swallowing: shrink the
      // local cap toward what the daemon actually enforced.
      if (err.code === CODE_RATE_LIMITED && this.opts.maxInFlight > 1) {
        this.opts.maxInFlight--;
      }
      return true;
    }

    if (entry.expect !== f.type) {
      // A resync answering a request that wanted a resp, or the reverse. Neither
      // is producible by the daemon today; refusing it is cheaper than reasoning
      // about what it would mean.
      this.settle(id, () =>
        entry.reject(new WireError(`wire: expected a ${entry.expect} on ${id}, got a ${f.type}`)),
      );
      return true;
    }

    if (f.type === FRAME_RESYNC) {
      this.settle(id, () => entry.resolve(f.payload));
      return true;
    }

    const resp = (f.payload ?? { ok: false }) as Response;
    if (resp.ok === false) {
      this.settle(id, () => entry.reject(new DaemonError(entry.cmd, resp.error ?? "")));
      return true;
    }
    this.settle(id, () => entry.resolve(resp.data));
    return true;
  }

  /**
   * Reject every pending request and refuse every future one.
   *
   * Called from the transport's close path — a clean disconnect, a socket error,
   * a fatal refusal, an app backgrounding. After it the correlator is spent: a
   * reconnect builds a new one, because nothing about the old connection's ids
   * or sequence numbers survives.
   */
  failAll(err: Error = new ConnectionClosedError()): void {
    this.closed = err;
    const entries = [...this.pending.values()];
    this.pending.clear();
    this.queue.length = 0;
    this.inFlightCount = 0;
    for (const e of entries) {
      this.clearEntryTimers(e);
      e.reject(err);
    }
  }

  // --- internals ---------------------------------------------------------

  /**
   * Remove an entry, clear its timers, run `finish`, and hand its slot to the
   * next queued request. Idempotent by construction: an id already gone settles
   * nothing, which is what makes a timeout racing a reply harmless.
   */
  private settle(id: string, finish: () => void): void {
    const entry = this.pending.get(id);
    if (!entry) return;
    this.pending.delete(id);
    this.clearEntryTimers(entry);
    finish();
    if (entry.slotted) this.releaseSlot();
  }

  private clearEntryTimers(e: Pending): void {
    if (e.timer !== undefined) this.clearTimer(e.timer);
    if (e.onAbort && e.signal) e.signal.removeEventListener("abort", e.onAbort);
  }

  private releaseSlot(): void {
    if (this.inFlightCount > 0) this.inFlightCount--;
    if (this.inFlightCount < this.opts.maxInFlight && this.queue.length > 0) {
      this.queue.shift()!();
    }
  }

  private setTimer(fn: () => void, ms: number): unknown {
    return this.opts.setTimer ? this.opts.setTimer(fn, ms) : setTimeout(fn, ms);
  }

  private clearTimer(h: unknown): void {
    if (this.opts.clearTimer) this.opts.clearTimer(h);
    else clearTimeout(h as ReturnType<typeof setTimeout>);
  }
}

function toError(e: unknown): Error {
  return e instanceof Error ? e : new WireError(String(e));
}

function abortError(signal: AbortSignal): Error {
  const reason = (signal as { reason?: unknown }).reason;
  if (reason instanceof Error) return reason;
  const err = new WireError("request aborted");
  err.name = "AbortError";
  return err;
}
