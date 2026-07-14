package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/scm"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()

	st := NewStore(dir)
	st.Upsert(Session{
		ID:        "b-1",
		Source:    "ao",
		Project:   "beta",
		Issue:     "ENG-2",
		IssueUUID: "uuid-2",
		Branch:    "lola/eng-2-1",
		TmuxName:  "lola-beta-eng-2",
		AOStatus:  "working",
		Status:    "working",
		PR: &scm.PR{
			Number:         42,
			URL:            "https://github.com/acme/beta/pull/42",
			State:          "OPEN",
			Mergeable:      "MERGEABLE",
			ReviewDecision: "APPROVED",
			ChecksState:    "pass",
		},
	})
	st.Upsert(Session{ID: "a-1", Source: "native", Project: "alpha", Issue: "ENG-1"})
	if err := st.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got := NewStore(dir).Snapshot()
	want := st.Snapshot()
	if len(got) != len(want) {
		t.Fatalf("loaded %d sessions, want %d", len(got), len(want))
	}
	for i := range want {
		w, g := want[i], got[i]
		if g.ID != w.ID || g.Source != w.Source || g.Project != w.Project ||
			g.Issue != w.Issue || g.IssueUUID != w.IssueUUID || g.Branch != w.Branch ||
			g.TmuxName != w.TmuxName || g.AOStatus != w.AOStatus || g.Status != w.Status {
			t.Errorf("session %d: got %+v, want %+v", i, g, w)
		}
		if !g.FirstSeen.Equal(w.FirstSeen) || !g.LastSeen.Equal(w.LastSeen) {
			t.Errorf("session %d timestamps: got %v/%v, want %v/%v",
				i, g.FirstSeen, g.LastSeen, w.FirstSeen, w.LastSeen)
		}
		if (g.PR == nil) != (w.PR == nil) {
			t.Fatalf("session %d PR presence: got %v, want %v", i, g.PR, w.PR)
		}
		if w.PR != nil && *g.PR != *w.PR {
			t.Errorf("session %d PR: got %+v, want %+v", i, *g.PR, *w.PR)
		}
	}
}

// The P3 reaction-state fields must survive a save/reload round-trip.
func TestReactionStateRoundTrip(t *testing.T) {
	dir := t.TempDir()

	st := NewStore(dir)
	st.Upsert(Session{
		ID:                "r-1",
		Source:            "native",
		Project:           "nori",
		Issue:             "ENG-9",
		CIRetries:         2,
		LastReactedStatus: "ci_failed",
		Escalated:         true,
		AtPrompt:          true,
		PendingReaction:   "changes_requested",
	})
	// A session with all reaction fields at their zero values (omitempty must
	// not corrupt the reload).
	st.Upsert(Session{ID: "r-0", Source: "native", Project: "nori", Issue: "ENG-1"})
	if err := st.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok := NewStore(dir).Get("r-1")
	if !ok {
		t.Fatal("reloaded store missing r-1")
	}
	if got.CIRetries != 2 || got.LastReactedStatus != "ci_failed" || !got.Escalated ||
		!got.AtPrompt || got.PendingReaction != "changes_requested" {
		t.Errorf("reaction state not round-tripped: %+v", got)
	}

	zero, ok := NewStore(dir).Get("r-0")
	if !ok {
		t.Fatal("reloaded store missing r-0")
	}
	if zero.CIRetries != 0 || zero.LastReactedStatus != "" || zero.Escalated ||
		zero.AtPrompt || zero.PendingReaction != "" {
		t.Errorf("zero-value reaction state should stay zero: %+v", zero)
	}
}

func TestSnapshotSorted(t *testing.T) {
	st := NewStore(t.TempDir())
	st.Upsert(Session{ID: "3", Project: "zeta", Issue: "ENG-1"})
	st.Upsert(Session{ID: "2", Project: "alpha", Issue: "ENG-9"})
	st.Upsert(Session{ID: "1", Project: "alpha", Issue: "ENG-2"})

	snap := st.Snapshot()
	var order []string
	for _, s := range snap {
		order = append(order, s.ID)
	}
	if len(order) != 3 || order[0] != "1" || order[1] != "2" || order[2] != "3" {
		t.Fatalf("snapshot order = %v, want [1 2 3]", order)
	}
}

func TestUpsertPreservesFirstSeen(t *testing.T) {
	st := NewStore(t.TempDir())
	st.Upsert(Session{ID: "s", Project: "p", Issue: "ENG-1", Status: "working"})

	first := st.Snapshot()[0]
	if first.FirstSeen.IsZero() || first.LastSeen.IsZero() {
		t.Fatalf("timestamps not stamped on insert: %+v", first)
	}

	// A later upsert must keep FirstSeen — even if the caller supplies a
	// different one — and restamp LastSeen.
	st.Upsert(Session{ID: "s", Project: "p", Issue: "ENG-1", Status: "idle",
		FirstSeen: time.Now().Add(time.Hour)})

	got := st.Snapshot()[0]
	if !got.FirstSeen.Equal(first.FirstSeen) {
		t.Errorf("FirstSeen changed on upsert: got %v, want %v", got.FirstSeen, first.FirstSeen)
	}
	if got.LastSeen.Before(first.LastSeen) {
		t.Errorf("LastSeen went backwards: got %v, earlier than %v", got.LastSeen, first.LastSeen)
	}
	if got.Status != "idle" {
		t.Errorf("Status not updated: got %q, want %q", got.Status, "idle")
	}
}

func TestUpdateAppliesAtomically(t *testing.T) {
	st := NewStore(t.TempDir())
	st.Upsert(Session{ID: "s", Project: "p", Issue: "ENG-1", Status: "working",
		PR: &scm.PR{Number: 1, State: "OPEN"}})
	before := st.Snapshot()[0]

	got, ok := st.Update("s", func(sess *Session) bool {
		if sess.Status != "working" {
			t.Errorf("fn sees status %q, want the current record", sess.Status)
		}
		sess.Status = "needs_input"
		sess.PR.State = "MERGED" // fn works on a copy until it commits
		return true
	})
	if !ok || got.Status != "needs_input" {
		t.Fatalf("Update = (%+v, %v), want the mutated session", got, ok)
	}
	after := st.Snapshot()[0]
	if after.Status != "needs_input" || after.PR.State != "MERGED" {
		t.Errorf("stored session = %+v, want the mutation applied", after)
	}
	if after.LastSeen.Before(before.LastSeen) {
		t.Errorf("LastSeen went backwards: %v -> %v", before.LastSeen, after.LastSeen)
	}
	if !after.FirstSeen.Equal(before.FirstSeen) {
		t.Errorf("FirstSeen changed on update: %v -> %v", before.FirstSeen, after.FirstSeen)
	}
	// The returned session must not alias store state.
	got.PR.State = "CLOSED"
	if st.Snapshot()[0].PR.State != "MERGED" {
		t.Error("mutating the returned PR leaked into the store")
	}
}

func TestUpdateDiscardsWhenFnReturnsFalse(t *testing.T) {
	st := NewStore(t.TempDir())
	st.Upsert(Session{ID: "s", Project: "p", Issue: "ENG-1", Status: "dead"})
	before := st.Snapshot()[0]

	if _, ok := st.Update("s", func(sess *Session) bool {
		sess.Status = "working"
		return false
	}); !ok {
		t.Fatal("Update on a known ID must report ok")
	}
	after := st.Snapshot()[0]
	if after.Status != "dead" {
		t.Errorf("discarded mutation leaked: status = %q", after.Status)
	}
	if !after.LastSeen.Equal(before.LastSeen) {
		t.Errorf("discarded update must freeze LastSeen: %v -> %v", before.LastSeen, after.LastSeen)
	}
}

func TestUpdateUnknownID(t *testing.T) {
	st := NewStore(t.TempDir())
	called := false
	if _, ok := st.Update("ghost", func(*Session) bool { called = true; return true }); ok || called {
		t.Fatalf("Update on unknown ID: ok=%v called=%v, want false/false", ok, called)
	}
}

func TestPruneOlderThan(t *testing.T) {
	st := NewStore(t.TempDir())
	st.Upsert(Session{ID: "live", Project: "p", Issue: "ENG-1"})
	st.sessions["dead"] = Session{
		ID: "dead", Project: "p", Issue: "ENG-2",
		FirstSeen: time.Now().Add(-2 * time.Hour),
		LastSeen:  time.Now().Add(-time.Hour),
	}

	if n := st.PruneOlderThan(30 * time.Minute); n != 1 {
		t.Fatalf("PruneOlderThan removed %d, want 1", n)
	}
	snap := st.Snapshot()
	if len(snap) != 1 || snap[0].ID != "live" {
		t.Fatalf("after prune snapshot = %+v, want only live", snap)
	}
	if n := st.PruneOlderThan(30 * time.Minute); n != 0 {
		t.Fatalf("second prune removed %d, want 0", n)
	}
}

func TestCorruptFileTolerated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	st := NewStore(dir)
	if snap := st.Snapshot(); len(snap) != 0 {
		t.Fatalf("corrupt file loaded %d sessions, want 0", len(snap))
	}

	// The store must stay usable: upsert + save overwrite the corrupt file.
	st.Upsert(Session{ID: "s", Project: "p", Issue: "ENG-1"})
	if err := st.Save(); err != nil {
		t.Fatalf("Save after corrupt load: %v", err)
	}
	if snap := NewStore(dir).Snapshot(); len(snap) != 1 || snap[0].ID != "s" {
		t.Fatalf("reload after save = %+v, want the upserted session", snap)
	}
}

func TestMissingDirAndFileTolerated(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state", "does-not-exist-yet")

	st := NewStore(dir)
	if snap := st.Snapshot(); len(snap) != 0 {
		t.Fatalf("missing file loaded %d sessions, want 0", len(snap))
	}

	// Save must create parent directories itself.
	st.Upsert(Session{ID: "s", Project: "p", Issue: "ENG-1"})
	if err := st.Save(); err != nil {
		t.Fatalf("Save into missing dir: %v", err)
	}
}

func TestSaveFileMode(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	st.Upsert(Session{ID: "s", Project: "p", Issue: "ENG-1"})
	if err := st.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("sessions.json mode = %o, want 600", perm)
	}
}

func TestSnapshotDoesNotAliasPR(t *testing.T) {
	st := NewStore(t.TempDir())
	st.Upsert(Session{ID: "s", Project: "p", Issue: "ENG-1", PR: &scm.PR{Number: 1, State: "OPEN"}})

	snap := st.Snapshot()
	snap[0].PR.State = "MERGED"

	if got := st.Snapshot()[0].PR.State; got != "OPEN" {
		t.Fatalf("mutating a snapshot PR leaked into the store: State = %q", got)
	}
}
