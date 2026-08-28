// A `Transport` built on the wire package's `FrameChannel` seam.
//
// The wire package defines `Transport` (what the app needs) and `FrameChannel`
// (send a frame, receive frames, close) but implements neither: the byte-level
// half is the native plugin's job, because a JavaScript WebSocket cannot open
// the daemon's socket at all — the remote listener is RAW TLS 1.3 over TCP with
// a four-byte length prefix, not WSS, so there is no HTTP upgrade for a WebView
// to perform and no way for JS to pin a self-signed SPKI.
//
// So the native `LolaTransport` plugin owns TLS, pinning and framing, and this
// class owns everything above the frames: correlation, the client-side command
// allowlist, pane subscriptions, sequence-gap classification and connection
// state. Splitting it there is what keeps the Swift side small and lets the
// whole request/response and pane path be tested in Node against
// `FakeChannel` — none of which would be reachable if the correlation logic
// lived in Swift.
//
// It lives in wailsshim/ rather than wire/ only because wire/ is owned as a
// pure protocol mirror. It has no dependency on the shim and a human may
// reasonably decide it belongs one directory over.

import {
  CODE_UNKNOWN_CMD,
  CODE_UNSUPPORTED_VERSION,
  ConnectionClosedError,
  Correlator,
  MAX_PANE_NAME,
  WireError,
  WireRefusalError,
  alwaysFatalCode,
  base64ToBytes,
  bytesToBase64,
  classifyGap,
  clientMayAccept,
  commandDenied,
  ptyResizeFrame,
  ptyScrollFrame,
  ptyWriteFrame,
  subFrame,
  supportedFrameVersion,
  unsubFrame,
  requestFrame,
  validPaneName,
  type ConnectOptions,
  type ConnectionStatus,
  type Endpoint,
  type Frame,
  type FrameChannel,
  type PaneEvent,
  type PaneSubscription,
  type RequestFields,
  type ResyncPayload,
  type Transport,
  type TransportRequestOptions,
  type Unsubscribe,
  type Viewport,
} from "../wire";
import { helloFrame, FRAME_ERR, FRAME_PTY, FRAME_RESYNC } from "../wire";

/** How a channel is opened for an endpoint. Supplied by the native plugin. */
export type ChannelFactory = (endpoint: Endpoint, opts?: ConnectOptions) => Promise<FrameChannel>;

export interface ChannelTransportOptions {
  open: ChannelFactory;
  /** Per-request deadline. Defaults to the correlator's own 15s. */
  timeoutMs?: number;
  /**
   * Who performs the M1 bearer handshake.
   *
   *   "in-band"  (default) this transport sends the `remote.hello` frame itself
   *              once the channel is open. The shape a raw byte channel needs.
   *   "channel"  the channel is already authenticated when it is handed over.
   *              What the native plugin does — its `connect` only resolves once
   *              the daemon has accepted the key.
   *
   * Getting this wrong is not a cosmetic duplicate. A second hello on an
   * already authenticated connection arrives as an ordinary `req` naming a
   * command in the `remote.` namespace, which the daemon denies — and a denied
   * command is FATAL there: one err frame and the socket closes.
   */
  handshake?: "in-band" | "channel";
}

type Listener<T> = (v: T) => void;

function fanout<T>(set: Set<Listener<T>>, v: T, what: string): void {
  for (const l of [...set]) {
    try {
      l(v);
    } catch (err) {
      console.error(`wailsshim: ${what} listener threw`, err);
    }
  }
}

// ---------------------------------------------------------------------------
// Pane subscriptions
// ---------------------------------------------------------------------------

class ChannelPaneSubscription implements PaneSubscription {
  readonly pane: string;
  readonly id: string;
  screen: ResyncPayload | null = null;
  lastSeq = 0;
  exited = false;

  private readonly events = new Set<Listener<PaneEvent>>();
  private readonly errors = new Set<Listener<WireRefusalError>>();

  constructor(
    pane: string,
    id: string,
    private readonly owner: ChannelTransport,
  ) {
    this.pane = pane;
    this.id = id;
  }

  onEvent(l: Listener<PaneEvent>): Unsubscribe {
    this.events.add(l);
    return () => this.events.delete(l);
  }

  onError(l: Listener<WireRefusalError>): Unsubscribe {
    this.errors.add(l);
    return () => this.errors.delete(l);
  }

  /**
   * Route one frame addressed to this pane.
   *
   * Called for EVERY resync on the pane, the subscription's own acknowledgement
   * included. That is deliberate: the ack carries the first screen, and having
   * one place maintain `screen`/`lastSeq` means the state cannot depend on
   * whether a frame happened to be the one a pending `subscribe()` was waiting
   * for. The ack's event is delivered to no listeners (nobody has subscribed
   * yet at that point), which is harmless — `screen` is what the caller reads.
   */
  handle(f: Frame): void {
    if (f.type === FRAME_RESYNC) {
      const verdict = classifyGap(this.lastSeq, f);
      const screen = (f.payload ?? {}) as ResyncPayload;
      this.screen = screen;
      if (typeof f.seq === "number" && f.seq > 0) this.lastSeq = f.seq;

      if (screen.exited) {
        // A pane death arrives as a resync carrying `exited`, not as a distinct
        // frame type, and the daemon drops the subscription from its
        // authorization map BEFORE writing it — so anything typed in reply
        // comes back as `unknown_pane`. Retire the subscription here for the
        // same reason.
        this.exited = true;
        this.owner.retire(this.pane);
        fanout(this.events, { kind: "exit", screen, seq: this.lastSeq }, "pane");
        return;
      }

      // A gap that ARRIVES ON A RESYNC is self-healing: the bus drops a frame
      // for an overflowing subscriber, keeps its sequence advancing so the gap
      // stays visible, then repairs itself with a fresh full screen. Repaint
      // and adopt the new sequence — only a gap arriving on a `pty` frame is
      // a reason to re-subscribe.
      fanout(
        this.events,
        { kind: "resync", screen, seq: this.lastSeq, repaired: verdict === "repaired" },
        "pane",
      );
      return;
    }

    if (f.type === FRAME_PTY) {
      const verdict = classifyGap(this.lastSeq, f);
      const payload = (f.payload ?? {}) as { data?: string | null };
      // `data` carries no omitempty on the Go side and a nil []byte marshals as
      // JSON null, so both "" and null are reachable and neither is malformed.
      const data = base64ToBytes(payload.data);
      if (typeof f.seq === "number" && f.seq > 0) this.lastSeq = f.seq;
      fanout(
        this.events,
        { kind: "output", data, seq: this.lastSeq, gap: verdict === "torn" },
        "pane",
      );
      return;
    }

    if (f.type === FRAME_ERR) {
      fanout(this.errors, WireRefusalError.fromFrame(f), "pane error");
    }
  }

  /** Report a connection-level failure to this pane's error listeners. */
  fail(err: WireRefusalError): void {
    fanout(this.errors, err, "pane error");
  }

  async write(data: Uint8Array | string): Promise<void> {
    if (this.exited) throw new WireError(`wire: pane ${this.pane} has exited`);
    const bytes = typeof data === "string" ? new TextEncoder().encode(data) : data;
    if (bytes.length === 0) return; // the daemon ignores an empty write anyway
    // Sent WITHOUT a correlation id, for the same reason `unsub` carries none:
    // a successful pty write is never acknowledged, so an id would either leak
    // a pending promise until its deadline or resolve on a timeout that means
    // nothing. A refusal echoes the PANE, which is all the routing this needs.
    await this.owner.sendFrame(ptyWriteFrame(this.pane, bytesToBase64(bytes)));
  }

  async scroll(lines: number): Promise<void> {
    if (this.exited) return;
    if (!Number.isFinite(lines) || Math.trunc(lines) === 0) return;
    // ptyScrollFrame clamps to the daemon's own MaxScrollLines.
    await this.owner.sendFrame(ptyScrollFrame(this.pane, Math.trunc(lines)));
  }

  async resize(cols: number, rows: number): Promise<void> {
    if (this.exited) return;
    // Recorded and ignored by the daemon in M1: the pane is attached at the
    // developer's tmux window size and a phone-sized viewport pans. Sent anyway
    // so the daemon has the client's geometry the moment it starts using it.
    await this.owner.sendFrame(ptyResizeFrame(this.pane, cols, rows));
  }

  async close(): Promise<void> {
    this.owner.retire(this.pane);
    if (this.exited) return;
    // Unacknowledged by design — do not wait for a reply that never comes.
    await this.owner.sendFrame(unsubFrame(this.pane));
  }
}

// ---------------------------------------------------------------------------
// The transport
// ---------------------------------------------------------------------------

export class ChannelTransport implements Transport {
  status: ConnectionStatus = { phase: "idle" };

  private channel: FrameChannel | null = null;
  private correlator: Correlator | null = null;
  private endpoint: Endpoint | null = null;
  private connecting: Promise<void> | null = null;

  private readonly subs = new Map<string, ChannelPaneSubscription>();
  private readonly frameListeners = new Set<Listener<Frame>>();
  private readonly statusListeners = new Set<Listener<ConnectionStatus>>();
  private readonly refusalListeners = new Set<Listener<WireRefusalError>>();

  constructor(private readonly opts: ChannelTransportOptions) {}

  // --- lifecycle -----------------------------------------------------------

  async connect(endpoint: Endpoint, opts?: ConnectOptions): Promise<void> {
    if (this.connecting) return this.connecting;
    if (this.status.phase === "ready") return;

    this.connecting = this.doConnect(endpoint, opts).finally(() => {
      this.connecting = null;
    });
    return this.connecting;
  }

  private async doConnect(endpoint: Endpoint, opts?: ConnectOptions): Promise<void> {
    this.endpoint = endpoint;
    this.setStatus({ phase: "connecting", endpoint });
    let channel: FrameChannel;
    try {
      channel = await this.opts.open(endpoint, opts);
    } catch (err) {
      this.setStatus({ phase: "closed", endpoint, error: err as Error });
      throw err;
    }

    this.channel = channel;
    const correlator = new Correlator({
      send: (f) => channel.send(f),
      timeoutMs: this.opts.timeoutMs,
    });
    this.correlator = correlator;

    channel.onFrame((f) => this.route(f));
    channel.onClose((err) => this.onClosed(err));

    this.setStatus({ phase: "handshaking", endpoint });

    // M1's authenticator is a bearer key, and its hello is the ONE frame that
    // does not go through `request`: `remote.hello` is in the `remote.` prefix
    // the client-side denial mirror refuses, and its payload is `{key}` rather
    // than a protocol.Request. It is also the first frame on the connection by
    // contract — anything else before it is answered `denied` and closed.
    if (this.opts.handshake !== "channel" && endpoint.insecureKey) {
      try {
        await correlator.request(helloFrame(correlator.nextID(), endpoint.insecureKey), {
          immediate: true,
          timeoutMs: opts?.timeoutMs,
        });
      } catch (err) {
        await this.teardown(err as Error);
        throw err;
      }
    }

    this.setStatus({ phase: "ready", endpoint });
  }

  async disconnect(reason = "client disconnected"): Promise<void> {
    await this.teardown(new ConnectionClosedError(reason));
  }

  private async teardown(err: Error): Promise<void> {
    const ch = this.channel;
    this.channel = null;
    this.correlator?.failAll(err);
    this.correlator = null;

    const refusal =
      err instanceof WireRefusalError
        ? err
        : new WireRefusalError("connection_closed", err.message);
    for (const sub of this.subs.values()) sub.fail(refusal);
    this.subs.clear();

    this.setStatus({ phase: "closed", endpoint: this.endpoint ?? undefined, error: err });
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

    // Direction and version are checked here rather than in the native plugin:
    // the plugin should move bytes, and a client that renders a frame it is not
    // supposed to receive is exactly the class of bug the direction table
    // exists to prevent.
    if (!clientMayAccept(f)) {
      this.refuse(
        new WireRefusalError(
          "unknown_type",
          `client received an unacceptable frame (type ${String(f.type)}, v ${String(f.v)})`,
        ),
      );
      return;
    }

    // An `err` frame is readable at ANY version and must stay that way: its
    // field layout is frozen at v1 forever precisely so a peer that understands
    // nothing else can still read `unsupported_version` and say which side is
    // behind. Every other type has to be inside the window, and a frame outside
    // it is refused rather than guessed at — the daemon takes exactly this
    // posture in the other direction.
    if (f.type !== FRAME_ERR && !supportedFrameVersion(f.v)) {
      this.refuse(
        new WireRefusalError(
          CODE_UNSUPPORTED_VERSION,
          `the daemon sent envelope version ${String(f.v)}, which this client does not speak`,
        ),
      );
      return;
    }

    // The pane's own state first, so `screen` and `lastSeq` are maintained
    // uniformly whether or not a pending request happens to be waiting on this
    // frame's id — see ChannelPaneSubscription.handle.
    if (f.pane) this.subs.get(f.pane)?.handle(f);

    // A FATAL refusal is read off every `err` frame, correlated or not.
    //
    // Every fatal code the daemon produces — `unknown_cmd` and `denied` from
    // conn.authorize, `unsupported_version` from readValidated — is written in
    // reply to a frame the client sent, so it carries that frame's correlation
    // id and the correlator settles it. Consulting `alwaysFatalCode` only for
    // the UNCORRELATED remainder therefore made the check dead for exactly the
    // codes it exists to catch: the client would learn the connection was gone
    // only when the socket close arrived, and a close that is delayed or
    // swallowed leaves the transport reporting `ready` over a dead pipe.
    //
    // Read the code BEFORE `accept` settles the request, then let the request
    // reject on its own terms, then tear down. The pending promise still gets
    // the specific refusal it was waiting for; the connection still dies.
    const fatal =
      f.type === FRAME_ERR ? WireRefusalError.fromFrame(f) : undefined;

    if (this.correlator?.accept(f)) {
      if (fatal && alwaysFatalCode(fatal.code)) {
        this.refuse(fatal);
        void this.teardown(fatal);
      }
      return;
    }

    // Not correlated. An `err` with no pending id is a connection-level event,
    // and several of them are followed immediately by a close.
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

  async request<T = unknown>(
    cmd: string,
    fields: RequestFields = {},
    opts: TransportRequestOptions = {},
  ): Promise<T> {
    // The client-side mirror of the daemon's denial list, checked BEFORE
    // anything is sent. This is not belt and braces: a denied command is a
    // FATAL refusal on the daemon side — it writes one err frame and closes the
    // socket — so a single mistyped command would cost every live pane
    // subscription and a full reconnect. Refusing locally costs one rejection.
    if (commandDenied(cmd)) {
      throw new WireRefusalError(
        CODE_UNKNOWN_CMD,
        `${cmd} is not available remotely (refused by the client's own allowlist, so the connection survives)`,
      );
    }
    const correlator = this.requireCorrelator(cmd);
    return correlator.request<T>(requestFrame(correlator.nextID(), cmd, fields), {
      timeoutMs: opts.timeoutMs,
      signal: opts.signal,
    });
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
      // The subscription's acknowledgement IS its first resync, correlated on
      // the sub frame's id. Note that every LATER resync for this pane carries
      // the same id, which is why the correlator settles once and reports
      // itself unmatched afterwards — and why this id must never be reused for
      // an ordinary request.
      await correlator.request<ResyncPayload>(subFrame(id, pane, viewport), {
        expect: FRAME_RESYNC,
        timeoutMs: opts.timeoutMs,
        signal: opts.signal,
      });
    } catch (err) {
      this.subs.delete(pane);
      // A REFUSAL means the daemon created nothing: `remote.conn.subscribe`
      // answers `err` and registers no subscription, so there is nothing to
      // undo. A local timeout or an abort is the opposite case — the daemon
      // registers the subscription before its pump writes the first resync, so
      // giving up here can leave it pumping a pane nothing routes, holding a
      // panebus subscription and its tmux attach for the life of the connection.
      // `unsub` is unacknowledged by design, so undoing it costs one frame and
      // no wait.
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

  /** Drop a pane from the routing table. Called on exit and on close(). */
  retire(pane: string): void {
    this.subs.delete(pane);
  }

  /** Send one uncorrelated frame. Used by pane writes, scrolls and unsub. */
  async sendFrame(f: Frame): Promise<void> {
    const ch = this.channel;
    if (!ch) throw new ConnectionClosedError();
    await ch.send(f);
  }

  // --- observers -----------------------------------------------------------

  onFrame(l: Listener<Frame>): Unsubscribe {
    this.frameListeners.add(l);
    return () => this.frameListeners.delete(l);
  }

  onStatus(l: Listener<ConnectionStatus>): Unsubscribe {
    this.statusListeners.add(l);
    return () => this.statusListeners.delete(l);
  }

  onRefusal(l: Listener<WireRefusalError>): Unsubscribe {
    this.refusalListeners.add(l);
    return () => this.refusalListeners.delete(l);
  }

  // --- internals -----------------------------------------------------------

  private requireCorrelator(what: string): Correlator {
    if (!this.correlator || this.status.phase === "closed") {
      throw new ConnectionClosedError(`${what}: not connected`);
    }
    return this.correlator;
  }

  private setStatus(s: ConnectionStatus): void {
    this.status = s;
    fanout(this.statusListeners, s, "status");
  }
}
