// The Transport seam: what the app codes against, and what the native
// LolaTransport plugin implements.
//
// NOTHING IN THIS FILE IS PLATFORM-SPECIFIC. It names no Capacitor API, no
// WebSocket, no Network.framework type and no Wails binding. That is the point:
// the app builds against this interface, the iOS plugin bridge implements it,
// an in-memory fake implements it for tests, and — if M1's terminal experiment
// fails and the terminal screen has to become a native SwiftTerm view — that
// swap happens behind this same interface with nothing else in the plan moving.
//
// WHY THIS IS NOT A WebSocket INTERFACE. mobile/PLAN.md describes the transport
// as WSS and relies on close codes 4403 and 4503. The daemon that is actually in
// the tree does neither: internal/remote is a RAW TLS 1.3 LISTENER over TCP
// (tls.NewListener in server.go) speaking the length-prefixed frame codec, with
// no HTTP upgrade anywhere in the package and no close codes of any kind — it
// just closes the socket. A WKWebView cannot open that connection at all, which
// is precisely why PLAN.md's M1 section puts the socket in the native plugin.
// So this interface deliberately exposes CONNECTION STATE rather than close
// codes, and a reason string rather than a number.
//
// The obligations a conforming implementation has to the daemon, all of them
// read off internal/remote/server.go and conn.go:
//
//   - DRAIN THE SOCKET CONTINUOUSLY. Every server-side frame write is bounded at
//     15 s and a client that stops reading has its connection torn down. Buffer
//     on the client; never apply backpressure to the daemon.
//   - EXPECT SILENCE. There is no application-level keepalive in either
//     direction, and the daemon clears its read deadline after the handshake. An
//     attached pane is legitimately silent for minutes. TCP keepalive (30 s idle,
//     10 s interval, 3 probes) is the only liveness signal, so a dead peer takes
//     about a minute to notice.
//   - ONE READER. Frames are delivered in arrival order on one path.
//   - UNIQUE IDS. An id must be unique among everything in flight, and a pane
//     subscription's id stays in use for the whole life of the subscription.
//   - NOTHING RESUMES ACROSS A RECONNECT. New TCP, new TLS, a fresh hello, and
//     every pane subscribed again from scratch. Sequence numbers restart.

import type {
  ErrPayload,
  Frame,
  RequestFields,
  ResyncPayload,
  SubPayload,
} from "./protocol";
import type { WireRefusalError } from "./correlator";

/** Every listener registration returns its own unsubscribe. */
export type Unsubscribe = () => void;

// ---------------------------------------------------------------------------
// Endpoint and connection state
// ---------------------------------------------------------------------------

/**
 * Where to connect and how to prove the peer is the right one.
 *
 * The pin is not optional and there is no "trust the system store" mode.
 * `~/.lola/device.crt` is a self-signed ECDSA P-256 certificate whose only SAN
 * is the DNS name "lola" plus the two loopback addresses, so ordinary chain
 * validation cannot succeed and hostname verification would fail even before it.
 * The client therefore replaces trust evaluation entirely and compares the
 * server's SPKI hash itself — on iOS via
 * `sec_protocol_options_set_verify_block`, never `SecPolicyCreateSSL`.
 *
 * There is no in-band way to learn the pin in M1: the daemon logs it at startup
 * ("remote: phone listener up on ..., SPKI pin ...") and the operator carries it
 * across. M2's QR pairing replaces that.
 */
export interface Endpoint {
  host: string;
  /** Defaults to DEFAULT_REMOTE_PORT (7717). */
  port?: number;
  /**
   * base64(SHA-256(SubjectPublicKeyInfo)), standard alphabet WITH padding, as
   * printed by the daemon. Mirrors `remote.DeviceKey.SPKIPin`.
   */
  spkiPin: string;
  /**
   * M1's bearer key, the value of LOLA_REMOTE_INSECURE_KEY on the daemon's
   * machine. At least INSECURE_MIN_KEY_LEN characters or the daemon's listener
   * refuses to start.
   *
   * A RUNTIME value only: it comes from the environment or from a field the
   * operator types into the app. It is never committed to this repository, never
   * written to a log, and never placed in a URL. M2 deletes this field along
   * with the whole bearer path.
   */
  insecureKey?: string;
}

/**
 * Where a connection is in its life.
 *
 *   idle          never connected, or cleanly disconnected.
 *   connecting    TCP, then TLS, then the SPKI pin comparison.
 *   handshaking   the bearer hello is on the wire, awaiting its resp.
 *   ready         authenticated; requests and subscriptions are permitted.
 *   closed        this attempt is over. `error` says why when it was not asked for.
 */
export type ConnectionPhase = "idle" | "connecting" | "handshaking" | "ready" | "closed";

export interface ConnectionStatus {
  phase: ConnectionPhase;
  /** Set on an involuntary close, and on a failed connect. */
  error?: Error;
  /**
   * A refusal the daemon sent before closing, when there was one. It is what
   * separates the three failures a human can act on — a version skew names which
   * side is behind, `denied` means the bearer key is wrong, `frame_too_large`
   * means this client has a bug — from the many that just look like a dropped
   * socket.
   */
  refusal?: ErrPayload;
  /** Present from `connecting` onward. */
  endpoint?: Endpoint;
}

// ---------------------------------------------------------------------------
// Pane subscriptions
// ---------------------------------------------------------------------------

/** The advisory viewport carried on a `sub`. */
export type Viewport = SubPayload;

/**
 * What arrived on a pane, already decoded and already checked for a sequence
 * gap. The three kinds match the three things that can happen to a pane, and
 * they stay distinguishable because a DEATH and an unsubscribe must not look the
 * same at the client.
 */
export interface PaneOutputEvent {
  kind: "output";
  /** Raw PTY bytes, base64-decoded. Feed them straight to the emulator. */
  data: Uint8Array;
  seq: number;
  /**
   * Set when this frame's sequence number skipped. On a pty frame that means the
   * daemon's bus dropped output for a subscriber that fell behind, and the bytes
   * cannot be replayed — re-subscribe rather than render the corruption.
   */
  gap?: boolean;
}

export interface PaneResyncEvent {
  kind: "resync";
  screen: ResyncPayload;
  seq: number;
  /**
   * A resync arriving after a sequence jump is the daemon REPAIRING a drop it
   * already detected: it marks the subscriber desynced, withholds output while
   * the counter advances, and sends a fresh full screen. Repaint from it and
   * adopt its number. Do not re-subscribe.
   */
  repaired?: boolean;
}

export interface PaneExitEvent {
  kind: "exit";
  /** The final screen, when there was one. `exited` is true on it. */
  screen: ResyncPayload;
  seq: number;
}

export type PaneEvent = PaneOutputEvent | PaneResyncEvent | PaneExitEvent;

/**
 * One attached pane.
 *
 * The subscription is REPLACED rather than refused if the same pane is
 * subscribed again — that is the daemon's defined recovery from a sequence gap,
 * so a client that noticed one has somewhere to go.
 */
export interface PaneSubscription {
  readonly pane: string;
  /** The `sub` frame's correlation id, echoed on every resync for this pane. */
  readonly id: string;
  /** The most recent screen, starting with the subscription's own ack. */
  readonly screen: ResyncPayload | null;
  /** The last sequence number seen on this pane. */
  readonly lastSeq: number;
  /** True once an exit frame has arrived; nothing follows it. */
  readonly exited: boolean;

  /**
   * Write raw bytes to the pane's PTY master.
   *
   * A string is UTF-8 encoded. The daemon cancels copy mode first, so a pane
   * that a scroll left scrolled back still receives the keystroke. An empty
   * write is silently ignored by the daemon and is skipped here.
   *
   * This deliberately bypasses lola's AtPrompt idle gate: that gate exists to
   * stop lola's own automation typing into a mid-turn agent, not a human.
   */
  write(data: Uint8Array | string): Promise<void>;

  /**
   * Scroll the pane's own transcript. POSITIVE SCROLLS BACK into history,
   * negative scrolls forward again; zero is a no-op. That is the daemon's
   * convention, not a wheel delta's, and it is the opposite of what a browser's
   * `deltaY` means — `internal/tmux/client.go`'s ScrollPane opens with
   * `up := lines > 0`, and the desktop's LiveTerminal flips the wheel's sign
   * for exactly this reason. Clamped to MAX_SCROLL_LINES, as the daemon clamps
   * it anyway.
   *
   * The client must NEVER synthesize wheel bytes itself. The daemon decides
   * between the program's own transcript and tmux copy mode, and an agent runs
   * full-screen where tmux keeps no scrollback at all — getting that choice
   * wrong is not a degraded scroll, it is no scroll.
   */
  scroll(lines: number): Promise<void>;

  /**
   * State the subscriber's viewport.
   *
   * RECORDED AND IGNORED in M1: the bus attaches at the developer's tmux window
   * size and fans the untruncated stream out, so a phone pans client-side. It is
   * sent so the daemon's record is honest, not because anything will change.
   */
  resize(cols: number, rows: number): Promise<void>;

  /**
   * Drop the subscription.
   *
   * The `unsub` frame is UNACKNOWLEDGED — there is no reply of any kind — so
   * this resolves as soon as the frame is on the wire. A client cannot
   * distinguish "unsubscribed" from "frame lost", and does not need to.
   */
  close(): Promise<void>;

  /** Every event on this pane, in order. */
  onEvent(listener: (e: PaneEvent) => void): Unsubscribe;

  /**
   * A refusal naming this pane. `unknown_pane` covers four different daemon-side
   * conditions on purpose ("no pane named", "pane is not available", "this daemon
   * serves no panes", "not subscribed to this pane") so that a refusal cannot be
   * used to enumerate which sessions exist. Treat all of them as "re-fetch the
   * session list".
   */
  onError(listener: (e: WireRefusalError) => void): Unsubscribe;
}

// ---------------------------------------------------------------------------
// The transport
// ---------------------------------------------------------------------------

export interface ConnectOptions {
  signal?: AbortSignal;
  /** Bounds TCP + TLS + pin + hello. Defaults to HANDSHAKE_TIMEOUT_MS. */
  timeoutMs?: number;
}

export interface TransportRequestOptions {
  timeoutMs?: number;
  signal?: AbortSignal;
}

/**
 * The one seam between the Svelte app and the daemon.
 *
 * `request` is the whole of the command surface: the shim's DaemonService
 * methods are thin wrappers over it, and the response `data` shapes come from
 * the desktop's generated `@bindings/internal/protocol` types rather than from
 * anything in this package.
 */
export interface Transport {
  readonly status: ConnectionStatus;

  /**
   * Open the connection and complete the handshake. Resolves once the phase is
   * `ready`; rejects with the reason otherwise.
   *
   * The failure a user actually meets first is worth naming: on iOS, a DENIED
   * local-network permission is indistinguishable from an unreachable host
   * (EHOSTUNREACH) and the prompt never comes back, so a connect timeout to a
   * private address should be reported with a Settings hint rather than as a
   * network error.
   */
  connect(endpoint: Endpoint, opts?: ConnectOptions): Promise<void>;

  /**
   * Close the connection and reject every pending request.
   *
   * Also the correct response to the app being backgrounded: an iOS app's
   * queue is SIGSTOPped, the peer resets, and `NWConnection.state` still reads
   * `.ready` on resume until a send fails. Tearing down on `didEnterBackground`
   * and rebuilding on `willEnterForeground` is the only posture that does not
   * present a dead socket as a live one.
   */
  disconnect(reason?: string): Promise<void>;

  /**
   * Send one `req` frame and await its correlated reply.
   *
   * Rejects with DaemonError when the Response carried `ok: false`, with
   * WireRefusalError when the daemon sent an `err` on this id, with
   * RequestTimeoutError on the deadline, and with ConnectionClosedError when the
   * socket went away first. It never resolves for a command it did not send.
   *
   * A command on the unconditional denial list is refused LOCALLY, without a
   * frame, because the daemon answers a denied cmd with `unknown_cmd` and then
   * CLOSES — taking every live pane subscription with it. One mistyped command
   * would otherwise cost a full reconnect.
   */
  request<T = unknown>(
    cmd: string,
    fields?: RequestFields,
    opts?: TransportRequestOptions,
  ): Promise<T>;

  /**
   * Attach to a pane.
   *
   * Resolves once the first `resync` arrives, because that resync IS the
   * acknowledgement — there is no other ack — and it carries the pane's current
   * screen, which is what makes an alternate-screen agent paintable at all (a
   * fresh tmux attach replays nothing, so a subscriber with no prelude stares at
   * a blank pane until the agent next repaints).
   *
   * Rejects with WireRefusalError on `unknown_pane`.
   */
  subscribe(pane: string, viewport?: Viewport, opts?: TransportRequestOptions): Promise<PaneSubscription>;

  /** The live subscription for a pane, if any. */
  subscription(pane: string): PaneSubscription | undefined;

  /**
   * Every inbound frame, after decoding and before routing. For diagnostics, a
   * packet log, and tests. Application code should use `request`, `subscribe`
   * and the subscription's own events instead.
   */
  onFrame(listener: (f: Frame) => void): Unsubscribe;

  /** Connection state changes, including the initial one on registration. */
  onStatus(listener: (s: ConnectionStatus) => void): Unsubscribe;

  /**
   * A refusal that belonged to no request and no pane — an `err` frame with no
   * id. Several of these are immediately followed by a close, so a listener
   * should record the reason and let `onStatus` report the close.
   */
  onRefusal(listener: (e: WireRefusalError) => void): Unsubscribe;
}

// ---------------------------------------------------------------------------
// The byte-level seam beneath a Transport
// ---------------------------------------------------------------------------

/**
 * What a Transport implementation sits on: something that carries whole frames
 * in both directions and reports when it goes away.
 *
 * Two implementations are expected. The native plugin bridge posts decoded
 * envelopes across the Capacitor bridge and takes them back the same way — and
 * note the asymmetry there, because it shapes the PTY path: JS to native is a
 * structured clone via `postMessage` and is cheap, while native to JS is
 * `evaluateJavaScript` with the payload interpolated into JavaScript SOURCE,
 * which is a JSON encode plus a main-thread hop plus a parse per event. So a
 * conforming native channel must COALESCE pane output rather than emit one event
 * per socket read; the daemon's own 16 ms coalescing already gives it the
 * cadence for free.
 *
 * The other implementation is an in-memory fake, which is what every test in
 * this package uses.
 */
export interface FrameChannel {
  send(frame: Frame): void | Promise<void>;
  onFrame(listener: (f: Frame) => void): Unsubscribe;
  /** Fired once, when the channel is gone. `err` is absent for a clean close. */
  onClose(listener: (err?: Error) => void): Unsubscribe;
  close(reason?: string): void | Promise<void>;
}
