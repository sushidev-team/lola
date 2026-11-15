# Spec 03 — Hub blind relay

Status: implemented on `feature/mobile-rethink` (schedule: `00-build-plan.md`); this file stays the standing contract. Written as: contract for workflow 2 (daemon + hub) and workflow 3 (Swift transport).
Implements `docs/mobile-rethink.md` §3.3. Decisions there are settled; this file
makes them precise against the code as of `b107fb9`. Where the code forces a
change, it is listed under **Deviations** (§16).

Related specs: 01 (pairing, device registry, tiers, inner mutual TLS), 02
(events), 04 (client). **This spec is the only owner of the relay wire**: the
WebSocket codec, every `/v1/relay/*` request and answer (paths, headers,
statuses, close codes, limits), the hub relay, the daemon relay client and the
relay credential lifecycle. Spec 04 implements the phone side of §13 in its own
files and refers here for every wire fact. The inner protocol is spec 01's;
§13.5 lists exactly the fields and rules spec 01 must carry.

---

## 1. Summary

```
 phone ──TLS(hub cert)──WS /v1/relay/phone──▶ ┌──────────┐ ◀──WS /v1/relay/accept──TLS(hub)── daemon
  │ (Lola-Relay-Rv, Lola-Relay-Bearer)         │ lola-hub │   (hubproto.Sign, ?conn=<id>)      │
  │                                            │  splice  │ ◀──WS /v1/relay/daemon (control)───┤
  │                                            └──────────┘    (hubproto.Sign, standing)       │
  └──────────── inner mutual TLS 1.3 (phone key ⇄ daemon device.key), end to end ─────────────┘
```

- Three WebSocket endpoints and one signed POST. All on the hub's existing
  listener, all in `hubproto` first.
- The hub splices two WebSocket byte streams. Inside is the same mutual TLS 1.3
  session a LAN connection carries (spec 01). The hub has no key for it.
- The daemon dials out only. The hub never dials a daemon.
- The daemon hands the spliced stream to `remote.Server`, which wraps it in
  `tls.Server(raw, s.tlsConfig)` (spec 01 §4.1's field). One code path for LAN
  and relay from the TLS handshake on.
- iOS runs the inner TLS through a **loopback bridge** (§13): the WebSocket is
  piped to a one-shot `127.0.0.1` listener and the ordinary `NWConnection` TLS
  stack (the one the direct path uses) connects to it.
- Phone relay credentials: a per-phone rendezvous id (`rv`) and bearer, minted
  by the daemon, delivered only inside the inner TLS (`PairResult.Relay`,
  `relayCred`). The QR carries `{url, rv, tok}` of a single-use pairing window:
  `tok` is a random token the daemon mints for that window alone (never derived
  from the pairing secret); the hub stores its hash with the window's expiry.

---

## 2. WebSocket codec — `hubproto/ws.go`

Stdlib only (`bufio`, `crypto/rand`, `crypto/sha1`, `crypto/tls`,
`encoding/base64`, `encoding/binary`, `net`, `net/http`, `net/url`, `sync`,
`sync/atomic`, `time`). RFC 6455, version 13, no extensions, binary frames
only. Used by the hub (server role: both relay legs and the control channel)
and the daemon (client role: control channel and accept leg).

### 2.1 Types

```go
package hubproto

type WSRole uint8

const (
	WSClient WSRole = iota + 1 // masks every frame it sends; refuses masked frames
	WSServer                   // never masks; refuses unmasked frames
)

// WSConfig tunes one connection. The zero value is valid.
type WSConfig struct {
	MaxFramePayload int           // 0 = WSMaxFramePayload; larger frame → close 1009
	PingInterval    time.Duration // 0 = send no pings
	DeadAfter       time.Duration // 0 = no watchdog; else close if NOTHING was received this long
	WriteStall      time.Duration // 0 = none; bound on a data write when the caller set no write deadline
	// OnFrame, when set, is called by the reading goroutine for EVERY frame
	// read (any opcode), with its wire size (header + payload), after the
	// header is parsed and before the payload is consumed. The hub's splice
	// accounting hangs here (§6.2). It must not block.
	OnFrame func(wire int)
}

// WSConn is a WebSocket carrying an opaque byte stream. It implements net.Conn.
// One goroutine may Read while others Write; writes (data and control) are
// serialized by wsem.
type WSConn struct { /* nc net.Conn; br *bufio.Reader; role; cfg;
	wsem chan struct{} (cap 1: the write lock, acquirable with a timeout);
	wdl atomic.Int64 (caller's write deadline, unix nanos; 0 = none);
	rmu sync.Mutex; remaining int64; fragmented bool; emptyRun int;
	ctlWindowStart time.Time; ctlInWindow int;
	broken atomic.Bool; closeSent, closeRecv atomic.Bool; closeRecvCh chan struct{};
	peerCode uint16; lastRx atomic.Int64; done chan struct{} */ }

func NewWSConn(nc net.Conn, br *bufio.Reader, role WSRole, cfg WSConfig) *WSConn

// net.Conn
func (c *WSConn) Read(p []byte) (int, error)   // payload bytes of binary/continuation frames
func (c *WSConn) Write(p []byte) (int, error)  // split into FIN binary frames ≤ WSWriteChunk
func (c *WSConn) Close() error                 // CloseWithCode(WSCloseNormal) (§2.7)
func (c *WSConn) LocalAddr() net.Addr
func (c *WSConn) RemoteAddr() net.Addr
func (c *WSConn) SetDeadline(t time.Time) error      // SetReadDeadline(t) + SetWriteDeadline(t)
func (c *WSConn) SetReadDeadline(t time.Time) error  // passed to nc (only Read/ReadMessage read nc)
func (c *WSConn) SetWriteDeadline(t time.Time) error // STORED in wdl; never passed to nc (§2.6)

// Extras
func (c *WSConn) CloseWithCode(code uint16) error // reason text is never sent; bounded (§2.7)
func (c *WSConn) CloseNow(code uint16) error      // best-effort Close frame, no wait for the peer (§2.7)
func (c *WSConn) CloseWrite() error               // send Close, keep reading until peer Close/EOF
func (c *WSConn) PeerCloseCode() (uint16, bool)
func (c *WSConn) ReadMessage(limit int) ([]byte, error) // one FIN binary frame, ≤ limit; control channel only
func (c *WSConn) WriteMessage(p []byte) error           // one FIN binary frame; len ≤ RelayMaxControlMessage

// Server side.
func CheckUpgrade(r *http.Request) error // validation only, writes nothing (§2.3)
func AcceptWebSocket(w http.ResponseWriter, r *http.Request, cfg WSConfig) (*WSConn, error)

// Client side.
type WSDialer struct {
	TLSConfig        *tls.Config                           // nil = system roots; NextProtos forced to ["http/1.1"]
	Proxy            func(*http.Request) (*url.URL, error) // nil = http.ProxyFromEnvironment
	HandshakeTimeout time.Duration                         // 0 = WSHandshakeTimeout
	Config           WSConfig
}
func (d WSDialer) Dial(ctx context.Context, req *http.Request) (*WSConn, error)

// WSHandshakeError is a non-101 answer. Code and RetryAfter are RAW hub
// values: hubproto does not bound them. internal/hub converts this error
// before anything else sees it (§10.1).
type WSHandshakeError struct {
	Status     int
	Code       string        // ErrorResponse.code, or the Lola-Relay-Code header when the body is absent; "" otherwise
	RetryAfter time.Duration // from Retry-After seconds, 0 = none
}
func (e *WSHandshakeError) Error() string // "hubproto: websocket handshake refused: <status>" (no code text)

var (
	ErrWSHandshake = errors.New("hubproto: bad websocket handshake")
	ErrWSProtocol  = errors.New("hubproto: websocket protocol violation")
	ErrWSTooBig    = errors.New("hubproto: websocket frame too large")
	ErrWSClosed    = errors.New("hubproto: websocket closed")
	ErrWSBroken    = errors.New("hubproto: websocket stream broken by a failed write")
)
```

A data leg uses `Read`/`Write` only; the control channel uses
`ReadMessage`/`WriteMessage` only. Mixing them on one conn is a programming
error (documented, not checked).

### 2.2 Constants (in `hubproto/ws.go`)

| Name | Value | Why |
|---|---|---|
| `WSSubprotocol` | `"lola-relay.v1"` | required in `Sec-WebSocket-Protocol`; versions the relay |
| `WSMaxFramePayload` | `64 << 10` | read cap per frame; a bigger frame closes 1009 |
| `WSWriteChunk` | `16 << 10` | every outgoing data frame ≤ this (matches TLS record size) |
| `WSMaxControlPayload` | `125` | RFC 6455 §5.5 |
| `WSHandshakeTimeout` | `15 * time.Second` | daemon dial + upgrade bound |
| `WSControlWriteTimeout` | `2 * time.Second` | deadline of every control-frame write and of taking `wsem` for one |
| `WSCloseTimeout` | `2 * time.Second` | wait for the peer's Close after sending ours |
| `WSMaxHandshakeResponse` | `4 << 10` | non-101 body read cap |
| `WSMaxControlFramesPerSec` | `4` | control frames (ping, pong, close) received per 1 s window; above → close 1008 |
| `WSMaxEmptyFramesInARow` | `16` | consecutive zero-payload data frames; above → close 1002 |

### 2.3 Server handshake

`CheckUpgrade(r)` returns `ErrWSHandshake` (wrapped, naming the header, never
its value) unless all hold:

1. `r.Method == GET`, `r.ProtoAtLeast(1, 1)`, `r.ProtoMajor == 1` (no HTTP/2 or
   HTTP/3 extended CONNECT).
2. `Upgrade` has the token `websocket` (case-insensitive); `Connection` has the
   token `upgrade`.
3. `Sec-WebSocket-Version` is exactly `13`.
4. `Sec-WebSocket-Key` is exactly one value, base64 std of exactly 16 bytes.
5. `Sec-WebSocket-Protocol` lists `WSSubprotocol`.
6. **No `Origin` header.** Daemon and phone clients never send one; a browser
   always does. This refuses cross-site WebSocket hijacking by construction.
   (The hub answers 403 `forbidden` for this case, §5.) `URLSessionWebSocketTask`
   sends no `Origin` unless the request sets one (§13.2 forbids it).
7. `Sec-WebSocket-Extensions` is ignored and never echoed (no compression:
   compression over ciphertext gains nothing and adds a decompression-bomb
   surface).

`AcceptWebSocket` runs `CheckUpgrade`, then
`http.NewResponseController(w).Hijack()` (this walks `Unwrap()`, see §8.1),
then **clears both deadlines** (`nc.SetDeadline(time.Time{})`: `net/http` armed
`ReadTimeout`/`WriteTimeout` on the conn before the handler ran and a hijack
does not clear them), writes exactly:

```
HTTP/1.1 101 Switching Protocols\r\n
Upgrade: websocket\r\n
Connection: Upgrade\r\n
Sec-WebSocket-Accept: <base64(sha1(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))>\r\n
Sec-WebSocket-Protocol: lola-relay.v1\r\n
\r\n
```

under a `WSControlWriteTimeout` write deadline, and returns
`NewWSConn(nc, brw.Reader, WSServer, cfg)`; bytes already buffered in
`brw.Reader` are read first. If `brw.Reader.Buffered() > 0` before the 101 is
written (a client that sent frames before the upgrade answer), the conn is
closed: RFC 6455 forbids it.

### 2.4 Client handshake

`WSDialer.Dial(ctx, req)`:

1. `req` is a fully built `GET https://…` request whose headers the caller has
   set (and, for daemon legs, signed with `hubproto.Sign(req, dev, priv, nil)`
   — the signature covers method, `RequestURI()` including the query, and the
   empty body hash). Dial adds `Upgrade`, `Connection`, `Sec-WebSocket-Version`,
   a fresh 16-byte `Sec-WebSocket-Key` from `crypto/rand`, and
   `Sec-WebSocket-Protocol: lola-relay.v1`. It never sets `Origin`.
2. Proxy: `Proxy(req)`; a non-nil proxy URL with scheme `http` gets an
   `HTTP CONNECT host:443` (status 200 required, response ≤ 4 KiB, no auth
   beyond what the URL carries). Any other scheme is refused. This keeps parity
   with `internal/hub.Client`, whose `http.Client` honours
   `http.ProxyFromEnvironment`.
3. TCP dial with `net.Dialer{KeepAlive: 30s}`, TLS client with
   `ServerName = req.URL.Hostname()`, `MinVersion: tls.VersionTLS12`,
   **`NextProtos: []string{"http/1.1"}`** (ingress-nginx would otherwise
   negotiate h2, where an Upgrade does not exist). `http` scheme is allowed only
   for loopback hosts (tests, `lola-hub dev`).
4. `req.Write(conn)`, then `http.ReadResponse(br, req)`. Status 101 requires
   `Upgrade: websocket`, `Connection: upgrade`, the exact
   `Sec-WebSocket-Accept`, and `Sec-WebSocket-Protocol == WSSubprotocol`;
   anything else → `ErrWSHandshake`. Any other status → `*WSHandshakeError`
   from the body (`DecodeStrict` into `ErrorResponse`, ≤
   `WSMaxHandshakeResponse`), the `Lola-Relay-Code` header when the body does
   not decode, and `Retry-After`.
5. The whole of 2–4 runs under `min(ctx deadline, HandshakeTimeout)`; on
   success the deadlines are cleared and `NewWSConn(conn, br, WSClient, cfg)`
   returned.

### 2.5 Framing rules (both roles)

Reading a frame header:

- RSV1–3 set → close 1002. Unknown opcode → 1002.
- Mask bit must be 1 on a frame a server reads and 0 on a frame a client reads;
  otherwise 1002.
- Payload length uses the minimal encoding (a 16-bit length < 126 or a 64-bit
  length < 65536 → 1002); the 64-bit MSB must be 0 (1002).
- Payload > `MaxFramePayload` → 1009 before reading it.
- Control frames (`0x8` close, `0x9` ping, `0xA` pong): FIN must be 1 and
  payload ≤ 125, else 1002. They may arrive between fragments.
- Text frame (`0x1`) → 1003. The relay carries bytes only.
- After the header is parsed: `lastRx = now`, then `cfg.OnFrame(headerLen +
  payloadLen)` if set.

Flood rules (every role, every conn):

- **Control frames**: a counter per 1-second window (`ctlWindowStart`,
  `ctlInWindow`) counts every received close, ping and pong. More than
  `WSMaxControlFramesPerSec` in one window → close 1008. Pongs we send are
  therefore bounded by the same number.
- **Empty data frames**: a zero-payload binary or continuation frame increments
  `emptyRun`; any frame with payload resets it. `emptyRun >
  WSMaxEmptyFramesInARow` → close 1002.

Fragmentation policy (byte-stream semantics, **no reassembly buffer**):

- `Read` treats a binary frame with FIN=0 followed by continuation frames
  (`0x0`) exactly like a sequence of FIN=1 binary frames: each frame's payload
  is streamed to the caller as it arrives. A continuation without an open
  fragment, or a new binary frame while a fragment is open → 1002. Empty data
  frames are skipped within the flood rule (`Read` never returns `0, nil`).
- `ReadMessage(limit)` requires FIN=1 on a binary frame; a fragmented message
  → 1009 (the control channel never fragments). Payload > `limit` → 1009.
- `Write` never fragments: it emits FIN=1 binary frames of ≤ `WSWriteChunk`.

Masking (client role): each frame gets a fresh 4-byte key from `crypto/rand`
(read in 4 KiB batches into a per-conn pool to avoid a syscall per frame);
payload is XOR-masked into a scratch buffer, never in the caller's slice.

Ping/pong: a ping read inside `Read`/`ReadMessage` is answered with a pong of
the same payload (a control write, §2.6) before reading continues. A pong is
consumed silently. With `PingInterval > 0` a goroutine sends an empty ping
every interval (a control write; skipped if `wsem` is not free within
`WSControlWriteTimeout` — a ping is a courtesy, liveness is `lastRx`). With
`DeadAfter > 0` a watchdog closes the underlying conn when `now - lastRx >
DeadAfter` (so a vanished peer unblocks a `Read` that has no deadline — the
`remote` frame loop runs without one; §11). Both goroutines exit on close.

### 2.6 Deadlines and the write path

The caller's write deadline governs **data** only. Control frames never inherit
it. This is what keeps an idle relayed `remote` connection alive:
`remote.deadlineWriter` (`internal/remote/conn.go`) arms `now + writeTimeout`
(15 s) before each frame write and never clears it, so a deadline passed
through to `nc` would be in the past by the time the next hub ping (every
25 s) asks for a pong.

- `SetWriteDeadline(t)` stores `t` in `wdl`. Nothing else.
- **Data write** (`Write`, `WriteMessage`): acquire `wsem` (blocking; it is
  held at most `WSControlWriteTimeout` by a control write, or by another data
  write bounded as below). For each frame: `nc.SetWriteDeadline(d)` where
  `d = wdl` if set, else `now + cfg.WriteStall` if `WriteStall > 0`, else
  zero; then write header + payload. Release `wsem`. An expired `wdl` fails
  the write at once with a timeout before any byte is written (checked against
  `now` before the frame starts), so a timeout BETWEEN frames leaves the stream
  intact.
- **Control write** (pong, ping, Close): acquire `wsem` with a
  `WSControlWriteTimeout` timer (`select`); on timeout the frame is skipped
  (pong, ping) or handled as in §2.7 (Close). Under `wsem`:
  `nc.SetWriteDeadline(now + WSControlWriteTimeout)`, write, release. The next
  data write re-arms its own deadline, so nothing stale remains.
- **Any error from `nc.Write`** (timeout included) after the first byte of a
  frame may have left a partial frame: set `broken`, `nc.Close()`. Every later
  `Write`/`WriteMessage` returns `ErrWSBroken`; `Read` returns `ErrWSBroken`
  after buffered bytes. A WebSocket frame is never resumed.
- `SetReadDeadline` is passed to `nc`. Only `Read`/`ReadMessage` read `nc`,
  so a read deadline bounds exactly "waiting for the peer". A read timeout
  inside a frame (header or payload partly read) also sets `broken`.

### 2.7 Close

- Received Close: record the code (absent payload = 1005), signal
  `closeRecvCh`, reply with a Close carrying the same code if we have not sent
  one (control write), then `Read` returns `io.EOF`; `Write` afterwards returns
  `ErrWSClosed`.
- `CloseWithCode(code)` — bounded by `WSControlWriteTimeout + WSCloseTimeout`
  whatever the peer and any writer do:
  1. If `closeSent` was already set, wait for `done` and return.
  2. `nc.SetWriteDeadline(now + WSControlWriteTimeout)` **without** taking
     `wsem`. This fails a data write parked in `nc.Write` toward a peer that
     stopped reading (TCP window zero), which releases `wsem`.
  3. Take `wsem` with a `WSControlWriteTimeout` timer. Acquired: write the
     Close frame (2-byte code, no reason), release. Not acquired: skip the
     frame.
  4. If the frame was written, wait on `closeRecvCh` up to `WSCloseTimeout`
     (a goroutine inside `Read` receives it; if nobody reads, the wait simply
     times out). The server role does not wait once the peer's Close arrived
     (RFC 6455 §7.1.1).
  5. `nc.Close()`; close `done` (stops ping and watchdog goroutines).
- `CloseNow(code)`: steps 1–3 only, then `nc.Close()`. Used by shutdown paths
  that must not wait for a peer (§10.5, §11).
- `CloseWrite()`: write Close (control write), mark the write side shut, keep
  `Read` working until the peer's Close or EOF. Used by the hub splice for
  half-close (§6.2).
- Close codes: 1000–1011 are constants in `hubproto/ws.go` (the codec uses
  them); 4000–4004 are in `hubproto/relay.go`.

| Code | Name | Sent by | Meaning |
|---|---|---|---|
| 1000 | `WSCloseNormal` | any | normal end |
| 1001 | `WSCloseGoingAway` | any | process shutdown |
| 1002 | `WSCloseProtocol` | any | framing violation, empty-frame flood |
| 1003 | `WSCloseUnsupported` | any | text frame |
| 1008 | `WSClosePolicy` | any | control-frame flood; hub: limit or auth state changed, write stall |
| 1009 | `WSCloseTooBig` | any | frame over cap |
| 1011 | `WSCloseInternal` | any | internal error |
| 4000 | `RelayCloseReplaced` | hub | a newer control channel for this device |
| 4001 | `RelayCloseRevoked` | hub | device revoked or a paired rendezvous removed |
| 4002 | `RelayCloseIdle` | hub | `RelayIdleTimeout` with no payload either way |
| 4003 | `RelayCloseQuota` | hub | daily byte quota (device or rv) exhausted |
| 4004 | `RelayClosePeerGone` | hub | the other leg ended, or this leg arrived after the phone left |

Close codes are seen only by the daemon (accept leg, control channel) and by a
phone leg that was already spliced. Every refusal BEFORE a splice is an HTTP
status (§5.3); a phone never needs a 4xxx code to learn why a dial failed.

---

## 3. Relay wire — `hubproto/relay.go`

### 3.1 Paths and headers

```go
const (
	PathRelayDaemon     = "/v1/relay/daemon"     // GET ?v=1, WS upgrade, signed    -> control channel
	PathRelayAccept     = "/v1/relay/accept"     // GET ?conn=<id>, WS upgrade, signed -> daemon data leg
	PathRelayPhone      = "/v1/relay/phone"      // GET, WS upgrade, bearer           -> phone data leg
	PathRelayRendezvous = "/v1/relay/rendezvous" // POST RendezvousRequest, signed    -> RendezvousResponse

	HeaderRelayRV     = "Lola-Relay-Rv"     // phone leg: rendezvous id
	HeaderRelayBearer = "Lola-Relay-Bearer" // phone leg: bearer (or pairing token)
	HeaderRelayCode   = "Lola-Relay-Code"   // every non-101 phone-leg answer: the hubproto Code
	QueryRelayConn    = "conn"              // accept leg; in the query so hubproto.Sign covers it
	QueryRelayVersion = "v"                 // control leg; "1"
)
```

- The rendezvous id and the bearer ride custom headers, not the query
  (ingress-nginx's default access log records the request line) and not
  `Authorization` (Apple documents it as a header the URL Loading System may
  replace or drop, and ingress-nginx's default log format records neither
  custom header). The hub's own `accessLog` logs method, path and status only.
- The hub reads **only** `Lola-Relay-Bearer` for the phone credential; an
  `Authorization` header on a phone dial is ignored.
- `Lola-Relay-Code` is set on every non-101 answer of `PathRelayPhone`,
  because `URLSessionWebSocketTask` exposes a refused upgrade's status and
  headers but not its body (§13.2). The body is still the ordinary
  `ErrorResponse`.

### 3.2 Identifiers

| Value | Shape | Made by | Notes |
|---|---|---|---|
| rendezvous id `rv` | `^[0-9a-f]{32}$` (128 random bits) | daemon | one per paired phone (never in a QR); one per open pairing window (in that QR, single use) |
| bearer | `^[A-Za-z0-9_-]{43}$` (256 random bits, base64url raw) | daemon | a paired phone's hub credential; reaches the phone only inside the inner TLS |
| pairing token `tok` | same shape as a bearer, made by `NewRelayBearer` | daemon | the window's hub credential; only in the QR (`PairRelayQR.Tok`, spec 01 §6.2); presented in `Lola-Relay-Bearer` like a bearer |
| bearer hash | `^[0-9a-f]{64}$` = hex(sha256(bearer ASCII)) | daemon | the only form the hub stores |
| conn id | `^[0-9a-f]{32}$` | hub | one per phone dial |

```go
func NewRelayID() (string, error)                    // 32 hex
func NewRelayBearer() (bearer, hash string, err error) // also mints a window tok
func RelayBearerHash(bearer string) string
func ValidRelayID(s string) bool
func ValidRelayBearer(s string) bool
func ValidRelayHash(s string) bool
```

### 3.3 Rendezvous registration (signed POST, replace-all)

One request registers, rotates and revokes: the body is the device's **whole**
set; entries the hub holds for this device that are not in it are deleted.

```go
type RelayRendezvous struct {
	RV           string    `json:"rv"`
	BearerSHA256 string    `json:"bearer_sha256"`
	PrevSHA256   string    `json:"prev_sha256,omitempty"` // previous bearer during rotation
	PrevUntil    time.Time `json:"prev_until,omitzero"`   // required iff PrevSHA256 != ""
	ExpiresAt    time.Time `json:"expires_at,omitzero"`   // pairing windows only; marks the entry a WINDOW
}

type RendezvousRequest struct {
	Entries []RelayRendezvous `json:"entries"` // may be empty: drop all
}

type RendezvousResponse struct {
	Registered int      `json:"registered"`          // entries stored
	Conflicts  []string `json:"conflicts,omitempty"` // rvs of THIS request refused: owned by another device
}

func (r RendezvousRequest) Validate(now time.Time) error
```

`Validate`: ≤ `RelayMaxRendezvousPerDevice` entries; every field shape-valid;
no duplicate `rv`; `PrevSHA256 != BearerSHA256`; `PrevUntil` set iff
`PrevSHA256` set, and `PrevUntil ≤ now + RelayPrevBearerGraceMax + MaxSkew`;
`ExpiresAt` zero or `≤ now + RelayPairingTTLMax + MaxSkew`. **No lower
bound**: a `PrevUntil` or `ExpiresAt` already past (clock skew, a slow request)
stores an entry that is simply not live (§4); it never refuses the set. The
`MaxSkew` slack lets a daemon clock up to 5 minutes ahead of the hub's register
a full-length value. Errors name the field, never the value.

The daemon stays inside the bounds on its own clock (§10.6:
`RelayPrevBearerGrace` < `RelayPrevBearerGraceMax`, window expiry =
`PairSecretTTL` < `RelayPairingTTLMax`) and omits expired values, so the slack
is never needed by a correct daemon with a correct clock.

Example:

```json
{"entries":[
  {"rv":"6f1c…","bearer_sha256":"9a0e…"},
  {"rv":"b20d…","bearer_sha256":"11f3…","prev_sha256":"c7d2…","prev_until":"2026-11-20T10:00:00Z"},
  {"rv":"0e55…","bearer_sha256":"4b8a…","expires_at":"2026-11-14T10:05:00Z"}
]}
```

### 3.4 Control-channel messages

Each message is one FIN binary frame carrying one JSON object, ≤
`RelayMaxControlMessage`. The receiver decodes into `RelayMsg` with
`encoding/json` (**lenient**: unknown fields are ignored), then switches on
`t`. An unknown `t` is ignored. A known `t` whose known fields fail validation
(table below) is a protocol error: close 1002. A future hub or daemon may add
fields to a known `t`; it may never change the meaning or shape of an existing
one.

```go
type RelayMsg struct {
	T      string `json:"t"`
	V      int    `json:"v,omitempty"`         // hello
	PingS  int    `json:"ping_s,omitempty"`    // hello: display only
	Max    int    `json:"max_conns,omitempty"` // hello: display only
	Conn   string `json:"conn,omitempty"`      // incoming, cancel, reject
	RV     string `json:"rv,omitempty"`        // incoming
	Reason string `json:"reason,omitempty"`    // reject, bye
}
```

| `t` | Direction | Fields and validation | Meaning |
|---|---|---|---|
| `hello` | hub → daemon | `v == RelayControlVersion` (else close + `unsupported`); `ping_s`, `max_conns` ignored except for one log line | first message after the 101 |
| `incoming` | hub → daemon | `ValidRelayID(conn)`, `ValidRelayID(rv)` | a phone authenticated for `rv`; dial accept within `RelayAcceptTimeout` |
| `cancel` | hub → daemon | `ValidRelayID(conn)` | the phone gave up; do not dial (a late dial gets 404) |
| `bye` | hub → daemon | `reason` ∈ `replaced`,`revoked`,`shutdown`, else treated as `shutdown` | sent before the matching close code |
| `reject` | daemon → hub | `ValidRelayID(conn)`, `reason` ∈ `busy`,`unknown`,`off` | the daemon will not accept |

**Hub values never steer the daemon.** The daemon's ping interval is its own
`RelayPingInterval`, its relay slot count its own `maxRelayConnections`; a
`hello` that names other values changes nothing. Unsolicited daemon → hub
messages other than `reject` are ignored. No message carries a session id,
path, device name, command or byte of the inner stream.

### 3.5 New error codes (in `relay.go`, added to the hub's `messages` map)

| Code | Status | Fixed message |
|---|---|---|
| `CodeOffline = "offline"` | 503 | `"daemon not connected"` |
| `CodeBusy = "busy"` | 503 | `"relay busy"` |
| `CodeRelayTimeout = "relay_timeout"` | 504 | `"daemon did not answer"` |

Existing codes reused: `bad_request` 400, `unauthorized` 401, `forbidden` 403,
`not_found` 404, `rate_limited` 429 (with `Retry-After`), `internal` 500.
(`conflict` 409 is no longer used by the relay: §5.1 reports conflicts per
entry.)

### 3.6 Limits (all `hubproto/relay.go` constants)

| Constant | Value | Enforced by |
|---|---|---|
| `RelayControlVersion` | `1` | hello `v` |
| `RelayMaxControlMessage` | `4 << 10` | both |
| `RelayPingInterval` | `25 * time.Second` | hub pings every leg; daemon pings its control channel |
| `RelayDeadAfter` | `60 * time.Second` | watchdog on every leg, both sides |
| `RelayWriteStall` | `30 * time.Second` | `WSConfig.WriteStall` on every hub leg and the daemon's legs; the hub's splice and control writer also arm it per write |
| `RelayAcceptTimeout` | `6 * time.Second` | hub waits for the accept leg; daemon bounds its accept dial |
| `RelayPhoneAttemptTimeout` | `8 * time.Second` | phone: one relay upgrade (dial to 101); must exceed `RelayAcceptTimeout` (mirrored by spec 04's `RELAY_UPGRADE_TIMEOUT_MS`) |
| `RelayFreshCredentialGrace` | `60 * time.Second` | phone: a 401 within this long after storing a new bearer (pairing or `relayCred`) is a backoff, not `credentialDead` — the hub may not have the new hash yet (mirrored by spec 04's `RELAY_FRESH_GRACE_MS`) |
| `RelayIdleTimeout` | `30 * time.Minute` | hub closes a splice with no payload either way (4002) |
| `RelayHalfCloseGrace` | `2 * time.Second` | hub, after one leg ended |
| `RelaySpliceBytesPerSec` / `RelaySpliceBurst` | `2 << 20` / `4 << 20` | hub, per splice, per direction (shaped, wire bytes) |
| `RelayDeviceBytesPerSec` | `4 << 20` | hub, all splices of a device, per direction (shaped, wire bytes) |
| `RelayOrgBytesPerSec` | `16 << 20` | hub, all splices of an org, per direction (shaped, wire bytes) |
| `RelayDailyBytesPerDevice` | `4 << 30` | hub, wire bytes both directions summed, UTC day (4003) |
| `RelayDailyBytesPerRV` | `1 << 30` | hub, same, per rendezvous id (4003) |
| `RelayMaxConnsPerDevice` | `4` | hub (live + pending), daemon (relay slots) |
| `RelayMaxConnsPerRV` | `2` | hub (live + pending), daemon (per-rv relay slots) |
| `RelayMaxConnsPerOrg` | `64` | hub |
| `RelayMaxConnsHub` | `512` | hub |
| `RelayMaxRendezvousPerDevice` | `17` | hub, `Validate` (16 phones = `devices.MaxDevices`, + 1 window) |
| `RelayDialRatePerRV` / `RelayDialBurstPerRV` | `0.2` / `3` | hub, authenticated phone dials per rv |
| `RelayDialRateAuth` / `RelayDialBurstAuth` | `100` / `200` | hub, all phone dials that passed the credential check |
| `RelayDialRateUnauth` / `RelayDialBurstUnauth` | `20` / `50` | hub, phone dials whose credential check FAILED |
| `RelayDialRatePerIP` / `RelayDialBurstPerIP` | `1` / `20` | hub, phone dials per client network (/64 IPv6, /32 IPv4); only with a client IP source (§8.3) |
| `RelayPairingTTLMax` | `10 * time.Minute` | `Validate` (`ExpiresAt`) |
| `RelayPrevBearerGraceMax` | `7 * 24 * time.Hour` | `Validate` (`PrevUntil`) |
| `RelayPrevBearerGrace` | `6 * 24 * time.Hour` | daemon: `prev_until = now + this` |
| `RelayBearerMaxAge` | `30 * 24 * time.Hour` | daemon rotation trigger |
| `RelayRecheckInterval` | `60 * time.Second` | hub revocation sweep |
| `RelayBackoffMin` / `RelayBackoffMax` | `1s` / `5 * time.Minute` | daemon control reconnect |
| `RelayBackoffResetAfter` | `60 * time.Second` | daemon: a channel up this long resets backoff |
| `RelayRateLimitedFloor` | `60 * time.Second` | daemon backoff floor after 429 |
| `RelayUnsupportedRecheck` | `6 * time.Hour` | daemon after a 404; phone pause after a 404 |
| `RelayPhoneUnauthorizedPause` | `1 * time.Hour` | phone pause after a 401 |

---

## 4. Hub store — `hub/internal/store/relay.go`

`Data` gains one field (edit in `store.go`):

```go
Relay map[string]*RelayEntry `json:"relay,omitempty"` // by rv; rv is unique hub-wide
```

and `Data.init()` (`store.go`, called by `NewData` and after loading a
snapshot) gains:

```go
if d.Relay == nil {
	d.Relay = map[string]*RelayEntry{}
}
```

```go
// RelayEntry is one rendezvous of one device. Only hashes are stored.
type RelayEntry struct {
	RV         string    `json:"rv"`
	Device     string    `json:"device"` // device id that registered it
	Org        string    `json:"org"`
	BearerHash string    `json:"bearer_hash"`
	PrevHash   string    `json:"prev_hash,omitempty"`
	PrevUntil  time.Time `json:"prev_until,omitzero"`
	ExpiresAt  time.Time `json:"expires_at,omitzero"` // non-zero = pairing window
	UpdatedAt  time.Time `json:"updated_at"`
}

// Window reports whether e is a pairing-window entry.
func (e *RelayEntry) Window() bool { return !e.ExpiresAt.IsZero() }
// Live reports whether e can authenticate a phone at now (a window: now < ExpiresAt).
func (e *RelayEntry) Live(now time.Time) bool
// Matches compares bearerHash against BearerHash and, while PrevUntil > now,
// PrevHash, each with subtle.ConstantTimeCompare. Both comparisons always run.
func (e *RelayEntry) Matches(bearerHash string, now time.Time) bool
func (d *Data) RelayEntriesOf(device string) []*RelayEntry
```

No business rules in the store (hub `AGENTS.md`). Persistence is the existing
snapshot; registrations are rare (pairing, rotation, revoke, reconnect sync),
so a full snapshot write per registration is fine. Sweep (§8.2) drops expired
windows, clears `PrevHash` after `PrevUntil`, and drops every entry of a
revoked or deleted device. Data size: 17 entries × ~250 B per device. An old
snapshot has no `relay` key and loads to an empty map.

---

## 5. Hub endpoints — `hub/internal/api/relay.go`

Routes added in `Handler()`:

```go
mux.HandleFunc(hubproto.PathRelayRendezvous, s.authed(http.MethodPost, s.handleRendezvous))
mux.HandleFunc(hubproto.PathRelayDaemon, s.authedUpgrade(s.handleRelayDaemon))
mux.HandleFunc(hubproto.PathRelayAccept, s.authedUpgrade(s.handleRelayAccept))
mux.HandleFunc(hubproto.PathRelayPhone, s.handleRelayPhone)
```

`Config.Relay *relay.Hub` (nil → the four routes answer 404 `not_found`, so a
hub built without the relay looks exactly like an old hub). `Config.ClientIP
func(*http.Request) (netip.Prefix, bool)` (nil = no client IP source; §8.3).

`authedUpgrade(h)` is `authed(http.MethodGet, …)` with one more first step:
`hubproto.CheckUpgrade(r)` → on failure 400 `bad_request`, or 403 `forbidden`
when the cause is an `Origin` header. Body is empty (`readBody` yields 0 bytes
and the signature covers `sha256("")`). Signature, revoked-device check,
per-device rate limit and nonce replay cache are unchanged. **A 401 `unauthorized`
is returned before any upgrade**, so a revoked device learns it exactly as on
any other signed call.

### 5.1 `POST /v1/relay/rendezvous`

1. `decode` → `RendezvousRequest`; `Validate(now)` → 400.
2. `store.Update`: `liveDevice`; for each entry: an existing `Relay[rv]` whose
   `Device` differs → **skip it** and append the rv to `Conflicts` (the rv is
   128 random bits and a paired phone's rv never leaves the daemon except to
   this hub, so a conflict is a bug or an attack; one bad entry must not block
   the rest — in particular the removal of a revoked phone). Delete this
   device's entries not in the set; upsert the rest with `Org = dev.Org`,
   `UpdatedAt = now`.
3. After commit: `s.cfg.Relay.Retain(device, kept)` where `kept` is the set of
   rvs this device now owns:
   - a removed **paired** entry (no `ExpiresAt`): its live splices and pending
     dials close 4001;
   - a removed **window** entry: only its pending dials fail (503 `offline` to
     the phone). A live splice that started on a window rv carries the pairing
     connection; the daemon closes that connection after the pairing outcome
     (spec 01 §6.4 step 7), so the hub never needs to cut it.
4. 200 `{"registered": n, "conflicts": [...]}`.

### 5.2 `GET /v1/relay/daemon` (control)

1. `authedUpgrade` passed. `?v=1` is required (signed); another value → 400.
2. `hubproto.AcceptWebSocket(w, r, WSConfig{PingInterval: RelayPingInterval,
   DeadAfter: RelayDeadAfter, WriteStall: RelayWriteStall})`.
3. `relay.Hub.AttachControl(dev.ID, dev.Org, ws)`: an existing link for the
   device gets `bye{replaced}` + close 4000, and its pending dials fail 503
   `offline`. Live splices are NOT closed (they do not depend on the control
   channel).
4. Send `hello{v:1, ping_s:25, max_conns:4}`. Run the link: one writer
   goroutine draining a bounded `out chan []byte` (cap 16; full → close 1008:
   a daemon that does not read its control channel is broken), each write
   under `RelayWriteStall`; one reader handling `reject`. The handler goroutine
   returns when the link ends.

### 5.3 `GET /v1/relay/phone` (phone leg)

Order matters. Every refusal before step 9 is an ordinary HTTP response with an
`ErrorResponse` body and the `Lola-Relay-Code` header, never a WebSocket.

1. `CheckUpgrade` → 400 / 403.
2. Client network bucket, only when `Config.ClientIP` yields a prefix (§8.3):
   `AllowIP(prefix)` → 429 + `Retry-After: 5`.
3. `Lola-Relay-Rv` exactly once and `ValidRelayID`; `Lola-Relay-Bearer`
   exactly once and `ValidRelayBearer`. Else → step 5's failure path.
4. `store.View`: entry `Relay[rv]`, `Live(now)`, `Matches(RelayBearerHash(b),
   now)`, device exists and not revoked. (One sha256 and one map lookup under a
   read lock: cheap enough to run for every dial.)
5. **Failure of 3 or 4**: `AllowUnauth()` (charges the unauth bucket) → empty →
   429 + `Retry-After: 5`; else 401 `unauthorized`. Unknown rv, wrong bearer,
   expired window and revoked device answer alike. Failed probes never spend
   the budget valid phones use.
6. **Success**: `AllowAuth(rv)` (the per-rv bucket, then the auth bucket; a dial
   the per-rv bucket refuses spends no shared token, and a dial the auth bucket
   refuses gets its per-rv token back, so one credential throttles only itself) →
   429 + `Retry-After` (seconds until a token, ≥ 1).
7. `relay.Hub.Reserve(device, org, rv)` → caps (rv live+pending, device
   live+pending, org, hub) and daily quotas (rv, device) → 503 `busy`.
8. No control link for the device → 503 `offline` (and `release()`). Else
   `Offer` creates a `Pending` (§6.1) and sends `incoming{conn, rv}`. Wait for
   the first of: the accept leg delivered to `ready`; `reject` (`busy`/`off` →
   503 `busy`; `unknown` → 401); control link ends (503 `offline`);
   `RelayAcceptTimeout` (504 `relay_timeout`, send `cancel{conn}`);
   `r.Context().Done()` (client gone; send `cancel`). On every exit except the
   first: `Abandon(p)` (§6.1).
9. Upgrade the phone: `AcceptWebSocket(w, r, WSConfig{PingInterval:
   RelayPingInterval, DeadAfter: RelayDeadAfter, WriteStall: RelayWriteStall,
   OnFrame: <splice accounting>})`. Failure → daemon leg `CloseNow(4004)`,
   `release()`. Then `relay.Hub.Splice` (§6.2) runs **in this handler
   goroutine** until both legs end.

The phone gets its 101 only once the daemon's leg exists, so every failure the
phone sees is an HTTP status, and a phone never holds an upgraded socket that
leads nowhere. The phone's `RelayPhoneAttemptTimeout` (8 s) exceeds
`RelayAcceptTimeout` (6 s) for this reason.

### 5.4 `GET /v1/relay/accept?conn=<id>` (daemon data leg)

1. `authedUpgrade` passed (the query, hence the conn id, is signed).
2. `ValidRelayID(conn)`; `relay.Hub.Claim(conn, dev.ID)` (§6.1) moves the
   pending entry `waiting → claimed` only if it exists, is unexpired, is
   `waiting`, and **its device equals the signing device**. Otherwise 404
   `not_found` (one answer for unknown, expired, claimed, abandoned, other
   device's).
3. `AcceptWebSocket(w, r, WSConfig{PingInterval: RelayPingInterval,
   DeadAfter: RelayDeadAfter, WriteStall: RelayWriteStall, OnFrame: …})`.
   An upgrade failure leaves the entry `claimed` with nothing in `ready`; the
   phone handler's `RelayAcceptTimeout` (504) then abandons it, exactly as for
   a daemon that never came.
4. `relay.Hub.Deliver(p, ws)`: under `Hub.mu`, if `p` is still `claimed` the
   conn goes into `p.ready` (cap 1, never blocks) and Deliver returns true;
   if `p` is `abandoned` it returns false and the handler closes the leg
   `CloseNow(4004)`. Either way this handler goroutine then returns; a
   delivered conn is owned by the phone handler.

### 5.5 Logging

Lines name the device id's first 8 hex characters, the org and the conn id's
first 8. Never the rv, the bearer, a header value, a client IP, or a byte of a
leg. One line per splice at its end: `relay: device=xxxxxxxx org=… conn=…
dur=… up=<wire bytes> down=<wire bytes> end=<close code>`.

---

## 6. Hub relay core — `hub/internal/relay/`

A package with no store and no HTTP routing: in-memory registry, splice and
limiters. Pure enough to test with `net.Pipe`.

### 6.1 Types and the pending state machine

```go
package relay

type Hub struct {
	Now  func() time.Time
	Logf func(string, ...any)

	mu       sync.Mutex
	links    map[string]*link    // device id -> control link
	pending  map[string]*Pending // conn id
	splices  map[*splice]struct{}
	perRV    map[string]int // live + pending
	perDev   map[string]int // live + pending
	perOrg   map[string]int
	total    int
	devRate  map[string]*byteBucket // per direction
	orgRate  map[string]*byteBucket
	dailyDev map[string]*dayCount   // device -> wire bytes today (UTC)
	dailyRV  map[string]*dayCount   // rv -> wire bytes today (UTC)
	dialRV, dialIP *keyedBuckets    // bounded LRU (max 10k keys each)
	dialAuth, dialUnauth *bucket
	closed   bool
}

type pendingState uint8

const (
	pendingWaiting   pendingState = iota // offered; incoming sent
	pendingClaimed                       // the device's accept request passed Claim
	pendingSpliced                       // the phone handler took the leg
	pendingAbandoned                     // the phone handler left without a leg
)

type Pending struct {
	Conn, Device, Org, RV string
	ready   chan *hubproto.WSConn // cap 1
	state   pendingState          // guarded by Hub.mu
	expires time.Time             // offer time + RelayAcceptTimeout
	release func()                // from Reserve; runs once
}

func New(now func() time.Time, logf func(string, ...any)) *Hub
func (h *Hub) AllowIP(p netip.Prefix) bool                  // per client network
func (h *Hub) AllowUnauth() bool                            // after a failed credential check
func (h *Hub) AllowAuth(rv string) bool                     // per-rv, then auth bucket
func (h *Hub) Reserve(device, org, rv string) (release func(), err error) // ErrBusy
func (h *Hub) AttachControl(device, org string, ws *hubproto.WSConn) (run func())
func (h *Hub) Offer(device, org, rv string, release func()) (*Pending, error) // ErrOffline
func (h *Hub) Claim(conn, device string) (*Pending, bool)  // waiting -> claimed
func (h *Hub) Deliver(p *Pending, ws *hubproto.WSConn) bool // claimed: into ready; abandoned: false
func (h *Hub) Take(p *Pending, ws *hubproto.WSConn) bool    // phone handler: claimed -> spliced
func (h *Hub) Abandon(p *Pending)                           // -> abandoned; drains ready (CloseNow 4004); release()
func (h *Hub) Splice(p *Pending, phone *hubproto.WSConn)
func (h *Hub) Retain(device string, kept map[string]bool)   // §5.1 step 3
func (h *Hub) Recheck(live func(device string) bool)        // closes links+splices of dead devices, 4001
func (h *Hub) Shutdown()                                    // bye{shutdown}, CloseNow 1001 on every leg
```

```
 Offer ──▶ waiting ──Claim(device ok)──▶ claimed ──Deliver: ws into ready──▶ (Take) ──▶ spliced
              │                             │
              └──────── Abandon ────────────┴──▶ abandoned   (a later Deliver returns false)
```

- `Abandon` runs on every phone-handler exit that did not `Take` a leg
  (timeout, reject, link end, client gone, phone upgrade failure). Under
  `Hub.mu`: state → `abandoned`, delete from `pending`. Then, outside the lock:
  drain `ready` non-blockingly and `CloseNow(4004)` any leg found; `release()`.
- `Deliver` before `Abandon`: the leg sits in `ready`; `Abandon` drains it.
  `Deliver` after `Abandon`: returns false; the accept handler closes it. No
  interleaving leaves a leg unowned, so a stray leg can never hold one of the
  daemon's relay slots behind hub pings.
- `release()` (from `Reserve`) runs exactly once: on splice end, or in
  `Abandon`, or on a refusal before `Offer`.

### 6.2 Splice

```go
func (h *Hub) Splice(p *Pending, phone *hubproto.WSConn) {
	daemon := <-p.ready // present: Take succeeded
	up   := shaped(phone,  p.splice, dirUp)   // Reader wrapper: sleeps while rate debt > burst
	down := shaped(daemon, p.splice, dirDown)
	go copyHalf(daemon, up)   // 32 KiB buffer from a sync.Pool
	go copyHalf(phone, down)
	// wait both, or idle/quota/revoke/shutdown signal
}
```

- `copyHalf(dst, src)`: loop `n := src.Read(buf)`; `dst.SetWriteDeadline(now
  + RelayWriteStall)`; `dst.Write(buf[:n])`. A peer that keeps sending but
  stops reading therefore stalls its writer for at most `RelayWriteStall`,
  after which the splice ends (close 1008 to the reading side). On return
  (EOF = peer sent Close, or error) call `dst.CloseWrite()` (Close 1000 toward
  the other side, or the peer's code mapped: a peer 1001 is forwarded as 1001,
  anything else as `RelayClosePeerGone` 4004).
- When the first half ends, a `RelayHalfCloseGrace` timer starts; when it fires
  or the second half ends, both legs get `CloseWithCode` (bounded, §2.7).
  Inner TLS sends `close_notify` before its Close, so the grace is enough for
  a clean end.
- **Accounting is in wire bytes**, from each leg's `OnFrame` (every frame,
  every opcode, header included): bytes add to `dailyDev[device]` and
  `dailyRV[rv]` and to the splice, device and org rate debts of that
  direction. Past `RelayDailyBytesPerRV` or `RelayDailyBytesPerDevice`: close
  both legs 4003 and `Reserve` refuses that rv / device until the UTC day
  changes. A flood of empty frames or pings is thus charged like data, and the
  codec's flood rules (§2.5) close it outright.
- Shaping: the `shaped` Reader sleeps while any debt of its direction exceeds
  its burst (bounded sleep slices of 100 ms so a close interrupts). Shaping
  slows, never closes.
- Idle: any payload byte either way (`Read` returning `n > 0`) resets
  `lastData`; a 1-minute ticker per splice closes both legs 4002 after
  `RelayIdleTimeout`. Control and empty frames do not count.

### 6.3 Memory

Per splice: 2 × 32 KiB copy buffers (pooled) + 2 × `bufio.Reader` 4 KiB +
frame scratch 16 KiB × 2 ≈ 110 KiB; 512 splices ≈ 56 MiB. The deployment
limit goes from 128 Mi to 256 Mi (§9).

---

## 7. What the hub can and cannot observe

| Can observe | Cannot observe |
|---|---|
| Which device (daemon) has a control channel, and when | Any plaintext: commands, session ids, paths, pane text, diffs, images |
| Each rendezvous id of a device: how many phones it has paired (rv is a stable pseudonym per phone) | The phone's identity key, the device name, which tiers it holds |
| When each phone connects via relay, for how long, bytes each way, frame timing | The daemon's or the phone's TLS private key; anything the inner TLS protects |
| The phone's public IP (as the ingress reports it; §8.3) | The phone's LAN addresses (they ride in the QR and inside the inner TLS) |
| Bearer hashes; bearers and pairing tokens transiently in a phone request in RAM | The pairing secret or the 6-digit code |
| Inner TLS ClientHello/ServerHello metadata (TLS 1.3 encrypts certificates) | Which daemon key is pinned (TLS 1.3 encrypts the server certificate) |

The hub must never log or store the bearer, the client IP, or any leg byte.
Traffic analysis (volume, timing, rv linkage) is an accepted risk, stated in
`hub/DESIGN.md` (§17.3).

---

## 8. Hub process changes — `hub/cmd/lola-hub/serve.go`

### 8.1 Hijack through the access log

`statusWriter` becomes:

```go
type statusWriter struct {
	http.ResponseWriter
	status int  // valid only when wrote
	wrote  bool // WriteHeader or Write was called
}

func (w *statusWriter) WriteHeader(code int)          { w.status, w.wrote = code, true; w.ResponseWriter.WriteHeader(code) }
func (w *statusWriter) Write(p []byte) (int, error)   { if !w.wrote { w.status, w.wrote = http.StatusOK, true }; return w.ResponseWriter.Write(p) }
func (w *statusWriter) Unwrap() http.ResponseWriter   { return w.ResponseWriter }
```

`Unwrap` lets `http.NewResponseController(w).Hijack()` reach the real writer.
`accessLog` builds `&statusWriter{ResponseWriter: w}` and logs, after the
handler: `sw.status` if `sw.wrote`; else `101` if the request had an `Upgrade`
header (a hijack writes through the raw conn, never through `statusWriter`);
else `200` (net/http's implicit status). The logged duration is the whole
splice. Path only, as today.

### 8.2 Timeouts, shutdown, sweep

- `http.Server` keeps `ReadHeaderTimeout 10s`, `ReadTimeout 30s`,
  `WriteTimeout 30s`, `IdleTimeout 2m` for ordinary requests. A relay handler
  either answers before 30 s (the phone waits ≤ `RelayAcceptTimeout`) or
  hijacks; `AcceptWebSocket` clears the conn deadlines, so these timeouts never
  apply to a spliced leg. After the hijack, liveness is the WebSocket ping and
  `RelayDeadAfter`; write progress is `RelayWriteStall`.
- `hs.Shutdown` does not track hijacked conns. `serve` calls
  `hs.RegisterOnShutdown(rh.Shutdown)`: every control link gets `bye{shutdown}`
  and `CloseNow(1001)`, every splice leg `CloseNow(1001)`. `CloseNow` never
  waits on a peer, so shutdown stays inside its 10 s context.
- The existing sweep ticker also calls `srv.RelaySweep()` (in `api/relay.go`):
  drop expired / revoked entries from the store, and `rh.Recheck(liveDevice)`.
  A separate ticker at `RelayRecheckInterval` runs `Recheck` alone (the sweep
  interval is configurable and may be long). Revocation through the CLI
  (another process writing the store) is therefore effective on relay within
  60 s; revocation through the API (`/v1/org/devices/revoke`, same process)
  calls `rh.Recheck` immediately after commit.

### 8.3 Client IP for the per-network bucket

Behind ingress-nginx `r.RemoteAddr` is the controller pod, and behind the
DigitalOcean load balancer (`hub/README.md`: `157.245.24.200`) without proxy
protocol nginx's `$remote_addr` — hence `X-Real-IP` — is the balancer or node
address, not the phone's. A per-IP bucket keyed on it would be one more global
bucket that a single client empties for everyone. So the bucket is **off unless
the operator names a verified source**:

New flag `--client-ip SOURCE`:

| Value | Meaning |
|---|---|
| `none` (default) | no per-network bucket; `Config.ClientIP == nil` |
| `remote-addr` | `r.RemoteAddr`'s host (`lola-hub dev`, a hub with no proxy in front) |
| `header:<Name>` | first value of `<Name>`, `netip.ParseAddr`; unparseable → no bucket for that request |

The address becomes a prefix: IPv4 → `/32`; IPv6 → `/64` (one subscriber
allocation, so rotating addresses inside it does not mint new keys; an
IPv4-mapped IPv6 address is unmapped first). Never logged.

Production runs `none` until the cluster's shared ingress controller enables
proxy protocol (`use-proxy-protocol: "true"` in the controller ConfigMap and
`service.beta.kubernetes.io/do-loadbalancer-enable-proxy-protocol: "true"` on
its LoadBalancer Service). Both live outside this repository and affect every
ingress on the cluster, so this spec does not change them; `hub/README.md`
names the step and its check (§9). Without the bucket, the unauth/auth split
(§5.3) still keeps a probe flood from denying valid phones.

### 8.4 Single replica

Control links, pending dials and splices live in process memory. A phone dial
and its daemon's control link must land on the same process. The deployment is
already `replicas: 1`, `strategy: Recreate` (the file store forces it). This
becomes a second reason, written in the deployment comment and in
`hub/AGENTS.md`: **the relay requires one hub process; scaling out needs
routing by device id and is not built**. A rollout drops every control channel
and splice; daemons reconnect with backoff (jittered, §10.3), phones re-race.

---

## 9. Deployment — `hub/deploy/k8s`, `hub/README.md`

`overlays/production/ingress.yaml` annotations added:

```yaml
    # The relay (/v1/relay/*) holds WebSocket connections open for hours.
    # The hub pings every 25 s, so these are an upper bound, not the liveness check.
    nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-send-timeout: "3600"
```

ingress-nginx forwards `Upgrade`/`Connection` by default (HTTP/1.1 upstream).
No `proxy-buffering` change is needed: an upgraded connection is not buffered.
A controller reload ends upgraded connections after its
`worker-shutdown-timeout`; that is an ordinary reconnect.

`base/deployment.yaml`: no `--client-ip` arg (default `none`, §8.3);
`resources.limits.memory: 256Mi`, `requests.memory: 64Mi`; the replica comment
gains the relay reason (§8.4). `.github/workflows/hub.yml` needs no change (its
path filter already covers `hub/**` and `hubproto/**`).

`hub/README.md` deploy notes gain: the WebSocket timeouts; the single-replica
rule; and **enabling the per-network bucket**: after the cluster's controller
runs proxy protocol, add `"--client-ip", "header:X-Real-IP"` to the deployment,
then verify from two different networks that `kubectl logs` of the controller
shows two different client addresses for `/v1/relay/phone` (the hub itself
never logs one).

---

## 10. Daemon relay client

### 10.1 `internal/hub/relay.go` (client half, no daemon state)

```go
// RelayClient speaks the relay endpoints for one identity.
type RelayClient struct {
	BaseURL string
	Key     *Key
	Dialer  hubproto.WSDialer // seam; tests inject a loopback dialer
}

func (c *Client) SyncRendezvous(ctx context.Context, entries []hubproto.RelayRendezvous) (hubproto.RendezvousResponse, error)
func (rc *RelayClient) DialControl(ctx context.Context) (*hubproto.WSConn, error)              // GET /v1/relay/daemon?v=1, signed
func (rc *RelayClient) DialAccept(ctx context.Context, conn string) (*hubproto.WSConn, error) // GET /v1/relay/accept?conn=…, signed

// Relay status file, read by doctor without the daemon.
const RelayStatusFile = "hub-relay.json" // in StateDir, 0600, written atomically
type RelayStatus struct {
	State            string    `json:"state"`
	Reason           string    `json:"reason,omitempty"`
	Since            time.Time `json:"since"`
	LastError        string    `json:"last_error,omitempty"`
	LastErrAt        time.Time `json:"last_error_at,omitzero"`
	Registered       int       `json:"registered"`                  // entries the hub holds for us, as far as we know
	RegisteredURL    string    `json:"registered_url,omitempty"`    // hub those entries live on
	RegisteredDevice string    `json:"registered_device,omitempty"` // hub device id that registered them
}
func SaveRelayStatus(dir string, st RelayStatus) error
func LoadRelayStatus(dir string) (RelayStatus, bool)
```

**Error conversion** (in `DialControl`, `DialAccept`, before returning): a
`*hubproto.WSHandshakeError` becomes `&APIError{Status: e.Status, Code:
cleanCode(e.Code)}` — `cleanCode`/`cleanText` are the closed-set and bounded
filters already in `internal/hub/client.go` — with `RetryAfter` carried on a
new `APIError.RetryAfter time.Duration` field (0 for every existing call). So
a 401 `unauthorized` on a relay dial satisfies `hubRevoked`
(`internal/daemon/hubpolicy.go`) exactly as on any other signed call, and no
hub-supplied text reaches `LastError`, `hub-relay.json` or `hubStatus`
unfiltered. `SyncRendezvous` goes through `Client.do` and gets this for free;
it additionally drops any `Conflicts` entry that is not an rv of the request.

Control channel `WSConfig{PingInterval: RelayPingInterval, DeadAfter:
RelayDeadAfter, WriteStall: RelayWriteStall}`; accept legs `WSConfig{DeadAfter:
RelayDeadAfter, WriteStall: RelayWriteStall}` (the hub pings them).

### 10.2 `internal/daemon/hubrelay.go`

```go
type hubRelay struct {
	mu        sync.Mutex
	state     string // §10.3
	reason    string
	since     time.Time
	lastErr   string
	lastErrAt time.Time
	active    int // live relayed remote conns
	reg       hub.RelayStatus // Registered* fields, persisted with the state
	kick      chan struct{} // cap 1; hubRelayNudge
	dirty     chan struct{} // cap 1; hubRelayDirty
	gen       atomic.Uint64 // bumped by hubRelayDirty
	syncMu    sync.Mutex    // serializes EVERY SyncRendezvous (§10.3)
	synced    chan struct{} // closed and replaced after each successful sync (pairing waits on it)
	syncedGen uint64        // gen of the last successful sync
	credMu    sync.Mutex    // serializes relay-record mutations (relayCred, conflicts, pairConfirm)
	incoming  *bucket       // relayIncomingPerSec / Burst
	accepting chan struct{} // cap relayAcceptConcurrency
	// The open pairing window's relay (rv, tok hash, expiry) lives on spec 01's
	// Pairer (SetWindowRelay / Window), not here: one owner for the window.
	// seams
	newClient func(url, dir string) (*hub.Client, *hub.RelayClient, hub.Identity, error)
}

const (
	relayIncomingPerSec    = 1.0
	relayIncomingBurst     = 8
	relayAcceptConcurrency = 2
	relayPairOfferWait     = 5 * time.Second
	relayHelloTimeout      = 10 * time.Second
	relayDirtyRetry        = 60 * time.Second
	relaySyncRepeatMax     = 3 // re-sends when gen moved during a sync
)

func (d *Daemon) hubRelayLoop(ctx context.Context) // started beside hubLoop, d.wg.Add(1)
func (d *Daemon) hubRelayNudge()                   // re-evaluate now (handleReload, startRemote, stopRemote)
func (d *Daemon) hubRelayDirty()                   // the rendezvous set changed (§10.6)
func (d *Daemon) hubRelayJoined(h config.HubConfig) bool // §10.3 "joined"; file I/O, call with no lock held
func (d *Daemon) hubRelayRelease(ctx context.Context)    // §10.3 cleanup POST; hubLeave calls it
func (d *Daemon) relayEntries(now time.Time) []hubproto.RelayRendezvous
func (d *Daemon) relayEligible() bool                                                   // §10.6 step 1, from facts
func (d *Daemon) relayPairOffer(ctx context.Context, t remote.Ticket) *remote.PairRelayQR // §10.6; spec 01 pairBegin
func (d *Daemon) relayMint() *remote.ConfirmRelay                                       // §10.6; spec 01 pairConfirm
func (d *Daemon) handleRelayCred(ctx context.Context, a protocol.RelayCredArgs) (protocol.RelayCredData, error)
func (d *Daemon) relayStatus() *protocol.HubRelayInfo
```

### 10.3 State machine

States (`protocol.HubRelayState*`): `off`, `syncing`, `connecting`,
`connected`, `backoff`, `unsupported`, `refused`.

**Inputs**, gathered at each evaluation **without ever holding two locks**
(the daemon's lock order is `remoteMu → d.mu`, `internal/daemon/daemon.go`):

1. Under `d.mu`, copy `cfg.Remote`, `cfg.Hub`, `cfgErr`; release.
2. Under `remoteMu`, read `d.remote != nil`; release.
3. With no lock: `joined := d.hubRelayJoined(hub)` — `hub.Enabled`, identity
   loads (`hub.LoadIdentity`), its URL equals `[hub].url`, the key on disk is
   the one that enrolled (the checks `newRealHubClient` makes); a revocation
   record means `LoadIdentity` fails (reason `revoked`).
4. `entries := d.relayEntries(now)`.

**Wanted** = `Remote.RelayOn()` ∧ `Hub.Enabled` ∧ `cfgErr == ""` ∧ `joined` ∧
`d.remote != nil`. Not wanted → reason one of `disabled`, `hub_off`,
`not_joined`, `revoked`, `remote_off`. Wanted with `len(entries) == 0` →
reason `no_phones`.

```
          ┌──────── kick / not wanted / no entries ───────────────────┐
          ▼                                                           │
 off ──wanted+entries──▶ syncing ──200──▶ connecting ──101+hello──▶ connected
  ▲                      │  │                │  │                   │
  │                404 ──┘  └─ 401     404 ──┘  └─ 401         close/│dead/bye
  │                 ▼          ▼        ▼          ▼                  ▼
  │            unsupported  refused  unsupported refused           backoff ──┐
  │            (6h or kick) (kick)                                            │
  └───────────────── other error / 429 / 5xx ──────────▶ backoff ─── delay ───┘
```

- **Into `off`** (any reason): first `hubRelayRelease` when `reg.Registered >
  0` and the reason is not `revoked` (a revoked device's entries are swept by
  the hub; it cannot sign anyway). `no_phones` is reached through a sync of
  the empty set when anything was registered. Then close a live control
  channel 1000.
- **`hubRelayRelease(ctx)`**: build a signed client from the identity in the
  state dir **for `reg.RegisteredURL`** (not `[hub].url`: on a URL change the
  entries live on the old hub); if the identity no longer loads for that URL,
  or its device id is not `reg.RegisteredDevice`, give up (log once). POST
  `{"entries":[]}` bounded by `hub.DefaultRequestTimeout`; on 200 set
  `Registered = 0`, persist. A 401 goes through `d.hubRevokedIf`. Best effort:
  a failure is logged and retried at the next evaluation (the persisted
  `Registered` survives restarts, so a crash between a config change and the
  POST still cleans up on the next start).
- `syncing`: `SyncRendezvous(relayEntries(now))` (through `syncMu`, below),
  bounded by `hub.DefaultRequestTimeout`. Sets `reg.Registered`,
  `RegisteredURL`, `RegisteredDevice`.
- `connecting`: `DialControl`, bounded by `WSHandshakeTimeout`. On 101 read
  `hello` within `relayHelloTimeout`; `v != 1` → close, `unsupported`.
- `connected`: read loop (§10.4). A `dirty` signal runs a sync on a goroutine
  (failure → stays connected, retried on the next `dirty` or after
  `relayDirtyRetry`). A `kick` re-gathers the inputs; anything not wanted, a
  different URL or identity, or no entries closes the channel 1000 and
  re-evaluates (cleanup above).
- `backoff`: delay = full jitter in `[0, min(RelayBackoffMax, RelayBackoffMin
  << fails))`, floor `RelayRateLimitedFloor` after 429 (or `Retry-After` when
  larger, capped at `RelayBackoffMax`). `fails` resets when a channel stayed
  `connected` ≥ `RelayBackoffResetAfter`.
- `unsupported`: 404 from either endpoint (old hub, §15). Re-evaluate after
  `RelayUnsupportedRecheck` or on `kick`.
- `refused`: 401 `unauthorized` on a signed relay call → `d.hubRevokedIf(ctx,
  err)` (`hub.MarkRevoked`, identity dropped, overlay dropped via
  `hubForceReload`). That reload kicks the loop, which then finds `revoked`.

**Every sync goes through `syncMu`** (the `syncing` state, a `dirty` sync and
the cleanup POST): take `syncMu`, read `g := gen`, build the set, POST; on
success record `syncedGen = g` and close/replace `synced`; if `gen != g` after
the POST, repeat (at most `relaySyncRepeatMax` times, then leave it to the next
`dirty`). An older replace-all can therefore never land after a newer one and
bring back a revoked phone's rv.

**Conflicts** in a 200 answer: for a window rv → the window loses relay
(`d.pairer.SetWindowRelay(id, "", "")`; `relayPairOffer` returns nil, §10.6) and
the entry is dropped; for a device rv
→ under `credMu`, the device's `Relay.RV` is replaced by a fresh `NewRelayID()`
(bearer kept; the registry write dirties the set), one log line (`hub relay:
rendezvous conflict for device <id>; new rendezvous id issued`). The phone learns
the new rv from its next `relayCred` (§10.6). The list is the hub's claim, so a
device rotates at most once per `relayConflictRotateEvery` (1 h); a further
conflict inside that window keeps the rv, logs once, and dirties nothing, so a
hub that names every rv on every POST cannot drive a rewrite-and-resync loop.
An rv naming neither the window nor a device changes nothing.

Every transition writes `hub-relay.json` (on change) and logs one line:
`hub relay: <state> (<reason>)`. Loop exit writes `off` with reason `stopped`.

### 10.4 Control read loop and accepting

For each message:

- `incoming{conn, rv}`:
  1. `rv` ∈ `relayEntries(now)` (paired device, or the open window) else
     `reject{unknown}`.
  2. `d.incoming` bucket else `reject{busy}`.
  3. `slot, ok := srv.ReserveRelay(rv)` (§11; whole pool and per-rv) else
     `reject{busy}`.
  4. Non-blocking send on `accepting` else release slot, `reject{busy}`.
  5. Goroutine (panic-guarded): `DialAccept(ctx, conn)` with
     `context.WithTimeout(runCtx, RelayAcceptTimeout)`; failure releases slot
     and logs (`hubRevokedIf` on 401); success →
     `srv.ServeRelayed(ws, remote.RelayMeta{RV: rv}, slot)`. The goroutine
     returns once `ServeRelayed` has registered the conn (it does not wait for
     the session).
- `cancel{conn}`: best effort: a dial in flight for `conn` is cancelled.
- `bye{revoked}`: treat as `refused` only after the next signed call confirms
  401 (a `bye` is the hub's claim; the signed call is the fact).
  `bye{replaced}`: log, `backoff` (another process with this key is running —
  `lola doctor` names it). `bye{shutdown}`: `backoff` with `fails = 0`.

### 10.5 Shutdown

The loop runs on the daemon's cancellable run context (the one passed to
`hubLoop`), panic-guarded per step (`safeRelayStep`, recover → log →
`backoff`). It **never blocks shutdown**:

- every dial uses a context derived from the run context;
- a `context.AfterFunc(ctx, func(){ ws.CloseNow(1001) })` is registered for the
  control conn and every in-flight accept dial, so a blocked `ReadMessage`
  returns; `CloseNow` never waits for the hub (§2.7);
- the cleanup POST (`hubRelayRelease`) is NOT run on shutdown: entries stay
  registered across a restart (the persisted `Registered` field is the record);
- relayed sessions are owned by `remote.Server`, whose `Close` is bounded and
  runs in `stopRemote` (§11.2).

Unlike `hubLoop`'s cycles, nothing here is shielded with
`context.WithoutCancel`: no step leaves state half-written if cut (the
rendezvous POST is idempotent replace-all).

### 10.6 Rendezvous set and credential lifecycle

The daemon stores **hashes only**. A plaintext bearer exists only in the reply
that delivers it (`PairResult.Relay` or `relayCred`), inside the inner TLS.
The window token `tok` exists in plaintext only in the QR payload; its hash
lives on spec 01's `Pairer` (`SetWindowRelay`).

`relayEntries(now)` (sorted by rv):

- each registry device whose `Relay` is set → `{rv, bearer_sha256}` plus
  `{prev_sha256, prev_until}` **only while** `PrevUntil > now`;
- the window, **only while** `rv, tokHash, exp, ok := d.pairer.Window()`
  reports `ok` (a `waiting` pairing with a relay rv) and `exp > now` → `{rv,
  bearer_sha256: tokHash, expires_at: exp}`.

No rv appears twice: a window rv is freshly minted and is never assigned to a
device (below). `TestRelayEntriesNoDuplicateRV` pins it.

**Pairing window** — `relayPairOffer(ctx, t)`, called by spec 01's `pairBegin`
handler after `Pairer.Begin` and before building the QR:

1. `d.relayEligible()` — from facts, no live channel needed: `RelayOn` ∧
   `Hub.Enabled` ∧ `cfgErr == ""` ∧ `joined` ∧ `d.remote != nil` ∧ state ∉
   {`unsupported`, `refused`}. Not eligible → nil (QR without relay).
2. `rv := NewRelayID()`; `tok, tokHash := NewRelayBearer()`;
   `d.pairer.SetWindowRelay(t.ID, rv, tokHash)` (false → nil: the pairing is
   no longer waiting); `hubRelayDirty()` and `hubRelayNudge()` (the window
   alone makes the loop wanted even with no paired phone). The window entry's
   `expires_at` is `t.ExpiresAt` (`begin + PairSecretTTL`, 5 min ≤
   `RelayPairingTTLMax`).
3. Wait up to `relayPairOfferWait` for a successful sync with `syncedGen ≥` the
   gen bumped in step 2 that did not report `rv` as a conflict.
4. Success → `&remote.PairRelayQR{URL: hub URL, RV: rv, Tok: tok}`. Timeout,
   conflict, `unsupported` or `refused` → `d.pairer.SetWindowRelay(t.ID, "",
   "")`, `hubRelayDirty()`, nil. `tok` is dropped from memory when this
   function returns; it is never logged or stored.

The control channel may still be coming up when the QR is shown; a phone that
dials before it is up gets 503 `offline` and retries (§13.3). A first phone
on a relay-only daemon therefore gets relay in its QR.

**Pairing confirm** — spec 01's `handlePairConfirm` calls `d.relayMint()` and
passes the result to `Pairer.Confirm(args, relay)`. `relayMint`: if
`!d.relayEligible()` → nil (no `PairResult.Relay`; the phone gets a credential
later through `relayCred`). Else mint `rv := NewRelayID()` and `bearer, hash :=
NewRelayBearer()` and return `&remote.ConfirmRelay{Bearer: bearer, Record:
devices.DeviceRelay{RV: rv, BearerHash: hash, RotatedAt: now}}` (no prev).
`Confirm` writes the record with the registry entry in one `Add` and answers
`PairResult.Relay = {rv, bearer}`; the registry subscriber calls
`hubRelayDirty()`, and the next sync drops the window (no longer `waiting`).
The QR rv and token die with the window: a photographed QR is useless after
pairing, and the device's rv has never been outside the inner TLS.

**Ongoing — `relayCred`** (a phone request, tier `read`, §13.5): the phone
sends it after every authenticated session's probe, and again right after it
stored a new bearer (that second call is the acknowledgement).

```go
// internal/protocol/relay.go
const CmdRelayCred = "relayCred"

type RelayCredArgs struct {
	Holds string `json:"holds,omitempty"` // hex sha256 of the bearer the phone holds; "" = none
}
type RelayCredData struct {
	Available bool   `json:"available"`        // relay is eligible on this daemon now
	URL       string `json:"url,omitempty"`    // hub base URL (phones learn a changed hub here)
	RV        string `json:"rv,omitempty"`     // this device's rendezvous id
	Bearer    string `json:"bearer,omitempty"` // set only when the phone must store a new bearer
}
```

`handleRelayCred(ctx, a)`: `peer, ok := remote.PeerFromContext(ctx)` (spec
02); `!ok` → error `"relay credentials need a paired phone connection"` (the
unix socket never gets one). `a.Holds` must be `""` or `ValidRelayHash`, else
error. Not eligible → `{Available: false}`. Else under `credMu`, with `r :=
dev.Relay`:

| Condition | Action | Reply |
|---|---|---|
| `r == nil` (paired while relay was off, or record lost) | mint rv + bearer, no prev | `{true, url, rv, bearer}` |
| `Holds == r.BearerHash`, `now - RotatedAt < RelayBearerMaxAge` | if `PrevHash != ""`: clear prev (the phone acknowledged), dirty | `{true, url, rv}` |
| `Holds == r.BearerHash`, age ≥ `RelayBearerMaxAge` | rotate: `PrevHash = BearerHash`, `PrevUntil = now + RelayPrevBearerGrace`, new bearer, `RotatedAt = now`, dirty | `{true, url, rv, bearer}` |
| `Holds == r.PrevHash` (the phone never stored the last rotation) | new bearer; prev stays the one the phone holds, `PrevUntil = now + RelayPrevBearerGrace`; dirty | `{true, url, rv, bearer}` |
| anything else (`""`, unknown) | new bearer, `PrevHash = ""`; dirty | `{true, url, rv, bearer}` |

A phone that missed a rotation recovers on its next direct or relayed session
while its prev is live, instead of waiting for the next 30-day trigger. The
handler logs one line, never a bearer or hash: `hub relay: cred device=<id>
action=<current|ack|rotated|reissued|issued> via=<lan|relay>`. The reply
travels only on the caller's connection: it is a response, not an event, so no
other connection or device can receive it.

`revokeDevice` (spec 01) deletes the registry entry and calls
`hubRelayDirty()`; the next sync removes the rv and the hub closes its splices
(4001). Spec 01's `DropDevice` already ended the inner session.

---

## 11. Handing the spliced conn to `remote.Server` — `internal/remote/relay.go`

### 11.1 API

```go
// maxRelayConnections equals hubproto.RelayMaxConnsPerDevice and
// maxRelayConnsPerRV equals hubproto.RelayMaxConnsPerRV; a test pins both.
// remote does not import hubproto, so the values are repeated, not referenced.
const (
	maxRelayConnections = 4
	maxRelayConnsPerRV  = 2
)

// ErrServerClosed is returned by ServeRelayed on a closed Server.
var ErrServerClosed = errors.New("remote: server closed")

type RelayMeta struct{ RV string }

type RelaySlot struct{ s *Server; rv string; once sync.Once }
func (sl *RelaySlot) Release()

// ReserveRelay takes one relay slot for rv. Relay slots are spec 01 §5.1's
// "pre-auth (relay)" pool, SEPARATE from the LAN pre-auth pool, so a relay flood
// cannot lock a LAN phone out, nor the reverse; the per-rv count keeps one phone
// (or one stolen bearer) from taking the whole pool. The slot is released as
// soon as the connection authenticates (it then holds an authed slot) or ends.
func (s *Server) ReserveRelay(rv string) (*RelaySlot, bool)

// ServeRelayed runs nc (a spliced relay stream) through the same TLS handshake
// and frame loop as an accepted socket. It owns nc and the slot from here on.
func (s *Server) ServeRelayed(nc net.Conn, meta RelayMeta, slot *RelaySlot) error // ErrServerClosed
```

`ServeRelayed`:

1. **Under `s.mu`, in one critical section**: if `s.closed`, unlock, close
   `nc`, release slot, return `ErrServerClosed`; else `s.wg.Add(1)`, unlock.
   (`Close` sets `closed` under `s.mu` before `wg.Wait`, so no `Add` can race
   the `Wait`.)
2. `tc := tls.Server(relayNetConn{nc, meta, s}, s.tlsConfig)` — **the same
   `*tls.Config` the listeners use** (spec 01 §4.1's field: mutual TLS 1.3,
   `ClientAuth: RequireAnyClientCert`, daemon P-256 `device.key`).
3. `go s.serveConn(tc, slot.Release)` (spec 01 §5.1: `release` is idempotent,
   frees the pre-auth slot, and is called by `conn.run` right after
   `Authenticate` succeeds — then the conn takes an authed slot — or on close;
   the accept path passes its LAN pre-auth release).
4. `relayNetConn.RemoteAddr()` returns `relayAddr{}` with `Network() ==
   "relay"`, `String() == "relay"`. `tuneKeepAlive` is a no-op on it (not
   TCP); liveness is `RelayDeadAfter`.
5. `relayNetConn` implements spec 01's `relayed` interface (`relayRV() string`
   returning `meta.RV`); `conn.authenticate` therefore sets `Handshake.Via =
   remote.ViaRelay` and `RelayRV = meta.RV`, and the accept path gets
   `ViaLAN`. Spec 01's authorizer applies §13.5 rule 4; audit lines read
   `via=relay`.
6. `relayNetConn.Close()`: if `s` is closing (`s.ctx.Err() != nil`), call
   `nc.(interface{ CloseNow(uint16) error }).CloseNow(1001)` when available
   (`*hubproto.WSConn` has it); else `nc.Close()` (bounded, §2.7).

`handshakeTimeout` (10 s), `writeTimeout` (15 s via `deadlineWriter` →
`WSConn.SetWriteDeadline`, which governs data frames only, §2.6),
`reqConcurrency` and every tier rule apply unchanged: from the TLS handshake on,
a relayed connection is a LAN connection.

### 11.2 Shutdown

`closeConns` (`server.go`) shuts every conn down **concurrently** (one
goroutine per conn, then wait): `tls.Conn.Close` sends `close_notify` under its
own 5 s write deadline, and in series four stalled relayed conns would add
20 s to `stopRemote`. With `relayNetConn.Close` using `CloseNow`, each relayed
conn ends within `max(5 s TLS alert, WSControlWriteTimeout)`.

### 11.3 Server without listeners

`[remote].bind = "off"` with relay on must still produce a `Server` (it serves
relayed conns only). `listen` changes: zero resolved addresses is allowed when
`Options.RelayOnly` is true; it builds the `Server` with no listeners and no
accept loops. `Addrs()` is empty, `Port()` is 0.

In `startRemote` (`internal/daemon/remotewire.go`):

- the gate changes from `rc.Listens()` to `rc.Serves(d.hubRelayJoined(hub))`
  (§12.1), computed after the config copy is released and before `remoteMu` is
  taken (the identity read is file I/O; no lock held); the result is stored in
  `d.remoteServes atomic.Bool`;
- `Options.RelayOnly = !rc.Listens()`;
- when `len(srv.Addrs()) == 0` the log line is `remote: relay-only server up
  (SPKI pin <pin>)` and `startAdvertiserLocked` is not called (there is no port
  to advertise);
- after the server exists (or is stopped, in `stopRemote`): `d.hubRelayNudge()`.

`handleReload` (`internal/daemon/server.go`), after `d.mu.Unlock()`:

```go
serves := nc.Remote.Serves(d.hubRelayJoined(nc.Hub))
remoteChanged := old.Remote != nc.Remote || serves != d.remoteServes.Load()
...
if remoteChanged { d.reloadRemote() }
d.hubRelayNudge()
```

Join (`handleHubJoin`), leave (`handleHubLeave`) and revocation
(`hubRevokedIf` → `hubForceReload`) all end in `handleReload`, so a relay-only
server appears on join and disappears on leave or revocation with no other
call site. See Deviation D2.

---

## 12. Config, status, doctor

### 12.1 `[remote].relay`

`internal/config/remote.go`. The resolved struct stays **pointer-free**:
`handleReload` compares `old.Remote != nc.Remote` and `remoteFile` tests
`r == (RemoteConfig{})`; a `*bool` would make every reload a change (a fresh
pointer per `Load`) and drop every phone connection.

```go
type RemoteConfig struct {
	// …
	// RelayOff keeps the daemon LAN/VPN only. Zero value = relay on (the key
	// `relay` is absent or true). Effective only with enabled = true and a
	// joined hub.
	RelayOff bool `toml:"-"`
}

func (r RemoteConfig) RelayOn() bool { return r.Enabled && !r.RelayOff }
// Serves reports whether a remote.Server should exist: a socket to bind, or a
// relay to serve. Listens keeps its meaning (a socket is bound).
func (r RemoteConfig) Serves(hubJoined bool) bool { return r.Listens() || (r.RelayOn() && hubJoined) }
```

On-disk mirror: `fileRemoteConfig.Relay *bool \`toml:"relay,omitempty"\``.
`resolveRemote`: `RelayOff = fr.Relay != nil && !*fr.Relay`. `remoteFile`
writes `Relay: ptr(!r.RelayOff)` like every other field (explicit round trip).
Not a `[defaults]` key, not inheritable, no validation beyond TOML bool. The
desktop exposes it as `SettingsDTO.RemoteRelay` (JSON `remoteRelay`, bool)
in `desktop/configsvc.go` (`= !RelayOff`, written back the same way; landed by
the build item that owns `configsvc.go`), rendered by spec 04's Phones panel. The
`off` row of the `RemoteBinds` comment gains: "Does not stop the hub relay;
`relay = false` or `enabled = false` does." `README.md` `[remote]` table and
`config.example.toml` document it.

### 12.2 `hubStatus` and the new command

`internal/protocol/hub.go`:

```go
type HubRelayInfo struct {
	State       string    `json:"state"`            // off|syncing|connecting|connected|backoff|unsupported|refused
	Reason      string    `json:"reason,omitempty"` // fixed vocabulary, §10.3
	Since       time.Time `json:"since,omitzero"`
	LastError   string    `json:"lastError,omitempty"` // cleanText-bounded
	LastErrorAt time.Time `json:"lastErrorAt,omitzero"`
	ActiveConns int       `json:"activeConns"`
	Rendezvous  int       `json:"rendezvous"`
}
// HubStatusData gains:
Relay *HubRelayInfo `json:"relay,omitempty"` // nil when [remote] is disabled
```

Filled by `hubStatusData` (`internal/daemon/hubwire.go`) from
`d.relayStatus()`. No rv, bearer, hash or conn id appears in it: `hubStatus` is
a read a phone may make. The desktop's Settings → Phones relay status reads
this field.

`internal/protocol/relay.go` (new): `CmdRelayCred`, `RelayCredArgs`,
`RelayCredData` (§10.6).

### 12.3 Doctor

`internal/doctor/relayready.go`: `RelayResult(cfg *config.Config, dir string,
daemonUp bool) (Result, bool)`; shown when `[remote].enabled`. `daemonUp` is
the `OK` of the existing `daemonResult` (unix-socket dial): the status file is
read only while a daemon answers, because a daemon that crashed leaves its last
state behind. Name `checkRemoteRelay = "phone relay"`. Never critical.

| Condition (first match) | OK | Detail | Fix |
|---|---|---|---|
| `relay = false` | yes | `off: phones connect on LAN/VPN only` | — |
| `!daemonUp` | no | `daemon not running; relay state unknown` | `FixStartDaemon` |
| hub not joined | no | `join a hub to reach phones off-network` | `FixJoinHub` |
| `refused` | no | `the hub refused this device` | `FixRejoinHub` |
| `unsupported` | no | `this hub has no relay; it needs an upgrade` | `FixRelayHubUpgrade = "relay_hub_upgrade"` (new) |
| `backoff` | no | `cannot reach the hub relay: <class>` | — |
| `off/no_phones` | yes | `no paired phone` | — |
| `connected`, `syncing`, `connecting` | yes | the state word | — |

`FixRelayHubUpgrade` is added to `AllFixIDs` (`internal/doctor/fix.go`) and to
`FIXES` in `desktop/frontend/src/lib/components/SetupChecklist.svelte` as an
informational entry (`desktop/doctorsvc_test.go` fails on a FixID without
one). The entry's exact shape follows the `FixAction` kinds that file
defines; it opens nothing and runs nothing.

---

## 13. Phone side (iOS) — the contract spec 04 implements

Spec 04 owns every Swift file (`LolaTransportCore/*`, `LolaTransportPlugin/*`):
Core `RelayWire.swift` (request builder, `RelayRefusal`, pause/backoff rules)
and `RelayCredentials.swift` (Keychain record), Plugin `RelayLeg.swift`
(`URLSessionWebSocketTask`), `LoopbackBridge.swift` and `RelayPath.swift`.
This section is normative for them; spec 04 refers here for every wire fact
and must not restate a different one.

### 13.1 Why a loopback bridge

The relay leg is TLS (hub's public certificate) carrying WebSocket carrying the
inner mutual TLS 1.3 to the daemon. Network.framework's TLS (`sec_protocol`)
runs on a connection's protocol stack; it has no API to run over an arbitrary
byte stream the app supplies, and the direct path already uses it with the
client identity and the SPKI pin.

| | Option | Inner protocol | Deps | Verdict |
|---|---|---|---|---|
| A | `NWProtocolFramer` stack `[innerTLS, WSFramer, outerTLS, TCP]` | unchanged | none | **Not chosen.** TLS above a custom framer (and the HTTP Upgrade the framer would have to perform) is not a documented configuration. |
| B | **Loopback bridge**: WS leg ⇄ one-shot `NWListener` on `127.0.0.1` ⇄ ordinary TLS `NWConnection` | unchanged | none | **Chosen.** Every API is documented and testable in the simulator; inner TLS is byte-identical to the direct path. |
| C | SwiftNIO + NIOSSL over NIO WebSocket | unchanged | several MB | Fallback if B fails on a device. |
| D | Noise-style handshake replacing inner TLS on both paths | changes | none | **Rejected.** Home-made channel crypto with no external review. |
| E | gomobile-bound Go transport | unchanged | ~10 MB | Rejected: a second build system. |

### 13.2 The relay leg and the bridge

**Credentials**: `RelayCredentials{hubURL, rv, bearer}` (spec 04's Keychain
item, account = `daemonId`, `AfterFirstUnlockThisDeviceOnly`, never synced; the
ONLY store of a bearer). During pairing the phone holds the QR's `{url, rv,
tok}` in memory only and presents `tok` as the bearer.

**Request** (pure, `RelayWire.swift`): `URLRequest(url: url +
PathRelayPhone)`, `timeoutInterval = RelayPhoneAttemptTimeout` (8 s), headers
`Lola-Relay-Rv: <rv>` and `Lola-Relay-Bearer: <bearer or window tok>`. No
`Authorization`, no `Origin`, no cookies (`httpShouldHandleCookies = false`).

**Leg** (`RelayPath.swift`): `URLSessionWebSocketTask` via
`session.webSocketTask(with: request)` with `Sec-WebSocket-Protocol:
lola-relay.v1` (`protocols: ["lola-relay.v1"]`); an ephemeral
`URLSessionConfiguration` (no cache, no cookie store); system trust for the
hub's certificate; system proxy honoured. Not `NWProtocolWebSocket`: it does
not expose the status or headers of a refused upgrade (Deviation D1). The
phone pings nothing (the hub pings; URLSession answers pongs).

**Refusal mapping** (pure, `RelayWire.swift`), from
`(task.response as? HTTPURLResponse)` status and its `Lola-Relay-Code` and
`Retry-After` headers (the body is not available):

```swift
enum RelayRefusal: Equatable {
    case credentialDead            // 401 (any code)
    case hubTooOld                 // 404 (any code, or no Lola-Relay-Code: an old hub)
    case daemonOffline             // 503 + offline
    case busy                      // 503 + busy, or 503 without a known code
    case timeout                   // 504, or RelayPhoneAttemptTimeout elapsed
    case rateLimited(TimeInterval) // 429 + Retry-After (default 5 s)
    case forbidden, badRequest, other(Int)
}
static func refusal(status: Int, code: String?, retryAfter: String?) -> RelayRefusal
```

A spliced leg that later closes (any 4xxx or 1xxx code) is an ordinary drop for
the connector (spec 04 `peer_closed`); close codes are never needed to classify
a dial.

**Bridge** (`RelayPath.swift`):

1. Open the relay leg first. Only its 101 proves the daemon dialled its accept
   leg; a refusal ends the attempt before any listener exists.
2. `NWListener(using: .tcp)` with
   `parameters.requiredLocalEndpoint = .hostPort(host: "127.0.0.1", port: .any)`,
   `parameters.acceptLocalOnly = true`, `newConnectionLimit = 1`. Wait for
   `.ready`, read `listener.port`.
3. Build the inner connection with **the function the direct path uses**,
   spec 04's `LolaTLS.parameters(pin:identity:)`: TLS 1.3, client identity,
   SPKI pin verify block. Endpoint `.hostPort("127.0.0.1", port)`,
   `requiredInterfaceType = .loopback`.
4. Accept only the inner connection's own socket: an accepted connection whose
   remote port differs from the inner connection's local port
   (`inner.currentPath?.localEndpoint`) is cancelled at once and the listener
   keeps listening; while that local port is not yet known the accepted
   connection is held unread. The listener is cancelled when the matching peer
   arrives, or the attempt fails after `LOOPBACK_MATCH_TIMEOUT_MS` (2 s, spec
   04). A local process that connects first wastes nothing.
5. Pipe, one receive in flight per direction: loopback
   `receive(minimumIncompleteLength: 1, maximumLength: 16384)` →
   `ws.send(.data(chunk))` → next receive after the send completes;
   `ws.receive` (`.data` only; `.string` → fail) → loopback `send` → next
   receive after completion. Backpressure is end to end.
6. Teardown: any side ending cancels all three. No reconnect inside the
   bridge; the connector (spec 04) owns retries.

The `ready` frame (spec 01 §4.6) on the inner connection is the
"authenticated" signal, as on the direct path; the inner `NWConnection`
reaching `.ready` is not.

Security of the bridge: the loopback hop carries ciphertext only. Another app
that connects to the listener first gets the relay stream and would have to
complete the inner TLS as the client, which needs the phone's non-exportable
key; it cannot. The real attempt fails and is re-raced: a local denial of
service that needs malware on the phone. Step 4 narrows even that.

Cost: one extra copy per chunk, ~2 ms per handshake for the listener. Relay
reconnect budget (plan §8, < 1.2 s): phone→hub WS ≈ 3 RTT, daemon accept leg ≈
3 RTT (daemon↔hub) overlapped with the phone's wait, inner TLS 1.3 ≈ 1
end-to-end RTT plus certificate verify. At 100 ms phone RTT and 50 ms daemon
RTT this is ≈ 0.6–0.8 s.

### 13.3 Racing, pauses and credential refresh (requirements on spec 04)

- Relay is a race candidate only with stored `RelayCredentials` (or, while
  pairing, the QR's `relay`). The upgrade timeout is `RelayPhoneAttemptTimeout`
  (8 s, spec 04's `RELAY_UPGRADE_TIMEOUT_MS`); the whole relay attempt
  (upgrade + inner TLS + `ready`) is spec 04's `RELAY_ATTEMPT_TIMEOUT_MS`
  (12 s).
- A 401 within `RelayFreshCredentialGrace` (60 s) of storing a new bearer is
  treated as `daemonOffline` (backoff): the daemon's sync of the new hash may
  still be in flight.
- `credentialDead` (401 otherwise): no relay attempts for that daemon for
  `RelayPhoneUnauthorizedPause` (1 h), or until a `relayCred` reply delivers a
  bearer. The next authenticated session (any path) sends `relayCred` with
  `holds` OMITTED, which the §10.6 table answers with a reissued bearer.
  `hubTooOld` (404): pause `RelayUnsupportedRecheck` (6 h), or until the `url`
  changes. Neither is permanent: a hub restored from backup or a misrouted
  ingress recovers on its own.
- `daemonOffline`, `busy`, `timeout`: exponential backoff 2 s → 60 s for relay
  attempts; direct attempts are unaffected. `rateLimited(t)`: wait `t`.
- After every authenticated session (direct or relay), the phone sends
  `relayCred {holds: hex(sha256(stored bearer)) or omitted}` natively (request
  id `lola-relaycred-<n>`; spec 04 §1.2: it never reaches JS). A reply with
  `bearer` → store `{url, rv, bearer}` atomically in the record, then send
  `relayCred` again with the new hash (the acknowledgement). A reply with
  `available: false` keeps the stored record (relay may come back). A changed
  `url` or `rv` replaces the stored one.
- On background the bridge and leg are cancelled; on foreground the racer
  starts again.

### 13.4 Swift tests (spec 04's Core test target)

`RelayWireTests`: request headers (exact names, no `Authorization`, no
`Origin`); refusal mapping for every row of §3.5 plus 401, 404 with and
without `Lola-Relay-Code`, 429 with and without `Retry-After`; pause, backoff
and fresh-credential-grace rules of §13.3 on an injected clock.

### 13.5 What spec 01 carries (settled; listed here as the relay's interface)

The inner protocol is untouched: the same mutual TLS 1.3 handshake, the same
`remote.pair` exporter-HMAC pairing, the same frames, on both paths. Spec 01
carries these fields and rules in its own sections:

1. **QR**: `PairRelayQR{URL, RV, Tok}` (spec 01 §6.2), filled from
   `d.relayPairOffer` (§10.6). `RV` is the window's single-use rv, `Tok` the
   window token minted with `NewRelayBearer`; the hub holds `sha256(tok)` as a
   window entry with `expires_at = Ticket.ExpiresAt` (`PairSecretTTL`).
2. **`PairResult.Relay`** = `protocol.PairRelay{RV, Bearer}` (JSON `rv`,
   `bearer`), from `Pairer.Confirm(args, d.relayMint())`
   (§10.6). A fresh rv, never the QR's.
3. **`devices.Device.Relay *DeviceRelay`** (`RV`, `BearerHash`, `PrevHash`,
   `PrevUntil`, `RotatedAt`; hashes only) and `Registry.SetRelay(id, r)`
   (same write-first rule as `SetTiers`). Shapes validated on load
   (`ValidRelayID`, `ValidRelayHash` repeated in `devices`; a test pins them
   equal); an invalid record is dropped with a log line, the device kept; one
   rv on two devices breaks the file. `Add` replacing an SPKI writes the relay
   it is given (nil clears it). Revocation reaches the hub through the
   registry subscriber (`devicesChanged` → `hubRelayDirty`).
4. **Handshake and authorizer.** `Handshake.Via` (`remote.ViaLAN` |
   `remote.ViaRelay`) and `Handshake.RelayRV`; `Peer.Via` is copied from it.
   `conn.authenticate` sets them from the `relayed` interface that
   `relayNetConn` implements (§11.1). In `deviceAuthorizer.Authenticate`, when
   `Via == ViaRelay`: a registered SPKI must have `Relay != nil && Relay.RV ==
   RelayRV`; an unregistered SPKI (a pairing candidate) must have `RelayRV ==
   Pairer.windowRV()` (the rv `SetWindowRelay` recorded; "" matches no
   handshake), then the ordinary `remote.pair` rules. Anything else →
   `not_paired`, logged `remote: relay rv mismatch device=<id|->`. The pairing
   connection closes after its outcome; the phone reconnects with its own rv.
5. **`PairStatusData.Via`** `json:"via,omitempty"`, set from the presenting
   connection in `confirming` and `paired`; the desktop confirm dialog and
   `lola pair` show "via relay" (§14 "Photographed QR").
6. **Tier and fields**: `relayCred` → tier `read` in `commandTiers`; `fields.go`
   entry `{fields: fArgs, args: reflect.TypeOf(protocol.RelayCredArgs{})}`. Not
   audited (read tier); its handler writes its own line (§10.6). The
   `server.go` `handle` case routes it to `d.handleRelayCred`. Spec 01's
   handled count is 175, read 24.
7. **`pairBegin`** calls `d.relayPairOffer(ctx, ticket)` after `Pairer.Begin()`
   and puts a non-nil result in `PairPayload.Relay` and `PairBeginData.Relay =
   true`; with no usable `addrs` and no relay it rejects the pairing and
   answers `Problem` (spec 01 §6.2 table).

## 14. Abuse cases

| Case | What the attacker gets | Mitigation |
|---|---|---|
| **Hub replays** recorded ciphertext of a phone leg toward the daemon | Nothing. TLS 1.3 server randoms are fresh per handshake; Go's server accepts no 0-RTT data. A replayed accept/control request fails the nonce cache (`replayCache`) and the ±5 min skew. | Inner TLS 1.3; `hubproto.Sign` covers the conn id (query); hub replay cache unchanged. |
| **Hub sends forged `incoming`** (unknown rv, floods) | Daemon dials accept legs that lead nowhere; CPU for TLS handshakes. | Unknown rv rejected without dialling; `relayIncomingPerSec` 1/s burst 8; `relayAcceptConcurrency` 2; relay pool 4, 2 per rv; `handshakeTimeout` 10 s. |
| **Hub sends hostile values** (`hello` ping/caps, error codes, long texts) | Nothing. | `hello` values are display-only (§3.4); handshake codes pass `cleanCode`, texts `cleanText` (§10.1). |
| **Hub cross-connects two tenants** (phone of A to daemon B) | Nothing. Phone A pins A's SPKI and aborts on B's certificate; daemon B finds no registry entry for A's key and fails the handshake. | Pinning both ways; §13.5 rule 4 binds rv to device. Honest hub: `Claim` requires the pending conn's device to equal the signing device. |
| **Hub MITM / impersonation** | Nothing beyond drop/delay. | Hub holds no key for the inner TLS; phone verifies the daemon pin, daemon requires a registered client key (spec 01). |
| **Stolen phone bearer** (backup, malware) | Relay legs to the daemon under that phone's rv: ciphertext only, no authentication (no phone key). Takes at most that rv's 2 relay slots and its 1 GiB/day. | Per-rv dial bucket 0.2/s burst 3; per-rv cap 2 at hub and daemon; relay pool separate from LAN; rotation every 30 days; `revokeDevice` removes the rv at the hub on the next sync. Bearer stored `ThisDeviceOnly` and never in a QR. |
| **Photographed QR** (shoulder-surfing, screen share) | **Within the pairing window (≤ 5 min, until a valid `remote.pair`)**: the photo carries the pin, the secret, the window rv and `tok`, so the relay lets the holder attempt pairing from anywhere on the internet, not only from the LAN. A valid proof moves the pairing to `confirming` with the intruder's key; the real phone then gets "another phone is pairing", and the desktop shows the intruder's name `via relay` and asks for a code only the intruder's phone displays. After the window: nothing (the window rv and token die with it; the device's rv never appears in a QR). | The human TYPES the code shown on the phone in their hand (`maxConfirmTries` 3, spec 01 §6.4); `PairStatusData.Via` shown in the confirm dialog (§13.5 rule 5); window TTL 5 min; `maxUnpairedConns` / `maxUnpairedPerSource` (spec 01). |
| **Malicious paired phone** (any tier, `read` included) | Can hold 2 of the daemon's 4 relay slots indefinitely (one request per 29 min defeats idle) and spend 1 GiB/day of relay bytes. Two such phones fill the pool or, with four, the device's 4 GiB. | Per-rv caps bound one phone to half the pool and a quarter of the quota; revocation (`revokeDevice`) ends it at once. Stated as the accepted bound. |
| **Brute force of rv/bearer** | Nothing: 128 + 256 random bits. | Uniform 401; constant-time compare; unauth bucket. |
| **Unauthenticated dial flood** (many IPs, IPv6 /64 rotation) | Failed dials get 429 sooner. Valid phones are unaffected: their dials never draw on the unauth bucket. | Unauth bucket charged only on failure; auth bucket and per-rv bucket for valid dials; per-network bucket (/64, /32) where a client IP source is configured (§8.3). |
| **Ping / empty-frame flood** on a spliced leg | A close. | `WSMaxControlFramesPerSec` (1008), `WSMaxEmptyFramesInARow` (1002); wire bytes charged to rate and quotas. |
| **Non-reading peer** (TCP window zero) | Nothing beyond its own splice. | `RelayWriteStall` per write on every hub leg; `CloseWithCode` never waits on a blocked writer (§2.7). |
| **DoS of the daemon via connection floods** through the relay | At most 4 relayed handshakes in flight per daemon, 2 per rv; the LAN pre-auth pool (spec 01 §5.1: 32, 2 per IP), the authenticated pool and the unix socket are untouched. | Hub caps (rv 2, device 4 incl. pending, org 64, hub 512); daemon caps (§10.4); `handshakeTimeout`; `RelayDeadAfter`. |
| **DoS of the hub** (idle splices, many control links) | Bounded resource use. | `RelayMaxConnsHub` 512; idle 30 min; daily quotas; memory limit 256 Mi; `Origin` refused (no browser-driven floods). |
| **A browser page opens a WS to the hub** (CSWSH) | Nothing. | `Origin` present → 403. |
| **Enrolled device registers another device's rv** | Its own entry for that rv is refused (`conflicts`); the owner's set still commits. A squatted rv of an absent owner entry makes the owner mint a new rv for that phone. | Global rv uniqueness, per-entry conflicts (§5.1); device rvs never leave the daemon except to the hub. |
| **Revoked hub device keeps relaying** | Until the next recheck. | `Recheck` every 60 s (CLI revocation), immediately for API revocation; control and accept dials need a live device. |
| **Credentials left on a hub** after the last phone is revoked, `relay = false`, a URL change or `hubLeave` | Nothing: the empty set is posted. | §10.3 cleanup POST to the hub that holds them; persisted `Registered` retries after a crash. |
| **Hub logs metadata** | Timing, volume, IP (ingress), rv linkage. | Accepted risk, documented; nothing else is logged (§5.5). |

---

## 15. Backward compatibility

| Daemon | Hub | Behaviour |
|---|---|---|
| new | old (no `/v1/relay/*`) | Old hub's mux answers `404 not_found` (its `/` catch-all) to the rendezvous POST and to the control dial. Daemon → `unsupported`, rechecks every 6 h and on reload. `relayPairOffer` returns nil, so QRs carry no relay; `relayCred` answers `available: false`. No other hub call changes. |
| old | new | An old daemon never calls `/v1/relay/*`; the store's `relay` map stays empty; `omitempty` keeps old snapshots byte-identical. A new hub reading an old snapshot finds no `relay` key (`init` makes the map). |
| new | new, built without relay (`Config.Relay == nil`) | Same as old hub: 404. |
| phone | old hub | Phone leg 404 without `Lola-Relay-Code` → `hubTooOld`, relay paused 6 h or until the hub URL changes. |
| phone (old app, `lola1.` bearer) | any | Gone with spec 01 (`lola_insecure` deleted); not a relay concern. |

`hubproto` additions are new files and new constants only; no existing frame,
path or field changes. `APIError.RetryAfter` is a new field in `internal/hub`
(zero for every existing call). The hub's `DecodeStrict` applies to the new
`RendezvousRequest`; a future field there needs the same "leave it out for an
older hub" rule as C3/C6. Control messages are decoded leniently (§3.4), so a
field added to a known `t` reaches older daemons harmlessly.

---

## 16. Deviations from `docs/mobile-rethink.md`

- **D1 — iOS relay leg uses `URLSessionWebSocketTask`, not
  `NWProtocolWebSocket`.** Plan §5 names `NWProtocolWebSocket`. It does not
  expose the status or headers of a refused upgrade, and the phone must tell
  401 (credential dead) from 404 (hub too old) from 503 (daemon offline).
  Smallest fix: the WS leg uses `URLSessionWebSocketTask`; the hub puts the
  code in `Lola-Relay-Code` (URLSession exposes headers, not the body); the
  credential rides `Lola-Relay-Bearer` (Apple reserves `Authorization` in the
  URL Loading System). Inner TLS stays on `NWConnection` via the loopback
  bridge, so direct and relay share one TLS implementation.
- **D2 — `[remote].bind = "off"` (or loopback) with relay on.** Today
  `startRemote` gates on `Listens()` and `listen` fails when nothing binds, so a
  relay-only daemon would have no `remote.Server`. Smallest fix:
  `RemoteConfig.Serves(hubJoined)` gates `startRemote`; `Options.RelayOnly`
  lets `listen` build a listener-less `Server`; `handleReload` rebuilds the
  server when `Serves` flips (join, leave, revocation). **This changes what
  `bind = "off"` means**: it stops listening on every interface but no longer
  disables phone access through the relay. `relay = false` (keep LAN settings,
  no relay) or `enabled = false` (nothing at all) does. The `RemoteBinds`
  comment, `README.md` and `config.example.toml` say so.
- **D3 — Credential refresh is a phone request, not a pushed event.** Plan §3.3
  says the bearer is "minted at pairing" and is silent on rotation. A pushed
  frame would need a spec 02 topic that no phone watches. Smallest fix: the
  phone asks with `relayCred` (tier `read`, its own record only) after each
  authenticated session; the reply carries a new bearer only when one is due,
  and the phone's next `relayCred` acknowledges it.
- **D4 — Relay connections get their own pre-auth slot pool.** Spec 01 §5.1's
  LAN pre-auth pool is taken at accept for LAN sockets. Sharing it would let a
  relay flood lock out a LAN phone. Fix: a separate pool of
  `maxRelayConnections = 4`, at most `maxRelayConnsPerRV = 2` per phone; a
  relayed conn moves to the shared authenticated pool after `Authenticate`.
- **D5 — Hub process changes the plan did not list:** `statusWriter.Unwrap`
  and `wrote` (or the hijack fails behind the access log and 101 is
  unloggable), explicit deadline clearing after hijack (or `WriteTimeout` kills
  every leg at 30 s), `RegisterOnShutdown` (or `Shutdown` leaves legs open),
  memory limit 128 Mi → 256 Mi, and a `--client-ip` flag that defaults to no
  per-network bucket, because behind the DigitalOcean balancer without proxy
  protocol every client shares one address.
- **D6 — Inner protocol unchanged.** The loopback bridge makes replacing inner
  TLS unnecessary, so spec 01 changes by fields only (§13.5), not by protocol.
- **D7 — The phone waits less than the plan's generic timeouts suggest.**
  `RelayAcceptTimeout` is 6 s and the phone's upgrade 8 s, so a relay attempt
  (upgrade + inner TLS + `ready`, 12 s) fits spec 04's 13 s race. The daemon's
  accept dial is ~3 RTT to a hub it already holds a connection to; 6 s is
  still a wide margin.
- **D8 — The QR's window token is random, not derived.** The plan's QR has
  `relay: {url, rv}`. A relayed first connection needs a hub credential; a
  random `tok` (in the QR, hash at the hub, dies with the window) keeps the
  pairing secret entirely off the hub, where a derived token would put a
  function of it there.

---

## 17. Documentation text

### 17.1 `AGENTS.md` (root)

**Replace** the Team hub bullet "Only what the protocol names leaves the
machine: …" with:

```markdown
  - Only what the protocol names leaves the machine in plaintext: repo slugs,
    validated findings, item ids, a PR URL, a delivery word, rendezvous ids and
    bearer SHA-256 hashes — never a session id, path or pane text in the clear.
    Phone traffic crosses the hub only inside the end-to-end mutual TLS session
    (see **The hub relay is blind**). `hubStatus` is a read a phone may make.
```

and **add** this block right after it:

```markdown
  - **The hub relay is blind** (`hubproto/ws.go`, `hubproto/relay.go`,
    `internal/hub/relay.go`, `internal/daemon/hubrelay.go`,
    `internal/remote/relay.go`, `docs/mobile/03-hub-relay.md`). Rules:
    - The relayed stream IS the phone's mutual TLS 1.3 session, terminated by
      `remote.Server` (`ServeRelayed` → `tls.Server(raw, s.tlsConfig)`) and
      by the phone. Never terminate, inspect or frame-parse it anywhere else;
      never add a relay-only command path.
    - The daemon dials out only: a standing control channel
      (`/v1/relay/daemon`) and one accept leg per phone connection
      (`/v1/relay/accept?conn=`), both signed with `hubproto.Sign`. The hub
      never dials a daemon, and its `hello` values never steer the daemon.
    - Credentials: the daemon keeps and registers HASHES only (replace-all
      `POST /v1/relay/rendezvous`, every POST serialized). A phone's rv and
      bearer reach it only inside the inner TLS (`PairResult.Relay`,
      `relayCred`); the QR carries a single-use window rv and a random window
      token (`tok`) whose hash expires with the window. Never log an rv, a
      bearer, a token or a hash.
      Leaving, `relay = false`, a hub URL change and revoking the last phone
      all post the empty set to the hub that holds the entries.
    - An `incoming` is the hub's claim: the daemon dials only for an rv it
      registered, under its own rate limit and relay pool (4, at most 2 per
      rv, separate from the LAN pool). A relayed peer must present the client
      key bound to that rv, or pair through the open window's rv.
    - `WSConn` write deadlines govern data frames only; control frames carry
      their own short deadline, and a close never waits on a blocked writer.
      `remote`'s per-frame write deadline would otherwise kill an idle relayed
      phone at the first hub ping.
    - A 401 `unauthorized` on a relay dial is a revocation like any signed
      call (converted to `hub.APIError`); a 404 means an older hub
      (`unsupported`, recheck 6 h), never an error to retry hard.
    - The relay loop never blocks shutdown: every dial and read is bound to the
      run context and closes with `CloseNow`.
    - `[remote].relay` (default on) only matters with `[remote].enabled`;
      `bind = "off"` stops listening but not the relay. A relay-only daemon
      runs a listener-less `remote.Server`, rebuilt when the hub is joined or
      left. `RemoteConfig` stays pointer-free (`RelayOff`): `handleReload`
      compares it by value.
```

In **Architecture map → Core**, `remote` becomes: "`remote` — TLS phone
listener and relay acceptor (one TLS config, one frame loop for both); `mdns`
advertises it; …". In **What lola is → Roles**: "`mobile/` the phone client
(via `internal/remote`, directly or through the hub relay)".

### 17.2 `hub/AGENTS.md`

In **The one rule**, after "The hub never opens a connection to a daemon, never
merges, never pushes.":

```markdown
- The relay (`internal/relay`, `/v1/relay/*`) is BLIND. It splices two
  WebSocket byte streams carrying an end-to-end mutual TLS session between a
  phone and its daemon. Never parse, buffer beyond a copy chunk, log or store a
  relayed byte. Never add a key, an endpoint or a mode that would let the hub
  terminate that TLS. The daemon dials both the control channel and every data
  leg; the hub only answers.
- Relay credentials are stored as SHA-256 only (`store.RelayEntry`), compared
  in constant time, and a phone dial answers 401 alike for an unknown rv, a
  wrong bearer, an expired window and a revoked device. A failed dial draws on
  its own bucket, never on the one valid phones use. Never log an rv, a
  bearer, a relay header or a client IP.
- Every refusal before a splice is an HTTP status with `Lola-Relay-Code`; the
  phone gets its 101 only once the daemon's leg exists. Every relay limit is a
  `hubproto` constant; do not add a hub-local one. `Origin` on an upgrade is
  refused (403): only daemons and the phone app connect.
- Every hub leg write is bounded (`RelayWriteStall`); accounting is in wire
  bytes, every opcode. A pending dial is waiting → claimed → spliced |
  abandoned under `relay.Hub.mu`; nothing else hands over a leg.
- The relay state is in process memory: the hub must run as ONE process.
  Scaling out needs routing by device id and is not built.
```

In **What the hub may see**: append "relay metadata: which devices hold a
control channel, rendezvous ids (one stable pseudonym per paired phone), when
and how long phones connect, byte counts, and the client IP the ingress
reports. It must never see relayed plaintext."

### 17.3 `hub/DESIGN.md`

- "Why it exists" item 3: "… a phone off the daemon's network has no route
  without a relay" → append "(built: see *Blind relay*)".
- "What the hub may see" table: right column row "Plaintext phone traffic
  (later: relay)" → "Plaintext phone traffic: the relay carries an end-to-end
  mutual TLS session it cannot open"; left column gains "Relay metadata:
  rendezvous ids, connection times, byte counts, client IP".
- New section after "Self-service teams":

```markdown
## Blind relay

A paired phone off the daemon's network reaches it through the hub. Contract:
lola's `docs/mobile/03-hub-relay.md`.

- **Outbound only, still.** The daemon holds a signed WebSocket control channel
  (`GET /v1/relay/daemon`). A phone dials `GET /v1/relay/phone` with a
  rendezvous id and a bearer in `Lola-Relay-*` headers; the hub checks the
  bearer's SHA-256, tells the daemon `incoming`, the daemon dials
  `GET /v1/relay/accept?conn=…` (signed), and the hub splices the two legs.
  The phone's 101 waits for the daemon's leg; every refusal is an HTTP status.
- **Blind.** Inside the legs is mutual TLS 1.3 between the phone's key and the
  daemon's P-256 identity, each pinned by the other at pairing. The hub can
  drop, delay and count; it cannot read, inject or impersonate.
- **Credentials.** The daemon registers its whole set of (rendezvous id,
  bearer hash) pairs with a signed replace-all POST; a conflicting rv is
  refused alone. Pairing-window entries expire within 10 minutes; rotation
  keeps the previous hash for at most 7 days.
- **Limits.** Per rendezvous id 2 connections and 1 GiB a day, per device 4
  and 4 GiB, per org 64, per hub 512; shaped byte rates per splice, device and
  org, counted in wire bytes; 30 minutes idle; dial buckets per rendezvous id,
  for authenticated and for failed dials, and per client network when the
  ingress reports real client addresses. All in `hubproto`.
- **One process.** Control channels and splices live in memory; the hub runs
  as a single replica.
- **Old daemons** never call it; a **new daemon facing an old hub** gets 404 and
  stays LAN-only.
```

- "Phases" item 3: "push gateway (APNs + FCM, opaque handles only) and the blind
  relay for the phone" → "push gateway (APNs + FCM, opaque handles only). The
  blind relay for the phone is built (see *Blind relay*)."
- "Non-goals": "Hub-initiated connections to daemons." stays unchanged (the
  relay keeps it).

---

## 18. Tests

### hubproto (`hubproto/ws_test.go`, `hubproto/relay_test.go`)

- `TestWSHandshakeAcceptKey` — RFC 6455 §1.3 sample key → `s3pPLMBiTxaQ9kYGzzhZRbK+xOo=`.
- `TestCheckUpgradeRefuses` — table: POST, HTTP/1.0, missing/foreign Upgrade,
  Connection without upgrade, version 8, key of 15/17 bytes, two keys, missing
  subprotocol, `Origin` present.
- `TestWSClientServerRoundTrip` — `httptest` server + `WSDialer`, 1 MiB random
  stream both ways via `io.Copy`, byte-identical.
- `TestWSMaskingRules` — server refuses an unmasked frame (1002); client
  refuses a masked frame (1002).
- `TestWSFrameRules` — RSV bits, unknown opcode, non-minimal length, 64-bit MSB,
  control frame > 125, fragmented control, text frame (1003), frame >
  `WSMaxFramePayload` (1009).
- `TestWSFragmentedStream` — FIN=0 + continuations with a ping in between read
  as one stream, no reassembly; continuation without start → 1002.
- `TestWSReadMessageRefusesFragments` — 1009.
- `TestWSWriteChunks` — a 100 KiB `Write` yields frames ≤ `WSWriteChunk`, FIN=1.
- `TestWSPingPong` — ping answered with identical payload; `DeadAfter` closes a
  silent peer; a pinging peer keeps it alive.
- `TestWSPongAfterExpiredDataDeadline` — `SetWriteDeadline(now+10ms)`, one
  `Write`, wait 50 ms, peer pings three times: three pongs arrive, the conn
  stays up, a later `Write` after `SetWriteDeadline(now+1s)` succeeds.
- `TestWSDataWriteDeadline` — a blocked data writer (peer not reading) returns
  a timeout (`net.Error.Timeout()`) at the deadline and the conn is
  `ErrWSBroken` afterwards; an already-expired deadline fails before any byte.
- `TestWSCloseWithBlockedWriter` — a writer parked on a peer with TCP window
  zero; `CloseWithCode` returns within `WSControlWriteTimeout +
  WSCloseTimeout + 100ms` and the writer returns an error.
- `TestWSCloseHandshake` — code echoed; `Read` → EOF; `CloseWrite` keeps
  reading; close bounded against a peer that never answers; `CloseNow` does
  not wait.
- `TestWSPingFlood` — 5 pings within one second → close 1008; 4 → fine.
- `TestWSEmptyFrameFlood` — 17 empty binary frames in a row → 1002; 16 then a
  data frame → fine.
- `TestWSOnFrameCountsWireBytes` — every opcode reported with header size.
- `TestWSDialNonUpgradeAnswer` — 401/404/429 JSON bodies → `WSHandshakeError`
  with code and `Retry-After`; a body-less answer with `Lola-Relay-Code` →
  that code.
- `TestWSDialForcesHTTP11ALPN` — TLS server offering h2 + http/1.1 negotiates
  http/1.1.
- `TestWSDialProxyConnect` — CONNECT through a fake proxy.
- `TestWSConnIsNetConn` — `var _ net.Conn = (*WSConn)(nil)`; `nettest.TestConn`-style
  checks hand-written (no x/net).
- `TestRendezvousValidate` — every rule of §3.3 incl. a value at `max +
  MaxSkew` (accepted), `max + MaxSkew + 1s` (refused), a past `PrevUntil` and
  `ExpiresAt` (accepted); errors never contain values.
- `TestRelayIDShapes`, `TestRelayBearerHash` — golden vectors.
- `TestRelayMsgLenient` — unknown `t` ignored; unknown field on a known `t`
  ignored; bad known field → protocol error.
- `TestRelayConstantsOrdering` — `RelayPhoneAttemptTimeout > RelayAcceptTimeout`,
  `RelayDeadAfter > 2*RelayPingInterval`, `RelayFreshCredentialGrace >= 30*time.Second`,
  `RelayPrevBearerGrace + MaxSkew <=
  RelayPrevBearerGraceMax`, `RelayMaxConnsPerRV <= RelayMaxConnsPerDevice`,
  `RelayDailyBytesPerRV <= RelayDailyBytesPerDevice`.

### hub (`hub/internal/relay/*_test.go`, `hub/internal/api/relay_test.go`, `hub/internal/store/relay_test.go`, `hub/cmd/lola-hub/serve_test.go`)

- `TestRelayEndToEnd` — httptest hub, a daemon signing via `hubproto`, a phone
  with a registered bearer: control → phone dial → incoming → accept → 1 MiB
  each way → both close; access log line has no rv/bearer and logs 101.
- `TestPhoneDialUniform401` — unknown rv, wrong bearer, expired window,
  revoked device, prev hash after `PrevUntil`, bearer in `Authorization`
  instead of `Lola-Relay-Bearer`: identical status, body and `Lola-Relay-Code`.
- `TestPhoneDialOffline503`, `TestPhoneDialTimeout504`,
  `TestPhoneDialDaemonReject` (busy → 503, unknown → 401) — each carries
  `Lola-Relay-Code`.
- `TestPhoneGetsNo101BeforeDaemon` — no upgrade until the accept leg arrives.
- `TestAcceptAfterPhoneGoneClosesLeg` — phone handler times out; the accept leg
  claimed just before gets `Deliver == false` and close 4004; claimed after →
  404; caps released once.
- `TestAbandonDrainsReadyLeg` — `Deliver` then `Abandon`: the leg is closed
  4004, nothing leaks.
- `TestAcceptOtherDeviceConn404` — device B cannot claim device A's conn.
- `TestAcceptReplayRefused` — the same signed accept request twice → replay.
- `TestControlReplaced` — second control for a device: first gets `bye`+4000.
- `TestRendezvousReplaceAll`, `TestRendezvousConflictDoesNotBlockOthers` (one
  squatted rv in `conflicts`, the removal of another rv in the same request
  commits), `TestRendezvousRemovalClosesSplice` (paired entry → 4001),
  `TestWindowRemovalKeepsSplice` (window entry removed → live splice survives,
  pending dial fails).
- `TestRecheckClosesRevoked` — CLI-style revocation in the store closes link
  and splices within one `Recheck`.
- `TestCaps` — per rv (2), per device (live+pending), per org, hub → 503 busy;
  `release` runs exactly once.
- `TestShaping` — a splice moving data at > `RelaySpliceBytesPerSec` is slowed
  (fake clock), not closed; pings and empty frames are charged.
- `TestSpliceNonReadingPeerCloses` — the daemon leg stops reading; the phone
  leg keeps sending; the splice ends within `RelayWriteStall` (shortened) and
  `Close` does not hang.
- `TestIdleClose` — 4002 after `RelayIdleTimeout` (fake clock), pings ignored.
- `TestDailyQuota` — device 4003 and rv 4003, then `Reserve` refuses until the
  UTC day changes.
- `TestHalfClose` — phone sends Close; daemon leg gets Close; both closed
  within `RelayHalfCloseGrace`.
- `TestDialBuckets` — per-rv; auth bucket; an invalid-credential flood exhausts
  the unauth bucket (429) while a valid phone is still admitted; per-network
  with `--client-ip remote-addr`: IPv6 addresses rotating inside one /64 share
  a bucket, two /64s do not; IPv4 /32; `none` → no per-network bucket.
- `TestOriginRefused403`.
- `TestServeHijackThroughAccessLog` — `statusWriter.Unwrap` works under the
  real `serve` wiring; a leg survives past `WriteTimeout` (shortened in test);
  101 is logged; a handler that writes a body only logs 200.
- `TestServeShutdownClosesLegs` — shutdown sends 1001 to every leg, bounded.
- `TestRelayDisabledIs404` — `Config.Relay == nil`.
- `TestStoreRelayEntryMatchesConstantTime` (both compares always run),
  `TestStoreOldSnapshotLoads` (no `relay` key → empty, non-nil map; an upsert
  does not panic), `TestStoreNewDataHasRelayMap`.
- `hub/e2e`: one relay case through `lola-hub dev`.

### daemon (`internal/hub/relay_test.go`, `internal/daemon/hubrelay_test.go`, `internal/remote/relay_test.go`, `internal/config/remote_test.go`, `internal/doctor/relayready_test.go`)

- `TestRelayStateMachine` — fake hub (httptest + hubproto): off reasons,
  syncing → connecting → connected; 404 → unsupported (recheck timer); 401 →
  refused + revocation file written; 429 floor; jittered backoff bounds; reset
  after 60 s.
- `TestRelayDialControl401Revokes` — 401 `unauthorized` on `DialControl` →
  `*hub.APIError` → `hubRevokedIf` writes `state/hub-revoked.json`.
- `TestRelayHandshakeErrorIsCleaned` — a hub code outside the closed set and a
  4 KiB message reach neither `hub-relay.json` nor `hubStatus`.
- `TestRelayHelloValuesIgnored` — `hello{ping_s:0, max_conns:999}` changes
  neither the ping interval nor the slot count.
- `TestRelayLoopNeverBlocksShutdown` — cancel the run context while connected,
  while dialling control, while an accept dial hangs, while a hub write is
  stalled: loop returns within `WSControlWriteTimeout + 100ms`; `d.wg` drains.
- `TestRelayIncomingRejects` — unknown rv, bucket empty, no slot, per-rv full,
  accept concurrency full → correct `reject` reason, no dial.
- `TestRelayDirtyResyncs` — `hubRelayDirty` while connected issues one POST
  with the full set.
- `TestRelaySyncSerialized` — a reconnect sync and a dirty sync overlap; the
  hub receives them in order and the last one reflects the newest set (a
  revoked rv does not come back).
- `TestRelayRevokeLastPhonePostsEmpty` — revoke the only device: exactly one
  POST `{"entries":[]}`, then `off/no_phones`.
- `TestRelayReleaseOnLeaveAndURLChange` — `hubLeave` posts the empty set
  before the identity goes; changing `[hub].url` posts it to the OLD URL;
  `relay = false` posts it; a restart with `Registered > 0` and relay off
  posts it.
- `TestRelayEntriesNoDuplicateRV` — window open + device paired through it:
  never the same rv twice; expired prev and expired window omitted.
- `TestRelayClockAhead` — daemon clock 4 min ahead of the fake hub's: a
  rotation's `prev_until` and a window's `expires_at` are accepted.
- `TestRelayPairOfferFirstPhoneRelayOnly` — `bind = "off"`, hub joined, no
  paired device: `pairBegin` yields a QR with `relay{url, rv, tok}` and the hub
  received `sha256(tok)` as a window entry; an `unsupported` hub yields one
  without and the window carries no relay (`Pairer.Window` ok=false).
- `TestRelayPairConfirmFreshRV` — `PairResult.Relay.RV` differs from the QR
  rv; the window entry is gone from the next sync; no prev hash;
  `relayMint` returns nil when not eligible.
- `TestRelayCredUnixSocketRefused`, `TestRelayCredEmptyHoldsReissues`.
- `TestRelayCredTable` — every row of the §10.6 table, incl. recovery when the
  phone holds the prev hash; the unix socket is refused; no bearer in logs.
- `TestRelayConflictReissuesRV` — a `conflicts` answer for a device rv mints a
  new rv, keeps the bearer, and `relayCred` returns the new rv.
- `TestServeRelayedUsesListenerTLSConfig` — same `*tls.Config` pointer.
- `TestServeRelayedEndToEnd` — `net.Pipe` → `tls.Client` with a registered
  client key reaches the frame loop; an unregistered key fails the handshake.
- `TestRelayedConnSurvivesIdlePings` — a relayed `remote` conn over a real
  `WSConn` pair: one frame written, then 3 hub pings over > `writeTimeout`
  (shortened): the conn stays up and the next frame arrives.
- `TestServeRelayedRacesClose` — `ServeRelayed` concurrent with `Close`
  (`-race`): either `ErrServerClosed` or a conn that `Close` waited for.
- `TestRelaySlotsSeparateFromLAN` — 4 relay conns do not reduce LAN admits;
  a third conn on one rv is refused.
- `TestServeRelayedRVBinding` — registered device presenting another device's
  rv is rejected (needs spec 01 authorizer).
- `TestRelayOnlyServerHasNoListeners` — `bind = "off"`, relay on, server
  exists, `Addrs()` empty, no advertiser started, relay-only log line.
- `TestRelayOnlyServerFollowsJoin` — join → server appears; leave → it goes;
  a relay-only daemon with LAN bind unchanged → no rebind on join.
- `TestStopRemoteBoundedWithRelayConns` — 4 stalled relayed conns: `Close`
  returns within ~5 s, not 20.
- `TestMaxRelayConnectionsMatchesHubproto` (both constants).
- `TestRemoteRelayDefaultAndServes` — absent → on; `relay = false` → off;
  `enabled=false` → off; Save round trip without growing the key on a config
  that has no `[remote]`.
- `TestReloadIdenticalRemoteDoesNotRebind` — reloading an unchanged config
  with `relay = true` does not call `reloadRemote`.
- `TestRelayDoctorRows` — every row of §12.3 incl. a stale `connected` file
  with no daemon; `AllFixIDs` contains the new id.
- `TestHubStatusRelayCarriesNoSecrets` — marshalled `hubStatus` contains no rv,
  bearer or hash.

### Swift

`RelayWireTests` (§13.4) in spec 04's Core tests. The bridge is exercised in
the simulator e2e run of workflow 5 against `lola-hub dev`; on-device behaviour
(Secure Enclave identity through the bridge, loopback exemption from the Local
Network prompt) is listed as unverified per plan §10.

---

## 19. Work items

**Order and cross-spec file ownership: `docs/mobile/00-build-plan.md` is
authoritative.** It gives every shared file to one item per wave; the hunks of
§19.1 are landed by whichever build item owns that file in that wave (item 15
below is therefore not a separate build item). The list here is this spec's
decomposition and its dependencies.

1. **WebSocket codec.** `hubproto/ws.go`, `hubproto/ws_test.go`.
2. **Relay wire.** `hubproto/relay.go`, `hubproto/relay_test.go` (paths,
   headers, ids, `RendezvousRequest/Response`, `RelayMsg`, codes, the 4xxx
   close codes, every limit constant incl. `RelayFreshCredentialGrace`).
   [none: the 1000–1011 codes live in `ws.go`]
3. **Hub store.** `hub/internal/store/relay.go`,
   `hub/internal/store/relay_test.go`, and in `hub/internal/store/store.go` the
   `Relay` field and its `init()` lines. [2]
4. **Hub relay core.** `hub/internal/relay/hub.go`, `pending.go`, `splice.go`,
   `limits.go`, `*_test.go`. [1, 2]
5. **Hub API.** `hub/internal/api/relay.go`, `hub/internal/api/relay_test.go`,
   route lines + `messages` entries + `Config.Relay` + `Config.ClientIP` in
   `hub/internal/api/api.go`, `rh.Recheck` call after commit in
   `hub/internal/api/teams.go` (revoke handler). [1–4]
6. **Hub process.** `hub/cmd/lola-hub/serve.go`, `serve_test.go`
   (`statusWriter`, `RegisterOnShutdown`, `--client-ip`, relay sweep/recheck
   tickers), `hub/e2e/e2e_test.go` relay case. [5]
7. **Hub deploy + docs.** `hub/deploy/k8s/overlays/production/ingress.yaml`,
   `hub/deploy/k8s/base/deployment.yaml`, `hub/DESIGN.md`, `hub/AGENTS.md`,
   `hub/README.md` (WebSocket timeouts, single replica, enabling the
   per-network bucket and its check). [none]
8. **Daemon relay client.** `internal/hub/relay.go`, `internal/hub/relay_test.go`;
   in `internal/hub/client.go` only the `SyncRendezvous` method and the
   `APIError.RetryAfter` field. [1, 2]
9. **Remote relay acceptor.** `internal/remote/relay.go`,
   `internal/remote/relay_test.go` (`RelayMeta`, `RelaySlot`, `ReserveRelay`,
   `ServeRelayed`, `ErrServerClosed`, `relayNetConn`, `relayAddr`). Uses the
   `server.go` hunks H1 of §19.1. [spec 01 W4c, 15(H1)]
10. **Protocol types.** `internal/protocol/hub.go` (`HubRelayInfo`,
    `HubStatusData.Relay`, `HubRelayState*` constants), `internal/protocol/relay.go`
    (new: `CmdRelayCred`, `RelayCredArgs`, `RelayCredData`). [none]
11. **Daemon relay loop.** `internal/daemon/hubrelay.go`,
    `internal/daemon/hubrelay_test.go`; `internal/daemon/hubwire.go`
    (`hubStatusData` fills `Relay` from `d.relayStatus()`);
    `internal/daemon/hubcmds.go` (`handleHubLeave` calls `d.hubRelayRelease`
    before `hub.Leave`, bounded by its own `hub.DefaultRequestTimeout`).
    [2, 8, 9, 10, 12, spec 01 W2 (`Registry.SetRelay`), spec 01 W4b
    (`Pairer.Window`, `Pairer.SetWindowRelay`, `Ticket`, `ConfirmRelay`),
    spec 02 (`remote.PeerFromContext`)]
12. **Config.** Hunk H3 in `internal/config/remote.go` and
    `internal/config/remote_test.go` cases (§19.1, applied by item 15).
    [spec 01 W6] — no exclusive file; listed so the hunk has an author.
13. **Doctor.** `internal/doctor/relayready.go`,
    `internal/doctor/relayready_test.go`; hunk H5 (§19.1) for `fix.go` and
    `SetupChecklist.svelte`. [10, spec 01 W11]
14. **Docs.** Hunk H6: root `AGENTS.md` text of §17.1, `README.md` `[remote]`
    `relay` row and the `bind = "off"` note, `config.example.toml` `relay`
    line. [all; spec 01 W12 and W6]
15. **Cross-spec hunks** (sequenced: each hunk lands only after the named
    owner's item merged; the author is the spec 03 builder, the file's owner
    reviews). Exactly the hunks of §19.1, nothing else in those files.

### 19.1 Hunks in files other items own

| Id | File (owner) | Hunk |
|---|---|---|
| H1 | `internal/remote/server.go` (spec 01 W4c) | relay pool fields (`relaySlots chan struct{}` cap 4, `relayPerRV map[string]int` under `s.mu`); `serveConn(nc net.Conn, release func())` with the accept path passing `s.release`; `Options.RelayOnly` and the zero-address `listen` path; `closeConns` concurrent (§11.2) |
| H2 | `internal/remote/auth.go`, `deviceauth.go` (spec 01 W4b) | none from spec 03: `Handshake.Via/RelayRV`, the `relayed` interface and the rv rule are spec 01's own (§13.5 rule 4) |
| H3 | `internal/config/remote.go`, `remote_test.go` (spec 01 W6) | `RelayOff`, `RelayOn`, `Serves`, `fileRemoteConfig.Relay`, resolve/mirror lines, `RemoteBinds` comment line (§12.1); tests `TestRemoteRelayDefaultAndServes` |
| H4 | `internal/daemon/remotewire.go`, `server.go`, `daemon.go` (spec 01 W5b) | `startRemote`: `Serves` gate, `remoteServes`, `RelayOnly`, relay-only log line, no advertiser without addresses, `hubRelayNudge` after start/stop (§11.3); `handleReload`: the `serves` comparison and `hubRelayNudge` (§11.3); `handle`: `case protocol.CmdRelayCred` → `d.handleRelayCred`; `daemon.go`: `hubRelay` field, `remoteServes atomic.Bool`, `d.wg.Add(1); go d.hubRelayLoop(ctx)` beside `go d.hubLoop(ctx)`; test `TestReloadIdenticalRemoteDoesNotRebind` in W5b's test file |
| H5 | `internal/doctor/fix.go` (spec 01 W11), `desktop/frontend/src/lib/components/SetupChecklist.svelte` (spec 01 W11 must own it for its own new FixID) | `FixRelayHubUpgrade` + `AllFixIDs`; the `relay_hub_upgrade` informational entry; the `RelayResult` row wiring in `doctor.go` |
| H6 | `AGENTS.md`, `README.md` (spec 01 W12), `config.example.toml` (spec 01 W6) | §17.1 text; `[remote].relay` row and `bind = "off"` note; one `relay = true` line with comment |
| H7 | `internal/daemon/pairing.go` (spec 01 W5a) | `pairBegin` calls `d.relayPairOffer(ctx, ticket)` after `Pairer.Begin()`; `pairConfirm` passes `d.relayMint()` to `Pairer.Confirm`; revocation reaches the hub through the registry subscriber (no call in `pairing.go`) |
| H8 | `internal/remote/tiers.go`, `fields.go` (spec 01 W4a) | `relayCred` → `TierRead`; its field spec (§13.5 rule 6) |
