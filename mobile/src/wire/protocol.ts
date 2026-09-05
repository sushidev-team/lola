// The remote protocol, mirrored by hand from Go.
//
// This file is a HAND-MAINTAINED MIRROR across a language boundary, in exactly
// the same spirit as desktop/frontend/src/lib/theme.ts mirrors internal/state:
// there is no code generator in common between the two sides, so the only thing
// holding them together is care plus a test. Here the test is the golden vector
// set in testdata/frames.json, which both this package and
// internal/protocol/goldenvectors_test.go read and assert byte-for-byte against.
// If you change a field name, a json tag, an omitempty, or the ORDER of the
// fields in one of the Go structs, you must change it here and add or amend a
// vector, or the two sides drift silently and the failure lands on a phone with
// no debugger attached.
//
// Every type below names the Go type and file it mirrors. The Go sources are:
//
//   internal/protocol/frame.go       the envelope, the frame kinds, the payloads
//   internal/protocol/framecodec.go  the length-prefixed framing and its cap
//   internal/protocol/protocol.go    Request / Response, shared with the unix socket
//   internal/remote/policy.go        the unconditional command denials
//   internal/remote/insecure.go      the M1 bearer-key hello
//   internal/tmux/client.go          the scroll clamp
//   internal/config/remote.go        the default port
//
// Nothing in this file performs I/O or touches a platform API. The codec lives
// in ./codec.ts, the transport seam in ./transport.ts.

// ---------------------------------------------------------------------------
// Framing constants
// ---------------------------------------------------------------------------

/**
 * The byte count of the length prefix: a big-endian uint32 giving the size of
 * the JSON body that follows. There is no magic number and no type byte.
 *
 * Mirrors `protocol.FrameHeaderBytes` (internal/protocol/frame.go).
 */
export const FRAME_HEADER_BYTES = 4;

/**
 * The maximum size of one encoded frame BODY, in either direction. The prefix
 * itself does not count against it: a body of exactly this many bytes is legal.
 *
 * MUST EQUAL `protocol.MaxFrameBytes` (internal/protocol/frame.go), which is
 * `1 << 20`. The daemon refuses a larger inbound prefix before allocating and
 * then closes the connection, so a client that disagrees about this number does
 * not get a rejected frame, it gets a dropped socket.
 */
export const MAX_FRAME_BYTES = 1 << 20;

/**
 * Envelope versions this client speaks. A daemon accepts `v` in
 * `[FRAME_VERSION_MIN, FRAME_VERSION_CURRENT]` and refuses anything else with an
 * `err` frame carrying its own bounds, then closes.
 *
 * Mirrors `protocol.FrameVersionCurrent` / `FrameVersionMin`.
 */
export const FRAME_VERSION_CURRENT = 1;
export const FRAME_VERSION_MIN = 1;

/**
 * The TCP port the daemon's phone listener binds when `[remote].port` is unset.
 *
 * Mirrors `config.DefaultRemotePort` (internal/config/remote.go).
 */
export const DEFAULT_REMOTE_PORT = 7717;

// ---------------------------------------------------------------------------
// Frame kinds and their directions
// ---------------------------------------------------------------------------

/**
 * The frame kinds, mirroring the `Frame*` constants in internal/protocol/frame.go.
 *
 *   req     client -> daemon: payload is a Request, `cmd` names it
 *   resp    daemon -> client: payload is a Response, `id` echoes the req
 *   sub     client -> daemon: subscribe to `pane`; payload is SubPayload
 *   unsub   client -> daemon: drop the subscription on `pane`; no payload, no ack
 *   resync  daemon -> client: payload is ResyncPayload
 *   pty     BOTH: PTYOutputPayload outbound, PTYInputPayload inbound
 *   err     BOTH: payload is ErrPayload
 */
export const FRAME_REQ = "req";
export const FRAME_RESP = "resp";
export const FRAME_SUB = "sub";
export const FRAME_UNSUB = "unsub";
export const FRAME_RESYNC = "resync";
export const FRAME_PTY = "pty";
export const FRAME_ERR = "err";

export type FrameType =
  | typeof FRAME_REQ
  | typeof FRAME_RESP
  | typeof FRAME_SUB
  | typeof FRAME_UNSUB
  | typeof FRAME_RESYNC
  | typeof FRAME_PTY
  | typeof FRAME_ERR;

/** Bit flags for the direction a frame kind may legitimately travel in. */
export const DIR_TO_DAEMON = 1;
export const DIR_TO_CLIENT = 2;

/**
 * The directions declared for a frame type, and 0 for a type this build does
 * not know — which is what makes every predicate below fail closed on an
 * unknown type without a special case.
 *
 * Mirrors `protocol.FrameDirections`.
 */
export function frameDirections(t: string): number {
  switch (t) {
    case FRAME_REQ:
    case FRAME_SUB:
    case FRAME_UNSUB:
      return DIR_TO_DAEMON;
    case FRAME_RESP:
    case FRAME_RESYNC:
      return DIR_TO_CLIENT;
    case FRAME_PTY:
    case FRAME_ERR:
      return DIR_TO_DAEMON | DIR_TO_CLIENT;
  }
  return 0;
}

/** Mirrors `protocol.KnownFrameType`. */
export function knownFrameType(t: string): boolean {
  return frameDirections(t) !== 0;
}

/** Mirrors `protocol.DaemonAcceptsFrame`: may the daemon RECEIVE this type. */
export function daemonAcceptsFrame(t: string): boolean {
  return (frameDirections(t) & DIR_TO_DAEMON) !== 0;
}

/** Mirrors `protocol.ClientAcceptsFrame`: may a client RECEIVE this type. */
export function clientAcceptsFrame(t: string): boolean {
  return (frameDirections(t) & DIR_TO_CLIENT) !== 0;
}

/**
 * Mirrors `protocol.SupportedFrameVersion`. The window is a closed interval;
 * there is no v0.
 */
export function supportedFrameVersion(v: number): boolean {
  return (
    Number.isInteger(v) && v >= FRAME_VERSION_MIN && v <= FRAME_VERSION_CURRENT
  );
}

// ---------------------------------------------------------------------------
// The envelope
// ---------------------------------------------------------------------------

/**
 * One message of the remote protocol.
 *
 * Mirrors `protocol.Frame` (internal/protocol/frame.go). The FIELD ORDER below
 * is the Go struct's declaration order, and it is load-bearing for the encoder:
 * encoding/json emits struct fields in declaration order, so reproducing the
 * daemon's exact bytes means emitting `v`, `type`, `id`, `cmd`, `pane`, `seq`,
 * `payload` in that sequence. See ./codec.ts.
 *
 * Every field except `v` and `type` carries `omitempty` on the Go side, so an
 * absent field and a zero value are indistinguishable on the wire and must stay
 * indistinguishable here.
 *
 * `payload` is a decoded JSON value rather than raw bytes. Go keeps it raw so
 * the envelope can be authorized before anything untrusted is unmarshalled; a
 * client has no such boundary to defend, and carrying the decoded value removes
 * a whole class of double-encoding bug.
 */
export interface Frame {
  /** Envelope version. Always FRAME_VERSION_CURRENT on anything we send. */
  v: number;
  type: FrameType | string;
  /** Request correlation; echoed on the reply. Absent when there is none. */
  id?: string;
  /** `req` frames only. AUTHORIZATION READS THIS, never the payload. */
  cmd?: string;
  /** sub / unsub / pty / resync / err: the tmux session name. */
  pane?: string;
  /** Per-pane stream ordering, for gap detection. Never 0 on the wire. */
  seq?: number;
  /** Type-specific body. Absent when the frame has none. */
  payload?: unknown;
}

// ---------------------------------------------------------------------------
// Payloads
// ---------------------------------------------------------------------------

/**
 * The body of a `sub` frame.
 *
 * Mirrors `protocol.SubPayload`. Both fields are `omitempty` and both are
 * ADVISORY: the pane bus attaches once per pane at the tmux window size and
 * fans the full untruncated stream out, so a phone pans client-side rather than
 * reflowing the developer's window. They exist so the client can be told what
 * the real window size is and draw its "200 cols, panning" chip.
 *
 * A sub is acknowledged by the FIRST resync frame carrying the same `id`. There
 * is no other ack, and a refusal is an `err` frame on that id.
 */
export interface SubPayload {
  cols?: number;
  rows?: number;
}

/**
 * A coherent snapshot of a pane's screen, rendered by the daemon's shadow
 * emulator.
 *
 * Mirrors `protocol.ResyncPayload`. Note which fields carry `omitempty` and
 * which do not, because the difference is visible on the wire:
 *
 *   cols, rows, cursorX, cursorY  ALWAYS present, even as 0. An exit frame with
 *                                 no final screen is `{"cols":0,...,"exited":true}`.
 *   lines                         omitted when empty.
 *   altScreen, exited, cursorHidden  omitted when false.
 *
 * `lines` are screen rows with ANSI SGR preserved and TRAILING BLANK ROWS
 * TRIMMED, so `lines.length` may be less than `rows` and the client pads the
 * remainder rather than assuming a full grid.
 *
 * `cursorHidden` is stated in the NEGATIVE deliberately, and a client must
 * honour that sense: absent or false means the caret is VISIBLE. A client that
 * inverts it paints no caret on every pane, which reads as an agent that is not
 * waiting for input when it is.
 */
export interface ResyncPayload {
  cols: number;
  rows: number;
  lines?: string[];
  cursorX: number;
  cursorY: number;
  altScreen?: boolean;
  /** The pane's child has ended. Terminal: nothing follows it on this stream. */
  exited?: boolean;
  /** DECTCEM, negated. Absent or false means the caret is visible. */
  cursorHidden?: boolean;
}

/**
 * Raw pane output, daemon to client.
 *
 * Mirrors `protocol.PTYOutputPayload`. `Data []byte` has NO `omitempty`, so the
 * field is always written — and Go marshals a nil slice as `null`, not as `""`.
 * A client must therefore treat `null`, `""` and an absent field as the same
 * empty payload; naively handing `null` to a base64 decoder throws.
 */
export interface PTYOutputPayload {
  /** Standard base64 with padding, or null for an empty flush. */
  data: string | null;
}

/**
 * The closed vocabulary of PTYInputPayload.action.
 *
 * Mirrors `protocol.PTYActionWrite` / `PTYActionScroll` / `PTYActionResize`. An
 * unrecognized action is refused with `unknown_type` on the frame's id and is
 * never approximated, because guessing here types bytes into a live agent.
 */
export const PTY_ACTION_WRITE = "write";
export const PTY_ACTION_SCROLL = "scroll";
export const PTY_ACTION_RESIZE = "resize";

export type PTYAction =
  typeof PTY_ACTION_WRITE | typeof PTY_ACTION_SCROLL | typeof PTY_ACTION_RESIZE;

/**
 * A client action on a subscribed pane.
 *
 * Mirrors `protocol.PTYInputPayload`. It is the most sensitive frame in the
 * protocol: a write is raw bytes into an interactive process on the developer's
 * Mac, authorized by the SUBSCRIPTION rather than by a command tier, and it
 * deliberately bypasses the daemon's AtPrompt idle gate — that gate stops lola's
 * own automation typing mid-turn, not a human.
 *
 *   write   `data` is written to the PTY master, cancelling copy mode first.
 *           An empty `data` is silently ignored by the daemon.
 *   scroll  `lines` is handed to tmux.ScrollPane, where POSITIVE scrolls BACK
 *           into history and negative scrolls forward (`up := lines > 0`). The
 *           client must NEVER synthesize wheel bytes itself: the daemon decides
 *           between the program's own transcript and tmux copy mode, and getting
 *           that choice wrong is not a degraded scroll but no scroll at all.
 *   resize  Recorded and IGNORED in M1. A phone cannot shrink the developer's
 *           tmux window, so the viewport is client-side panning.
 */
export interface PTYInputPayload {
  action: PTYAction | string;
  /** Standard base64 with padding. Omitted when empty. */
  data?: string;
  lines?: number;
  cols?: number;
  rows?: number;
}

/**
 * A machine-readable refusal, in either direction.
 *
 * Mirrors `protocol.ErrPayload`. THIS SHAPE IS FROZEN AT v=1 FOREVER: it has to
 * stay decodable by a peer that understands nothing else about the version it is
 * talking to, which is what lets a version mismatch produce a named screen
 * rather than a socket error.
 *
 * `minV` / `maxV` are set only on `unsupported_version`, and they are what let
 * the app say which side is behind.
 */
export interface ErrPayload {
  code: ErrCode | string;
  message?: string;
  minV?: number;
  maxV?: number;
}

/**
 * The refusal codes. This list is CLOSED: an unrecognized code must be rendered
 * as a generic failure and never interpreted.
 *
 * Mirrors the `Code*` constants in internal/protocol/frame.go.
 */
export const CODE_UNSUPPORTED_VERSION = "unsupported_version";
export const CODE_UNKNOWN_TYPE = "unknown_type";
export const CODE_UNKNOWN_CMD = "unknown_cmd";
export const CODE_DENIED = "denied";
export const CODE_UNKNOWN_PANE = "unknown_pane";
export const CODE_FRAME_TOO_LARGE = "frame_too_large";
export const CODE_RATE_LIMITED = "rate_limited";
export const CODE_INTERNAL = "internal";

export type ErrCode =
  | typeof CODE_UNSUPPORTED_VERSION
  | typeof CODE_UNKNOWN_TYPE
  | typeof CODE_UNKNOWN_CMD
  | typeof CODE_DENIED
  | typeof CODE_UNKNOWN_PANE
  | typeof CODE_FRAME_TOO_LARGE
  | typeof CODE_RATE_LIMITED
  | typeof CODE_INTERNAL;

/**
 * Whether a refusal carrying this code tears the connection down.
 *
 * Derived from internal/remote/conn.go and pane.go, where the distinction is a
 * property of the CALL SITE rather than of the code, so the two families overlap:
 * `unknown_type` is fatal for a bad envelope and non-fatal for a bad pty action,
 * `internal` is fatal for a malformed envelope and non-fatal for a failed write,
 * and `rate_limited` is fatal for a full pane queue and non-fatal for too many
 * requests in flight.
 *
 * The honest reading is therefore: a client cannot tell from the code alone.
 * What it CAN do is stop guessing — treat the codes below as "assume the socket
 * is going away" and let the transport's close handler be the authority, which
 * is what `alwaysFatalCode` names. Everything else is a per-frame refusal that
 * the connection is expected to survive.
 */
export function alwaysFatalCode(code: string): boolean {
  return (
    code === CODE_UNSUPPORTED_VERSION ||
    code === CODE_UNKNOWN_CMD ||
    code === CODE_DENIED ||
    code === CODE_FRAME_TOO_LARGE
  );
}

// ---------------------------------------------------------------------------
// Request / Response
// ---------------------------------------------------------------------------

/**
 * The request body of a `req` frame.
 *
 * Mirrors the flat fields of `protocol.Request` (internal/protocol/protocol.go)
 * that a remote peer may set. Two Go behaviours are reproduced exactly:
 *
 *   - `cmd` has NO `omitempty`, so it is always written, even as `""`. An empty
 *     cmd is denied by the daemon, so this only matters for byte fidelity.
 *   - `force` is CLEARED unconditionally by `remote.normalizeRequest` before the
 *     request reaches a handler, so it is deliberately absent from this type.
 *     Sending it would not be an error, it would just be silently dropped, and a
 *     field that looks settable but is not is worse than one that is missing.
 *
 * `cmd` is also taken from the ENVELOPE and overwrites whatever the payload
 * says, so the two must agree. `requestPayload` below builds them together.
 *
 * The `data` shapes a response carries (SessionsData, StatusData, ...) are NOT
 * mirrored here. They already exist as generated TypeScript in the desktop's
 * `@bindings/internal/protocol` barrel, which the mobile build aliases at the
 * same files; duplicating them would create the third mirror this package
 * exists to argue against.
 */
export interface RequestFields {
  poll?: string;
  dryRun?: boolean;
  provider?: string;
  project?: string;
  ref?: string;
  session?: string;
  event?: string;
  detail?: string;
  text?: string;
  /** The size the asking client can show, for cmd=shellCreate. */
  cols?: number;
  rows?: number;
  lines?: number;
  /** Typed argument payload for the project-centric commands. */
  args?: unknown;
}

/**
 * The response body of a `resp` frame.
 *
 * Mirrors `protocol.Response`. `ok` is always present; `error` and `data` carry
 * `omitempty`. An `ok: false` is an APPLICATION error and is not an `err`
 * frame — the correlator surfaces it as a rejection all the same, because every
 * caller in the desktop store treats the two identically.
 */
export interface Response<T = unknown> {
  ok: boolean;
  error?: string;
  data?: T;
}

// ---------------------------------------------------------------------------
// Command policy, mirrored so a mistake costs a refusal instead of the socket
// ---------------------------------------------------------------------------

/**
 * The namespace the transport reserves for commands it speaks to ITSELF — M1's
 * in-band bearer-key hello, and whatever M2's pairing needs.
 *
 * Mirrors `remote.remoteCmdPrefix` (internal/remote/policy.go).
 */
export const REMOTE_CMD_PREFIX = "remote.";

/**
 * The `req` frame that carries M1's bearer key.
 *
 * Mirrors `remote.helloCmd` (internal/remote/insecure.go). Its payload is
 * `{"key":"..."}` and is deliberately NOT a Request: the key is not a command
 * argument, and putting it in one would make it something a future handler could
 * read. See `helloFrame` below.
 */
export const HELLO_CMD = REMOTE_CMD_PREFIX + "hello";

/**
 * The shortest bearer key the daemon's M1 listener accepts; a shorter one makes
 * it refuse to start at all.
 *
 * Mirrors `remote.insecureMinKeyLen` (internal/remote/insecure.go).
 */
export const INSECURE_MIN_KEY_LEN = 16;

/**
 * Commands refused for EVERY remote peer, unconditionally, in code rather than
 * in configuration.
 *
 * Mirrors `remote.deniedCommands` (internal/remote/policy.go). It is mirrored
 * here for one narrow reason, and it is not defence: a denied `cmd` is answered
 * with `unknown_cmd` and then the daemon CLOSES THE CONNECTION, taking every
 * live pane subscription with it. So a single mistyped command costs a full
 * reconnect and a re-subscribe of every pane. Checking locally first turns that
 * into a rejected promise.
 *
 * This is NOT the capability model and must not be read as one. M2 adds a closed
 * allowlist of tiers on top, enforced in the daemon and in the native plugin;
 * this list is the floor beneath it, and a command absent from this list is not
 * thereby permitted.
 */
export const DENIED_COMMANDS: readonly string[] = [
  "stop",
  "reload",
  "renameProject",
  "hookEvent",
  "pairBegin",
  "pairStatus",
  "pairConfirm",
  "devices",
  "revokeDevice",
];

const deniedSet = new Set(DENIED_COMMANDS);

/**
 * Whether `cmd` is refused for every remote peer.
 *
 * Mirrors `remote.CommandDenied`, including its two non-obvious cases: an EMPTY
 * command is denied (a req frame naming nothing has nothing to authorize), and
 * anything in the `remote.` namespace is denied — the bearer hello included,
 * which is why the handshake is sent by the transport's connect path and never
 * through the ordinary request path.
 */
export function commandDenied(cmd: string): boolean {
  if (cmd === "") return true;
  if (cmd.startsWith(REMOTE_CMD_PREFIX)) return true;
  return deniedSet.has(cmd);
}

// ---------------------------------------------------------------------------
// Pane names and bounds
// ---------------------------------------------------------------------------

/**
 * The shape a lola tmux session name may take.
 *
 * Mirrors `panebus.paneNameRe` (internal/panebus/panebus.go). The suffixes are
 * lola's auxiliary sessions: `-shell-N` embedded shells, `-review` a visible
 * review pass, `-dev-N` a dev tab. All of them resolve, because the daemon's
 * identity gate maps an auxiliary name back to its parent session.
 *
 * A pane name is `SessionInfo.tmuxName || SessionInfo.id`.
 */
export const PANE_NAME_RE =
  /^lola-[A-Za-z0-9._-]+(?:-shell-\d+|-review|-dev-\d+)?$/;

/**
 * The longest pane name that may reach a frame.
 *
 * Mirrors `remote.maxPaneName` (internal/remote/pane.go) and
 * `panebus.MaxPaneNameLen`, which are the same number for different reasons —
 * one caps what may reach a frame and a log line, the other what may reach a
 * tmux argv. Names lola builds are nowhere near either.
 */
export const MAX_PANE_NAME = 128;

/** Mirrors `panebus.ValidName`, minus the daemon-side existence check. */
export function validPaneName(name: string): boolean {
  if (name === "" || utf8Length(name) > MAX_PANE_NAME) return false;
  return PANE_NAME_RE.test(name);
}

/**
 * One scroll request's clamp. The daemon clamps server-side anyway; clamping
 * here keeps the request honest about what it will actually do.
 *
 * Mirrors `tmux.MaxScrollLines` (internal/tmux/client.go).
 */
export const MAX_SCROLL_LINES = 500;

/**
 * How many `req` frames one connection may have in flight before the daemon
 * refuses further ones with a non-fatal `rate_limited`.
 *
 * Mirrors `remote.reqConcurrency` (internal/remote/server.go). The correlator
 * uses it as a queue depth so a burst waits instead of being refused.
 */
export const MAX_REQUESTS_IN_FLIGHT = 4;

/**
 * The depth of the daemon's ordered pane-input queue. Overflowing it is FATAL —
 * the daemon closes the connection rather than silently dropping keystrokes into
 * a live agent.
 *
 * Mirrors `remote.paneQueueDepth` (internal/remote/server.go).
 */
export const PANE_QUEUE_DEPTH = 256;

/**
 * The window covering the TLS handshake AND the bearer hello. After it the
 * daemon CLEARS its read deadline: an attached pane is legitimately silent for
 * minutes, and there is no application-level keepalive in either direction.
 *
 * Mirrors `remote.handshakeTimeout` (internal/remote/server.go).
 */
export const HANDSHAKE_TIMEOUT_MS = 10_000;

/**
 * The bound on one server-side frame write. A client that stops READING has its
 * connection torn down, so a transport must drain the socket continuously and
 * buffer on the client rather than applying backpressure to the daemon.
 *
 * Mirrors `remote.writeTimeout` (internal/remote/server.go).
 */
export const WRITE_TIMEOUT_MS = 15_000;

// ---------------------------------------------------------------------------
// Frame builders
// ---------------------------------------------------------------------------

/**
 * The M1 bearer-key handshake frame: the FIRST and ONLY frame a client sends
 * before authenticating, answered by an ordinary `resp` on the same id.
 *
 * Mirrors the shape `remote.insecureAuthorizer.Authenticate` requires
 * (internal/remote/insecure.go). Every failure — wrong type, wrong cmd, bad
 * payload, wrong key — is the identical `denied` / "authenticate first" refusal
 * followed by a close, so a client learns nothing from the shape of a rejection
 * except that it was rejected.
 *
 * The key is a runtime value: it comes from the environment or from a field the
 * operator types into the app. It is never committed, never logged, and never
 * placed in a URL.
 */
export function helloFrame(id: string, key: string): Frame {
  return {
    v: FRAME_VERSION_CURRENT,
    type: FRAME_REQ,
    id,
    cmd: HELLO_CMD,
    payload: { key },
  };
}

/**
 * The `req` payload, with its keys in `protocol.Request`'s DECLARATION ORDER and
 * its `omitempty` fields dropped when zero.
 *
 * The order is not cosmetic: encoding/json emits struct fields in declaration
 * order, so reproducing the daemon's bytes means reproducing the order. A spread
 * of a caller-built object would emit whatever order the caller happened to type.
 */
export function requestPayload(
  cmd: string,
  fields: RequestFields = {},
): Record<string, unknown> {
  const p: Record<string, unknown> = { cmd }; // no omitempty on Cmd
  if (fields.poll) p.poll = fields.poll;
  if (fields.dryRun) p.dryRun = true;
  if (fields.provider) p.provider = fields.provider;
  if (fields.project) p.project = fields.project;
  if (fields.ref) p.ref = fields.ref;
  if (fields.session) p.session = fields.session;
  if (fields.event) p.event = fields.event;
  if (fields.detail) p.detail = fields.detail;
  // Hook is deliberately absent: cmd=hookEvent is denied for every remote peer.
  // Force is deliberately absent: normalizeRequest clears it unconditionally.
  if (fields.text) p.text = fields.text;
  // Cols/Rows precede Lines, as they do in the Go struct — this function
  // reproduces encoding/json's declaration order, not a spread.
  if (fields.cols) p.cols = Math.trunc(fields.cols);
  if (fields.rows) p.rows = Math.trunc(fields.rows);
  if (fields.lines) p.lines = Math.trunc(fields.lines);
  if (fields.args !== undefined && fields.args !== null) p.args = fields.args;
  return p;
}

/**
 * A `req` frame. `cmd` is written to BOTH the envelope and the payload because
 * that is what the daemon's own round trip produces; the envelope's copy is the
 * one that is authorized and the one that overwrites the payload's.
 */
export function requestFrame(
  id: string,
  cmd: string,
  fields: RequestFields = {},
): Frame {
  return {
    v: FRAME_VERSION_CURRENT,
    type: FRAME_REQ,
    id,
    cmd,
    payload: requestPayload(cmd, fields),
  };
}

/** A `sub` frame. `cols`/`rows` are advisory; omit them and the daemon does not care. */
export function subFrame(
  id: string,
  pane: string,
  viewport?: SubPayload,
): Frame {
  const f: Frame = { v: FRAME_VERSION_CURRENT, type: FRAME_SUB, id, pane };
  if (viewport && (viewport.cols || viewport.rows)) f.payload = viewport;
  return f;
}

/**
 * An `unsub` frame. It carries no payload and receives NO ACKNOWLEDGEMENT of any
 * kind, so a client must never wait for a reply to one — it cannot distinguish
 * "unsubscribed" from "frame lost", and the distinction does not matter.
 */
export function unsubFrame(pane: string): Frame {
  return { v: FRAME_VERSION_CURRENT, type: FRAME_UNSUB, pane };
}

/** A `pty` write frame. `data` is base64 of the raw bytes. */
export function ptyWriteFrame(
  pane: string,
  dataBase64: string,
  id?: string,
): Frame {
  const f: Frame = { v: FRAME_VERSION_CURRENT, type: FRAME_PTY, pane };
  if (id) f.id = id;
  // `PTYInputPayload.Data` is `json:"data,omitempty"`, so an empty payload is
  // OMITTED by the daemon's own encoder rather than written as `""`. Every
  // builder in this file reproduces omitempty exactly, because the golden
  // vectors compare BYTES: a field written where Go would drop it is a drift
  // that only shows up the day someone pins the frame in question.
  f.payload = dataBase64
    ? { action: PTY_ACTION_WRITE, data: dataBase64 }
    : { action: PTY_ACTION_WRITE };
  return f;
}

/**
 * A `pty` scroll frame. POSITIVE `lines` scrolls BACK into history, negative
 * scrolls forward again — the daemon's convention, mirrored from ScrollPane's
 * `up := lines > 0`. The value is clamped to `MAX_SCROLL_LINES`, matching what
 * the daemon will do with it anyway.
 */
export function ptyScrollFrame(
  pane: string,
  lines: number,
  id?: string,
): Frame {
  const n = Math.trunc(lines);
  const clamped = Math.max(-MAX_SCROLL_LINES, Math.min(MAX_SCROLL_LINES, n));
  const f: Frame = { v: FRAME_VERSION_CURRENT, type: FRAME_PTY, pane };
  if (id) f.id = id;
  // `Lines` is `json:"lines,omitempty"`. A zero scroll is a no-op the daemon
  // ignores, and the caller already returns early on one; the omission keeps the
  // bytes identical to what Go would write for the same struct.
  f.payload = clamped
    ? { action: PTY_ACTION_SCROLL, lines: clamped }
    : { action: PTY_ACTION_SCROLL };
  return f;
}

/**
 * A `pty` resize frame. RECORDED AND IGNORED by the M1 daemon; it is sent so the
 * daemon has an honest record of the subscriber's viewport, not because anything
 * will change.
 */
export function ptyResizeFrame(
  pane: string,
  cols: number,
  rows: number,
  id?: string,
): Frame {
  const f: Frame = { v: FRAME_VERSION_CURRENT, type: FRAME_PTY, pane };
  if (id) f.id = id;
  // `Cols` and `Rows` are both `json:"...,omitempty"`, and they are omitted
  // independently: a zero column count with a real row count writes only rows.
  const payload: { action: string; cols?: number; rows?: number } = {
    action: PTY_ACTION_RESIZE,
  };
  const c = Math.trunc(cols);
  const r = Math.trunc(rows);
  if (c) payload.cols = c;
  if (r) payload.rows = r;
  f.payload = payload;
  return f;
}

/** An `err` frame, for a refusal this client made locally. */
export function errorFrame(id: string, code: string, message?: string): Frame {
  const payload: ErrPayload = { code };
  if (message) payload.message = message;
  return { v: FRAME_VERSION_CURRENT, type: FRAME_ERR, id, payload };
}

// ---------------------------------------------------------------------------
// Sequence numbers
// ---------------------------------------------------------------------------

/**
 * What a client should do about a gap in a pane's sequence numbers.
 *
 * Sequence numbers are per pane, shared across `resync` and `pty`, start at 1
 * and come from the bus VERBATIM — including the frames the bus DROPPED for a
 * subscriber that fell behind, which is the only way a gap is visible at all.
 *
 * The recovery is already built into the daemon: a subscriber whose queue
 * overflowed is marked desynced, further output is withheld while the counter
 * keeps advancing, and the next flush sends a fresh full `resync`. So from the
 * client a drop looks like `seq N` -> jump -> `seq M` carrying a resync.
 *
 *   "ok"        contiguous, or the first frame of a subscription.
 *   "repaired"  a gap arriving on a RESYNC. Self-healing: repaint from it and
 *               adopt M. Do NOT re-subscribe; the daemon already repaired.
 *   "torn"      a gap arriving on a PTY frame. The byte stream is missing a
 *               range that cannot be replayed (a byte range cannot resume from
 *               halfway through an escape sequence), so re-subscribe.
 */
export type GapVerdict = "ok" | "repaired" | "torn";

/**
 * Classify one pane frame against the last sequence number seen on that pane.
 * `lastSeq` of 0 means nothing has been seen yet.
 */
export function classifyGap(lastSeq: number, frame: Frame): GapVerdict {
  const seq = frame.seq ?? 0;
  if (lastSeq === 0 || seq === 0 || seq === lastSeq + 1) return "ok";
  if (seq <= lastSeq) return "ok"; // a duplicate or a re-subscribe restart; not a gap
  return frame.type === FRAME_RESYNC ? "repaired" : "torn";
}

// ---------------------------------------------------------------------------

/** UTF-8 byte length of a string, without allocating an encoder per call. */
function utf8Length(s: string): number {
  let n = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    if (c < 0x80) n += 1;
    else if (c < 0x800) n += 2;
    else if (c >= 0xd800 && c <= 0xdbff && i + 1 < s.length) {
      const d = s.charCodeAt(i + 1);
      if (d >= 0xdc00 && d <= 0xdfff) {
        n += 4;
        i++;
        continue;
      }
      n += 3;
    } else n += 3;
  }
  return n;
}
