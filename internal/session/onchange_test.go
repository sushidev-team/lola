package session

import (
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/scm"
)

type rec struct {
	id         string
	prev, next *Session
}

func watch(st *Store) *[]rec {
	var got []rec
	st.OnChange(func(c Change) {
		r := rec{id: c.ID}
		if c.Prev != nil {
			p := *c.Prev
			r.prev = &p
		}
		if c.Next != nil {
			n := *c.Next
			r.next = &n
		}
		got = append(got, r)
	})
	return &got
}

func TestOnChangeFiresForEveryCommit(t *testing.T) {
	st := NewStore(t.TempDir())
	got := watch(st)

	// Upsert insert, then Upsert replace.
	st.Upsert(Session{ID: "a", Branch: "b1"})
	if len(*got) != 1 || (*got)[0].prev != nil || (*got)[0].next == nil || (*got)[0].next.Branch != "b1" {
		t.Fatalf("insert: %+v", *got)
	}
	st.Upsert(Session{ID: "a", Branch: "b2", PR: &scm.PR{Number: 7}})
	c := (*got)[1]
	if len(*got) != 2 || c.prev == nil || c.prev.Branch != "b1" || c.next.Branch != "b2" || c.next.PR == nil || c.next.PR.Number != 7 {
		t.Fatalf("replace: %+v", *got)
	}

	// Update true fires; fn=false and a missing id do not.
	st.Update("a", func(s *Session) bool { s.Branch = "b3"; return true })
	if len(*got) != 3 || (*got)[2].prev.Branch != "b2" || (*got)[2].next.Branch != "b3" {
		t.Fatalf("update true: %+v", *got)
	}
	st.Update("a", func(s *Session) bool { s.Branch = "zzz"; return false })
	st.Update("missing", func(s *Session) bool { return true })
	if len(*got) != 3 {
		t.Fatalf("no-op update fired: %d", len(*got))
	}

	// Apply: insert, update, declined.
	st.Apply("n", func(s *Session, exists bool) bool { s.Branch = "x"; return true })
	if len(*got) != 4 || (*got)[3].prev != nil || (*got)[3].next.Branch != "x" {
		t.Fatalf("apply insert: %+v", *got)
	}
	st.Apply("n", func(s *Session, exists bool) bool { s.Branch = "y"; return true })
	if len(*got) != 5 || (*got)[4].prev.Branch != "x" || (*got)[4].next.Branch != "y" {
		t.Fatalf("apply update: %+v", *got)
	}
	st.Apply("n", func(s *Session, exists bool) bool { return false })
	st.Apply("", func(s *Session, exists bool) bool { return true })
	if len(*got) != 5 {
		t.Fatalf("declined apply fired: %d", len(*got))
	}

	// Delete, and Delete of a missing id.
	if !st.Delete("n") {
		t.Fatal("delete")
	}
	if len(*got) != 6 || (*got)[5].prev == nil || (*got)[5].next != nil || (*got)[5].id != "n" {
		t.Fatalf("delete: %+v", *got)
	}
	st.Delete("n")
	if len(*got) != 6 {
		t.Fatalf("delete missing fired")
	}

	// Lane write reports the base id.
	st.Upsert(Session{ID: "s", Stack: true, Apps: []App{{Name: "web"}}})
	n := len(*got)
	st.Update("s~web", func(v *Session) bool { v.Branch = "lane"; return true })
	if len(*got) != n+1 || (*got)[n].id != "s" || (*got)[n].prev == nil || (*got)[n].next == nil {
		t.Fatalf("lane: %+v", (*got)[n:])
	}

	// PruneOlderThan: one event per dropped id.
	st.Delete("s")
	st.Upsert(Session{ID: "old"})
	time.Sleep(5 * time.Millisecond)
	n = len(*got)
	if st.PruneOlderThan(time.Millisecond) == 0 {
		t.Fatal("nothing pruned")
	}
	pruned := (*got)[n:]
	if len(pruned) == 0 || pruned[0].next != nil || pruned[0].prev == nil {
		t.Fatalf("prune: %+v", pruned)
	}

	// nil clears.
	st.OnChange(nil)
	st.Upsert(Session{ID: "q"})
}
