package daemon

import (
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/session"
)

// notableTransition keeps spawns, resumes, and real state changes; drops the
// idle↔working turn churn and the internal non-statuses.
func TestNotableTransition(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{"", "working", true},              // spawn
		{"needs_input", "working", true},   // resumed
		{"idle", "working", false},         // routine turn start
		{"working", "idle", false},         // routine turn end
		{"working", "needs_input", true},   // needs you
		{"ci_pending", "ci_failed", true},  // CI broke
		{"review_pending", "merged", true}, // merged
		{"working", "no_pr", false},        // internal
		{"working", "orphaned", false},     // internal
		{"working", "", false},             // no status
	}
	for _, c := range cases {
		if got := notableTransition(c.from, c.to); got != c.want {
			t.Errorf("notableTransition(%q,%q) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

// The ring never grows past its cap and keeps the newest entries.
func TestEventLogRingCap(t *testing.T) {
	l := newEventLog(3)
	for i := 0; i < 10; i++ {
		l.record(sessionEvent{id: string(rune('a' + i))})
	}
	snap := l.snapshot()
	if len(snap) != 3 {
		t.Fatalf("ring len = %d, want cap 3", len(snap))
	}
	if snap[0].id != "h" || snap[2].id != "j" {
		t.Errorf("ring kept the wrong window: %q..%q, want h..j", snap[0].id, snap[2].id)
	}
}

// End to end through the store: a spawn (explicit) and a status transition (via
// the OnTransition wiring) both land in the feed, newest first, with an Ago.
func TestEventFeedThroughStore(t *testing.T) {
	d := &Daemon{events: newEventLog(64), sessions: session.NewStore(t.TempDir())}
	d.sessions.OnTransition(d.recordSessionEvent)

	sess := session.Session{ID: "s1", Issue: "ENG-1", Status: "working"}
	d.sessions.Upsert(sess)
	d.recordSessionEvent("", sess) // spawn birth, as the dispatch site does

	// A notable transition flows through the callback.
	d.sessions.Update("s1", func(cur *session.Session) bool {
		cur.Status = "needs_input"
		return true
	})
	// A routine idle→working churn is filtered out (must not appear).
	d.sessions.Update("s1", func(cur *session.Session) bool {
		cur.Status = "idle"
		return true
	})

	feed := d.eventFeed(time.Now())
	if len(feed) != 2 {
		t.Fatalf("feed has %d events, want 2 (spawn + needs_input): %+v", len(feed), feed)
	}
	// Newest first: the needs_input transition leads, the spawn trails.
	if feed[0].To != "needs_input" || feed[0].Issue != "ENG-1" {
		t.Errorf("newest event = %+v, want ENG-1 → needs_input", feed[0])
	}
	if feed[1].From != "" || feed[1].To != "working" {
		t.Errorf("oldest event = %+v, want the spawn (from \"\")", feed[1])
	}
	if feed[0].Ago == "" {
		t.Error("event must carry a formatted Ago")
	}
}
