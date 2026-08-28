import type { PluginListenerHandle } from '@capacitor/core';

/**
 * LolaTransport is the native half of the mobile client's connection to a lola
 * daemon. It exists because the M1 daemon speaks raw TLS 1.3 over TCP with a
 * four-byte length prefix, and a WKWebView cannot open that socket at all: the
 * JavaScript `WebSocket` constructor performs an HTTP upgrade the daemon never
 * answers, and `fetch` cannot hold a bidirectional stream. Everything about the
 * socket therefore lives in Swift, and the WebView receives decoded frames.
 *
 * What the plugin owns:
 *
 *   - the TCP connection, its TLS 1.3 handshake, and the certificate decision;
 *   - the length-prefixed framing in both directions, including reassembly of a
 *     frame split across TCP segments and refusal of an oversized one;
 *   - the M1 bearer handshake, so a connection is only reported as connected
 *     once the daemon has actually accepted the key;
 *   - coalescing inbound frames on a short tick, so a busy pane does not cost
 *     one bridge crossing per read.
 *
 * What the plugin deliberately does NOT own:
 *
 *   - the envelope. It never parses a frame body beyond the bearer handshake,
 *     and it has no opinion about `sub`, `pty`, `resync` or a request's `id`.
 *     Those live in `mobile/src/wire`, which is shared with the tests and with
 *     any future transport, and duplicating them in Swift would create a second
 *     copy of the protocol to keep honest.
 *   - reconnection policy. The plugin reports what happened and stops; deciding
 *     whether and when to try again is the app's, because only the app knows
 *     whether the user is looking at a terminal or at a cached list.
 */
export interface LolaTransportPlugin {
  /**
   * Opens a connection and completes the handshake. Resolves once the daemon
   * has accepted the bearer key (or immediately after TLS when no key is
   * supplied); rejects with a `LolaFailureCode` in the error's `code` field
   * otherwise.
   *
   * Calling `connect` while a connection is open tears the old one down first.
   * Every connection gets a new `epoch`, and every event carries the epoch it
   * belongs to, so callbacks queued behind a teardown are recognisably stale.
   */
  connect(options: LolaConnectOptions): Promise<LolaConnectResult>;

  /**
   * Closes the connection. Resolves even when nothing was open. A `closed`
   * state event is emitted with `code: 'client_closed'`.
   */
  disconnect(options?: LolaDisconnectOptions): Promise<void>;

  /**
   * Sends one or more frames. Each string is a complete, already-serialized
   * JSON frame body; the plugin adds the length prefix and writes the batch in
   * a single socket write. Rejects if the connection is not established, if a
   * body exceeds the protocol's maximum frame size, or if the write fails.
   */
  send(options: LolaSendOptions): Promise<void>;

  /** Reports the current connection phase and cheap traffic counters. */
  status(): Promise<LolaStatusResult>;

  /**
   * Batched inbound frames. Each element of `frames` is one complete JSON frame
   * body, in the order the daemon wrote it. Bodies are NOT parsed natively; the
   * listener parses them, which is what keeps one copy of the envelope.
   */
  addListener(
    eventName: 'frames',
    listenerFunc: (event: LolaFramesEvent) => void,
  ): Promise<PluginListenerHandle>;

  /** Connection lifecycle. See `LolaConnectionPhase`. */
  addListener(
    eventName: 'state',
    listenerFunc: (event: LolaStateEvent) => void,
  ): Promise<PluginListenerHandle>;

  removeAllListeners(): Promise<void>;
}

export interface LolaConnectOptions {
  /** Host or IP of the daemon. Never compiled in; the app supplies it. */
  host: string;

  /** Defaults to 7717, mirroring `config.DefaultRemotePort`. */
  port?: number;

  /**
   * Base64 SHA-256 of the daemon's SubjectPublicKeyInfo — the value the daemon
   * logs at startup as `SPKI pin ...`, and from M2 the value the pairing QR
   * carries. Standard base64 with padding, the encoding `DeviceKey.SPKIPin`
   * produces.
   *
   * The daemon's certificate is self-signed, is in no trust store, and carries
   * `DNSNames: ["lola"]`, so ordinary system evaluation cannot succeed against
   * a LAN address even in principle. The pin is the whole of the server's
   * identity; there is no weaker check to fall back to.
   */
  spkiPin?: string;

  /**
   * Required when `spkiPin` is omitted. Omitting the pin means the connection
   * accepts whatever certificate the peer presents and merely REPORTS what it
   * saw in `LolaConnectResult.spkiPin`, which is a real man-in-the-middle
   * exposure and is only tolerable while the pin has no distribution channel
   * (M2's pairing QR is what gives it one).
   *
   * The flag exists so that state cannot be reached by accident: an absent
   * `spkiPin` is exactly what a typo'd option name or an unset config field
   * looks like, and a security control that disappears when a field is
   * misspelled is not a control.
   */
  allowUnpinned?: boolean;

  /**
   * M1's bearer key, from `LOLA_REMOTE_INSECURE_KEY` on the daemon. At least 16
   * characters, or the daemon's listener refuses to start. Supplied by the app
   * at connect time from the field the human typed; never committed, never
   * logged, never placed in a URL.
   *
   * Omit it against a daemon built without `-tags lola_insecure`, where the
   * handshake is mutual TLS instead and an in-band hello would be denied.
   */
  insecureKey?: string;

  /** TCP + TLS establishment budget. Default 10000, mirroring the daemon. */
  connectTimeoutMs?: number;

  /**
   * Budget for the bearer handshake once TLS is up. Default 10000, mirroring
   * `remote.handshakeTimeout`. There is no read deadline after this: an
   * attached pane is legitimately silent for minutes and the daemon sets none
   * either.
   */
  handshakeTimeoutMs?: number;

  /**
   * How long one write may remain unacknowledged by the transport before the
   * connection is considered broken. Default 15000, mirroring
   * `remote.writeTimeout`.
   */
  writeTimeoutMs?: number;

  /**
   * Inbound coalescing window in milliseconds. Default 16, which is the
   * daemon's own `panebus.DefaultFlushInterval` — matching it means the plugin
   * adds at most one extra flush window of latency rather than imposing a
   * second, differently-phased cadence on top of the first. Zero delivers every
   * frame on its own bridge event, which is only useful for debugging.
   */
  flushIntervalMs?: number;

  /**
   * Flush early once a batch reaches this many bytes of frame bodies. Default
   * 262144. It bounds the size of one bridge event, which is a JavaScript
   * source string the WebView has to parse.
   */
  maxBatchBytes?: number;
}

export interface LolaDisconnectOptions {
  /** Recorded in the `closed` state event. Purely for diagnosis. */
  reason?: string;
}

export interface LolaSendOptions {
  /** Complete JSON frame bodies, without a length prefix. */
  frames: string[];
}

export interface LolaConnectResult {
  /** Monotonic per-connection counter. Every event carries the same field. */
  epoch: number;
  host: string;
  port: number;

  /**
   * The pin actually presented by the peer, always reported — both so a pinned
   * connection can be audited and so an unpinned first connection has something
   * to show a human who is about to write the pin down.
   */
  spkiPin: string;

  /** True when `spkiPin` was supplied and matched. */
  pinned: boolean;
}

export type LolaConnectionPhase =
  /** TCP and TLS are being established. */
  | 'connecting'
  /** TLS is up and the bearer handshake is in flight. */
  | 'handshaking'
  /** Authenticated. Frames may be sent. */
  | 'connected'
  /** The connection never became usable, or died. See `code`. */
  | 'failed'
  /** Closed in an orderly way, by this side or by the peer. */
  | 'closed';

/**
 * Why a connection failed or closed. The distinction the UI actually needs is
 * "the daemon is not reachable from this network" versus "the daemon refused
 * us", because the first is fixed by joining the right WiFi and the second by
 * correcting the key or the pin.
 */
export type LolaFailureCode =
  /**
   * No route, refused, or the local-network permission was denied. iOS reports
   * a denied local-network permission as an ordinary unreachable-host failure
   * and never re-prompts, so these genuinely cannot be told apart here; the app
   * is expected to offer the Settings hint on a first-connect failure.
   */
  | 'network'
  /** The connect or handshake budget elapsed. */
  | 'timeout'
  /** The TLS handshake failed for a reason other than the pin. */
  | 'tls'
  /** The peer's SPKI hash is not the pinned one. */
  | 'pin_mismatch'
  /** The daemon answered the bearer handshake with a refusal. */
  | 'rejected'
  /** The peer violated the framing: a zero-length or oversized frame. */
  | 'protocol'
  /** The daemon closed the connection. */
  | 'peer_closed'
  /** `disconnect()` was called. */
  | 'client_closed'
  /** The app was backgrounded; see the note on `state` events. */
  | 'backgrounded'
  /** A defect in the plugin. */
  | 'internal';

export interface LolaStateEvent {
  epoch: number;
  phase: LolaConnectionPhase;
  /** Present on `failed` and `closed`. */
  code?: LolaFailureCode;
  /** A short human line. Never carries the key or any pane bytes. */
  reason?: string;
  /** Present on `connected`. */
  spkiPin?: string;
  /** Present on `connected`. */
  pinned?: boolean;

  /**
   * The daemon's own `ErrPayload.code`, when the failure was a refusal frame
   * rather than a transport failure. Kept separate from `code` because the two
   * vocabularies belong to different layers and merging them would lose the
   * distinction between "the socket died" and "the daemon said no, and here is
   * the machine-readable reason it gave".
   */
  daemonCode?: string;

  /**
   * Present only alongside `daemonCode: 'unsupported_version'`. They are the
   * daemon's own envelope-version bounds, and they exist so the app can name
   * which side is behind ("update lola on this host" versus "update the app")
   * instead of showing a connect error.
   */
  minV?: number;
  maxV?: number;
}

export interface LolaFramesEvent {
  epoch: number;
  /** Complete JSON frame bodies, in wire order. */
  frames: string[];
}

export interface LolaStatusResult {
  epoch: number;
  phase: LolaConnectionPhase;
  host?: string;
  port?: number;
  pinned?: boolean;
  framesIn: number;
  framesOut: number;
  bytesIn: number;
  bytesOut: number;
}

/**
 * Mirrors `protocol.MaxFrameBytes`. It is a protocol constant rather than a
 * tuning knob, so it is not a connect option: both ends must refuse at the same
 * size or an oversized frame becomes a hang on one side and a close on the
 * other.
 */
export const LOLA_MAX_FRAME_BYTES = 1 << 20;

/** Mirrors `config.DefaultRemotePort`. */
export const LOLA_DEFAULT_PORT = 7717;
