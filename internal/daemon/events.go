package daemon

// Activity feed (TUI "what's happening" ticker): a small in-memory ring of
// recent NOTABLE session status transitions. It replaces the old needs-you
// sparkline — instead of plotting one scalar over a tiny window, it names WHICH
// session changed and WHAT it changed to.
//
// Capture is centralized: session.Store.OnTransition fires recordSessionEvent
// for every Update that changes Status (hook path, observer, answer, kill,
// reactions), and the spawn site records the birth explicitly (spawn enters via
// Upsert, which has no callback). So the feed's vocabulary is EXACTLY the stored
// derived Status — no separate derivation to drift.
//
// The ring is intentionally NOT persisted: the daemon is long-lived (launchd
// KeepAlive), so the feed survives TUI restarts; a daemon restart starts it
// fresh, which is the right clean slate.

import (
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

// eventLogCap bounds the activity ring. A rail feed shows ~4-10 lines; a couple
// dozen of scrollback is plenty and keeps the oldest churn from lingering.
const eventLogCap = 64

// sessionEvent is one recorded transition. from "" marks a spawn.
type sessionEvent struct {
	at    time.Time
	id    string
	issue string
	title string
	from  string
	to    string
}

// eventLog is the mutex-guarded ring. Appends drop the oldest past the cap.
type eventLog struct {
	mu   sync.Mutex
	ring []sessionEvent
	cap  int
}

func newEventLog(capacity int) *eventLog {
	if capacity < 1 {
		capacity = 1
	}
	return &eventLog{cap: capacity}
}

func (l *eventLog) record(e sessionEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ring = append(l.ring, e)
	if len(l.ring) > l.cap {
		l.ring = l.ring[len(l.ring)-l.cap:]
	}
}

// snapshot returns a copy of the ring oldest-first.
func (l *eventLog) snapshot() []sessionEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]sessionEvent, len(l.ring))
	copy(out, l.ring)
	return out
}

// notableTransition decides whether a from→to status change is worth a feed
// line. A spawn (from "") always is. Routine noise is dropped: the idle↔working
// turn churn (working is kept ONLY as a "resumed" signal out of needs_input),
// and the internal non-statuses that never read as an event to a human.
func notableTransition(from, to string) bool {
	if to == "" {
		return false
	}
	if from == "" {
		return true // spawn
	}
	switch to {
	case "idle", "no_pr", "no_signal", "orphaned", "none":
		return false
	case "working":
		return from == "needs_input" // resumed after waiting on a human
	default:
		return true
	}
}

// recordSessionEvent is the single capture helper wired to both
// session.Store.OnTransition (from = prior Status) and the spawn site
// (from = ""). It applies the notability filter, then stamps and rings the
// event. Cheap and lock-safe: it only touches the event ring, never the store,
// so it is safe to run inside the store's transition callback.
func (d *Daemon) recordSessionEvent(from string, s session.Session) {
	if d.events == nil || !notableTransition(from, s.Status) {
		return
	}
	d.events.record(sessionEvent{
		at:    time.Now(),
		id:    s.ID,
		issue: s.Issue,
		title: s.Title,
		from:  from,
		to:    s.Status,
	})
}

// eventFeed flattens the ring into render-ready protocol.Events, NEWEST FIRST,
// with each Ago formatted against now (the request time), matching how
// SessionInfo.Age is computed.
func (d *Daemon) eventFeed(now time.Time) []protocol.Event {
	if d.events == nil {
		return nil
	}
	evs := d.events.snapshot()
	out := make([]protocol.Event, 0, len(evs))
	for i := len(evs) - 1; i >= 0; i-- {
		e := evs[i]
		out = append(out, protocol.Event{
			ID:    e.id,
			Issue: e.issue,
			Title: e.title,
			From:  e.from,
			To:    e.to,
			Ago:   formatAge(now.Sub(e.at)),
		})
	}
	return out
}
