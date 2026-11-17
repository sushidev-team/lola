# 00 — Build plan (workflows 2–4)

Status: authoritative schedule for implementing `docs/mobile-rethink.md` through
specs `01-pairing-and-tiers.md`, `02-events.md`, `03-hub-relay.md` and
`04-client.md`. Where a spec's own "Work items" section disagrees with this file
about order or file ownership, this file wins; for behaviour, the spec wins.

---

## 0. Rules for every item

1. **Waves run in order; sub-waves run in order; items inside one sub-wave run
   in parallel.** An item depends only on items of EARLIER sub-waves (or on a
   stub file an earlier sub-wave created, §0.4). Nothing in a sub-wave waits
   for a sibling.
2. **One owner per file per sub-wave.** Every file an item creates, edits or
   deletes is listed under "Owns". A file appears in at most one item of a
   sub-wave. Different sub-waves may touch the same file; the later owner
   rebases on the earlier one's result. "Owns `x` (hunk: …)" means the item
   edits only that part of `x`.
3. **Shared files are landed by their owner for every spec.** When an item
   owns a file that several specs change (e.g. `internal/remote/server.go`), it
   lands every spec's change to that file that belongs to its sub-wave, listed
   under "Also lands". Other items code against the signatures given here.
4. **Stub files bridge the daemon sub-waves.** Wave B3 item B3-3 creates
   `internal/daemon/zz_stub_<feature>.go` files holding no-op versions of
   functions that wave B4 items implement. Each B4 item DELETES exactly its stub
   file and adds the real code with the SAME signature (§3, B3-3 table). A
   signature drift fails `make check`, which is the point. No stub survives
   wave B.
5. **Verification.** Every item runs the command(s) named in its "Verify" line
   before it is done, plus `gofmt`-clean Go. Commands:
   - Go: `make check` (fmtcheck, build, vet, test; covers `hubproto/`).
   - Hub: `cd hub && make check` (needed whenever `hubproto/` or `hub/` changes).
   - Desktop frontend: `cd desktop/frontend && npm run check && npm test`.
   - Swift Core: `cd mobile/plugins/lola-transport && LOLA_CORE_ONLY=1 swift test`.
   - Swift plugin build (Mac only): `cd mobile && npm run sync && xcodebuild -project ios/App/App.xcodeproj -scheme App -destination 'generic/platform=iOS Simulator' build`.
   - Mobile: until the end of wave D the whole of `mobile/` does not type-check
     (it already fails with 13 errors today, and the old `views/` keep importing
     removed modules until D2-3 deletes them). So a mobile item verifies its OWN
     files: `cd mobile && npm run check:owned -- <owned paths…>` (B5-2 adds this
     script: it runs `svelte-check --output machine` and fails on any error whose
     file is under a given path) and `cd mobile && npx vitest run <owned test files>`.
     From D3 on: `cd mobile && npm run check && npm test && npm run build && npm run budget`.
6. **No item deletes a file another file still imports** unless it also owns
   that importer. Leftover exports in `mobile/` are removed by D2-3.
7. Builders never run git commit/stash/reset; the orchestrator merges.

## 1. Settled cross-spec decisions (the names everyone uses)

| Topic | Decision | Source |
|---|---|---|
| Path of a connection | `Handshake.Via` / `Peer.Via` / `ReadyData.via` / `PairStatusData.via` ∈ `"lan"` \| `"relay"`; constants `remote.ViaLAN`, `remote.ViaRelay`; log/audit field `via=` | 01 §4.2 |
| Authenticated signal | the `ready` frame (`protocol.FrameReady`, `ReadyData{deviceId,name,tiers,via,host}`); post-handshake refusal = `err` `not_paired` | 01 §4.6 |
| QR relay | `relay: {url, rv, tok}`; `tok` random (`hubproto.NewRelayBearer`), hash registered as a window entry; no derived token | 01 §6.2, 03 §3.2, §10.6 |
| Window relay owner | `Pairer.SetWindowRelay(id, rv, tokHash)` / `Pairer.Window()`; `Pairer.Begin()` takes no options | 01 §6.3 |
| Paired relay credential | `PairResult.Relay = PairRelay{rv, bearer}`, fresh rv from `d.relayMint()` passed to `Pairer.Confirm(args, *ConfirmRelay{Bearer, Record})` | 01 §6.4, 03 §10.6 |
| Credential refresh | `relayCred {holds?}` → `RelayCredData{available,url,rv,bearer}`; handled daemon command, tier `read`, self-only via `remote.PeerFromContext`; no `relayCredAck`, no `relayCredRenew`, no `device` topic | 03 §10.6 |
| Tier changes | `setDeviceTiers` closes the device's connections; tiers come only from `ready` | 01 §4.6 |
| Relay headers | `Lola-Relay-Rv`, `Lola-Relay-Bearer`, `Lola-Relay-Code`; never `Authorization` | 03 §3.1 |
| Relay timeouts (phone) | upgrade 8 s (`RelayPhoneAttemptTimeout` = `RELAY_UPGRADE_TIMEOUT_MS`), attempt 12 s, race 13 s, fresh-credential grace 60 s | 03 §3.6, 04 §1.4 |
| Pairing slots | `maxUnpairedConns` 4, `maxUnpairedPerSource` 2 = `PAIR_MAX_PARALLEL` | 01 §6.3, 04 §1.5 |
| TransportCommands | `{watch, unwatch}` | 01 §8.1 |
| Handled command count | 175: read 24, answer 12, control 23, destructive 3, shell 6, never 107 | 01 §8.3 |
| Recall / answer on phone | `sshRecall`, `sshAnswer` in `TalkCommands`, tier `answer` | 01 §8.4 |
| Widgets | never sent to a phone (`publicTimeline` drops `html`/`data`); `SSHWidget` placeholder | 01 §8.6, 04 §3.2 item 8 |
| Swift ownership | spec 04 owns every Swift file; 03 §13 is the normative relay contract | 03 §13, 04 §1.1 |
| Attach rate | non-last parts exempt from `writeRate`; `attachBytesRate` 2 MiB/s, burst 20 MiB; last part one write | 01 §7 |
| Event box memory | `maxWatchingConns = 8`; a ninth watching connection gets `limit`, the client polls | 02 §9, 04 §2.5 |
| Golden writers | env `LOLA_WRITE_VECTORS=1` for every Go-written vector file | 02 §13, 04 §2.2 |
| `req_remote_pair` vector | only in `mobile/src/wire/testdata/pairing.json` | 01 §6.6, 02 §13 |

## 2. Wave overview

| Wave | Sub-wave | Items |
|---|---|---|
| A — Go contracts and leaves | A1 | A1-1 protocol: pairing, tiers, talk, timeline, relay, hub status · A1-2 protocol: frames v2, events, attach, frame vectors · A1-3 hubproto WebSocket codec · A1-4 hubproto relay wire · A1-5 QR leaf · A1-6 commit hooks (session, sshstore) · A1-7 config `[remote].relay` |
| | A2 | A2-1 device registry · A2-2 event mailbox · A2-3 remote tiers and request rebuild · A2-4 remote pairing payload · A2-5 hub store relay entries · A2-6 daemon relay client (`internal/hub`) · A2-7 hub relay core |
| B — daemon + hub behaviour | B1 | B1-1 remote auth + Pairer + device authorizer · B1-2 daemon tests off `CommandDenied` · B1-3 hub relay API · B1-4 hub deploy + hub docs · B1-5 remote rate limits · B1-6 doctor rows |
| | B2 | B2-1 remote transport · B2-2 hub process |
| | B3 | B3-1 remote event push · B3-2 remote relay acceptor · B3-3 daemon wiring + stubs |
| | B4 | B4-1 pairing handlers · B4-2 sessionTimeline + tickets cache · B4-3 watch hub + producers · B4-4 poke sites · B4-5 attachments + push routing · B4-6 daemon relay loop |
| | B5 | B5-1 desktop PhonesService + M1 settings removal · B5-2 build/CI cleanup + mobile owned-check script |
| C — Phones UI, CLI, client foundation | C1 | C1-1 `lola pair` / `lola devices` · C1-2 Swift Core · C1-3 Capacitor bridge guard · C1-4 TS wire v2 · C1-5 desktop pushed-source seams · C1-6 desktop Settings → Phones · C1-7 plugin TS surface + native access layer |
| | C2 | C2-1 Swift plugin · C2-2 shim services + conformance · C2-3 push bridge, watch registry, resume · C2-4 router, desk-nav, deep links, copy, tiers store · C2-5 bundle budget + mobile CI |
| | C3 | C3-1 design system + app shell |
| D — interface screens | D1 | D1-1 Connect + pairing UI · D1-2 Inbox + Activity · D1-3 Session screen · D1-4 Terminal segment · D1-5 Projects + Settings |
| | D2 | D2-1 preview fixtures · D2-2 performance tests · D2-3 delete superseded client modules |
| | D3 | D3-1 docs (AGENTS.md, READMEs, handoff) + full gate |

Wave gates (run by the orchestrator after the last sub-wave of the wave):
A, B: `make check` and `cd hub && make check`. C: the same plus
`cd desktop/frontend && npm run check && npm test` and the Swift Core command.
D: everything, including the full mobile gate of §0.5.

Transitional facts builders must not "fix":
- From A1-2 until C1-4, `mobile/src/wire/codec.test.ts` fails (vectors are v2).
- From B2-1 until B5-2, `go build -tags lola_insecure` does not compile and the
  CI step that builds it is red; B5-2 deletes that step.
- Untagged daemons have no phone listener until B3-3 wires `startRemote`, so the
  temporary loss of the old denylist's talk entries (A1-1 moves them out of
  `SSHHumanCommands`) reaches no peer.

---

## 3. Items

Format: **Id — title** · Specs · Owns · Also lands · Provides · Tests · Verify.

### Wave A1

**A1-1 — Protocol: pairing, tiers, Conversation split, timeline, relay, hub status**
- Specs: 01 §6.1, §6.4 (`PairRequest`, `PairResult`, `PairRelay`), §6.6 (function vectors), §8.4, §8.6 (args type); 03 §10.6 (`relayCred` types), §12.2.
- Owns: `internal/protocol/devices.go` (new), `internal/protocol/devices_test.go` (new), `internal/protocol/sessiontimeline.go` (new), `internal/protocol/relay.go` (new), `internal/protocol/talk.go`, `internal/protocol/ssh.go`, `internal/protocol/hub.go`, `internal/protocol/protocol.go` (old `PairBeginData` and M1 comments removed, `Request.Cmd` comment), `internal/protocol/pairing_vectors_test.go` (new), `internal/protocol/testdata/pairing_vectors.json` (new); `internal/daemon/talk_contract_test.go` (hunk: list `TalkCommands` instead of `SSHHumanCommands`; `CommandDenied` call stays until B1-2), `internal/daemon/ssh_test.go` (hunk: iterate `TalkCommands` as a third list), `internal/daemon/talk.go` (comment only); deletes `internal/daemon/pairbegin_off_test.go` (it reads removed fields).
- Provides:
  ```go
  const CmdPairBegin, CmdPairStatus, CmdPairConfirm, CmdPairReject, CmdDevices, CmdRevokeDevice, CmdSetDeviceTiers, CmdRemotePair string
  var LocalOnlyCommands []string
  const TierRead, TierAnswer, TierControl, TierDestructive, TierShell, TierNever string
  var Tiers, DefaultTiers []string
  const PairStateWaiting, PairStateConfirming, PairStatePaired, PairStateRejected, PairStateExpired, PairStateFailed, PairStateCancelled string
  const PairReasonTooManyAttempts, PairReasonCodeMismatch, PairReasonPhoneLeft, PairReasonRegistry, PairReasonFull string
  type PairBeginArgs, PairBeginData, PairStatusArgs, PairStatusData /* incl. Via */, PairConfirmArgs, PairRejectArgs,
       DeviceInfo, DevicesData, RevokeDeviceArgs, RevokeDeviceData, SetDeviceTiersArgs, PairRequest, PairResult, PairRelay /* {RV, Bearer} */
  func PairProof(secret, exporter []byte) []byte
  func PairCode(exporterCode []byte, daemonSPKI, phoneSPKI [32]byte) string
  var TalkCommands []string // incl. CmdSSHRecall, CmdSSHAnswer; SSHHumanCommands loses those 11
  const CmdSessionTimeline = "sessionTimeline"; type SessionTimelineArgs struct{ Session, Conn string; After int64; Limit int }
  const CmdRelayCred = "relayCred"; type RelayCredArgs struct{ Holds string }; type RelayCredData struct{ Available bool; URL, RV, Bearer string }
  type HubRelayInfo struct{...}; HubStatusData.Relay *HubRelayInfo; const HubRelayStateOff … HubRelayStateRefused
  ```
- Tests: `TestPairingVectors`, `TestTalkAndSSHListsAreDisjoint`, `TestLocalOnlyCommandsNamed`, `TestPairStatusHasNoCodeField`, `TestHubStatusRelayCarriesNoSecrets` (marshal shape only); updated `TestTalkCommandsAreHumanOnly`, `TestSSHEveryCommandIsHandled`.
- Verify: `make check`.

**A1-2 — Protocol: envelope v2, events, attach types, frame vectors**
- Specs: 01 §4.6 (`FrameReady`, `ReadyData`, codes), §6.5, §6.6 (frame vectors); 02 §2, §3, §4, §5, §8.1, §9 (wire limits), §12.1, §12.3 (types), §13.
- Owns: `internal/protocol/frame.go`, `internal/protocol/events.go` (new), `internal/protocol/attach.go` (new), `internal/protocol/events_test.go` (new), `internal/protocol/goldenvectors_test.go`, `mobile/src/wire/testdata/frames.json`, `mobile/src/wire/testdata/events.json` (new), `mobile/src/wire/testdata/pairing.json` (new).
- Provides:
  ```go
  const FrameVersionCurrent = 2; const FrameVersionMin = 2
  Frame.Topic string `json:"topic,omitempty"` // after Pane
  const FrameEvent = "event"; const FrameReady = "ready" // both DirToClient
  type ReadyData struct{ DeviceID, Name string; Tiers []string; Via, Host string }
  const CodeNotPaired, CodePairRejected, CodePairExpired, CodePairFailed string
  const TopicSessions, TopicProjects, TopicStatus, TopicTalk, TopicFeed, TopicTimeline, TopicDiff string; const MaxTopicBytes = 160
  func ParseTopic(t string) (kind, session string, ok bool)
  const CmdWatch, CmdUnwatch, CmdAttachImage, CmdAttachmentImage string
  type WatchArgs, UnwatchArgs, WatchData, WatchResult; const WatchCodeInvalidTopic, WatchCodeUnknownSession, WatchCodeLimit, WatchCodeUnavailable string
  type SessionsEvent, FeedEvent, OmittedItem, TimelineEvent, DiffEvent, TalkEvent, StatusEvent, ProjectsEvent; const DiffCause*
  type TopicEvent interface{ Merge(TopicEvent) (TopicEvent, bool); Size() int; Part() bool }
  func SizeOf[T TopicEvent](ev T) T
  const MaxWatchIDBytes, EnvelopeMaxBytes, MaxWatchTopicsPerRequest, MaxWatchTopicsPerConn, EventMaxBytes, EventItemMaxBytes,
        FeedSnapshotItems, FeedDeltaItems, TimelineSnapshotItems, TimelineDeltaItems, TimelineSnapshotMaxBytes; WatchRetryAfter
  type AttachImageArgs, AttachImageData, AttachmentImageArgs, AttachmentImageData; const AttachChunkBytes, AttachMaxBytes, AttachMaxParts
  var AttachMimes map[string]string; const AttachErr* (02 §12.1, §12.3)
  ```
- Tests: `TestParseTopic`, `TestFrameEventDirection`, `TestEventBudgetFitsFrame`, `TestSizeOfIsEncodedLength`, `TestSessionsEventMerge`, `TestFeedEventMerge`, `TestTimelineEventMerge`, `TestGoldenVectors*` over the directory (union covers `event` and `ready`), `TestWriteGoldenVectors` (`LOLA_WRITE_VECTORS=1`).
- Verify: `make check`.

**A1-3 — hubproto WebSocket codec**
- Specs: 03 §2 (incl. `WSClose*` 1000–1011 constants in `ws.go`).
- Owns: `hubproto/ws.go` (new), `hubproto/ws_test.go` (new).
- Provides: 03 §2.1 verbatim (`WSRole`, `WSConfig`, `WSConn` + methods, `CheckUpgrade`, `AcceptWebSocket`, `WSDialer.Dial`, `WSHandshakeError`, `ErrWS*`), constants of 03 §2.2, `WSCloseNormal … WSCloseInternal`.
- Tests: 03 §18 "hubproto" `TestWS*` list.
- Verify: `make check`; `cd hub && make check`.

**A1-4 — hubproto relay wire**
- Specs: 03 §3 (incl. `RelayFreshCredentialGrace`), close codes 4000–4004.
- Owns: `hubproto/relay.go` (new), `hubproto/relay_test.go` (new).
- Provides: `PathRelay*`, `HeaderRelayRV`, `HeaderRelayBearer`, `HeaderRelayCode`, `QueryRelayConn`, `QueryRelayVersion`; `NewRelayID`, `NewRelayBearer`, `RelayBearerHash`, `ValidRelayID`, `ValidRelayBearer`, `ValidRelayHash`; `RelayRendezvous`, `RendezvousRequest` (+`Validate(now)`), `RendezvousResponse`, `RelayMsg`; `CodeOffline`, `CodeBusy`, `CodeRelayTimeout`; `RelayCloseReplaced … RelayClosePeerGone`; every limit of 03 §3.6.
- Tests: `TestRendezvousValidate`, `TestRelayIDShapes`, `TestRelayBearerHash`, `TestRelayMsgLenient`, `TestRelayConstantsOrdering`.
- Verify: `make check`; `cd hub && make check`.

**A1-5 — QR leaf**
- Specs: 01 §2 (`internal/qr`), §9.1 rendering, §11 `internal/qr`.
- Owns: `internal/qr/qr.go`, `internal/qr/terminal.go`, `internal/qr/qr_test.go`, `internal/qr/testdata/vectors.json` (all new; matrices generated once from `desktop/frontend/src/lib/qr.ts` with a throwaway script outside the repo; `qr.ts` is not edited).
- Provides:
  ```go
  type Level int; const (L Level = iota; M; Q; H)
  type Matrix struct{ Size int /* modules per side */ }; func (m *Matrix) Dark(x, y int) bool
  func Encode(data []byte, lvl Level) (*Matrix, error)
  type TerminalOptions struct{ NoColor bool; Columns int }
  var ErrTooWide error // Size+8 > Columns
  func Terminal(m *Matrix, o TerminalOptions) (string, error) // quiet zone 4, two module rows per line, SGR per 01 §9.1
  ```
- Tests: port of `qr.test.ts`, `TestMatchesTSVectors`, `TestTerminalSGRDarkModules`, `TestTerminalNoColorInverts`, `TestTerminalTooNarrow`.
- Verify: `make check`.

**A1-6 — Commit hooks in leaves**
- Specs: 02 §7.2 (`session.Change`, `OnChange`), §7.5 (`SetNotify`).
- Owns: `internal/session/session.go`, `internal/session/onchange_test.go` (new), `internal/sshstore/sshstore.go`, `internal/sshstore/notify_test.go` (new).
- Provides: `type session.Change struct{ ID string; Prev, Next *Session }`, `func (s *Store) OnChange(fn func(Change))`; `func (s *sshstore.Store) SetNotify(fn func())`.
- Tests: `TestOnChangeFiresForEveryCommit`, `TestSetNotifyAfterUnlock`.
- Verify: `make check`.

**A1-7 — Config: `[remote].relay`**
- Specs: 03 §12.1, D2.
- Owns: `internal/config/remote.go` (hunk: `RelayOff`, `RelayOn`, `Serves`, `fileRemoteConfig.Relay`, resolve/mirror, `RemoteBinds` comment; `InsecureLAN` stays until B5-1), `internal/config/remote_test.go`, `config.example.toml` (`[remote]` block: drop `insecure_lan` and M1 sentences, add `relay`, state pairing and `bind = "off"` semantics per 01 §10 and 03 §12.1).
- Provides: `func (r RemoteConfig) RelayOn() bool`, `func (r RemoteConfig) Serves(hubJoined bool) bool`, field `RelayOff bool`.
- Tests: `TestRemoteRelayDefaultAndServes`.
- Verify: `make check`.

### Wave A2

**A2-1 — Device registry**
- Specs: 01 §3.
- Owns: `internal/devices/devices.go`, `internal/devices/journal.go`, `internal/devices/devices_test.go` (all new).
- Provides: 01 §3.1 verbatim (`Device`, `DeviceRelay`, `Registry`, `Open`, `Err`, `Unsaved`, `Lookup`, `Get`, `LookupRV`, `List`, `Add(name, platform string, spki [32]byte, tiers []string, relay *DeviceRelay)`, `Revoke`, `SetTiers`, `SetRelay`, `Touch`, `Subscribe`, `HashSPKI`, `IDFor`, constants, errors).
- Tests: 01 §11 `internal/devices` list (incl. `TestRelayRegexesMatchHubproto`, which imports `hubproto` in the test only).
- Verify: `make check`.

**A2-2 — Event mailbox**
- Specs: 02 §8.
- Owns: `internal/eventbox/box.go`, `internal/eventbox/box_test.go` (new).
- Provides: 02 §8.2 (`Item`, `New(onSnap func(topic string)) *Box`, `Start`, `WantsSnapshot`, `Install`, `Offer`, `End`, `Stop`, `Next`, `Close`), constants `SlotQueueMax`, `ConnPendingMaxBytes`, `BoxSnapshotMaxBytes`, `ResnapshotLimit`, `ResnapshotWindow`.
- Tests: 02 §15 `internal/eventbox` list.
- Verify: `make check`.

**A2-3 — Remote tiers and request rebuild**
- Specs: 01 §8.1, §8.3, §8.5; 02 §10.4 rows; 03 §13.5 rule 6; 04 §2.2 (tier golden file).
- Owns: `internal/remote/tiers.go`, `internal/remote/tiers_test.go`, `internal/remote/fields.go`, `internal/remote/fields_test.go`, `internal/remote/tiers_golden_test.go`, `mobile/src/wire/testdata/tiers.json` (all new). Must not declare any identifier `policy.go` declares (`CommandDenied`, `normalizeRequest`, `auditable`, `mutatingCommands`, `deniedCommands`, `logSafe`); `policy.go` is deleted in B2-1.
- Provides:
  ```go
  func TierFor(cmd string) string
  func CommandTiers() map[string]string
  var TransportCommands = []string{protocol.CmdWatch, protocol.CmdUnwatch}
  func frameTier(f *protocol.Frame) string
  type checkResult struct{ dropped []string; attachBytes int; attachLast bool }
  func rebuild(cmd string, in protocol.Request, has func(tier string) bool) (protocol.Request, checkResult, error)
  ```
- Tests: 01 §11 tiers + fields lists (incl. `TestRelayCredIsReadWithFieldSpec`, `TestRemoteArgsFieldsAreReviewed`), `TestTierGolden` (writes with `LOLA_WRITE_VECTORS=1`).
- Verify: `make check`.

**A2-4 — Remote pairing payload**
- Specs: 01 §6.2 (`pairpayload.go`), §6.6.
- Owns: `internal/remote/pairpayload.go`, `internal/remote/pairpayload_test.go` (new).
- Provides: `PairPayloadPrefix`, `PairPayloadVersion`, `MaxPairPayloadBytes`, `type PairPayload`, `type PairRelayQR struct{ URL, RV, Tok string }`, `EncodePairPayload(PairPayload) (string, error)`, `DecodePairPayload(string) (PairPayload, error)`, `ErrNotPairPayload`.
- Tests: `TestPairPayloadRoundTripAndBounds`, `TestPairPayloadRejectsLola1`, `TestPairPayloadVectors`.
- Verify: `make check`.

**A2-5 — Hub store relay entries**
- Specs: 03 §4.
- Owns: `hub/internal/store/relay.go` (new), `hub/internal/store/relay_test.go` (new), `hub/internal/store/store.go` (hunk: `Data.Relay`, `init` lines).
- Provides: `type RelayEntry`, `(*RelayEntry).Window() bool`, `.Live(now) bool`, `.Matches(bearerHash string, now time.Time) bool`, `(*Data).RelayEntriesOf(device string) []*RelayEntry`.
- Tests: `TestStoreRelayEntryMatchesConstantTime`, `TestStoreOldSnapshotLoads`, `TestStoreNewDataHasRelayMap`.
- Verify: `cd hub && make check`.

**A2-6 — Daemon relay client (`internal/hub`)**
- Specs: 03 §10.1.
- Owns: `internal/hub/relay.go` (new), `internal/hub/relay_test.go` (new), `internal/hub/client.go` (hunk: `SyncRendezvous`, `APIError.RetryAfter`).
- Provides: `type RelayClient struct{ BaseURL string; Key *Key; Dialer hubproto.WSDialer }`, `(*Client).SyncRendezvous(ctx, []hubproto.RelayRendezvous) (hubproto.RendezvousResponse, error)`, `(*RelayClient).DialControl(ctx) (*hubproto.WSConn, error)`, `(*RelayClient).DialAccept(ctx, conn string) (*hubproto.WSConn, error)`, `RelayStatusFile`, `type RelayStatus`, `SaveRelayStatus(dir string, st RelayStatus) error`, `LoadRelayStatus(dir string) (RelayStatus, bool)`, `APIError.RetryAfter time.Duration`.
- Tests: handshake-error conversion through `cleanCode`/`cleanText` (part of `TestRelayHandshakeErrorIsCleaned`), `SyncRendezvous` drops foreign conflicts, status file round trip 0600.
- Verify: `make check`.

**A2-7 — Hub relay core**
- Specs: 03 §6.
- Owns: `hub/internal/relay/hub.go`, `pending.go`, `splice.go`, `limits.go` and their `*_test.go` (all new).
- Provides: 03 §6.1 API verbatim (`New`, `AllowIP`, `AllowUnauth`, `AllowAuth`, `Reserve`, `AttachControl`, `Offer`, `Claim`, `Deliver`, `Take`, `Abandon`, `Splice`, `Retain`, `Recheck`, `Shutdown`, `ErrBusy`, `ErrOffline`).
- Tests: 03 §18 hub list items that need no HTTP (`TestCaps`, `TestShaping`, `TestSpliceNonReadingPeerCloses`, `TestIdleClose`, `TestDailyQuota`, `TestHalfClose`, `TestAbandonDrainsReadyLeg`, `TestDialBuckets` core half).
- Verify: `cd hub && make check`.

### Wave B1

**B1-1 — Remote auth types, Pairer, device authorizer**
- Specs: 01 §4.2, §4.3, §4.4, §6.3, §6.4; 03 §13.5 rule 4.
- Owns: `internal/remote/auth.go` (ADDITIVE hunk: `ViaLAN`, `ViaRelay`, `Peer.Via`, `Handshake.Via`, `Handshake.RelayRV`, `Handshake.SetReadDeadline`, `relayed`, `ErrPairingDone`; `Peer.Insecure` and the M1 fields stay until B2-1), `internal/remote/pairing.go`, `internal/remote/pairing_test.go`, `internal/remote/deviceauth.go`, `internal/remote/deviceauth_test.go` (new); deletes `internal/remote/connectcode.go`, `connectcode_test.go`.
- Provides:
  ```go
  func NewPairer(reg *devices.Registry, now func() time.Time, logf func(string, ...any)) *Pairer
  type Ticket struct{ ID string; Secret []byte; ExpiresAt time.Time }
  func (p *Pairer) Begin() (Ticket, error)
  func (p *Pairer) SetWindowRelay(id, rv, tokHash string) bool
  func (p *Pairer) Window() (rv, tokHash string, expires time.Time, ok bool)
  func (p *Pairer) Status(ctx context.Context, a protocol.PairStatusArgs) (protocol.PairStatusData, error)
  type ConfirmRelay struct{ Bearer string; Record devices.DeviceRelay }
  func (p *Pairer) Confirm(a protocol.PairConfirmArgs, relay *ConfirmRelay) (protocol.PairStatusData, error)
  func (p *Pairer) Reject(id string) (protocol.PairStatusData, error)
  const PairStatusMaxWait = 25 * time.Second
  func newDeviceAuthorizer(reg *devices.Registry, p *Pairer, host string, now func() time.Time, logf func(string, ...any)) *deviceAuthorizer // implements Authorizer
  ```
- Tests: 01 §11 "pairing" list and the unit half of "auth" (fake `Handshake` built from a real `tls.Client`/`tls.Server` pair over `net.Pipe`): `TestRelayRVMustMatchDevice`, `TestRelayedCandidateNeedsWindowRV`, `TestNotPairedFrameOnPostHandshakeRefusal` (authorizer level), `TestSetWindowRelayOnlyWhileWaiting`.
- Verify: `make check`.

**B1-2 — Daemon tests off `CommandDenied`**
- Specs: 01 §13.1 W5e table, §8.4.
- Owns: `internal/daemon/sshwidget_test.go`, `sshprompt_test.go`, `reviewverify_test.go`, `reviewcapture_test.go`, `reviewpackage_test.go`, `talk_contract_test.go` (each: the `CommandDenied` assertions only).
- Provides: nothing; unblocks B2-1 deleting `policy.go`.
- Tests: the rewritten assertions (exact tiers per 01 W5e table; `TestTalkCommandsAreHumanOnly` asserts `read` for feed/feedOutput/talkState/endedSessions and `answer` for the rest incl. `sshRecall`, `sshAnswer`).
- Verify: `make check`.

**B1-3 — Hub relay API**
- Specs: 03 §5.
- Owns: `hub/internal/api/relay.go` (new), `hub/internal/api/relay_test.go` (new), `hub/internal/api/api.go` (hunks: four routes, `messages` entries, `Config.Relay *relay.Hub`, `Config.ClientIP`), `hub/internal/api/teams.go` (hunk: `Recheck` after revoke commit).
- Provides: `(*Server).RelaySweep()`; HTTP behaviour of 03 §5.
- Tests: 03 §18 hub HTTP list (`TestRelayEndToEnd`, `TestPhoneDialUniform401`, `TestPhoneDialOffline503`, `TestPhoneDialTimeout504`, `TestPhoneDialDaemonReject`, `TestPhoneGetsNo101BeforeDaemon`, `TestAcceptAfterPhoneGoneClosesLeg`, `TestAcceptOtherDeviceConn404`, `TestAcceptReplayRefused`, `TestControlReplaced`, `TestRendezvous*`, `TestWindowRemovalKeepsSplice`, `TestRecheckClosesRevoked`, `TestOriginRefused403`, `TestRelayDisabledIs404`).
- Verify: `cd hub && make check`.

**B1-4 — Hub deploy and hub docs**
- Specs: 03 §9, §17.2, §17.3.
- Owns: `hub/deploy/k8s/overlays/production/ingress.yaml`, `hub/deploy/k8s/base/deployment.yaml`, `hub/DESIGN.md`, `hub/AGENTS.md`, `hub/README.md`.
- Provides: nothing to code.
- Verify: `cd hub && make check` (unchanged code) and `kubectl kustomize hub/deploy/k8s/overlays/production >/dev/null` if `kubectl` is installed.

**B1-5 — Remote rate limits**
- Specs: 01 §7 (buckets, `extCommands`, attach accounting).
- Owns: `internal/remote/ratelimit.go`, `internal/remote/ratelimit_test.go` (new).
- Provides:
  ```go
  var extCommands map[string]bool // prs, tickets, prReadiness, mergePR, addressPRFeedback, openPr, openTicket
  type limiter struct{ /* map[string]*deviceLimits, now */ }
  func newLimiter(now func() time.Time) *limiter
  func (l *limiter) allowReq(dev, tier, cmd string, attachBytes int, attachLast bool) bool // tier bucket, extRate, attach bytes
  func (l *limiter) allowPTY(dev string, n int) bool
  func (l *limiter) allowSub(dev string) bool
  func (l *limiter) forget(dev string)                       // DropDevice
  func (l *limiter) sweep(live func(dev string) bool)        // idle 10 min
  ```
- Tests: `TestExtCommandsAreTheExternalOnes`, `TestAttachPartsChargeBytesNotWrites`, bucket arithmetic on an injected clock.
- Verify: `make check`.

**B1-6 — Doctor rows (`phones`, `phone relay`)**
- Specs: 01 §10 (`phones` row, `FixPhonesRegistry`); 03 §12.3.
- Owns: `internal/doctor/fix.go` (`FixPhonesRegistry = "phones_registry"`, `FixRelayHubUpgrade = "relay_hub_upgrade"`, `AllFixIDs`), `internal/doctor/doctor.go` (hunk: both rows wired), `internal/doctor/phones.go` (new: `PhonesResult(home string) Result`), `internal/doctor/phones_test.go`, `internal/doctor/relayready.go`, `internal/doctor/relayready_test.go` (new), `desktop/frontend/src/lib/components/SetupChecklist.svelte` (two `FIXES` entries only).
- Provides: `PhonesResult`, `RelayResult(cfg *config.Config, dir string, daemonUp bool) (Result, bool)`.
- Tests: `TestPhonesRowFailsOnBrokenOrUnsavedRegistry`, `TestRelayDoctorRows`; `desktop/doctorsvc_test.go` `TestEveryDoctorFixHasAnAppAction` passes unchanged.
- Verify: `make check`; `cd desktop/frontend && npm run check && npm test`.

### Wave B2

**B2-1 — Remote transport**
- Specs: 01 §1 (remote deletions), §4.1, §4.5, §4.6, §5, §5.1, §7, §8.1 (`conn.authorize`), §8.8; 02 §10.1–10.2 (call sites only); 03 §11.1 H1, §11.2, §11.3.
- Owns: `internal/remote/server.go`, `conn.go`, `identity.go`, `identity_test.go`, `auth.go` (removes `Insecure`, `Path`, M1 types), `audit.go` (new: `logSafe` and the audit line, moved from `policy.go`), `peerctx.go` (new), `events.go` (new, STUB bodies, owned by B3-1 afterwards), `doc.go`, `bind.go`, `bind_test.go`, `harness_test.go`, `server_test.go`, `listen_test.go`, `pane.go` (pty-write audit, pane limits via `limiter`), `pane_test.go`, `transport_test.go` (new: real-TLS integration tests); deletes `insecure.go`, `insecure_off.go`, `insecure_test.go`, `insecure_off_test.go`, `policy.go`, `policy_test.go`.
- Also lands: 02's `Options.Events`, the `dispatchEvents`/`WithPeer` call sites in `dispatchReq` and `stopEvents` in `shutdown`; 03's `Options.RelayOnly`, zero-address `listen` path, relay pool fields (`relaySlots`, `relayPerRV`), `serveConn(nc net.Conn, release func())`, concurrent `closeConns`.
- Provides:
  ```go
  type Options struct{ Bind, Dir string; Port int; Handle func(context.Context, protocol.Request) protocol.Response; Panes PaneBus;
      Devices *devices.Registry; Pairer *Pairer; Host string; Now func() time.Time; Logf func(string, ...any);
      HandlerGrace time.Duration; Events Events; RelayOnly bool }
  func Listen(ctx context.Context, opts Options) (*Server, error)
  func (s *Server) DropDevice(id string) int
  func (s *Server) DeviceConns(id string) int
  func (s *Server) Addrs() []BindAddr; func (s *Server) Port() int // unchanged signatures; empty / 0 for a relay-only server
  // events.go (interface final; bodies stubbed until B3-1):
  type Events interface {
      Watch(ctx context.Context, peer Peer, topic, watchID string, box *eventbox.Box) (stop func(), res protocol.WatchResult)
      Resnapshot(topic string)
  }
  const maxWatchingConns = 8
  func (c *conn) dispatchEvents(id string, req protocol.Request) bool // stub: false
  func (c *conn) stopEvents()                                      // stub: no-op
  // peerctx.go
  func WithPeer(ctx context.Context, p Peer) context.Context
  func PeerFromContext(ctx context.Context) (Peer, bool)
  // server.go fields used by B3-2: s.tlsConfig *tls.Config; s.relaySlots chan struct{}; s.relayPerRV map[string]int (under s.mu); s.closed bool; s.wg
  ```
- Tests: 01 §11 "auth" (real TLS), "transport" and "audit" lists; `TestUntaggedBuildListens`-equivalent at the remote level (`TestListenNeedsDevicesAndPairer`, `TestUnsavedRegistryRefusesListen`).
- Verify: `make check`.

**B2-2 — Hub process**
- Specs: 03 §8.
- Owns: `hub/cmd/lola-hub/serve.go`, `hub/cmd/lola-hub/serve_test.go`, `hub/e2e/e2e_test.go` (relay case).
- Provides: `--client-ip` flag; `statusWriter.Unwrap`; `RegisterOnShutdown(rh.Shutdown)`; sweep/recheck tickers.
- Tests: `TestServeHijackThroughAccessLog`, `TestServeShutdownClosesLegs`, `TestDialBuckets` per-network half, the e2e relay case.
- Verify: `cd hub && make check`.

### Wave B3

**B3-1 — Remote event push**
- Specs: 02 §10.
- Owns: `internal/remote/events.go` (replaces B2-1's stub bodies), `internal/remote/events_test.go` (new).
- Provides: real `dispatchEvents`, `stopEvents`, the pump with per-event `AuthorizeFrame`, box slots under `maxWatchingConns`.
- Tests: 02 §15 `internal/remote` list incl. `TestWatchingConnsBounded`.
- Verify: `make check`.

**B3-2 — Remote relay acceptor**
- Specs: 03 §11.1.
- Owns: `internal/remote/relay.go`, `internal/remote/relay_test.go` (new).
- Provides: `const maxRelayConnections = 4; const maxRelayConnsPerRV = 2`; `var ErrServerClosed`; `type RelayMeta struct{ RV string }`; `type RelaySlot`; `func (sl *RelaySlot) Release()`; `func (s *Server) ReserveRelay(rv string) (*RelaySlot, bool)`; `func (s *Server) ServeRelayed(nc net.Conn, meta RelayMeta, slot *RelaySlot) error`; `relayNetConn` implementing `relayed`.
- Tests: `TestServeRelayedUsesListenerTLSConfig`, `TestServeRelayedEndToEnd`, `TestRelayedConnSurvivesIdlePings`, `TestServeRelayedRacesClose`, `TestRelaySlotsSeparateFromLAN`, `TestServeRelayedRVBinding`, `TestStopRemoteBoundedWithRelayConns`, `TestMaxRelayConnectionsMatchesHubproto`.
- Verify: `make check`.

**B3-3 — Daemon wiring and stubs**
- Specs: 01 §5 (wiring), §8.7, §13.1 W5b; 02 §7.2 (`OnChange` registration), §7.4 (`setLinOK` sites), §16 daemon hunks; 03 §11.3, §19.1 H4.
- Owns: `internal/daemon/server.go`, `internal/daemon/daemon.go`, `internal/daemon/remotewire.go`, `internal/daemon/remotelisten.go` (untagged), `internal/daemon/remotewire_test.go`, `internal/daemon/remotetiers_test.go` (new), `internal/daemon/remotewire_integration_test.go`, `internal/daemon/mdnswire_test.go` (untagged; devices registered with `Registry.Add` instead of pairing), the stub files below (new); deletes `pairbegin.go`, `pairbegin_off.go`, `pairbegin_test.go`, `regeneratekey.go`, `regeneratekey_off.go`, `remotelisten_off.go`, `remotewire_off_test.go`.
- Lands in `server.go`: cases for the 7 `LocalOnlyCommands`, `sessionTimeline`, `relayCred`, `attachImage`/`attachmentImage` through `if r, ok := d.handlePush(ctx, req); ok { return r }` at the top of `handle`; removes `regenerateRemoteKey`; `handleReload`: `Serves` comparison, `hubRelayNudge`, `PokeGlobal(pokeSessionsAll|pokeProjects|pokeStatus)`; `handleEnable`: the same `PokeGlobal`. In `daemon.go`: fields `devices`, `pairer`, `watch *watchHub`, `hubRelay *hubRelay`, `remoteServes atomic.Bool`; `newDaemon` creates registry, pairer, `d.devices.Subscribe(d.devicesChanged)`, `d.watch = newWatchHub(d)`, the `OnChange` registration; `Run` starts `d.watch.run` and `d.hubRelayLoop` on `d.wg`; the four `d.linOK =` → `d.setLinOK(…)`. In `remotewire.go`: `startRemote` (`Serves` gate, `Options{Devices, Pairer, Host, Events: d.watch, RelayOnly}`, `Unsaved` refusal, relay-only log line, no advertiser without addresses, `hubRelayNudge`), `stopRemote` nudge, `handleRemote` + `remoteMayAnswer` (sessions strip via `remoteSessionInfo`), `devicesChanged`, `dropRemoteDevice(id string) int`.
- Stub files (each deleted by the named B4 item, which defines the same signatures for real):

  | Stub file | Deleted by | Functions (exact signatures) |
  |---|---|---|
  | `zz_stub_pairing.go` | B4-1 | `func (d *Daemon) handlePairBegin(ctx context.Context, a protocol.PairBeginArgs) (protocol.PairBeginData, error)`; `handlePairStatus(ctx context.Context, a protocol.PairStatusArgs) (protocol.PairStatusData, error)`; `handlePairConfirm(a protocol.PairConfirmArgs) (protocol.PairStatusData, error)`; `handlePairReject(a protocol.PairRejectArgs) (protocol.PairStatusData, error)`; `handleDevices() protocol.DevicesData`; `handleRevokeDevice(a protocol.RevokeDeviceArgs) (protocol.RevokeDeviceData, error)`; `handleSetDeviceTiers(a protocol.SetDeviceTiersArgs) (protocol.DeviceInfo, error)` |
  | `zz_stub_timeline.go` | B4-2 | `func (d *Daemon) handleSessionTimeline(a protocol.SessionTimelineArgs) (protocol.SSHTimelineData, error)` |
  | `zz_stub_watch.go` | B4-3 | `type watchHub struct{…}`; `type pokeBits uint8` + the 8 `poke*` constants; `func newWatchHub(d *Daemon) *watchHub`; `(h *watchHub) run(ctx context.Context)` (stub: `defer h.d.wg.Done()`, return); `PokeSession(id string, bits pokeBits)`; `PokeDiff(id, cause string)`; `PokeGlobal(bits pokeBits)`; `Resnapshot(topic string)`; `Watch(ctx context.Context, peer remote.Peer, topic, watchID string, box *eventbox.Box) (func(), protocol.WatchResult)` (stub answers `WatchCodeUnavailable`); `func remoteSessionInfo(si protocol.SessionInfo) protocol.SessionInfo`; `func talkFieldsChanged(prev, next *session.Session) bool`; `func headChanged(prev, next *session.Session) bool`; `func (d *Daemon) setLinOK(v bool)` |
  | `zz_stub_pokes.go` | B4-4 | `func (d *Daemon) talkReadErr(id string) bool`; `func (h *feedHub) pin(id string)`; `func (h *feedHub) unpin(id string)` |
  | `zz_stub_push.go` | B4-5 | `func (d *Daemon) handlePush(ctx context.Context, req protocol.Request) (protocol.Response, bool)` (stub: `false`) |
  | `zz_stub_relay.go` | B4-6 | `type hubRelay struct{}`; `func (d *Daemon) hubRelayLoop(ctx context.Context)` (stub: `defer d.wg.Done()`, return — `Run` does `d.wg.Add(1)` before both); `hubRelayNudge()`; `hubRelayDirty()`; `hubRelayJoined(h config.HubConfig) bool` (stub: false); `hubRelayRelease(ctx context.Context)`; `relayEligible() bool` (false); `relayPairOffer(ctx context.Context, t remote.Ticket) *remote.PairRelayQR` (nil); `relayMint() *remote.ConfirmRelay` (nil); `handleRelayCred(ctx context.Context, a protocol.RelayCredArgs) (protocol.RelayCredData, error)`; `relayStatus() *protocol.HubRelayInfo` (nil) |

- Tests: `TestEveryHandledCommandHasATier`, `TestTierTableNamesOnlyRealCommands`, `TestHandleRemoteRefusesLocalOnly`, `TestPairCommandsNotOnBridge`, `TestHandleRemoteRefusesSSHScopedAnswer`, `TestRemoteRecallTakesBackQueuedMessage`, `TestUntaggedBuildListens`, `TestRegistrySurvivesListenerReload`, `TestNewDaemonHasRegistryAndPairer`, `TestRevokeUnsavedStopsListener`, `TestReloadIdenticalRemoteDoesNotRebind`.
- Verify: `make check`.

### Wave B4

**B4-1 — Pairing and device handlers**
- Specs: 01 §6.1–6.4 (daemon side), §8.8 device log lines, §6.2 `listenerDial`; 03 §19.1 H7.
- Owns: `internal/daemon/pairing.go`, `internal/daemon/pairing_test.go` (new); deletes `zz_stub_pairing.go`.
- Uses: `d.relayPairOffer`, `d.relayMint` (stub until B4-6, same signatures), `d.dropRemoteDevice`.
- Tests: 01 §11 daemon pairing list (`TestPairBegin*`, `TestPairConfirmWritesRegistryAndAudits`, `TestRevokeDeviceDropsConnections`, `TestRevokeNotifiesRelay` (the test subscribes its own counter to `d.devices`; the revoke must fire it), `TestSetDeviceTiersDropsConnections`, `TestDevicesReportsConnected`).
- Verify: `make check`.

**B4-2 — `sessionTimeline` and the tickets cache**
- Specs: 01 §8.6, §7 (`ticketsCache`).
- Owns: `internal/daemon/sessiontimeline.go`, `internal/daemon/sessiontimeline_test.go` (new), `internal/daemon/tickets.go` (hunk: `ticketsCache`, `ticketsTTL`), `internal/daemon/tickets_cache_test.go` (new); deletes `zz_stub_timeline.go`.
- Provides: `handleSessionTimeline`, `publicTimeline(protocol.SSHTimelineData) protocol.SSHTimelineData`.
- Tests: `TestSessionTimelineStripsPrivateAndConnDetails`, `TestSessionTimelinePagingMatchesSSH`, `TestPublicTimelineIsAnAllowlist`, `TestSessionTimelineHasNoPromptAnswer`, `TestTicketsCachedPerProjectAndScope`.
- Verify: `make check`.

**B4-3 — Watch hub and producers**
- Specs: 02 §4–§9 (daemon side), §7.1–§7.7.
- Owns: `internal/daemon/watchhub.go`, `watchsessions.go`, `watchprobe.go`, `watchfeed.go`, `watchtopics.go`, `watch_test.go`, `watch_lock_test.go` (all new); deletes `zz_stub_watch.go`.
- Uses: `d.talkReadErr`, `feedHub.pin/unpin` (stub until B4-4), `d.handleSessionTimeline` (stub until B4-2; tests that need real redaction run in B4-2's tests or after the wave).
- Tests: 02 §15 `internal/daemon` list except attach and the poke-site tests owned by B4-4 (`TestTimelineNotifyFromAnyWriter`, `TestDiffPokes` site half, `TestFeedPinSurvivesAgentSwitch`).
- Verify: `make check`.

**B4-4 — Poke sites**
- Specs: 02 §7.3 (feed poke, pins, `talkReadErr`), §7.4, §7.5, §7.6, §14 (talkfeed header comment).
- Owns: `internal/daemon/status.go`, `internal/daemon/ssh.go` (hunk: `SetNotify` in `sshStore`), `internal/daemon/talkfeed.go`, `internal/daemon/talkstate.go`, `internal/daemon/checkpoints.go`, `internal/daemon/pokes_test.go` (new); deletes `zz_stub_pokes.go`.
- Tests: `TestTimelineNotifyFromAnyWriter`, `TestDiffPokes`, `TestFeedPinnedNotEvicted`, `TestFeedPinSurvivesAgentSwitch`, `TestPokeNoWatchersIsFree` (against the real hub once merged; against the stub it asserts no panic).
- Verify: `make check`.

**B4-5 — Attachments and push routing**
- Specs: 02 §10.4 (unix refusal), §12.
- Owns: `internal/daemon/attachimage.go`, `internal/daemon/attachimage_test.go`, `internal/daemon/server_push.go` (all new); deletes `zz_stub_push.go`.
- Provides: `handlePush`, `handleAttachImage(ctx, a protocol.AttachImageArgs) (protocol.AttachImageData, error)`, `handleAttachmentImage(ctx, a protocol.AttachmentImageArgs) (protocol.AttachmentImageData, error)`; constants of 02 §12.2.
- Tests: 02 §15 "internal/daemon attach" list, `TestUnixSocketWatchRefused`.
- Verify: `make check`.

**B4-6 — Daemon relay loop**
- Specs: 03 §10.2–§10.6, §12.2 (`hubStatusData`), §19.1 item 11.
- Owns: `internal/daemon/hubrelay.go`, `internal/daemon/hubrelay_test.go` (new), `internal/daemon/hubwire.go` (hunk: `hubStatusData.Relay`), `internal/daemon/hubcmds.go` (hunk: `handleHubLeave` → `hubRelayRelease`); deletes `zz_stub_relay.go`.
- Tests: 03 §18 daemon list (state machine, revocation, cleanup, `TestRelayPairOfferFirstPhoneRelayOnly`, `TestRelayPairConfirmFreshRV`, `TestRelayCredTable`, `TestRelayCredUnixSocketRefused`, `TestRelayCredEmptyHoldsReissues`, `TestRelayConflictReissuesRV`, `TestRelayOnlyServerHasNoListeners`, `TestRelayOnlyServerFollowsJoin`, `TestRelayLoopNeverBlocksShutdown`).
- Verify: `make check`.

### Wave B5

**B5-1 — Desktop PhonesService and M1 settings removal**
- Specs: 01 §1 (config/TUI/desktop rows), §9.3; 03 §12.1 (`SettingsDTO.RemoteRelay`).
- Owns: `desktop/phonesvc.go`, `desktop/phonesvc_test.go` (new), `desktop/configsvc.go` (delete `RemoteInsecureLAN`, `ConnectCode`, `ConnectCodeDTO`, `RegenerateRemoteKey`; add `RemoteRelay`), `desktop/configsvc_test.go`, `desktop/main.go` (register `PhonesService`), `desktop/frontend/bindings/**` (regenerated: `cd desktop && wails3 task common:generate:bindings`), `desktop/frontend/src/lib/views/SettingsForm.svelte` + `SettingsForm.test.ts` (remove the connect panel, regenerate button and `insecure_lan` row only), `internal/config/remote.go` (hunk: delete `InsecureLAN`), `internal/config/remote_test.go` (`TestInsecureLANKeyIgnored`), `internal/tui/settingsform.go`, `internal/tui/settingsform_test.go`.
- Provides: 01 §9.3 `PhonesService` methods; bindings `PhonesService` in `desktop/frontend/bindings/github.com/sushidev-team/lola/desktop/`; `SettingsDTO.remoteRelay`.
- Tests: `TestPhonesServiceRoundTrips`, `TestInsecureLANKeyIgnored`, updated settings tests.
- Verify: `make check`; `cd desktop/frontend && npm run check && npm test`.

**B5-2 — Build and CI cleanup; mobile owned-check script**
- Specs: 01 §10 (Makefile, ci.yml), §1 rows for `contrib/` and `mobile/package.json`; this plan §0.5.
- Owns: `Makefile`, `.github/workflows/ci.yml`, `mobile/package.json` (delete `daemon`, `daemon:info`; add `"check:owned": "node scripts/check-owned.mjs"`), `mobile/scripts/check-owned.mjs` (new); deletes `contrib/lola-mobile-dev.sh`.
- Provides: `npm run check:owned -- <path…>`: exit 1 when `svelte-check --tsconfig ./tsconfig.json --output machine` reports an ERROR in a file under any given path (relative to `mobile/`), exit 0 otherwise; prints the matching lines.
- Tests: `mobile/scripts/check-owned.test.mjs` (`node --test`): parses a captured machine-output sample.
- Verify: `make check`; `cd mobile && node --test scripts/check-owned.test.mjs`.

### Wave C1

**C1-1 — `lola pair` and `lola devices`**
- Specs: 01 §9.1, §9.2.
- Owns: `paircmd.go`, `paircmd_test.go` (new, repo root), `main.go` (register `pair`, `devices`).
- Tests: `TestPairCmdConfirmsWithTypedCode`, `TestPairCmdEmptyLineRejects`, `TestPairCmdRetriesWrongCode`, `TestPairCmdNeverPrintsCode`, `TestPairCmdRejectsOnSIGINT`, `TestPairCmdShowsViaRelay`, `TestDevicesCmdResolvesName`.
- Verify: `make check`.

**C1-2 — Swift Core**
- Specs: 04 §1.1 (Core rows), §1.4 `PathRace`, §1.5 builders, §1.6, §1.8; 03 §13.2–13.4 (`RelayWire`, `RelayCredentials`).
- Owns: `mobile/plugins/lola-transport/Package.swift`, `ios/Sources/LolaTransportCore/*` except `BridgeOriginGuard.swift`, `ios/Tests/LolaTransportCoreTests/*` except `BridgeOriginGuardTests.swift` (deletes `HelloHandshake.swift` + its test).
- Provides (Swift): `PairPayload.decode(_:) throws -> PairPayload`; `PairHandshake.proof(secret:exporter:) -> String`, `.code(exporterCode:daemonSPKI:clientSPKI:) -> String`, `.body(name:proof:) -> Data`, `.classify(_:) -> PairReply`; `ClientCertificate.build(publicKeyX963:sign:) throws -> Data`; `struct PathRace { mutating func step(now: Int, event: RaceEvent) -> [RaceAction] }`; `ReconnectPolicy`; `PairingRecord`; `RelayCredentials` (`load/save/delete(daemonId:)`, `storedAt`); `RelayWire.request(url:rv:bearer:) -> URLRequest`, `RelayWire.refusal(status:code:retryAfter:) -> RelayRefusal`, `RelayPolicy`; `InboundFilter`; `LolaKeychain`; `ConnectionState`. Constants of 04 §1.2 limits and §1.4 table.
- Tests: 04 §1.8 (minus bridge guard), `RelayWireTests`, `RelayCredentialsTests`; reads `internal/protocol/testdata/pairing_vectors.json`.
- Verify: Swift Core command.

**C1-3 — Capacitor bridge guard**
- Specs: 04 §1.9.
- Owns: `ios/Sources/LolaTransportCore/BridgeOriginGuard.swift`, `ios/Tests/LolaTransportCoreTests/BridgeOriginGuardTests.swift`, `ios/Sources/LolaTransportPlugin/BridgeGuard.swift` (all new), `mobile/src/native/capacitorguard.test.ts` (new), `mobile/src/screens/dev/BridgeGuard.svelte` (new; routed by C2-4).
- Provides: `BridgeOriginGuard.admit(isMainFrame:scheme:host:port:app:) -> Bool`; `func installBridgeGuard(bridge: CAPBridgeProtocol?) -> Bool` (C2-1 calls it first in `load()`; false → every method rejects `internal`).
- Tests: `BridgeOriginGuardTests`, `capacitorguard.test.ts`.
- Verify: Swift Core command; `cd mobile && npx vitest run src/native/capacitorguard.test.ts`.

**C1-4 — TS wire v2**
- Specs: 02 §2 (TS mirror), §6.2, §18 item 2; 04 §2.2 (`wire/tiers.ts`), §2.5 (`events.ts` state machine), §2.6 (wire splits, `limits.ts`), A16 (`push.ts`).
- Owns: `mobile/src/wire/protocol.ts`, `limits.ts` (new), `codec.ts`, `codecjson.ts` (new), `events.ts` (new), `push.ts` (new), `tiers.ts` (new), `fake.ts`, `correlator.ts`, `index.ts`, and their tests (`protocol.test.ts`, `codec.test.ts`, `events.test.ts`, `tiers.test.ts`, `limits.test.ts`, `correlator.test.ts`). Keeps any export another file still imports (`commandDenied`, `DENIED_COMMANDS`, hello helpers) until C2-3/D2-3 remove the importers.
- Provides: `FRAME_VERSION_CURRENT = 2`, `FRAME_EVENT`, `FRAME_READY`, `Frame.topic`; `parseTopic`, `TopicState`, `onWatchResult`, `onEvent`, `onStreamErr`, `WatchCode`; `TIER`, `isRead(cmd)`; limits of 02 §9 and 04 §2.6; `push.ts` types.
- Tests: listed files; vectors from `testdata/*.json` via `import.meta.glob`.
- Verify: `cd mobile && npm run check:owned -- src/wire && npx vitest run src/wire`.

**C1-5 — Desktop pushed-source seams**
- Specs: 04 §3.1, §3.2 items 1–8.
- Owns: `desktop/frontend/src/lib/pushsource.ts`, `composerattach.ts` (new), `talk.svelte.ts`, `feed.svelte.ts`, `ssh.svelte.ts`, `components/session/{Timeline,Workspace,Composer,QueueRows}.svelte`, `components/ssh/SSHWidget.svelte`, and their tests.
- Provides: `PushSource`, `setPushSource`, `pushSource`, `FeedPush`, `PushError`; `setImagePicker`; `data-queue-row`, `data-widget-unavailable`.
- Tests: 04 §3.2 test list incl. `SSHWidget.test.ts`.
- Verify: `cd desktop/frontend && npm run check && npm test`.

**C1-6 — Desktop Settings → Phones**
- Specs: 04 §7.
- Owns: `desktop/frontend/src/lib/phones.svelte.ts`, `desktop/frontend/src/lib/components/phones/*` (new), `desktop/frontend/src/lib/views/SettingsForm.svelte` + `SettingsForm.test.ts`.
- Tests: `phones/PhonesPanel.test.ts`, `phones/PairDialog.test.ts` (incl. stale-pairing test, "via relay").
- Verify: `cd desktop/frontend && npm run check && npm test`.

**C1-7 — Plugin TS surface and native access layer**
- Specs: 04 §1.2 (TS), §2.1.
- Owns: `mobile/plugins/lola-transport/src/definitions.ts`, `src/index.ts`, `src/web.ts`, `mobile/src/native/index.ts`, `native/web.ts`, `native/index.test.ts` (new), `mobile/src/lib/{scan,appstate,openurl,keyboardinset}.ts` + tests, `mobile/tsconfig.json` (both paths).
- Provides: `LolaTransportPlugin` interface and types of 04 §1.2; `native`, `keyboard`, `browser`.
- Verify: `cd mobile && npm run check:owned -- src/native src/lib/scan.ts src/lib/appstate.ts src/lib/openurl.ts src/lib/keyboardinset.ts plugins/lola-transport/src && npx vitest run src/native src/lib/scan src/lib/appstate src/lib/openurl src/lib/keyboardinset`.

### Wave C2

**C2-1 — Swift plugin**
- Specs: 04 §1.2–§1.7; 03 §13.2 (leg, bridge).
- Owns: `ios/Sources/LolaTransportPlugin/*` except `BridgeGuard.swift` (splits `LolaConnection.swift` into `Session.swift`/`Connector.swift`; new `LolaTLS.swift`, `DirectPath.swift`, `RelayPath.swift`, `RelayLeg.swift`, `LoopbackBridge.swift`, `ClientIdentity.swift`, `PathMonitor.swift`, `+Media`, `+Haptics`; deletes `+Secrets`), `LolaTransport.podspec`.
- Provides: the native implementation of C1-7's `definitions.ts`; `LolaTLS.parameters(pin:identity:) -> NWParameters`.
- Verify: Swift Core command; the Swift plugin build command (§0.5). Hardware paths stay unverified (04 §14).

**C2-2 — Shim services and conformance**
- Specs: 04 §2.2–§2.4.
- Owns: `mobile/src/wailsshim/{desktop,desktop.test,index,errors,conformance}.ts`, `mobile/src/wailsshim/services/*` (+ tests).
- Verify: `cd mobile && npm run check:owned -- src/wailsshim/services src/wailsshim/desktop.ts src/wailsshim/conformance.ts src/wailsshim/index.ts src/wailsshim/errors.ts && npx vitest run src/wailsshim/services src/wailsshim/desktop.test.ts`.

**C2-3 — Push bridge, watch registry, resume**
- Specs: 04 §2.5, §2.6 bugs 1, 2, 7, §3.3.
- Owns: `mobile/src/wailsshim/{bridge,watch,channeltransport,panesubs,capacitorchannel,pluginerror,events,runtime,screen}.ts` (+ tests), `mobile/src/lib/{resume,pushsource,connection.svelte,diagnose}.ts` (+ tests), `mobile/src/main.ts`.
- Verify: `cd mobile && npm run check:owned -- src/wailsshim/bridge.ts src/wailsshim/watch.ts src/wailsshim/channeltransport.ts src/wailsshim/panesubs.ts src/wailsshim/capacitorchannel.ts src/lib/resume.ts src/lib/pushsource.ts src/lib/connection.svelte.ts src/main.ts && npx vitest run src/wailsshim/bridge src/wailsshim/watch src/lib/resume src/lib/pushsource`.

**C2-4 — Router, desk-nav, deep links, copy, tiers store**
- Specs: 04 §4.2, §5.1, §5.4, §5.5, §5.7, §6.5, §8.3.
- Owns: `mobile/src/lib/{router.svelte,desknav,deskprefs,copy,tiers.svelte,haptics,clock.svelte}.ts` (+ tests), `mobile/src/lib/devlink.ts` (+ test), `mobile/scripts/dev-link.sh`.
- Verify: `cd mobile && npm run check:owned -- src/lib/router.svelte.ts src/lib/desknav.ts src/lib/deskprefs.ts src/lib/copy.ts src/lib/tiers.svelte.ts src/lib/haptics.ts src/lib/clock.svelte.ts src/lib/devlink.ts && npx vitest run src/lib/router src/lib/desknav src/lib/copy src/lib/tiers src/lib/clock src/lib/devlink`.

**C2-5 — Bundle budget and mobile CI**
- Specs: 04 §8.1 (bundle row), §8.4, §9.
- Owns: `mobile/vite.config.ts`, `mobile/package.json` (`budget` script), `mobile/scripts/bundle-budget.mjs` (new), `.github/workflows/mobile.yml` (new).
- Verify: `cd mobile && npm run build && npm run budget` (budget may report the old entry until D; it must run and parse the manifest).

### Wave C3

**C3-1 — Design system and app shell**
- Specs: 04 §4.3, §4.4, §6, §8.2 (`VirtualList`).
- Owns: `mobile/src/app.css`, `mobile/src/phonecss.test.ts`, `mobile/src/lib/mobiletokens.ts`, `mobile/src/lib/components/{Sheet,TabBar,SegmentBar,VirtualList,Banner}.svelte` (+ tests), `mobile/src/App.svelte` (router outlet; ConfirmDialog; banners incl. `key_changed`; sheet host; `/dev/bridge-guard` route), and one-line PLACEHOLDER files that D1 replaces: `mobile/src/screens/{Connect,Inbox,Activity,Projects,ProjectDetail,PRPicker,TicketPicker,Settings}.svelte`, `mobile/src/screens/session/{Session,TerminalSegment}.svelte`. `App.svelte` lazy-imports them from a fixed route map, so no D1 item edits `App.svelte`.
- Verify: `cd mobile && npm run check:owned -- src/App.svelte src/app.css src/lib/mobiletokens.ts src/lib/components/Sheet.svelte src/lib/components/TabBar.svelte src/lib/components/SegmentBar.svelte src/lib/components/VirtualList.svelte src/lib/components/Banner.svelte && npx vitest run src/phonecss.test.ts src/lib/components/VirtualList`.

### Wave D1

**D1-1 — Connect and pairing UI** — 04 §5.2 Connect, §1.5 outcomes, §5.5 copy. Owns `mobile/src/screens/Connect.svelte`, `mobile/src/lib/{pairing.svelte,paircopy}.ts` (+ tests). Verify: owned check + vitest on them.

**D1-2 — Inbox and Activity** — 04 §5.2 Inbox/Activity, §2.6 bug 3, §8.1 keyed-delta and virtualization rows. Owns `mobile/src/screens/{Inbox,Activity}.svelte`, `mobile/src/lib/inbox.svelte.ts`, `mobile/src/lib/components/{InboxCard,InboxRow}.svelte`, `mobile/src/sheets/filter.svelte`, `lib/components/{FilterSheet,SectionHeader,StatusChip,StatusDot,MetaPill}.svelte` (+ tests). Verify: owned check + vitest.

**D1-3 — Session screen** — 04 §4.1, §4.2 (use), §5.2 Session, §5.3 sheets. Owns `mobile/src/screens/session/{Session,SessionHeader,ConversationSegment,ChangesSegment,ReviewSegment}.svelte`, `mobile/src/sheets/{sessionMenu,newSession,attach,model,merge,feedback,checkpoints,bestOf,rename,broadcast,tasks,switchAgent,reviewPackage}.svelte` (+ tests). Verify: owned check + vitest.

**D1-4 — Terminal segment and splits** — 04 §2.6 bug 4 and splits, §5.2 Terminal. Owns `mobile/src/screens/session/TerminalSegment.svelte`, `mobile/src/lib/{terminalgeometry.svelte,terminalinput,panetabs.svelte,panepin,panepinstore,viewport,keybytes,keygesture,resync}.ts`, `mobile/src/lib/components/{MobileTerminal,PaneTabs,AccessoryBar,AccessoryKey,PaneMenuSheet,ViewSettings,DevLinksSheet,TouchButton}.svelte`, `mobile/src/sheets/{devLinks,paneMenu,viewSettings}.svelte` (+ tests). Verify: owned check + vitest.

**D1-5 — Projects and Settings** — 04 §5.2 Projects/Settings. Owns `mobile/src/screens/{Projects,ProjectDetail,PRPicker,TicketPicker,Settings}.svelte`, `mobile/src/lib/{projects.svelte,settings.svelte}.ts`, `mobile/src/sheets/{connection,unpair}.svelte` (+ tests). Verify: owned check + vitest.

Each D1 item replaces the C3-1 placeholder screen files it owns; none edits `App.svelte`.

### Wave D2

**D2-1 — Preview fixtures** — 04 §5.6. Owns `mobile/preview.html`, `mobile/src/{Preview.svelte,preview.ts}`, `mobile/src/fixtures/*`. Verify: owned check; `cd mobile && npm run build`.

**D2-2 — Performance tests** — 04 §8.1 idle, send-to-echo, keyed-delta and virtualization budgets. Owns `mobile/src/perf/*.test.ts` (new). Verify: `cd mobile && npx vitest run src/perf`.

**D2-3 — Delete superseded client modules** — 04 §2.6 hygiene deletions, §13 item 18. Owns (deletes) `mobile/src/lib/{secretstore,discovery,endpoint,pairpayload,nav.svelte,sheets}.ts` (+ tests), `mobile/src/lib/testdata/connectcode.json`, `mobile/src/views/*`, `mobile/src/lib/components/{SessionCard,SessionRow,FilterRail,TriageChips}.svelte` (+ tests), leftover exports in `mobile/src/wire/protocol.ts` (`HELLO_CMD`, `INSECURE_MIN_KEY_LEN`, `helloFrame`, `DENIED_COMMANDS`, `commandDenied`). Verify: `cd mobile && npm run check && npm test`.

### Wave D3

**D3-1 — Documentation and full gate**
- Specs: 01 §10, §10.1; 02 §14; 03 §17.1; 04 §11, §2.6 (READMEs), §13 item 19.
- Owns: `AGENTS.md`, `README.md` (`lola pair`, `lola devices`, `[remote]` table incl. `relay`, `bind = "off"` note), `mobile/README.md`, `mobile/HANDOFF.md`, `mobile/PLAN.md` (header), `docs/unified-session.md` (phone rows), status lines of `docs/mobile/0[1-4]-*.md`.
- AGENTS.md merge rules: apply 01 §10.1, 02 §14, 03 §17.1 and 04 §11 in that order; where two specs rewrite the same line, use the merged text below; where two state the same rule, keep the 01 wording once.
  - **Architecture map → Core**, the `protocol` bullet becomes: "`protocol` — wire types. `remote` — the phone listener and relay acceptor (one TLS config, one frame loop for both): mutual TLS 1.3, device lookup, pairing, capability tiers, event push; `devices` — the paired-phone registry (`~/.lola/devices.json`); `eventbox` — per-connection event mailbox (coalescing, resnapshot, stream end; transport-free); `qr` — byte-mode QR for `lola pair`; `mdns` advertises the listener; `panebus` multiplexes one tmux attach per pane across subscribers."
  - **What lola is → Roles**: "`mobile/` the phone client (via `internal/remote`, directly or through the hub relay)".
  - New sections, in this order after **SSH**: **Phones** (01), **Server push** (02, placed after "Typing into an agent" as 02 says), **Phone client** (04, after "Desktop session view"); the hub relay block (03) goes under **Team hub**.
- Verify (full gate): `make check`; `cd hub && make check`; `cd desktop/frontend && npm run check && npm test`; Swift Core command; `cd mobile && npm run check && npm test && npm run build && npm run budget`.

---

## 4. Shared-file ownership index

| File | Owner by sub-wave |
|---|---|
| `internal/protocol/frame.go` | A1-2 |
| `internal/protocol/protocol.go`, `talk.go`, `ssh.go`, `hub.go` | A1-1 |
| `internal/config/remote.go`, `remote_test.go` | A1-7 (relay), B5-1 (`InsecureLAN` removal) |
| `internal/remote/auth.go` | B1-1 (additive), B2-1 (removals) |
| `internal/remote/events.go` | B2-1 (interface + stubs), B3-1 (implementation) |
| `internal/remote/server.go`, `conn.go` | B2-1 |
| `internal/daemon/server.go`, `daemon.go`, `remotewire.go` | B3-3 |
| `internal/daemon/talk_contract_test.go` | A1-1 (lists), B1-2 (`CommandDenied`) |
| `internal/daemon/hubwire.go`, `hubcmds.go` | B4-6 |
| `internal/doctor/fix.go`, `doctor.go`, `SetupChecklist.svelte` | B1-6 |
| `desktop/configsvc.go`, `desktop/main.go`, `desktop/frontend/bindings/**` | B5-1 |
| `desktop/frontend/src/lib/views/SettingsForm.svelte` (+ test) | B5-1 (M1 removal), C1-6 (Phones panel) |
| `mobile/package.json` | B5-2 (scripts), C2-5 (`budget`) |
| `mobile/src/wire/testdata/*.json` | A1-2 (`frames`, `events`, `pairing`), A2-3 (`tiers`) |
| `mobile/src/App.svelte` | C3-1 |
| `Makefile`, `.github/workflows/ci.yml` | B5-2 |
| `AGENTS.md`, `README.md` | D3-1 |
| `config.example.toml` | A1-7 |

## 5. Risks not settled by the specs

1. **TLS exporter parity.** That iOS `sec_protocol_metadata_create_secret`
   (no context) equals Go's `ExportKeyingMaterial(label, nil, 32)` is proven
   only on a device or Simulator run; the golden vectors prove the HMACs, not
   the exporter. A mismatch breaks pairing on every phone and shows up first in
   the D-wave Simulator harness (04 §5.7).
2. **Capacitor bridge guard.** That WebKit exposes `bridge` to a sandboxed
   `data:` frame, and that re-registering the handler in `load()` precedes
   every page script, is verified only by the DEBUG harness route. Widgets do
   not reach the phone (A13), so the guard is defence in depth, not the only
   barrier.
3. **Relay per-network bucket is off in production** (`--client-ip none`)
   until the shared ingress enables proxy protocol, which is outside this
   repository. The auth/unauth bucket split is the only flood control there.
4. **Relay against the deployed hub** is unverified (the hub deploys from
   `main`); a new daemon against the old hub degrades to `unsupported` by
   design.
5. **Intermediate red states** (§2 "Transitional facts"): the tagged build
   between B2-1 and B5-2 and the mobile type-check until D2-3. Any CI run on an
   intermediate state shows them.
6. **`PairStatusData.Replaces` is practically always empty**: the phone makes a
   fresh key per pairing and a registered key authenticates as paired before
   pairing logic runs. Harmless, but the "re-pairs <id>" text will rarely show.
7. **Hardware-only behaviour** (camera scan, Secure Enclave identity through
   the loopback bridge, Wi-Fi ↔ cellular migration timing, DiffView scroll
   budget on an iPhone 12-class device) stays unverified (plan §10, 04 §14).
