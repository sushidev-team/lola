# 01 — Pairing, device registry, mutual TLS, capability tiers

Status: implemented on `feature/mobile-rethink` (schedule: `00-build-plan.md`); this file stays the standing contract. Written as: contract for workflow 2 (daemon + hub). Implements `docs/mobile-rethink.md`
§3.1 and §3.2. Sibling specs: `02` events (`watch`, `attachImage`, envelope v2),
`03` relay, `04` client seams. Where this spec names something another spec owns,
it says so and fixes only the interface.

Everything below was checked against the tree on 2026-11-14 (`feature/mobile-rethink`).

---

## 0. Summary of what changes

| Today (M1) | After |
|---|---|
| Listener compiled only with `-tags lola_insecure` (`internal/remote/insecure.go`, `internal/daemon/remotelisten.go`, `pairbegin.go`, `regeneratekey.go`) | No build tag. `[remote].enabled` + `[remote].bind` decide whether it listens. |
| One shared bearer key (`~/.lola/remote.key`, `LOLA_REMOTE_INSECURE_KEY`) sent in-band as `remote.hello` | Mutual TLS 1.3. Each phone has its own P-256 client certificate; its SPKI hash is looked up in `~/.lola/devices.json`. |
| Connect code `lola1.<b64url>` carrying the key | QR payload `lola2.<b64url>` carrying a one-time pairing secret. The phone shows a 6-digit code; the human TYPES it on the desktop (or `lola pair`) to confirm. |
| Revocation = `regenerateRemoteKey` (cuts every phone) | `revokeDevice <id>` (cuts one, closes its live connections, drops its relay rendezvous). |
| A client learns it is refused only by the socket closing | A paired connection gets one `ready` frame before anything else; a post-handshake refusal gets an `err` frame `not_paired` (§4.6). |
| One connection pool, slot taken at accept | Pre-auth pool (global + per source IP) and a separate authenticated pool (§5.1). |
| `internal/remote/policy.go` is a DENYLIST; anything else reaches `d.handle` | `internal/remote/tiers.go` is a closed ALLOWLIST: every command maps to exactly one of `read`, `answer`, `control`, `destructive`, `shell`, `never`. |
| `normalizeRequest` clears `Force` only | Per-command request REBUILD from a field allowlist (`internal/remote/fields.go`). |
| A denied command closes the connection | A refusal answers on the frame id and the connection survives (strike limit). |
| Conversation commands live in `protocol.SSHHumanCommands` | They move to `protocol.TalkCommands`. |
| Phones get no Conversation, no review packages, no SSH story | Tiers grant them; SSH stays readable only through the new `sessionTimeline`. |

---

## 1. Deletions

Delete, in one change, with no compatibility shim (M1 clients are gone with the key; the
plan states the daemon may refuse them):

| Path | Action |
|---|---|
| `internal/remote/insecure.go`, `insecure_off.go`, `insecure_test.go`, `insecure_off_test.go` | delete. `insecure.go:104` defines `remote.Listen` today; after the deletion the single untagged `Listen` lives in `internal/remote/server.go` (W4c). |
| `remote.CommandDenied` (in `policy.go`) | delete. Its daemon-test call sites are rewritten to `remote.TierFor(cmd)` assertions: `talk_contract_test.go` by W5d, `pairbegin_test.go` is deleted by W5a, the other five files by W5e (§13, with the rewrite table). |
| `internal/remote/connectcode.go`, `connectcode_test.go` (`lola1.`, `ConnectCode`, `EncodeConnectCode`, `DecodeConnectCode`) | delete |
| `internal/remote/policy.go`, `policy_test.go` | replaced by `tiers.go`, `fields.go` (+ tests) |
| `internal/daemon/pairbegin.go`, `pairbegin_off.go`, `pairbegin_test.go`, `pairbegin_off_test.go` | replaced by `internal/daemon/pairing.go` (+ test) |
| `internal/daemon/regeneratekey.go`, `regeneratekey_off.go` | delete; `case "regenerateRemoteKey"` removed from `server.go` |
| `internal/daemon/remotelisten_off.go`, `remotewire_off_test.go` | delete |
| `internal/daemon/remotelisten.go` | drop the `//go:build lola_insecure` line; body unchanged |
| `//go:build lola_insecure` on `internal/daemon/remotewire_integration_test.go`, `mdnswire_test.go` | drop the tag; rewrite their setup to pair a test client cert instead of `t.Setenv(remote.InsecureKeyEnv, …)` |
| Symbols: `remote.InsecureKeyEnv`, `InsecureLANEnv`, `insecureKeyFile`, `helloCmd` (`remote.hello`), `insecureHello`, `newAuthorizer` (both halves), `RegenerateInsecureKey`, `insecureAuthorizer`, `(*Server).InsecureKey`, `Options.InsecureLAN`, `Peer.Insecure`, `ErrNoAuthorizer` | delete |
| `config.RemoteConfig.InsecureLAN` / `fileRemoteConfig.InsecureLAN` (`insecure_lan`) | delete. BurntSushi decoding ignores unknown keys (no `Undecoded` check in `internal/config`), so an old file still loads; the next `Save` drops the key. Pinned by a test. |
| `protocol.PairBeginData` fields `Code`, `Key`, `Insecure` and their doc comment in `internal/protocol/protocol.go` (lines ~158–190, ~1450+) | replaced (§6.1) |
| `desktop/configsvc.go`: `SettingsDTO.RemoteInsecureLAN`, `PairBegin`, `RegenerateRemoteKey` and the connect-details DTO (~1379–1450) | delete; moved to `desktop/phonesvc.go` (§9.3) |
| `internal/tui/settingsform.go`: the `insecure_lan` row | delete |
| `contrib/lola-mobile-dev.sh` | delete |
| `Makefile`: targets `mobile-dev`, `mobile-lan`, `mobile-info` and their comment block; `.PHONY` entries | delete |
| `Makefile`: `daemon`, `daemon-dev` | rewrite (§10) |
| `.github/workflows/ci.yml`: step `build/vet/test (lola_insecure)` (lines 82–93) | delete |
| `mobile/package.json` scripts `daemon`, `daemon:info` | delete (the client spec 04 owns the rest of `mobile/`) |
| `config.example.toml` `insecure_lan` block and every "milestone 1 / lola_insecure" sentence in the `[remote]` comment | rewrite to the §5 truth |
| `~/.lola/remote.key` on users' machines | left on disk, never read. `lola doctor` does not mention it. |

Client-side M1 code (`mobile/plugins/lola-transport/.../HelloHandshake.swift`, `LolaDevLink.swift`,
`mobile/src/lib/pairpayload.ts`, `secretstore.ts`, `testdata/connectcode.json`, `scripts/dev-link.sh`,
`Connect.svelte`, `diagnose.ts`, `wire/protocol.ts` codes) is owned by spec 04. It stops working
the moment this lands; that is accepted by the plan.

---

## 2. Packages and ownership

```
internal/protocol   wire: command names, tiers, pairing + device types, TalkCommands, codes
internal/devices    NEW stdlib leaf: the registry file (~/.lola/devices.json)
internal/remote     TLS listener: mTLS config, device authorizer, Pairer, tiers, field rebuild, rate limits
internal/qr         NEW stdlib leaf: byte-mode QR encoder + terminal renderer (port of desktop/frontend/src/lib/qr.ts)
internal/daemon     socket handlers (pairing.go, sessiontimeline.go), wiring (remotewire.go)
main package        paircmd.go: `lola pair`, `lola devices`
desktop             phonesvc.go: PhonesService (local socket client)
```

Import rules: `devices` imports only stdlib + `internal/protocol` (for tier names).
`remote` imports `devices`, `protocol`, `panebus`-free types as today. `qr` imports nothing.
Nothing imports `internal/daemon`.

---

## 3. Device registry — `internal/devices`

### 3.1 File

Path: `filepath.Join(config.Home(), "devices.json")` (`$LOLA_HOME` honoured; the daemon passes
`d.home`). Mode 0600, directory 0700 (create if absent; never re-permission an existing dir,
log if group/world accessible — same rule as `remote.ensureDir`). A second file,
`devices.revoked` (0600, same dir), is the append-only revocation journal (§3.2).

```json
{
  "version": 1,
  "epoch": 7,
  "devices": [
    {
      "id": "dd3f9a1c07be41d2a",
      "name": "Martin's iPhone",
      "platform": "ios",
      "spki": "q1w2…44 chars std base64…=",
      "tiers": ["read", "answer", "control", "destructive", "shell"],
      "epoch": 7,
      "pairedAt": "2026-11-14T10:02:11Z",
      "lastSeenAt": "2026-11-14T18:40:00Z",
      "relay": {
        "rv": "6f1c0e55b20d4b8a9a0e11f3c7d24b8a",
        "bearer_sha256": "9a0e…64 hex…",
        "rotated_at": "2026-11-14T10:02:11Z"
      }
    }
  ]
}
```

```go
package devices

const (
    FileName             = "devices.json"
    RevokedFileName      = "devices.revoked"
    FileVersion          = 1
    MaxDevices           = 16
    MaxNameRunes         = 64
    lastSeenPersistEvery = 10 * time.Minute
)

type Device struct {
    ID         string       `json:"id"`       // "d" + hex(SPKIHash[:8]) = 17 chars — deterministic per key
    Name       string       `json:"name"`     // display only; never used for a decision
    Platform   string       `json:"platform,omitempty"` // "ios"; display only
    SPKI       string       `json:"spki"`     // std base64 of SHA-256(SubjectPublicKeyInfo)
    Tiers      []string     `json:"tiers"`    // subset of protocol.Tiers, always contains "read"
    Epoch      uint64       `json:"epoch"`    // file epoch at the moment this entry was written by Add
    PairedAt   time.Time    `json:"pairedAt"`
    LastSeenAt time.Time    `json:"lastSeenAt,omitzero"`
    Relay      *DeviceRelay `json:"relay,omitempty"` // spec 03 §13.5; nil = this device has no relay rendezvous
}

// DeviceRelay is the device's hub rendezvous. HASHES ONLY: the plaintext bearer exists on the
// phone and, once, in the reply that delivers it (PairResult.Relay or a relayCred reply, spec 03 §10.6).
type DeviceRelay struct {
    RV         string    `json:"rv"`                    // ^[0-9a-f]{32}$
    BearerHash string    `json:"bearer_sha256"`         // ^[0-9a-f]{64}$ = hex(sha256(bearer ASCII))
    PrevHash   string    `json:"prev_sha256,omitempty"` // previous bearer during rotation
    PrevUntil  time.Time `json:"prev_until,omitzero"`   // required iff PrevHash != ""
    RotatedAt  time.Time `json:"rotated_at"`
}

func (d Device) Has(tier string) bool

var (
    ErrBroken        = errors.New("devices: registry file is unreadable") // wraps the parse error
    ErrUnknownDevice = errors.New("devices: no such device")
    ErrFull          = errors.New("devices: device limit reached")
    ErrBadTiers      = errors.New("devices: tiers must be known and include read")
    ErrBadName       = errors.New("devices: name is empty, too long or not printable")
    ErrBadRelay      = errors.New("devices: relay record is malformed")
    ErrRevokeUnsaved = errors.New("devices: revoked for this run only: neither devices.json nor devices.revoked could be written")
)

type Registry struct { /* mu sync.RWMutex; path string; st file; denied map[[32]byte]uint64;
                          broken error; unsaved bool; subs []func(); now func() time.Time; logf */ }

func Open(home string, now func() time.Time, logf func(string, ...any)) *Registry // never nil
func (r *Registry) Err() error                              // non-nil ⇔ broken (wraps ErrBroken)
func (r *Registry) Unsaved() error                          // non-nil ⇔ a revocation lives only in memory (§3.2)
func (r *Registry) Lookup(spki [32]byte) (Device, bool)     // false when broken or journal-revoked
func (r *Registry) Get(id string) (Device, bool)            // false when broken or journal-revoked
func (r *Registry) LookupRV(rv string) (Device, bool)       // device whose Relay.RV == rv
func (r *Registry) List() []Device                          // copy, sorted by PairedAt
func (r *Registry) Add(name, platform string, spki [32]byte, tiers []string, relay *DeviceRelay) (Device, error)
func (r *Registry) Revoke(id string) (Device, error)
func (r *Registry) SetTiers(id string, tiers []string) (Device, error)
func (r *Registry) SetRelay(id string, relay *DeviceRelay) (Device, error) // spec 03: relayCred, rv conflicts
func (r *Registry) Touch(id string)                         // LastSeenAt=now; persists at most every lastSeenPersistEvery
func (r *Registry) Subscribe(fn func())                     // called after every Add/Revoke/SetTiers/SetRelay, outside r.mu
func HashSPKI(rawSPKI []byte) [32]byte                      // sha256
func IDFor(h [32]byte) string                               // "d"+hex(h[:8])
```

The daemon subscribes `d.devicesChanged` (§5), which calls spec 03's `d.hubRelayDirty()` so the
hub's rendezvous set follows every pairing, revocation and rotation.

### 3.2 Rules

- **Missing file** → empty registry, not broken. The file is created on the first `Add`.
- **Fail closed, never rewrite.** Unparseable JSON, `version != 1`, a duplicate `id`/`spki`,
  or an `spki` that is not 32 bytes of base64 → `Open` returns a registry with `Err() != nil`.
  A broken registry authenticates nobody (`Lookup`/`Get` → false), and every mutator returns
  `ErrBroken` without touching the file. Recovery is a human moving the file away (named in
  the error). Same posture as `claudetrust`.
- **Unknown tier names** inside a valid file are dropped with one log line (fewer rights, never
  more). `read` missing after that → entry kept with `["read"]` and logged.
- **Relay records.** A `relay` object that fails its shape rules (`rv` 32 lowercase hex,
  hashes 64 lowercase hex, `prev_sha256 != bearer_sha256`, `prev_until` set iff `prev_sha256`)
  is DROPPED from that entry with one log line (fewer rights: that phone loses the relay path,
  keeps direct). The same `rv` on two devices is ambiguous binding → the whole file is broken
  (fail closed). `devices` repeats the regexes (it does not import `hubproto`); a test pins them
  equal to `hubproto.ValidRelayID`/`ValidRelayHash`. `Add`/`SetRelay` return `ErrBadRelay` for
  a malformed record or an `rv` another device holds.
- **Atomic save:** temp file in the same dir, `Chmod(0600)`, write, `Sync`, `Close`, `Rename`
  (the `remote.writeSecret` shape; copy the ~25 lines, `devices` is a leaf). Write first, swap
  memory second: a failed write leaves memory unchanged and returns the error — **except
  `Revoke`** (next bullet).
- **Revocation survives restarts even when `devices.json` cannot be written.** `Revoke`:
  1. tries the atomic save without the entry; success → done (and the journal is truncated,
     because `devices.json` is now authoritative);
  2. on failure, appends one line `<spki std base64> <entry epoch>\n` to `devices.revoked`
     (`O_WRONLY|O_APPEND|O_CREATE`, 0600, `Sync`). `Open` reads the journal and treats every
     registry entry whose SPKI is listed with `Epoch ≤` the journal's epoch as absent (a later
     re-pair has a higher epoch and is not hidden). A journal line that does not parse makes the
     registry broken (fail closed). Returns `fmt.Errorf("revoked; %s could not be rewritten, the revocation is kept in %s: %w")`;
  3. if the journal append fails too, the entry is removed from memory anyway, `Unsaved()`
     becomes non-nil, and `Revoke` returns `ErrRevokeUnsaved` ("revoked for this run only: neither
     devices.json nor devices.revoked could be written"). While `Unsaved() != nil` the daemon
     keeps the phone listener and the relay OFF (`startRemote` refuses with that error as
     `remoteErr`; a running listener is stopped by the handler), and `lola doctor`'s `phones`
     row fails. Every later mutation and `Touch` persist retry the save; the first success
     clears it and the listener comes back on the next `reconcileRemoteBind`.
  In all three cases memory drops the device at once: a revoked phone loses access now.
- **Epoch.** File-level `epoch` increments on every `Add`; the new entry takes the new value.
  `Add` of an SPKI already present REPLACES the entry (same `id`, new name/tiers/epoch/pairedAt).
  `SetTiers` and `Revoke` do not change any epoch. A connection remembers the epoch it
  authenticated under (§4.4); a mismatch on any later frame ends it.
- `Add` validates: `name` trimmed, 1..`MaxNameRunes` runes, no control/format runes
  (`unicode.IsControl`, `unicode.Is(unicode.Cf)`) → `ErrBadName`; tiers ⊆ `protocol.Tiers`,
  contains `read`, deduplicated and stored in `protocol.Tiers` order → `ErrBadTiers`;
  `len(devices) == MaxDevices` and SPKI not present → `ErrFull`.
- The daemon is the only writer. Hand edits while it runs are overwritten on the next save.
- Concurrency: one `sync.RWMutex`. `Lookup`/`Get` are read-locked and allocation-free enough
  to run on every frame.

---

## 4. Mutual TLS and authentication — `internal/remote`

### 4.1 TLS config (`identity.go`)

`(*DeviceKey).TLSConfig()` gains a parameter and becomes:

```go
func (dk *DeviceKey) TLSConfig(verify func(tls.ConnectionState) error) *tls.Config {
    return &tls.Config{
        Certificates:           []tls.Certificate{dk.Certificate},
        MinVersion:             tls.VersionTLS13,
        SessionTicketsDisabled: true,
        ClientAuth:             tls.RequireAnyClientCert,
        VerifyConnection:       verify,
    }
}
```

`Server` builds it once in `listen` with `verify = s.verifyPeer` and keeps it in the FIELD
`s.tlsConfig *tls.Config` (spec 03's `ServeRelayed` wraps a raw conn with
`tls.Server(raw, s.tlsConfig)`; one config, one code path).

`verifyPeer(cs tls.ConnectionState) error`, run inside the handshake. It holds no
per-connection state (`VerifyConnection` has nowhere to keep any), so it takes no slot:

1. `len(cs.PeerCertificates) != 1` → `errPeerCert`. (One self-signed leaf; no chains.)
2. Leaf public key must be `*ecdsa.PublicKey` on `elliptic.P256()` → else `errPeerKey`.
   (Secure Enclave keys are P-256; refusing other algorithms keeps the parse surface small.)
3. `h := devices.HashSPKI(leaf.RawSubjectPublicKeyInfo)`.
4. `reg.Lookup(h)` ok → accept.
5. else `pairer.windowOpen()` → accept as a pairing candidate. (The unpaired slot is taken
   later, in `Authenticate`, which owns the connection — §4.3.)
6. else → `errUnpaired`. Go answers the client with a `bad_certificate` alert; no application
   byte, no JSON parser, no envelope decoder runs. The log line
   `remote: refused unpaired certificate from <addr> (fingerprint <fp>)` is throttled by ONE
   global token bucket (`refusalLogRate` 1/s, burst 10, §7); suppressed lines are counted and
   the count is logged with the next line that passes. No per-address map exists, so cycling
   source addresses costs no memory.

Validity dates and EKU of the client certificate are NOT checked (self-signed, pinned by SPKI).
The private-key proof is TLS 1.3's `CertificateVerify`, which Go always checks.

**TLS 1.3 ordering.** The client sends its Finished before the server checks the client
certificate, so `NWConnection` reaches `.ready` even for a refused certificate; the
`bad_certificate` alert surfaces on the client's first receive. A client therefore never treats
a finished TLS handshake as authenticated: only the `ready` frame (§4.6) is.

### 4.2 `Peer` (`auth.go`)

```go
const (
    ViaLAN   = "lan"   // a socket accepted by a listener (LAN, VPN, loopback)
    ViaRelay = "relay" // a spliced hub stream handed in by spec 03's ServeRelayed
)

type Peer struct {
    DeviceID    string    // registry id
    Label       string    // device name at authentication time; log only
    Epoch       uint64    // Device.Epoch at authentication
    Via         string    // ViaLAN | ViaRelay — copied from Handshake.Via
    RemoteAddr  string
    ConnectedAt time.Time
}

type Handshake struct {
    RemoteAddr      string
    TLS             *tls.ConnectionState
    At              time.Time
    Via             string // ViaLAN | ViaRelay
    RelayRV         string // the rendezvous id the hub matched; "" unless Via == ViaRelay
    NextFrame       func() (*protocol.Frame, error)
    Send            func(*protocol.Frame) error
    SetReadDeadline func(time.Time) error // pairing manages its own deadlines (§6.4)
}

// relayed is implemented by spec 03's relayNetConn. conn.authenticate sets
// hs.Via = ViaRelay and hs.RelayRV = rc.relayRV() when tc.NetConn() implements it,
// else hs.Via = ViaLAN.
type relayed interface{ relayRV() string }
```

`Insecure` and `Path` are gone. `ErrUnauthenticated`, `ErrDenied` stay; new sentinel
`ErrPairingDone` (a pairing connection ended after its outcome frame; logged at info, not as a
rejection). The `Authorizer` interface keeps its two methods; its only production implementation
is `deviceAuthorizer` (`deviceauth.go`). `conn.authenticate` fills `SetReadDeadline` with
`c.nc.SetReadDeadline` and keeps its own 10 s deadline + clearing defer for every other path.

### 4.3 `deviceAuthorizer.Authenticate`

```
hs.TLS == nil                          → ErrUnauthenticated (in-memory pipes never authenticate)
h := HashSPKI(leaf)
dev, ok := reg.Lookup(h)
ok && hs.Via == ViaRelay && (dev.Relay == nil || dev.Relay.RV != hs.RelayRV)
                                       → notPaired("relay rv mismatch")
ok                                     → reg.Touch(dev.ID); Peer{DeviceID, Label: dev.Name, Epoch: dev.Epoch, Via}
!ok && pairer.pending()                → candidate (pending = waiting or confirming; present
                                         refuses anything but waiting, §6.4 step 2):
    hs.Via == ViaRelay && hs.RelayRV != pairer.windowRV()  → notPaired("relay rv mismatch")
    return pairer.present(ctx, hs, h, sourceKey(hs))
        // §6.4; always ends in ErrPairingDone or an error. present waits
        // pairFirstFrameTimeout for the remote.pair, and only then takes
        // pairer.admitUnpaired(source) (!ok: err not_paired, close), so a
        // silent connection never holds a candidate slot.
otherwise                              → notPaired("not paired")
```

`sourceKey(hs)` = for `ViaLAN` the IPv4 address of `hs.RemoteAddr`, or its IPv6 /64 (a host
picks addresses inside its /64 at will); `"relay:"+hs.RelayRV` for `ViaRelay`.
`notPaired(reason)` sends the best-effort `err` frame of §4.6, logs, through a throttled
bucket (any peer can cause it while a window is open),
`remote: rejected <addr> via=<via>: <reason>` (relay mismatches log
`remote: relay rv mismatch device=<id|->`, spec 03 §13.5 rule 4), and returns
`ErrUnauthenticated`.

The lookup is repeated here even though `verifyPeer` did it: a revoke or window expiry may land
between handshake and the first frame. The relay rule binds a rendezvous to ONE device: a stolen
bearer of phone A cannot carry phone B's session, and a relayed candidate must use the open
window's rv.

On success `Authenticate` returns, `conn` moves the connection from its pre-auth slot to an
authenticated slot (§5.1), enforces `maxConnsPerDevice` (§7), sends the `ready` frame (§4.6),
and only then starts the frame loop.

### 4.4 `deviceAuthorizer.AuthorizeFrame` — every inbound frame

```
dev, ok := reg.Get(peer.DeviceID)
!ok || dev.Epoch != peer.Epoch         → ErrUnauthenticated → conn closes (revoked / re-paired)
tier := frameTier(f)                    // §8.1; "never" handled before this call by conn.authorize
!dev.Has(tier)                          → ErrDenied → CodeDenied, conn survives
```

Tiers are read live from the registry, so `setDeviceTiers` applies on the device's next frame.
`AuthorizeFrame` has NO side effects (no strike, no bucket, no log): spec 02's pump calls it
before every pushed event (spec 02 §10.3). Strikes and rate limits stay in `conn.authorize`.
Pane OUTPUT frames are not re-checked per frame: `revokeDevice` calls `DropDevice`
synchronously after `Registry.Revoke`, and `setDeviceTiers` does the same, so a revoked or
downgraded device's attached panes end with its connections.

### 4.5 Active closing

```go
func (s *Server) DropDevice(id string) int   // shutdown() every conn whose peer.DeviceID == id; returns count
func (s *Server) DeviceConns(id string) int  // live conns for that id (for `devices`)
```

`revokeDevice` calls `DropDevice` after `Registry.Revoke`, and `setDeviceTiers` after
`Registry.SetTiers` (§4.6); the per-frame check (4.4) covers
the race in between and connections on a listener being rebuilt. `Revoke` notifies the
registry's subscribers, so `d.devicesChanged` → spec 03's `hubRelayDirty()` re-syncs the hub's
replace-all rendezvous set without that device's `rv`; the hub then closes that rv's splices
and refuses its bearer. A failed sync is retried by spec 03's loop (next `dirty`, or 60 s while
connected). Until it lands, a revoked phone can still make the hub send `incoming` for its old
`rv`; spec 03 §10.4 step 1 answers `reject{unknown}` without dialling, because `relayEntries`
is built from the registry, which no longer holds the device.

### 4.6 Readiness and post-handshake refusal

TLS 1.3 cannot tell a client it was accepted (§4.1). Two frames make the outcome explicit:

```go
// internal/protocol/frame.go
const FrameReady = "ready" // daemon -> client, exactly once, first frame of a paired connection

type ReadyData struct {
    DeviceID string   `json:"deviceId"`
    Name     string   `json:"name"`
    Tiers    []string `json:"tiers"`  // what this device may do now; the client hides the rest
    Via      string   `json:"via"`    // "lan" | "relay"
    Host     string   `json:"host"`   // os.Hostname() of the daemon, display only
}
// {"v":2,"type":"ready","payload":{"deviceId":"dd3f…","name":"…","tiers":[…],"via":"lan","host":"…"}}

const CodeNotPaired = "not_paired" // err frame, then close
```

- After `Authenticate` succeeds, the daemon writes exactly one `ready` frame before reading any
  frame. A client counts a path as authenticated ONLY when `ready` arrives; the racing connector
  (plan §5, spec 04) cancels the losing path on the first `ready`, never on TLS `.ready`.
- Every `Authenticate` refusal after a completed TLS handshake (revoked between handshake and
  auth, epoch change, broken registry, relay rv mismatch, pairing limits) writes a best-effort
  `{"v":2,"type":"err","payload":{"code":"not_paired","message":"this device is not paired"}}`
  (`ErrPayload`) and closes. The
  client maps `not_paired` and a `bad_certificate` alert to the same "not paired" state, and
  anything else (EOF, timeout, no `ready` within `handshakeTimeout`) to a network failure.
- `ready` is sent once per connection. Tier changes apply per frame (§4.4), and
  `setDeviceTiers` also closes every live connection of that device (`d.dropRemoteDevice`),
  so the phone reconnects and its next `ready` carries the new tiers. No pushed device topic
  exists.

---

## 5. Listener lifecycle (untagged)

`remote.Listen(ctx, opts)` is the single, untagged entry point:

```go
type Options struct {
    Bind, Dir string
    Port      int
    Handle    func(context.Context, protocol.Request) protocol.Response
    Panes     PaneBus
    Devices   *devices.Registry   // required; owned by the daemon, survives listener rebuilds
    Pairer    *Pairer             // required; owned by the daemon, survives listener rebuilds
    Host      string              // os.Hostname(), for ReadyData/PairResult
    Now       func() time.Time
    Logf      func(string, ...any)
    HandlerGrace time.Duration
    // removed: InsecureLAN. Spec 02 adds Events; spec 03 adds RelayOnly.
}
```

`Listen` and `Options` live in `internal/remote/server.go` (they move there from the deleted
`insecure.go`).

- `Devices == nil || Pairer == nil` → error `remote: Options.Devices and Options.Pairer are required`.
- `opts.Devices.Unsaved() != nil` → error (the §3.2 rule: no listener while a revocation lives
  only in memory); `startRemote` records it as `remoteErr`.
- `opts.Devices.Err() != nil` → still listens (so `lola doctor` and pairing report the problem
  through the daemon rather than a dead port), authenticates nobody, and logs
  `remote: devices.json is unreadable; no phone can connect: <err>` once per start.
- Bind is exactly `[remote].bind` (`resolveBind` unchanged). The M1 forced-loopback rule and its
  warnings are deleted. Defaults stay `enabled = false`, `bind = "localhost"`.
- Daemon wiring (`internal/daemon/remotewire.go` `startRemote`): pass `Devices: d.devices`,
  `Pairer: d.pairer`, `Host`. `d.devices = devices.Open(home, time.Now, logf)` and
  `d.pairer = remote.NewPairer(d.devices, time.Now, logf)` are created ONCE in
  `newDaemon(cfg, lin, logger, home)` (`daemon.go:591`, which already has `home`), not in `Run`,
  so every test daemon built with `newDaemon`/`newTestDaemon` has both and `startRemote` /
  `d.handle` never meet nil. A reload or `reconcileRemoteBind` rebuild keeps paired state and an
  open pairing window. `newDaemon` also calls `d.devices.Subscribe(d.devicesChanged)`;
  `devicesChanged` (in `remotewire.go`) calls `d.hubRelayDirty()` (spec 03 §10.2; a no-op in
  `internal/daemon/zz_stub_relay.go` until the relay loop lands, `00-build-plan.md`). New fields on `Daemon` next to `remote`/`remoteErr`:
  `devices *devices.Registry`, `pairer *remote.Pairer`. Lock order unchanged (`remoteMu → d.mu`);
  `devices` and `pairer` have their own mutexes and are never held across either; subscribers
  run outside `devices`' lock.

### 5.1 Connection pools

The listener is untagged and meant for `bind = "lan"`, so an unauthenticated LAN peer must not
be able to fill the slots a paired phone needs. Two pools replace the single `maxConnections`:

| Pool | Taken | Released / moved | Bound |
|---|---|---|---|
| pre-auth (LAN) | at accept, before TLS, keyed by source IP | on close, or MOVED to authed after `Authenticate` | `maxPreAuthConns` global, `maxPreAuthPerIP` per source IP |
| pre-auth (relay) | spec 03's `ReserveRelay`, before TLS | same move after `Authenticate` | spec 03's `maxRelayConnections` (4) |
| authed | after `Authenticate` succeeds | on close | `maxAuthedConns` global, `maxConnsPerDevice` per device |

- `acceptLoop`: `admitPreAuth(ip)` replaces `admit()`; refusal closes at once and logs (the
  existing line, with the pool named). The per-IP map holds only IPs with a live pre-auth
  socket, so it is bounded by `maxPreAuthConns`.
- `serveConn(nc, release func())` (spec 03 adds the parameter; this spec defines its meaning):
  `release` is idempotent (`sync.Once`) and frees the PRE-AUTH slot. `conn.run` calls it right
  after `Authenticate` succeeds, then `admitAuthed(deviceID)`: if `maxConnsPerDevice` is reached
  the device's OLDEST connection is `shutdown()` (logged `remote: device=<id> evicted oldest
  connection`): the newest one has just proven the key, the oldest is the likeliest half-open
  socket left by a network change. `maxAuthedConns = MaxDevices × maxConnsPerDevice`, so it can
  only be reached by paired devices; if it is, the new connection is closed and logged. The
  authed slot is released in `run`'s defer.
- A pairing candidate keeps its pre-auth slot for its whole life (it never authenticates).
- Effect: established phone connections are never displaced by strangers. A NEW connection
  still needs a pre-auth slot for ≤ `handshakeTimeout`; filling that pool takes
  `maxPreAuthConns / maxPreAuthPerIP` = 16 distinct source addresses dialling continuously,
  each socket costing the attacker a completed TCP handshake. The relay path has its own pool
  (spec 03), so a LAN flood never blocks a relayed reconnect, nor the reverse.

---

## 6. Pairing

### 6.1 Socket commands (local socket only)

New constants in `internal/protocol/devices.go`:

```go
const (
    CmdPairBegin      = "pairBegin"
    CmdPairStatus     = "pairStatus"
    CmdPairConfirm    = "pairConfirm"
    CmdPairReject     = "pairReject"
    CmdDevices        = "devices"
    CmdRevokeDevice   = "revokeDevice"
    CmdSetDeviceTiers = "setDeviceTiers"

    CmdRemotePair = "remote.pair" // consumed by Authenticate; never reaches d.handle
)
// The relay credential command is spec 03's: protocol.CmdRelayCred ("relayCred",
// internal/protocol/relay.go), a handled read-tier command (§8.3).

// LocalOnlyCommands are reachable from the 0600 unix socket and nowhere else:
// tier "never" in internal/remote, refused again by Daemon.handleRemote, and
// named by no hook-bridge allowlist.
var LocalOnlyCommands = []string{CmdPairBegin, CmdPairStatus, CmdPairConfirm, CmdPairReject,
    CmdDevices, CmdRevokeDevice, CmdSetDeviceTiers}

const (
    TierRead        = "read"
    TierAnswer      = "answer"
    TierControl     = "control"
    TierDestructive = "destructive"
    TierShell       = "shell"
    TierNever       = "never" // a classification, never a grantable tier
)
var Tiers        = []string{TierRead, TierAnswer, TierControl, TierDestructive, TierShell}
var DefaultTiers = Tiers // plan §3.2: all on; shell is a named toggle in the confirm UI
```

Wire types (all `json` tags exactly as written):

```go
type PairBeginArgs struct {
    AllowLoopback bool `json:"allowLoopback,omitempty"` // simulator/dev: accept a loopback-only QR (§6.2)
}
type PairBeginData struct {
    Pairing   string    `json:"pairing"`             // 16 hex; "" with Problem
    Payload   string    `json:"payload"`             // "lola2.…" — SECRET until burned; never logged
    ExpiresAt time.Time `json:"expiresAt,omitzero"`
    Addrs     []string  `json:"addrs,omitempty"`     // as in the payload, for display
    Port      int       `json:"port,omitempty"`
    Pin       string    `json:"pin,omitempty"`       // daemon SPKI, std base64 (DeviceKey.SPKIPin)
    Relay     bool      `json:"relay,omitempty"`     // payload carries a relay rendezvous (spec 03)
    Problem   string    `json:"problem,omitempty"`   // one sentence; nothing else is set
}

type PairStatusArgs struct {
    Pairing string `json:"pairing"`
    Since   string `json:"since,omitempty"`  // a State; with WaitMs, block until State != Since
    WaitMs  int    `json:"waitMs,omitempty"` // clamped to PairStatusMaxWait
}
type PairStatusData struct {
    Pairing    string      `json:"pairing"`
    State      string      `json:"state"`                // PairState* below
    DeviceName string      `json:"deviceName,omitempty"` // confirming|paired: what the phone said, sanitized
    Platform   string      `json:"platform,omitempty"`
    NameClash  bool        `json:"nameClash,omitempty"`  // confirming: DeviceName equals a paired device's name
    Replaces   string      `json:"replaces,omitempty"`   // confirming: id of the entry this key re-pairs, if any
    TriesLeft  int         `json:"triesLeft,omitempty"`  // confirming: code entries left (maxConfirmTries)
    Via        string      `json:"via,omitempty"`        // confirming|paired: "lan" | "relay" — the presenting connection's path
    Device     *DeviceInfo `json:"device,omitempty"`     // paired only
    Reason     string      `json:"reason,omitempty"`     // failed only: PairReason*
    ExpiresAt  time.Time   `json:"expiresAt,omitzero"`   // deadline of the CURRENT state
}
// There is deliberately NO code field: the daemon never shows the code. The human reads it on
// the phone and types it (§6.4 "Why the human types the code").

type PairConfirmArgs struct {
    Pairing string   `json:"pairing"`
    Code    string   `json:"code"`            // the 6 digits the human TYPED from the phone; spaces stripped
    Tiers   []string `json:"tiers,omitempty"` // empty = DefaultTiers
    Name    string   `json:"name,omitempty"`  // optional rename; default the phone's name
}
// → PairStatusData (State "paired", Device set)

type PairRejectArgs struct{ Pairing string `json:"pairing"` }
// → PairStatusData (State "rejected"); also cancels a "waiting" pairing (closes the QR)

type DeviceInfo struct {
    ID          string    `json:"id"`
    Name        string    `json:"name"`
    Platform    string    `json:"platform,omitempty"`
    Tiers       []string  `json:"tiers"`
    PairedAt    time.Time `json:"pairedAt"`
    LastSeenAt  time.Time `json:"lastSeenAt,omitzero"`
    Connected   int       `json:"connected"`   // Server.DeviceConns; 0 when the listener is down
    Fingerprint string    `json:"fingerprint"` // hex(SPKIHash[:8]) in 4-char groups: "d3f9 a1c0 7be4 1d2a"
}
type DevicesData struct {
    Devices []DeviceInfo `json:"devices"`
    Problem string       `json:"problem,omitempty"` // registry broken: names the file
}

type RevokeDeviceArgs struct{ ID string `json:"id"` }
type RevokeDeviceData struct {
    ID     string `json:"id"`
    Closed int    `json:"closed"` // live connections ended
}

type SetDeviceTiersArgs struct {
    ID    string   `json:"id"`
    Tiers []string `json:"tiers"`
}
// → DeviceInfo
```

All of these travel in `Request.Args` (no new flat `Request` fields).
Handlers live in `internal/daemon/pairing.go`; `server.go` gains one case per command.

### 6.2 QR payload — `lola2.`

`internal/remote/pairpayload.go`:

```go
const (
    PairPayloadPrefix   = "lola2."
    PairPayloadVersion  = 2
    MaxPairPayloadBytes = 1024 // whole string, prefix included
    maxPayloadAddrs     = 8
)

type PairPayload struct {
    V      int          `json:"v"`              // 2
    Pin    string       `json:"pin"`            // daemon SPKI SHA-256, std base64 (44 chars)
    Addrs  []string     `json:"addrs,omitempty"`// IP literals, no zone ids, ≤ 8; may be empty when Relay is set
    Port   int          `json:"port,omitempty"` // 1..65535 when Addrs non-empty
    Secret string       `json:"secret"`         // base64url (no pad) of 32 random bytes
    Relay  *PairRelayQR `json:"relay,omitempty"`// spec 03 fills it
}
type PairRelayQR struct {
    URL string `json:"url"` // hub base URL, https
    RV  string `json:"rv"`  // rendezvous id this phone keeps after pairing (32 hex)
    Tok string `json:"tok"` // pairing-window token (43 base64url, 256 random bits, minted by the
                            // daemon with hubproto.NewRelayBearer); the hub holds only its SHA-256,
                            // registered with expires_at = window expiry (spec 03 §10.6)
}

func EncodePairPayload(p PairPayload) (string, error)  // "lola2." + base64url(json), RawURLEncoding
func DecodePairPayload(s string) (PairPayload, error)  // tolerant of unknown fields; v must be 2
```

Validation (both directions): `v == 2`; `pin` decodes to 32 bytes; `secret` decodes to 32 bytes;
at least one of {non-empty `addrs` with valid `port`, `relay`}; every addr parses as an IP
without `%`; `relay.url` is `https`, `relay.rv` 32 lowercase hex, `relay.tok` 43 base64url
chars; total length ≤ `MaxPairPayloadBytes`. A `lola1.` string → `ErrNotPairPayload`.

The pairing secret never leaves the inner TLS session and never reaches the hub in any form; the
hub admits a relayed pairing dial only by `tok`, which proves nothing to the daemon.

`addrs`/`port` come from the running listener through `listenerDial` (moved from
`internal/daemon/pairbegin.go` into `pairing.go`) with one change of ORDER: non-loopback
addresses first (in `remote.ReachableHosts()` order for unspecified binds, then explicit
binds), loopback addresses last (kept for the simulator). `handlePairBegin` then decides:

`handlePairBegin` order: `ticket := Pairer.Begin()`, then `qr := d.relayPairOffer(ctx, ticket)`
(spec 03 §10.6; nil = no relay), then the table:

| Listener addrs | `qr != nil` | Result |
|---|---|---|
| any non-loopback | either | QR as usual |
| loopback only | yes | QR as usual (the relay is the phone's path) |
| loopback only | no | `Pairer.Reject(ticket.ID)`; `Problem: "The listener only accepts this Mac; set [remote].bind = \"lan\" or join a hub."`, no QR |
| no listener (relay-only server, spec 03 §11.3) | yes | QR with relay only (no addrs) |
| no server, or no listener and no relay | no | `Pairer.Reject(ticket.ID)`; `Problem` as today ("The phone listener is off …") |

A test build that wants a simulator-only QR sets `[remote].bind = "127.0.0.1"` AND passes
`pairBegin` args `{"allowLoopback": true}` (`PairBeginArgs.AllowLoopback`, accepted on the local
socket only — the whole command is local-only).

**Relay in the QR** (interface to spec 03): `relayPairOffer` decides eligibility from facts
(`d.relayEligible()`), mints `rv` (`hubproto.NewRelayID`) and `tok, tokHash`
(`hubproto.NewRelayBearer`), records them on the open window with
`Pairer.SetWindowRelay(ticket.ID, rv, tokHash)`, syncs the hub (window entry, `expires_at` =
`ticket.ExpiresAt`) and returns `&PairRelayQR{URL, RV: rv, Tok: tok}`; on any failure it clears
the window's relay and returns nil. `tok` is in the payload only; it is never stored, logged
or returned elsewhere.

### 6.3 State machine (`internal/remote/pairing.go`)

One pairing at a time. Memory only; nothing about a pairing is persisted. Every transition is a
compare-and-swap on the current state under `p.mu`: an event that finds the pairing in another
state than the one it expects changes nothing and refuses only its own connection.

```
                 pairBegin
                    │
                    ▼
   ┌──────────── waiting ─────────────────────────────┐ (secret live; window open;
   │                                                  │  ExpiresAt = begin + PairSecretTTL)
   │ pairReject / pairBegin (new) → cancelled         │
   │ TTL elapsed                  → expired           │ remote.pair with a VALID proof and a
   │ (a bad proof, however many: that conn closes,    │ remote.pair with a VALID proof and a
   │  state stays waiting, counter +1)                │ valid name (CAS waiting→confirming):
   │                                                  │ secret BURNED, Code computed (never shown)
   │                                                  ▼
   │                                              confirming  (ExpiresAt = presentation + PairConfirmTTL)
   │                                                  │
   │   pairConfirm(code ok) ──────────────────────────┼──▶ paired      (registry written; phone answered OK; conn closed)
   │   pairConfirm(code wrong) #maxConfirmTries ──────┼──▶ failed(code_mismatch)
   │   registry write fails ──────────────────────────┼──▶ failed(registry | full)
   │   pairReject ────────────────────────────────────┼──▶ rejected
   │   TTL elapsed ───────────────────────────────────┼──▶ expired
   │   phone conn closes / sends any frame ───────────┼──▶ failed(phone_left)
   │   pairBegin (new) ───────────────────────────────┴──▶ cancelled
   ▼
 terminal states: paired | rejected | expired | failed | cancelled
 (retained PairTerminalRetention for pairStatus, then forgotten → pairStatus answers error "unknown pairing")
```

Events that do NOT change the state:

- A candidate whose `remote.pair` carries a wrong proof while `waiting`: its connection gets
  `CodePairFailed` "pairing failed" and closes; `badProofs++`; the secret stays live. With 32
  random bytes a wrong proof teaches the sender nothing, so neither burning on it nor failing
  the pairing after N of them may happen: either would let anyone who can reach the port cancel
  every pairing (a throwaway certificate completes the handshake while a window is open, and a
  per-pairing cap costs such a peer only N connections). The count is an audit signal: the
  `badPairProofsAlert`-th (5) writes one unthrottled line, `remote: pair pairing=<id> has seen
  <n> wrong proofs; …`; each refusal's own line goes through the candidate log bucket.
  `PairReasonTooManyAttempts` stays in the vocabulary for old clients; the daemon no longer
  produces it.
- A malformed name, a malformed `remote.pair`, a timeout, or any other first frame while
  `waiting`: that connection closes (§6.4 step 1); nothing is counted or burned.
- Any candidate presenting while the pairing is `confirming` (even with a valid proof — only a
  holder of the photographed QR can make one): its connection gets `CodePairFailed`
  "another phone is pairing" and closes; the confirming pairing is untouched.

Constants:

```go
const (
    PairSecretTTL          = 5 * time.Minute
    PairConfirmTTL         = 2 * time.Minute
    PairTerminalRetention  = 60 * time.Second
    PairStatusMaxWait      = 25 * time.Second
    pairSecretBytes        = 32
    pairIDBytes            = 8
    badPairProofsAlert     = 5               // wrong proofs that write one alert line (never a state change)
    pairFirstFrameTimeout  = 3 * time.Second // a candidate's wait for its remote.pair
    maxConfirmTries        = 3               // typed codes per pairing before it fails
    maxUnpairedConns       = 4               // concurrent candidates (all sources) past their first frame
    maxUnpairedPerSource   = 2               // concurrent candidates per sourceKey (§4.3); = spec 04 PAIR_MAX_PARALLEL,
                                             // so the phone's own pairing race never trips it
)
// States
const (
    PairStateWaiting = "waiting"; PairStateConfirming = "confirming"; PairStatePaired = "paired"
    PairStateRejected = "rejected"; PairStateExpired = "expired"; PairStateFailed = "failed"
    PairStateCancelled = "cancelled"
)
// Reasons (failed only)
const (
    PairReasonTooManyAttempts = "too_many_attempts"; PairReasonCodeMismatch = "code_mismatch"
    PairReasonPhoneLeft = "phone_left"; PairReasonRegistry = "registry"; PairReasonFull = "full"
)
```

(The `PairState*`/`PairReason*` names live in `internal/protocol/devices.go`; the durations and
limits in `internal/remote/pairing.go`, except `PairStatusMaxWait`, which the daemon clamps with
and is exported from `remote`.)

`Pairer` API:

```go
func NewPairer(reg *devices.Registry, now func() time.Time, logf func(string, ...any)) *Pairer

type Ticket struct{ ID string; Secret []byte; ExpiresAt time.Time }
func (p *Pairer) Begin() (Ticket, error) // cancels any open pairing first; the window has no relay yet

// SetWindowRelay records (rv, tokHash) on pairing id while it is `waiting`; "" clears them.
// Returns false (and changes nothing) when id is not the open, waiting pairing. Called only by
// spec 03's relayPairOffer.
func (p *Pairer) SetWindowRelay(id, rv, tokHash string) bool

// Window reports the open window's relay entry for spec 03's relayEntries; ok=false when no
// window is waiting or it carries no relay rv.
func (p *Pairer) Window() (rv, tokHash string, expires time.Time, ok bool)

func (p *Pairer) Status(ctx context.Context, a protocol.PairStatusArgs) (protocol.PairStatusData, error)

// ConfirmRelay is the device's long-lived relay credential, minted by the daemon (spec 03's
// d.relayMint: a FRESH rv and hubproto.NewRelayBearer) before Confirm; nil = this phone gets
// no relay credential now (it gets one later through relayCred, spec 03 §10.6).
type ConfirmRelay struct {
    Bearer string               // plaintext, sent once in PairResult.Relay, never stored
    Record devices.DeviceRelay  // hashes only; written with the registry entry
}
func (p *Pairer) Confirm(a protocol.PairConfirmArgs, relay *ConfirmRelay) (protocol.PairStatusData, error)
func (p *Pairer) Reject(id string) (protocol.PairStatusData, error)

// transport side (unexported, used by verifyPeer / deviceAuthorizer)
func (p *Pairer) windowOpen() bool                          // state == waiting && now < ExpiresAt (verifyPeer)
func (p *Pairer) pending() bool                             // waiting or confirming, not expired (Authenticate)
func (p *Pairer) windowRV() string                          // the open window's SetWindowRelay rv; "" matches no handshake
func (p *Pairer) admitUnpaired(source string) (release func(), ok bool) // maxUnpairedConns / maxUnpairedPerSource
func (p *Pairer) present(ctx context.Context, hs *Handshake, phone [32]byte) (Peer, error) // always returns an error
```

Expiry is evaluated lazily on every call and by one `time.AfterFunc` per state deadline (which
also wakes `Status` waiters and the blocked `present`). `Confirm` errors (all `error` → the
socket's `{ok:false,error}`): unknown pairing; state not `confirming` ("not waiting for a
confirmation"); code mismatch ("the code does not match the phone; <n> tries left", state
unchanged until the `maxConfirmTries`-th mismatch, which moves it to `failed(code_mismatch)`);
bad tiers; registry error (state → `failed(registry|full)`). The comparison is
`hmac.Equal([]byte(strip(a.Code)), []byte(live))` where `strip` removes ASCII spaces.

The daemon's `handlePairConfirm` passes `relay = d.relayMint()` (spec 03 §10.6): nil when
`d.relayEligible()` is false, else a FRESH rv and bearer, never the window's rv (the QR's rv and
`tok` die with the window). After `Confirm` returns `paired` it calls nothing else: the
registry's subscriber (`devicesChanged` → `hubRelayDirty`) publishes the new rendezvous and the
next sync drops the window entry (the pairing is no longer `waiting`).

### 6.4 The handshake on the wire

Pre-conditions: the phone pinned `pin` (it aborts unless the server's leaf SPKI SHA-256 equals it)
and presented its own P-256 self-signed client certificate; `verifyPeer` admitted it as a
pairing candidate; `Authenticate` took an unpaired slot and, for a relayed candidate, checked
`hs.RelayRV == windowRV()` (§4.3).

1. **The only frame an unpaired certificate may send** is the first one:
   `{"v":2,"type":"req","id":"<id>","cmd":"remote.pair","payload":<PairRequest>}` within
   `handshakeTimeout` (10 s, the read deadline `conn.authenticate` set). Any other type, any
   other `cmd`, a malformed payload, or a timeout → err frame `CodeDenied` "pair first" (best
   effort), close. Nothing is counted or burned.

   ```go
   type PairRequest struct {
       Proof    string `json:"proof"`              // base64url(HMAC-SHA256(secret, exporterProof))
       Name     string `json:"name"`               // device's own name, ≤ MaxNameRunes
       Platform string `json:"platform,omitempty"` // "ios"
   }
   ```

2. **State check.** Not `waiting` → `CodePairFailed` "another phone is pairing" (when
   `confirming`) or "pairing is closed" (otherwise), close. State untouched.

3. **Proof.**
   ```
   exporterProof = cs.ExportKeyingMaterial("EXPORTER-lola-pair-proof-v1", nil, 32)
   proof         = HMAC-SHA256(key = secret, msg = exporterProof)
   ```
   Compared with `hmac.Equal`. Mismatch → `badProofs++` (§6.3), err `CodePairFailed`
   "pairing failed", close; the pairing stays `waiting`. Binding to the exporter makes a captured proof useless on any other TLS
   session (including across the relay).

4. **Name.** `devices`' name rules (§3.2) → err `CodePairFailed` "the device name is not
   accepted", close; state stays `waiting` (the proof was valid, but nothing was confirmed yet;
   a valid proof with a bad name is a client bug, not an attack, and is logged).

5. **Burn and code.** CAS `waiting → confirming`; the secret is zeroed and the window closes
   (`verifyPeer` now refuses every unknown certificate). Both sides compute the code; the
   daemon keeps it in memory for `Confirm` and never returns it on any socket reply, log or UI:
   ```
   exporterCode = cs.ExportKeyingMaterial("EXPORTER-lola-pair-code-v1", nil, 32)
   mac  = HMAC-SHA256(key = exporterCode,
                      msg = "lola-pair-code-v1" || daemonSPKIHash[32] || phoneSPKIHash[32])
   code = fmt.Sprintf("%06d", binary.BigEndian.Uint64(mac[:8]) % 1_000_000)
   ```
   `PairStatusData` gets `DeviceName`, `Platform`, `NameClash` (true when a paired device has the
   same name, case-insensitively), `Replaces` (the id when this key is already registered),
   `TriesLeft = maxConfirmTries`, `Via` (`hs.Via`). The phone shows the 6 digits and "Type this
   code on your Mac". The desktop and `lola pair` show "via relay" when `Via == "relay"`: a
   relayed candidate can be anywhere on the internet (spec 03 §14 "Photographed QR").
   The daemon sends nothing yet.

6. **Wait, with one reader.** `present` calls `hs.SetReadDeadline(time.Time{})` (the wait may
   last `PairConfirmTTL`) and starts ONE watcher goroutine that calls `hs.NextFrame()` once and
   sends the result (frame or error) on a buffered channel `got`. It is the only reader of the
   connection from here on. `present` selects on `got` and the pairing's state-change signal:
   - `got` fires first (any frame, EOF, or error) → CAS `confirming → failed(phone_left)`.
   - a terminal state arrives first → `hs.SetReadDeadline(time.Now())` to cut the watcher's read,
     receive from `got` (the watcher has returned; a frame it read in that instant is discarded:
     the state is already terminal), then step 7.
   Nothing else reads the connection: `present` always ends the connection (step 7), so no frame
   loop ever starts behind a watcher. TCP keep-alive (`tuneKeepAlive`) or spec 03's
   `RelayDeadAfter` detects a vanished phone.

7. **Outcome** on the `remote.pair` frame id, then close (`present` returns `ErrPairingDone`):
   - `paired` → `resp` frame, `Response{OK:true, Data: PairResult}`. The phone reconnects (any
     path) as a paired device and waits for `ready` (§4.6). Pairing is rare; one reconnect is
     cheaper than handing a half-read stream to the frame loop.
     ```go
     type PairResult struct {
         DeviceID string     `json:"deviceId"`
         Name     string     `json:"name"`
         Tiers    []string   `json:"tiers"`
         Host     string     `json:"host"`            // Options.Host, display only
         Relay    *PairRelay `json:"relay,omitempty"` // nil when Confirm got no ConfirmRelay
     }
     type PairRelay struct {
         RV     string `json:"rv"`     // the rendezvous this device keeps (fresh; never the QR's)
         Bearer string `json:"bearer"` // its long-lived bearer; the QR's tok is retired by this pairing
     }
     ```
     The new rv reaches the hub through an asynchronous sync, so a relayed reconnect right
     after pairing can meet a 401; the phone treats a 401 within
     `hubproto.RelayFreshCredentialGrace` of storing a bearer as a backoff, not a dead
     credential (spec 03 §13.3).
   - `rejected` → err `CodePairRejected`; `expired` → `CodePairExpired`; `failed`/`cancelled` →
     `CodePairFailed`.

8. **Audit**, one line per transition and per refused candidate (never the secret, proof, tok,
   code or payload):
   `remote: pair pairing=<id> state=<state> reason=<reason|-> device=<id|-> name=<logSafe> addr=<remoteAddr> via=<lan|relay> bad=<badProofs>`

**Why the human types the code.** The secret authenticates whoever holds the QR. A person who
photographed it can present a valid proof first; the pairing then sits in `confirming` with the
intruder's key, and the real phone gets "another phone is pairing" and shows no code. The
desktop and `lola pair` never display the code: they ask the human to TYPE the 6 digits the
phone in their hand shows. The intruder's code exists only on the intruder's phone, so the
human cannot type it, and three wrong guesses fail the pairing (`maxConfirmTries`; a blind guess
succeeds with probability 3 × 10⁻⁶). A Confirm/Reject button next to a displayed code would
prove nothing — a click is not a comparison. `NameClash` and `Replaces` let the confirm prompt
say "new device" when the name equals an already-paired phone's (the name is chosen by the
phone and proves nothing).

### 6.5 New refusal codes (`internal/protocol/frame.go`)

Append to the closed list (additive):

```go
CodePairRejected = "pair_rejected"
CodePairExpired  = "pair_expired"
CodePairFailed   = "pair_failed"
CodeNotPaired    = "not_paired" // §4.6
```

An unpaired certificate with no window open never gets a frame (TLS alert, surfacing on the
client's first receive). The client maps a `bad_certificate` alert and `not_paired` to "not
paired" (spec 04).

### 6.6 Golden vectors

`internal/protocol/testdata/pairing_vectors.json`: fixed `secret`, `exporterProof`,
`exporterCode`, `daemonSPKIHash`, `phoneSPKIHash` (hex) → expected `proof` (base64url) and
`code`; plus three `lola2.` payloads (direct only, relay only with `{url, rv, tok}`, both) with
their decoded JSON. The proof and code are pinned by `internal/protocol/pairing_vectors_test.go`
(pure functions `protocol.PairProof(secret, exporter []byte) []byte` and
`protocol.PairCode(exporterCode []byte, daemonSPKI, phoneSPKI [32]byte) string` live in
`internal/protocol/devices.go` so Go has one implementation). The payloads are pinned by
`internal/remote/pairpayload_test.go` (`TestPairPayloadVectors`, reading
`../protocol/testdata/pairing_vectors.json`; `protocol` cannot import `remote`). In spec 04 the
Swift `LolaTransportCore` tests read the same file.

Wire-frame vectors go in `mobile/src/wire/testdata/pairing.json` (the file spec 02's split
assigns to this spec, same schema as `frames.json`; the ONLY home of `req_remote_pair`):
`req_remote_pair`, `resp_pair_result` (with and without `relay: {rv, bearer}`),
`err_pair_failed`, `err_pair_rejected`, `err_pair_expired`, `err_not_paired`, `ready`.

---

## 7. Limits (all named constants)

| Constant | Value | Where | Effect when exceeded |
|---|---|---|---|
| `maxPreAuthConns` | 32 | `server.go` | accept closed, logged (replaces `maxConnections`) |
| `maxPreAuthPerIP` | 2 | `server.go`, per source IP | accept closed, logged |
| `maxAuthedConns` | `devices.MaxDevices × maxConnsPerDevice` = 64 | `server.go` | new authenticated conn closed, logged |
| `maxConnsPerDevice` | 4 | `server.go` | the device's OLDEST connection is shut down, logged |
| `maxRelayConnections` | 4 | spec 03 `relay.go` | relay pre-auth pool (spec 03) |
| `reqConcurrency` | 4 (unchanged) | `server.go` | `CodeRateLimited` |
| `readRate` / `readBurst` | 20/s / 60 | `ratelimit.go`, per device | `CodeRateLimited`, conn survives |
| `writeRate` / `writeBurst` | 1/s / 10 | per device; req frames of tier answer/control/destructive/shell, EXCEPT non-final `attachImage` parts, which pay one read token instead | `CodeRateLimited` |
| `extRate` / `extBurst` | 0.2/s / 3 | per device; `extCommands` (below), charged IN ADDITION to the tier bucket | `CodeRateLimited` |
| `attachBytesRate` / `attachBytesBurst` | 2 MiB/s / 20 MiB | per device; raw `Data` bytes of every `attachImage` part | `CodeRateLimited` |
| `ptyBytesRate` / `ptyBytesBurst` | 32 KiB/s / 256 KiB | per device; `pty` write payload bytes | `CodeRateLimited` on the pane (`refusePane`), bytes dropped |
| `subRate` / `subBurst` | 5/s / 20 | per device; `sub` frames | `CodeRateLimited` on the pane |
| `scrollRate` / `scrollBurst` | 10/s / 30 | per device; `pty` scroll frames (each execs tmux twice) | `CodeRateLimited` on the pane, frame dropped |
| `credRate` / `credBurst` | 1/30 s / 6 | per device; `relayCred`, IN ADDITION to the read bucket (it may mint a bearer: a registry write and a hub sync) | `CodeRateLimited` |
| `logRate` / `logBurst` | 0.2/s / 10 | per device; lines a device causes at no other cost: rate-limit and in-flight refusal audits, a refused non-final `attachImage` part, an inbound `err` frame, a failed scroll | line suppressed and counted |
| `maxRefusalsPerConn` | 64 | `conn.go` | conn closed, logged |
| `refusalLogRate` / `refusalLogBurst` | 1/s / 10 | `identity.go`, global | log line suppressed and counted |
| `maxUnpairedConns` | 4 | `pairing.go`, global | candidate gets `not_paired`, closed |
| `maxUnpairedPerSource` | 2 | `pairing.go`, per `sourceKey` | candidate gets `not_paired`, closed (= spec 04 `PAIR_MAX_PARALLEL`) |
| `badPairProofsAlert` | 5 | `pairing.go`, per pairing | one alert line; the pairing is untouched |
| `pairFirstFrameTimeout` | 3 s | `pairing.go`, per candidate | close (no slot was taken) |
| `maxConfirmTries` | 3 | `pairing.go`, per pairing | pairing `failed(code_mismatch)` |
| `handshakeTimeout` | 10 s (unchanged) | `server.go` | close |
| `ticketsTTL` | 30 s | `internal/daemon/tickets.go` | served from cache |

`extCommands` (in `ratelimit.go`, pinned by `TestExtCommandsAreTheExternalOnes`) are the
reachable commands whose handler calls `gh` or the Linear API on the request path: `prs`,
`tickets`, `prReadiness`, `mergePR`, `addressPRFeedback`, `openPr`, `openTicket`. At 0.2/s one
device costs at most 720 external calls an hour, against GitHub's 5,000/h authenticated quota
that the observer, merge queue and reviews share. `prs` also keeps its 20 s `prCache`; `tickets`
gains the same shape: a `ticketsCache` keyed by `(project, scope)` with `ticketsTTL`, one fetch
in flight per key (the `prCache` per-entry lock), so a burst of phones and the desktop share one
Linear query. `prs.refresh` from a phone is dropped by `fields.go` (§8.5), so a phone always gets
the cached list when it is fresh.

`attachImage` accounting: `fields.go`'s check for `attachImage` decodes `Last` and `len(Data)`
(spec 02 owns the rest of the check) and returns them; `conn.authorize` charges the byte bucket
for every part and the write bucket only for the `last:true` part. A 20 MiB photo therefore
uploads at link speed and costs one composer "write".

Buckets are keyed by `DeviceID` in a `map[string]*deviceLimits` on `Server` (so a direct and a
relayed connection of one phone share them), created on first use, deleted when the device has
no live connection for 10 minutes or on `DropDevice`. Token bucket: `golang.org/x/time` is not a
dependency, so `ratelimit.go` carries a 30-line bucket (`tokens float64, last time.Time`,
`Options.Now` clock).

---

## 8. Capability tiers — the closed allowlist

### 8.1 Mechanics (`internal/remote/tiers.go`)

```go
// commandTiers is the ONLY place a command becomes reachable from a phone.
var commandTiers = map[string]string{ /* table 8.3 */ }

// TierFor returns the tier of a req frame's cmd. Unknown, empty and "remote."-prefixed → TierNever.
func TierFor(cmd string) string

// frameTier: req → TierFor(cmd); sub, unsub → read; pty → decode PTYInputPayload.Action:
// write → shell, scroll → read, resize → read, other → never; err (inbound) → read; anything else → never.
func frameTier(f *protocol.Frame) string

// TransportCommands are tiered req cmds that internal/remote answers itself and never passes
// to Options.Handle: watch, unwatch (spec 02). remote.pair is NOT here: it is consumed by
// Authenticate before any tier applies, and on a paired connection it is TierNever like every
// "remote." cmd. relayCred (spec 03) is an ordinary handled command, not a transport one.
var TransportCommands = []string{protocol.CmdWatch, protocol.CmdUnwatch}

// CommandTiers returns a copy of commandTiers (tests, and the tier golden file of spec 04).
func CommandTiers() map[string]string
```

`conn.authorize` becomes:

```
tier := frameTier(f)
tier == never     → refuse(id, CodeUnknownCmd, "command is not available remotely"); strike++; continue
AuthorizeFrame    → ErrUnauthenticated: close.  ErrDenied: refuse(id, CodeDenied, "not permitted for this device"); strike++
rebuild (§8.5)    → error: refuse(id, CodeDenied, <fixed sentence>); strike++
rate limits (§7)  → tier bucket, then extRate (extCommands), then attach bytes → refuse(id, CodeRateLimited, …)
```

A refusal no longer closes the connection (plan §5: the client's denial mirror is deleted). The
strike counter (`maxRefusalsPerConn`) bounds a probing client. Framing errors (bad version,
unknown frame type, wrong direction, oversized, malformed envelope) still close — the stream
cannot be resynchronised.

`doc.go`'s "denied command closes" paragraph and its M1 tag narrative are rewritten to this.

### 8.2 Tier meaning

A phone is a human surface. The tiers bound what a STOLEN or misused phone can do; they are not
a sandbox, and their text says exactly what each one lets happen on the Mac.

| Tier | Grants | Default at pairing |
|---|---|---|
| `read` | Every read. Mandatory: a device without it is refused by `devices`. | on (locked) |
| `answer` | Words and decisions to a parked or running agent through the human send paths; review marks and notes. Words are never a command line: every send passes `sanitizeAgentText`, which defuses a leading `!` (§12 item 11). **Approving an agent's permission card (`agentChoose`) runs what the agent proposed** — a Bash card runs that command on the Mac, inside the agent's sandbox if it has one. | on |
| `control` | Starts, steers and merges work; never discards uncommitted work and never removes a session. **Starting work runs CONFIGURED commands inside trees the agent can write**: `dev` runs the project's `dev_commands`, opens and `revive` run `post_create`, and both resolve tools (`npm`, `node_modules/.bin`, …) from the worktree. | on |
| `destructive` | Removes sessions: `kill` without `force`, `closeExitedTemporary`, and `bestOfPick` (it kills the losing attempts and removes their clean worktrees). | on |
| `shell` | Raw keystrokes into any pane, new shells, and every command that runs a command line or a program the PHONE chooses or that the agent wrote: `pty` write, `shellCreate`, `open`, `openManual{agent:false}`, `reviewPackageVerify`, `reviewPackageCapture`. | on, its own named toggle in the confirm UI |
| `never` | Classification only. Unreachable whatever the device holds. | — |

Turning `shell` off removes raw terminal access and free command lines from the phone. It is not
a guarantee that no code runs on the Mac: `answer` can approve an agent's tool call, and
`control` starts configured commands in agent-writable trees.

### 8.3 The table — every command `d.handle` routes, plus new ones

Enumerated from `internal/daemon/server.go` `(*Daemon).handle` (every `case` label, constants
resolved from `internal/protocol`): 166 case labels today, minus the deleted
`regenerateRemoteKey` = 165, plus 7 new handled commands (`pairStatus`, `pairConfirm`,
`pairReject`, `devices`, `revokeDevice`, `setDeviceTiers`, `sessionTimeline`; `pairBegin` already
exists) = 172, plus spec 02's two handled commands `attachImage` and `attachmentImage` (`d.handle`
cases reached through `Options.Handle` with the peer in the context) = 174, plus spec 03's
`relayCred` = 176, plus the desktop-only `askMerge` and `addApps` = 178, plus the plan gate's `planSubmit`, `planDecide`, `planGate` = 181, plus desktop-only `removeEndedWorktree` = **182 handled**. Plus `TransportCommands` (`watch`, `unwatch`), answered by
`internal/remote`, and `remote.pair`, consumed by `Authenticate`.

Split of the 182: read 24, answer 12, control 24, destructive 3, shell 6, never 113.
(Coverage was checked mechanically against `server.go` while writing this spec.)

**read** (24 handled; plus `watch`, `unwatch` from `TransportCommands`)

| Command | Notes |
|---|---|
| `status`, `sessions`, `projects`, `hubStatus` | `sessions` keeps `handleRemote`'s `SessionInfo.SSH = nil` strip |
| `panes`, `pane` | |
| `diff`, `checkpoints`, `checkpointDiff` | |
| `prReadiness`, `prs`, `tickets` | `extCommands`: `extRate` per device (§7); `tickets` cached (`ticketsTTL`) |
| `bestOfCompare` | read-only ranking over session state; calls no external API |
| `reviewPackages`, `reviewPackageImage`, `reviewPackageDiff`, `reviewPackageChecks` | `reviewPackageChecks` reads retained records; no exec |
| `agentFeed`, `agentFeedOutput`, `talkState`, `endedSessions` | `protocol.TalkCommands` |
| **`sessionTimeline`** (new) | §8.6 |
| `attachmentImage` | spec 02 §12.3; field spec owned by spec 02 |
| `watch`, `unwatch` | transport (spec 02) |
| `relayCred` | spec 03 §10.6, `d.handleRelayCred`. SELF-ONLY: it reads the device from `remote.PeerFromContext` (spec 02) and changes nothing but that device's own `Relay` record; the unix socket (no peer) is refused. It may issue or rotate the caller's own bearer, so every device may send it (`read` is mandatory). |

**answer** (12)

| Command | Field rules (§8.5) |
|---|---|
| `agentSay` | `Conn` DROPPED (setting it hands the SSH wheel back, an ssh human act) |
| `agentChoose` | approving a permission card runs the proposed tool call (§8.2) |
| `agentStop`, `agentQueueRelease`, `agentSetting` | |
| `answer` | `Session`, `Text` |
| `feedback` | queues, never forces (unchanged handler) |
| `reviewPackageMark`, `reviewPackageFeedback` | plan omitted `reviewPackageFeedback`; it is the review-package note, the analogue of `feedback` |
| `attachImage` | spec 02; byte bucket, only `last:true` costs a write (§7) |
| `sshRecall` | moved to `TalkCommands` (§8.4): takes back an undelivered "You" message (plan §6 "recall") |
| `sshAnswer` | moved to `TalkCommands`: answers an agent's question element; `handleRemote` refuses an element whose `Conn != ""` (§8.7) |

**control** (23)

`addressPRFeedback`, `mergePR` (`confirmUnready` allowed, audited), `openPr`, `openTicket`,
`openManual`, `openExisting` (field rules: §8.5), `revive`, `review`, `coderabbit`,
`resolveConflict`, `switchAgent`, `dev`, `devFreePort`, `planDecide`, `sessionTask`, `renameSession`,
`broadcast`, `bestOf`, `restoreCheckpoint`, `forkCheckpoint`, `pollOnce`, `enable`, `disable`,
`reviewPackageRecover`.

`enable`/`disable` write `config.toml` (`enablePollInFile`). They are the only config writes a
phone has; the plan lists them under control explicitly, and they flip one poll's `enabled`.
No other config write may join a tier (§10.1).

**destructive** (3)

`kill` (`Force` never carried; `KeepWorktree` allowed), `closeExitedTemporary`, `bestOfPick`
(`discardAttempt` kills each loser's agent and aux tabs, closes its SSH, runs
`nat.Kill(…, removeWorktree=true)`, deletes clean branches and closes the losers' PRs).

**shell** (6)

`shellCreate`, `paneClose`, `paneResize`, `open` (always creates a throwaway worktree + shell),
`reviewPackageVerify` (runs `Spec.Command`, a free command line, in the worktree),
`reviewPackageCapture` (runs `node -e` with `cmd.Dir = s.Worktree`, so `require` resolves the
browser helper from the agent-writable `node_modules`, against any http(s) URL the caller
names). Frame-level: `pty` with `action:"write"`.

**never** — unreachable whatever the device holds (113)

| Group | Commands |
|---|---|
| Daemon lifecycle / config | `stop`, `reload`, `renameProject`, `bulkProjects` |
| Agent in-pane channels | `hookEvent`, `agentReport`, `planSubmit`, `planGate`, `reviewPackagePublish`, `reviewPackageComplete` |
| Pairing / devices (`protocol.LocalOnlyCommands`) | `pairBegin`, `pairStatus`, `pairConfirm`, `pairReject`, `devices`, `revokeDevice`, `setDeviceTiers` |
| Transport-internal | `remote.pair` (and every `remote.*`), `""` |
| Branch graph (Mac only) | `askMerge` (asks a session's agent to merge a branch picked in the Mac's branch graph), `removeEndedWorktree` (explicit cleanup of a retained checkout through its ended history; may discard unverifiable files) |
| Session menu (Mac only) | `addApps` (checks further members of its stack out into a running stack session; it can restart a sandboxed agent, and a phone has no such menu) |
| Host browser | `openURL` (opens a URL on the Mac; the phone opens dev URLs itself — §12 item 3) |
| Hub human (`protocol.HubHumanCommands`, 13) | `hubJoin`, `hubLeave`, `hubBoard`, `hubOrg`, `hubClaim`, `hubRelease`, `hubSync`, `hubStats`, `hubCreateTeam`, `hubInvite`, `hubTeamSettings`, `hubRevoke`, `hubProbe` |
| SSH agent (`protocol.SSHAgentCommands`, 6) | `sshAgentHosts`, `sshAgentRequest`, `sshAgentRun`, `sshAgentPoll`, `sshAgentCancel`, `sshAgentPost` |
| SSH human (`protocol.SSHHumanCommands`, 24) | `sshHosts`, `sshOpen`, `sshConnect`, `sshHumanRun`, `sshShareBlock`, `sshTerminalCd`, `sshGo`, `sshDeny`, `sshApprove`, `sshRevokeGrant`, `sshTakeWheel`, `sshHandbackPreview`, `sshHandback`, `sshStop`, `sshClose`, `sshTrash`, `sshTerminal`, `sshTimeline`, `sshOutput`, `sshSay`, `sshProposal`, `sshResolve`, `sshWidgetStatus`, `sshPromptAnswer` |
| Planning (not in plan §6) | `plan`, `planApply` |
| Goals (not in §6) | `goalPropose`, `goalProposal`, `goalProposals`, `goalAcceptPlan`, `goalEditPlan`, `goalRecovery`, `goalDismissProgress`, `goalSyncBrief`, `goalAcceptBrief`, `goalOverview`, `goals`, `goalCreate`, `goalShow`, `goalUsage`, `goalPublish`, `goalPublishReviewed`, `goalStartOptions`, `goalStart`, `goalStartV2`, `goalControl`, `goalDeliveryEvidence` |
| Review profiles (not in §6) | `reviewProfiles`, `profileResults`, `profileReview`, `profileFindings`, `profileDecideFinding`, `profileCalibration` |
| Knowledge (not in §6) | `knowledgeVersion`, `knowledgeList`, `knowledgeLookup`, `knowledgeExport`, `knowledgeCapture`, `knowledgeCorrect`, `knowledgeAccept`, `knowledgeDispute`, `knowledgeMaintain`, `knowledgeImprovementCatalog`, `knowledgeImprovementSources`, `knowledgeImprovementDecisions`, `knowledgeImprovementHistory`, `knowledgeImprovements`, `knowledgeImprovement`, `knowledgeDecideImprovement`, `knowledgeExportImprovement` |
| Comparison (not in §6) | `comparisonTemplate`, `comparisonPrepare`, `comparisonReport` |

`sshWidgetStatus` stays `never`: phones receive no widget documents (§8.6), so they have no
widget fault to report. `sshProposal` stays `never`: it runs a command on an SSH host.

Deleted with this change: `regenerateRemoteKey` (not in the table; the AST test proves it is gone
from `handle`).

`never` entries are written into `commandTiers` explicitly (value `TierNever`), so the
totality test can tell "classified as never" from "forgotten".

### 8.4 Conversation commands move to `protocol.TalkCommands`

`internal/protocol/talk.go`:

```go
// TalkCommands are the Conversation commands: HUMAN side, never on the hook bridge,
// reachable from a paired phone only by tier (internal/remote/tiers.go). Never add one
// to SSHAgentCommands or SSHHumanCommands. sshRecall and sshAnswer keep their historical
// ssh prefix: they act on the Conversation store of ANY session (recall a queued message,
// answer a question element), not on an SSH host.
var TalkCommands = []string{CmdAgentFeed, CmdAgentFeedOutput, CmdTalkState, CmdAgentSay, CmdAgentStop,
    CmdAgentChoose, CmdAgentSetting, CmdAgentQueueRelease, CmdEndedSessions,
    CmdSSHRecall, CmdSSHAnswer}
```

`internal/protocol/ssh.go`: remove those eleven from `SSHHumanCommands` (it becomes the 24 ssh*
names above) and rewrite the "Every ssh command is refused over internal/remote for now" sentence
to "Every SSHAgentCommands and SSHHumanCommands entry is tier never in internal/remote; a phone
reads SSH only through sessionTimeline."

The hook bridge (`internal/daemon/hookbridge.go`) is an allowlist (`hookBridgeCmds`) and never
read `SSHHumanCommands`, so it is unaffected.

Tests that change:

- `internal/remote/policy_test.go` `TestEverySSHCommandIsDenied` → moved to `tiers_test.go`,
  same name, asserts `TierFor(cmd) == TierNever` for every entry of `SSHAgentCommands` and
  `SSHHumanCommands`. Unchanged in meaning: phones get no ssh-host command. Plus
  `TestTalkCommandsAreNotSSHCommands` (the two lists are disjoint, and `TalkCommands` ∩
  `SSHAgentCommands` = ∅).
- `internal/daemon/talk_contract_test.go` `TestTalkCommandsAreHumanOnly`: `slices.Contains(protocol.SSHHumanCommands, cmd)`
  → `slices.Contains(protocol.TalkCommands, cmd)`; `!remote.CommandDenied(cmd)` →
  `remote.TierFor(cmd)` is `read` for feed/feedOutput/talkState/endedSessions and `answer` for
  the rest. Bridge assertions unchanged.
- `internal/daemon/ssh_test.go` `TestSSHBridgeAdmitsAgentVerbsOnlyWithAValidToken` and
  `TestSSHEveryCommandIsHandled`: iterate `protocol.TalkCommands` as a third list.

### 8.5 Request rebuild (`internal/remote/fields.go`)

Replaces `normalizeRequest`. Every command whose tier is not `never` MUST have an entry
(`TestEveryTieredCommandHasAFieldSpec`), transport commands included.

```go
type flat uint32 // bit per protocol.Request flat field
const (
    fSession flat = 1 << iota; fText; fProject; fRef; fPoll; fDryRun; fProvider
    fKeepWorktree; fCols; fRows; fLines; fArgs
)
// Never carried for any command: Force, Event, Detail, Hook, HookToken.

type fieldSpec struct {
    fields flat
    args   reflect.Type                                   // the handler's Args type; nil when !fArgs
    check  func(req *protocol.Request, has func(string) bool) (res checkResult, err error)
    audit  []string                                       // Args json fields named in the audit line
}
type checkResult struct {
    dropped     []string // Args/flat fields removed, named in the audit line
    attachBytes int      // attachImage only: len(Data) of this part
    attachLast  bool     // attachImage only
}

var requestFields = map[string]fieldSpec{ /* below */ }

// rebuild returns a FRESH Request: Cmd from the envelope, only the listed flat fields copied,
// Args copied verbatim when fArgs, then check run. err → CodeDenied with err's fixed sentence.
func rebuild(cmd string, in protocol.Request, has func(string) bool) (protocol.Request, checkResult, error)
```

| Command(s) | Flat fields | Args type | Check |
|---|---|---|---|
| `status`, `sessions`, `projects`, `hubStatus`, `endedSessions` | — | — | |
| `panes`, `revive`, `coderabbit`, `resolveConflict`, `diff`, `checkpoints`, `closeExitedTemporary` | Session | — | |
| `pane` | Session, Lines | — | |
| `answer`, `renameSession` | Session, Text | — | |
| `review` | Session, Provider | — | |
| `kill` | Session, KeepWorktree | — | (Force structurally absent) audit `keepWorktree` |
| `shellCreate` | Session, Cols, Rows | — | |
| `open` | Project, Ref | — | |
| `enable`, `disable` | Poll | — | |
| `pollOnce` | Poll, DryRun | — | |
| `prReadiness`, `addressPRFeedback`, `mergePR` | Session, Args | `PRReadinessArgs` | `mergePR`: audit `method`, `confirmUnready` |
| `prs` | Args | `PrsArgs` | decode, `Refresh = false`, re-marshal; dropped `refresh` |
| `tickets` | Args | `TicketsArgs` | |
| `openPr` | Args | `OpenPrArgs` | |
| `openTicket` | Args | `OpenTicketArgs` | |
| `openManual`, `openExisting` | Args | `OpenManualArgs` | `Location ∉ {"", "worktree"}` or `Path != ""` → deny "only a new worktree can be opened from a phone"; `!Agent && !has(shell)` → deny "opening a shell needs the shell permission"; audit `agent` |
| `bestOf` / `bestOfPick` / `bestOfCompare` | Args | `BestOfArgs` / `BestOfPickArgs` / `BestOfCompareArgs` | `bestOfPick`: audit `winner` |
| `feedback` | Args | `FeedbackArgs` | |
| `broadcast` | Args | `BroadcastArgs` | |
| `checkpointDiff`, `restoreCheckpoint`, `forkCheckpoint` | Args | `CheckpointArgs` | |
| `switchAgent` | Args | `SwitchAgentArgs` | |
| `dev` | Args | `DevArgs` | audit `on` |
| `devFreePort` | Args | `DevFreePortArgs` | |
| `paneClose` / `paneResize` | Args | `PaneCloseArgs` / `PaneResizeArgs` | |
| `sessionTask` | Session, Args | `ReportArgs` | |
| `planDecide` | Session, Args | `PlanDecideArgs` | the human plan verdict; audit names `approve` only, never the comment |
| `agentFeed`, `agentFeedOutput`, `talkState`, `agentChoose`, `agentStop`, `agentQueueRelease`, `agentSetting` | Args | matching `Agent*Args` / `TalkStateArgs` | |
| `agentSay` | Args | `AgentSayArgs` | decode, `Conn = ""`, re-marshal; dropped `conn`; audit `stopFirst` |
| `sshRecall` | Args | `SSHRecallArgs` | |
| `sshAnswer` | Args | `SSHAnswerArgs` | (element `Conn` rule is in `handleRemote`, §8.7: `remote` cannot see the element) |
| `reviewPackages` | Session | — | |
| `reviewPackageDiff` | Session, Args | `ReviewDiffArgs` | |
| `reviewPackageImage`, `reviewPackageMark` | Session, Args | `ReviewImageArgs` / `ReviewMarkArgs` | |
| `reviewPackageChecks`, `reviewPackageRecover`, `reviewPackageVerify` | Session, Args | `ReviewVerifyArgs` | |
| `reviewPackageCapture` | Session, Args | `ReviewCaptureArgs` | |
| `reviewPackageFeedback` | Session, Args | `ReviewFeedbackArgs` | |
| `sessionTimeline` | Args | `SessionTimelineArgs` | |
| `relayCred` | Args | `RelayCredArgs{Holds string \`json:"holds,omitempty"\`}` (spec 03) | — (self-only by construction, §8.3) |
| `watch`, `unwatch`, `attachImage`, `attachmentImage` | spec 02 | spec 02 | spec 02 (the `attachImage` check also returns `attachBytes`/`attachLast`, §7) |

Every `check` that rejects uses a fixed sentence (no echo of the payload). A dropped field is
named in the audit line (`dropped=conn`); the request proceeds. `endedSessions` and
`reviewPackages` copy no Args because their handlers read none (`CmdReviewPackages` reads only
`req.Session`, `internal/daemon/reviewpackage.go`); giving them Args later is a reviewed change
to this table.

**Args drift guard.** `TestRemoteArgsFieldsAreReviewed` holds a golden
`map[cmd][]string` of the JSON field names of each `fieldSpec.args` type (via `reflect`, nested
structs flattened with `.`). Adding a field to any remotely reachable Args type fails it until a
human adds the name — the M2 promise that "a future handler that starts reading a new field" is
reviewed, not inherited.

### 8.6 `sessionTimeline` (new, read)

```go
// internal/protocol/sessiontimeline.go
const CmdSessionTimeline = "sessionTimeline"
type SessionTimelineArgs struct {
    Session string `json:"session"`
    Conn    string `json:"conn,omitempty"`
    After   int64  `json:"after,omitempty"`
    Limit   int    `json:"limit,omitempty"`
}
// Reply: SSHTimelineData — the SAME type, so the desktop Conversation components mount on
// the phone unchanged; removed fields are zero values.
```

Handler `internal/daemon/sessiontimeline.go`:

```go
func (d *Daemon) handleSessionTimeline(a protocol.SessionTimelineArgs) (protocol.SSHTimelineData, error) {
    data, err := d.handleSSHTimeline(protocol.SSHTimelineArgs{Session: a.Session, Conn: a.Conn, After: a.After, Limit: a.Limit})
    if err != nil { return protocol.SSHTimelineData{}, err }
    return publicTimeline(data), nil
}
```

`publicTimeline` builds a NEW value by ALLOWLIST copy (a field added to these types later is
absent until added here — `TestPublicTimelineIsAnAllowlist` pins the kept set by reflection):

| Type | Kept | Removed |
|---|---|---|
| `SSHConnInfo` | `ID`, `Session`, `Alias`, `Label`, `Tier`, `Entry`, `Sandboxed`, `State`, `Wheel`, `WheelSince`, `Why`, `Scope`, `RequestedAt` | `Host`, `User`, `ResolvedUser`, `ResolvedHostname`, `Cwd`, `TermCwd`, `TermTracked`, `TermAtPrompt`, `TermElevated`, `Tab`, `TabAlive`, `StateDetail`, `Prompt` (the shell's sign-in / secret prompt card) and every other field |
| `SSHTimelineItem` | `ID`, `Kind`, `Order`, `Seq`, `At`, `Block`, `Element`, `System`, `Human` | an item whose `Block.Private` is true is DROPPED entirely (the `Seq` gap is fine; clients page by `LastSeq`) |
| `SSHBlock` | everything except | `Host`, `User`, `Cwd` |
| `SSHElement` | everything except | `Cwd`, `HTML`, `Data` (the agent-authored widget document and its data: a phone renders the element's text fields only, until spec 04 proves widget isolation on the phone, §10.1) |
| `SSHSystemEvent` | all | — |
| `SSHHumanMessage` | everything except | `Transcript` (the handback transcript of the human's terminal) |
| `SSHTimelineData` | `Session`, `Items`, `LastSeq`, `More`, `Connections` (filtered as above) | — |

Prompt answers (`sshPromptAnswer` secrets) are memory-only and never in the store; the test
asserts none appears. The `timeline:<session>` topic of spec 02 MUST serve `publicTimeline`
output, never `handleSSHTimeline`'s.

Widgets are dropped rather than forwarded because the desktop's isolation (`sandbox="allow-scripts"`,
`data:` documents, one `MessageChannel` port, `desktop/runtimeguard.go`) does not exist in the
Capacitor client: any render outside an isolated frame would hand agent JavaScript the transport
plugin and every tier the device holds. Re-adding `HTML`/`Data` is a change to this table AND to
the AGENTS rule in §10.1, gated on spec 04's isolation test.

Locally (unix socket) `sessionTimeline` is answered too; the desktop keeps using `sshTimeline`.

### 8.7 `handleRemote` (`internal/daemon/remotewire.go`)

```go
func (d *Daemon) handleRemote(ctx context.Context, req protocol.Request) protocol.Response {
    if slices.Contains(protocol.LocalOnlyCommands, req.Cmd) || remote.TierFor(req.Cmd) == protocol.TierNever {
        d.logf("", "remote: handleRemote refused %s (tier gate bypassed?)", req.Cmd)
        return protocol.Response{OK: false, Error: "not available remotely"}
    }
    if req.Cmd == protocol.CmdSSHAnswer && !d.remoteMayAnswer(req) {
        return protocol.Response{OK: false, Error: "this question is about an SSH host; answer it on the Mac"}
    }
    if req.Cmd == "sessions" { /* existing SSH strip */ }
    return d.handle(ctx, req)
}
```

Defence in depth: reaching the first branch is a bug in `internal/remote`, so it logs.

`remoteMayAnswer` decodes `SSHAnswerArgs`, reads the session's Conversation store
(`d.sshStore(a.Session, false)`) and returns true only when the item exists, is an element, and
its `Conn == ""`. An element scoped to an SSH connection is an SSH human act and stays on the
desktop; anything it cannot read (no store, unknown element) returns false. The handler re-checks
the revision as for the desktop, so the two reads cannot disagree about which element is
answered.

### 8.8 Audit

Replaces `mutatingCommands`/`auditable`. A req frame is audited iff its tier ≠ `read`, whether
it succeeded, failed, or was refused for tier/field reasons. Format (one line, `logSafe` on every
string, no payload, no `Text`, no Args values except the `audit` fields which are bools/enums):

```
remote: audit device=<id> name=<name> via=<lan|relay> tier=<tier> cmd=<cmd> session=<session|-> ok=<true|false> code=<refusal code|-> fields=<k=v,…|-> dropped=<f,…|->
```

Additionally, once per (connection, pane): `remote: audit device=<id> name=<name> via=<via> tier=shell pty-write pane=<pane>`.

Local device commands log (daemon side, `pairing.go`):
`devices: revoke device=<id> name=<name> closed=<n> saved=<file|journal|memory>`, `devices: tiers device=<id> tiers=<t,…>`,
`devices: paired device=<id> name=<name> tiers=<t,…>`.

### 8.9 What does NOT change

Every phone command goes to the same `d.handle` case as the desktop's, so `typeToAgent`, the
idle gate, `lockHumanPrompt`/`lockHumanMessage`, `paneWaitingNow`, `typedNote`, the
feedback/broadcast queues, `agentChoose`'s hash re-read and `agentStop`'s debounce all apply
unchanged. A phone send is a human send. Raw `pty` write is the terminal (as the desktop's
`TermService.Write`), not a send, and is therefore `shell`.

---

## 9. CLI and desktop clients

### 9.1 `lola pair` (`paircmd.go`, repo root; registered in `main.go`)

```
lola pair [--no-shell] [--tiers read,answer,control,destructive,shell] [--name NAME] [--json]
```

1. `pairBegin`. `Problem` → print it, exit 1.
2. Print the QR of `Payload` (`internal/qr`, ECC level M, quiet zone 4 modules, two module rows
   per text line), then one line `Scan with Lola on your phone. Expires in 5:00.` Rendering:
   - **Default (TTY, `NO_COLOR` unset):** the whole block, quiet zone included, is wrapped in
     `\x1b[30;47m` … `\x1b[0m` per line (black foreground on white background), so the result
     does not depend on the terminal theme. Glyphs draw DARK modules in the foreground: top dark +
     bottom dark → `█`, top only → `▀`, bottom only → `▄`, neither → space.
   - **`NO_COLOR` set:** no SGR. Glyphs draw LIGHT modules (`█` = both light, `▀` = top light,
     `▄` = bottom light, space = both dark), which reads correctly on a dark-background terminal.
     On a light-background terminal the code is inverted; the `--json` payload is the way out.
   - **Width check:** columns needed = modules + 8 (quiet zone). Terminal width =
     `term.GetSize(os.Stdout.Fd())` (`github.com/charmbracelet/x/term`, already a dependency),
     else `$COLUMNS`, else 80. Needed > width → no QR; print
     `Widen the terminal to <n> columns to show the code, or run lola pair --json.` and keep
     waiting (the window is open). `MaxPairPayloadBytes` (1024) needs up to version 30 at level
     M (137 modules → 145 columns), so a relay+LAN payload usually does not fit 80 columns.
   - The payload text is printed only with `--json` or when stdout is not a TTY (simulator dev
     links read it there).
3. Long-poll `pairStatus{pairing, since, waitMs: 25000}` until the state changes.
4. On `confirming`, print one line naming the device (suffixed ` (via relay)` when
   `Via == "relay"`), prefixed with `New device: ` when
   `NameClash` or `Replaces` is set (`Replaces` also prints `(re-pairs <id>)`):
   `"<DeviceName>" wants to connect. Type the 6-digit code shown on the phone (empty line rejects): `
   Read one line from stdin. Empty → `pairReject`. Otherwise `pairConfirm{pairing, code: <line>, tiers}`;
   a "does not match" error prints `That code does not match; <n> tries left.` and reads again;
   any other error or a terminal state prints it and exits 1. Print `Paired <name>.` and exit 0
   on `paired`. The CLI never prints the code; it never reaches the CLI.
5. SIGINT at any point → `pairReject` (bounded 2 s), exit 130.

`--json` emits one JSON object per line: `{"event":"begin",…PairBeginData}`,
`{"event":"status",…PairStatusData}`; it still reads the code line from stdin. Nothing is ever
confirmed without a code line on stdin.

Tiers: default `protocol.DefaultTiers`; `--no-shell` removes `shell`; `--tiers` replaces the set
(must include `read`). Never logs the payload.

### 9.2 `lola devices`

```
lola devices                         table: NAME  ID  TIERS  LAST SEEN  CONNECTED
lola devices revoke <id|name>        revokeDevice; prints "Revoked <name>; closed <n> connection(s)."
lola devices tiers <id|name> <t,…>   setDeviceTiers; prints the new set
lola devices --json                  DevicesData
```

`<name>` resolves client-side against `devices` and must match exactly one entry, else exit 1
naming the candidates' ids. `Problem` (broken registry) → print it, exit 1.

### 9.3 Desktop `PhonesService` (`desktop/phonesvc.go`)

New bound service (registered in `desktop/main.go`), thin over the socket like `SSHService`:

```go
func (s *PhonesService) Begin() (protocol.PairBeginData, error)                       // 5s timeout; no args (AllowLoopback is CLI/dev only)
func (s *PhonesService) Status(a protocol.PairStatusArgs) (protocol.PairStatusData, error) // timeout = WaitMs + 5s
func (s *PhonesService) Confirm(a protocol.PairConfirmArgs) (protocol.PairStatusData, error)
func (s *PhonesService) Reject(pairing string) (protocol.PairStatusData, error)
func (s *PhonesService) Devices() (protocol.DevicesData, error)
func (s *PhonesService) Revoke(id string) (protocol.RevokeDeviceData, error)
func (s *PhonesService) SetTiers(id string, tiers []string) (protocol.DeviceInfo, error)
```

`Confirm` takes the code the human TYPED; `PairStatusData` carries no code to display. Spec 04's
confirm sheet is a 6-digit input with the device name ("New device" badge on `NameClash` /
`Replaces`, "via relay" on `Via == "relay"`), Reject, and the tier toggles; it never renders a
code.

Bindings regenerated (`desktop/frontend/bindings/...`). The Settings → Phones UI is spec 04's;
this spec only removes the dead Remote-tab connect panel, the regenerate-key button and the
`insecure_lan` toggle from `SettingsForm.svelte` (+ test) so the frontend compiles. The same
build item adds spec 03 §12.1's `SettingsDTO.RemoteRelay` to `desktop/configsvc.go` (it owns that
file); spec 04's Phones panel later renders its checkbox.

---

## 10. Build, CI, docs

- `Makefile`:
  ```make
  # Install the daemon binary this machine runs (PATH), from the working tree. Not live until restart.
  daemon:
  	GOCACHE=$(GOCACHE) GOFLAGS='$(GOFLAGS)' go install .
  # The same, then run it in the foreground.
  daemon-dev: daemon
  	lola run
  ```
  (use the Makefile's existing `GOCACHE`/`GOFLAGS` variables). Remove `mobile-dev`, `mobile-lan`,
  `mobile-info` and the M1 comment block. `mobile-sim`/`mobile-device` stay (spec 04 owns them).
- `ci.yml`: delete the `lola_insecure` step. Nothing replaces it: the listener is in the default build.
- `config.example.toml` `[remote]`: drop `insecure_lan`; state that pairing is `lola pair` or
  Settings → Phones, the listener needs `bind = "lan"` (or an IP) for a direct path, and each
  phone has its own key and permissions.
- `README.md`: add `lola pair` and `lola devices` rows to the command table; `[remote]` table
  loses `insecure_lan`.
- `internal/doctor`: one row `phones` — fails when `devices.json` or `devices.revoked` is
  unreadable (`Registry.Err`) or a revocation is unsaved (`Registry.Unsaved`), with `Fix` id
  `phones_registry` (snake_case like every FixID; `FixPhonesRegistry` in `internal/doctor/fix.go`,
  added to `AllFixIDs`; message names the file, never its contents). `desktop/doctorsvc_test.go`
  `TestEveryDoctorFixHasAnAppAction` then requires a `phones_registry:` entry in
  `desktop/frontend/src/lib/components/SetupChecklist.svelte` `FIXES` (action: open Settings →
  Phones, which shows the problem sentence); W11 owns that line.

### 10.1 AGENTS.md text (exact replacements)

Architecture map, **Core**, replace the `protocol` bullet with:

> - `protocol` — wire types. `remote` — the phone listener: mutual TLS 1.3, device lookup,
>   pairing, capability tiers; `devices` — the paired-phone registry (`~/.lola/devices.json`);
>   `qr` — byte-mode QR for `lola pair`; `mdns` advertises the listener; `panebus` multiplexes
>   one tmux attach per pane across subscribers.

New section after **SSH**:

> ### Phones (`internal/remote`, `internal/devices`; spec `docs/mobile/01-pairing-and-tiers.md`)
> - **A phone is a second HUMAN surface, identified by its key.** The listener is mutual TLS 1.3
>   (`RequireAnyClientCert`, P-256 only); the client SPKI hash is looked up in
>   `~/.lola/devices.json`. Nothing on the wire names a device. An unknown key fails the
>   handshake unless a pairing window is open. No build tag gates the listener. A client counts
>   a connection as authenticated only on the `ready` frame (TLS 1.3 finishes before the server
>   judges the client certificate); post-handshake refusals send `not_paired`.
> - **Pairing is local, one-shot, and the human types the code.** `protocol.LocalOnlyCommands`
>   (pair*, devices, revokeDevice, setDeviceTiers) are tier `never`, refused again in
>   `handleRemote`, absent from the hook bridge. The secret lives 5 min and is burned only by a
>   VALID proof (a wrong one closes only that connection and never moves the pairing, so a
>   stranger on the LAN cannot cancel pairings). The daemon never displays the 6-digit code:
>   `pairConfirm` must carry the code the human read on the phone (`maxConfirmTries`). An
>   unpaired connection may send exactly one frame: `remote.pair`. The payload, secret and
>   relay `tok` are never logged. The pairing connection closes after its outcome; the phone
>   reconnects as a paired device.
> - **`internal/remote/tiers.go` is a closed allowlist.** Every command `d.handle` routes maps to
>   exactly one of read / answer / control / destructive / shell / never
>   (`TestEveryHandledCommandHasATier` parses `server.go`). A new command is unreachable until a
>   human writes it into a tier. Unknown = never. `pty` write is `shell`. What the tiers mean on
>   the Mac: `answer` can approve an agent's permission card, which runs what the agent proposed;
>   `control` starts configured commands (`dev_commands`, `post_create`) inside trees the agent
>   can write; `destructive` removes sessions (`kill`, `closeExitedTemporary`, `bestOfPick`);
>   `shell` is raw terminal input and every command line or program the phone or the agent
>   chose (`open`, `openManual{agent:false}`, `reviewPackageVerify`, `reviewPackageCapture`).
>   Turning `shell` off is not a guarantee that no code runs. Anything that runs an arbitrary
>   command line, or agent-writable code chosen by the request, is `shell`, never `control`.
> - **`enable`/`disable` (one poll's `enabled`) are the only config writes a phone has; no other
>   config write may be tiered.**
> - **Requests are rebuilt, not forwarded** (`fields.go`): only each command's listed fields
>   survive; `Force` is never carried; a phone opens worktrees only and needs `shell` for a
>   shell; `agentSay.Conn` and `prs.refresh` are dropped. A new field on a reachable Args type
>   fails `TestRemoteArgsFieldsAreReviewed` until reviewed.
> - **Same handlers, same gates.** A phone send is a human send through `typeToAgent`,
>   `sanitizeAgentText` (a leading `!` is defused on every surface), `lockHumanPrompt`/
>   `lockHumanMessage` and `typedNote`. Nothing new types into an agent.
> - **Revocation is immediate and survives restarts.** Every frame re-reads the device (id +
>   epoch); `revokeDevice` also closes its live connections and drops its relay rendezvous at
>   the hub (registry subscriber → `hubRelayDirty`); re-pairing a key bumps its epoch; tier
>   edits apply on the next frame and close the device's live connections, so its next
>   `ready` carries the new tiers. `devices.json` is 0600 and atomic; an unparseable one
>   authenticates nobody and is never rewritten. A revoke that cannot rewrite it is journaled
>   in `devices.revoked`; one that cannot write either keeps the listener and relay off for the
>   run and fails `lola doctor`'s `phones` row.
> - **Unauthenticated peers cannot crowd out paired phones.** Slots are taken at accept in a
>   pre-auth pool capped globally and per source IP (relay has its own pool, spec 03); a
>   connection moves to the authenticated pool only after `Authenticate`. Per device, the OLDEST
>   connection is evicted when a new one authenticates past `maxConnsPerDevice`.
> - **External APIs are budgeted per device.** Commands that call `gh` or Linear on the request
>   path (`extCommands`) take an extra `extRate` bucket; `tickets` is cached like `prs`.
> - **SSH stays on the desktop.** Phones read it only through `sessionTimeline`, an allowlist
>   copy with no Private blocks, prompt cards, host/user/cwd, handback transcript or widget
>   document; `sessions` strips `SSH`. Conversation commands are `protocol.TalkCommands`
>   (including `sshRecall` and `sshAnswer`, which act on any session's Conversation store; an
>   answer to an element scoped to an SSH connection is refused in `handleRemote`), not ssh
>   commands.
> - **Widgets never run on a phone.** `sessionTimeline` drops `SSHElement.HTML`/`Data`. Adding
>   them back requires the phone to render widgets only in a sandboxed `data:` iframe that the
>   native bridge is not injected into (`WKUserScript` `forMainFrameOnly`), proven by a spec 04
>   test, and a change to this rule.
> - **A phone opens URLs itself, and only http(s)** to a `dev_forward` address or the URL's own
>   host; `openURL` (which opens a browser on the Mac) is tier `never`. Untrusted pane text never
>   becomes a non-http(s) open on either surface.
> - Every non-read remote command writes one `remote: audit` line naming the device, never the
>   payload. Rate limits are per device, shared across its connections.

**SSH** section, replace "bridge and phones get no human commands" with:

> (`SSHAgentCommands` vs `SSHHumanCommands`; the bridge and phones get no ssh human command —
> they are tier `never`; Conversation commands, including `sshRecall`/`sshAnswer`, are
> `protocol.TalkCommands`)

**Team hub**, replace "are denied by `internal/remote` as a list" with "are tier `never` in
`internal/remote/tiers.go`".

**Teardown, dev sessions, ports**, in the "Dev URLs" bullet replace "opened via `cmd=openURL`
(http(s) only), never `window.open`" with "opened via `cmd=openURL` (http(s) only) on the Mac,
never `window.open`; a phone opens them itself under the Phones rule".

**Desktop app**, replace "Bound services: Talk, SSH, Daemon, Term, Config, Context, Doctor,
Linear, Update." with "Bound services: Talk, SSH, Daemon, Term, Config, Context, Doctor,
Linear, Update, Phones."

**Testing**: append "Pairing in a simulator or E2E run is confirmed with `lola pair` (the
code line on stdin, taken from the simulator: spec 04's DEBUG build logs the code, release
builds never do), never by driving the desktop UI."

---

## 11. Tests

`internal/devices` (`devices_test.go`, `$LOLA_HOME = t.TempDir()`):
- `TestOpenMissingIsEmpty`, `TestSaveIs0600AndAtomic` (no temp left, mode 0600)
- `TestUnparseableFailsClosedAndIsNotRewritten` (bytes identical after every mutator), `TestUnknownVersionFailsClosed`, `TestDuplicateSPKIFailsClosed`, `TestDuplicateRVFailsClosed`
- `TestAddSameSPKIReplacesAndBumpsEpoch`, `TestAddValidatesName`, `TestMaxDevices`, `TestIDIs17Chars`
- `TestSetTiersRequiresRead`, `TestUnknownTierInFileDropped`, `TestMalformedRelayRecordDropped`, `TestRelayRegexesMatchHubproto`
- `TestRevokeUnknown`, `TestRevokeJournalsWhenFileUnwritable` (devices.json read-only → line in
  devices.revoked; re-`Open` hides the device), `TestRevokeJournalAppliesOnlyUpToItsEpoch`
  (re-paired key with a higher epoch is visible), `TestRevokeSuccessTruncatesJournal`,
  `TestRevokeUnsavedWhenBothUnwritable` (memory drops it, `Unsaved()` non-nil, next good save clears it),
  `TestUnparseableJournalFailsClosed`
- `TestTouchPersistsAtMostEvery`, `TestSubscribersRunOutsideLock`, `TestLookupRV`

`internal/protocol`:
- `TestPairingVectors` (proof and code from `testdata/pairing_vectors.json`; the payload round
  trip incl. `relay.tok` is `internal/remote`'s `TestPairPayloadVectors`)
- `TestTalkAndSSHListsAreDisjoint`, `TestLocalOnlyCommandsNamed`, `TestPairStatusHasNoCodeField`
  (reflection: no field of `PairStatusData`/`PairBeginData` is named or tagged `code`)

`internal/qr`:
- port of `desktop/frontend/src/lib/qr.test.ts` against published constants (format/version
  strings, RS generator polynomials, capacity table); `TestMatchesTSVectors` reading
  `internal/qr/testdata/vectors.json` (matrices the TS encoder produced for 4 payloads)
- `TestTerminalSGRDarkModules` (default mode wraps every line in `\x1b[30;47m`…`\x1b[0m` and
  maps dark modules to `█▀▄`), `TestTerminalNoColorInverts`, `TestTerminalTooNarrow`
  (version-30 payload at 80 columns → the "Widen the terminal" line, no QR)

`internal/remote`:
- tiers: `TestTierTableIsClosed` (values ⊆ Tiers ∪ {never}), `TestEverySSHCommandIsDenied`,
  `TestHubHumanCommandsAreDenied`, `TestLocalOnlyCommandsAreNever`, `TestTalkCommandsAreTiered`
  (incl. `sshRecall`/`sshAnswer` → answer), `TestRemotePrefixIsNever`, `TestFrameTierPTYActions`,
  `TestBestOfPickIsDestructive`, `TestCommandsThatExecAreShell` (`open`, `reviewPackageVerify`,
  `reviewPackageCapture`, `shellCreate`)
- fields: `TestEveryTieredCommandHasAFieldSpec`, `TestRemoteArgsFieldsAreReviewed`,
  `TestRebuildNeverCarriesForceOrHookFields`, `TestRebuildDropsUnlistedFlatFields`,
  `TestEnvelopeCmdWins`, `TestOpenManualLocationRefused`, `TestOpenManualShellNeedsShellTier`,
  `TestAgentSayConnDropped`, `TestPrsRefreshDropped`, `TestRelayCredIsReadWithFieldSpec`
- auth (real TLS on loopback, generated client certs):
  `TestUnknownCertRejectedAtHandshake`, `TestNonP256CertRejected`, `TestTwoCertChainRejected`,
  `TestPairedCertAuthenticates`, `TestReadyIsFirstFrame`, `TestNotPairedFrameOnPostHandshakeRefusal`
  (revoke between handshake and `Authenticate`), `TestRevokeClosesLiveConn`,
  `TestRepairBumpsEpochClosesOldConn`, `TestTierDowngradeAppliesNextFrame`,
  `TestBrokenRegistryAuthenticatesNobody`, `TestUnsavedRegistryRefusesListen`,
  `TestRelayRVMustMatchDevice` (a `relayed` conn with another device's rv → `not_paired`),
  `TestRelayedCandidateNeedsWindowRV`, `TestRefusalLogIsGloballyThrottled` (10,000 source
  addresses → bounded lines, no per-address state)
- pairing: `TestPairHappyPathThenReconnect` (outcome `resp`, close, reconnect gets `ready`),
  `TestPairCodeMatchesBothSides`, `TestPairBadProofDoesNotBurn` (window stays `waiting`; a
  second candidate with the right proof then pairs), `TestPairBadProofsNeverFailThePairing`
  (a flood of wrong proofs, then the real phone pairs), `TestSilentCandidatesHoldNoPairingSlot`, `TestPairValidProofBurnsAndClosesWindow`,
  `TestPairCandidateWhileConfirmingIsRefusedWithoutStateChange`, `TestPairNoFrameDoesNotBurn`,
  `TestUnpairedMaySendOnlyRemotePair` (sub, pty, other req → closed), `TestPairExpiredWaiting`,
  `TestPairConfirmTimeout`, `TestPairRejected`, `TestPairConfirmWrongCodeCountsDown`
  (`TriesLeft` 3→2→1, third mismatch → `failed(code_mismatch)`), `TestPairConfirmStripsSpaces`,
  `TestPairPhoneLeft`, `TestPairWatcherIsTheOnlyReader` (race detector: frame sent at the
  instant of confirm is discarded, no second reader), `TestPairBeginCancelsOpen`,
  `TestPairedDeviceRemotePairIsUnknownCmd`, `TestPairStatusLongPollWakes`,
  `TestPairNameClashAndReplaces`, `TestPairPayloadRoundTripAndBounds`,
  `TestPairPayloadRejectsLola1`, `TestPairPayloadVectors`, `TestSetWindowRelayOnlyWhileWaiting`,
  `TestUnpairedSlotsPerSource` (two concurrent candidates from one IP pass, a third →
  `not_paired`), `TestUnpairedSlotsReleasedOnFailure` (three sequential failed
  candidates, then a fourth still gets a slot), `TestPairConfirmRelayRecordWritten`
- transport: `TestRefusalKeepsConnection`, `TestStrikeLimitCloses`,
  `TestRateLimitSharedAcrossDeviceConns`, `TestMaxConnsPerDeviceEvictsOldest`,
  `TestUnauthenticatedFloodCannotLockOutPairedDevice` (fill the pre-auth pool from several
  loopback source addresses; a paired device's existing conn keeps working and, once one
  flooding socket times out, a new paired conn authenticates; per-IP cap refuses a third socket
  from one address), `TestPreAuthSlotMovesToAuthedPool`, `TestPtyWriteNeedsShell`,
  `TestPtyBytesRateLimited`, `TestExtRateLimitsExternalCommands`,
  `TestExtCommandsAreTheExternalOnes`, `TestAttachPartsChargeBytesNotWrites`
- audit: `TestAuditLineNamesDeviceNeverPayload`, `TestAuditRecordsDroppedAndRefused`,
  `TestReadIsNotAudited`, `TestPairAuditNeverCarriesSecret` (secret, proof, tok and code absent)

`internal/daemon`:
- `TestEveryHandledCommandHasATier` (`go/parser` over `server.go`: every `case` label of
  `(*Daemon).handle`, `protocol.X` selectors resolved by parsing `../protocol/*.go` const decls;
  each must be a key of `remote.CommandTiers()` — an exported copy of the map for tests)
- `TestTierTableNamesOnlyRealCommands` (every key is a handled case, a
  `protocol.LocalOnlyCommands` entry, a `remote.TransportCommands` entry (`watch`, `unwatch`),
  or `remote.pair`)
- `TestSetDeviceTiersDropsConnections`
- `TestHandleRemoteRefusesLocalOnly`, `TestPairCommandsNotOnBridge`,
  `TestHandleRemoteRefusesSSHScopedAnswer`, `TestRemoteRecallTakesBackQueuedMessage`
- `TestPairBeginProblemWhenListenerOff`, `TestPairBeginLoopbackOnlyProblem` (default
  `bind = "localhost"`, no relay → `Problem`, no window), `TestPairBeginAllowLoopback`,
  `TestPairBeginNonLoopbackFirst`, `TestPairBeginNeverLogsPayload`,
  `TestPairConfirmWritesRegistryAndAudits`, `TestRevokeDeviceDropsConnections`,
  `TestRevokeNotifiesRelay` (subscriber fires `hubRelayDirty` stub), `TestRevokeUnsavedStopsListener`,
  `TestDevicesReportsConnected`, `TestRegistrySurvivesListenerReload`,
  `TestNewDaemonHasRegistryAndPairer` (`newDaemon` + `startRemote`, no `Run`)
- `TestTicketsCachedPerProjectAndScope`
- `TestSessionTimelineStripsPrivateAndConnDetails`, `TestSessionTimelinePagingMatchesSSH`,
  `TestPublicTimelineIsAnAllowlist` (incl. `SSHElement.HTML`/`Data` absent),
  `TestSessionTimelineHasNoPromptAnswer`
- `TestUntaggedBuildListens` (replaces `remotewire_off_test.go`)
- `!` lines: no new test. The phone path is the desktop's path, already pinned by
  `TestSanitizeNeutralizesBang` (`talkgate_test.go:300`), `TestAnswerSendsBangAsText`
  (`talkgate_test.go:370`) and `TestTalkITSayBangIsDefused` (`talk_it_test.go:604`).
- updated: `TestTalkCommandsAreHumanOnly`, `TestSSHBridgeAdmitsAgentVerbsOnlyWithAValidToken`,
  `TestSSHEveryCommandIsHandled`; `remotewire_integration_test.go`, `mdnswire_test.go` untagged;
  the five W5e files (§13)
- `internal/config`: `TestInsecureLANKeyIgnored`
- `internal/doctor`: `TestPhonesRowFailsOnBrokenOrUnsavedRegistry`
- root: `TestPairCmdConfirmsWithTypedCode`, `TestPairCmdEmptyLineRejects`,
  `TestPairCmdRetriesWrongCode`, `TestPairCmdNeverPrintsCode`, `TestPairCmdRejectsOnSIGINT`,
  `TestDevicesCmdResolvesName`
- desktop: `TestPhonesServiceRoundTrips` (fake socket), `configsvc_test.go` loses the M1 cases,
  `TestEveryDoctorFixHasAnAppAction` passes with `phones_registry`

Gate: `make check` (no tag anywhere), `go tool nm $(command -v lola) | grep deviceAuthorizer`
after `make daemon`.

---

## 12. Deviations from the plan

1. **`reviewPackageVerify` and `reviewPackageCapture` are `shell`, not `control`.**
   `reviewPackageVerify` runs `Spec.Command` (`goalpolicy.Check`,
   `internal/reviewverify/verify.go` `Reserve`), a free command line, against the worktree.
   `reviewPackageCapture` runs `node -e <script>` with `cmd.Dir = s.Worktree`
   (`internal/reviewcapture/capture.go` `runNode`, called from
   `internal/daemon/reviewcapture.go` `CaptureInto`), so `require` resolves the browser helper
   from the agent-writable `node_modules`: host execution of agent-controlled code on an
   unsandboxed checkout. It also fetches any http(s) URL the caller names (including intranet
   and loopback services) and the PNG is readable through `reviewPackageImage`; as `shell` that
   is no more than a shell holder can already do with `curl`. Smallest fix: the tier entries.
   `reviewPackageRecover` stays `control`.
2. **`open` is `shell`; `openManual`/`openExisting` are `control` with field rules.** `open`
   always creates a shell; `openManual` with `agent:false` creates one too, and its
   `location`/`path` (`temporary`, `checkout`, `main`) open an agent or shell in an arbitrary
   or main checkout. Plan §6 says "open … worktree", so a phone gets `location` "" / `worktree`
   only, and `agent:false` needs `shell`.
3. **`openURL` is `never`.** It opens a URL in the Mac's browser (`handleOpenURL`); the plan's
   "open URL" is the phone opening a dev URL itself. The shim (spec 04) maps `store.openURL` to
   the phone's browser through `dev_forward`, http(s) only (AGENTS rule in §10.1). Allowing it
   would only let a phone pop windows on an unattended Mac.
4. **`agentSay.conn` is dropped for phones.** Setting it makes `sshSpeakFrom` hand the SSH wheel
   back in that tab — an ssh human act, which the plan keeps on the desktop.
5. **`reviewPackageFeedback` → `answer`, `bestOfCompare` → `read`, `closeExitedTemporary` →
   `destructive`, `endedSessions` → `read`, `attachmentImage` → `read`.** The plan's table did
   not name them; each follows from what the handler does.
6. **Commands outside plan §6 are `never`**: `plan`, `planApply`, goals, knowledge, review
   profiles, comparison. Adding any later is a one-line tier edit plus a field spec.
7. **`enable`/`disable` are config writes** (plan says config writes are never, and also lists
   them under control). Kept in `control` as listed; they flip one poll's `enabled` and nothing
   else. §10.1 states it as an AGENTS rule so it is not taken as a precedent.
8. **A refusal no longer closes the connection** (plan §5 implies it); bounded by
   `maxRefusalsPerConn = 64`. Framing errors still close.
9. **The human TYPES the code; the desktop never shows it.** Plan §3.1 has the desktop show the
   code with Confirm / Reject. A displayed code plus a button proves no comparison happened, and
   the photographed-QR race (§6.4) is only stopped if the human must reproduce what the real
   phone shows. `PairConfirmArgs.Code` is the typed code; `PairStatusData` has no code field;
   `maxConfirmTries = 3`.
10. **The secret burns only on a VALID proof.** Plan §3.1 says "burned on first presentation,
    success or not". Any peer that can reach the port (LAN, a sandboxed container on a `lan`
    bind, a hostile relay) can complete the handshake with a throwaway certificate while a
    window is open, so burn-on-any-content let anyone cancel every pairing. A wrong proof
    reveals nothing about a 32-byte secret; it now closes only its own connection. A per-pairing
    cap on wrong proofs was the same attack at five connections, so it is an alert line only.
11. **`!` lines are not available on any surface.** Plan §6 lists "`!` line" under Speak.
    `sanitizeAgentText` (`internal/daemon/autoprompt.go`) gives every human and automated send a
    leading space before a `!`, because in Claude Code's composer a leading `!` runs the rest on
    the Mac with no permission prompt and no hook; `TestTalkITSayBangIsDefused`,
    `TestSanitizeNeutralizesBang` and `TestAnswerSendsBangAsText` pin it. The parity item is
    dropped: a phone cannot run a `!` line, and neither can the desktop's Conversation.
12. **The pairing connection closes after its outcome.** Continuing it as the authenticated
    session would leave the pairing watcher and the frame loop reading one stream; one reconnect
    at pairing time is cheaper than that hand-off. The plan's reconnect budgets (§8) are about
    foreground and path changes, not pairing.
13. **`sshRecall` and `sshAnswer` move to `TalkCommands` at `answer`.** Plan §3.2 makes every
    ssh command `never`, but the desktop's Conversation uses `SSHService.Recall` (plan §6
    "recall") and `SSHService.Answer` (question elements) on every session, not only SSH ones
    (`desktop/frontend/src/lib/ssh.svelte.ts:1135`, `:1167`). An answer to an element scoped to
    an SSH connection (`Element.Conn != ""`) is refused in `handleRemote`. `sshWidgetStatus`
    stays `never` (phones get no widgets, Deviation 14); `sshProposal` stays `never` (it runs a
    command on a host). Spec 04 hides the widget-fault and proposal controls.
14. **Widgets are not sent to phones.** `sessionTimeline` drops `SSHElement.HTML`/`Data` until
    spec 04 proves the phone renders them in an isolated frame (§8.6, §10.1).
15. **`bestOfPick` is `destructive`.** Plan §3.2 lists `bestOf*` under control, but the pick
    kills the losing attempts and removes their clean worktrees and branches
    (`internal/daemon/bestof.go` `discardAttempt`). `bestOf` stays `control`, `bestOfCompare`
    is `read`.
16. **Connection pools split and per-device eviction keeps the newest.** The plan says only
    "request concurrency stays bounded". An untagged LAN listener with one accept-time pool let
    any unauthenticated peer lock out paired phones (§5.1).
17. **The QR's `relay` carries `tok`.** Plan §3.1 shows `relay?: {url, rv}`; a phone whose first
    connection is relayed must present a credential to the hub before anything else exists.
    `tok` is random (not derived from the secret), so the pairing secret never reaches the hub
    in any form.
18. **`setDeviceTiers` closes the device's live connections.** The plan has tiers "editable on
    the desktop"; a phone learns its tiers only from `ready`, so a change reconnects it. No
    pushed device topic exists (one fewer stream, one fewer native-only frame on the phone).

---

## 13. Work items

### 13.0 Scheduling across specs

**`docs/mobile/00-build-plan.md` is authoritative** for order and file ownership across specs
01–04: it merges this spec's items with specs 02–04 into waves in which every file has exactly
one owner. The items below are this spec's own decomposition; the build plan names, for each
wave item, which of them (and which hunks of other specs) it carries. Where a file is shared
across specs (`internal/remote/server.go`, `conn.go`, `internal/protocol/frame.go`,
`internal/daemon/server.go`, `daemon.go`, `remotewire.go`, `internal/doctor/fix.go`,
`SetupChecklist.svelte`, `desktop/configsvc.go`, `AGENTS.md`, `README.md`), the build plan gives
it to ONE item per wave, which lands every spec's hunk for that file.

### 13.1 Items

Each item owns the files listed; no file appears in two items of this spec. Order and
cross-spec merging: `00-build-plan.md`.

1. **W1 — protocol contract.** `internal/protocol/devices.go` (new: command, tier, state,
   reason constants; `LocalOnlyCommands`, `Tiers`, `DefaultTiers`;
   `PairBeginArgs`, `PairBeginData`, `PairStatusArgs/Data`, `PairConfirmArgs`, `PairRejectArgs`,
   `DeviceInfo`, `DevicesData`, `RevokeDeviceArgs/Data`, `SetDeviceTiersArgs`, `PairRequest`,
   `PairResult`, `PairRelay{RV, Bearer}`, `ReadyData`; `PairProof`, `PairCode`),
   `internal/protocol/sessiontimeline.go` (new), `internal/protocol/talk.go` (`TalkCommands`
   incl. `sshRecall`/`sshAnswer`, comment), `internal/protocol/ssh.go` (`SSHHumanCommands`
   without the eleven, comment), `internal/protocol/frame.go` (`FrameReady`, `ReadyData`, four
   codes), `internal/protocol/protocol.go` (delete old `PairBeginData` + M1 comments,
   `Request.Cmd` comment), `internal/protocol/testdata/pairing_vectors.json`,
   `internal/protocol/pairing_vectors_test.go`, `mobile/src/wire/testdata/pairing.json`.
2. **W2 — registry.** `internal/devices/devices.go`, `internal/devices/journal.go`
   (`devices.revoked`), `internal/devices/devices_test.go`.
3. **W3 — QR.** `internal/qr/qr.go`, `internal/qr/terminal.go`, `internal/qr/qr_test.go`,
   `internal/qr/testdata/vectors.json` (generate the matrices once from
   `desktop/frontend/src/lib/qr.ts` with a throwaway script; do not edit `qr.ts`).
4. **W4a — tiers and rebuild.** `internal/remote/tiers.go`, `internal/remote/tiers_test.go`,
   `internal/remote/fields.go`, `internal/remote/fields_test.go`. Exports `TierFor`,
   `CommandTiers()`, `TransportCommands`, unexported `frameTier`, `rebuild`. (`policy.go` and
   `policy_test.go` are deleted by W4c, which removes their last caller in `conn.go`, after W5e
   moved the daemon tests off `CommandDenied`; `00-build-plan.md`.)
5. **W4b — pairing and authorizer.** `internal/remote/pairing.go`, `pairing_test.go`,
   `internal/remote/pairpayload.go`, `pairpayload_test.go`, `internal/remote/deviceauth.go`,
   `deviceauth_test.go`, `internal/remote/auth.go` (`Handshake.Via/RelayRV/SetReadDeadline`,
   `Peer.Via`, `relayed`, `ErrPairingDone`); delete `connectcode.go`, `connectcode_test.go`.
6. **W4c — transport.** `internal/remote/server.go` (incl. `Listen`/`Options` moved from
   `insecure.go`, pools), `conn.go` (`ready`, `not_paired`, slot move, `rebuild`, audit),
   `identity.go` (`TLSConfig(verify)`, `verifyPeer`, refusal log bucket),
   `identity_test.go`, `ratelimit.go` (new: buckets, `extCommands`), `audit.go` (new: `logSafe`
   and the audit line, moved from `policy.go`),
   `ratelimit_test.go` (new), `doc.go`, `harness_test.go`, `server_test.go`, `listen_test.go`,
   `pane.go` (pty-write audit line and pane rate limits only), `pane_test.go`; delete
   `insecure.go`, `insecure_off.go`, `insecure_test.go`, `insecure_off_test.go`, `policy.go`,
   `policy_test.go` (surviving assertions move to `tiers_test.go`/`ratelimit_test.go`).
7. **W5a — daemon pairing/device handlers.** `internal/daemon/pairing.go` (handlers,
   `listenerDial` moved and reordered, loopback `Problem`, relay QR through `d.relayPairOffer`
   and `ConfirmRelay` through `d.relayMint` (spec 03; stubbed until the relay loop lands),
   `setDeviceTiers` → `d.dropRemoteDevice`),
   `internal/daemon/pairing_test.go`; delete `pairbegin.go`, `pairbegin_off.go`,
   `pairbegin_test.go`, `pairbegin_off_test.go`, `regeneratekey.go`, `regeneratekey_off.go`.
8. **W5b — daemon wiring.** `internal/daemon/server.go` (cases: 6 new local commands,
   `sessionTimeline`, remove `regenerateRemoteKey`, rewrite `pairBegin` comment),
   `internal/daemon/remotewire.go` (`startRemote` options + `Unsaved` gate, `handleRemote` guard
   and `remoteMayAnswer`, `devicesChanged`, `DropDevice` helper used by W5a through
   `d.dropRemoteDevice(id) int`; the spec 03 functions it calls live in stub files until the
   relay loop lands, `00-build-plan.md`),
   `internal/daemon/daemon.go` (fields, `newDaemon` creates registry + pairer and subscribes,
   comments), `internal/daemon/remotelisten.go` (untag); delete `remotelisten_off.go`,
   `remotewire_off_test.go`; untag and rewrite `remotewire_integration_test.go`,
   `mdnswire_test.go`; add `internal/daemon/remotetiers_test.go` (AST totality tests) and
   `internal/daemon/remotewire_test.go` cases for `remoteMayAnswer` and `newDaemon`.
9. **W5c — sessionTimeline.** `internal/daemon/sessiontimeline.go`,
   `internal/daemon/sessiontimeline_test.go`.
10. **W5d — list split fallout.** `internal/daemon/talk_contract_test.go`,
    `internal/daemon/ssh_test.go` (the two tests named in §8.4), `internal/daemon/talk.go`
    (comment naming `SSHHumanCommands` → `TalkCommands`).
11. **W5e — `CommandDenied` call sites.** Rewrite each assertion; the hook-bridge halves stay:

    | File:line | Today | After |
    |---|---|---|
    | `internal/daemon/sshwidget_test.go:141` | `!remote.CommandDenied(CmdSSHWidgetStatus)` | `remote.TierFor(CmdSSHWidgetStatus) != protocol.TierNever` → fail |
    | `internal/daemon/sshprompt_test.go:474` | `CmdSSHPromptAnswer` denied | `TierFor == TierNever` |
    | `internal/daemon/reviewverify_test.go:103` | `CmdReviewComplete` denied | `TierFor == TierNever` |
    | `internal/daemon/reviewverify_test.go:52` | Verify/Checks/Recover/Feedback denied | `TierFor`: Verify → `shell`, Checks → `read`, Recover → `control`, Feedback → `answer` (each asserted exactly) |
    | `internal/daemon/reviewcapture_test.go:151` | Capture denied | `TierFor(CmdReviewCapture) == protocol.TierShell` |
    | `internal/daemon/reviewpackage_test.go:63` | Publish/Packages/Image/Mark/Diff denied | Publish → `never`; Packages, Image, Diff → `read`; Mark → `answer` |

    The test names that say "human only" keep their meaning for the hook bridge; their failure
    messages change from "reachable from a phone" to "wrong tier for a phone".
12. **W5f — tickets cache.** `internal/daemon/tickets.go` (`ticketsCache`, `ticketsTTL`),
    `internal/daemon/tickets_test.go`.
13. **W6 — config.** `internal/config/remote.go`, `internal/config/remote_test.go`,
    `config.example.toml` (`[remote]` block).
14. **W7 — CLI.** `paircmd.go`, `paircmd_test.go` (repo root), `main.go` (register `pair`,
    `devices`).
15. **W8 — desktop.** `desktop/phonesvc.go`, `desktop/phonesvc_test.go`, `desktop/configsvc.go`,
    `desktop/configsvc_test.go`, `desktop/main.go` (service registration),
    `desktop/frontend/bindings/**` (regenerated), `desktop/frontend/src/lib/views/SettingsForm.svelte`,
    `SettingsForm.test.ts` (remove the M1 connect panel, regenerate button, insecure toggle).
16. **W9 — TUI.** `internal/tui/settingsform.go`, `internal/tui/settingsform_test.go`.
17. **W10 — build and CI.** `Makefile`, `.github/workflows/ci.yml`, `mobile/package.json`
    (two scripts only); delete `contrib/lola-mobile-dev.sh`.
18. **W11 — doctor.** `internal/doctor/fix.go` (`FixPhonesRegistry`, `AllFixIDs`), the doctor
    file that holds the readiness rows (the `phones` row) + its test,
    `desktop/frontend/src/lib/components/SetupChecklist.svelte` (the `phones_registry` FIXES
    entry only).
19. **W12 — docs.** `AGENTS.md` (§10.1 text), `README.md` (command rows, `[remote]` table).
    Spec 04 owns `mobile/README.md`, `mobile/HANDOFF.md`, `mobile/PLAN.md`.
