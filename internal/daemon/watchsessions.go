package daemon

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/eventbox"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

// The `sessions` topic: a store-commit poke, a projector that diffs rows by
// fingerprint, and the helpers the commit hook in newDaemon uses.

const (
	sessionsDebounce    = 150 * time.Millisecond
	sessionsMinInterval = 500 * time.Millisecond
	sessionsTick        = 10 * time.Second
)

// remoteSessionInfo is the phone's view of one session row: the ssh glance
// carries the exact command waiting for approval and the agent's latest
// summary, so it is dropped. handleRemote and the projector both call it.
func remoteSessionInfo(si protocol.SessionInfo) protocol.SessionInfo {
	si.SSH = nil
	si.Share = nil // team sharing is a desktop matter; a phone never sees who is attached
	return si
}

// talkFieldsChanged says a commit moved something the Conversation composer
// shows: an insert or delete, or any of the agent axis, input reason, prompt
// flag, queue hold, deferral reason or agent kind. LastSeen, PR facts, usage and
// the observer's routine refresh never poke talk.
func talkFieldsChanged(prev, next *session.Session) bool {
	if prev == nil || next == nil {
		return true
	}
	return prev.AgentState != next.AgentState ||
		prev.InputReason != next.InputReason ||
		prev.AtPrompt != next.AtPrompt ||
		prev.QueueHeld != next.QueueHeld ||
		prev.AutomationDeferredReason != next.AutomationDeferredReason ||
		prev.Agent != next.Agent
}

// headChanged says the session's branch or PR number moved (both sides present).
func headChanged(prev, next *session.Session) bool {
	if prev == nil || next == nil {
		return false
	}
	prNum := func(s *session.Session) int {
		if s.PR == nil {
			return 0
		}
		return s.PR.Number
	}
	return prev.Branch != next.Branch || prNum(prev) != prNum(next)
}

// setLinOK records whether the Linear client is usable and pokes the status
// topic when the answer changed. The caller holds d.mu; the poke takes only a
// leaf lock.
func (d *Daemon) setLinOK(v bool) {
	changed := d.linOK != v
	d.linOK = v
	if changed && d.watch != nil {
		d.watch.PokeGlobal(pokeStatus)
	}
}

// sessionsVolatile zeroes, on a COPY, every wire field that moves with the
// clock rather than with the session. They are sent with their current value
// but never make a row "changed". A new clock-derived field belongs here
// (TestSessionsFingerprintIgnoresClock).
func sessionsVolatile(si protocol.SessionInfo) protocol.SessionInfo {
	si.Age = ""
	si.HeadlineAgo = ""
	si.PRObservedAt = time.Time{}
	if si.Board != nil {
		b := *si.Board
		b.UpdatedAgo = ""
		si.Board = &b
	}
	return si
}

// eventsFingerprint hashes the activity ring without its clock-derived Ago.
func eventsFingerprint(evs []protocol.Event) uint64 {
	c := make([]protocol.Event, len(evs))
	copy(c, evs)
	for i := range c {
		c[i].Ago = ""
	}
	b, _ := json.Marshal(c)
	return fingerprint(b)
}

type rowState struct {
	fp      uint64
	size    int
	info    protocol.SessionInfo
	omitted bool // the row exceeds EventItemMaxBytes: only its id is sent
}

type sessionsProjector struct {
	mu        sync.Mutex // L1
	ran       bool
	gen       uint64 // bumped on every publish that sent something
	order     []string
	published map[string]rowState
	evs       []protocol.Event
	evFP      uint64
	lastRun   time.Time
	snap      []eventbox.Event // cached reset parts for snapGen
	snapGen   uint64
	snapOK    bool
}

type sessionsProducer struct {
	h *watchHub
	p sessionsProjector
}

func (*sessionsProducer) timing() timing {
	return timing{debounce: sessionsDebounce, minInterval: sessionsMinInterval, tick: sessionsTick}
}
func (*sessionsProducer) timeout() time.Duration { return statusRunTimeout }
func (*sessionsProducer) replace() bool          { return false }

func (sp *sessionsProducer) build(_ context.Context, _ *topicState, snapOnly, want bool, _ string) topicOut {
	p := &sp.p
	p.mu.Lock()
	defer p.mu.Unlock()
	now := sp.h.now()
	var out topicOut
	if !(snapOnly && p.ran && now.Sub(p.lastRun) < sessionsMinInterval) {
		out.deltas = p.project(sp.h.d.sessionsData())
		p.lastRun = now
	}
	p.ran = true
	if want {
		out.parts = p.parts()
	}
	return out
}

// project diffs a fresh sessions read against what was last sent and returns
// the delta events (none when nothing changed). The first run only records.
func (p *sessionsProjector) project(data protocol.SessionsData) []eventbox.Event {
	if p.published == nil {
		p.published = map[string]rowState{}
	}
	next := make(map[string]rowState, len(data.Sessions))
	order := make([]string, 0, len(data.Sessions))
	var upsert []protocol.SessionInfo
	var omitted []string
	for _, si := range data.Sessions {
		si = remoteSessionInfo(si)
		vb, _ := json.Marshal(sessionsVolatile(si))
		rs := rowState{fp: fingerprint(vb), info: si}
		old, had := p.published[si.ID]
		if had && old.fp == rs.fp {
			// Unchanged: keep the row as sent, clock fields included.
			next[si.ID] = old
			order = append(order, si.ID)
			continue
		}
		full, _ := json.Marshal(si)
		rs.size = len(full)
		if rs.size > protocol.EventItemMaxBytes {
			rs.omitted, rs.info = true, protocol.SessionInfo{}
			omitted = append(omitted, si.ID)
		} else {
			upsert = append(upsert, si)
		}
		next[si.ID] = rs
		order = append(order, si.ID)
	}
	var remove []string
	for id := range p.published {
		if _, ok := next[id]; !ok {
			remove = append(remove, id)
		}
	}
	sort.Strings(remove)
	evFP := eventsFingerprint(data.Events)
	evChanged := evFP != p.evFP
	first := !p.ran
	p.published, p.order, p.evs, p.evFP = next, order, data.Events, evFP
	if first || (len(upsert) == 0 && len(remove) == 0 && len(omitted) == 0 && !evChanged) {
		return nil
	}
	p.gen++
	return splitSessions(upsert, remove, omitted, data.Events, evChanged)
}

// splitSessions cuts a delta into events of at most EventMaxBytes by measured
// size. Remove, omitted and the ring go in the first event.
func splitSessions(upsert []protocol.SessionInfo, remove, omitted []string, evs []protocol.Event, withEvents bool) []eventbox.Event {
	var out []eventbox.Event
	cur := &protocol.SessionsEvent{Remove: remove, Omitted: omitted}
	if withEvents {
		cur.Events = evs
		if cur.Events == nil {
			cur.Events = []protocol.Event{}
		}
	}
	budget := protocol.EventMaxBytes - 4096
	size := 0
	flush := func() {
		out = append(out, protocol.SizeOf(cur))
		cur = &protocol.SessionsEvent{}
		size = 0
	}
	for _, r := range upsert {
		b, _ := json.Marshal(r)
		if size+len(b) > budget && len(cur.Upsert) > 0 {
			flush()
		}
		cur.Upsert = append(cur.Upsert, r)
		size += len(b) + 1
	}
	flush()
	return out
}

// parts builds the reset parts from the published rows, cached by generation.
func (p *sessionsProjector) parts() []eventbox.Event {
	if p.snapOK && p.snapGen == p.gen {
		return p.snap
	}
	var parts []*protocol.SessionsEvent
	cur := &protocol.SessionsEvent{Reset: true, Events: p.evs}
	if cur.Events == nil {
		cur.Events = []protocol.Event{}
	}
	budget := protocol.EventMaxBytes - 4096
	size := 0
	for _, id := range p.order {
		rs := p.published[id]
		if rs.omitted {
			cur.Omitted = append(cur.Omitted, id)
			continue
		}
		b, _ := json.Marshal(rs.info)
		if size+len(b) > budget && len(cur.Upsert) > 0 {
			parts = append(parts, cur)
			cur = &protocol.SessionsEvent{}
			size = 0
		}
		cur.Upsert = append(cur.Upsert, rs.info)
		size += len(b) + 1
	}
	parts = append(parts, cur)
	out := make([]eventbox.Event, len(parts))
	for i, e := range parts {
		e.More = i < len(parts)-1
		out[i] = protocol.SizeOf(e)
	}
	p.snap, p.snapGen, p.snapOK = out, p.gen, true
	return out
}
