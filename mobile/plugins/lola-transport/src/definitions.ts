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
   * A rejection caused by a REFUSAL rather than a transport failure also
   * carries the daemon's own `ErrPayload.code` — plus `minV`/`maxV` for a
   * version skew — in the rejection's data. Capacitor nests that dictionary,
   * so it arrives as `err.data.daemonCode`, not `err.daemonCode`.
   *
   * It is on the rejection rather than only on the `state` event because the
   * two do NOT arrive in that order: the native side settles this call before
   * it emits the event, and the two cross the bridge as separate evaluations,
   * so a caller's catch block runs while the event is still in flight. Reading
   * the event instead means putting a timer on every connect failure — and
   * getting it wrong means a refused bearer key is reported as an unreachable
   * host, which is the same screen as a wrong address and has a completely
   * different fix.
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
   * Whether a QR scan could succeed on this device right now, asked BEFORE a
   * Scan button is drawn.
   *
   * The iOS Simulator is why this is a separate call rather than something to
   * discover on a tap: it has no camera and cannot be given one, so every scan
   * there fails, and a button that always fails reads as a broken feature
   * instead of as a property of the machine. Hide or disable the control when
   * `available` is false and say which of the reasons it is.
   *
   * Resolves; it never rejects. "You cannot scan here" is an answer.
   */
  scanCapability(): Promise<LolaScanCapabilityResult>;

  /**
   * Opens a full-screen camera scanner and resolves with the decoded string.
   *
   * The plugin does NOT interpret the payload. It returns exactly what the
   * symbol carried, and the app decides whether that is a pairing token, a QR
   * from some other product, or noise. Keeping the enrolment format out of the
   * transport plugin is deliberate: the format is still being written, and a
   * scanner that silently rejects an unfamiliar string is very hard to diagnose
   * from a phone.
   *
   * Cancellation RESOLVES with `cancelled: true`. A human changing their mind
   * is the ordinary way this ends, and putting it on the error channel makes
   * every call site tell an expected outcome apart from a broken camera by
   * reading a code - which is how it ends up rendered as a red banner.
   *
   * Rejects with a `LolaScanErrorCode` in the error's `code` field when a scan
   * could not be attempted at all.
   */
  scanQR(options?: LolaScanOptions): Promise<LolaScanResult>;

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

  /**
   * A connection handed to the app by a `lola-dev://connect?...` URL.
   *
   * DEVELOPMENT ONLY, and the app has an obligation attached to it. See
   * `LolaDevLinkEvent`. This event does not exist in a release build: the whole
   * path is compiled out unless the package is built in its debug
   * configuration, so a listener registered here simply never fires.
   *
   * The event is RETAINED until a listener consumes it, because a cold launch
   * delivers the URL while the WebView is still loading and an ordinary event
   * would be posted to nobody. Register the listener during startup and the
   * pending link arrives as soon as you do.
   */
  addListener(
    eventName: 'devLink',
    listenerFunc: (event: LolaDevLinkEvent) => void,
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

export interface LolaScanOptions {
  /**
   * One line of guidance under the viewfinder. Defaults to a sentence naming
   * the desktop app. Keep it short; it sits over a live camera image.
   */
  prompt?: string;
}

export interface LolaScanResult {
  /** True when the human dismissed the scanner without scanning anything. */
  cancelled: boolean;

  /**
   * The decoded string, exactly as the symbol carried it. Present only when
   * `cancelled` is false.
   *
   * Treat it as untrusted input: anyone can print a QR code. Parse it, and
   * refuse anything that is not the shape you expect.
   */
  value?: string;
}

/** Why a scan could not be attempted. Never a `LolaFailureCode`. */
export type LolaScanErrorCode =
  /**
   * No capture device. The Simulator, always. Ask `scanCapability()` first so
   * this never reaches a user as a tap that fails.
   */
  | 'no_camera'
  /**
   * Camera access was declined. iOS asks exactly once, so the only way back is
   * Settings and the app has to say so rather than offering a retry that
   * cannot prompt.
   */
  | 'camera_denied'
  /** Camera access is disallowed by policy. There is no toggle to offer. */
  | 'camera_restricted'
  /** Nothing to present on, or the capture graph would not assemble. */
  | 'unavailable';

export interface LolaScanCapabilityResult {
  /** False when scanning cannot work here. Hide or disable the control. */
  available: boolean;

  /**
   * The camera authorization as iOS reports it. `notDetermined` counts as
   * available: the prompt has not been shown, and hiding the button would
   * guarantee it never is. The scanner requests access when it opens, which is
   * the moment a human has expressed the intent that makes the prompt sensible.
   */
  authorization: 'notDetermined' | 'authorized' | 'denied' | 'restricted';

  /**
   * Set when `available` is false. `unsupported` is the web fallback, where
   * there is no plugin at all rather than a device that happens to lack a
   * camera.
   */
  reason?: 'no_camera' | 'denied' | 'restricted' | 'unsupported';
}

/**
 * A connection handed over by a development URL, and the reason it is safe to
 * have at all.
 *
 * `mobile/PLAN.md`, under Pairing, settles that the pairing payload is an
 * opaque `lola1.` token and deliberately NOT a URI, because a custom scheme
 * cannot be claimed exclusively and the system camera would hand the secret to
 * whichever app registered it. That argument holds here with more force, not
 * less: M1's bearer key is longer-lived than M2's `qr_secret`. So this is NOT
 * the pairing mechanism and never becomes one. It is a testing affordance for
 * the one case a camera cannot cover - an iOS Simulator, which has no camera -
 * so that an agent or a CI job can put the app in front of a live daemon.
 *
 * Three things fence it, and the third one is yours:
 *
 *   1. the scheme is `lola-dev`, not the app's own scheme;
 *   2. every line that turns a URL into a connection is compiled out of a
 *      release build;
 *   3. the app MUST show a persistent banner for as long as a connection that
 *      arrived this way is up. That is what makes it a labelled test fixture
 *      rather than a hidden back door. `source` is the flag to key it off -
 *      hold it beside the connection and clear it when the connection ends.
 *
 * The fields are exactly the ones `connect` already takes from the form a human
 * fills in; a development path that could reach settings the UI cannot would be
 * a second, unreviewed way to configure the app.
 */
export interface LolaDevLinkEvent {
  /**
   * HOW the link arrived, which is the one fact that decides whether the app
   * may act on it unattended. Both raise the banner; only one may connect.
   *
   * - `dev-url` — the OS URL router, on behalf of some app. ANY app on the
   *   device can ask iOS to open one, which is exactly PLAN.md's objection to
   *   URL-routed pairing, so the app fills its form and waits for a human.
   * - `dev-launch` — this process's own launch environment or argv. Setting
   *   either requires being the thing that STARTED the process: a debugger on
   *   a device, `simctl` on a Simulator. Whoever can do that already owns the
   *   machine and does not need this feature, so the app may connect on its
   *   own — which is what makes the scriptable path scriptable at all, since
   *   iOS 26 puts an untappable confirmation in front of the URL and `simctl`
   *   has no gesture API.
   */
  source: 'dev-url' | 'dev-launch';

  /** Never empty. Already rejected if it looks like a pasted URL. */
  host: string;

  /** Absent when the URL omitted it; treat that as `LOLA_DEFAULT_PORT`. */
  port?: number;

  /**
   * Normalized to standard padded base64 - the spelling `SPKIPin.matches`
   * compares and `DeviceKey.SPKIPin()` produces - whichever alphabet the URL
   * used.
   *
   * ALWAYS PRESENT on a delivered event: a link with no pin, or with a pin that
   * does not decode to 32 bytes, is rejected whole. An unpinned connection
   * accepts whatever certificate answers, which is the one genuinely dangerous
   * state this transport has, so both spellings of that mistake fail at the
   * same fence. The field stays optional only because the payload type is
   * shared with a future shape that may not carry one.
   */
  spkiPin?: string;

  /**
   * M1's bearer key. Length is NOT checked here; the app already refuses below
   * `INSECURE_MIN_KEY_LEN` and can say so in the field.
   *
   * The URL may spell it `key=<the key>` or `keyfile=<a bare filename>`; the
   * second is read out of the app's own Documents directory and deleted, and it
   * exists because iOS writes the whole URL to the device's persistent unified
   * log for every delivery route it has — `simctl openurl`, the launch
   * environment and argv alike — before the app runs. A key spelled into the
   * query string is therefore disclosed whatever the plugin does with it. Only
   * a bare name is accepted: a separator, a parent reference or a leading dot
   * is refused, so nothing outside this app's container can be opened.
   */
  insecureKey?: string;

  /**
   * A tmux pane to open once the connection is up, when the URL named one.
   *
   * It is a DESTINATION, not a capability: a link that never connects cannot
   * use it, and the daemon re-resolves any pane name against its own session
   * store before anything is exec'd. It exists because the terminal is the
   * screen the whole app is a bet on and it was the only screen a reviewer
   * could not photograph — it is reached solely by tapping a session row, the
   * Simulator has no gesture API, and its device window is absent from the
   * accessibility tree.
   */
  pane?: string;

  /** The session `pane` belongs to. Defaults to `pane` when the URL omits it. */
  session?: string;
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
