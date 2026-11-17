package protocol

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
)

// Server push: the event frame, its topics and their payloads. The contract is
// docs/mobile/02-events.md; the TypeScript mirror is mobile/src/wire/events.ts.

// Topics. A topic is a global name ("sessions", "projects", "status") or
// "<kind>:<session>" for the four per-session kinds.
const (
	TopicSessions = "sessions"
	TopicProjects = "projects"
	TopicStatus   = "status"
	TopicTalk     = "talk"
	TopicFeed     = "feed"
	TopicTimeline = "timeline"
	TopicDiff     = "diff"

	// MaxTopicBytes bounds a canonical topic: a 9-byte kind, the colon, a
	// 128-byte session id and slack.
	MaxTopicBytes = 160
)

// laneSep is session.LaneSep. protocol is a leaf and cannot import session.
const laneSep = "~"

// maxTopicSession bounds the session part of a topic.
const maxTopicSession = 128

// ParseTopic splits a topic into its kind and session ("" for the global
// topics). ok is false for anything outside the grammar; it never normalises,
// so a topic that parses is already canonical.
//
//	topic   = "sessions" | "projects" | "status" | kind ":" session
//	kind    = "talk" | "feed" | "timeline" | "diff"
//	session = 1..128 bytes, a valid session id, and not a lane id "<id>~<app>"
func ParseTopic(t string) (kind, session string, ok bool) {
	if len(t) == 0 || len(t) > MaxTopicBytes {
		return "", "", false
	}
	switch t {
	case TopicSessions, TopicProjects, TopicStatus:
		return t, "", true
	}
	kind, session, found := strings.Cut(t, ":")
	if !found {
		return "", "", false
	}
	switch kind {
	case TopicTalk, TopicFeed, TopicTimeline, TopicDiff:
	default:
		return "", "", false
	}
	if !validTopicSession(session) {
		return "", "", false
	}
	return kind, session, true
}

// validTopicSession mirrors sshstore.ValidSession plus the length bound and the
// no-lane rule (session.BaseID(id) == id).
func validTopicSession(id string) bool {
	if id == "" || len(id) > maxTopicSession {
		return false
	}
	if id == "." || id == ".." || strings.HasPrefix(id, ".") || filepath.Base(id) != id {
		return false
	}
	if strings.ContainsAny(id, `/\`+laneSep) {
		return false
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// Commands of the push and attachment vocabulary.
const (
	CmdWatch           = "watch"           // WatchArgs   -> WatchData
	CmdUnwatch         = "unwatch"         // UnwatchArgs -> WatchData
	CmdAttachImage     = "attachImage"     // AttachImageArgs     -> AttachImageData
	CmdAttachmentImage = "attachmentImage" // AttachmentImageArgs -> AttachmentImageData
)

// WatchArgs starts streams. It travels in Request.Args; the request's frame id
// is the watch id every event of these streams carries.
type WatchArgs struct {
	Topics []string `json:"topics"` // 1..MaxWatchTopicsPerRequest, canonical, no duplicates
}

// UnwatchArgs stops streams.
type UnwatchArgs struct {
	Topics []string `json:"topics,omitempty"`
	All    bool     `json:"all,omitempty"` // every topic on this connection
}

// WatchData is the reply of watch and unwatch.
type WatchData struct {
	Topics []WatchResult `json:"topics"` // one per requested topic, in request order; [] for unwatch{all}
}

// WatchResult is one topic's outcome.
type WatchResult struct {
	Topic   string `json:"topic"`
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`    // WatchCode*, set when !OK
	Message string `json:"message,omitempty"` // a short fixed sentence, never built from input
}

// Watch refusal codes. The list is closed; a client treats an unknown code as
// failed.
const (
	WatchCodeInvalidTopic   = "invalid_topic"   // not in the grammar
	WatchCodeUnknownSession = "unknown_session" // no live or readable ended session
	WatchCodeLimit          = "limit"           // a limit below; retry after an unwatch
	WatchCodeUnavailable    = "unavailable"     // this daemon has no event hub
)

// Wire limits. Daemon-internal ones live beside their code.
const (
	MaxWatchIDBytes          = 64      // watch id: ^[A-Za-z0-9_-]{1,64}$
	EnvelopeMaxBytes         = 1 << 10 // an event frame minus its payload
	MaxWatchTopicsPerRequest = 32
	MaxWatchTopicsPerConn    = 32
	EventMaxBytes            = 768 << 10 // EventMaxBytes + EnvelopeMaxBytes + 4 <= MaxFrameBytes
	EventItemMaxBytes        = 256 << 10 // an item above it is reported as omitted
	FeedSnapshotItems        = 300
	FeedDeltaItems           = 300
	TimelineSnapshotItems    = 500
	TimelineDeltaItems       = 200
	TimelineSnapshotMaxBytes = 2 << 20 // beyond it a snapshot is a newest window with Older

	// WatchRetryAfter is how long a client waits before re-watching a topic
	// ended with rate_limited.
	WatchRetryAfter = 30 * time.Second
)

// TopicEvent is one pushed value. It is named so because Event is already the
// activity-ring row. Implemented by *SessionsEvent, *FeedEvent, *TimelineEvent,
// *DiffEvent, *TalkEvent, *StatusEvent and *ProjectsEvent.
type TopicEvent interface {
	// Merge folds next (later, same topic, same stream) into a copy of the
	// receiver and reports false when the kinds cannot merge, a part is
	// involved, or the result would exceed EventMaxBytes; the caller then keeps
	// both. Neither operand is modified.
	Merge(next TopicEvent) (TopicEvent, bool)
	// Size is the encoded payload size in bytes, measured by SizeOf when the
	// producer built the event.
	Size() int
	// Part reports a snapshot part (Reset or More set). Parts never merge.
	Part() bool
}

// sizer is how SizeOf records a measurement without widening TopicEvent, so a
// test double implementing the three exported methods still satisfies it.
type sizer interface{ setSize(int) }

// SizeOf marshals ev once and records the length, then returns it. json.Marshal
// output (HTML-escaped) is what the FrameWriter embeds, so the recorded size is
// the number of payload bytes on the wire. A value that cannot be marshalled is
// returned unmeasured.
func SizeOf[T TopicEvent](ev T) T {
	b, err := json.Marshal(ev)
	if err != nil {
		return ev
	}
	if s, ok := any(ev).(sizer); ok {
		s.setSize(len(b))
	}
	return ev
}

// measured returns the recorded size, or measures now when none was recorded.
func measured(recorded int, v any) int {
	if recorded > 0 {
		return recorded
	}
	b, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return len(b)
}

// remeasure records the merged value's exact size and refuses it above
// EventMaxBytes.
func remeasure[T TopicEvent](ev T) (TopicEvent, bool) {
	ev = SizeOf(ev)
	if ev.Size() > EventMaxBytes {
		return nil, false
	}
	return ev, true
}

// ---- sessions ----

// SessionsEvent is the `sessions` topic: a snapshot (Reset, possibly in parts),
// then keyed deltas. Rows are the REMOTE projection of SessionInfo.
type SessionsEvent struct {
	Reset   bool          `json:"reset,omitempty"`   // discard everything held; this (with its parts) is the full set
	More    bool          `json:"more,omitempty"`    // this reset continues in the next event(s)
	Upsert  []SessionInfo `json:"upsert,omitempty"`  // whole rows, keyed by id
	Remove  []string      `json:"remove,omitempty"`  // ids gone from the store
	Omitted []string      `json:"omitted,omitempty"` // ids whose row exceeds EventItemMaxBytes; fetch with `sessions`
	Events  []Event       `json:"events,omitempty"`  // the activity ring, WHOLE; on reset (first part) and when it changed

	size int
}

func (e *SessionsEvent) setSize(n int) { e.size = n }
func (e *SessionsEvent) Size() int     { return measured(e.size, e) }
func (e *SessionsEvent) Part() bool    { return e.Reset || e.More }

// Merge folds a later delta in. Upserts are keyed by id (the later wins); a
// later remove deletes the pending upsert and joins Remove; a later upsert
// takes the id out of Remove. Events is the later's when it carries one, else
// the earlier's (the ring is sent whole, so a delta without it means unchanged).
// Omitted is the union, minus ids the later event upserted or removed.
func (e *SessionsEvent) Merge(next TopicEvent) (TopicEvent, bool) {
	n, ok := next.(*SessionsEvent)
	if !ok || n == nil || e.Part() || n.Part() {
		return nil, false
	}
	out := &SessionsEvent{Events: e.Events}
	if n.Events != nil {
		out.Events = n.Events
	}
	at := map[string]int{}
	put := func(r SessionInfo) {
		if i, ok := at[r.ID]; ok {
			out.Upsert[i] = r
			return
		}
		at[r.ID] = len(out.Upsert)
		out.Upsert = append(out.Upsert, r)
	}
	for _, r := range e.Upsert {
		put(r)
	}
	out.Remove = append(out.Remove, e.Remove...)

	drop := map[string]bool{} // ids whose pending upsert is cancelled
	for _, r := range n.Upsert {
		put(r)
		out.Remove = removeString(out.Remove, r.ID)
	}
	for _, id := range n.Remove {
		drop[id] = true
		if !containsString(out.Remove, id) {
			out.Remove = append(out.Remove, id)
		}
	}
	for _, id := range n.Omitted {
		drop[id] = true // the stale pending row would mislead; the client fetches it
	}
	if len(drop) > 0 {
		kept := out.Upsert[:0]
		for _, r := range out.Upsert {
			if !drop[r.ID] {
				kept = append(kept, r)
			}
		}
		out.Upsert = kept
	}

	upserted := map[string]bool{}
	for _, r := range n.Upsert {
		upserted[r.ID] = true
	}
	for _, id := range append(append([]string(nil), e.Omitted...), n.Omitted...) {
		if containsString(out.Omitted, id) || containsString(n.Remove, id) {
			continue
		}
		if upserted[id] && !containsString(n.Omitted, id) {
			continue
		}
		out.Omitted = append(out.Omitted, id)
	}
	return remeasure(out)
}

func removeString(list []string, s string) []string {
	out := list[:0]
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ---- feed ----

// FeedEvent is the `feed:<id>` topic: a snapshot (Reset), then keyed deltas and
// scan progress. Items is never null.
type FeedEvent struct {
	Reset    bool            `json:"reset,omitempty"` // discard held items; Items is the newest page
	Older    bool            `json:"older,omitempty"` // reset only: older history exists; page it with agentFeed{before}
	Session  string          `json:"session"`
	Epoch    string          `json:"epoch"`
	LastSeq  int64           `json:"lastSeq"`
	Scanning bool            `json:"scanning,omitempty"`
	Health   string          `json:"health"`
	Items    []AgentFeedItem `json:"items"`             // never null
	Omitted  []OmittedItem   `json:"omitted,omitempty"` // items over EventItemMaxBytes

	size int
}

// OmittedItem names an item too large to send. A client fetches it with the
// pull command of its topic.
type OmittedItem struct {
	ID  string `json:"id"`
	Seq int64  `json:"seq"`
}

func (e *FeedEvent) setSize(n int) { e.size = n }
func (e *FeedEvent) Size() int     { return measured(e.size, e) }
func (e *FeedEvent) Part() bool    { return e.Reset }

// Merge folds a later delta or scan-progress event in: items keyed by id keep
// the higher seq; LastSeq, Health and Scanning come from the later; Omitted is
// unioned. A different session or epoch does not merge.
func (e *FeedEvent) Merge(next TopicEvent) (TopicEvent, bool) {
	n, ok := next.(*FeedEvent)
	if !ok || n == nil || e.Part() || n.Part() || e.Session != n.Session || e.Epoch != n.Epoch {
		return nil, false
	}
	out := &FeedEvent{
		Session: n.Session, Epoch: n.Epoch, LastSeq: n.LastSeq,
		Scanning: n.Scanning, Health: n.Health,
		Items: mergeKeyed(e.Items, n.Items, func(i AgentFeedItem) (string, int64) { return i.ID, i.Seq }),
	}
	out.Omitted = mergeOmitted(e.Omitted, n.Omitted, out.Items, func(i AgentFeedItem) (string, int64) { return i.ID, i.Seq })
	return remeasure(out)
}

// mergeKeyed upserts b into a by id, keeping the higher seq, in first-seen
// order. The result is never nil.
func mergeKeyed[T any](a, b []T, key func(T) (string, int64)) []T {
	out := make([]T, 0, len(a)+len(b))
	at := map[string]int{}
	for _, list := range [][]T{a, b} {
		for _, it := range list {
			id, seq := key(it)
			if i, ok := at[id]; ok {
				if _, oldSeq := key(out[i]); seq >= oldSeq {
					out[i] = it
				}
				continue
			}
			at[id] = len(out)
			out = append(out, it)
		}
	}
	return out
}

// mergeOmitted unions two omitted lists (higher seq per id) and drops entries
// an item in items already supersedes.
func mergeOmitted[T any](a, b []OmittedItem, items []T, key func(T) (string, int64)) []OmittedItem {
	have := map[string]int64{}
	for _, it := range items {
		id, seq := key(it)
		have[id] = seq
	}
	var out []OmittedItem
	at := map[string]int{}
	for _, list := range [][]OmittedItem{a, b} {
		for _, o := range list {
			if seq, ok := have[o.ID]; ok && seq >= o.Seq {
				continue
			}
			if i, ok := at[o.ID]; ok {
				if o.Seq > out[i].Seq {
					out[i] = o
				}
				continue
			}
			at[o.ID] = len(out)
			out = append(out, o)
		}
	}
	return out
}

// ---- timeline ----

// TimelineEvent is the `timeline:<id>` topic. It is built only from the
// redacted session timeline reader, never from the raw SSH store.
type TimelineEvent struct {
	Reset       bool              `json:"reset,omitempty"`
	More        bool              `json:"more,omitempty"`  // a reset continues
	Older       bool              `json:"older,omitempty"` // reset only: the snapshot is a newest window
	Session     string            `json:"session"`
	LastSeq     int64             `json:"lastSeq"`
	Items       []SSHTimelineItem `json:"items"`                 // upsert by id, never null
	Connections []SSHConnInfo     `json:"connections,omitempty"` // WHOLE; on the first reset part and when changed
	Omitted     []OmittedItem     `json:"omitted,omitempty"`     // items over EventItemMaxBytes

	size int
}

func (e *TimelineEvent) setSize(n int) { e.size = n }
func (e *TimelineEvent) Size() int     { return measured(e.size, e) }
func (e *TimelineEvent) Part() bool    { return e.Reset || e.More }

// Merge folds a later delta in: items keyed by id keep the higher seq; LastSeq
// comes from the later; Connections is the later's when present (it is sent
// whole), else the earlier's; Omitted is unioned.
func (e *TimelineEvent) Merge(next TopicEvent) (TopicEvent, bool) {
	n, ok := next.(*TimelineEvent)
	if !ok || n == nil || e.Part() || n.Part() || e.Session != n.Session {
		return nil, false
	}
	out := &TimelineEvent{
		Session: n.Session, LastSeq: n.LastSeq, Connections: e.Connections,
		Items: mergeKeyed(e.Items, n.Items, func(i SSHTimelineItem) (string, int64) { return i.ID, i.Seq }),
	}
	if n.Connections != nil {
		out.Connections = n.Connections
	}
	out.Omitted = mergeOmitted(e.Omitted, n.Omitted, out.Items, func(i SSHTimelineItem) (string, int64) { return i.ID, i.Seq })
	return remeasure(out)
}

// ---- diff ----

// DiffEvent is the `diff:<id>` topic: a signal-only counter. The daemon runs no
// git to produce it; a client fetches the diff itself while the view is open.
type DiffEvent struct {
	Rev   int64  `json:"rev"`             // per session, daemon memory, monotonic; starts at 1
	Cause string `json:"cause,omitempty"` // DiffCause*: display and debug only

	size int
}

// Why a diff revision moved.
const (
	DiffCauseEdit       = "edit"       // a feed item of category edit or run (a CLAIM; display-only)
	DiffCauseCheckpoint = "checkpoint" // a checkpoint was committed
	DiffCauseRestore    = "restore"    // a restore moved files
	DiffCauseHead       = "head"       // the session's branch or PR number changed
)

func (e *DiffEvent) setSize(n int) { e.size = n }
func (e *DiffEvent) Size() int     { return measured(e.size, e) }
func (e *DiffEvent) Part() bool    { return false }
func (e *DiffEvent) Merge(next TopicEvent) (TopicEvent, bool) {
	n, ok := next.(*DiffEvent)
	if !ok || n == nil {
		return nil, false
	}
	return remeasure(&DiffEvent{Rev: n.Rev, Cause: n.Cause})
}

// ---- replace topics: talk, status, projects ----

// TalkEvent is the `talk:<id>` topic. It marshals as the inner TalkStateData.
type TalkEvent struct {
	TalkStateData
	size int
}

func (e TalkEvent) MarshalJSON() ([]byte, error) { return json.Marshal(e.TalkStateData) }
func (e *TalkEvent) setSize(n int)               { e.size = n }
func (e *TalkEvent) Size() int                   { return measured(e.size, e.TalkStateData) }
func (e *TalkEvent) Part() bool                  { return false }
func (e *TalkEvent) Merge(next TopicEvent) (TopicEvent, bool) {
	n, ok := next.(*TalkEvent)
	if !ok || n == nil {
		return nil, false
	}
	return remeasure(&TalkEvent{TalkStateData: n.TalkStateData})
}

// StatusEvent is the `status` topic. It marshals as the inner StatusData.
type StatusEvent struct {
	StatusData
	size int
}

func (e StatusEvent) MarshalJSON() ([]byte, error) { return json.Marshal(e.StatusData) }
func (e *StatusEvent) setSize(n int)               { e.size = n }
func (e *StatusEvent) Size() int                   { return measured(e.size, e.StatusData) }
func (e *StatusEvent) Part() bool                  { return false }
func (e *StatusEvent) Merge(next TopicEvent) (TopicEvent, bool) {
	n, ok := next.(*StatusEvent)
	if !ok || n == nil {
		return nil, false
	}
	return remeasure(&StatusEvent{StatusData: n.StatusData})
}

// ProjectsEvent is the `projects` topic. It marshals as the inner ProjectsData.
type ProjectsEvent struct {
	ProjectsData
	size int
}

func (e ProjectsEvent) MarshalJSON() ([]byte, error) { return json.Marshal(e.ProjectsData) }
func (e *ProjectsEvent) setSize(n int)               { e.size = n }
func (e *ProjectsEvent) Size() int                   { return measured(e.size, e.ProjectsData) }
func (e *ProjectsEvent) Part() bool                  { return false }
func (e *ProjectsEvent) Merge(next TopicEvent) (TopicEvent, bool) {
	n, ok := next.(*ProjectsEvent)
	if !ok || n == nil {
		return nil, false
	}
	return remeasure(&ProjectsEvent{ProjectsData: n.ProjectsData})
}
