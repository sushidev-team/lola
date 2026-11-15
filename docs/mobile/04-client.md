# 04 — Client architecture and interface

Status: implemented on `feature/mobile-rethink` (schedule: `00-build-plan.md`); this file stays the standing contract. Written as: spec for workflow 3 (client foundation) and workflow 4 (interface) of
`docs/mobile-rethink.md`. Covers plan §5, §6, §7, §8. Written against the code
on `feature/mobile-rethink` as of 2026-11-14 (Capacitor iOS 8.5.0, Vite 8.3.8).

Sibling specs, referenced by name and never redefined here:

- **01 — pairing, device identity, tiers** (`docs/mobile/01-pairing-and-tiers.md`)
- **02 — events and new commands** (`docs/mobile/02-events.md`: `watch`/`event`, `attachImage`, `attachmentImage`)
- **03 — hub relay** (`docs/mobile/03-hub-relay.md`: `/v1/relay/*`, the iOS relay leg and loopback bridge)

Section 0 lists every assumption this spec makes about them; all of them hold in
the sibling specs as revised together with this one. A builder who finds one
false fixes this spec, not the code around it.

---

## 0. Assumptions about specs 01, 02, 03

| # | Spec | Assumption | Where this spec depends on it |
|---|------|-----------|------------------------------|
| A1 | 01 §6.2, 03 §3.2 | QR payload is `lola2.<base64url(JSON), no padding>` with `{v:2, pin, addrs?:[string], port?, secret, relay?:{url, rv, tok}}`. `pin` = daemon SPKI SHA-256 in std base64, padded (44 chars, `DeviceKey.SPKIPin()`); `secret` = base64url of 32 bytes; `tok` = the pairing-window token (`^[A-Za-z0-9_-]{43}$`, random, minted by the daemon), presented to the hub as the bearer. No expiry field: expiry is learnt from `pair_expired`. Whole string ≤ `MaxPairPayloadBytes` = 1024. | §1.5 `PairPayload.swift`, §5.2 Connect |
| A2 | 01 §6.3, §6.4 | An unpaired connection is mutual TLS 1.3 with the phone's self-signed cert. Its first and only frame is `{"v":2,"type":"req","id":"lola-pair","cmd":"remote.pair","payload":{proof, name, platform:"ios"}}`, `proof = base64url_nopad(HMAC-SHA256(secret, TLS-Exporter("EXPORTER-lola-pair-proof-v1", "", 32)))`. The daemon burns the secret only on a VALID proof (a wrong proof closes that connection), sends NOTHING until a terminal state, then answers on id `lola-pair`: `resp ok:true` with `PairResult{deviceId, name, tiers, host, relay?:{rv, bearer}}`, or `err` with code `pair_rejected` / `pair_expired` / `pair_failed`, then closes the connection. Any frame the phone sends while waiting, or EOF, fails the pairing (`phone_left`). At most `maxUnpairedConns` = 4 unpaired candidates in total and `maxUnpairedPerSource` = 2 per source address (relay: per rv); a refused candidate gets `err not_paired`; with no window open an unpaired certificate gets a `bad_certificate` alert. | §1.5 pair flow |
| A3 | 01 §6.4 step 5, §6.6 | Both sides compute the 6-digit code with `PairCode(exporterCode, daemonSPKIHash, phoneSPKIHash)` (exporter label `EXPORTER-lola-pair-code-v1`). The daemon never shows it: the human types it on the Mac. `internal/protocol/testdata/pairing_vectors.json` holds golden vectors for proof, code and payloads; `PairHandshakeTests.swift` and `PairPayloadTests.swift` read that file. | §1.5 |
| A4 | 01 §4.6 | A paired connection gets exactly one `{"type":"ready","payload":ReadyData{deviceId,name,tiers,via,host}}` frame before anything else. A path is authenticated ONLY when `ready` arrives. A post-handshake refusal is `err` code `not_paired`, then close; an unpaired certificate with no window open gets a `bad_certificate` alert. | §1.4 "authenticated", §1.6 classification |
| A5 | 01 §4.6, §4.4 | Tiers arrive in `ReadyData.tiers`, once per connection. A tier edit on the Mac (`setDeviceTiers`) closes the device's live connections; the reconnect's `ready` carries the new tiers. There is no pushed device topic. | §5.4 tier-gated controls |
| A6 | 01 §8.1 | A refused command answers on its id (`denied`, `unknown_cmd`, `rate_limited`) and the socket stays up. `maxRefusalsPerConn` = 64 strikes close it. Framing errors still close. | §2.6 bug 2, §2.3 `OpenURL` |
| A7 | 01 §8.3 | `kill` carries `keepWorktree`; `force` is never carried. | §2.3 `Kill` |
| A8 | 01 §6.1, §9.3 | Desktop `PhonesService` (`desktop/phonesvc.go`, 01's): `Begin() PairBeginData`, `Status(PairStatusArgs{pairing, since, waitMs}) PairStatusData` (long poll ≤ `PairStatusMaxWait` 25 s), `Confirm(PairConfirmArgs{pairing, code, tiers, name}) PairStatusData`, `Reject(pairing) PairStatusData`, `Devices() DevicesData`, `Revoke(id) RevokeDeviceData`, `SetTiers(id, tiers) DeviceInfo`. States: `waiting|confirming|paired|rejected|expired|failed|cancelled`. `PairStatusData` carries no code (the human types the code shown on the phone) and carries `via` ("lan" \| "relay"). | §7 Phones UI |
| A9 | 01 §9.1 | `lola pair --json` prints one JSON object per line (`{"event":"begin",…}`, `{"event":"status",…PairStatusData}`) and reads exactly one line from stdin when the state is `confirming`: the 6-digit code. Nothing is confirmed without that line. | §5.7 harness |
| A10 | 02 §3, §5.1, §6 | Frame `{type:"event", id:<watch id>, topic, seq, payload}`. `seq` starts at 1 per watch id, `+1` per event. Watch ids match `^[A-Za-z0-9_-]{1,64}$`. `watch {args:{topics}}` → `resp ok:true` with `WatchData{topics:[WatchResult{topic, ok, code?, message?}]}`; codes `invalid_topic`, `unknown_session`, `limit`, `unavailable`. An `err` frame with `id` = watch id and `topic` ends ONE stream (`frame_too_large`, `rate_limited` → retry after `WatchRetryAfter` 30 s, `denied`). A re-watch replaces the stream (new id, seq 1). `MaxWatchTopicsPerRequest` = `MaxWatchTopicsPerConn` = 32, `MaxProbedSessions` = 8 (4 per device); at most 8 connections per daemon hold watches (`maxWatchingConns`; a ninth gets `limit` on every topic). | §2.5, §3 |
| A11 | 02 §5 | Payloads: `sessions` = `SessionsEvent{reset?, more?, upsert?, remove?, omitted?, events?}` (`events` whole, only on reset or change; a reset may span events until `more:false`; `omitted` ids must be fetched with `sessions`); `projects` = `ProjectsData`; `status` = `StatusData`; `talk:<id>` = `TalkStateData`; `feed:<id>` = `FeedEvent{reset?, older?, session, epoch, lastSeq, scanning?, health, items, omitted?}` (scan progress events carry `scanning:true, items:[]`); `timeline:<id>` = `TimelineEvent{reset?, more?, older?, session, lastSeq, items, connections?, omitted?}` (`older:true` → page `sessionTimeline` from `after:0` up to `lastSeq`); `diff:<id>` = `DiffEvent{rev, cause?}`. `talk`/`diff` refuse an ended session with `unknown_session`. | §2.5, §3 |
| A12 | 02 §5.2, §5.4 | The `sessions` fingerprint excludes `age`, `headlineAgo`, `board.updatedAgo`, `prObservedAt`, `Event.ago`; the `talk` fingerprint excludes `feedSeq` (still sent). Clients render ages from `firstSeen`, `headlineAt`, `board.updatedAt`, `Event.at`. | §3.2, §8 idle budget |
| A13 | 01 §8.6 | `sessionTimeline` (read) takes `SessionTimelineArgs{session, conn?, after?, limit?}` (the JSON fields of `SSHTimelineArgs`) and answers the redacted `SSHTimelineData`: no Private blocks, no prompt cards, and elements WITHOUT `html`/`data` (widgets never reach a phone). | §2.3 `SSHService.Timeline`, §3.2 item 8 |
| A14 | 02 §12, 01 §7 | `attachImage {session, name, mime, data, part, last}`: `name` = client-random `^[A-Za-z0-9_-]{8,64}$`, `data` ≤ `AttachChunkBytes` = 512 KiB raw, parts strictly sequential (wait for each `resp`), `AttachIdleTimeout` 60 s, total ≤ 20 MiB; the last part answers `{received, path, thumb:""}`. Non-last parts are exempt from `writeRate` and charged to `attachBytesRate`/`attachBytesBurst` (2 MiB/s / 20 MiB); the `last:true` part costs one write. `attachmentImage {session, path, offset}` (read) answers ≤ 512 KiB chunks `{mime, size, offset, data, eof?}`. | §2.3 image methods |
| A15 | 03 §3.1, §3.6, §5.3, §13 | Relay phone leg: `GET wss://<relay.url>/v1/relay/phone`, no query string, headers `Lola-Relay-Rv: <rv>` and `Lola-Relay-Bearer: <bearer or window tok>` (never `Authorization`), `Sec-WebSocket-Protocol: lola-relay.v1`, no `Origin`, no cookies, over `URLSessionWebSocketTask`. Every refusal is an HTTP status before the 101 (400, 401, 403, 404, 429 + `Retry-After`, 503 `offline`/`busy`, 504 `relay_timeout`) with the hubproto code in `Lola-Relay-Code`, mapped by 03's `RelayRefusal`. Upgrade timeout `RelayPhoneAttemptTimeout` 8 s; `RelayFreshCredentialGrace` 60 s. 03 §13 is the normative contract; THIS spec owns every Swift file implementing it (§1.1). | §1.3 `RelayPath` |
| A16 | 02 §13 | Event payload types no desktop binding reaches (`SessionsEvent`, `FeedEvent`, `TimelineEvent`, `DiffEvent`, `WatchData`, `AttachImageData`, `AttachmentImageData`) are mirrored by hand in `mobile/src/wire/push.ts`, pinned by `mobile/src/wire/testdata/events.json` (02's vectors). Types a binding reaches (`TalkStateData`, `SSHTimelineData`, `SessionsData`) come from `@bindings/internal/protocol`. | §2 |
| A17 | 01 §5.1, §7, §8.1 | `maxConnsPerDevice` = 4; on overflow the daemon closes the device's OLDEST connection. `remote.CommandTiers()` exports the closed tier map; `internal/remote/tiers_golden_test.go` writes it to `mobile/src/wire/testdata/tiers.json` (`LOLA_WRITE_VECTORS=1`). | §1.4 parallel cap, §2.3 tiers |
| A18 | 03 §10.6 | `relayCred {args:{holds?}}` (tier `read`, acts only on the calling device's own record) → `RelayCredData{available, url?, rv?, bearer?}`. `bearer` is present only when the phone must store a new one; the phone's next `relayCred` (with the new hash) is the acknowledgement. `holds` omitted asks for a reissue (used after a 401). | §1.2 native frames, §1.3 |
| A19 | 01 §8.3, §8.4, §8.7 | `sshRecall {args: SSHRecallArgs{session, item}}` and `sshAnswer {args: SSHAnswerArgs}` are `protocol.TalkCommands` at tier `answer` (they act on any session's Conversation store); the daemon refuses an `sshAnswer` to an element scoped to an SSH connection ("this question is about an SSH host; answer it on the Mac"). `sshProposal`, `sshWidgetStatus` and every other ssh command are `never`. | §2.3 `SSHService` |

---

## 1. Swift transport plugin (`mobile/plugins/lola-transport`)

### 1.1 Targets and files

`Package.swift` keeps two targets. `LolaTransportCore` stays pure (Foundation,
Security, CryptoKit; no Network.framework, no UIKit, no Capacitor) so
`swift test` runs on a macOS host. Everything that touches a socket, the
Secure Enclave, WebKit or UIKit lives in `LolaTransportPlugin`.

`Package.swift` changes:

```swift
let coreOnly = ProcessInfo.processInfo.environment["LOLA_CORE_ONLY"] == "1"
// platforms: [.iOS(.v15), .macOS(.v13)]   — CryptoKit is @available(macOS 10.15); SwiftPM's
//                                            default macOS floor is lower and Core would not compile.
// The Capacitor dependency, the LolaTransportPlugin target and the product are appended only when !coreOnly.
```

SwiftPM compiles every file in a target directory, so new files need no
manifest edit; `LolaTransport.podspec` uses directory globs for the same reason
(item 2 verifies and fixes the globs if it does not).

Core tests that use the Keychain (`LolaKeychainTests`, `PairingRecordTests`'
store half) `XCTSkip` when `#if os(macOS)` (a host test process has no
keychain-access-group entitlement and gets `errSecMissingEntitlement`, -34018).
Everything else runs on the host.

| Target | File | Status | Owns |
|--------|------|--------|------|
| Core | `FrameCodec.swift` | keep | 4-byte prefix, `maxFrameBytes = 1 << 20` |
| Core | `SPKIPin.swift` | keep | pin from DER, constant-time compare |
| Core | `ConnectionState.swift` | rewrite | phases and failure codes of §1.6 |
| Core | `JSONText.swift` | keep | |
| Core | `HelloHandshake.swift` | **delete** (+ `HelloHandshakeTests.swift`) | bearer hello is gone |
| Core | `LolaKeychain.swift` | rewrite | generic-password CRUD (`SecItemAdd`/`SecItemUpdate`, atomic per item) |
| Core | `ClientCertificate.swift` | new | DER builder for a self-signed X.509 v3 cert over a P-256 key, signer injected |
| Core | `PairPayload.swift` | new | strict `lola2.` decoder (A1) |
| Core | `PairHandshake.swift` | new | `remote.pair` body, reply classifier, proof and code (A2, A3) |
| Core | `PathRace.swift` | new | pure race state machine, injected clock (§1.4) |
| Core | `ReconnectPolicy.swift` | new | backoff ladders (§1.6) |
| Core | `PairingRecord.swift` | new | Codable record kept in the Keychain (§1.5) |
| Core | `InboundFilter.swift` | new | which inbound frames stay native (§1.2) |
| Core | `BridgeOriginGuard.swift` | new | pure admit rule for WebKit script messages (§1.9) |
| Core | `RelayCredentials.swift` | new | Keychain item `{hubURL, rv, bearer}`, account = `daemonId`, `AfterFirstUnlockThisDeviceOnly`; the ONLY store of a bearer; `storedAt` for the fresh-credential grace |
| Core | `RelayWire.swift` | new | 03 §13.2: request builder (headers), `RelayRefusal` mapping, pause / backoff / fresh-grace policy (03 §13.3) |
| Plugin | `LolaTransportPlugin.swift` | rewrite | the typed JS surface of §1.2; no protocol logic; `load()` installs the bridge guard first |
| Plugin | `LolaConnection.swift` (981 lines) | **split** into `Session.swift` (one authenticated byte stream: framing, `ready`, native frames, coalescing, write watchdog) and `Connector.swift` (race, migrate, resume hooks) | |
| Plugin | `LolaTLS.swift` | new | `LolaTLS.parameters(pin:identity:)`, the one `NWParameters` builder (both paths and 03's bridge) |
| Plugin | `DirectPath.swift` | new | `NWConnection` TCP + mutual TLS |
| Plugin | `RelayPath.swift` | new | thin `LolaPath` adapter over `LoopbackBridge` |
| Plugin | `RelayLeg.swift` | new | 03 §13.2 leg: `URLSessionWebSocketTask`, ephemeral session, refused-upgrade status + headers |
| Plugin | `LoopbackBridge.swift` | new | 03 §13.2 bridge: one-shot `127.0.0.1` listener that accepts only the inner connection's own port (§1.3) |
| Plugin | `ClientIdentity.swift` | new | key generation (Secure Enclave when available), cert storage, `SecIdentity` lookup |
| Plugin | `PathMonitor.swift` | new | `NWPathMonitor` wrapper, debounced |
| Plugin | `BridgeGuard.swift` | new | main-frame-only `bridge` script-message handler (§1.9) |
| Plugin | `LolaDiscovery.swift` | keep, now called by `Connector`, not by JS | mDNS candidates |
| Plugin | `LolaTransportPlugin+Secrets.swift` | **delete** | no bearer key |
| Plugin | `LolaTransportPlugin+Scanner.swift`, `LolaQRScanner.swift` | change | `scanPairing()` keeps the payload native |
| Plugin | `LolaTransportPlugin+Media.swift` | new | `pickImage`, `readClipboardImage` |
| Plugin | `LolaTransportPlugin+Haptics.swift` | new | `haptic` |
| Plugin | `LolaDevLink.swift`, `+DevLink.swift` | rewrite | DEBUG-only `pair=` and `route=` (§5.7) |
| Plugin | `LolaTransportPlugin+Open.swift`, `+Discovery.swift` | keep `openURL` (http/https only); delete the JS `discover` method | |
| Plugin | `LolaLog.swift` | keep | gains `devLinkNote(_:)` (DEBUG only, §5.7) |

### 1.2 The one typed JS surface (`plugins/lola-transport/src/definitions.ts`)

Rewritten whole. Every method the app calls is declared here, and the app
reaches the plugin only through `mobile/src/native/index.ts` (§2.1). No method,
listener or frame batch carries a secret: the pairing secret, the QR's `tok`,
every relay bearer (`PairResult.relay.bearer`, `relayCred` replies) and the
private key never cross the bridge, because Capacitor logs every resolved
payload and every listener event in Debug builds
(`CAPLog.print("TO JS", …)`).

```ts
export type DaemonId = string;             // base64url(SHA-256(daemon SPKI)), no padding
export type PathKind = "direct" | "relay";   // client-side name; "direct" is the daemon's ReadyData.via "lan"
export type Phase = "idle" | "racing" | "ready" | "migrating" | "closed" | "failed";
export type FailureCode =
  | "offline"            // NWPathMonitor reports no usable interface
  | "unreachable"        // every candidate failed (TCP, WebSocket, TLS before ready, or a wrong key on some)
  | "timeout"
  | "key_changed"        // every candidate that finished a TLS handshake showed the SAME non-pinned key (§1.4)
  | "not_paired"         // authenticated refusal: `not_paired` err or a TLS alert AFTER the pinned handshake completed
  | "unsupported_version"
  | "relay_unavailable"  // relay was the only candidate and the hub answered 503 offline/busy or 504
  | "protocol" | "peer_closed" | "client_closed" | "backgrounded" | "internal";

export type RelayState = "none" | "ok" | "backoff" | "credential_dead" | "hub_too_old";
export interface PathInfo { kind: PathKind; host?: string; port?: number; interface?: "wifi" | "cellular" | "wired" | "other"; }
export interface DeviceInfo { deviceId: string; name: string; tiers: string[]; host: string; }

export interface LolaTransportPlugin {
  // --- pairing ------------------------------------------------------------
  scanCapability(): Promise<{ available: boolean; authorization: "notDetermined"|"authorized"|"denied"|"restricted"; reason?: "no_camera"|"denied"|"restricted"|"unsupported" }>;
  /** Opens the scanner. A `lola2.` payload is parsed and held NATIVELY; only its public half comes back. */
  scanPairing(opts?: { prompt?: string }): Promise<
    | { cancelled: true }
    | { cancelled: false; pairRef: string; daemonId: DaemonId; addrs: number; relay: boolean }
    | { cancelled: false; invalid: "not_lola" | "old_format" | "malformed" }>;
  /** Races to ONE TLS session (§1.5), sends `remote.pair` on it, resolves right after the write with the locally computed code. */
  pairBegin(opts: { pairRef: string; deviceName: string }): Promise<{ code: string; path: PathInfo }>;
  /** Abandons a pairing: closes its socket (the daemon records phone_left), drops the held payload. */
  pairCancel(opts: { pairRef: string }): Promise<void>;

  // --- paired daemons -----------------------------------------------------
  daemons(): Promise<{ daemons: { daemonId: DaemonId; name: string; pairedAt: number; relay: RelayState; secureEnclave: boolean; lastPath?: PathKind }[] }>;
  /** Renames locally only (display). */
  rename(opts: { daemonId: DaemonId; name: string }): Promise<void>;
  /** Deletes key, certificate, pairing record and relay credentials. Idempotent. */
  forget(opts: { daemonId: DaemonId }): Promise<void>;

  // --- connection ---------------------------------------------------------
  /** Races every known path (§1.4). Resolves when one sent `ready`. */
  connect(opts: { daemonId: DaemonId }): Promise<{ epoch: number; path: PathInfo; raceMs: number; device: DeviceInfo }>;
  disconnect(opts?: { reason?: string }): Promise<void>;
  /** Complete JSON bodies; the plugin adds the prefix. Rejects on a closed epoch, and rejects a batch that touches native state (§1.2 "native frames"). */
  send(opts: { epoch: number; frames: string[] }): Promise<void>;
  /** Accept a migration the plugin announced with `migrating` (§1.7). */
  adopt(opts: { epoch: number }): Promise<void>;
  status(): Promise<{ epoch: number; phase: Phase; path?: PathInfo; relay: RelayState; framesIn: number; framesOut: number; bytesIn: number; bytesOut: number; launchedAt: number }>;

  // --- device -------------------------------------------------------------
  pickImage(opts: { source: "camera" | "library" }): Promise<{ cancelled: true } | { cancelled: false; mime: "image/jpeg" | "image/png"; data: string; width: number; height: number }>;
  readClipboardImage(): Promise<{ present: false } | { present: true; mime: "image/png" | "image/jpeg"; data: string }>;
  haptic(opts: { kind: HapticKind }): Promise<void>;
  /** http/https only (enforced natively, `LolaTransportPlugin+Open.swift`). */
  openURL(opts: { url: string }): Promise<void>;
  /** Keeps the screen awake while a pairing waits for the Mac (§1.5). */
  setIdleTimerDisabled(opts: { disabled: boolean }): Promise<void>;

  addListener(e: "frames", f: (ev: { epoch: number; frames: string[] }) => void): Promise<PluginListenerHandle>;
  addListener(e: "state", f: (ev: StateEvent) => void): Promise<PluginListenerHandle>;
  addListener(e: "device", f: (ev: { epoch: number; device: DeviceInfo }) => void): Promise<PluginListenerHandle>;
  addListener(e: "pairing", f: (ev: PairingEvent) => void): Promise<PluginListenerHandle>;
  addListener(e: "appState", f: (ev: { active: boolean }) => void): Promise<PluginListenerHandle>;
  addListener(e: "devLink", f: (ev: DevLinkEvent) => void): Promise<PluginListenerHandle>;
  removeAllListeners(): Promise<void>;
}

export interface StateEvent {
  epoch: number;
  phase: Phase;
  path?: PathInfo;                 // on ready / migrating
  code?: FailureCode;              // on failed / closed
  daemonCode?: string;             // the daemon's ErrPayload.code when it spoke
  minV?: number; maxV?: number;    // with unsupported_version
  raceMs?: number;                 // on ready
  relay?: RelayState;
}
export type PairingOutcome = "paired" | "refused" | "expired" | "failed" | "dropped";
export interface PairingEvent { pairRef: string; outcome: PairingOutcome; daemonId?: DaemonId; name?: string; reason?: string }
export type HapticKind = "light" | "medium" | "rigid" | "selection" | "success" | "warning" | "error";
export interface DevLinkEvent { source: "dev-url" | "dev-launch"; route?: string; sheet?: string; pairRef?: string; daemonId?: DaemonId; }

export const LOLA_MAX_FRAME_BYTES = 1 << 20;   // = protocol.MaxFrameBytes
export const LOLA_DEFAULT_PORT = 7717;         // = config.DefaultRemotePort
```

Removed from the surface: `secretSet`, `secretHas`, `secretDelete`, `scanQR`,
`discover`, and the `host/port/spkiPin/allowUnpinned/insecureKey/keyRef`
connect options. There is no unpinned mode: a payload without a 32-byte pin is
`malformed`.

**Native frames** (`InboundFilter.swift`, `Session.swift`). The plugin owns a
small part of the stream and JS never sees it:

| Frame | Native action | Reaches JS |
|-------|---------------|-----------|
| `ready` (first frame) | marks the attempt authenticated; becomes `connect()`'s `device` and a `device` listener event for that epoch | as `device` event (no secret in it) |
| `resp`/`err` whose `id` starts with `lola-` | answers to native requests (`lola-pair`, `lola-relaycred-<n>`) | never |

Right after `ready`, on every epoch, the native session sends
`{"v":2,"type":"req","id":"lola-relaycred-<n>","cmd":"relayCred","args":{"holds":"<hex sha256 of the stored bearer>"}}`
(`holds` omitted when no bearer is stored or after a `credentialDead`, A18).
The reply is handled natively: `available:false` keeps the stored record;
`url`/`rv` different from the stored ones replace them; a `bearer` is saved with
`RelayCredentials.save` (one `SecItemUpdate`, `storedAt = now`) and acknowledged
by sending `relayCred` again with the new hash. A refused `relayCred` (an older
daemon: `unknown_cmd`) is logged and ignored.

Outbound guard: `send` rejects the whole batch with `internal` "reserved frame"
if any frame has an `id` starting with `lola-` or `cmd == "relayCred"`. JS
therefore cannot read or disturb the relay credential.

Limits (`definitions.ts` and Swift `Limits` enum, asserted equal by
`mobile/src/wire/limits.test.ts`):

| Constant | Value | Meaning |
|----------|-------|---------|
| `MAX_PAIR_PAYLOAD_CHARS` | 1024 | = 01 `MaxPairPayloadBytes`; longer scan = `malformed` |
| `PAIR_HOLD_MS` | 300000 | a scanned payload is dropped from memory after this (= 01 `PairSecretTTL`) |
| `MAX_PICK_LONG_EDGE_PX` | 2048 | `pickImage` downscales to this |
| `MAX_PICK_BYTES` | 4 MiB | after JPEG q=0.82; larger is re-encoded at q=0.6, then refused |
| `MAX_CLIPBOARD_BYTES` | 8 MiB | `readClipboardImage` refuses larger |
| `FLUSH_INTERVAL_MS` | 16 | inbound coalescing, = `panebus.DefaultFlushInterval` |
| `MAX_BATCH_BYTES` | 262144 | flush early above this |
| `HAPTIC_MIN_GAP_MS` | 50 | haptics closer than this are dropped |

### 1.3 Paths

```swift
protocol LolaPath: AnyObject {
    var info: PathInfo { get }                       // kind, host, port, interface
    /// Start TCP (+ relay leg) and the mutual-TLS handshake. `.tlsReady` fires when the
    /// inner NWConnection reached `.ready` (handshake complete, server Finished verified,
    /// pin matched). Authentication (`ready` frame, A4) is the Session's job.
    func start(on queue: DispatchQueue, events: @escaping (PathEvent) -> Void)
    func send(_ bytes: Data, completion: @escaping (NWError?) -> Void)
    func receive(_ handler: @escaping (Data?, Bool /*eof*/, NWError?) -> Void)
    func cancel()
}
enum PathEvent {
    case tlsReady(exporter: (String, Int) -> Data?)  // sec_protocol_metadata_create_secret
    case failed(PathFailure)
    case viabilityChanged(Bool), betterPathAvailable
}
enum PathFailure: Equatable {
    case network(String)                 // TCP / DNS / WebSocket transport
    case tlsBeforeReady(String)          // any TLS failure or alert before `.ready`: unauthenticated
    case keyMismatch(spki: Data)         // verify block saw a certificate whose SPKI hash != pin
    case alertAfterReady(String)         // TLS alert after `.ready`: authenticated (A4 bad_certificate)
    case relay(RelayRefusal)             // 03's mapping of the refused upgrade
    case timeout
}
```

**`LolaTLS.parameters(pin:identity:)`** (`LolaTLS.swift`, the single builder):
TLS 1.3 min and max; fixed SNI `"lola-daemon"`
(`sec_protocol_options_set_tls_server_name`) on both paths so the ClientHello
is identical; verify block = SPKI pin match (moved from
`LolaConnection.parameters(for:pinBox:verifyQueue:)`), which also records the
presented SPKI hash so a mismatch is reported as `keyMismatch(spki:)`; client
identity via `sec_protocol_options_set_local_identity(sec, sec_identity_create(identity)!)`;
TCP keepalive 30/10/3; `noDelay`. `LoopbackBridge` calls this function for the
inner connection (03 §13.2 step 3).

**Why a TLS alert before `.ready` proves nothing.** A plaintext alert can come
before the ServerHello, and an on-path host can present the daemon's public
certificate, pass the pin check in the verify block (which runs before
`CertificateVerify` is checked) and then send an alert under handshake keys it
negotiated itself. Only after `.ready` has the stack verified
`CertificateVerify` and the server `Finished`, so only then is an alert from
the pinned daemon. TLS 1.3 sends the daemon's `bad_certificate` (01 §4.1 step 6)
after the client's `Finished`, i.e. after `.ready`, so the real refusal is
always `alertAfterReady`.

**`DirectPath`**: `NWConnection(host:port:using: LolaTLS.parameters(pin:identity:))`.
`viabilityUpdateHandler` and `betterPathUpdateHandler` feed `PathEvent`.

**`RelayPath`**: a ≤ 80-line adapter. `start` loads `RelayCredentials` for the
daemon, builds `LoopbackBridge(leg: RelayLeg(request: RelayWire.request(…)),
innerParameters: LolaTLS.parameters(pin:identity:))`, maps its outcomes to
`PathEvent` (a refused upgrade → `.failed(.relay(refusal))`; inner `.ready` →
`.tlsReady`), and forwards `send`/`receive` to the inner connection. During
pairing it is built from the QR `{url, rv, tok}` held in memory instead of the
Keychain (`tok` goes in `Lola-Relay-Bearer`).

`LoopbackBridge` (03 §13.2 steps 1–6 are normative): the relay leg's 101 comes
first; then the listener accepts a connection only if its remote port equals
the inner `NWConnection`'s local port; any other connection is cancelled at
once and the listener keeps listening (no `newConnectionLimit = 1`) until the
matching peer arrives or `LOOPBACK_MATCH_TIMEOUT_MS = 2000` passes. When the
inner local port is not yet known at accept time, the accepted connection is
held unread until the inner `pathUpdateHandler` reports it (same timeout). A
local process that connects first therefore wastes nothing.

Relay refusal mapping (03 §13.2 `RelayRefusal`, §13.3 rules), per relay
candidate only:

| Refusal | Effect | `RelayState` |
|---------|--------|--------------|
| `credentialDead` (401) within `RELAY_FRESH_GRACE_MS` (60 000 = 03 `RelayFreshCredentialGrace`) of `RelayCredentials.storedAt` | treated as `daemonOffline` (the hub may not have the new hash yet) | `backoff` |
| `credentialDead` (401) otherwise | relay skipped for this daemon for `RELAY_UNAUTHORIZED_PAUSE_MS` (1 h = 03 `RelayPhoneUnauthorizedPause`) or until a `relayCred` reply stores a bearer; the next authenticated session sends `relayCred` with `holds` omitted, which reissues | `credential_dead` |
| `hubTooOld` (404) | relay skipped for `RELAY_UNSUPPORTED_PAUSE_MS` (6 h = 03 `RelayUnsupportedRecheck`) or until the stored hub URL changes | `hub_too_old` |
| `daemonOffline`, `busy` (503), `timeout` (504 or upgrade timeout) | relay backoff `RELAY_BACKOFF` 2, 4, 8, 16, 32, 60 s, then 60 s; direct unaffected | `backoff` |
| `rateLimited(t)` (429) | relay skipped for `max(t, 2 s)` | `backoff` |
| `forbidden`, `badRequest`, `other` | candidate failure, backoff as 503 | `backoff` |

None of these is ever `not_paired`: a hub answer is not the daemon's.

### 1.4 The racing connector (`Connector.swift` + pure `PathRace.swift`)

Candidates for one daemon, in order:

1. `record.lastGood` (direct host:port that last authenticated), if any.
2. `record.addrs` from the QR, ranked by `rankAddresses` rules (private IPv4 before link-local, IPv6 last) — the ranking moves from `mobile/src/lib/pairpayload.ts` into `PathRace.swift`.
3. mDNS results from `LolaDiscovery` (`_lola._tcp`), started at t=0, appended as they arrive, deduped by host:port against 1–2 and each other, at most `MAX_MDNS_CANDIDATES` = 8 per race. They are unauthenticated hints; a wrong one costs one attempt.
4. Relay, when `RelayCredentials` exists for the daemon and its `RelayState` allows an attempt now.

Schedule (constants in `PathRace.swift`):

| Constant | Value | Rule |
|----------|-------|------|
| `DIRECT_STAGGER_MS` | 150 | direct candidate *n* starts at `n × 150` ms |
| `RELAY_DELAY_MS` | 300 | relay starts at 300 ms, or immediately when no direct candidate exists or the path monitor reports no Wi-Fi/wired interface |
| `DIRECT_ATTEMPT_TIMEOUT_MS` | 4000 | per direct attempt, TCP + TLS + `ready` |
| `RELAY_UPGRADE_TIMEOUT_MS` | 8000 | relay leg dial to 101 (= 03 `RelayPhoneAttemptTimeout`, > `RelayAcceptTimeout` 6 s) |
| `RELAY_ATTEMPT_TIMEOUT_MS` | 12000 | per relay attempt: upgrade + inner TLS + `ready` |
| `RACE_TIMEOUT_MS` | 13000 | whole race (`RELAY_DELAY_MS` + `RELAY_ATTEMPT_TIMEOUT_MS` < it) |
| `PREFER_DIRECT_GRACE_MS` | 250 | if the relay sends `ready` first, wait this long for a direct attempt already at `.tlsReady`; direct wins a tie |
| `MAX_PARALLEL_ATTEMPTS` | 3 | attempts in flight in a cold race |
| `MAX_DIRECT_PARALLEL` | 2 | direct attempts in flight at once (= 01 `maxPreAuthPerIP`: the daemon refuses a third pre-auth socket from one address) |
| `MAX_MIGRATION_ATTEMPTS` | 2 | attempts in flight in a background race while an epoch is live |
| `MAX_MDNS_CANDIDATES` | 8 | per race |

Why 3 and 2: the daemon authenticates every attempt that completes TLS
(01 §4.3), so each one holds an authed slot until cancelled, and
`maxConnsPerDevice` = 4 evicts the OLDEST (A17). Cold: 3 attempts plus one
half-open socket from before a network change = 4; the stale socket is the
oldest, so it is the one evicted. Migration: live epoch + 2 attempts + one
stale = 4; nothing live is evicted.

"Authenticated" = the `ready` frame arrived (A4). The winner's epoch is
reported with its `ReadyData`; every other attempt is cancelled (socket closed;
nothing was sent on it except, for a loser that already authenticated, the
native `relayCred` request, which is harmless to repeat).

`PathRace` is a value type: `mutating func step(now: Int, event: RaceEvent) -> [RaceAction]`
where `RaceAction ∈ {start(candidate), cancel(candidate), win(candidate), fail(FailureCode)}`
and `RaceEvent` carries `PathFailure` per candidate. All timing and
classification tests live in Core.

**Classification.** Failures are per candidate; only one of them ends the race
early:

| Signal | Scope | Effect |
|--------|-------|--------|
| `network`, `timeout`, `tlsBeforeReady` | that candidate | candidate failed; race continues |
| `keyMismatch(spki)` | that candidate | candidate failed; the SPKI is recorded; race continues |
| `relay(…)` | relay candidate | per §1.3 table; race continues with direct |
| `not_paired` err frame, or `alertAfterReady` | daemon (authenticated) | race ends at once with `not_paired` |
| `unsupported_version` err after `.tlsReady` | daemon | race ends at once with `unsupported_version` |

When every candidate has failed: `key_changed` if at least one candidate
reported `keyMismatch`, every candidate that reached the certificate stage
reported the SAME wrong SPKI, and that set includes the relay candidate when the
daemon has relay credentials. Else `offline` when the monitor has no satisfied
path; else `relay_unavailable` if relay was the only candidate and its refusal
was 503/504; else `timeout` if any attempt timed out; else `unreachable`. A
single injected mDNS host, or a hub that presents another key, therefore never
produces `key_changed` while another path succeeds or disagrees.

### 1.5 Identity and pairing

**Key** (`ClientIdentity.generate(daemonId:)`): one key per paired daemon, so
two daemons cannot correlate the phone and revocation on one never touches the
other (Deviation D1).

```swift
let tag = Data("dev.sushi.lola.client.\(daemonId)".utf8)
var priv: [String: Any] = [kSecAttrIsPermanent as String: true, kSecAttrApplicationTag as String: tag]
var attrs: [String: Any] = [kSecAttrKeyType as String: kSecAttrKeyTypeECSECPrimeRandom, kSecAttrKeySizeInBits as String: 256]
if SecureEnclave.isAvailable {
    attrs[kSecAttrTokenID as String] = kSecAttrTokenIDSecureEnclave
    priv[kSecAttrAccessControl as String] =
        SecAccessControlCreateWithFlags(nil, kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly, .privateKeyUsage, nil)!
} else {
    priv[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly   // no access control object
}
attrs[kSecPrivateKeyAttrs as String] = priv
```

`.privateKeyUsage` is a Secure Enclave flag; the software branch (Simulator,
devices without an enclave) sets accessibility only. No biometric gate (an
unattended reconnect must read it), never synced, never exported.
`daemons()` reports `secureEnclave` per record.

**Certificate** (`ClientCertificate.build(publicKeyX963:sign:)` in Core):
X.509 v3, serial = 16 random bytes with the top bit cleared, issuer = subject =
`CN=lola-phone`, notBefore = now − 1 h, notAfter = now + 10 y,
`ecdsa-with-SHA256`, no extensions except `basicConstraints CA:FALSE`. The
signer closure calls `SecKeyCreateSignature(.ecdsaSignatureMessageX962SHA256)`.
The daemon trusts the SPKI only (01 §4.1), so nothing else in the cert matters.
Stored as `kSecClassCertificate` with `kSecAttrLabel = "lola.client.<daemonId>"`;
`SecIdentity` is fetched with `kSecClass: kSecClassIdentity, kSecAttrLabel: …`.

**Pairing record** (`PairingRecord`, Keychain generic password, service
`dev.sushi.lola.mobile.pairing`, account = `daemonId`, accessible
`AfterFirstUnlockThisDeviceOnly`). It holds no secret:

```swift
struct PairingRecord: Codable {
  var v = 2
  var daemonId: String; var name: String; var pin: String
  var deviceId: String              // PairResult.deviceId
  var addrs: [String]; var port: Int; var lastGood: String?
  var relayURL: String?             // hub base URL; the bearer lives ONLY in RelayCredentials (account = daemonId)
  var relayState: String            // RelayState raw value, persisted with its deadline
  var relayRetryAt: Date?
  var pairedAt: Date; var secureEnclave: Bool
}
```

**Pair flow.** `scanPairing` decodes the payload (`PairPayload.decode`) and
holds `{payload}` in memory under a random `pairRef` for `PAIR_HOLD_MS`.
`pairBegin`:

1. Generate key + cert for the payload's `daemonId` (deleted again on any non-`paired` outcome).
2. **Race to one TLS session.** Candidates: the QR `addrs` (ranked) then the QR relay (`RelayPath` built from `{url, rv, tok}`). No mDNS, no `lastGood`. At most `PAIR_MAX_PARALLEL` = 2 in flight (= 01 `maxUnpairedPerSource`, so two direct attempts from this phone's address are both admitted), same stagger and timeouts as §1.4. The first attempt to reach `.tlsReady` wins; every other attempt is cancelled before any application byte is written on it (the daemon does not burn the secret on a connection that sent no `remote.pair`, 01 §6.4 step 1).
3. On the winner only: compute `proof = PairHandshake.proof(secret:, exporter: exporter("EXPORTER-lola-pair-proof-v1", 32))` (base64url, no padding, pinned by A3's vectors) and write `PairHandshake.body(name:proof:)` with id `lola-pair`. The secret and `tok` are dropped from memory when that write is issued, success or not.
4. Compute `code = PairHandshake.code(exporterCode: exporter("EXPORTER-lola-pair-code-v1", 32), daemonSPKI:, clientSPKI:)` locally and resolve `pairBegin` with `{code, path}` as soon as the write completed. No daemon frame is awaited (A2: the daemon sends nothing yet).
5. The socket stays open and the plugin sends nothing on it. It reads one frame natively (`PairHandshake.classify`):

| Daemon answer | Outcome | Native action |
|---------------|---------|---------------|
| `resp ok:true` `PairResult` | `paired` | write `PairingRecord`; if `relay` present, `RelayCredentials.save({hubURL: qr.url, rv: relay.rv, bearer: relay.bearer})` (a fresh rv and bearer, 03 §10.6); the daemon then closes the socket (01 §6.4 step 7) and the app connects normally |
| `err pair_rejected` | `refused` | delete key + cert |
| `err pair_expired` | `expired` | delete key + cert |
| `err pair_failed` | `failed` (`reason` = message) | delete key + cert |
| `err not_paired` (window closed, or too many candidates, before the proof was read) | `expired` | delete key + cert |
| EOF / socket error after the write | `dropped` | delete key + cert |

If the race never reaches `.tlsReady`, or the winner fails before step 3's write,
`pairBegin` rejects with `unreachable`/`timeout` and the payload stays held (the
secret was not presented); the human may retry. A `tlsBeforeReady` or
`alertAfterReady` failure during the pairing race (the window closed, or a
third handshake was refused) rejects with `expired`, never `not_paired`.

While a pairing is past step 4 the app calls `setIdleTimerDisabled(true)`
(auto-lock would background the app, close the socket and fail the pairing as
`phone_left`), and `false` on any outcome. The `code` screen says to keep the
app open (§5.5).

### 1.6 State machines

Pairing (native, per `pairRef`):

```
held ──pairBegin──▶ racing ──TLS ready (one winner)──▶ sent ──resp ok──────▶ paired
  │                   │                                 │   ├─pair_rejected─▶ refused
  │                   └─no TLS / winner died before ────┤   ├─pair_expired──▶ expired
  │                     write: reject, stay held        │   ├─pair_failed───▶ failed
  │                   └─alert / bad_certificate ─▶ expired └─EOF / error───▶ dropped
  └─PAIR_HOLD_MS / pairCancel─▶ dropped (memory only)
```

Connection (native, one at a time):

```
idle ─connect─▶ racing ─win(ready)─▶ ready(epoch=n) ─path change─▶ ready + background race
                  │                     │                              │ better path sent ready
                  │                     │                              ▼
                  │                     │                         migrating(n+1) ─adopt─▶ ready(n+1), close n
                  │                     │                              └─JS silent 2s─▶ ready(n+1), close n
                  │                     ├─viability lost / EOF─▶ racing (backoff 0)
                  │                     ├─not_paired err─▶ failed(not_paired)
                  │                     └─background─▶ closed(backgrounded)
                  └─fail(code)─▶ failed ─retryable && backoff─▶ racing
```

Retryable: `offline`, `unreachable`, `timeout`, `relay_unavailable`,
`peer_closed`, `backgrounded`, `key_changed`. Terminal (no automatic retry):
`not_paired`, `unsupported_version`. `key_changed` keeps retrying on the ladder
but the app shows a warning with "Pair again" (§5.5); it never routes to
Connect on its own.

`ReconnectPolicy`: 0, 1, 2, 4, 8, 15 s, then 15 s forever; reset to 0 on path
change, on foreground, and on `connect()`. The relay candidate additionally
follows `RELAY_BACKOFF` (§1.3). The plugin retries on its own only in
`ready → racing` after a drop and in `failed` with a retryable code **while the
app is foreground**; it never retries in the background.

### 1.7 Path monitor re-race, foreground, resume

- `PathMonitor` wraps `NWPathMonitor()` with a 500 ms debounce
  (`PATH_DEBOUNCE_MS`). On a change while `ready`:
  - active is **relay** and the new path has Wi-Fi or wired → background
    direct-only race (`MAX_MIGRATION_ATTEMPTS`).
  - active is **direct** and its `NWConnection` reports
    `viabilityUpdateHandler(false)` or `betterPathUpdateHandler(true)` →
    background full race (`MAX_MIGRATION_ATTEMPTS`).
  - path `unsatisfied` → nothing; the socket's own failure drives recovery.
- A background race that wins emits `state{phase:"migrating", epoch:n+1, path}`.
  The new socket already received `ready`. JS answers `adopt({epoch:n+1})`
  once its correlator has no in-flight **mutating** request (§3.3: every
  command whose tier is not `read`), or after `MIGRATE_DRAIN_MS = 2000`,
  whichever comes first (the plugin adopts on its own at 2000 ms). Then epoch
  *n* is closed with `client_closed` and the app resumes on *n+1* (§3.3).
- Foreground (`willEnterForegroundNotification`) emits `appState{active:true}`
  and, if a daemon was connected before backgrounding, starts `racing`
  immediately with `lastGood` first. The app does not need to call `connect`.
- Background (`didEnterBackgroundNotification`) closes the socket (unchanged
  rule from `LolaConnection.observeAppLifecycle`). This satisfies 02 §6.2
  "Background: unwatch or close".

### 1.8 Swift tests (`ios/Tests/LolaTransportCoreTests/`)

- `PathRaceTests`: stagger, relay delay, prefer-direct grace, tie, parallel caps (3 cold, 2 migration), mDNS cap and dedupe, timeout; **an injected mDNS candidate with `keyMismatch` followed by a relay `ready` → `win(relay)`**; **a relay 401 followed by a direct `ready` → `win(direct)` and `RelayState.credential_dead`**; a relay 401 within `RELAY_FRESH_GRACE_MS` of `storedAt` → `backoff`, not `credential_dead`; `tlsBeforeReady` alert on one candidate and success on another → win; `alertAfterReady` → `fail(not_paired)` at once; all candidates same wrong SPKI including relay → `key_changed`; two different wrong SPKIs → `unreachable`; relay-only 503 → `relay_unavailable`.
- `ReconnectPolicyTests`: both ladders, resets, `Retry-After` floor.
- `PairPayloadTests`: 01's three golden payloads; 1025 chars → `malformed`; bad pin length; `lola1.` → `old_format`; unknown `v`; relay without `tok` → `malformed`.
- `PairHandshakeTests`: body bytes, `proof` is base64url without padding and equals 01's vectors, code equals 01's vectors, reply classification table of §1.5.
- `PairRaceTests` (pairing variant of `PathRace`): never more than 2 in flight; the first `.tlsReady` wins and every other attempt is cancelled before a write; a second `.tlsReady` after the win is cancelled.
- `InboundFilterTests`: a batch `[ready, resp lola-relaycred-1 {bearer}, event talk:x]` yields JS frames `[event talk:x]`, one `device` notification, one credential save and one acknowledging `relayCred` carrying the new hash; `available:false` keeps the record; outbound batches with `lola-` ids or `cmd:"relayCred"` are rejected.
- `RelayWireTests` (03 §13.4): exact header names, no `Authorization`/`Origin`; refusal mapping for every 03 §3.5 row plus 401, 404 with and without `Lola-Relay-Code`, 429 with and without `Retry-After`; pause, backoff and fresh-grace rules on an injected clock.
- `RelayCredentialsTests`: Codable round-trip, keyed by `daemonId` (host: Keychain half `XCTSkip`).
- `ClientCertificateTests`: DER parses with `SecCertificateCreateWithData`; SPKI round-trips through `SPKIPin.pin`; signature verifies with CryptoKit.
- `PairingRecordTests`: Codable round-trip, `v` mismatch rejected, no field holds a bearer.
- `BridgeOriginGuardTests`: §1.9 table.
- Kept: `FrameCodec*`, `FrameDecoder*`, `SPKIPin*`, `JSONText*`, `GoldenVectors`. Delete `HelloHandshakeTests.swift`; rewrite `LolaKeychainTests.swift` (host: `XCTSkip`) and `ConnectionStateTests.swift`.

Acceptance for item 1: `cd mobile/plugins/lola-transport && LOLA_CORE_ONLY=1 swift test`
is green on a Mac before CI runs it.

### 1.9 Capacitor bridge guard (untrusted frames)

The Conversation mounts agent-written widgets in sandboxed `data:` iframes
(`ssh/dock/DockArea.svelte`, mounted by `Workspace.svelte`). On the desktop,
`desktop/runtimeguard.go` refuses Wails runtime calls from Origin `null`. In
Capacitor 8.5.0, `WebViewDelegationHandler` registers the `bridge` handler with
`contentController.add(self, name: "bridge")`, which WebKit exposes to every
frame, and `userContentController(_:didReceive:)` dispatches every message to
`bridge.handleJSCall` without looking at `message.frameInfo`. A widget could
therefore call `LolaTransport.send` (and every other plugin) as the paired
human.

**Fix** (`LolaTransportPlugin/BridgeGuard.swift`, installed as the first line
of `LolaTransportPlugin.load()`; `CapacitorBridge.registerPlugins()` runs
`load()` during bridge init, before `loadWebView`, so before any page script):

```swift
final class MainFrameOnlyHandler: NSObject, WKScriptMessageHandler {
    weak var inner: WebViewDelegationHandler?
    let appOrigin: (scheme: String, host: String, port: Int)   // from bridge.config.serverURL
    func userContentController(_ c: WKUserContentController, didReceive m: WKScriptMessage) {
        let o = m.frameInfo.securityOrigin
        guard BridgeOriginGuard.admit(isMainFrame: m.frameInfo.isMainFrame,
                                      scheme: o.protocol, host: o.host, port: o.port,
                                      app: appOrigin) else { LolaLog.bridgeDropped(); return }
        inner?.userContentController(c, didReceive: m)
    }
}
// install: guard let b = bridge as? CapacitorBridge else { guardMissing = true; return }
//   let h = b.webViewDelegationHandler
//   h.contentController.removeScriptMessageHandler(forName: "bridge")
//   h.contentController.add(MainFrameOnlyHandler(inner: h, appOrigin: …), name: "bridge")
```

`BridgeOriginGuard.admit` (Core, pure) admits only `isMainFrame == true` AND an
origin equal to the app origin (`capacitor://localhost` in release; the
configured dev server origin when `server.url` is set). Every message type
(`message`, `js.error`, `cordova`) goes through it. If the guard cannot be
installed (`guardMissing`), every `LolaTransport` method rejects with
`internal` "bridge guard missing", so a broken guard fails closed for the
plugin that holds the session.

Version pin: `mobile/src/native/capacitorguard.test.ts` reads
`node_modules/@capacitor/ios/package.json` and fails unless the version equals
`REVIEWED_CAPACITOR_IOS = "8.5.0"`, with the message "Capacitor iOS is X; the
bridge guard was read against 8.5.0. Re-read WebViewDelegationHandler.swift and
CapacitorBridge.registerPlugins: is the `bridge` handler still added under that
name, is `webViewDelegationHandler` still public, does `load()` still run before
the page? Then update REVIEWED_CAPACITOR_IOS." It also asserts the handler file
still contains `name: handlerName` and `handlerName = "bridge"`.

Harness test (DEBUG, Simulator): route `/dev/bridge-guard` (DEBUG-only screen)
mounts a `sandbox="allow-scripts"` `data:` iframe that calls
`webkit.messageHandlers.bridge.postMessage({type:"message", pluginId:"LolaTransport", methodName:"haptic", options:{kind:"error"}, callbackId:"x"})`;
the harness asserts the device log shows `bridge-guard dropped` and not
`To Native -> LolaTransport haptic`.

`DockArea` (and therefore `Workspace`) is mounted on the phone only after this
guard has landed (work item 11 depends on item 17).

---

## 2. JS foundation: native access, shim, bridge

### 2.1 `mobile/src/native/` (new) — the only place that knows Capacitor

- `native/index.ts`: `export const native: LolaTransportPlugin | null` built
  from `registerPlugin("LolaTransport")` behind
  `Capacitor.isPluginAvailable("LolaTransport")` (the official API; never a
  `Capacitor.Plugins` probe, which is a Proxy that invents functions). Also
  `export const keyboard` from a static `import { Keyboard } from "@capacitor/keyboard"`
  behind `isPluginAvailable("Keyboard")`, and `export const browser` likewise.
- `native/web.ts`: the browser/dev fallback: `scanCapability` → `unsupported`,
  every connection method rejects `"no native transport"`, `haptic` no-op.
- Probes replaced by an import of `native` (item 3):
  `src/lib/scan.ts:81-86`, `src/lib/appstate.ts:48-53`,
  `src/lib/openurl.ts:84-123` (both probes), `src/lib/keyboardinset.ts:78-109`;
  `src/lib/devlink.ts:175,292` (item 7). `lib/discovery.ts` and
  `lib/secretstore.ts` are deleted by item 18.
- `tsconfig.json` gains `"lola-transport": ["./plugins/lola-transport/src/index.ts"]`
  so `npm run check` needs no plugin build (fixes the current
  `Cannot find module 'lola-transport'` error), and the
  `@bindings-generated/desktop` path of §2.4. `vite.config.ts` keeps resolving
  the package normally.

### 2.2 Shim layout

`mobile/src/wailsshim/desktop.ts` (948 lines) becomes a barrel mirroring the
generated `index.ts` exactly:

```ts
import * as ConfigService from "./services/config";
import * as ContextService from "./services/context";
// … one import per generated service …
export { ConfigService, ContextService, DaemonService, DoctorService, HubService, LinearService,
         PhonesService, ProjectGitService, SSHService, SandboxLoginService, TalkService, TermService, UpdateService };
export type * from "@bindings/desktop/models";   // replaces the hand list at desktop.ts:921-948
export type { PaneCloseData, PaneInfo, PaneKind, PanesData, ShellCreateData } from "../wire";
```

Files under `mobile/src/wailsshim/services/`: `config.ts`, `context.ts`,
`daemon.ts`, `doctor.ts`, `hub.ts`, `linear.ts`, `phones.ts`, `projectgit.ts`,
`ssh.ts`, `sandboxlogin.ts`, `talk.ts`, `talkimage.ts`, `term.ts`, `update.ts`,
`reasons.ts`, `routes.ts`. Each service file is plain exported functions (no
`namespace`), one per generated method, plus one exported `ROUTES` table:

```ts
// services/routes.ts
export type Route =
  | { kind: "fwd"; cmd: string }          // one request; its tier is TIER[cmd] (wire/tiers.ts), never written here
  | { kind: "local" }                      // answered on the phone
  | { kind: "platform"; reason: Reason }   // rejects UnsupportedOnMobileError(method, reason)
  | { kind: "absent" };                    // deliberately not exported; shared code feature-probes it
export type Routes<S> = Record<keyof S & string, Route>;
```

`services/reasons.ts`: `MAC_ONLY = "Only on your Mac."`,
`CONFIG = "Edit settings on your Mac."`, `NO_TIER = "This phone can't do that."`,
`MAC_LINK = "Open this link on your Mac."`.

**Tier source of truth.** `mobile/src/wire/tiers.ts` exports
`TIER: Record<string, "read"|"answer"|"control"|"destructive"|"shell"|"never">`
and `isRead(cmd) = TIER[cmd] === "read"`. `mobile/src/wire/testdata/tiers.json`
is written by the Go test `internal/remote/tiers_golden_test.go`
(`LOLA_WRITE_VECTORS=1 go test ./internal/remote -run TestTierGolden`, the same
switch spec 02's golden writer uses) from `remote.CommandTiers()`; without it
that test fails when the file differs. The Go test and the JSON belong to the
Go tiers build item (`00-build-plan.md`). `wire/tiers.test.ts` asserts `TIER` deep-equals the JSON. Every
`services/*.test.ts` asserts that each `fwd` route's `cmd` exists in `TIER` and
is not `never`, and `lib/tiers.svelte.ts` (§5.4) reads `TIER` for gating.
Nothing else in the client spells a tier.

`bridge.request` gains one rule: while `document.visibilityState === "hidden"`
every request rejects at once with `ShimNotConnectedError` (no queueing behind a
suspended socket).

### 2.3 Method table (the exact list to implement)

Request field placement follows the desktop Go service (`desktop/daemonsvc.go`,
`talksvc.go`, `reviewpackagesvc.go`): flat `session`/`text`/`project`/`ref`/
`poll`/`provider` fields where Go sets them on `protocol.Request`, otherwise
`args`. The Tier column is informative and is `TIER[cmd]` (01 §8.3); the
conformance and route tests (§2.4) are the authority, not this table.
`conformance.ts` enforces completeness, so no method counts are given.

**DaemonService**

| Method | Route | cmd | Tier | Note |
|--------|-------|-----|------|------|
| Alive | LOCAL | — | — | `bridge.alive()` |
| Sessions, Projects, Status | FWD | sessions, projects, status | read | used only by `store.refresh()`; the stream is pushed (§3) |
| Pane | FWD | pane | read | |
| PRs, Tickets | FWD | prs, tickets | read | args `{project, refresh}` / `{project, scope}` |
| Diff | FWD | diff | read | flat `session` |
| Checkpoints | FWD | checkpoints | read | |
| CheckpointDiff | FWD | checkpointDiff | read | args `{session, seq}` |
| PRReadiness | FWD | prReadiness | read | `session` + args `{number}` |
| ReviewPackages | FWD | reviewPackages | read | |
| ReviewPackageDiff | FWD | reviewPackageDiff | read | args `{from, to}` |
| ReviewPackageImage | FWD | reviewPackageImage | read | args `{package, artifact}` |
| ReviewPackageChecks | FWD | reviewPackageChecks | read | args `{package}` |
| BestOfCompare | FWD | bestOfCompare | read | |
| Answer | FWD | answer | answer | |
| SendFeedback | FWD | feedback | answer | args = `FeedbackArgs` |
| ReviewPackageMark | FWD | reviewPackageMark | answer | args `{package, previous}` |
| ReviewPackageFeedback | FWD | reviewPackageFeedback | answer | args as `reviewpackagesvc.go` builds them |
| Kill(session, force, keepWorktree) | FWD | kill | destructive | **fix arity** (3 args). `force:true` rejects locally with the existing sentence; `keepWorktree` forwarded (A7) |
| CloseExitedTemporary | FWD | closeExitedTemporary | destructive | |
| Revive, Review, CodeRabbit, ResolveConflict | FWD | revive, review, coderabbit, resolveConflict | control | |
| AddressPRFeedback | FWD | addressPRFeedback | control | `session` + args `{number}` |
| MergePR | FWD | mergePR | control | `session` + args `{number, repo, head, method, confirmUnready}` |
| SwitchAgent, Dev, DevFreePort | FWD | switchAgent, dev, devFreePort | control | |
| SessionTask | FWD | sessionTask | control | `session` + args `{argv}` |
| RenameSession(id, name) | FWD | renameSession | control | flat `session`, `text` |
| Broadcast | FWD | broadcast | control | args = `BroadcastArgs` |
| RestoreCheckpoint | FWD | restoreCheckpoint | control | args `{session, seq}` |
| ForkCheckpoint | FWD | forkCheckpoint | control | args `{session, seq, agent}` |
| BestOf, BestOfPick | FWD | bestOf, bestOfPick | control | |
| OpenManual, OpenExisting, OpenPR, OpenTicket | FWD | openManual, openExisting, openPr, openTicket | control | 01 §8.5: only `location` ""/`worktree`; `agent:false` needs `shell` (the `newSession` sheet hides "Shell only" without it) |
| Open | FWD | open | **shell** | creates a shell |
| Enable, Disable, PollOnce | FWD | enable, disable, pollOnce | control | |
| ReviewPackageRecover | FWD | reviewPackageRecover | control | |
| ReviewPackageCapture | FWD | reviewPackageCapture | **shell** | runs `node` from the worktree (01 Deviation 1) |
| ReviewPackageVerify | FWD | reviewPackageVerify | **shell** | runs `Spec.Command` |
| OpenURL(url) | LOCAL | — | — | `openOnPhone(url)` (`lib/openurl.ts` → `native.openURL`): http/https only; a loopback host (`localhost`, `127.0.0.0/8`, `::1`) rejects `MAC_LINK` (the phone reaches dev servers through the dev-links sheet's `devforward` URLs). `openURL` is `never` (01), so it is never sent. |
| Reload, RenameProject, StartDaemon, StopDaemon, RestartDaemon, CLIInfo, InstallCLI, OpenFolder | PLATFORM | — | never | `MAC_ONLY` |
| Goal*, Knowledge*, Profile*, Comparison*, Plan* | PLATFORM | — | never | `MAC_ONLY`; no phone screen mounts them |
| Panes, ShellCreate, PaneClose, PaneResize | FWD (mobile extras) | panes, shellCreate, paneClose, paneResize | read / shell | unchanged bodies from `desktop.ts:226-340` |

**TalkService**: `State`→`talkState`, `Feed`→`agentFeed`,
`FeedOutput`→`agentFeedOutput`, `Ended`→`endedSessions` (read);
`Say`→`agentSay`, `Stop`→`agentStop`, `Choose`→`agentChoose`,
`Setting`→`agentSetting`, `QueueRelease`→`agentQueueRelease` (answer; args
object passed as `args`, matching `sshCall`). Image methods (`services/talkimage.ts`):

- `AttachClipboardImage(worktree)` — LOCAL+FWD: `native.readClipboardImage()`;
  `present:false` rejects `"No image to paste."`; else `uploadImage()`.
- `AttachImageFile(worktree, src)` — PLATFORM `MAC_ONLY`: `src` is a Mac path.
- `ReadAttachmentImage(worktree, path)` — FWD `attachmentImage` (read, A14):
  resolve `session` as the live `SessionInfo` whose `worktree` equals
  `worktree`, else the `endedSessions` row whose `keptWorktree` equals it; none
  → reject `"Open on your Mac to view."`. Fetch chunks sequentially from
  `offset 0` by `ATTACH_PART_BYTES` until `eof`, check `size ≤ 20 MiB`, return
  `data:<mime>;base64,…`. An in-memory LRU of `MAX_CACHED_ATTACHMENTS = 16`
  data URLs keyed by `path` serves repeats; an upload's own bytes are inserted
  into it on success.
- `uploadImage({session, name, mime, data})` (module-private): `name` = 16
  random base64url chars; splits `data` into `ATTACH_PART_BYTES = 512 KiB` raw
  parts (= 02 `AttachChunkBytes`) and sends `attachImage` parts one at a time,
  each after the previous `resp`. A part refused with `rate_limited` is re-sent
  unchanged after `ATTACH_RATE_RETRY_MS = 1100`, at most
  `ATTACH_RATE_RETRIES = 8` times per part (so a daemon without A14's exemption
  still completes a 16-part image well inside `AttachIdleTimeout` 60 s); any
  other refusal aborts with the daemon's sentence. The thumb is built
  client-side (canvas, long edge 144 px, JPEG q=0.7, matching `imageThumb` in
  `desktop/talkimage.go`); resolves `ImageAttachment{path, thumb}`. The
  `session` comes from the open route, not from `worktree`.

**SSHService**: `Timeline(a)` → FWD `sessionTimeline` (read, A13).
`Recall(session, item)` → FWD `sshRecall` (answer, A19), args
`{session, item}`, answering `SSHRecallData`, so `QueueRows.svelte` and the
Composer's ↑ keep working through `ssh.recall`. `Answer(a)` → FWD `sshAnswer`
(answer, A19); the daemon's refusal of an SSH-scoped element shows as its
sentence. `Proposal`, `WidgetStatus` and the other ssh methods are PLATFORM
`MAC_ONLY` (SSH host acts stay on the desktop; Deviation D5). `ssh.reportWidget`
already swallows the rejection; `ssh.proposal` flashes "Only on your Mac."

**TermService**: `Attach`, `Detach`, `Write`, `Resize`, `Scroll`, `Capture`,
`CaptureMany` unchanged. `DetachAll()` LOCAL: detach every bound pane. `SetApp`
LOCAL no-op. `Shells`, `Shell`, `CloseShell`, `CloseSessionShells` PLATFORM.

**HubService**: `Status` → FWD `hubStatus` (read). All others PLATFORM
(`protocol.HubHumanCommands` are never a phone's).

**ConfigService**: `ConfigExists` LOCAL `true`. `SandboxLoginNeeded` LOCAL
resolves `""` (its type is `Promise<string>`: "" = no login needed; the daemon
refuses the open with its own sentence if one is). `GetAppearance`,
`SetAppearance`, `GetTheme`, `SetTheme`, `PreviewTheme`, `Themes` are
**absent** (`theme-runtime.svelte.ts:231-318` feature-probes them). Everything
else PLATFORM `CONFIG`, including `ConnectCode`/`RegenerateRemoteKey` until 01
deletes them from the bindings.

**PhonesService** (01 §9.3): every method PLATFORM `MAC_ONLY`.

**ContextService.Publish** LOCAL no-op. **ProjectGitService**,
**SandboxLoginService**, **LinearService**, **DoctorService**,
**UpdateService**: all PLATFORM.

### 2.4 Compile-time conformance test

New file `mobile/src/wailsshim/conformance.ts` (type-only, imported by nothing,
included by `tsconfig.json`'s `src/**/*.ts`, so `npm run check` fails on drift).
New tsconfig path: `"@bindings-generated/desktop": ["../desktop/frontend/bindings/github.com/sushidev-team/lola/desktop/index.ts"]`
(types only; no Vite alias needed because the import is `import type`).

```ts
import type * as Gen from "@bindings-generated/desktop";
import type * as Shim from "./desktop";

/** Mobile-only methods allowed on a generated namespace. */
type Extras = { DaemonService: "Panes" | "ShellCreate" | "PaneClose" | "PaneResize" };
/** Generated methods deliberately not exported (shared code feature-probes them). */
type Absent = { ConfigService: "GetAppearance" | "SetAppearance" | "GetTheme" | "SetTheme" | "PreviewTheme" | "Themes" };

type Check<S, G, X extends string = never, A extends string = never> = {
  [K in Exclude<keyof G, A>]-?: K extends keyof S
    ? S[K] extends G[K] ? true : { error: "signature differs"; method: K }
    : { error: "missing"; method: K };
} & { [K in Exclude<keyof S, keyof G | X>]: { error: "extra"; method: K } }
  & { [K in Extract<keyof S, A>]: { error: "absent method exported"; method: K } };

type AllTrue<T> = { [K in keyof T]: T[K] extends true ? true : T[K] }[keyof T] extends true ? true : false;
type Svc = keyof typeof Gen;
type X<N> = N extends keyof Extras ? Extras[N] : never;
type A<N> = N extends keyof Absent ? Absent[N] : never;
type Report = { [N in Svc]: AllTrue<Check<(typeof Shim)[N], (typeof Gen)[N], X<N>, A<N>>> };
const ok: { [N in Svc]: true } = null as unknown as Report;   // fails to compile on any drift
void ok;
/** Every generated service has a shim module. */
const services: Record<Svc, unknown> = null as unknown as typeof Shim;
void services;
```

`$CancellablePromise<T>` resolves to the shim runtime's `Promise<T>`
(`wailsshim/runtime.ts:30`), so a shim method returning `Promise<T>` is
assignable. Parameter types are checked contravariantly under
`strictFunctionTypes`, so a shim method that drops a parameter still conforms —
arity and routing are therefore enforced at runtime by
`mobile/src/wailsshim/services/*.test.ts`: every method listed in the module's
`ROUTES` (which must have exactly the generated keys, checked by
`Routes<typeof Gen.X>`), `Kill.length === 3`, every `fwd` route sends its `cmd`
with the field placement of §2.3, every `platform` route rejects
`UnsupportedOnMobileError`, every `absent` key is not exported.

### 2.5 Push bridge

`startPolling`/`pollOnce`/`pollInto` and `DEFAULT_POLL_INTERVAL_MS` are
**deleted** from `wailsshim/bridge.ts`. Three layers replace them:

**`mobile/src/wire/events.ts`** (pure, 02 §6.2's client state machine; no DOM):

```ts
export type TopicState =
  | { s: "idle" }
  | { s: "awaiting"; watchId: string }
  | { s: "live"; watchId: string; seq: number; pending?: unknown[] }   // pending: an open `more` reset
  | { s: "failed"; code: WatchCode };
export type WatchCode = "invalid_topic" | "unknown_session" | "limit" | "unavailable" | "bad_request"
  | "frame_too_large" | "rate_limited" | "denied";   // the last three end a live stream (02 §3 err frame)
export type StreamAction =
  | { a: "deliver"; payload: unknown; reset: boolean }   // one complete event (a `more` reset is assembled first)
  | { a: "rewatch" }                                     // gap or wrong first event
  | { a: "fail"; code: WatchCode }
  | { a: "drop" };
export function onWatchResult(st: TopicState, watchId: string, r: WatchResult): [TopicState, StreamAction[]];
export function onEvent(st: TopicState, topic: Topic, f: EventFrame): [TopicState, StreamAction[]];
export function onStreamErr(st: TopicState, f: ErrFrame): [TopicState, StreamAction[]]; // id == watchId → fail(code)
```

- `awaiting`: drop events whose `id` ≠ `watchId`; the first accepted event must
  have `seq == 1` and, for `sessions`/`feed:`/`timeline:`, `reset:true`, else
  `rewatch`.
- `live`: drop `id` ≠ `watchId`; `seq == last+1` → apply; anything else →
  `rewatch`.
- `sessions` and `timeline:` resets with `more:true` are buffered in `pending`
  and delivered once, merged (`upsert`/`items` concatenated, the first event's
  `events`/`connections` kept), when `more:false` arrives.
- `WatchResult ok:false` → `failed(code)`; `resp ok:false` for the whole
  request → `failed("bad_request")` for every topic in it. An `err` frame
  whose `id` is the topic's watch id → `failed(code)`.
- `omitted` ids/items are delivered to the consumer as placeholders: the
  sessions bridge fetches omitted rows with one `sessions` request; feed and
  timeline consumers show a one-line "Too large to show here." row (copy key
  `TOO_LARGE`).

**`mobile/src/wailsshim/watch.ts`** (the registry over `ChannelTransport`):

```ts
export type Topic = "sessions" | "projects" | "status" | `talk:${string}` | `feed:${string}` | `timeline:${string}` | `diff:${string}`;
export interface Sub { on(payload: unknown, reset: boolean): void; onError(code: WatchCode): void }
export function watch(topic: Topic, sub: Sub): Unsubscribe;
```

- First subscriber on a topic → `watch {topics:[topic]}`; last removal →
  `unwatch {topics:[topic]}` after `UNWATCH_DELAY_MS = 1500` (a segment flip
  does not churn).
- All `watch`/`unwatch` calls in one macrotask go out as one request each
  (≤ `MaxWatchTopicsPerRequest` 32 topics; more → several requests).
- The watch id is the request id the correlator assigned; `ChannelTransport`
  gains `onEvent(listener)`, and `event` joins `clientAcceptsFrame` in
  `wire/protocol.ts`.
- `failed(code)` calls every subscriber's `onError(code)` once. `limit` is
  retried only when a new subscriber arrives after this client unwatched some
  topic, or on the next epoch; `rate_limited` after `WATCH_RETRY_AFTER_MS`
  (30 000 = 02 `WatchRetryAfter`); `unknown_session`, `frame_too_large` and
  `denied` are never retried for that topic on this epoch; the others are
  retried on the next epoch. Watch ids are correlator request ids, which the
  correlator generates as `w<n>` (matching 02 §3's pattern).
- A new epoch (§3.3) moves every topic with subscribers to `idle` and re-watches
  them in one request.
- On background the plugin closes the socket (§1.7), which drops every watch;
  JS may also send `unwatch {all:true}` before that.

**`mobile/src/wailsshim/bridge.ts`** (global topics → the store's events):

- `sessions`, `projects`, `status` are watched while connected. The bridge emits
  `daemon:projects`, `daemon:status` per event and `daemon:alive` from transport
  phase. `daemon:pusherr` is no longer emitted. If one of these three topics
  fails (`onError`, any code), the bridge polls that command every
  `FALLBACK_POLL_MS = 2000` while foreground and connected, until the next
  epoch re-watches it (a daemon past `maxWatchingConns` must still show a
  list).
- **Keyed sessions**: the bridge keeps `Map<id, SessionInfo>` and
  `lastEvents: Event[]`. A reset replaces the map; a delta applies
  `upsert`/`remove`; `events`, when present, replaces `lastEvents`. It emits
  `daemon:sessions` with `{sessions: [...map.values()], events: lastEvents}` at
  most once per animation frame (`requestAnimationFrame` coalescing). Because
  `events` is always the held array, `store.setActivity(e.data?.events ?? [])`
  (`store.svelte.ts:212`) never clears Activity on a delta. Unchanged sessions
  keep their object identity (values are frozen plain objects, read through
  `$state.raw` in §8.2).
- Session-scoped topics (`talk:`, `feed:`, `timeline:`, `diff:`) are opened only
  through the pushed source (§3), i.e. only while a component that needs them is
  mounted.

### 2.6 Bug fixes and hygiene (with files)

**Bugs**

| # | Bug | Fix | File (item) |
|---|-----|-----|------|
| 1 | Offline snapshot empty: `store.svelte.ts:189` keeps only `s.ssh` sessions unless `retainAll`, and mobile never sets it | `store.retainAll = true;` before `mount` | `mobile/src/main.ts` (5) |
| 2 | Stale denial mirror: `DENIED_COMMANDS`/`commandDenied` (`wire/protocol.ts:459-497`) refuse locally from a copy of the old denylist; a refusal was fatal | Delete both and their call sites in `wailsshim/channeltransport.ts`; `alwaysFatalCode` (`protocol.ts:358`) stops treating `denied` as fatal (A6); a `denied`/`unknown_cmd`/`rate_limited` refusal rejects that request only | `wire/protocol.ts`, `wailsshim/channeltransport.ts`, `wire/correlator.ts` (5) |
| 3 | Activity list key `ev.id + ev.to + ev.ago` changes every minute, re-mounting rows | Key `${ev.id}:${ev.at ?? ev.to}`; render age from `ev.at` with the shared clock (§8.3); `ev.ago` only when `at` is absent | `screens/Activity.svelte` (10) |
| 4 | Terminal `geom` churn: `views/Terminal.svelte:809` assigns a fresh object on every state push, re-running every `geom` reader | Assign only when a field differs: `if (!sameGeometry(geom, st)) geom = {…}`; `sameGeometry` added to `lib/viewport.ts` | `screens/session/TerminalSegment.svelte`, `lib/viewport.ts` (12) |
| 5 | 14 `svelte-check` errors (13 shim drift + plugin module) | §2.3 and §2.1 tsconfig path | shim services (4), `tsconfig.json` (3) |
| 6 | `scanQR` returned the pairing payload through the bridge, where Capacitor logs it | `scanPairing` (§1.2) | plugin (2) |
| 7 | `daemon:pusherr` banner can no longer be produced and its dedup map leaks | remove emission; the store's `pushErrors` stays empty on mobile | `bridge.ts` (5) |
| 8 | Widgets can call native plugins (§1.9) | bridge guard | plugin (17) |

**Hygiene**

- Deletions are all in item 18 (after every importer is gone): `wire/protocol.ts`
  `HELLO_CMD`, `INSECURE_MIN_KEY_LEN`, `helloFrame`, `REMOTE_CMD_PREFIX` uses,
  `DENIED_COMMANDS` (item 5 removes these inside its own file); `lib/secretstore.ts`
  (+test); `lib/discovery.ts` (+test); `lib/endpoint.ts` (+test);
  `lib/pairpayload.ts` (+test; parsing is native — the TS side keeps only the
  `invalid` → copy mapping in `lib/paircopy.ts`); `lib/testdata/connectcode.json`;
  `lib/nav.svelte.ts`, `lib/sheets.ts` (+tests); `views/*`; superseded
  `lib/components/{SessionCard,SessionRow,FilterRail,TriageChips}.svelte` (+tests).
  `scripts/dev-link.sh` loses its key/keyfile options (item 7).
- `FRAME_VERSION_CURRENT` becomes 2, `FRAME_VERSION_MIN` 2 (plan §4, 02 §2).
- One home for shared constants: `mobile/src/wire/limits.ts` exports
  `MAX_FRAME_BYTES`, `DEFAULT_REMOTE_PORT`, `MAX_REQUESTS_IN_FLIGHT`,
  `PANE_QUEUE_DEPTH`, `MAX_SCROLL_LINES`, `MAX_PANE_NAME`, and 02's
  `MAX_WATCH_TOPICS_PER_REQUEST`, `MAX_WATCH_TOPICS_PER_CONN`,
  `ATTACH_CHUNK_BYTES`; `wire/protocol.ts`' copies are removed;
  `limits.test.ts` asserts equality with the plugin's
  `LOLA_MAX_FRAME_BYTES`/`LOLA_DEFAULT_PORT` and with the Go values via
  `mobile/src/wire/testdata/` golden JSON.
- Comments: every header comment that narrates history (milestone names, "used
  to", debugging stories) is cut to the invariant it protects, ≤ 6 lines. Files:
  `App.svelte`, `lib/devlink.ts`, `plugins/lola-transport/src/definitions.ts`
  (rewritten), `wailsshim/*.ts`, `vite.config.ts` alias block (keep the ORDER
  rule, drop the essay).
- Split files over ~500 lines along their seams:
  `wailsshim/desktop.ts` (§2.2); `lib/connection.svelte.ts` → `connection.svelte.ts`
  (phase, path, diagnosis; ≤ 300) + `resume.ts` (§3.3); `views/Terminal.svelte`
  (956) → `screens/session/TerminalSegment.svelte` + `lib/terminalgeometry.svelte.ts`;
  `lib/components/MobileTerminal.svelte` (895) → view + `lib/terminalinput.ts`
  (accessory bytes, gestures); `lib/components/PaneTabs.svelte` (847) → strip +
  `lib/panetabs.svelte.ts`; `lib/panepin.ts` (727) → `panepin.ts` (lifecycle) +
  `panepinstore.ts` (persistence); `wire/protocol.ts` (809) → `protocol.ts`
  (frames) + `limits.ts`; `wire/codec.ts` (594) → `codec.ts` + `codecjson.ts`;
  `wailsshim/channeltransport.ts` (653) → `channeltransport.ts` + `panesubs.ts`;
  `views/TicketPicker.svelte` (572), `views/ProjectDetail.svelte` (585) → screen
  + `lib/` model. `views/Connect.svelte` (563) is rewritten (§5.2).
- `mobile/README.md` and `mobile/HANDOFF.md` rewritten to the new truth: no
  build tag, pairing by QR, Direct/Relay, the deep-link harness of §5.7, the
  budgets of §8 and how to run them. `mobile/PLAN.md` gets a two-line header
  pointing at `docs/mobile-rethink.md` and `docs/mobile/`.

---

## 3. The pushed-source seam in desktop stores

Goal: the desktop keeps polling exactly as today; the phone drives the same
stores from events. No desktop behaviour changes when no source is installed.

### 3.1 New desktop module `desktop/frontend/src/lib/pushsource.ts`

Structural types only, so the desktop needs no mobile import; `wire/push.ts`'s
02 types must be assignable to them (`mobile/src/lib/pushsource.test.ts`
asserts it with `satisfies`).

```ts
import type { AgentFeedItem, SSHTimelineData, TalkStateData } from "@bindings/internal/protocol";

export type PushError = "invalid_topic" | "unknown_session" | "limit" | "unavailable" | "bad_request"
  | "frame_too_large" | "rate_limited" | "denied";
export type FeedPush = { reset: boolean; older: boolean; epoch: string; lastSeq: number; items: AgentFeedItem[]; scanning: boolean; health: string };
export interface PushSource {
  talk(session: string, on: (d: TalkStateData) => void, onError: (e: PushError) => void): () => void;
  feed(session: string, on: (p: FeedPush) => void, onError: (e: PushError) => void): () => void;
  /** `d` is a whole SSHTimelineData on reset, a delta (`items` to upsert, `connections` when changed) otherwise. */
  timeline(session: string, on: (d: SSHTimelineData, reset: boolean) => void, onError: (e: PushError) => void): () => void;
  diff(session: string, on: (rev: number) => void, onError: (e: PushError) => void): () => void;
}
let current: PushSource | null = null;
/** Mobile installs one at boot. The desktop never does, so its stores poll as before. */
export function setPushSource(s: PushSource | null): void { current = s; }
export function pushSource(): PushSource | null { return current; }
```

**Rule for every store:** `onError` reverts that store, for that session and
for the rest of the mount, to its own desktop polling path (the code that runs
when `pushSource()` is null), except `unknown_session`, which marks it ended
(`talk`: `unsupported = true`; `diff`: no-op; `feed`/`timeline` accept ended
sessions, so they do not get it). A refused topic therefore degrades to polling,
never to an endless spinner.

### 3.2 Edits, by file (each is the whole change)

1. **`desktop/frontend/src/lib/talk.svelte.ts`**
   - Extract the success branch of `refresh` (lines 121-124) into
     `private accept(t: SessionTalk, d: TalkStateData)` which sets `t.data`,
     clears `unsupported`/`error`; `refresh` calls it.
   - Add `stopPush: (() => void) | undefined` to `SessionTalk`.
   - In `poll(id)`, inside `if (t.pollers === 1)`: `const src = pushSource(); if (src) { t.stopPush = src.talk(id, (d) => { this.accept(t, d); t.answered++; this.changed(); }, (e) => this.pushFailed(t, id, e)); } else { this.startPolling(t, id); }`
     where `startPolling` is the existing refresh + `setInterval` body, moved
     verbatim. `pushFailed` runs `startPolling` (or sets `unsupported` for
     `unknown_session`). The teardown branch calls `t.stopPush?.()` beside
     `clearInterval`.
   - The post-press `void this.refresh(id)` calls (lines ~213 and ~247, after
     `choose`/`setting`) run only when `t.stopPush` is unset: with a live push
     the daemon publishes the new state itself (02 §4: sends stale `talk`).
2. **`desktop/frontend/src/lib/feed.svelte.ts`**
   - Add `following = false` (plain field) and `follow(): () => void` to
     `SessionFeed`: returns a no-op when `pushSource()` is null; otherwise sets
     `following = true` and subscribes
     `src.feed(this.id, (p) => this.absorb(p), () => { this.following = false; void this.fetch(); })`;
     the returned stop sets `following = false`.
   - Add `private absorb(p: FeedPush)`: `p.reset` or `p.epoch !== this.epoch` →
     the tail branch of `fetch` (lines 85-90: `items = p.items`, `more = p.older`,
     `lastSeq = p.lastSeq`, `target = lastSeq`); else `items = mergeFeed(items, p.items)`,
     `lastSeq = max(lastSeq, p.lastSeq)`, `target = lastSeq`. Sets `epoch`,
     `scanning`, `health`, `loaded = true`, `error = ""`.
   - `sync()` gains a first line `if (this.following) return;`. `fetch()` is
     unchanged and, while following, is reached by nothing (`older()`,
     `output()`, `thread()` call `talk.feed` directly and stay as they are).
3. **`desktop/frontend/src/lib/components/session/Timeline.svelte`**
   - Beside the `f.sync` effect (line ~57): `$effect(() => f.follow());`.
   - The `FEED_SCAN_POLL_MS` effect (lines 61-65) gains a first line
     `if (f.following) return;` — 02 republishes resets while scanning.
4. **`desktop/frontend/src/lib/ssh.svelte.ts`**
   - `SessionView`: extract the body of the `while` loop in `fetch` (lines
     459-468) into `private absorb(r: SSHTimelineData)`. Add `pushed = false`
     (plain field) and `follow(): () => void`: no-op without a source; otherwise
     `this.pushed = true`, subscribe
     `src.timeline(this.id, (r, reset) => { if (reset) this.resetIfBehind(0); this.absorb(r); this.loaded = true; this.fetchedOk = true; }, () => { this.pushed = false; void this.fetch(); })`;
     the stop sets `pushed = false`.
   - `speak()` (lines 1115-1121): `void v.fetch(); void talk.refresh(session);`
     run only when `!v.pushed`. The catch branch's `void v.fetch()` likewise.
     With push the daemon publishes the echo on `timeline:`/`talk:`.
5. **`desktop/frontend/src/lib/components/session/Workspace.svelte`**
   - Add `$effect(() => { if (sessionId) return view.follow(); });` next to the
     `talk.poll` effect (line 79).
   - The three interval effects (lines 58-78) and the seq effect (47-55) gain a
     first line `if (view.pushed) return;`. With no source `pushed` is always
     false, so the desktop path is byte-identical in behaviour.
6. **`desktop/frontend/src/lib/changes.svelte.ts`** — not edited. The phone's
   Changes segment calls `changes.load(id)` when the `diff:<id>` source fires
   (§5.2); the desktop keeps loading on agent rest (`SessionEmbed.svelte:134-139`).
7. **`desktop/frontend/src/lib/composerattach.ts`** (new, 10 lines):
   `export let imagePicker: (() => Promise<ImageAttachment | null>) | null = null; export function setImagePicker(f) { imagePicker = f; }`.
   **`desktop/frontend/src/lib/components/session/Composer.svelte`**: when
   `imagePicker` is non-null, render one `<Button icon aria-label="Attach image">`
   with `AttachGlyph` in the composer's control row, calling
   `prepareImage(imagePicker)`; `prepareImage` (line 220) treats a `null` result
   as cancel: delete the pending token (`view.pastes.delete(n)` and remove the
   token from the draft, the same code `removeAttachment` runs). Desktop never
   sets a picker, so nothing renders there.
8. **`desktop/frontend/src/lib/components/ssh/SSHWidget.svelte`** — widgets
   never run on a phone (01 §8.6, §10.1): `sessionTimeline` delivers elements
   without `html`/`data`. On a client that called `setWidgetsUnavailable(true)`
   (`$lib/widgetsoff`, set by the phone at boot) the component renders one line
   `<p class="px-3 py-2 text-sm text-faint" data-widget-unavailable>Open on your
   Mac to view.</p>` instead of the iframe, sends no `widgetStatus`, arms no
   boot check and opens no `MessageChannel`. The desktop never sets it, so a
   widget whose `html` is empty still frames there. Test: `SSHWidget.test.ts`.

Tests (desktop, vitest):
- `talk.svelte.test.ts` — with a fake source, `poll` issues zero
  `TalkService.State` calls and applies pushed states; `choose` with a live push
  makes no `State` call; `onError("limit")` starts the interval path;
  `unknown_session` sets `unsupported`. Without a source, existing tests unchanged.
- `feed.svelte.test.ts` — `absorb` merge, epoch reset, snapshot; **while
  following, `sync(higherSeq)` (a talk event carrying a newer `feedSeq`) makes
  zero `agentFeed` calls**; `onError` reverts to `fetch`.
- `Timeline.test.ts` — scanning + following starts no interval.
- ssh view — `follow` + `absorb`; `speak` with `pushed` makes exactly one
  `agentSay` and zero `sessionTimeline`/`talkState` calls.
- `Composer.test.ts` — no attach button by default; with a picker, `null`
  leaves no token.
- `SSHWidget.test.ts` — item 8.

### 3.3 Mobile installs the source; resume

`mobile/src/lib/pushsource.ts` (new) implements `PushSource` over `watch()`
(§2.5): `talk` → `talk:<id>`, `feed` → `feed:<id>` (maps `FeedEvent` to
`FeedPush`), `timeline` → `timeline:<id>` (maps `TimelineEvent` to
`SSHTimelineData{session, items, lastSeq, connections}`, keeping the last
`connections` when an event omits them), `diff` → `diff:<id>` (passes `rev`).
Installed in `main.ts` with `setPushSource(mobilePush)` before `mount`.

`mobile/src/lib/resume.ts` (new): on `state{phase:"ready"}` for a new epoch
(first connect, reconnect, or adopted migration): `installTransport` binds the
new epoch; the watch registry re-sends `watch` for every topic with subscribers
(one request); every bound pane re-subscribes with its current viewport
(`bridge.attachPane` per binding). Resets replace state; nothing merges across
epochs.

Requests in flight on a closed epoch reject with `ConnectionClosedError`. A
request is re-sent once on the new epoch **only if `isRead(cmd)`**
(`wire/tiers.ts`, i.e. exactly 01's `read` tier). Every other command —
`answer` (`agentSay`, `agentChoose`, `answer`, `feedback`, `attachImage`, …),
`control`, `destructive`, `shell`, and anything absent from `TIER` — is never
re-sent: its caller gets `ConnectionClosedError`. The same `isRead` decides
whether a request counts as in-flight mutating for `adopt` (§1.7).

---

## 4. Desktop components in a 390 pt column

### 4.1 Mount map

| Component | Verdict | Why it works / what changes |
|-----------|---------|-----------------------------|
| `session/Workspace.svelte` | **unchanged** (+ §3.2 seam) | `SIDE_MIN_WIDTH = 1180` never met → no sidebar, compact drawer. `covered=false`, `changesInSide=false`, no `header` snippet. `HostBar`/`SSHTerminalPane` render only for ssh sessions with connections; `view.narrow` is never set to `terminal` on the phone, so no host shell mounts. |
| `TimelineSlot`, `Timeline`, `TimelineTurn`, `StoryRow`, `ToolRow`, `ToolOutput`, `WorkRun`, `DigLine`, `Receipt`, `MessageGallery`, `SystemMessage`, `Moment` | unchanged | `.cv-*` container queries at `max-width: 619.98px` and `600px` give the narrow layout (24/16 px gutters, single-column ledgers, person bubble full width). IntersectionObserver placeholders already virtualize turns. `MessageGallery` reads images through `ReadAttachmentImage` → `attachmentImage`. |
| `ActionZone`, `Composer`, `QueueRows`, `Tray`, `FirstOpen`, `SlashList`, `ModelSettings` | unchanged (+ §3.2 attach) | `.ssh-col` rows `auto minmax(72px,1fr) minmax(0,auto) auto`; `@container (max-height: 520px/420px)` handle the keyboard-shortened column. Recall works through `sshRecall` (A19). A `!` line is not a phone feature: the daemon defuses a leading `!` on every surface (01 Deviation 11). |
| Cards: `PermissionCard`, `QuestionCard`, `ModalCard`, `ChoiceCard`, `BackgroundCard`, `InlineTerminalCard`, `WidgetCard` | unchanged | "A question on a narrow card keeps all its answers" rule already exists. `InlineTerminalCard` attaches through the shim's `TermService`. |
| `../ssh/dock/DockArea.svelte` + widgets | unchanged; `SSHWidget` per §3.2 item 8 | Elements arrive without `html`/`data` (A13), so no widget document ever runs on the phone; `SSHWidget` shows "Open on your Mac to view." The bridge guard (§1.9) is defence in depth for any frame; Workspace (and so DockArea) is still mounted only after item 17 lands. Element answers go through `sshAnswer` (A19); proposals and widget status are `MAC_ONLY` (D5). |
| `ChangesView` (`compact`) + `DiffView` (`compact`) | unchanged | `compact` gives the one-column body (`.diff-compact .diff-body`) and the file `Select`. A line comment is DiffView's own gutter click → its inline editor (single line on the phone; range selection is shift-click, D6). `nav.openSession(id)` after Fork is mirrored by the desk-nav adapter (§4.2). |
| `BeforeMerge` (`reviewLayout`) | unchanged | Its "Review PR" calls `reviewWorkspace.open` → desktop `nav.overlay = 'pr-review'`, which the adapter maps to the Review package sheet. |
| `ReviewWorkspace` | unchanged, mounted in the full-screen `reviewPackage` sheet when `deskNav.overlay === 'pr-review'` | It is a `Modal fullscreen`; phone CSS (§4.3) removes the macOS traffic-light padding. Capture and Verify need `shell` (01 Deviation 1). |
| `ConfirmDialog` | unchanged, mounted once at the root of `mobile/src/App.svelte` | `Modal` already has `@media (max-width:480px)` full-width actions. z-index 40 sits above sheets (30). |
| `SessionEmbed`, `ViewStrip`, `SessionFooter`, `SessionBar`, `SessionSide`, `TerminalCell`, `SessionMenu`, `MainTopBar`, sidebar/roster components | **replaced** | phone chrome: `SessionHeader`, `SegmentBar`, `sheets/sessionMenu.svelte`, `TerminalSegment` (existing `MobileTerminal` + `PaneTabs` + `AccessoryBar`) |
| `LiveTerminal` | replaced by `MobileTerminal` | touch scroll, pin lifecycle, accessory bar |
| `ProjectGit*`, `Goal*`, `Knowledge*`, settings views, hub views | not mounted | desktop only |

### 4.2 Desk-nav adapter (`mobile/src/lib/desknav.ts`, new)

The shared components read the desktop `$lib/nav.svelte` singleton. The phone
writes it from its own route and reads two signals back:

- Route enters `session/:id` → `deskNav.view = "cockpit"`, `deskNav.selectedId = id`,
  `deskNav.focusedTerm = id` (enables `Timeline`'s window keys and `Tray`'s
  `canControl`); leaving clears `focusedTerm`. `deskNav.markSeen(id)` when the
  Conversation segment is visible and `agentStateSince` moves (the same effect
  as `SessionEmbed.svelte:124-129`).
- `deskNav.overlay === "pr-review"` → open sheet `reviewPackage`; closing the
  sheet calls `deskNav.closeOverlay()`.
- `deskNav.focusedTerm` changed by a component to another id (Fork,
  `nav.openSession`) → `router.push(/session/<id>/conversation)`.

### 4.3 Phone CSS scope (`mobile/src/app.css`)

All desktop components on a phone screen sit under `[data-phone-desk]` (the
Session screen root):

```css
[data-phone-desk] {
  --text-sm: 0.8125rem; --text-sm--line-height: 1.125rem;   /* 13/18 metadata */
  --text-base: 0.9375rem; --text-base--line-height: 1.375rem;/* 15/22 */
  --text-lg: 1.0625rem; --text-lg--line-height: 1.5rem;      /* 17/24 */
}
[data-phone-desk] [data-conversation] { font-size: 1rem; line-height: 1.5rem; } /* 16/24 prose */
[data-phone-desk] textarea, [data-phone-desk] input { font-size: max(16px, 1rem); } /* no iOS focus zoom */
[data-phone-desk] .modal-header.drag { padding-left: 16px; }  /* no traffic lights */
[data-phone-desk] .diff-row { font-size: 12px; }               /* 3rem gutters + code fit at 390 */

/* Hit area: grow the TOUCH target, never the box. Button.svelte's BASE class list
   contains `cursor-pointer`; xs (24 px) + 2×10 = 44, sm (28 px) + 2×8 = 44. */
[data-phone-desk] button.cursor-pointer { position: relative; }
[data-phone-desk] button.cursor-pointer::after { content: ""; position: absolute; inset: -10px; }
/* Dense rows keep their own targets: one-per-line gutters and inline row buttons. */
[data-phone-desk] :is(.diff-body, .line-gutter, [data-queue-row]) button::after,
[data-phone-desk] button.line-gutter::after { content: none; }
```

No rule sets `min-height` on generic controls: DiffView renders a `<button
class="line-gutter">` per line (`DiffView.svelte:499-535`), and QueueRows uses
`h-5! w-5!` icon buttons. A 44 px floor would triple the 2 000-line fixture's
height. The `::after` is transparent and carries no glyph, so the "glyphs are
sibling `<svg>`s" rule is untouched. `[data-queue-row]` is added as an
attribute by item 6 to the row element in `QueueRows.svelte` (one attribute,
desktop behaviour unchanged). `phonecss.test.ts` parses `app.css` and fails if
any `[data-phone-desk]` rule sets `min-height` or `height` on `button`, and if
the exclusion selector loses `.line-gutter` or `.diff-body`.

Tailwind v4 utilities read `var(--text-*)`, so redefining the variables on a
container rescales every shared utility without touching a desktop file. `rem`
follows `dynamictype.ts`' root size, so Dynamic Type applies.

### 4.4 Keyboard avoidance for the Composer (WKWebView)

Facts: `capacitor.config.ts` sets `Keyboard.resize = None` and
`ios.scrollEnabled = false`; `lib/keyboardinset.ts` already reports the
keyboard height from `@capacitor/keyboard` events with a `visualViewport`
fallback.

- The Session screen root is a grid `grid-template-rows: auto auto minmax(0,1fr)`
  (header, segment bar, body) with `padding-bottom: var(--kb, 0px)`. The body
  holds Workspace's `[container-type:size]` column, so the column's height
  shrinks by the keyboard and the desktop's `@container (max-height: …)` rules
  do the rest (story line hides at ≤ 520; a decision takes the story's row at
  ≤ 420). Measured: iPhone 15 (844 pt) column ≈ 667 pt; with the 336 pt
  keyboard ≈ 331 pt → the 420 rule applies, which is the intended "decide now"
  layout.
- `--kb = max(0, keyboardHeight − env(safe-area-inset-bottom))`, set on
  `document.documentElement` by `installKeyboardInset`; the transition is
  `padding-bottom 250ms cubic-bezier(0.17, 0.59, 0.4, 0.77)` (the iOS keyboard
  curve), none under reduced motion.
- `focusin` on a textarea inside `[data-phone-desk]` → `window.scrollTo(0, 0)`
  on the next frame (WebKit scrolls the document to reveal the caret even with
  scrolling disabled), and the Timeline scroller keeps its bottom anchor (its
  own stick-to-bottom logic).
- The tab bar is never drawn on the Session screen; nothing else shares the
  bottom edge.

### 4.5 localStorage on the phone

Per-viewer conveniences only, every access already wrapped in try/catch in the
desktop code. Keys a phone writes: `lola.seen` (nav), `lola.read.<id>`
(Timeline), `lola.diff.drafts` (feedback queue), `lola.diff.mode` (seeded to
`"unified"` on first boot by `mobile/src/lib/deskprefs.ts` when absent),
`lola.mergeMethod` (BeforeMerge), ReviewWorkspace draft key, `lola.ssh.dock`
and ssh drafts (ssh.svelte.ts), the theme cache, and the phone's own
`lib/prefs.ts`, `lib/daemonname.ts` (display names), `lib/panepin.ts`. Nothing
that identifies or authenticates a daemon is in localStorage: the pairing
record and relay credentials are in the Keychain (§1.5).
`connection.svelte.ts`'s endpoint persistence is deleted.

### 4.6 Polls that remain on the phone

With the pushed source installed, the only intervals left while the app is
foreground:

| Interval | Where | Mounted when | Gate |
|----------|-------|-------------|------|
| `sshClock` 1 s | `ssh.svelte.ts:56` | a running block is on screen | subscriber-counted |
| BeforeMerge 60 s | `BeforeMerge.svelte:119` | Review segment visible | `visibilityState` |
| ReviewWorkspace 60 s, ReviewVerification 3 s | review sheet | sheet open | visibility / busy |
| phone `minuteClock` | `mobile/src/lib/clock.svelte.ts` | any relative time on screen | subscriber-counted |
| a store's desktop poll | `talk`/`feed`/`timeline` | only after its topic was refused (§3.1 rule) | as on the desktop |

`FEED_SCAN_POLL_MS` does not run while following (§3.2 item 3). `talk.poll`'s
`document.hasFocus()` gate is never reached on the phone unless a topic was
refused. Every other request comes from a human press.

---

## 5. Information architecture

### 5.1 Router (`mobile/src/lib/router.svelte.ts`, replaces `lib/nav.svelte.ts` and `lib/sheets.ts`)

```ts
export type Seg = "conversation" | "changes" | "review" | "terminal";
export type Tab = "inbox" | "projects" | "activity" | "settings";
export type Route =
  | { name: "connect"; state: "scan" | "code" | "refused" | "expired" | "failed" | "dropped" | "not-paired" | "no-camera" }
  | { name: "tab"; tab: Tab; project?: string; pick?: "prs" | "tickets"; filter?: InboxFilter }
  | { name: "session"; id: string; seg: Seg; pane?: string }
  | { name: "dev"; page: "bridge-guard" };                  // DEBUG builds only; parseRoute returns null in release
export type InboxFilter = "" | "needs" | "working" | "review" | "done";
export const SHEETS = ["sessionMenu", "newSession", "attach", "model", "merge", "feedback",
  "checkpoints", "bestOf", "devLinks", "rename", "broadcast", "tasks", "switchAgent", "paneMenu",
  "viewSettings", "filter", "connection", "reviewPackage", "unpair"] as const;
export type SheetName = (typeof SHEETS)[number] | "";
class Router {
  stack = $state<Route[]>([{ name: "tab", tab: "inbox" }]);
  sheet = $state<SheetName>("");
  get top(): Route;
  push(r: Route): void;      // session over a tab
  replace(r: Route): void;   // segment change, tab change
  back(): void;              // pop, or tab-level up (pick → project → list); never to connect
  open(s: SheetName): void; close(): void;
}
export function parseRoute(path: string): Route | null;  // pure, fails closed
export function formatRoute(r: Route): string;
```

Path grammar (shared by dev links, preview fixtures and tests):

```
/connect | /connect/{code|refused|expired|failed|dropped|not-paired|no-camera}
/inbox[?filter=needs|working|review|done]
/session/<id>/{conversation|changes|review|terminal}[/<pane>]
/projects | /projects/<name> | /projects/<name>/{prs|tickets}
/activity | /settings
/dev/bridge-guard                       (DEBUG only)
query: sheet=<SheetName>
```

Unknown segments return `null` (caller keeps the current route). `/connect/*`
is accepted only when no daemon is paired (otherwise → `/inbox`).

Nav model: four tabs in the bottom bar (Inbox, Projects, Activity, Settings),
each keeping its own depth. Session is pushed over the current tab; back
returns to it. Left-edge swipe (start x ≤ 20 pt, pointer events) pops with the
spring of §6.4; it is disabled on the Terminal segment, which owns its touches.
Losing the connection never routes anywhere (unchanged rule from today's
`App.svelte`); only `not_paired` and an explicit Unpair lead to Connect.
`key_changed` shows a persistent warning banner with "Pair again" and keeps the
current screen.

### 5.2 Screens

Directory: `mobile/src/screens/` (replaces `mobile/src/views/`).

**Connect** (`screens/Connect.svelte`, `lib/pairing.svelte.ts`)
- `scan`: full-bleed viewfinder (native scanner presented on appear), one line.
  No camera (Simulator) or denied → `no-camera` state.
- `code`: the 6-digit code in a 34 pt mono, grouped `123 456`, one line telling
  the human to type it on the Mac, and a Cancel. The plugin keeps the screen
  awake (§1.5).
- `refused` / `expired` / `failed` / `dropped` / `not-paired`: title, one line,
  one button.
- Pairing done → `success` haptic, `router.replace(/inbox)`.

**Inbox** (`screens/Inbox.svelte`, `lib/inbox.svelte.ts`)
- Cards ("Needs you"): sessions with `kanbanKey ∈ {needs, fixing}` or
  `delivery === "approved"`, ordered by `sortRank`. Card = title (2 lines max),
  project · branch, state line, **one** action:

  | Condition (first match) | Card body | Action | Does |
  |------|------|--------|------|
  | `waiting_input`, watched `talk.prompt.kind === "permission"`, and exactly one choice whose `label.trim()` matches `^Yes$` | `prompt.lines` (clamped to 6 lines, "Show all" expands in place) | Approve | `talk.choose(id, prompt.hash, yes.key)` with the hash of the SAME `TalkStateData` whose lines are on screen (`medium` haptic) |
  | `waiting_input` otherwise (other prompt kinds, no exact `Yes`, unwatched) | state line | Answer | push `/session/<id>/conversation` (the shared card shows the full request) |
  | `merge_conflict` | state line | Resolve | `store.resolveConflict(id)` |
  | `ci_failed` | state line | See checks | push `/session/<id>/review` |
  | `changes_requested` | state line | Address | `DaemonService.AddressPRFeedback(id, number)` |
  | `approved` | state line | Merge | open `merge` sheet |

  `approveChoice(prompt): AgentChoice | null` (in `lib/inbox.svelte.ts`) is the
  one rule: exact `^Yes$` label, exactly one match; never a label containing
  "don't ask", "always", "session" or any other text. If the card's state
  changes while the human reads it (new hash), the card re-renders the new lines
  before Approve is enabled again (disabled for `APPROVE_SETTLE_MS = 600` after
  a hash change). The daemon still re-reads the pane against the hash
  (`agentChoose`).

  The Inbox watches `talk:<id>` only for the first `MAX_INBOX_TALK_WATCH = 3`
  needs cards, and only while it is the top route (02
  `MaxProbedSessionsPerDevice` = 4 leaves a slot for an open session); the rest
  show Answer.
- Rows below, one dense row per session (56 pt, two lines): sections Working,
  In review, Done. Done shows `DONE_VISIBLE = 20` rows then "Show all".
- Filter chips (`filter` sheet on small widths) map to `InboxFilter`.
- No pull to refresh.

**Session** (`screens/session/Session.svelte`)
- Header (44 pt): back, title (1 line), state glyph, `…` → `sessionMenu`. The
  **Model** control lives in the session menu (row "Model" → `model` sheet), not
  in the header or the Conversation.
- `SegmentBar`: Conversation · Changes · Review · Terminal (content-sized
  segments, 13 pt semibold). Review is hidden when the session has no PR and
  no review packages; Terminal hidden when there is no pane and no shell.
- Conversation: `<Workspace sessionId />` under `[data-phone-desk]`.
- Changes: `<ChangesView sessionId compact />` (lazy chunk). Watches
  `diff:<id>` while visible and calls `changes.load(id)` when `rev` changes,
  debounced 500 ms.
- Review: `<BeforeMerge session reviewLayout />` at the top, then a
  "Review packages (n)" row (opens `reviewPackage`), then a **"Files changed"**
  row that switches to the Changes segment when the session offers Changes (the
  diff is not repeated in Review). A session without a Changes segment keeps
  `<DiffView sessionId compact />` here. Sticky bar: "Send feedback (n)" (n =
  `feedback` drafts for the session; opens `feedback`) and "Merge" (opens
  `merge`; disabled with the readiness reason when not mergeable). A line
  comment is a tap on DiffView's line gutter, which opens DiffView's own inline
  editor (single line; `cmd=feedback` queues, never forces).
- Terminal: existing `MobileTerminal` + `PaneTabs` + `AccessoryBar` (lazy
  chunk). Attached only while this segment is visible; switching away detaches
  (AGENTS.md: "Terminal attaches only while on screen").

**Projects** (`screens/Projects.svelte`, `ProjectDetail.svelte`,
`PRPicker.svelte`, `TicketPicker.svelte`): the current screens, split per §2.6;
detail actions open PR / ticket / worktree (`newSession` sheet for the
worktree form: project, agent, prompt; "Shell only" shown only with `shell`).

**Activity** (`screens/Activity.svelte`): `store.activity`, keyed per bug 3,
virtualized.

**Settings** (`screens/Settings.svelte`): This phone (name), Mac (name,
rename), Connection (Direct · Wi-Fi / Relay; opens `connection` sheet with the
Details disclosure: path, epoch, last race ms, relay state, counters), Theme,
About (version, budgets of last launch), Unpair (destructive).

### 5.3 Sheets

`lib/components/Sheet.svelte` is rewritten with detents. Each sheet is one
component in `mobile/src/sheets/`. **Sheets default to the `fit` detent**: the
panel is as tall as its content, capped at 100 % − top inset − 44 pt, and the
content scrolls inside. A titled sheet draws a 56 pt header under the grab handle
(cancel | headline title | **Done**, or a bold accent `action` that dims to 40 %
when disabled). The panel is pinned to the screen bottom and lifted by `--kb`.
`reviewPackage`, `checkpoints`, `bestOf` and `newSession` stay `large` (or
`full`); `medium` in the table below is the maximum for the others, which now
open `fit`.

| Sheet | Detents | Content | Commands |
|-------|---------|---------|----------|
| `sessionMenu` | medium | Rename, Switch agent, Dev server (toggle), Open dev URL, Tasks, Checkpoints, Compare attempts, Broadcast, Review with…, CodeRabbit, Revive, Resolve conflict, Remove session. Rows hidden without the tier (§5.4). | renameSession, switchAgent, dev, sessionTask, checkpoints, bestOfCompare, broadcast, review, coderabbit, revive, resolveConflict, kill; dev URLs open on the phone via `devforward` |
| `newSession` | large | project, PR / ticket / worktree, agent, prompt; "Shell only" needs `shell` | openPr, openTicket, openManual |
| `attach` | medium | Camera, Photos, Paste | `native.pickImage`, `readClipboardImage` → `uploadImage` |
| `model` | medium | `ModelSettings` (shared) | agentSetting |
| `merge` | medium | method (Squash/Merge/Rebase from `lola.mergeMethod`), readiness summary, Merge; unready → "Merge anyway" (second confirm) | mergePR (`confirmUnready`) |
| `feedback` | large | pending line comments (`feedback.svelte.ts`), note, Send | feedback |
| `checkpoints` | large | list, diff preview, Restore, Fork | checkpoints, checkpointDiff, restoreCheckpoint, forkCheckpoint |
| `bestOf` | large | attempts compared, Pick | bestOfCompare, bestOfPick |
| `devLinks` | medium | existing `DevLinksSheet` | — |
| `rename`, `broadcast`, `tasks`, `switchAgent` | medium | one field / list each | as named |
| `paneMenu`, `viewSettings` | medium | existing terminal sheets | paneClose, paneResize |
| `filter` | medium | Inbox filter (existing `FilterSheet`) | — |
| `connection` | medium | path details | — |
| `reviewPackage` | full | `ReviewWorkspace` | reviewPackage* (Verify needs `shell`) |
| `unpair` | — (ConfirmDialog) | | `native.forget` |

Destructive confirmations always use the shared `confirm` store and
`ConfirmDialog`, never a sheet.

**Sheet footer and discard.** A sheet may pin a `footer` for non-text decisions
(Mark reviewed, Restore, Pick, Merge): it sits above the home indicator and does
not scroll. Text-entry sheets keep `Cancel | title | action` in the header and ask
"Discard changes?" (`copy.sheet.discard`) when closed while dirty. `newSession`
opens at the `fit` detent.

### 5.4 Tier-gated controls

`lib/tiers.svelte.ts` holds the device's tiers: set from `connect()`'s
`device.tiers` (A4) and replaced on every `device` listener event, which the
plugin emits on each new epoch's `ready` (a tier edit on the Mac closes the
connection, A5, so the reconnect brings the new set). `can(cmd)` = the device holds `TIER[cmd]`
(`wire/tiers.ts`); `TIER[cmd] === "never"` or unknown → false. A control whose
command fails `can` is not rendered (menus) or disabled with "This phone can't
do that." (sticky bar). A refusal from the daemon is still shown as its
sentence; the client list is a courtesy, the daemon gate is the truth.

### 5.5 Copy (every string; titles ≤ 3 words, body ≤ 1 short sentence)

| Screen / state | Title | Body / label |
|---|---|---|
| Connect · scan | Pair with Mac | Scan the code in Lola on your Mac. |
| Connect · no-camera | Can't scan | This device has no camera. |
| Connect · camera denied | Camera off | Allow camera access in Settings. · button "Open Settings" |
| Connect · invalid scan | Not a Lola code | Scan the code on your Mac. |
| Connect · old code | Update Lola | This code is from an older Lola. |
| Connect · code | Enter this code | On your Mac. · status "Waiting for your Mac…" · "Cancel" |
| Connect · refused | Pairing refused | Make a new code on your Mac. · "Scan new code" |
| Connect · expired | Code expired | Make a new code on your Mac. · "Scan new code" |
| Connect · failed | Pairing failed | Make a new code on your Mac. · "Scan new code" |
| Connect · dropped | Connection lost | Your Mac stopped answering. · "Scan again" |
| Connect · not-paired | Phone removed | Pair it again from your Mac. · "Pair again" |
| Banner · key_changed | — | {mac}'s identity changed. · "Pair again" |
| Connect · version | Update needed | Update Lola on your Mac. / Update this app. |
| Banner · offline | — | No network. |
| Banner · unreachable | — | Can't reach {mac}. · "Details" |
| Banner · racing | — | Reconnecting… |
| Inbox | Inbox | — |
| Inbox sections | Needs you · Working · In review · Done | "Show all" · "Clear filter" |
| Inbox card kinds | — | Permission · Question · Menu open · Needs you |
| Inbox empty | Nothing needs you | — (compact row, no summary line) |
| Inbox connecting | — | status "Connecting…" (no snapshot, no banner) |
| Inbox card actions | — | Approve · Answer · Resolve · See checks · Address · Merge |
| Session segments | — | Conversation · Changes · Review · Terminal |
| Session gone | Session ended | Its story stays for 14 days. |
| Review bar | — | Send feedback ({n}) · Merge · "Merge anyway…" · "Couldn't check the PR." · no drafts: "Comment on a line to send feedback." |
| Review rows | — | Files changed · {n} checks failed · Agent review · Open #{n} on GitHub |
| Session empty | No messages yet | working: "The agent is working in the terminal." · idle: "Send a message to start." |
| Session menu | — | Rename · Switch agent · Model · Start/Stop dev server · Open dev server · Tasks · Checkpoints · Compare attempts · Message all · Agent review · CodeRabbit review · Revive · Resolve conflict · Remove session |
| Review packages row | — | Review packages ({n}) |
| Merge sheet | Merge PR | Squash and merge · Merge · Rebase and merge · ready: "All checks passed." · unready: "Not ready." + "Merge anyway" |
| Discard confirm (dirty text sheet) | Discard changes? | What you typed will be lost. · "Discard" |
| Remove confirm | Remove session? | Deletes its worktree and branch. · "Remove" |
| Projects | Projects | empty: "No projects." |
| Project actions | — | Start from PR · Start from ticket · New session · Check Linear now · Resume · Pause |
| Activity | Activity | empty: "Nothing yet." |
| Settings | Settings | sections "This phone" · "Appearance" · rename row "Rename" (Mac name is the group header; placeholder names are hidden) · Connection · Use system · About · Unpair |
| Connection values | — | Direct · Relay · Offline · hero line "Wi-Fi · 180 ms" (relay: "180 ms") |
| Project chips / tickets / PRs | — | "{n} working" · "Has session" · "External" (forks and bot authors; "dependabot[bot]" shown as "Dependabot") |
| New session | New session | agent "{agent}" · "Shell, no agent" |
| Message all sheet | Message all | "{project} · {n} agents" · placeholder "Message" |
| Feedback sheet | "1 comment" / "{n} comments" | "Send" · placeholder "Add a note" |
| Tasks / dev servers / model | "No tasks yet" · "No dev servers" · "No model settings" | "Start one from the session menu." · "Start dev server" · "This agent has none to change." |
| Checkpoints / best-of | "Compare attempts" | "Restore" · "New session from here" · "{n} files" · "Attempt {n} · {agent}" · "Pick" · "Kept" · "Still working." |
| Review package | Review | "Latest" |
| Unpair confirm | Unpair phone? | You'll need to scan again. · "Unpair" |
| Attach sheet | Add image | Camera · Photos · Paste |
| No tier | — | This phone can't do that. |
| Mac only | — | Only on your Mac. |
| Mac link | — | Open this link on your Mac. |
| Image read miss | — | Open on your Mac to view. |
| Oversize item (`TOO_LARGE`) | — | Too large to show here. |
| Paste miss | — | No image to paste. |
| Dev link banner | — | Development link |
| Refusals (systemic, `lib/refusal.ts`) | — | `unknown_cmd` → "Update Lola on your Mac to use this." · `denied` → the daemon's sentence, else "This phone can't do that." · `rate_limited` → "Too many requests; try again." · other → the daemon's message, else "Couldn't do that." · offline → "Not connected." |

Every string lives in `mobile/src/lib/copy.ts` as one `as const` object;
`copy.test.ts` asserts every title ≤ 3 words and every body ≤ 12 words and one
sentence (exactly one terminal `.`/`…`; a `;` joins clauses). A `{placeholder}`
and a bare `·` are not words. The refusals and the two one-line hints
(`reviewBar.noDrafts`, `newSession.needsText`) are whole sentences and keep the
body budget. Desktop components keep their own strings.

**Refusals speak a sentence.** `ShimBridge.request()` rewrites a rejection's
`message` and `toString()` to `refusalText(err)` and rethrows the same error
object, so `instanceof` and `.code` keep working while the shared stores'
`String(err)` and `err.message` show only the sentence, never a class name or a
wire code.

### 5.6 Preview fixtures (`mobile/preview.html`, `src/Preview.svelte`)

`Preview.svelte` becomes a fixture runner: it mounts the real `App.svelte`
with a `FakeTransport` (`src/wire/fake.ts`, extended to answer commands, answer
`watch` with `WatchData`, and emit `event` frames with watch ids) seeded from
`src/fixtures/<name>.ts`.
URL: `preview.html?route=<path>&sheet=<name>&fixture=<name>&theme=<id>&kb=<px>`
(`kb` fakes the keyboard inset). Viewport 390×844 by default.

| Fixture | Content | Routes it serves |
|---------|---------|-----------------|
| `empty` | 0 sessions, 2 projects | /inbox, /projects, /activity |
| `busy` | 14 sessions over all columns, 30 events | /inbox, /inbox?filter=*, /activity |
| `permission` | waiting_input + permission prompt with choices `Yes` / `Yes, and don't ask again` / `No` | /inbox, /session/s1/conversation |
| `question` | AskUserQuestion prompt, 4 choices | /session/s2/conversation |
| `long-story` | 300 feed items, 40 tool rows, 1 subagent | /session/s3/conversation |
| `composer` | draft with paste token and image token | /session/s3/conversation?sheet=attach, kb=336 |
| `diff` | 40 files, 2 000 lines, 3 drafts | /session/s4/changes, /session/s4/review?sheet=feedback |
| `merge-ready` | approved, green, mergeable | /session/s5/review?sheet=merge |
| `ci-red` | ci_failed, 2 failing checks | /inbox, /session/s6/review |
| `review-package` | 2 packages, 3 screenshots | /session/s5/review?sheet=reviewPackage |
| `terminal` | resync screen 80×24 | /session/s1/terminal |
| `offline` | last snapshot, phase failed/unreachable | /inbox |
| `key-changed` | last snapshot, phase failed/key_changed | /inbox |
| `pairing` | — | /connect and every `/connect/*` state |
| `settings` | relay path, 2 tiers missing | /settings?sheet=connection, /session/s1/conversation?sheet=sessionMenu |

### 5.7 Deep links (Simulator screenshot harness)

`lola-dev://open?route=<path>&sheet=<name>` (DEBUG only, `LOLA_DEV_LINK`
compiled out of release). Launch-environment variant
`SIMCTL_CHILD_LOLA_DEV_LINK` carries the same query plus optional
`pair=<lola2 payload>`:

- `pair=` is parsed natively into a `pairRef` and, for `source: dev-launch`
  only, `pairBegin` runs with device name "Simulator". For `dev-launch`
  pairings only, the plugin writes `lola-dev pair-code <6 digits>` to the
  unified log (`LolaLog.devLinkNote`, DEBUG builds only; the code is a display
  value, not a secret). A `dev-url` link with `pair=` only fills the Connect
  screen and waits for a tap (unchanged fence).
- The harness (`mobile/scripts/dev-link.sh --pair`) runs `lola pair --json`,
  reads the payload from its `begin` line, launches the Simulator with it,
  waits for the `{"event":"status","state":"confirming"}` line, reads the code
  from `xcrun simctl spawn booted log stream --predicate 'eventMessage BEGINSWITH "lola-dev pair-code"'`,
  and writes the one stdin line `lola pair` asks for (A9: the code). Nothing is
  confirmed without that line, and no non-interactive confirm flag exists.
- Once paired, links carry no credentials at all: only `route` and `sheet`.
- The persistent "Development link" banner stays (unchanged obligation), shown
  while a connection made after a dev link is up.
- `mobile/scripts/dev-link.sh` takes `--route`, `--sheet`, `--pair`.
- Every route of §5.1 and every sheet of §5.3 is reachable; `devlink.test.ts`
  iterates `SHEETS` and a route table and asserts each parses and applies.

---

## 6. Phone design system

Tokens live in `mobile/src/app.css` (`@theme`) and
`mobile/src/lib/mobiletokens.ts`; colours stay the desktop Catppuccin tokens
(`$lib/catppuccin.ts`, four flavors).

### 6.1 Type scale (pt at default Dynamic Type; `rem` against `dynamictype.ts` root)

| Token | Size / line | Weight | Use |
|-------|-------------|--------|-----|
| `--text-title` | 28/34 | 700, −0.018em | screen titles (Inbox, Projects, …) |
| `--text-headline` | 17/22 | 600 | card titles, sheet titles |
| `--text-body` | 17/24 | 400 | rows line 1, form fields |
| `--text-reading` | 16/24 | 400 | Conversation prose (`[data-phone-desk]`) |
| `--text-callout` | 15/20 | 400/500 | card body, buttons |
| `--text-subhead` | 13/18 | 600 | segment labels, metadata line 2 |
| `--text-footnote` | 12/16 | 400 | ages, counts (`tabular-nums`) |
| `--text-micro` | 11/13 | 700, 0.07em, uppercase | section labels (`text-xs` tier) |
| `--text-mono` | 13/18 | 400 | code, tool output; diff rows 12/18; terminal 12 (`viewport.ts` `FONT_DEFAULT`) |
| `--text-code` | 34/40 mono | 600 | the pairing code only |

### 6.2 Spacing, radii, elevation

- 4 pt grid: 4, 8, 12, 16, 20, 24, 32, 44. Side gutter 16: the `--gutter` token
  and the `px-gutter` utility. Card padding 16.
  Row min height 44; dense session row 56; card min height 112.
- Hit targets ≥ 44×44 for phone-owned controls (padding, not glyph size);
  shared desktop buttons get the `::after` extension of §4.3.
- Radii: chip/pill 999, button 10, card 14, sheet top corners 14, composer 20,
  segment bar 9 (track) / 7 (thumb).
- Elevation by surface, not shadow: `canvas` (ground) → `panel` (cards,
  composer) → `sel` (pressed/selected). Hairline borders `edge-soft`. One
  shadow only, on sheets and the tab bar: dark flavors
  `0 -8px 24px rgb(0 0 0 / 0.32)`, latte `0 -8px 24px rgb(0 0 0 / 0.10)`.
  Scrim behind sheets `rgb(0 0 0 / 0.40)`.

**Primitives** (`lib/components/`). Lists are inset grouped: `ListGroup` (a
`mx-4`, 12 pt-radius `panel` with an optional caps header, count and footnote)
holds `ListRow`s (44 pt, 52 pt with a 22 px leading icon; label, detail on line
2, right-aligned value, check or chevron). Separators are a 0.5 pt `edge-soft`
`::before` hairline between adjacent `[data-list-row]`s, inset by `--row-inset`
(16, or 52 with an icon). `Switch` is 51 × 31 with a 27 px thumb. `EmptyState`
is a centred 56 px glyph at 60 %, headline, body and optional primary action.
`LargeTitle` pins the top inset and gives every tab root the same baseline.

**One filled primary.** `TouchButton` variant `primary` is the accent fill
(`bg-accent`) under `--color-on-accent`, computed in `mobiletokens.ts` with
`readable()` (dark text on the dark flavors, white on latte and GitHub Light,
≥ 4.5:1); disabled is 40 % opacity. `secondary` stays tinted. `prominent` makes
it the 50 pt, 12 pt-radius, full-width call to action (callout, 600).

**Dynamic Type ceiling.** `ROOT_MAX` is 28 px (it was 23): the phone follows the
system text size through AX1 and clamps at about AX2. Beyond that the dense
layout (two-line session rows, form captions, the tab bar) is not designed;
system zoom serves those users better, and it scales the terminal too.

### 6.3 Layout facts at 390 pt

Content width 358 pt (16 pt gutters). Segment bar 358 × 36. Inbox card 358
wide. Conversation band uses the desktop's narrow container rule (24/16 px
padding → 350 pt text). Bottom bar 49 pt + safe area. Header 44 pt + safe area.

### 6.4 Motion

Springs use Svelte's `Spring` (`svelte/motion`); durations below are the
settle times they produce.

| Motion | Spec | Reduced motion |
|--------|------|----------------|
| Push/pop session | translateX spring stiffness 0.12, damping 0.85 (≈ 350 ms); under edge-swipe the position follows the finger, release with velocity > 500 pt/s or > 35 % width pops | 150 ms opacity crossfade |
| Sheet present | translateY spring stiffness 0.15, damping 0.82 (≈ 300 ms) to detent; detents fit (default) = content height capped at 100 % − top inset − 44 pt, medium = 52 % of height, large = 92 %, full = 100 % − safe top | 150 ms fade |
| Sheet dismiss | drag > 30 % of sheet height or velocity > 800 pt/s; spring to 100 % | fade |
| Segment thumb | `transform` 220 ms `cubic-bezier(0.2, 0.8, 0.2, 1)` | none |
| Card resolve (Inbox) | height collapse + fade 240 ms `cubic-bezier(0.4, 0, 0.2, 1)` | instant |
| Row insert/remove | fade + 8 pt slide 180 ms; only keyed rows that changed animate | instant |
| Keyboard inset | 250 ms `cubic-bezier(0.17, 0.59, 0.4, 0.77)` | instant |
| Banner | slide from top 200 ms | fade |

Nothing animates on a push event that did not change the row (no
list-wide transitions).

### 6.5 Haptics (`native.haptic`, `lib/haptics.ts`)

| Event | Kind |
|-------|------|
| Message sent (`agentSay` ok) | `light` |
| Card decision (`agentChoose`), Approve | `medium` |
| Stop / Stop & send | `rigid` |
| Segment or tab change | `selection` |
| Pairing done, merge ok, feedback sent | `success` |
| Daemon refusal shown, confirm dialog for a destructive act, `key_changed` banner | `warning` |
| `not_paired` | `error` |

Haptics do not follow `prefers-reduced-motion`; iOS's own System Haptics
setting governs them (the feedback generators respect it). Throttled by
`HAPTIC_MIN_GAP_MS`.

---

## 7. Desktop Settings → Phones

The Go service is 01's `desktop/phonesvc.go` `PhonesService` (A8); this spec
adds no Go. The UI calls the generated bindings for it.

### 7.1 UI (`desktop/frontend/src/lib/components/phones/`)

- `PhonesPanel.svelte` replaces the QR/key part of the `tab === "remote"`
  branch in `views/SettingsForm.svelte` (lines ~722-860 and the
  `remoteInsecureLan` row at ~1381, both already removed by 01's desktop item
  so the frontend compiles once the bindings lose them); the tab label becomes
  "Phones". Listener rows (enabled, bind, port, advertise, dev forward) stay
  above it. After 01's desktop item, item 16 owns every edit to
  `SettingsForm.svelte` and its test (`00-build-plan.md`).
- Sections: **Pair a phone** (button "Pair a phone"), **Paired phones**
  (list), **Relay** (one status row from `hubStatus.relay` (03 §12.2) + the
  `[remote].relay` checkbox bound to `SettingsDTO.remoteRelay` (03 §12.1),
  saved through the existing settings save path).
- `PairDialog.svelte` (`Modal`, 420 px), driven by one pairing id:
  - On open: `PhonesService.Begin()`. `Problem` → show it and "Close". Else
    keep `pairing = data.pairing` for the dialog's life and render the QR
    (`QRCode.svelte`) of `data.payload` with "Scan with Lola on your phone." and
    a countdown to `expiresAt`.
  - Loop while open: `PhonesService.Status({pairing, since: state, waitMs: 25000})`
    (long poll; no interval). States verbatim from 01:
    `waiting` → QR; `confirming` → device name (plus "Replaces {name}" when
    `replaces` is set, a "New device" badge when `nameClash` or `replaces`,
    and "via relay" when `via === "relay"`), a 6-digit code input
    ("Type the code shown on the phone"), tier toggles (Read on and disabled;
    Answer, Control, Remove sessions on; Shell on, labelled "Terminal input"),
    buttons "Reject" / "Confirm" (enabled with 6 digits); `paired` → "Paired
    {name}." and close after 1 s; `rejected` / `expired` / `failed` /
    `cancelled` → one line (`expired`: "Code expired." + "New code") and
    "Close".
  - Confirm: `PhonesService.Confirm({pairing, code: typedDigits, tiers})`,
    where `pairing` is the id this dialog began with, never re-read. The
    daemon refuses a stale id or a wrong code; "the code does not match" keeps
    the dialog in `confirming` with `triesLeft` shown.
  - Reject / closing the dialog while `waiting` or `confirming` →
    `PhonesService.Reject(pairing)`.
- `DeviceRow.svelte`: name, fingerprint, "Connected · {n}" / "Last seen 2h
  ago", tier chips (same toggles, `PhonesService.SetTiers(id, tiers)` on
  change), "Revoke" (danger, shared `confirm`: title "Revoke phone?", body
  "It loses access now.", button "Revoke") → `PhonesService.Revoke(id)`.
- Store `desktop/frontend/src/lib/phones.svelte.ts`: `devices`, `pairing`,
  `status`, `busy`, `error`; `Devices()` on tab open and every 5 s while the
  Phones tab is visible.

Tests (`phones/PhonesPanel.test.ts`, `phones/PairDialog.test.ts`): each state
renders; Confirm sends `{pairing, code, tiers}` with the typed digits and the
dialog's own id; **a dialog begun for pairing A whose `Status` then reports
`cancelled` while a second `Begin` produced pairing B sends `Confirm` with A
(the fake daemon refuses it) and never with B**; closing while `confirming`
calls `Reject(A)`; Shell off removes `shell` from tiers; Revoke asks first.

---

## 8. Performance

### 8.1 Budgets and how each is measured

| Budget | Target | Measured by |
|--------|--------|-------------|
| Cold start → interactive Inbox | < 1.0 s warm connection (direct), < 2.5 s cold over relay | `status().launchedAt` (set in plugin `load()`) to `performance.mark("inbox-interactive")` (first `sessions` reset painted, after `tick()` + rAF). Logged as `perf boot=<ms> path=<kind>` in DEBUG and kept in Settings → About. Run: Simulator via dev link (direct), device for relay. |
| Reconnect after foreground | < 500 ms direct, < 1.2 s relay | `appState{active:true}` timestamp to the first reset event of the new epoch; `raceMs` in the `ready` state event for the transport part. |
| Send-to-echo | one round trip | `perf/send.test.ts` with `FakeTransport` and the push source installed: pressing Send produces exactly one `agentSay` req and zero `talkState`/`sessionTimeline` reqs (§3.2 items 1 and 4); the echo arrives as `timeline:`/`talk:` events. |
| Idle cost | 0 requests/s with nothing changing | `perf/idle.test.ts` with fake timers: Inbox and Conversation mounted, resets delivered, 120 s advanced, no events → zero `req` frames. Device check: `status()` `framesOut` unchanged over 60 s on the Inbox. |
| Keyed deltas | no work for unchanged rows | vitest: 200 sessions, a delta upserting 1 → `rowModel` (the per-row `$derived` input function in `lib/inbox.svelte.ts`) runs once; all other row objects `===` before/after. |
| Lists virtualized over 50 rows | `VIRTUALIZE_AT = 50` | vitest: 300 rows render ≤ visible + 2 × `OVERSCAN` DOM rows. |
| Bundle split | xterm and diff not in the entry chunk | `npm run budget` (`mobile/scripts/bundle-budget.mjs`): reads `dist/.vite/manifest.json` (needs `build.manifest: true`, §8.4); fails when the entry's static import graph includes `@xterm/*` or `DiffView`, or gzip sizes exceed: entry ≤ 220 KB, `session` chunk ≤ 260 KB, `terminal` ≤ 200 KB, `diff` ≤ 90 KB. |

### 8.2 Virtualization and keyed state

- `mobile/src/lib/components/VirtualList.svelte` (new): fixed-estimate heights
  per row kind (row 56, card 112, section 32), real heights measured with one
  shared `ResizeObserver`, `OVERSCAN = 6` rows, scroll position preserved on
  insert above the viewport. Used by Inbox rows, Activity, PR and ticket
  pickers. Below `VIRTUALIZE_AT` rows it renders a plain keyed `{#each}`.
- The Conversation uses the desktop's own turn placeholders
  (`TimelineTurn.svelte`, IntersectionObserver in `Timeline.svelte:342`); no
  second virtualizer.
- `lib/inbox.svelte.ts` keeps `SvelteMap<string, SessionInfo>` fed by the
  bridge's keyed map, values `$state.raw`. Rows receive the object; an unchanged
  object means Svelte skips the row. Sections are `$derived` id arrays.
- `DiffView` renders every row of every file in compact mode. The `diff`
  fixture (2 000 lines) must stay under 16 ms per frame while scrolling on an
  iPhone 12-class device; if it does not, the follow-up is a phone-only
  file-at-a-time wrapper, not a DiffView edit. Recorded as unverified until run.

### 8.3 One clock

`mobile/src/lib/clock.svelte.ts`: `export const minuteClock` built with
`createSubscriber` (as `sshClock` is), ticking at the next 30 s boundary
(`CLOCK_TICK_MS = 30_000`), stopped while hidden. Every phone-rendered age
(`Event.at`, `agentStateSince`, `headlineAt`, `firstSeen`, `updatedAt`) reads it
through `$lib/time.ts` formatting. Desktop components keep `sshClock` (1 s,
only while something runs).

### 8.4 Route-level code splitting

`screens/session/ChangesSegment.svelte`, `ReviewSegment.svelte` and the
`reviewPackage` sheet import `DiffView`/`ReviewWorkspace` dynamically;
`TerminalSegment.svelte` imports `MobileTerminal` (xterm, addons) dynamically;
`Connect.svelte` is its own chunk. `vite.config.ts` (Vite 8 bundles with
Rolldown, whose object-form `manualChunks` takes no RegExp):

```ts
build: {
  manifest: true,
  rolldownOptions: {
    output: {
      advancedChunks: {
        groups: [
          { name: "terminal", test: /[\\/]node_modules[\\/]@xterm[\\/]/ },
          { name: "diff", test: /DiffView\.svelte|[\\/]diff\.ts$|diffnavigation/ },
        ],
      },
    },
  },
}
```

If the installed Vite still reads `rollupOptions` only, the equivalent is
`rollupOptions.output.manualChunks(id) { if (/[\\/]@xterm[\\/]/.test(id)) return "terminal"; if (/DiffView\.svelte|[\\/]diff\.ts$|diffnavigation/.test(id)) return "diff"; }`
(function form); `npm run budget` proves whichever is used. The first visit to a
segment shows the segment skeleton for at most one frame after the chunk
resolves; chunks are prefetched with `import()` 1 s after the Inbox is
interactive (idle, foreground only).

---

## 9. CI (`.github/workflows/mobile.yml`, new)

Separate workflow, so `ci.yml` stays Go-only.

```yaml
name: Mobile
on:
  push:
    branches: [main]
    paths: ['mobile/**', 'desktop/frontend/**', '.github/workflows/mobile.yml']
  pull_request:
    branches: [main]
    paths: ['mobile/**', 'desktop/frontend/**', '.github/workflows/mobile.yml']
permissions:
  contents: read
jobs:
  web:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '22'
          cache: npm
          cache-dependency-path: |
            desktop/frontend/package-lock.json
            mobile/package-lock.json
            mobile/plugins/lola-transport/package-lock.json
      - run: npm ci
        working-directory: desktop/frontend      # shared components resolve their deps here
      - run: npm ci
        working-directory: mobile/plugins/lola-transport
      - run: npm ci
        working-directory: mobile
      - run: npm run check
        working-directory: mobile
      - run: npm test
        working-directory: mobile
      - run: npm run build && npm run budget
        working-directory: mobile
  swift:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v4
      - run: swift test
        working-directory: mobile/plugins/lola-transport
        env:
          LOLA_CORE_ONLY: '1'
```

`mobile/package.json` gains `"budget": "node scripts/bundle-budget.mjs"`.
`desktop/frontend` vitest and `make test` (which runs `tiers_golden_test.go`)
stay in their existing workflows.

---

## 10. Deviations

- **D1 — one client key per paired daemon** rather than one per phone. The plan
  says "the phone generates a P-256 key"; it does not say how many. Per-daemon
  keys keep two Macs from correlating a phone. If 01 requires a single key, set
  `ClientIdentity`'s tag to a constant; nothing else changes.
- **D2 — the scanner no longer returns the payload to JS** (`scanQR` →
  `scanPairing`), and the plugin runs the `relayCred` exchange natively. The
  pairing secret, the QR's `tok` and every relay bearer would otherwise be
  printed by Capacitor's bridge log in Debug builds.
- **D3 — `mobile/` now writes to `desktop/frontend/src/lib`** (§3, small seams,
  two new modules, one `data-queue-row` attribute). `mobile/README.md` and
  `vite.config.ts` state "no file under desktop/ is ever written by this
  project"; that rule is replaced (AGENTS.md text below).
- **D4 — Review segment composes `BeforeMerge` + `DiffView` and opens
  `ReviewWorkspace` as a full sheet**, rather than mounting `ReviewWorkspace`
  as the segment. `ReviewWorkspace` is a fullscreen `Modal` with its own
  close; mounting it as a segment would need a desktop prop.
- **D5 — Proposals and widgets stay desktop-only.** Plan §6 lists
  Conversation parity. `sshProposal` (runs a command on an SSH host) and
  `sshWidgetStatus` are `never` (01), and widget documents never reach the
  phone (A13): the phone shows "Open on your Mac to view." in place of a
  widget and "Only on your Mac." for a proposal. Recall (`sshRecall`) and
  element answers (`sshAnswer`, except elements scoped to an SSH connection)
  work on the phone (A19).
- **D6 — line comments on the phone are single-line.** DiffView's range
  selection is shift-click; the phone uses DiffView's own inline editor
  unchanged rather than a separate comment sheet.
- **D7 — Inbox Approve is narrower than the plan's "approve".** It renders the
  request and sends only an exact `Yes` choice; anything else routes to the
  Conversation. A persistent "don't ask again" grant is never one tap away.
- **D8 — (withdrawn)** The pairing socket is closed after the outcome by the
  daemon itself (01 Deviation 12); the app connects normally afterwards.
- **D9 — a hostile network can no longer end the connection.** The plan maps a
  changed key to "re-pair"; this spec shows `key_changed` only when every path,
  relay included, shows the same wrong key, and then as a warning. Only an
  authenticated `not_paired` routes to Connect.
- **D10 — no `!` line on the phone.** Plan §6 lists it; the daemon defuses a
  leading `!` on every surface (01 Deviation 11), so no surface offers it.
- **D11 — the relay leg uses `URLSessionWebSocketTask` plus a loopback bridge,
  not `NWProtocolWebSocket`** (03 D1): only URLSession exposes a refused
  upgrade's status and headers, and the bridge keeps one TLS stack for both
  paths.

## 11. AGENTS.md text

Replace the `mobile/` mention under **Architecture map → Core** (`remote — TLS
phone listener; …`) with nothing new (01 owns `remote`), and add this section
after **Desktop session view**:

```
### Phone client (`mobile/`, spec: `docs/mobile/04-client.md`)
- The phone runs the desktop's own Conversation, Changes and Review components
  through a shim (`mobile/src/wailsshim/`). `wailsshim/conformance.ts` makes
  `npm run check` fail when the shim drifts from the generated bindings: a new
  bound method needs a shim route (fwd, local, platform or absent) in the same
  change.
- Tiers have one source: `remote.CommandTiers()` → `mobile/src/wire/testdata/tiers.json`
  (`go test ./internal/remote -run TestTierGolden -update`) → `wire/tiers.ts`.
  The client never spells a tier by hand; it never sends a `never` command
  (`openURL` opens on the phone).
- Desktop stores take a pushed source (`$lib/pushsource.ts`). With none
  installed they poll exactly as before; only `mobile/src/main.ts` installs one.
  A refused topic reverts that store to polling. A desktop edit that adds a poll
  must add the matching push path or say why the phone does not need it.
- Secrets never cross the Capacitor bridge: the pairing secret, the QR's
  `tok`, every relay bearer and the client key stay in Swift and the Keychain.
  Capacitor logs every resolved payload and listener event in Debug builds.
  `relayCred` and every `lola-`-prefixed request id are native-only; the
  plugin refuses them from JS.
- Widgets never run on the phone: elements arrive without `html`/`data` and
  `SSHWidget` renders a placeholder on the phone (`setWidgetsUnavailable`).
- The Capacitor `bridge` handler accepts messages from the app's main frame
  only (`LolaTransportPlugin/BridgeGuard.swift`), so a widget iframe cannot
  reach a plugin. `mobile/src/native/capacitorguard.test.ts` pins the Capacitor
  version it was read against; re-read the guard on every Capacitor upgrade.
- Only an authenticated signal ends a connection: a `not_paired` frame or a
  TLS alert after the pinned handshake completed. A wrong key, a TLS failure
  before `.ready`, an mDNS hint or a hub refusal fails one candidate only.
- The client re-sends a request after a connection change only if its tier is
  `read`; everything else rejects with `ConnectionClosedError`. A migration
  waits for in-flight non-read requests (`MIGRATE_DRAIN_MS`).
- Inbox Approve sends only an exact `Yes` choice, with the hash of the request
  text it rendered; never a "don't ask again" option.
- The phone's tier list hides controls; it never grants. The daemon gate is the
  truth, and a refusal is shown as the daemon's sentence.
- Screens are reached in tests by `lola-dev://open?route=…` or
  `preview.html?route=…&fixture=…`, never by synthetic taps. A screen or sheet
  no route reaches is a bug.
```

In **Desktop session view**, change "Terminal attaches only while on screen; no
hidden/read-only clients." to "Terminal attaches only while on screen, on both
surfaces; no hidden/read-only clients." and append to the **Widgets are
untrusted** bullet: "On the phone the same rule rests on
`LolaTransportPlugin/BridgeGuard.swift` (main frame only) +
`capacitorguard.test.ts`, re-read on every Capacitor upgrade."

---

## 12. Test list

**Swift (Core)**: §1.8. **Harness (Simulator, DEBUG)**: §1.9 bridge guard,
§5.7 pairing.

**Go**: `internal/remote/tiers_golden_test.go` (`TestTierGolden`).

**Mobile vitest** (new or rewritten):

- `native/index.test.ts` — unavailable plugin → web fallback; no `Capacitor.Plugins` access (spy).
- `native/capacitorguard.test.ts` — §1.9 version pin.
- `wire/tiers.test.ts` — `TIER` equals `testdata/tiers.json`; `isRead` true exactly for `read`.
- `wire/events.test.ts` — 02 §6.2 table: stale id dropped, first event must be seq 1 + reset, gap → rewatch, `more` reset assembled into one delivery, `WatchResult` codes → failed, against `testdata/events.json`.
- `wailsshim/services/*.test.ts` — `ROUTES` keys equal generated keys; each fwd sends its cmd and field placement from §2.3 and its cmd is in `TIER` and not `never`; platform rejects `UnsupportedOnMobileError`; absent keys not exported; `Kill.length === 3`, `force` rejected; `OpenURL` never sends a frame, opens http(s) natively, rejects loopback with `MAC_LINK`; `SandboxLoginNeeded` resolves `""`; `Recall` sends `sshRecall`, `Answer` sends `sshAnswer`; no route sends `relayCred`.
- `wailsshim/services/talkimage.test.ts` — 512 KiB parts, one in flight, `rate_limited` on part 11 is retried after 1.1 s and the 16-part upload completes, other refusals abort; thumb size; `ReadAttachmentImage` fetches chunks to `eof`, resolves an ended session by `keptWorktree`, caches by path.
- `wailsshim/watch.test.ts` — refcount, delayed unwatch, batching ≤ 32, `onError` per code, `limit` retried only after an unwatch or on a new epoch, `rate_limited` after 30 s, a stream-ending `err` frame fails only its topic, watch ids match `^[A-Za-z0-9_-]{1,64}$`.
- `wailsshim/bridge.test.ts` — keyed sessions map preserves identity; **a delta without `events` keeps the last events (Activity not cleared)**; rAF coalescing; `daemon:alive` from phase; hidden → immediate reject; `omitted` ids fetched with one `sessions`; a failed `sessions` topic falls back to `FALLBACK_POLL_MS` polling until the next epoch.
- `lib/pushsource.test.ts` — 02 payloads map onto `FeedPush`/`SSHTimelineData`; types `satisfies` the desktop interface.
- `lib/resume.test.ts` — new epoch re-watches every live topic in one request, re-subscribes panes; **every non-read command in `TIER` (`agentSay`, `agentChoose`, `answer`, `feedback`, `attachImage`, `mergePR`, `kill`, `shellCreate`, …) in flight on a closed epoch rejects `ConnectionClosedError` and is not re-sent; a read is re-sent once**.
- `lib/router.test.ts` — `parseRoute`/`formatRoute` round-trip for every grammar line; unknown → null; `/connect/*` while paired → inbox; `/dev/*` null in release; `back()` order.
- `lib/devlink.test.ts` — every route and sheet applies; `dev-url` never pairs.
- `lib/inbox.test.ts` — card selection and action table; `approveChoice` picks only an exact `Yes`, returns null for `Yes, and don't ask again` alone, for two `Yes`, for none; Approve sends the hash of the rendered state; hash change disables Approve for `APPROVE_SETTLE_MS`; `MAX_INBOX_TALK_WATCH`; keyed delta budget (§8.1).
- `lib/tiers.test.ts` — set from `connect()`, replaced by the next epoch's `device` event; `can()` per `TIER`.
- `lib/desknav.test.ts` — route ↔ desktop nav mapping, overlay → sheet, Fork → push.
- `lib/copy.test.ts` — copy budget.
- `lib/clock.test.ts` — 30 s boundary, stops when unsubscribed/hidden.
- `phonecss.test.ts` — §4.3 rules.
- `components/VirtualList.test.ts` — DOM row bound, anchor preservation.
- `wire/limits.test.ts` — constants equal across TS, plugin and Go golden file.
- `screens/*.test.ts` — Connect states and copy (every pairing outcome, `pairBegin` rejection keeps the scan); Inbox empty/busy/offline/key-changed; Session segments hidden by data; Review sticky bar counts; Settings unpair asks.
- `perf/idle.test.ts`, `perf/send.test.ts` — §8.1 idle and send-to-echo.
- `Activity.test.ts` — key stable across an `ago` change (bug 3).
- `TerminalSegment.test.ts` — identical geometry push does not reassign (bug 4).

**Desktop vitest**: §3.2 tests; `phones/PhonesPanel.test.ts`, `phones/PairDialog.test.ts`.

---

## 13. Work items

Each item owns its files exclusively; no file appears in two items, and no
item deletes a file another item's code imports (all deletions are item 18).
**Order and cross-spec ownership: `docs/mobile/00-build-plan.md` is
authoritative**; it maps these items onto waves C and D (and merges spec 02's
TS wire item 2 with the wire part of item 5).

1. **Swift Core** — `mobile/plugins/lola-transport/Package.swift`,
   `ios/Sources/LolaTransportCore/*` (incl. `RelayCredentials.swift`,
   `RelayWire.swift`) except `BridgeOriginGuard.swift` (item 17),
   `ios/Tests/LolaTransportCoreTests/*` except `BridgeOriginGuardTests.swift`
   (17). §1.1 Core rows, §1.4 `PathRace`, §1.5 builders, §1.6, §1.8, 03 §13.2–13.4.
   Acceptance: `LOLA_CORE_ONLY=1 swift test` green on a Mac.
2. **Swift plugin** (after 1's public API) —
   `ios/Sources/LolaTransportPlugin/*` (incl. `RelayLeg.swift`,
   `LoopbackBridge.swift`, `RelayPath.swift`) except `BridgeGuard.swift` (item 17);
   `LolaTransport.podspec`; `src/definitions.ts`, `src/index.ts`, `src/web.ts`.
   §1.2-§1.7. `LolaTransportPlugin.load()` calls item 17's `installBridgeGuard`
   first. Hardware paths stay marked unverified.
3. **Native access layer** (after 2's `definitions.ts`) — `mobile/src/native/index.ts`,
   `native/web.ts`, `native/index.test.ts`,
   `mobile/src/lib/{scan,appstate,openurl,keyboardinset}.ts` (+ their tests),
   `mobile/tsconfig.json` (both paths of §2.1/§2.4).
4. **Shim services, tiers, conformance** (after 01's bindings regeneration that
   adds `PhonesService`) — `mobile/src/wailsshim/{desktop,desktop.test,index,errors,conformance}.ts`,
   `mobile/src/wailsshim/services/*` (incl. `phones.ts`, `talkimage.ts`,
   `routes.ts`, `reasons.ts` and tests). Consumes `wire/tiers.ts` and
   `wire/testdata/tiers.json` (the TS wire item and the Go tiers item of the
   build plan).
5. **Wire, events, push bridge, resume** — `mobile/src/wire/*` except item 4's
   tiers files (incl. `events.ts`, `limits.ts`, `push.ts`, `fake.ts`,
   `testdata/events.json` import), `mobile/src/wailsshim/{bridge,watch,channeltransport,panesubs,capacitorchannel,pluginerror,events,runtime,screen}.ts`
   (+ tests), `mobile/src/lib/{resume,pushsource,connection.svelte,diagnose}.ts`
   (+ tests), `mobile/src/main.ts` (bug 1). Bugs 2, 7. Stops importing
   `lib/endpoint.ts`, `discovery.ts`, `secretstore.ts`, `pairpayload.ts`.
6. **Desktop seams** — `desktop/frontend/src/lib/{pushsource,composerattach}.ts`,
   `talk.svelte.ts`, `feed.svelte.ts`, `ssh.svelte.ts`,
   `components/session/{Timeline,Workspace,Composer,QueueRows}.svelte` (QueueRows:
   the `data-queue-row` attribute only), `components/ssh/SSHWidget.svelte`
   (§3.2 item 8) and their tests. §3.1-§3.2. Must keep `desktop/frontend` tests
   green.
7. **Router, desk-nav, deep links, copy, tiers** — `mobile/src/lib/{router.svelte,desknav,deskprefs,copy,tiers.svelte,haptics,clock.svelte}.ts`
   (+ tests), `mobile/src/lib/devlink.ts` (+ test; its two probes too),
   `mobile/scripts/dev-link.sh`. Does not delete `nav.svelte.ts`/`sheets.ts`.
8. **Design system + shell** (after 7) — `mobile/src/app.css`, `phonecss.test.ts`,
   `mobile/src/lib/mobiletokens.ts`, `mobile/src/lib/components/{Sheet,TabBar,SegmentBar,VirtualList,Banner}.svelte`
   (+ tests), `mobile/src/App.svelte` (root, router outlet, ConfirmDialog mount,
   banners incl. `key_changed`, sheet host).
9. **Connect + pairing UI** (after 3, 7) — `mobile/src/screens/Connect.svelte`,
   `mobile/src/lib/{pairing.svelte,paircopy}.ts` (+ tests).
10. **Inbox + Activity** (after 5, 7, 8) — `mobile/src/screens/{Inbox,Activity}.svelte`,
    `mobile/src/lib/inbox.svelte.ts`, `mobile/src/lib/components/{InboxCard,InboxRow}.svelte`,
    `mobile/src/sheets/filter.svelte`, and the existing
    `lib/components/{FilterSheet,SectionHeader,StatusChip,StatusDot,MetaPill}.svelte`
    if they change (+ tests). Bug 3.
11. **Session screen** (after 6, 7, 8, 17) — `mobile/src/screens/session/{Session,SessionHeader,ConversationSegment,ChangesSegment,ReviewSegment}.svelte`,
    `mobile/src/sheets/{sessionMenu,newSession,attach,model,merge,feedback,checkpoints,bestOf,rename,broadcast,tasks,switchAgent,reviewPackage}.svelte`
    (+ tests).
12. **Terminal segment + splits** — `mobile/src/screens/session/TerminalSegment.svelte`,
    `mobile/src/lib/{terminalgeometry.svelte,terminalinput,panetabs.svelte,panepin,panepinstore,viewport,keybytes,keygesture,resync}.ts`
    (+ tests),
    `mobile/src/lib/components/{MobileTerminal,PaneTabs,AccessoryBar,AccessoryKey,PaneMenuSheet,ViewSettings,DevLinksSheet,TouchButton}.svelte`
    (+ tests), `mobile/src/sheets/{devLinks,paneMenu,viewSettings}.svelte`. Bug 4.
13. **Projects + Settings screens** — `mobile/src/screens/{Projects,ProjectDetail,PRPicker,TicketPicker,Settings}.svelte`,
    their `lib/` models (`lib/projects.svelte.ts`, `lib/settings.svelte.ts`),
    `mobile/src/sheets/{connection,unpair}.svelte` (+ tests).
14. **Preview fixtures** (after 5, 8) — `mobile/preview.html`,
    `mobile/src/{Preview.svelte,preview.ts}`, `mobile/src/fixtures/*`.
15. **Performance + CI** — `mobile/scripts/bundle-budget.mjs`,
    `mobile/src/perf/*.test.ts`, `mobile/vite.config.ts` (chunks, `manifest`,
    alias comment trim), `mobile/package.json`, `.github/workflows/mobile.yml`.
16. **Desktop Phones UI** (after 01's `PhonesService` bindings) —
    `desktop/frontend/src/lib/phones.svelte.ts`,
    `desktop/frontend/src/lib/components/phones/*` (+ tests),
    `desktop/frontend/src/lib/views/SettingsForm.svelte` and its test (every
    edit: dead panel, regenerate button, `insecure_lan` row removed, PhonesPanel
    mounted).
17. **Capacitor bridge guard** — `ios/Sources/LolaTransportCore/BridgeOriginGuard.swift`,
    `ios/Tests/LolaTransportCoreTests/BridgeOriginGuardTests.swift`,
    `ios/Sources/LolaTransportPlugin/BridgeGuard.swift`,
    `mobile/src/native/capacitorguard.test.ts`,
    `mobile/src/screens/dev/BridgeGuard.svelte` (DEBUG route). §1.9.
18. **Delete superseded modules** (after 3, 5, 7, 9-14) — delete
    `mobile/src/lib/{secretstore,discovery,endpoint,pairpayload,nav.svelte,sheets}.ts`
    (+ tests), `mobile/src/lib/testdata/connectcode.json`, `mobile/src/views/*`,
    `mobile/src/lib/components/{SessionCard,SessionRow,FilterRail,TriageChips}.svelte`
    (+ tests), `mobile/plugins/lola-transport/ios/Sources/LolaTransportCore/HelloHandshake.swift`
    and `LolaTransportPlugin+Secrets.swift` if items 1/2 left them, and the
    remaining `mobile/scripts/dev-link.sh` key/keyfile options if item 7 left them. Acceptance:
    `npm run check`, `npm test` green.
19. **Docs** (last) — `mobile/README.md`, `mobile/HANDOFF.md`,
    `mobile/PLAN.md` header, `AGENTS.md` (§11 text), `docs/unified-session.md`
    rows "Phones and TUI" and "Phone" (they say phones get no Conversation
    commands; replace with a pointer to this spec).

## 14. Unverified by this build

Secure Enclave key generation and `SecIdentity` lookup on hardware; the camera
scan; that `sec_protocol_metadata_create_secret` (no context) equals Go's
`ExportKeyingMaterial(label, nil, 32)` (01's vectors prove the HMACs, not the
exporter; first device run checks it); that WebKit exposes `bridge` to a
sandboxed `data:` frame and the guard drops it (§1.9 harness); real Wi-Fi ↔
cellular migration timing; relay against the deployed hub; the DiffView scroll
budget on a real device; haptics.

## 15. Changes this spec requires in sibling specs

None outstanding. The requests of the first draft were settled when specs 01–04
were reconciled (2026-11-14): `tok` in the QR and `PairRelay{rv, bearer}` (01
§6.2, §6.4); OLDEST eviction (01 §7); `sshRecall`/`sshAnswer` instead of a new
`agentRecall` (01 §8.4); `relayCred` instead of `relayCredAck`/`relayCredRenew`
and a `device` topic (03 §10.6; tier changes reconnect, 01 §4.6); non-last
`attachImage` parts exempt from `writeRate` (01 §7); `remote.CommandTiers()`
(01 §8.1); `SettingsForm.svelte` handed over after 01's desktop item
(`00-build-plan.md`); the code is typed, never shown (01 §6.4); every Swift
file is this spec's (03 §13).
