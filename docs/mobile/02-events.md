# 02 — Server push: events, watch, attachImage

Status: implemented on `feature/mobile-rethink` (schedule: `00-build-plan.md`); this file stays the standing contract. Written as: spec for workflow 2 (daemon + hub) of `docs/mobile-rethink.md` §4,
revised after review (lock order, snapshot model, revocation, limits, attach
hardening, cross-spec contracts).
Scope: protocol v2, the `event` frame, `watch`/`unwatch`, the seven topics,
the daemon's change-notification hub, coalescing and limits, `attachImage`
and the `attachmentImage` read, and the shared golden vectors. Pairing, tiers,
the allowlist and the request rebuild are spec 01; the relay is spec 03; the
client bridge that consumes events is spec 04. This spec names every place it
depends on them (§16).

Everything below was checked against the code on `feature/mobile-rethink`
(2026-11-14).

---

## 1. What exists today (the facts this design rests on)

| Fact | Where |
|------|-------|
| Remote envelope is `protocol.Frame{V, Type, ID, Cmd, Pane, Seq, Payload}`, length-prefixed JSON, `MaxFrameBytes = 1 MiB`, `FrameVersionMin = FrameVersionCurrent = 1`. `Seq` is already a `uint64` "per-stream ordering" field (used by panes). `ID` has no length bound. `FrameErr` carries `ErrPayload{Code, Message, MinV, MaxV}`; `CodeFrameTooLarge`, `CodeRateLimited`, `CodeDenied` exist. | `internal/protocol/frame.go`, `framecodec.go` |
| `FrameWriter` is mutex-guarded and writes one frame per `Write`; several goroutines share it. Writes carry a 15 s deadline (`writeTimeout`); a failed write shuts the connection (`conn.failWrite`). | `internal/protocol/framecodec.go`, `internal/remote/conn.go` |
| The handshake reads frames through `conn.readValidated`, which refuses an unsupported envelope version and closes. | `internal/remote/conn.go:141-192` |
| Requests run on up to `reqConcurrency = 4` goroutines per connection; at most `maxConnections = 8` connections. Every `req` reaches the daemon through `Options.Handle(ctx, req)`, which has no connection identity. `remote.Peer` exists (`auth.go`). `Authorizer.AuthorizeFrame` is consulted for every INBOUND frame only. | `internal/remote/server.go`, `conn.go`, `auth.go` |
| The daemon log is append-only, unrotated, mirrored to stderr; a remote peer must not be able to drive writes into it. | `internal/remote/conn.go:288` |
| `handleRemote` strips `SessionInfo.SSH` from `sessions` and otherwise calls `d.handle`. | `internal/daemon/remotewire.go:533` |
| The session store has ONE commit callback, `OnTransition`, fired only when `Status` changes, under the store lock. `Upsert`, `Delete`, `PruneOlderThan` fire nothing. `Store.Update` returns `(sess, true)` whenever the id exists, also when `fn` returned false and nothing was stored. | `internal/session/session.go:865-1180`, `:1036-1054` |
| `sessionsData()` is pure memory except `sessionUsage` (ledger read). Clock-derived fields: `SessionInfo.Age`, `SessionInfo.HeadlineAgo`, `BoardInfo.UpdatedAgo` (`formatAgo(now.Sub(b.UpdatedAt))`), `Event.Ago`. `SessionInfo.PRObservedAt` is set to `now` on EVERY successful gh fetch (≈30 s). | `internal/daemon/server.go:777`, `report.go:133`, `observer.go:440`, `internal/protocol/protocol.go:497,:631` |
| `handleTalkState` = feed advance (≤ 2 MiB via `feedAdvance`, which `TryLock`s the reader) + `talkPaneState` (capture-pane + cursor = 2 tmux execs for claude) + `attention.Classify` + `readPrompt` + side effects: `reconcileFromPane` (moves the agent axis through `agentReconcile`, may set `InputReason`, commits through `sessions.Update` and calls `sessions.Save`) and `setTalkPrompt`. `talkPaneState` answers `ComposerUnknown` for agentless and goal-managed sessions and on a pane read error, `ComposerGone` when `!sshAgentAlive`. Pull-only. | `internal/daemon/talkstate.go:30-120`, `talksay.go:750-789` |
| The agent feed is a change-log: every item change moves `Feed.seq` and the item's `Seq`; `Feed.Page{After}` (exported) routes to the unexported `Feed.since`; `Page{After:0, Before:0}` is the NEWEST page, bounded by `maxReplyBytes` (768 KiB) but always holding at least one item. A new reader gets a new `Epoch`. It advances only when someone calls `feedEntry.advance` (`handleAgentFeed`, which holds `feedEntry.mu` across a wall-clock scan of up to `feedWallClock` = 1 s, or `advanceFeed`). `feedHub` keeps at most `feedSessions = 8` readers in LRU and REPLACES an entry when `kind` or `slug` changes. Its header comment: "pull-driven … Nothing runs for a session no client is looking at" and "display only". | `internal/agentfeed/store.go:155-215`, `internal/daemon/talkfeed.go` |
| lola's story for a session lives in `sshstore.Store` (`State.Seq`, items with `Order`/`Seq`; retention caps `MaxBlocks`, `MaxEvents`, `MaxHuman`). Every store is opened through `d.sshStore` (one seam). Writers: the timeline worker, ssh handlers, talk sends, `Volatile` (once-a-second block progress, which calls `st.Touch`). `Store.Timeline(conn, after, limit)` answers items with `Seq > after` in Seq order, OLDEST change first, bounded by count only. | `internal/sshstore/sshstore.go:248, :804-846`, `internal/daemon/ssh.go:219`, `sshrun.go:313` |
| Desktop polls `talkState` every 1 s per mounted view (`TALK_POLL_MS`), feed every 1 s while scanning (`FEED_SCAN_POLL_MS`). Phone polls `sessions` every 2 s. | `desktop/frontend/src/lib/talk.svelte.ts:24`, `feed.svelte.ts:16`, `mobile/src/wailsshim/bridge.ts:50` |
| The desktop attachment token is the ABSOLUTE host path `<worktree>/.lola/images/paste-<16 hex><ext>`, returned as `ImageAttachment{path, thumb}`. Caps: 20 MiB, png/jpeg/gif/webp, worktree must be under `config.Home()`, `.lola` and `images` must not be links, file `O_EXCL|O_NOFOLLOW` 0600. The read roots at the images folder itself (`EvalSymlinks(dir) == dir`, then `os.OpenRoot(dir)`, `root.Open(filepath.Base(path))`); a linked folder answers "this session's image folder is unavailable". | `desktop/talkimage.go:95-125`, `desktop/frontend/src/lib/composerpaste.ts:153` |
| A sandboxed worktree is bind-mounted at the SAME absolute path in the container, so a host path token is valid inside the sandbox. | `internal/runtime/sandbox.go:523` |
| An ended record (`sshstore.EndedInfo`) has `KeptWorktree` = the session's directory if it still existed, for every kind, including a borrowed `KindLocal` directory; it carries no kind. | `internal/daemon/talkstate.go:242-246` |
| Golden frame vectors: `mobile/src/wire/testdata/frames.json`, read by `internal/protocol/goldenvectors_test.go` and `mobile/src/wire/codec.test.ts`. Go is the source of truth. `mobile/src/wire/codec.ts` hand-writes the envelope in Go field order (`encodeFrameJSON`) and decodes a whitelist (`decodeFrameBody`: id/cmd/pane/seq). | `mobile/src/wire/codec.ts:293-319, :384-410` |

---

## 2. Protocol version 2

```go
// internal/protocol/frame.go
const (
	FrameVersionCurrent = 2
	FrameVersionMin     = 2
)
```

- The daemon REQUIRES v2 on EVERY frame, the handshake included: the pairing
  frame `remote.pair` is `"v":2` (spec 01 §6 must say `"v":2`, not `<1|2>`;
  `conn.authenticate` reads it through `readValidated`, which would refuse a
  v1 frame before pairing runs). v1 clients authenticated with the bearer key,
  which spec 01 deletes, so no v1 peer can exist. A v1 frame gets
  `UnsupportedVersionFrame` (`minV: 2, maxV: 2`) and a close — the existing
  path in `conn.readValidated`, unchanged.
- `ErrPayload`'s layout stays frozen (the comment in `frame.go` holds).
- What v2 changes in the ENVELOPE: one new field `Topic` and one new type
  `event`. Nothing else in v1's envelope changes.

```go
type Frame struct {
	V       int             `json:"v"`
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Cmd     string          `json:"cmd,omitempty"`
	Pane    string          `json:"pane,omitempty"`
	Topic   string          `json:"topic,omitempty"` // NEW: event frames, and an err frame that ends a stream
	Seq     uint64          `json:"seq,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

const FrameEvent = "event" // daemon -> client: Payload is the topic's event type

// FrameDirections gains: case FrameEvent: return DirToClient
```

`Topic` sits after `Pane` so the encoded byte order of every existing frame is
unchanged apart from `"v":2`. The TS codec mirrors it (`encodeFrameJSON`
writes `topic` between `pane` and `seq`, omitted when empty;
`decodeFrameBody` accepts `topic` only as a string, else
`FrameMalformedError("topic is not a string")`).

---

## 3. The `event` frame

```json
{"v":2,"type":"event","id":"w7","topic":"talk:lola-fe-42","seq":3,"payload":{…}}
```

| Field | Meaning |
|-------|---------|
| `id` | The `id` of the `watch` request that started THIS stream of the topic (the watch id). A client drops every event whose `id` is not the current watch id it holds for that topic, so re-watch is safe without ordering between the watch response and in-flight events (§6.3). |
| `topic` | Canonical topic string (§4). |
| `seq` | Per (connection, topic, watch id). The first event of a stream is `1`; each further event on that stream is exactly `+1`. Never 0. |
| `payload` | The topic's event type (§5). |

**Watch id.** A `watch` request's frame `id` must match `^[A-Za-z0-9_-]{1,64}$`
(`MaxWatchIDBytes = 64`); otherwise the request is `ok:false`,
`error: "bad watch request"`, and changes nothing. This bounds the envelope:
`{"v":2,"type":"event","id":<≤64>,"topic":<≤160>,"seq":<≤20 digits>,"payload":…}`
is under `EnvelopeMaxBytes = 1 KiB`, and `EventMaxBytes + EnvelopeMaxBytes +
4 ≤ MaxFrameBytes` holds by construction (`TestEventBudgetFitsFrame`).

**Ending a stream.** The daemon may end one stream without touching the rest
of the connection. It sends one `err` frame with `id` = the watch id,
`topic` = the topic, and `ErrPayload{Code, Message}`:

| Code | Message (fixed) | When | Client (§6.2) |
|------|-----------------|------|---------------|
| `frame_too_large` | `"this view is too large to stream"` | a value that cannot be split or omitted is over `EventMaxBytes` (§8.4) | `failed`; not retried on this connection |
| `rate_limited` | `"this view changes faster than this link"` | `ResnapshotLimit` overflows within `ResnapshotWindow` (§8.3) | `failed`; retry after `WatchRetryAfter` |
| `denied` | `"not permitted for this device"` | the device no longer holds `read` (§10.3) | `failed`; not retried |

After that frame the daemon sends nothing more for that watch id and has
already dropped the stream's state.

A daemon never sends an `event` the client did not watch. A client that
receives one for an unknown topic or a stale `id` drops it silently.

---

## 4. Topics

Grammar (canonical form, checked by `protocol.ParseTopic`):

```
topic   = "sessions" | "projects" | "status"
        | kind ":" session
kind    = "talk" | "feed" | "timeline" | "diff"
session = 1..128 bytes, sshstore.ValidSession(session) == true,
          session.BaseID(session) == session      // no lane ids "<id>~<app>"
```

```go
// internal/protocol/events.go
const (
	TopicSessions = "sessions"
	TopicProjects = "projects"
	TopicStatus   = "status"
	TopicTalk     = "talk"
	TopicFeed     = "feed"
	TopicTimeline = "timeline"
	TopicDiff     = "diff"
	MaxTopicBytes = 160
)

// ParseTopic splits a topic into its kind and session ("" for global topics).
// ok is false for anything not in the grammar; it never normalises.
func ParseTopic(t string) (kind, session string, ok bool)
```

| Topic | Event type | Kind of stream | Becomes stale when |
|-------|-----------|----------------|--------------------|
| `sessions` | `SessionsEvent` | snapshot, then keyed deltas | any store commit, config reload, usage ledger, activity ring |
| `projects` | `ProjectsEvent` (= `ProjectsData`) | replace | reload, enable/disable, poll status, runtime health |
| `status` | `StatusEvent` (= `StatusData`) | replace | poll status, Linear auth, usage, runtime health |
| `talk:<id>` | `TalkEvent` (= `TalkStateData`) | replace | the pane (no signal), hooks, sends, queue, prompt |
| `feed:<id>` | `FeedEvent` | snapshot, then keyed deltas | transcript bytes |
| `timeline:<id>` | `TimelineEvent` | snapshot, then keyed deltas | `sshstore` commits |
| `diff:<id>` | `DiffEvent` | replace (a counter) | edits, turns, checkpoints, restores, head |

A watch on a session topic needs the session to be live (`d.sessions.Get`) or
ended-and-readable (`sshstore.ReadEnded != nil`); otherwise the result is
`unknown_session`. `talk` and `diff` refuse an ended session with
`unknown_session` (no pane, no worktree to watch); `feed` and `timeline`
accept it (both stay readable 14 days).

---

## 5. Payloads (exact wire shapes)

### 5.1 Requests and the watch reply

```go
const (
	CmdWatch           = "watch"           // WatchArgs   -> WatchData
	CmdUnwatch         = "unwatch"         // UnwatchArgs -> WatchData
	CmdAttachImage     = "attachImage"     // AttachImageArgs     -> AttachImageData
	CmdAttachmentImage = "attachmentImage" // AttachmentImageArgs -> AttachmentImageData
)

type WatchArgs struct {
	Topics []string `json:"topics"` // 1..MaxWatchTopicsPerRequest, canonical, no duplicates
}

type UnwatchArgs struct {
	Topics []string `json:"topics,omitempty"`
	All    bool     `json:"all,omitempty"` // every topic on this connection
}

type WatchData struct {
	Topics []WatchResult `json:"topics"` // one per requested topic, in request order; [] for unwatch{all}
}

type WatchResult struct {
	Topic   string `json:"topic"`
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`    // WatchCode*, set when !OK
	Message string `json:"message,omitempty"` // a short fixed sentence, never built from input
}

// Watch refusal codes. Closed list; a client treats an unknown code as failed.
const (
	WatchCodeInvalidTopic   = "invalid_topic"   // not in the grammar
	WatchCodeUnknownSession = "unknown_session" // §4
	WatchCodeLimit          = "limit"           // a §9 limit; retry after an unwatch
	WatchCodeUnavailable    = "unavailable"     // this daemon has no event hub (Options.Events nil)
)
```

Args travel in `Request.Args` like every other typed command:
`{"cmd":"watch","args":{"topics":["sessions","talk:lola-fe-42"]}}`.

The `resp` is `ok:true` whenever the request decoded, even if every topic was
refused; per-topic outcomes are in `WatchData`. A malformed request (`args`
missing, zero or more than `MaxWatchTopicsPerRequest` topics, a duplicate, a
watch id outside §3's pattern) is `ok:false` with `error: "bad watch request"`
and changes nothing. `unwatch` of a topic not watched is `ok:true` for it.

### 5.2 `sessions`

```go
type SessionsEvent struct {
	Reset   bool          `json:"reset,omitempty"`   // discard everything held; this (with its parts) is the full set
	More    bool          `json:"more,omitempty"`    // this reset continues in the next event(s)
	Upsert  []SessionInfo `json:"upsert,omitempty"`  // whole rows, keyed by id
	Remove  []string      `json:"remove,omitempty"`  // ids gone from the store
	Omitted []string      `json:"omitted,omitempty"` // ids whose row exceeds EventItemMaxBytes (§8.4); fetch with `sessions`
	Events  []Event       `json:"events,omitempty"`  // the activity ring, WHOLE; on reset (first part) and when it changed
}
```

- Rows are the REMOTE projection: `remoteSessionInfo(si)` = `si` with
  `SSH = nil` (`internal/daemon/watchsessions.go`). `handleRemote`'s
  `sessions` strip and the projector call the same function.
- A reset larger than `EventMaxBytes` is split into parts: the first has
  `reset:true, more:true` (and `events`), the following carry only `upsert`
  and `more:true`, the last `more:false`. A client may render progressively;
  it treats the set as complete after `more:false`.
- **Fields outside the change fingerprint.** One list, `sessionsVolatile`
  in `watchsessions.go`, zeroed on a COPY before hashing; sent with their
  current value:

  | Field | Why excluded |
  |-------|--------------|
  | `SessionInfo.Age` | clock-derived from `FirstSeen` |
  | `SessionInfo.HeadlineAgo` | clock-derived from `HeadlineAt` |
  | `SessionInfo.Board.UpdatedAgo` | clock-derived from `Board.UpdatedAt` |
  | `SessionInfo.PRObservedAt` | moves on every successful gh fetch; `PRStale` (kept) is the display fact |
  | `Event.Ago` (activity ring) | clock-derived from `Event.At` |

  Clients render relative time from `firstSeen`, `headlineAt`,
  `board.updatedAt` and `Event.at` with one shared clock (plan §8). The
  `.age` readers in `mobile/src/lib/components` move to `firstSeen` (spec 04).
  A new clock-derived wire field must be added to this list
  (`TestSessionsFingerprintIgnoresClock` builds two projections an hour apart
  over the same records and requires equal fingerprints).

### 5.3 `projects`, `status`

Payload is `ProjectsData` / `StatusData` verbatim, each event a full
replacement. Their fingerprints exclude nothing; `Usage` moves with tokens,
which is why status has a tick (§7.4).

### 5.4 `talk:<id>`

Payload is `TalkStateData` verbatim, each event a full replacement.

- The fingerprint EXCLUDES `feedSeq` (sent with its current value). A push
  client learns about feed movement from `feed:<id>`. `feedEpoch` and
  `health` are included.
- A session that disappears without an ended record yields
  `{"session":id,"composer":"gone","health":"unsupported","caps":{…zero},"feedSeq":0}`
  once, then nothing.

### 5.5 `feed:<id>`

```go
type FeedEvent struct {
	Reset    bool            `json:"reset,omitempty"`    // discard held items; Items is the newest page
	Older    bool            `json:"older,omitempty"`    // reset only: older history exists; page it with agentFeed{before}
	Session  string          `json:"session"`
	Epoch    string          `json:"epoch"`
	LastSeq  int64           `json:"lastSeq"`
	Scanning bool            `json:"scanning,omitempty"`
	Health   string          `json:"health"`
	Items    []AgentFeedItem `json:"items"`              // never null
	Omitted  []OmittedItem   `json:"omitted,omitempty"`  // items over EventItemMaxBytes (§8.4)
}

type OmittedItem struct {
	ID  string `json:"id"`
	Seq int64  `json:"seq"`
}
```

- **Reset:** `e.feed.Page(ctx, AgentFeedArgs{Session:id})` (After 0, Before 0,
  `FeedSnapshotItems`) exactly as `handleAgentFeed` serves a first page, with
  the health `judge` gives. `markHistory` applies only to `before` pages, so a
  pushed reset and a pulled first page are the same bytes. Bounded by
  `agentfeed.maxReplyBytes` (768 KiB), never split.
- **Delta:** `e.feed.Page(ctx, AgentFeedArgs{Session:id, Epoch:epoch,
  After:lastPublished, Limit:FeedDeltaItems})`, which routes to the unexported
  `since`; looped while its `LastSeq` is below `e.feed.LastSeq()`, one event
  per page. `lastPublished == 0` is never sent as `After` (a zero cursor means
  "newest page" to `Page`); with `lastPublished == 0` the publisher sends a
  reset. An item can appear in several deltas (tool `running` → `ok`); the
  client upserts by `id`, keeping the higher `seq`. Deltas are idempotent: an
  item already in the client's snapshot may arrive again.
- **Epoch change** (new reader, eviction, agent switch, segment rotation) is
  ALWAYS published as a reset.
- **Scanning.** While `e.feed.Scanning()` the newest page is not the tail. The
  publisher sends ONE reset when it first sees scanning (so the view shows
  what exists), then only progress events
  `{reset:false, scanning:true, epoch, lastSeq, health, items:[]}` at most
  once per `feedScanProgressInterval` while `LastSeq` moves, and ONE reset
  when scanning ends. Bytes during a scan are therefore two resets (≤ 2 ×
  768 KiB) plus ≈ 150 B/s, whatever the transcript size
  (`TestFeedScanBytesBounded`).
- An agent without a feed (`!agent.Caps(k).Feed`) gets one reset with
  `health:"unsupported"` and empty items, then nothing.

### 5.6 `timeline:<id>`

```go
type TimelineEvent struct {
	Reset       bool              `json:"reset,omitempty"`
	More        bool              `json:"more,omitempty"`        // a reset continues
	Older       bool              `json:"older,omitempty"`       // reset only: the snapshot is a newest window (below)
	Session     string            `json:"session"`
	LastSeq     int64             `json:"lastSeq"`
	Items       []SSHTimelineItem `json:"items"`                 // upsert by id, never null
	Connections []SSHConnInfo     `json:"connections,omitempty"` // WHOLE; on the first reset part and when changed
	Omitted     []OmittedItem     `json:"omitted,omitempty"`     // items over EventItemMaxBytes
}
```

**Source.** Built ONLY from spec 01's redacted reader:

```go
// spec 01 §8.6, internal/daemon/sessiontimeline.go
func (d *Daemon) handleSessionTimeline(a protocol.SessionTimelineArgs) (protocol.SSHTimelineData, error)
// = publicTimeline(handleSSHTimeline(...)): private Run blocks dropped, Host/User/Cwd,
//   TermElevated, Prompt, handback Transcript removed.
```

The producer calls
`d.handleSessionTimeline(protocol.SessionTimelineArgs{Session: id, After: after, Limit: n})`
and reads `protocol.SSHTimelineData{Session, Connections, Items, LastSeq, More}`.
There is no other timeline type. `watchtopics.go` must not import or call
`handleSSHTimeline`, `sshstore.Store.Timeline` or `d.sshStore` for content
(`TestTimelineProducerUsesOnlyPublicTimeline` checks the file's call graph
with `go/parser`; only `SetNotify` wiring in `ssh.go` touches the store).

- **Snapshot:** loop `handleSessionTimeline{After: cursor, Limit:
  TimelineSnapshotItems}` from cursor 0 while `More`, collecting items. If
  the encoded total is ≤ `TimelineSnapshotMaxBytes`, send them all (split
  into parts by encoded size, `more:true` on all but the last). Otherwise
  keep the items with the highest `Order` that fit, set `older:true` on the
  first part, and let the client complete history on demand by pulling
  `sessionTimeline` pages from `After:0` up to the event's `lastSeq`
  (upserting by id). `lastSeq` is the last page's `LastSeq`.
- **Delta:** `handleSessionTimeline{After: lastPublished, Limit:
  TimelineDeltaItems}` looped while `More`, one event per page;
  `connections` included when their fingerprint moved.
- A block that turns shareable (`sshShareBlock` → `Touch`) appears as an
  upsert; sharing is one-way.

### 5.7 `diff:<id>`

```go
type DiffEvent struct {
	Rev   int64  `json:"rev"`             // per session, daemon memory, monotonic; starts at 1
	Cause string `json:"cause,omitempty"` // DiffCause*: display/debug only
}

const (
	DiffCauseEdit       = "edit"       // a feed item of category edit or run (a CLAIM; display-only)
	DiffCauseCheckpoint = "checkpoint" // recordCheckpointAt committed
	DiffCauseRestore    = "restore"    // handleRestoreCheckpoint moved files
	DiffCauseHead       = "head"       // the session's branch or PR number changed
)
```

A "maybe changed" tick, signal-only: the daemon runs NO git to produce it.
The topic's value exists from the moment it is first watched:
`{rev: <current or 1>, cause: ""}`, so the first event (`seq:1`) is sent at
watch time and the client learns `rev` immediately. A false positive costs
one lazy `diff` the client makes only while the Changes view is on screen; a
false negative is bounded by the next turn's checkpoint.

---

## 6. Stream semantics

### 6.1 Server state per (connection, topic) — in the connection's `eventbox.Box`

```
              Start                     snapshot installed
  none ────────────────▶ awaiting_snapshot ────────────────────▶ live
   ▲                          ▲   │                                │
   │ Stop / End / Close       │   │ Install refused (budget):      │ overflow (§8.3)
   │ (any state)              │   └── stays, re-requested later    ▼
   │                          └─────────────────────────────── needs_snapshot
```

- `awaiting_snapshot` and `needs_snapshot` hold no deltas: `Offer` to such a
  slot is dropped (the coming snapshot supersedes it); `Next` skips it.
- The snapshot is built and installed ONLY by a producer run on a hub `sem`
  slot (§7.1). The connection reader (`Events.Watch`) and the pump
  (`Box.Next`) never build one.
- **Re-watch** (`watch` of a topic already watched on this connection): conn
  calls the OLD stream's `stop()` FIRST, then `Events.Watch` with the new
  watch id. `stop` removes the hub registration only if its stored watch id
  equals the one it was created for, and `Box.Stop(topic, watchID)` drops the
  slot only if the slot's watch id matches; so a late old `stop` can never
  remove the new stream. New stream: `seq` from 1, a fresh snapshot.
- Rapid re-watch costs no repeated work: snapshot builds per topic are
  coalesced (one in flight, all waiting slots served by it) and cached per
  producer version (§7.1 rule 7). `watch` is also a read-tier request
  bounded by spec 01's `readRate`.

### 6.2 Client state per topic (spec 04 implements it in `mobile/src/wire/events.ts`)

```
 idle ──watch sent──▶ awaiting ──event(id==watchId, seq==1)──▶ live
  ▲                     │  ▲                                      │
  │                     │  └──────── seq != last+1 (gap) ─────────┤ (send watch again)
  │ resp ok:false /     │                                         │
  │ err(id==watchId)    ▼                                         │
  │                  failed(code) ◀──── err(id==watchId) ─────────┤
  └──────────────────────────────────────── disconnect ───────────┘
```

- `awaiting`: drop events whose `id` is not the new watch id. The first
  accepted event must have `seq == 1`; for delta topics it also has
  `reset:true` (a client asserts this and re-watches if not).
- `live`: `seq == last+1` → apply. `reset:true` → replace held state (a
  `more:true` reset is assembled until `more:false`). Anything else is a gap
  → `watch` that topic again. A gap cannot happen on an intact connection;
  the rule exists so a client bug or a future transport cannot silently
  desynchronise.
- `failed(limit)`: retry only after the client itself unwatched something.
  `failed(unknown_session)`, `failed(frame_too_large)`, `failed(denied)`:
  never retried on this connection. `failed(rate_limited)`: retry after
  `WatchRetryAfter`.
- Reconnect (any path, spec 03/04): every desired topic goes back to `idle`
  and is re-watched in ONE `watch` request.
- Background (Capacitor `pause`): the client sends `unwatch{all:true}` or
  closes the connection. "Screen off costs nothing" (plan §4) is a client
  obligation the daemon backs with "no watcher, no work" (§7).

### 6.3 Why events carry the watch id

The `watch` resp and events are written by different goroutines through one
mutex-guarded `FrameWriter`. An event of the old stream may be on the wire
after the resp of a re-watch. Tagging each event with the watch id lets the
client tell streams apart without serialising responses with events.

---

## 7. Where the daemon learns that something changed

### 7.1 The hub, the snapshot model and the lock order

One `watchHub` per `Daemon` (`d.watch`, `internal/daemon/watchhub.go`). It owns
one `topicState` per WATCHED topic and nothing for unwatched topics.

```go
type watchHub struct {
	d *Daemon

	mu     sync.Mutex             // LEAF: guards topics, probed; never held while taking another lock
	topics map[string]*topicState // only watched topics
	probed map[string]map[string]int // session id -> device key -> watch refcount (talk/feed), §9
	active atomic.Int32           // len(topics); 0 makes every poke a no-op

	pokeMu sync.Mutex             // LEAF: taken under store locks and Box.mu, takes nothing
	pokes  map[string]pokeBits    // session id -> bits
	diffs  map[string]string      // session id -> latest diff cause
	snaps  map[string]bool        // topics with a slot waiting for a snapshot
	global pokeBits               // pokeSessionsAll | pokeStatus | pokeProjects
	wake   chan struct{}          // cap 1, non-blocking send

	probes *prober                // §7.3
	sem    chan struct{}          // producer concurrency, watchProducerConcurrency
}

type pokeBits uint8

const (
	pokeRow         pokeBits = 1 << iota // the session's SessionInfo may have changed
	pokeTalk                             // probe talk:<id> soon
	pokeFeed                             // feed:<id> moved (a reader advanced)
	pokeTimeline                         // timeline:<id> moved
	pokeDiff                             // diff:<id> maybe changed (cause in diffs)
	pokeSessionsAll                      // every row (config reload)
	pokeStatus
	pokeProjects
)

type topicState struct {
	mu    sync.Mutex
	topic string
	dead  bool                       // set when the last sub left; Watch retries the map lookup
	subs  map[*eventbox.Box]subInfo  // box -> {watchID, deviceKey}
	last  eventbox.Event             // replace topics: last published value; nil before the first
	// delta topics keep their cursor and snapshot cache in the producer
}

// Poke API, all non-blocking, all safe under any lock in the daemon:
func (h *watchHub) PokeSession(id string, bits pokeBits)
func (h *watchHub) PokeDiff(id, cause string)
func (h *watchHub) PokeGlobal(bits pokeBits)
func (h *watchHub) Resnapshot(topic string) // the Box's onSnap callback (§8)
```

**Lock order (total; every lock the push path touches is named):**

| Level | Locks | May take while held |
|-------|-------|---------------------|
| L1 producer | `sessionsProjector.mu`; `feedPub.mu` (per session); `timelinePub.mu` (per session) | L2 (taken and released inside compute), L3, L4, L5 |
| L2 data | `session.Store.mu`, `feedHub.mu`, `feedEntry.mu`, `sshstore.Store.mu`, `d`'s own locks taken by `sessionsData`/`statusData`/`projectsData`/`handleTalkState` | L5 only (pokes), plus the existing L2→L2 edges of today's code; the push path adds none |
| L3 | `topicState.mu` | L4, L5 |
| L4 | `eventbox.Box.mu` | L5 |
| L5 leaves | `watchHub.pokeMu`, `watchHub.mu`, `prober.mu`, `attachRegistry.mu` | nothing |

Rules that follow from it:

1. A poke with `active == 0` returns after one atomic load. Idle cost is zero.
2. A poke takes only `pokeMu`, records bits, and does a non-blocking send on
   `wake`. It never calls the store, a producer or a `Box`. That makes it safe
   inside `Store.Update` (store lock held), inside `sshstore` writers, inside
   `feedEntry.advance` (`feedEntry.mu` held) and inside `Box` (`Box.mu` held).
3. The hub loop (`h.run(ctx)`, on `d.wg`, panic-guarded like `safeObserve`)
   wakes on `wake` or on `watchTick` (a ticker that runs only while
   `active > 0`), swaps the poke sets out under `pokeMu`, and schedules
   producer runs honouring each topic's debounce and min interval. The loop
   itself takes no lock above L5 and does no I/O.
4. Producer runs execute on `sem` slots, never on the loop, the connection
   reader or the pump; each under `context.WithoutCancel(ctx)` + its own
   timeout, panic-guarded. At most one run per topic is in flight.
5. **Compute, then install.** A run computes with no lock above L1 held
   (delta topics hold their L1 lock; replace topics hold none), then takes
   `topicState.mu` and, in one critical section, (a) offers the new delta or
   value to every `live` slot and (b) installs the snapshot into every slot
   that wants one (`Box.WantsSnapshot`). Nothing is computed, read from a
   store, or executed while `topicState.mu` is held. So for every subscriber:
   snapshot first, then exactly the deltas published after it.
6. `Events.Watch` (connection reader goroutine) takes `watchHub.mu` (L5) to
   find or create the `topicState` and releases it, then takes
   `topicState.mu` → `Box.Start(topic, watchID)` (L3 → L4) and marks the
   topic in `snaps` (L5). It builds nothing and performs no I/O. If it finds
   `dead` set it repeats the lookup. Stop: under `topicState.mu`, remove the
   sub if its watch id matches; if `subs` is now empty set `dead`, release,
   then delete the map entry under `watchHub.mu` if it still points at this
   state.
7. **Snapshot runs.** For a topic in `snaps`, the loop schedules one run
   (coalesced, at most one per `snapshotMinInterval` per topic):
   - replace topic with `last != nil`: install `[last]` under
     `topicState.mu` (no producer, no compute);
   - replace topic with `last == nil`: a normal producer run (a probe for
     `talk`, `statusData`/`projectsData` for global topics) scheduled
     immediately; its result is installed as seq 1. Bounded by
     `talkProbeTimeout` / `statusRunTimeout`;
   - delta topic: the producer run first publishes any pending delta to
     `live` slots, then installs a snapshot taken at the producer's NEW
     cursor. The snapshot is cached keyed by the producer's version
     (sessions: projector generation; feed: `(epoch, lastPublished,
     scanning)`; timeline: `lastPublished`); a cache hit is a copy under the
     L1 lock, no recompute.
8. The box's `onSnap` callback (overflow, refused install) is
   `h.Resnapshot(topic)`: L4 → L5, a poke.

`TestNoLockInversion` (`internal/daemon/watch_lock_test.go`, run under
`-race`, 10 s timeout): 8 goroutines re-watch `sessions` and `feed:<id>` in a
loop on 4 boxes while 4 goroutines drive commits (store `Update`,
`feedEntry.advance` via a fake source) and a desktop-style `handleAgentFeed`
holds `feedEntry.mu` across a scan; every box must receive a reset within 2 s
of each watch, and the test fails on timeout (a deadlock) or a race report.

### 7.2 `sessions` — store commit hook + projector

**Signal.** A new callback on `session.Store`:

```go
// internal/session/session.go

// Change is one committed change. Prev is nil on insert, Next nil on delete.
// Both are copies owned by the callback for its duration only.
type Change struct {
	ID         string // the BASE id; a lane write reports its session
	Prev, Next *Session
}

// OnChange registers fn, invoked under the store lock after a change was
// COMMITTED: Upsert (always); Update / Apply only when fn returned true and
// the record was stored (Update's (sess, true) return also covers a no-op and
// must not be used as the signal); Delete when it removed a record; each id
// PruneOlderThan drops. Lane writes go through updateLane and are covered.
// fn must not call back into the store, must not block and must not retain
// Prev/Next.
func (s *Store) OnChange(fn func(Change))
```

`OnTransition` stays as is. The daemon registers in `newDaemon`, next to the
`OnTransition` registration:

```go
d.sessions.OnChange(func(c session.Change) {
	bits := pokeRow
	if talkFieldsChanged(c.Prev, c.Next) { bits |= pokeTalk }
	d.watch.PokeSession(c.ID, bits)
	if headChanged(c.Prev, c.Next) { d.watch.PokeDiff(c.ID, protocol.DiffCauseHead) }
})
```

- `talkFieldsChanged`: insert or delete, or any of `AgentState`,
  `InputReason`, `AtPrompt`, `QueueHeld`, `AutomationDeferredReason`,
  `Agent` differ. `LastSeen`, PR facts, usage and the observer's routine
  refresh never poke talk.
- `headChanged`: `Branch` or `PR.Number` differ (both sides non-nil).

Every existing writer — `handleHookEvent` (including hooks relayed by the
sandbox bridge through `hookbridge.go` → `d.handle`), the observer's native
pass, reactions, answer, kill, revive, best-of, talk queue fields — is covered
without touching it.

Other inputs:

| Input | Poke site |
|-------|-----------|
| config reload (`devCommands`, `devParallel`, display names) | `handleReload` and `handleEnable` → `PokeGlobal(pokeSessionsAll\|pokeProjects\|pokeStatus)` |
| usage ledger, port leases, `displayOverlay`'s time window | `sessionsTick` |
| activity ring (`recordSessionEvent`) | fires from `OnTransition`/spawn, which are store commits → covered |

**Projector** (`internal/daemon/watchsessions.go`):

```go
type sessionsProjector struct {
	mu        sync.Mutex           // L1
	gen       uint64               // bumped on every publish that sent something
	published map[string]rowState  // id -> last sent row
	events    uint64               // fingerprint of the activity ring as last sent
	lastRun   time.Time
	snap      []protocol.TopicEvent // cached reset parts for gen
	snapGen   uint64
}
type rowState struct {
	fp   uint64               // FNV-1a 64 of json.Marshal(copy with sessionsVolatile zeroed)
	size int                  // encoded size of info
	info protocol.SessionInfo
}
```

`run()` (under `mu`): `data := d.sessionsData()`; per row
`si = remoteSessionInfo(si)`, fingerprint per §5.2; `upsert` for new or
changed `fp`, `remove` for ids no longer present; fingerprint `data.Events`
with every `Ago = ""` and include `events` when it moved. Nothing changed →
nothing sent, `gen` unchanged. Then (rule 5) take `topicState.mu`: offer the
delta to live slots, install `snapshot()` into waiting slots, where
`snapshot()` builds reset parts from `published` (cached by `gen`).

Schedule: after `sessionsDebounce` from the first poke, at most once per
`sessionsMinInterval`, and every `sessionsTick` while watched. A snapshot run
inside `sessionsMinInterval` of the last run installs from `published`
without recomputing (rule 7).

**Cost per run:** one `sessionsData()` (pure memory plus `sessionUsage` per
row) and N row marshals + hashes. At N = 30, well under 2 ms of CPU, zero
execs. An observer pass that only refreshes PR facts changes no fingerprint
and sends nothing.

### 7.3 `talk:<id>` and `feed:<id>` — the prober (the bounded tick)

The pane has no change signal, so a session watched for `talk` is probed on a
schedule; `feed` piggybacks on the same probe.

```go
// internal/daemon/watchprobe.go
type prober struct {
	mu   sync.Mutex // L5
	runs map[string]*probeRun // only sessions with a talk or feed watcher
}

type probeRun struct {
	id        string
	talk      bool      // a talk:<id> watcher exists -> read the pane
	feed      bool      // a feed:<id> watcher exists
	due       time.Time // zero = parked (no tick, poke only)
	inFlight  bool
	lastStart time.Time
	errs      int       // consecutive pane read failures
}
```

**What a probe is.** For `talk`: exactly `d.handleTalkState(ctx,
TalkStateArgs{Session:id})` — the desktop's poll, with ALL its effects:
`feedAdvance` (`TryLock`, never waits on a scan), the pane read, and
`reconcileFromPane`, which moves the agent axis through `agentReconcile` on
pane evidence only (`ActivityWaiting` for a prompt, the live working cue for
working) and commits/saves the session. A watch therefore counts as a viewer,
exactly like an open desktop Conversation. The probe never types, never calls
`typeToAgent`, `sendKeys`, `agentChoose` or any `lock*Prompt`, and moves no
field the pane did not prove. For a feed-only run: `d.advanceFeed(ctx, s,
talkFeedBudget)`.

**Schedule** (`nextDue`), decided from session flags BEFORE the composer word:

| Condition (first match wins) | Next probe |
|------------------------------|------------|
| session not live (ended or gone) | publish §5.4 gone value once; parked |
| `IsAgentless()`, `GoalManaged()`, or `!sshAgentAlive(s)` | parked (poke only) |
| last pane read failed (`errs > 0`; seen as `ComposerUnknown` with an error from the read) | `min(talkProbeActive << errs, talkProbeResting)` |
| composer `working`, `prompt`, `modal`, `draft`, `unknown`; or `AgentState` working / waiting_input | `talkProbeActive` (1 s) |
| `resting` | `talkProbeResting` (15 s) — catches a draft typed in the terminal |
| feed-only run, `Scanning` | `feedScanProbe` (1 s) |
| feed-only run, not scanning | parked (poke only) |

`watchprobe.go` calls `talkPaneState` through `handleTalkState` and learns a
read error through a probe-only seam `d.talkReadErr(id) bool` (set by
`talkPaneState` when `readTalkPane` failed; reset on success; part of work
item 7's `talkstate.go` hunk).

**Pokes.** `pokeTalk` (from `OnChange` only when talk fields changed, §7.2;
from `talkReadAsync` after a hook says a prompt is up) sets
`due = max(now, lastStart + talkProbeMinGap)` with `talkProbeMinGap =
talkProbeActive = 1 s`. So a session is probed at most once per second
whatever the hook rate, and the probe's own commit (`reconcileFromPane` →
`OnChange` → `pokeTalk`) only reasserts the 1 s cadence it already has.
`setTalkPrompt` does not poke (it runs inside the probe, whose result is
published directly). At most one probe per session in flight; at most
`watchProducerConcurrency` producer runs daemon-wide; each bounded by
`talkProbeTimeout`.

After a probe:

- talk: fingerprint (`TalkStateData` JSON with `FeedSeq = 0`); publish to
  `talk:<id>` (rule 5) when changed.
- feed: if `feed:<id>` is watched, run the feed publisher.
- diff: if the publisher saw an item with `Tool.Category ∈ {edit, run}`,
  `PokeDiff(id, edit)`. This reads an agent CLAIM and drives only a
  display-only tick (§14).

**Feed publisher** (`internal/daemon/watchfeed.go`). Per session a `feedPub
{mu sync.Mutex (L1); epoch string; lastPublished int64; scanning bool;
lastProgress time.Time; snap cache}`. Under `feedPub.mu`: resolve the entry
(`feedHub.entry`, L2, released), `e.mu.Lock()` (L2; bounded by
`handleAgentFeed`'s `feedWallClock` scan, and taken on a `sem` slot, never on
the loop), compute per §5.5 (reset / delta pages / scan progress), copy the
result, `e.mu.Unlock()`, then take `topicState.mu` and publish/install
(rule 5). The publisher holds `feedPub.mu`, never `feedEntry.mu`, while it
holds `topicState.mu`.

**Feed signal from other readers.** `feedEntry.advance` is the one choke
point every reader goes through. When it returns changed items it calls
`d.watch.PokeSession(id, pokeFeed)` (L2 → L5), so a desktop pull that
advances the reader also pushes to phones. `pokeFeed` schedules the feed
publisher only, never a probe.

**Reader pinning.** Pins live on the hub, not on the entry, so an entry
replaced on agent switch keeps its pin:

```go
type feedHub struct {
	mu      sync.Mutex
	entries map[string]*feedEntry
	pins    map[string]int // session id -> watch refcount (talk or feed), > 0 = pinned
	clock   int64
}
func (h *feedHub) pin(id string)   // pins[id]++
func (h *feedHub) unpin(id string) // pins[id]--, delete at 0; never below 0
```

`feedHub.entry`'s LRU eviction skips ids with `pins[id] > 0`. Pinned sessions
are capped by `MaxProbedSessions` (= `feedSessions` = 8), so the hub holds at
most `feedSessions + 1` readers.

**Cost per probe (claude):** 2 tmux execs (`capture-pane`, cursor), one
`Classify` over ≤ 50 lines, one transcript `stat` + read of appended bytes
(≤ 2 MiB, normally < 10 KB), one marshal + FNV. Codex/opencode: ≤ 1 exec.

### 7.4 `status`, `projects`

Pokes: `statusTracker` gets `onChange func()` called after `begin`, `end`,
`setError`, `setLastSpawn` (set to `PokeGlobal(pokeStatus|pokeProjects)`);
the four `d.linOK` assignments in `daemon.go` become `d.setLinOK(v)`, which
pokes `pokeStatus` when the value changed; reload/enable poke both. Plus
`statusTick` while watched (`Usage` moves with tokens; `runtimeHealth` has no
signal).

Producer: `d.statusData(ctx)` / `d.projectsData(ctx)` under
`statusRunTimeout`, fingerprint, publish on change, `statusMinInterval`
between runs. Cost: `runtimeHealth` per distinct agent binary, ≤ 1–3 execs
per run, ≤ one run per `statusTick` absent pokes.

### 7.5 `timeline:<id>`

Signal: `sshstore.Store` gains

```go
// internal/sshstore/sshstore.go
// SetNotify sets fn, called AFTER the lock is released whenever Update
// committed (persisted or not) or Volatile's fn returned true. nil clears it.
func (s *Store) SetNotify(fn func())
```

`d.sshStore` calls `s.SetNotify(func(){ d.watch.PokeSession(session, pokeTimeline) })`
right after `sshstore.Open`, so every writer is covered by the one seam.

Producer (`timelinePub{mu (L1), lastPublished, connFP, snap cache}` in
`watchtopics.go`): §5.6, with `timelineDebounce` after the first poke. Cost:
the redaction over an in-memory read under the store's read lock; no disk
read, no exec.

### 7.6 `diff:<id>`

Pokes (`PokeDiff(id, cause)`):

| Site | Cause |
|------|-------|
| feed publisher, item of category edit/run (§7.3) | `edit` |
| end of `recordCheckpointAt` (a turn's snapshot committed) | `checkpoint` |
| end of `handleRestoreCheckpoint` on success | `restore` |
| `OnChange`, `Branch` or `PR.Number` changed (§7.2) | `head` |

Producer: after `diffDebounce` from the first poke, and at least
`diffMinInterval` after the previous event: under `topicState.mu`, `rev++`,
`last = {rev, cause}`, offer. Cost: a counter. The first watch creates
`last = {rev: max(stored rev, 1)}` (rule 7, seq 1 at once).

### 7.7 Cost versus today

| Situation | Today | With push |
|-----------|-------|-----------|
| Phone open, nothing changing, 20 sessions | `sessions` every 2 s: full projection + ≈40 KB TLS write per poll ≈ 72 MB/h; radio never idles | 0 requests, 0 bytes; one in-memory projection per 10 s tick that sends nothing (observer PR refreshes and clocks are outside the fingerprint) |
| Phone looking at a working session | (not possible today; a 1 s `talkState` poll would be ≈2–3 execs/s per phone view plus a feed poll) | ≤ 1 probe/s = ≤ 2 execs/s, shared by every watcher of that session, whatever the hook rate; bytes only when composer/prompt/feed change |
| Same session resting for an hour | desktop poll: 2–3 execs/s ≈ 7 200–10 800 execs/h | 2 execs / 15 s = 480 execs/h, and 0 bytes |
| Session row changes (hook) | seen up to 2 s late, whole list resent | one projection for all connections, one row sent, ≤ `sessionsDebounce` late |
| Large transcript scan on a watched feed | (desktop only) | 2 resets + ≈150 B/s progress |
| Screen off | poll stops only if the app stops it | unwatched → no topic state → pokes return after one atomic load |

The desktop keeps polling in this build (plan §5 "same store, two drivers").

---

## 8. Coalescing and backpressure — `internal/eventbox`

A stdlib + `protocol` leaf so the remote connection and a future local stream
(§11) share it.

### 8.1 Event interface

```go
// internal/protocol/events.go — beside the wire types that implement it, so
// eventbox imports protocol without a cycle. (protocol.Event is already the
// activity-ring row; hence TopicEvent.)
type TopicEvent interface {
	// Merge folds next (later, same topic, same stream) into the receiver and
	// reports false when the result would exceed EventMaxBytes or the kinds
	// cannot merge; the caller then keeps both.
	Merge(next TopicEvent) (TopicEvent, bool)
	// Size is the encoded payload size in bytes: len(json.Marshal(ev)),
	// measured when the producer built it (SizeOf), exact, never estimated.
	Size() int
	// Part reports a snapshot part (Reset or More set); parts never merge.
	Part() bool
}

// SizeOf marshals ev once and records the length; producers call it on every
// event they hand to a box. json.Marshal's output (HTML-escaped) is what the
// FrameWriter embeds, so the recorded size is the bytes on the wire.
func SizeOf[T TopicEvent](ev T) T

// Implemented by *SessionsEvent, *FeedEvent, *TimelineEvent, *DiffEvent and
// thin wrappers TalkEvent{TalkStateData}, StatusEvent{StatusData},
// ProjectsEvent{ProjectsData} whose MarshalJSON emits the inner value verbatim.
```

Merge rules (on the protocol types):
- Replace topics: the newer wins, always mergeable.
- `SessionsEvent` (deltas only): upserts keyed by id (later wins); a later
  remove deletes the pending upsert and is added to `remove`; a later upsert
  removes the id from `remove`; `events`, `omitted` from the later.
- `FeedEvent`, `TimelineEvent` (deltas and scan progress): items keyed by id
  keep the higher `seq`; `lastSeq`, `health`, `scanning`, `connections`,
  `epoch` from the later; `omitted` unioned. Different `epoch` → not
  mergeable.
- A part (`Reset` or `More`) never merges and is never merged into.
- Size: `a.Size() + b.Size()` is an upper bound of the merge; within
  `EventMaxBytes` the merge is accepted and its size re-measured with
  `SizeOf`; above it, the merge is re-measured exactly once and refused if
  still above.

### 8.2 Box API

```go
package eventbox

type Event = protocol.TopicEvent

type Item struct {
	Topic, WatchID string
	Seq            uint64      // assigned at take time
	Event          Event       // nil when End != nil
	End            *protocol.ErrPayload // the stream's final err frame (§3)
}

// New makes one box per connection. onSnap is called under Box.mu when a slot
// needs a snapshot it does not have; it must be a poke (L5 only).
func New(onSnap func(topic string)) *Box

func (b *Box) Start(topic, watchID string)                         // slot := awaiting_snapshot, seq 0; called under topicState.mu
func (b *Box) WantsSnapshot(topic, watchID string) bool             // awaiting_snapshot or needs_snapshot for this watch id
func (b *Box) Install(topic, watchID string, parts []Event) bool    // replaces the slot queue; false: stale id, closed, or snapshot budget
func (b *Box) Offer(topic, watchID string, ev Event)               // live slots only; never blocks
func (b *Box) End(topic, watchID string, e protocol.ErrPayload)    // queue the final err; slot removed once taken
func (b *Box) Stop(topic, watchID string)                          // unwatch: drop the slot if the id matches
func (b *Box) Next(ctx context.Context) (Item, bool)               // the pump's only call
func (b *Box) Close()
```

Callers: `Events.Watch` calls `Start` and the hub's stop func calls `Stop`
(both under `topicState.mu`); producer runs call `WantsSnapshot`, `Install`,
`Offer`, `End` under `topicState.mu`; the pump calls `Next`; `conn.shutdown`
calls `Close`. `Watch` returns as soon as `Start` and the snapshot poke are
done; the first event reaches the pump once a producer run installed it.

### 8.3 Slot rules

- A `live` slot holds a FIFO of at most `SlotQueueMax` delta events. `Offer`
  merges into the tail when `Merge` allows, else appends.
- **Installed snapshots** are exempt from `SlotQueueMax`: a slot may hold
  parts P1..Pk followed by at most ONE delta "tail". While parts remain,
  every new delta folds into that tail (never into a part). A tail that
  cannot merge is an overflow.
- **Snapshot budget.** The parts installed in a box count toward
  `BoxSnapshotMaxBytes`. `Install` refuses (slot stays wanting) when the box
  already holds parts and the new ones would exceed it; when queued part
  bytes drain below the budget, the box calls `onSnap` for each still-waiting
  topic. A single snapshot is at most `TimelineSnapshotMaxBytes` /
  `maxReplyBytes`, both ≤ `BoxSnapshotMaxBytes`, so a box with no parts always
  accepts one.
- **Delta budget.** Pending delta bytes across slots ≤ `ConnPendingMaxBytes`;
  above it, the slot with the largest pending deltas overflows first.
- **Overflow** (a full slot that cannot merge, a tail that cannot merge, or
  the delta budget): the slot drops its queue (parts and deltas), becomes
  `needs_snapshot` and calls `onSnap`. Each overflow is counted per slot; the
  `ResnapshotLimit`-th within `ResnapshotWindow` instead calls
  `End(rate_limited)`. So a link too slow for a topic ends that stream after
  a bounded number of resnapshots rather than rebuilding forever.
- Memory per connection ≤ `ConnPendingMaxBytes + BoxSnapshotMaxBytes`.
- `Next` picks topics by kind priority `talk > sessions > feed > timeline >
  diff > status > projects`, round-robin within a kind, assigns `seq` at take
  time, and returns an `End` item last for its slot.
- The producer side never blocks on a connection. A connection whose writes
  stall hits `writeTimeout` (15 s) and is shut by `failWrite`; its box is
  closed and its watchers removed.

### 8.4 Size rules (no frame can be oversize)

- Every item a delta topic emits (one `SessionInfo`, `AgentFeedItem`,
  `SSHTimelineItem`) is measured by `json.Marshal`; one above
  `EventItemMaxBytes` is not sent: its `{id, seq}` (sessions: its id) goes in
  the event's `omitted` and the producer logs once per (session, item id).
  Producers split events by measured size so each is ≤ `EventMaxBytes`.
- A replace value (`TalkStateData`, `StatusData`, `ProjectsData`) above
  `EventMaxBytes` cannot be split: the topic is not published to anyone;
  each subscribed slot gets `End(frame_too_large)`; logged once per topic
  per value fingerprint.
- The pump encodes every frame. A frame above `MaxFrameBytes` cannot happen
  given the bounds above (`TestEventBudgetFitsFrame`); if it does, the pump
  drops it, ends that stream with `err frame_too_large` (never a
  resnapshot), and logs once per (connection, topic). No path re-snapshots
  after an oversize frame, so nothing loops and the log cannot be driven.

---

## 9. Limits (named constants)

Wire limits in `internal/protocol/events.go` / `attach.go` (mirrored in
`mobile/src/wire/events.ts`); daemon-internal ones in
`internal/daemon/watchhub.go` / `attachimage.go`; box limits in
`internal/eventbox/box.go`.

| Constant | Value | Where | Why |
|----------|-------|-------|-----|
| `MaxTopicBytes` | 160 | protocol | 9-byte kind + 128-byte id + slack |
| `MaxWatchIDBytes` | 64 | protocol | bounds the envelope (§3) |
| `EnvelopeMaxBytes` | 1 KiB | protocol | event frame minus payload |
| `MaxWatchTopicsPerRequest` | 32 | protocol | one request re-watches everything |
| `MaxWatchTopicsPerConn` | 32 | protocol | sessions/projects/status + ~7 sessions × 4 |
| `EventMaxBytes` | 768 KiB | protocol | + `EnvelopeMaxBytes` + 4 ≤ `MaxFrameBytes`; = `agentfeed.maxReplyBytes` |
| `EventItemMaxBytes` | 256 KiB | protocol | an item above it is `omitted` |
| `FeedSnapshotItems` / `FeedDeltaItems` | 300 / 300 | protocol | = `agentfeed.DefaultLimit` |
| `TimelineSnapshotItems` / `TimelineDeltaItems` | 500 / 200 | protocol | page sizes |
| `TimelineSnapshotMaxBytes` | 2 MiB | protocol | beyond it the snapshot is a newest window with `older` |
| `WatchRetryAfter` | 30 s | protocol | client backoff after `rate_limited` |
| `MaxProbedSessions` | 8 | daemon | = `feedSessions`; pinned readers never exceed the LRU |
| `MaxProbedSessionsPerDevice` | 4 | daemon | one device cannot take every probe slot |
| `watchProducerConcurrency` | 4 | daemon | = `reqConcurrency` |
| `watchTick` | 250 ms | daemon | hub loop granularity while active |
| `snapshotMinInterval` | 250 ms | daemon | snapshot builds per topic coalesce |
| `talkProbeActive` / `talkProbeMinGap` | 1 s / 1 s | daemon | = `TALK_POLL_MS`; hook bursts cannot raise the rate |
| `talkProbeResting` | 15 s | daemon | also the read-error backoff ceiling |
| `talkProbeTimeout` | 5 s | daemon | |
| `feedScanProbe` / `feedScanProgressInterval` | 1 s / 1 s | daemon | = `FEED_SCAN_POLL_MS` |
| `sessionsDebounce` / `sessionsMinInterval` / `sessionsTick` | 150 ms / 500 ms / 10 s | daemon | |
| `statusMinInterval` / `statusTick` / `statusRunTimeout` | 2 s / 30 s / 5 s | daemon | |
| `timelineDebounce` | 200 ms | daemon | `Volatile` progress is 1 Hz |
| `diffDebounce` / `diffMinInterval` | 1 s / 3 s | daemon | |
| `SlotQueueMax` | 2 | eventbox | deltas per live slot |
| `ConnPendingMaxBytes` | 2 MiB | eventbox | delta bytes per connection |
| `BoxSnapshotMaxBytes` | 4 MiB | eventbox | installed snapshot bytes per connection; (2 + 4) × `maxWatchingConns` 8 = 48 MiB worst case |
| `maxWatchingConns` | 8 | remote (`events.go`) | connections of one `Server` that hold an event box; spec 01's authenticated pool admits 64, so the box memory needs its own bound (§10.2) |
| `ResnapshotLimit` / `ResnapshotWindow` | 3 / 60 s | eventbox | then `End(rate_limited)` |

**Probe slots.** A `talk:<id>` or `feed:<id>` watch for session S by device D
(`Peer.DeviceID`; `"local"` for a local peer) is admitted when S is already
probed for D, or when D probes fewer than `MaxProbedSessionsPerDevice`
sessions AND (S is already probed by someone, or fewer than
`MaxProbedSessions` sessions are probed). Otherwise that topic is refused with
`limit`. Counted in `watchHub.probed` across all of D's connections. A
device's slot for S is released when its last talk/feed watch of S stops —
by `unwatch`, connection close, `DropDevice` on revoke, or the pump's
revocation check (§10.3), which all run the same stop funcs. Topics already
watched are never evicted. A watch beyond `MaxWatchTopicsPerConn` is refused
with `limit`.

---

## 10. Remote wiring (`internal/remote`)

### 10.1 Interface

```go
// internal/remote/events.go
type Events interface {
	// Watch registers box for topic on behalf of peer (§7.1 rule 6). It builds
	// nothing and performs no I/O; the snapshot follows from a producer run.
	// stop is idempotent and removes only this watch id's registration.
	Watch(ctx context.Context, peer Peer, topic, watchID string, box *eventbox.Box) (stop func(), res protocol.WatchResult)
	// Resnapshot is the box's onSnap: a non-blocking poke.
	Resnapshot(topic string)
}

// Options gains (one hunk in server.go):
//	Events Events // nil: watch answers every topic with WatchCodeUnavailable
```

### 10.2 Connection

- After spec 01's per-frame `AuthorizeFrame` and `rebuild` (both unchanged),
  `conn.dispatchReq` calls `if c.dispatchEvents(id, req) { return }`
  (`events.go`) before `Options.Handle`. `dispatchEvents` handles
  `watch`/`unwatch` INLINE on the reader goroutine (registration only, no I/O
  besides the resp) and returns false for every other command.
- `conn` gains (in `events.go`, guarded by `c.evMu`): `box *eventbox.Box`
  (created lazily on the first watch, with `onSnap = Options.Events.Resnapshot`;
  creation takes one of the `Server`'s `maxWatchingConns` box slots, released in
  `stopEvents`; with none free, every topic of that `watch` answers
  `limit` / "too many watching connections" and no box is made — spec 04's
  stores then fall back to their polling path),
  `watches map[string]watchEntry{watchID string; stop func()}`, and one pump
  goroutine on `c.wg`, started with the box.
- Re-watch order (§6.1): old `stop()`, `box.Stop(topic, oldID)`, then
  `Events.Watch` with the new id; on refusal the topic is simply unwatched.
- `unwatch{all}`: every stop func, every `box.Stop`.
- `conn.shutdown` calls `c.stopEvents()`: every stop func, then
  `box.Close()`. Stop funcs take only L3/L5 locks, which are never held across
  I/O or waits, so shutdown always completes.
- `dispatchReq` runs `Options.Handle` with `WithPeer(c.ctx, c.peer)`
  (`internal/remote/peerctx.go`: `WithPeer`, `PeerFromContext(ctx) (Peer,
  bool)`), so `attachImage` can key uploads by device. A unix-socket request
  has no peer and is keyed `"local"`.

Edits to `conn.go` are exactly three call sites (`dispatchEvents` in
`dispatchReq` plus `WithPeer` there, `c.stopEvents()` in `shutdown`); all
logic lives in `events.go`.

### 10.3 The pump re-authorizes every pushed frame

Spec 01 makes revocation and tier changes immediate by checking every INBOUND
frame (`AuthorizeFrame`), and relies on it to cover the race between
`Registry.Revoke` and `DropDevice`, connections on a listener being rebuilt,
and relayed connections. A watcher that never sends another frame would never
reach that check, so push must check outbound too:

```go
// before writing EACH event frame:
err := c.srv.auth.AuthorizeFrame(c.ctx, c.peer, &pushProbe) // pushProbe = Frame{V:2, Type:"req", Cmd:"watch"}
switch {
case errors.Is(err, ErrUnauthenticated): c.shutdown()                       // revoked or re-paired: nothing more is sent
case errors.Is(err, ErrDenied):          end every stream with err `denied`;  // read tier withdrawn; conn survives
case err != nil:                          c.shutdown()
}
```

`AuthorizeFrame` for a `watch` frame is a registry `Get` + epoch compare + tier
lookup: a map read per event. Spec 01 must keep `AuthorizeFrame` free of side
effects (no strike count, no rate bucket) so the pump may call it; strikes
stay in `conn.authorize`. `TestRevokedSilentWatcherStopsReceiving`: a device
watches `sessions`, is revoked in the registry WITHOUT `DropDevice` reaching
its connection, a store commit follows; no event frame is written and the
connection closes.

### 10.4 Field specs and tiers (rows spec 01's `fields.go` / `tiers.go` carry)

| Command | Tier | Flat fields | Args type | Check | Audit |
|---------|------|-------------|-----------|-------|-------|
| `watch` | read | Args | `WatchArgs` | — (decoded by `dispatchEvents` from the REBUILT request) | not audited (read) |
| `unwatch` | read | Args | `UnwatchArgs` | — (same) | not audited |
| `attachmentImage` | read | Args | `AttachmentImageArgs` | — | not audited |
| `attachImage` | answer | Args | `AttachImageArgs` | — (validation is the handler's) | ONLY a part with `last:true` and any refused part write the audit line; fields `mime`, `part`. Non-last accepted parts write none. |

`watch`/`unwatch` stay in `remote.TransportCommands` (consumed before
`Handle`) and have field specs so the drift guard
(`TestRemoteArgsFieldsAreReviewed`) covers `WatchArgs`/`UnwatchArgs`.

**Rate.** Non-last `attachImage` parts are exempt from spec 01's `writeRate`
bucket; each part instead takes `len(Data)` from a per-device
`attachBytesRate` / `attachBytesBurst` (2 MiB/s / 20 MiB) bucket in spec 01's
`ratelimit.go` (spec 01 §7 is the source of the values); the `last:true` part takes one `writeRate` token. A 20 MiB
image then costs one write token, so an `agentSay` right after it is not
rate-limited.

The unix socket's `d.handle` answers `watch`/`unwatch` with
`{ok:false, error:"watch needs a stream connection"}` in this build.

---

## 11. The desktop later (not built; not precluded)

- The hub, producers and `eventbox` know nothing about TLS or frames. A future
  `cmd=stream` on the unix socket answers one OK line and then turns that
  connection into a v2 frame stream (same `framecodec`, no TLS) served by
  `remote`'s connection loop through `Server.ServeLocal(nc net.Conn, peer
  Peer)` with `Peer{DeviceID:"local", Local:true}`. Requests on it go to
  `d.handle` directly (no allowlist, as today on the socket), and the pump's
  authorization (§10.3) is a no-op for a local peer.
- `Peer.Local` selects the LOCAL sessions view (rows keep `SSH`), the only
  projection difference; topics, stream rules and limits are identical
  (`"local"` is one device for `MaxProbedSessionsPerDevice`).
- `desktop/` (`DaemonService`) would hold one stream connection and re-emit
  events as Wails events; `talk.svelte.ts` / `feed.svelte.ts` get the
  "pushed source" seam in spec 04.
- `TALK_POLL_MS` polling and push coexist on one session: the prober's probe
  and a desktop `talkState` are the same call.

---

## 12. `attachImage` — upload into `<worktree>/.lola/images`

### 12.1 Wire

```go
// internal/protocol/attach.go
type AttachImageArgs struct {
	Session string `json:"session"`
	Name    string `json:"name"`           // the UPLOAD KEY, ^[A-Za-z0-9_-]{8,64}$, client-random; NOT a filename
	Mime    string `json:"mime"`           // AttachMimes
	Data    []byte `json:"data"`           // base64 on the wire; 1..AttachChunkBytes
	Part    int    `json:"part"`           // 0-based, strictly sequential
	Last    bool   `json:"last,omitempty"`
}

// On a non-last part: Received only. On the last part: Path (+ Thumb), exactly
// the shape of desktop's ImageAttachment{path, thumb}.
type AttachImageData struct {
	Received int64  `json:"received"`
	Path     string `json:"path,omitempty"`  // the token: <worktree>/.lola/images/paste-<16 hex><ext>
	Thumb    string `json:"thumb,omitempty"` // always "" from the daemon; the client has the pixels
}

const (
	AttachChunkBytes = 512 << 10 // raw bytes per part; base64 ≈ 683 KiB < MaxFrameBytes
	AttachMaxBytes   = 20 << 20  // = desktop maxImageBytes
	AttachMaxParts   = AttachMaxBytes / AttachChunkBytes
)

var AttachMimes = map[string]string{ // mime -> stored extension
	"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp",
}

// Refusal sentences (Response.Error). Fixed; never built from input.
const (
	AttachErrNoFolder = "this session has no folder to put an image in" // = desktop
	AttachErrGone     = "this session's folder is gone"                 // = desktop
	AttachErrLink     = "refusing to write an image through a link in this session's folder" // = desktop
	AttachErrTooLarge = "that image is over 20 MB"                      // = desktop
	AttachErrType     = "that file is not an image"                     // = desktop
	AttachErrOrder    = "image upload out of order"
	AttachErrBusy     = "too many images uploading"
	AttachErrExpired  = "image upload expired"
	AttachErrPin      = "this sandboxed session cannot take images"
	AttachErrQuota    = "no room for more images in this session today"
	AttachErrDisk     = "the disk is too full to take an image"
)
```

### 12.2 Upload state machine (daemon, `internal/daemon/attachimage.go`)

Key: `(peerKey, session, name)`; `peerKey` = `Peer.DeviceID` or `"local"`.

```
   part 0 ─────────▶ open(received, next=1, f, imgRoot) ── part n==next ──▶ open
     │ (validates)        │       │                                           │
     │                    │       └── part n!=next / bytes>max / quota ──▶ aborted (tmp removed)
     │                    │ idle > AttachIdleTimeout ──────────────────────▶ expired (tmp removed)
     │                    └── last && checks ok ───────────────────────────▶ done (Path returned)
```

Part 0 (and only part 0):

1. The target must be a live agent session. Unknown sessions, stack lanes,
   agentless sessions and goal-managed sessions return `AttachErrNoFolder`.
2. The daemon resolves the attachment root from the session record. Owned
   worktrees use their worktree. Borrowed checkouts use Lola's private
   `local-sessions/<id>` directory. Host agent tabs and direct SSH sessions
   use their private `work` directory. Docker tabs use their existing
   `sandbox/<id>` mount. The resolved root must lie strictly under Lola's
   home. The client can use the optional `SessionInfo.attachmentRoot`
   override to choose the same root for a desktop paste.
3. Check the worktree's gitguard pin. A denied checkout returns
   `AttachErrPin`. A Docker tab requires its owner's matching pin.
   A direct SSH session's private scratch directory has no Git repository.
4. `root, err := os.OpenRoot(wt)`; error → `AttachErrGone`. ALL filesystem
   access below goes through `root` and then `imgRoot` (openat-based; a link
   cannot leave them), and NO command is executed — no `sips`, no git — so
   "no host commands in a sandboxed worktree after the agent starts" holds.
5. `root.Mkdir(".lola", 0o700)`, `root.Mkdir(".lola/images", 0o700)`
   (ErrExist ok); `root.Lstat` each; not a plain directory → `AttachErrLink`.
   `imgRoot, err := root.OpenRoot(".lola/images")`; error → `AttachErrLink`.
   `root` is closed; every later step uses `imgRoot` only.
6. `Mime ∈ AttachMimes` else `AttachErrType`.
7. Capacity:
   - open uploads for this `peerKey` < `AttachMaxOpenPerPeer` and daemon-wide
     < `AttachMaxOpenTotal`, else `AttachErrBusy`;
   - free space: `syscall.Statfs(wt)`, `Bavail × Bsize ≥ AttachMinFreeBytes`,
     else `AttachErrDisk` (also re-checked on the last part);
   - session quota: `existing` = sum of `Size()` of regular entries of
     `imgRoot.Open(".")` → `ReadDir(-1)`, plus `received` of other open
     uploads into the same session; `existing ≥ AttachMaxBytesPerSession` →
     `AttachErrQuota`;
   - device quota: `attachRegistry.daily[peerKey]` (bytes accepted in the
     current UTC day, daemon memory; a restart resets it) ≥
     `AttachMaxBytesPerDevicePerDay` → `AttachErrQuota`.
8. Sweep: remove entries of `imgRoot` named `.upload-*.part` whose `ModTime`
   is older than `AttachIdleTimeout` and that no open upload owns (crash
   leftovers).
9. `tmp = ".upload-<16 hex>.part"`, `f, err := imgRoot.OpenFile(tmp,
   O_CREATE|O_EXCL|O_RDWR|syscall.O_NOFOLLOW, 0o600)` (`O_EXCL` fails on any
   existing name, links included). The upload keeps `f` and `imgRoot` open
   until it ends.

Every part: `Part == next` else abort + `AttachErrOrder`; `len(Data)` in
`1..AttachChunkBytes`; `received + len(Data) ≤ AttachMaxBytes` else abort +
`AttachErrTooLarge`; `existing + received + len(Data) ≤
AttachMaxBytesPerSession` and the device's daily bytes + `len(Data) ≤
AttachMaxBytesPerDevicePerDay` else abort + `AttachErrQuota`; `f.Write`
(writes through the descriptor, never by name); `daily[peerKey] +=
len(Data)`; `next++`; re-arm the expiry timer.

Last part:

1. Sniff through the descriptor: `f.ReadAt(head[:min(512, received)], 0)`;
   `http.DetectContentType(head)` must EQUAL `Mime` (a `.webp` claim must
   sniff as webp), else abort + `AttachErrType`.
2. `final = "paste-<16 hex>" + AttachMimes[Mime]` (`newImageName` semantics,
   `crypto/rand`).
3. `imgRoot.Link(tmp, final)` (an existing name → new name, ≤ 3 tries).
4. Identity check: `li, _ := imgRoot.Lstat(final)`, `fi, _ := f.Stat()`;
   `li.Mode().IsRegular() && os.SameFile(li, fi)` must hold. Otherwise the
   agent swapped or re-linked `tmp` between the last write and the link:
   `imgRoot.Remove(final)`, abort + `AttachErrLink`.
5. `imgRoot.Remove(tmp)`, `f.Close()`, `imgRoot.Close()`.
6. Return `Path = filepath.Join(wt, ".lola", "images", final)`, `Thumb = ""`.

The check guarantees the token names the inode the daemon wrote at the moment
it answered. An agent owns its worktree and can rewrite the file afterwards;
that is true on the desktop too and is not a boundary.

**Expiry.** `attachRegistry` arms one `time.AfterFunc` while at least one
upload is open, set to the earliest `lastPart + AttachIdleTimeout`; it is
independent of the watch hub. Expiry closes `f` and removes `tmp` through
the upload's own `imgRoot`. A part arriving for an expired key gets
`AttachErrExpired`. Daemon shutdown aborts every open upload the same way.

Constants (daemon): `AttachIdleTimeout = 60 s`, `AttachMaxOpenPerPeer = 2`,
`AttachMaxOpenTotal = 8`, `AttachMaxBytesPerSession = 200 MiB`,
`AttachMaxBytesPerDevicePerDay = 500 MiB`, `AttachMinFreeBytes = 1 GiB`.

**Audit and rate:** §10.4 — one audit line for the last part (device,
session, mime, part count) or for a refusal; non-last parts take only the
attach byte bucket.

Client rules (spec 04): convert HEIC to JPEG and downscale to ≤ 2048 px long
edge before upload; send one part at a time and wait for its `resp` (parts are
refused out of order, and `reqConcurrency` would otherwise reorder them);
insert the returned `path` into `Pastes` exactly as the desktop composer does
with `ImageAttachment.path`, so the message carries the token `SENT_IMAGE_RE`
recognises on both surfaces.

Tier: `attachImage` is in `answer` (plan §3.2).

### 12.3 `attachmentImage` — reading an attachment remotely

```go
type AttachmentImageArgs struct {
	Session string `json:"session"`
	Path    string `json:"path"`             // the token as it appears in the message
	Offset  int64  `json:"offset,omitempty"` // multiple of AttachChunkBytes
}

type AttachmentImageData struct {
	Mime   string `json:"mime"`   // sniffed, one of AttachMimes
	Size   int64  `json:"size"`
	Offset int64  `json:"offset"`
	Data   []byte `json:"data"`   // ≤ AttachChunkBytes
	EOF    bool   `json:"eof,omitempty"`
}

const (
	AttachErrNotHere           = "this is not an attachment in this session" // = desktop
	AttachErrFolderUnavailable = "this session's image folder is unavailable" // = desktop
	AttachErrUnavailable       = "this image is no longer available"         // = desktop
	AttachErrUnreadable        = "could not read this image"                 // = desktop
	AttachErrNotImage          = "this attachment is not an image"           // = desktop
)
```

Daemon steps (`handleAttachmentImage`):

1. Worktree from the session, never from the client:
   - live: use the same session attachment root as an upload (§12.2 step 2);
   - ended: `sshstore.ReadEnded(d.home, id).KeptWorktree`, which must lie
     strictly under `filepath.Join(home, "worktrees")` + separator (an ended
     record carries no kind, and a borrowed directory is never under it);
   - neither → `AttachErrNotHere`.
2. `Path` must be absolute, `filepath.Clean(Path) == Path`,
   `filepath.Dir(Path) == filepath.Join(wt, ".lola", "images")`, and
   `filepath.Base(Path)` must match
   `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}\.(png|jpe?g|gif|webp)$` (case-insensitive
   extension) → else `AttachErrNotHere`.
3. Root at the images folder itself, as the desktop does:
   `root := os.OpenRoot(wt)` (error → `AttachErrFolderUnavailable`);
   `root.Lstat(".lola")` and `root.Lstat(".lola/images")` must be plain
   directories, then `imgRoot := root.OpenRoot(".lola/images")` → any failure
   `AttachErrFolderUnavailable`. Then `li := imgRoot.Lstat(base)`: missing, a
   symlink, or not regular → `AttachErrUnavailable`; `f := imgRoot.Open(base)`
   (error → `AttachErrUnavailable`); `fi := f.Stat()` must satisfy
   `os.SameFile(li, fi)` (else `AttachErrUnavailable`) and
   `fi.Size() ≤ AttachMaxBytes` (else `AttachErrUnreadable`). A link inside
   `images` can reach nothing outside it, and a link is never served.
4. Sniff the first 512 bytes (always `f.ReadAt(…, 0)`) → must be in
   `AttachMimes` → else `AttachErrNotImage`.
5. Return `[Offset, Offset+AttachChunkBytes)` via `f.ReadAt`;
   `Offset % AttachChunkBytes != 0` or `Offset > Size` → `AttachErrUnreadable`.

The mobile shim implements `TalkService.ReadAttachmentImage(worktree, path)`
by resolving the session whose `SessionInfo.attachmentRoot || SessionInfo.worktree` (or
`EndedSession.keptWorktree`) equals `worktree`, fetching chunks sequentially
and returning a `data:` URL — the same string the desktop returns, so
`MessageGallery.svelte` is unchanged.

Tier: `attachmentImage` is in `read` (added to spec 01's table; Deviation 4).

---

## 13. Golden vectors

Frame-hex vectors have ONE home, `mobile/src/wire/testdata/`, split into files
so specs can add vectors without editing one file:

```
mobile/src/wire/testdata/
  frames.json   envelope + panes (this spec rewrites every case to "v":2,
                drops req_hello_bearer, updates err_unsupported_version
                to minV 2 / maxV 2 / "daemon speaks envelope v2")
  events.json   NEW, this spec
  pairing.json  spec 01 §6.6 (req_remote_pair, ready, pairing results); the
                ONLY home of req_remote_pair
```

Spec 01's `internal/protocol/testdata/pairing_vectors.json` holds FUNCTION
vectors (proof, code, QR payload), not frames, and stays where it is.

`goldenvectors_test.go` loads every `*.json` in the directory (sorted) with
the same schema; `TestGoldenVectorsCoverEveryFrameKind` checks coverage across
the union (so `event` and spec 01's `ready` must appear). `codec.test.ts` does the same with
`import.meta.glob`. Hex is produced by the Go `FrameWriter`: a helper test
`TestWriteGoldenVectors`, run only with `LOLA_WRITE_VECTORS=1`, rewrites the
`hex` of every case from its `frame`; normal runs only compare.

`events.json` cases:

| Case | Pins |
|------|------|
| `req_watch_three_topics` | `{"cmd":"watch","args":{"topics":["sessions","talk:lola-fe-42","feed:lola-fe-42"]}}` inside a `req` with id `w7` |
| `resp_watch_partial` | `WatchData` with one `ok:true` and one `{ok:false, code:"unknown_session", message}` |
| `req_unwatch_all` | `args:{"all":true}` |
| `event_sessions_reset` | `id`, `topic`, `seq:1`, `reset:true`, one full `SessionInfo`, `events` |
| `event_sessions_delta` | `upsert` + `remove`, no `reset` key (omitempty) |
| `event_sessions_reset_more` | split snapshot: `reset:true, more:true` |
| `event_talk_prompt` | `TalkStateData` with `prompt` (hash, lines, choices, `since`) |
| `event_talk_gone` | the fixed gone shape of §5.4 |
| `event_feed_reset_older` | `reset`, `older`, `epoch`, `scanning`, items with tool |
| `event_feed_scan_progress` | `scanning:true`, `items:[]`, no `reset` |
| `event_feed_delta_omitted` | `items:[]` (never null) + `omitted:[{id,seq}]` |
| `event_timeline_delta` | items + `connections` |
| `event_diff` | `{"rev":4,"cause":"checkpoint"}` |
| `err_stream_too_large` | `err` with `id`, `topic`, `code:"frame_too_large"` |
| `req_attach_image_part0` | `data` base64 of 3 PNG-magic bytes, `part:0` without `last` |
| `resp_attach_image_last` | `{received, path}`, `thumb` omitted |
| `req_attachment_image_offset` | `offset` present |
| `resp_attachment_image_eof` | `data` base64, `eof:true` |
| `event_seq_large` | `seq` 4294967297 (> 2³²; TS reads it as a safe integer) |

---

## 14. AGENTS.md — new invariant text

Add under "Invariants", after "Typing into an agent":

```markdown
### Server push (contract: `docs/mobile/02-events.md`)
- **Remote envelope is v2 only** (`FrameVersionMin = FrameVersionCurrent = 2`),
  the pairing frame included. `event` frames carry the `id` of the `watch`
  that started the stream (≤ 64 bytes, `[A-Za-z0-9_-]`) and a per-stream
  `seq` from 1; a client drops other ids and re-watches on a gap. An `err`
  frame on a watch id ends that stream only.
- **Every pushed frame is authorized against the live registry**, not only
  inbound frames: the pump calls `AuthorizeFrame` before each event; a
  revoked peer is shut, a peer without `read` has its streams ended.
- **Pokes are free and blind.** `d.watch.Poke*` takes only `pokeMu`, never
  calls the store or a producer, and returns at once when nothing is watched.
  `session.Store.OnChange` and `sshstore.Store.SetNotify` are the two commit
  hooks; a new writer needs no poke of its own. Never call the store from
  inside them.
- **One lock order:** producer lock (`sessionsProjector.mu`, `feedPub.mu`,
  `timelinePub.mu`) → data locks (taken and released inside compute) →
  `topicState.mu` → `Box.mu` → leaves (`pokeMu`, `watchHub.mu`,
  `prober.mu`). Compute, then install: nothing is computed, read or executed
  under `topicState.mu`. Snapshots are built only by producer runs on the
  hub's `sem`, never on the connection reader or the pump.
- **No watcher, no work.** Topic state exists only while watched; the hub
  ticker stops at zero; the pane is probed only for a watched `talk:<id>`
  (≤ 1/s whatever the hook rate, 15 s resting, parked when gone), at most 8
  sessions daemon-wide and 4 per device.
- **A probe is exactly the desktop's talkState call**, including its pane
  reconciliation of the agent axis. A watch therefore counts as a viewer. It
  never types, and it moves the agent axis only by the pane evidence
  `talkPaneState` already trusts.
- **Feed claims may drive only display-only pokes** (diff ticks); nothing on
  the push path feeds a gate, slot, reaction or write-back.
- **Snapshot, then deltas, under one lock; a slow link degrades, never
  blocks.** `internal/eventbox` coalesces per slot, resnapshots on overflow
  at most `ResnapshotLimit` times per minute, then ends the stream. At most
  `maxWatchingConns` connections hold a box (memory bound). Nothing
  oversize is ever resnapshotted: items over `EventItemMaxBytes` are
  `omitted`, an unsplittable value ends its stream.
- Sessions events use `remoteSessionInfo` (the `handleRemote` projection).
  `age`, `headlineAgo`, `board.updatedAgo`, `prObservedAt` and `Event.ago`
  are outside the change fingerprint (`sessionsVolatile`); add any new
  clock-derived field there. The timeline topic serves only
  `handleSessionTimeline` (`publicTimeline`) output.
- `attachImage` writes only through `os.Root` of the session's
  `.lola/images` (under `$LOLA_HOME`, never a borrowed checkout), runs no
  command, refuses a sandboxed worktree whose gitguard pin is lost, keeps the
  upload's descriptor and verifies the linked file is that inode, is capped
  per upload, per session, per device per day and by free disk, and returns
  the desktop's token (`<attachment root>/.lola/images/paste-<16 hex><ext>`).
  `attachmentImage` roots at `.lola/images` and never serves a link.
```

Replace the agentfeed bullet's "display-only" note in "Untrusted output"
with: "`agentfeed` is display-only; its one non-display consumer is the
push path's diff tick, itself display-only."

In "Architecture map → Core" add: "`eventbox` — per-connection event
mailbox (coalescing, resnapshot, stream end); transport-free."

Work item 7 also rewrites `internal/daemon/talkfeed.go`'s header comment:

```go
// It is pull-driven or watch-driven: talkState moves a viewed session's reader a little
// every second, agentFeed moves it by wall clock, and the push prober (watchprobe.go)
// moves it for a session a phone watches. Nothing runs for a session no client is
// looking at or watching. Its one consumer outside a display is the push path's diff
// tick (an edit/run item pokes diff:<id>), which is itself display-only.
```

---

## 15. Tests

`internal/protocol`
- `TestParseTopic` — table: every kind, lane id refused, `..`, `/`, empty id,
  129-byte id, uppercase kind, trailing colon.
- `TestFrameEventDirection` — `event` is to-client only; daemon refuses it.
- `TestEventBudgetFitsFrame` — 64-byte id, 160-byte topic, max seq,
  `EventMaxBytes` payload of `<` characters (escaped) encodes ≤ `MaxFrameBytes`.
- `TestSizeOfIsEncodedLength` — `SizeOf` equals the payload bytes the
  `FrameWriter` writes, for HTML-escaped and control-character content.
- Golden: `TestGoldenVectors*` over the directory; `TestWriteGoldenVectors`
  gated by env.
- `TestSessionsEventMerge`, `TestFeedEventMerge`, `TestTimelineEventMerge` —
  upsert/remove interplay, epoch mismatch refuses, parts never merge, size
  bound refuses.

`internal/eventbox`
- `TestOfferNeverBlocks` (no reader, 10 000 offers, bounded memory).
- `TestOverflowBecomesSnapshot` — `SlotQueueMax`+1 unmergeable → `onSnap`
  called once, slot skipped by `Next` until `Install`, `seq` continues.
- `TestMultipartSnapshotUnderContinuousDeltas` — a 3-part install, 1 000
  offers while parts drain: parts arrive intact and in order, then one merged
  tail; no resnapshot.
- `TestResnapshotLimitEndsStream` — repeated overflow → `End(rate_limited)`
  after `ResnapshotLimit`.
- `TestSnapshotBudgetDefersInstall`, `TestConnPendingBudgetDropsLargestDelta`.
- `TestPriorityTalkFirst`, `TestRestartResetsSeq`, `TestStopMatchesWatchID`
  (a stale `Stop` leaves the new stream), `TestEndIsLast`.

`internal/session`, `internal/sshstore`
- `TestOnChangeFiresForEveryCommit` (Upsert, Update true, Update fn=false =
  no fire, Update of a missing id = no fire, Apply insert, Delete, Delete
  missing = no fire, PruneOlderThan, lane write → base id; Prev/Next correct).
- `TestSetNotifyAfterUnlock` (callback may call `View` without deadlock;
  Volatile returning false = no fire).

`internal/remote`
- `TestWatchInlineAndPump` with a fake `Events`: resp, then event `seq:1`
  carrying the watch id.
- `TestWatchIDBounded` — a 300 KiB id and an id with `/` → `bad watch request`.
- `TestRewatchNewIDAndSeq` (old stop before new Watch), `TestUnwatchAll`,
  `TestWatchLimitPerConn`, `TestWatchingConnsBounded` (a ninth connection's
  watch → every topic `limit`; a slot frees on close), `TestWatchWithoutEventsUnavailable`,
  `TestShutdownStopsWatches`, `TestPeerInHandleContext`.
- `TestOversizeFrameEndsStreamOnce` — a fake producer handing an oversize
  event: one `err frame_too_large`, no resnapshot, one log line.
- `TestRevokedSilentWatcherStopsReceiving` (§10.3),
  `TestDeniedReadEndsStreams` (tier withdrawn: `err denied` per stream, conn
  survives).
- `TestV1FrameRefused` (unsupported_version, minV 2), `TestV1PairFrameRefused`.

`internal/daemon` (fakes via seams: `paneVisible`, `paneCursor`,
`feedAdvance`, `$LOLA_HOME = t.TempDir()`)
- `TestPokeNoWatchersIsFree` — no topic state, poke does not touch `wake`.
- `TestNoLockInversion` (§7.1).
- `TestEveryTopicDeliversSeqOne` — each of the seven topics, freshly watched
  with no prior value, delivers `seq:1` within `talkProbeTimeout + 1 s`;
  `diff` delivers `{rev:1}` at once.
- `TestSessionsSnapshotThenDelta` — hook event → one-row upsert; delete →
  remove.
- `TestSessionsFingerprintIgnoresClock` — only clocks move → no event.
- `TestObserverPRRefreshIsQuiet` — an observer pass that only sets
  `PRObservedAt` → no event.
- `TestSessionsRemoteProjectionStripsSSH`.
- `TestTalkProbeCadence` — fake clock: working → 1 s, resting → 15 s, gone /
  agentless / goal-managed → parked, read errors back off to 15 s.
- `TestTalkProbeRateUnderHookBurst` — 100 hook commits in 1 s on a working
  session → ≤ 2 probes; an observer refresh of a resting session → no probe.
- `TestTalkProbeSharedAcrossWatchers` — two boxes, one pane read per tick.
- `TestWatchedProbeNeverTypes` — `sendKeys`, `pasteBuffer` and every typing
  seam panic; a watched probe over a prompt pane, a draft and a modal runs
  without calling them.
- `TestWatchedProbeNeedsPaneProofForAtPrompt` — a working record and a pane
  without `ActivityWaiting` → `AtPrompt` stays false after N probes.
- `TestTalkFingerprintIgnoresFeedSeq`.
- `TestFeedResetOnEpochChange`, `TestFeedDeltaAfterSnapshotNoLoss`
  (advance between snapshot and publish), `TestFeedPinnedNotEvicted`
  (9 desktop sessions do not evict a watched reader),
  `TestFeedPinSurvivesAgentSwitch`, `TestFeedScanBytesBounded` (a 50 MB fake
  transcript: two resets + progress only).
- `TestRewatchUsesSnapshotCache` — 100 re-watches of `feed:x` with an
  unmoved cursor → one `Page` call.
- `TestTimelineNotifyFromAnyWriter` — `applyTimeline`, a talk send, `Volatile`.
- `TestTimelineRedacted` — a block with `Private` set and a connection with
  `Prompt` set never appear (nor `Host`, `User`, `Cwd`, `TermElevated`,
  handback `Transcript`) in any `TimelineEvent`: the snapshot, every
  `more:true` part, and deltas.
- `TestTimelineProducerUsesOnlyPublicTimeline` (§5.6).
- `TestTimelineSnapshotWindow` — over `TimelineSnapshotMaxBytes` → newest
  by `Order`, `older:true`.
- `TestOversizeItemOmitted` — a 900 KiB timeline item → `omitted`, the rest
  delivered, one log line.
- `TestDiffPokes` — checkpoint, restore, feed edit item, branch change;
  debounce collapses.
- `TestProbedSessionsLimit` — ninth talk session → `limit`;
  `TestProbedSessionsPerDevice` — device A holding 4 cannot take a fifth,
  device B still can; A's slots free on revoke.
- `TestUnixSocketWatchRefused`.
- `TestHubShutdownDrains` — ctx cancel: loop exits, no goroutine leak.

`internal/daemon` attach
- `TestAttachImageHappyPath` — 3 parts, sniff matches, token path, 0600,
  under `.lola/images`, name pattern matches `SENT_IMAGE_RE`'s Go twin.
- `TestAttachImageRefusals` — table: unknown session, worktree outside home,
  malformed local session id, `.lola` symlink, `images`
  symlink to `/tmp`, symlinked tmp pre-planted, wrong mime sniff, HEIC mime,
  > 20 MiB, out-of-order part, part 0 twice, third concurrent upload,
  gitguard-denied worktree.
- `TestAttachImageSessionQuota`, `TestAttachImageDeviceDailyQuota`,
  `TestAttachImageDiskFloor` (Statfs seam).
- `TestAttachImageSwapBeforeLink` — between the last write and the link a
  hook replaces `tmp` with another file and with a symlink → `AttachErrLink`,
  no `paste-*` left behind.
- `TestAttachImageExpiry` — fake clock, nothing watched, tmp removed;
  `TestAttachImageSweepsStaleParts`.
- `TestAttachImageNoExec` — exec seams panic if called.
- `TestAttachmentImageRead` — chunks reassemble; ended session via
  `KeptWorktree`; refused: `../`, other session's images dir, non-clean path,
  symlinked file pointing outside, symlinked file pointing at another
  worktree file (`.lola/images/x.png -> ../../README.png`), linked `images`
  folder (`AttachErrFolderUnavailable`), another local session’s image, ended
  record whose `KeptWorktree` is outside `$LOLA_HOME/worktrees`.

`mobile/src/wire` (spec 04 consumes, this spec provides)
- `events.test.ts` — the client state machine of §6.2: stale id dropped, gap
  → re-watch, reset applies, `more` assembly, `failed(limit)` no auto-retry,
  stream `err` → failed with the code's retry rule.
- `codec.test.ts` — all vector files; `topic` round-trips and a non-string
  `topic` is malformed.

---

## 16. Dependencies on other specs and merge order

**Spec 01 must carry** (listed in the reply to the orchestrator as matching
changes):
- §6: the pairing frame is `"v":2` only.
- §8.3 tier table: `attachmentImage` → `read` (new row); `watch`/`unwatch`
  read (transport); `attachImage` answer.
- §8.5 `requestFields`: the four rows of §10.4 above.
- §7 rate limits: non-last `attachImage` parts exempt from `writeRate`;
  `attachBytesRate`/`attachBytesBurst` (2 MiB/s / 20 MiB) per device in `ratelimit.go`.
- §8.8 audit: `attachImage` audited only for `last:true` or a refusal.
- §4.4: `AuthorizeFrame` stays side-effect free (the pump calls it per
  event). The same outbound check should cover pane output frames (spec 01's
  concern: a revoked device with an attached pane keeps receiving PTY bytes
  until `DropDevice` reaches it).
- §8.6: `handleSessionTimeline` and `publicTimeline` exactly as written there;
  this spec calls nothing else.

**Shared files.** Spec 01 lands first for every shared file; spec 02's hunks
are small and disjoint, and all spec 02 logic lives in its own new files.

| File | Spec 01 item | Spec 02 item | Spec 02's hunks only |
|------|--------------|--------------|----------------------|
| `internal/protocol/frame.go` | W1 (three codes) | 1 | `FrameVersionCurrent/Min = 2`; `Frame.Topic`; `FrameEvent` + its `FrameDirections` case |
| `internal/remote/server.go` | W4c | 4 | `Options.Events` field + comment |
| `internal/remote/conn.go` | W4c | 4 | `dispatchEvents` + `WithPeer` in `dispatchReq`; `c.stopEvents()` in `shutdown` |
| `internal/remote/fields.go`, `tiers.go`, `ratelimit.go` | W4a, W4c | — | none: spec 01 enters §10.4's rows and the attach bucket |
| `internal/daemon/remotewire.go` | W5b | 6 | `Events: d.watch` in `startRemote`'s options; the `sessions` strip calls `remoteSessionInfo` |
| `internal/daemon/server.go` | W5b | 7 | one line at the top of `handle`: `if r, ok := d.handlePush(ctx, req); ok { return r }`; one `PokeGlobal` at the end of `handleReload` and of `handleEnable` |
| `internal/daemon/daemon.go` | W5b | 6 | field `watch *watchHub`; `newDaemon`: `newWatchHub` + `OnChange` registration; `Run`: start `h.run` on `d.wg`; four `d.linOK = v` → `d.setLinOK(v)` |
| `internal/daemon/talk.go` | W5d | — | none (routing moved to `server_push.go`) |
| `AGENTS.md` | W12 | 9 | one editor merges every spec's section (`00-build-plan.md`, wave D docs item) |

- **03 (relay):** none in this spec's files; a relayed `net.Conn` reaches the
  same `conn` loop, so §10.3 covers relayed watchers. Spec 03's `relayCred`
  handler reads the device with this spec's `remote.PeerFromContext`. Note for 03: scan bursts are bounded (§5.5),
  so push stays far below `RelaySpliceBytesPerSec`.
- **04 (client):** consumes `mobile/src/wire/events.ts`; owns `bridge.ts`, the
  pushed-source seams, `.age` → `firstSeen` (and `board.updatedAgo` →
  `board.updatedAt`), the shim's `ReadAttachmentImage`/attach path,
  background `unwatch`, watch ids matching §3, the `older` paging of timeline
  and the handling of `omitted` and stream-ending `err` frames.

---

## 17. Deviations from `docs/mobile-rethink.md`

1. **Event frames carry `id`.** Plan: `{type, topic, seq, payload}`. Added the
   watch request's `id` (an existing envelope field, now bounded to 64 bytes)
   so a re-watch needs no ordering between the resp and in-flight events.
2. **`watch`/`unwatch` are handled by `internal/remote`, not `d.handle`.**
   The watcher's lifetime is the connection's, and `Options.Handle` has no
   connection identity. The unix socket refuses them until a stream mode
   exists (§11).
3. **`attachImage.name` is the upload key, not a filename.** The stored name
   is daemon-chosen `paste-<16 hex><ext>` (the desktop's).
4. **New read command `attachmentImage`** (chunked, `read` tier). The plan
   asks for an image read but its tier table has no entry; 20 MiB images do
   not fit `MaxFrameBytes`, hence offsets.
5. **`diff:<id>` is signal-only** (no git fingerprint).
6. **`FrameVersionMin = 2` immediately**, the pairing frame included.
7. **Clock-derived and observer-refresh fields are not change signals**
   (`sessionsVolatile`); clients render from timestamps. Without this the
   plan's "idle cost: zero" is false (`PRObservedAt` moves every ≈30 s).
8. **`feedSeq` is excluded from the talk fingerprint**; push clients follow
   `feed:<id>`.
9. **Probing is capped at 8 sessions, 4 per device**, and watched readers are
   pinned; a request beyond is refused with `limit`.
10. **A watch counts as a viewer.** Plan §4 says the daemon "computes a cheap
    fingerprint"; the talk fingerprint needs the desktop's `talkState`, which
    reconciles the agent axis from the pane. A phone watching a session
    therefore drives that reconciliation as an open desktop view does; it
    never types. talkfeed.go's "pull-driven" contract is widened accordingly.
11. **Push needs an outbound authorization check** (§10.3); the plan's
    per-device revocation is otherwise not immediate for silent watchers.
12. **Attachments are quota-capped** (per session, per device per day, free
    disk); the plan names only size and type caps.
13. **Golden frame vectors become a directory** of files so parallel specs do
    not edit one JSON file.

---

## 18. Work items

Order and cross-spec file ownership: `docs/mobile/00-build-plan.md` is
authoritative. The items below are this spec's decomposition; the build plan
maps them onto waves (and gives every shared file to one item per wave).

Each item owns the listed files exclusively within this spec; shared files
with spec 01 are edited only in the hunks §16 names, after spec 01's item
landed. Dependencies: 1–5 none beyond this document's types; 6 needs 1, 3, 4
(`remote.Events`, `Peer`, `PeerFromContext` signatures) and 5; 7 needs 6's
poke API (stub `d.watch` until it lands) and 8's handler signatures; 8 needs
1 and 4 (`PeerFromContext`).

1. **Wire types (Go) + vectors.**
   `internal/protocol/frame.go` (§16 hunks),
   `internal/protocol/events.go` (new: topics, `ParseTopic`, watch types,
   event payload types, `OmittedItem`, `TopicEvent`, `SizeOf`, `Merge`/`Size`/
   `Part`, wire limits, stream-end messages),
   `internal/protocol/attach.go` (new: attach types, constants, sentences),
   `internal/protocol/events_test.go` (new),
   `internal/protocol/goldenvectors_test.go` (directory loader, writer helper),
   `mobile/src/wire/testdata/frames.json`, `mobile/src/wire/testdata/events.json` (new).
2. **Wire types (TS).**
   `mobile/src/wire/protocol.ts` (version 2, `FRAME_EVENT` in the direction
   table, `topic` field on `Frame`),
   `mobile/src/wire/codec.ts` (`encodeFrameJSON` writes `topic` between
   `pane` and `seq`, omitted when empty; `decodeFrameBody` type-checks and
   keeps `topic`),
   `mobile/src/wire/events.ts` (new: types, `parseTopic`, limits, watch-id
   generator matching §3, the §6.2 `TopicStream` state machine),
   `mobile/src/wire/events.test.ts` (new), `mobile/src/wire/codec.test.ts`,
   `mobile/src/wire/protocol.test.ts`, `mobile/src/wire/index.ts`.
3. **Event mailbox.**
   `internal/eventbox/box.go`, `internal/eventbox/box_test.go` (new package;
   imports only stdlib + `internal/protocol`).
4. **Remote connection.**
   `internal/remote/events.go` (new: `Events`, `dispatchEvents`, watch-id
   check, pump with per-event `AuthorizeFrame`, oversize handling,
   `stopEvents`), `internal/remote/peerctx.go` (new),
   `internal/remote/events_test.go` (new), `internal/remote/server.go` and
   `internal/remote/conn.go` (§16 hunks).
5. **Commit hooks in leaves.**
   `internal/session/session.go` (`Change`, `OnChange`),
   `internal/session/session_test.go`,
   `internal/sshstore/sshstore.go` (`SetNotify`),
   `internal/sshstore/sshstore_test.go`.
6. **Hub and producers.**
   `internal/daemon/watchhub.go` (hub, pokes, loop, `Events` impl, probe-slot
   accounting, `setLinOK`),
   `internal/daemon/watchsessions.go` (projector, `remoteSessionInfo`,
   `sessionsVolatile`, `talkFieldsChanged`, `headChanged`),
   `internal/daemon/watchprobe.go` (prober, schedule, talk publisher),
   `internal/daemon/watchfeed.go` (feed publisher, snapshot cache),
   `internal/daemon/watchtopics.go` (status, projects, timeline, diff
   producers),
   `internal/daemon/watch_test.go`, `internal/daemon/watch_lock_test.go` (new),
   `internal/daemon/daemon.go` and `internal/daemon/remotewire.go` (§16 hunks).
7. **Poke sites and routing.**
   `internal/daemon/status.go` (`statusTracker.onChange`),
   `internal/daemon/ssh.go` (`SetNotify` in `sshStore`),
   `internal/daemon/talkfeed.go` (poke in `feedEntry.advance`; `feedHub.pins`,
   `pin`/`unpin`, eviction skip; header comment of §14),
   `internal/daemon/talkstate.go` (poke in `talkReadAsync`; `talkReadErr`
   seam set by `talkPaneState`),
   `internal/daemon/checkpoints.go` (diff pokes),
   `internal/daemon/server_push.go` (new: `handlePush` — unix `watch`/`unwatch`
   refusal, `attachImage`/`attachmentImage` routing with the request's peer),
   `internal/daemon/server.go` (§16 hunks).
8. **Attachments.**
   `internal/daemon/attachimage.go` (upload registry, quotas, expiry timer,
   stale-part sweep, `handleAttachImage`, `handleAttachmentImage`),
   `internal/daemon/attachimage_test.go`.
9. **Docs.** `AGENTS.md` (§14 text; coordinate with 01/03 — one editor
   merges), this file's status line.
