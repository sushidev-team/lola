package sshstore

import (
	"strconv"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

func openTalk(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir(), "s1", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNextBatchJoinsUntilCommand(t *testing.T) {
	var st State
	st.EnqueueEntry(QueueEntry{Text: "one", Human: true})
	st.EnqueueEntry(QueueEntry{Text: "two", Human: true})
	st.EnqueueEntry(QueueEntry{Text: "/compact", Human: true, Command: true})
	st.EnqueueEntry(QueueEntry{Text: "three", Human: true})

	text, ids := st.NextBatch()
	if text != "one\n\ntwo" || len(ids) != 2 {
		t.Fatalf("head batch: %q %v", text, ids)
	}
	if st.PendingText() != text {
		t.Fatalf("PendingText must be exactly the head batch, got %q", st.PendingText())
	}
	st.ClaimBatch(text)
	st.Deliver(protocol.SSHViaPrompt, time.Now(), func(q QueueEntry) bool { return q.Claimed })

	text, ids = st.NextBatch()
	if text != "/compact" || len(ids) != 1 {
		t.Fatalf("a command goes alone: %q %v", text, ids)
	}
	st.ClaimBatch(text)
	st.Deliver(protocol.SSHViaPrompt, time.Now(), func(q QueueEntry) bool { return q.Claimed })

	if text, _ = st.NextBatch(); text != "three" {
		t.Fatalf("what is behind the command waits its turn: %q", text)
	}
}

func TestClaimIsNotSubstring(t *testing.T) {
	var st State
	st.EnqueueEntry(QueueEntry{Item: "h1", Text: "yes", Human: true})
	st.EnqueueEntry(QueueEntry{Item: "h2", Text: "yes please", Human: true})

	items := st.ClaimBatch("yes")
	if len(items) != 1 || items[0] != "h1" {
		t.Fatalf("claimed %v", items)
	}
	if !st.Queue[0].Claimed || st.Queue[1].Claimed {
		t.Fatalf("only what was typed may be claimed: %+v", st.Queue)
	}
}

func TestClaimOfTextThatIsNotTheHeadClaimsNothing(t *testing.T) {
	var st State
	st.EnqueueEntry(QueueEntry{Text: "first"})
	st.EnqueueEntry(QueueEntry{Text: "second"})
	if got := st.ClaimBatch("second"); got != nil || st.Queue[0].Claimed || st.Queue[1].Claimed {
		t.Fatalf("claimed %v, queue %+v", got, st.Queue)
	}
	if got := st.ClaimBatch("first\n\nsecond and more"); got != nil || st.Queue[0].Claimed {
		t.Fatalf("claimed %v, queue %+v", got, st.Queue)
	}
}

func TestClaimWalksTheWholeJoin(t *testing.T) {
	var st State
	st.EnqueueEntry(QueueEntry{Item: "h1", Text: "a"})
	st.EnqueueEntry(QueueEntry{Text: "[lola] nudge"})
	st.EnqueueEntry(QueueEntry{Item: "h2", Text: "b"})
	items := st.ClaimBatch("a\n\n[lola] nudge\n\nb")
	if len(items) != 2 || items[0] != "h1" || items[1] != "h2" {
		t.Fatalf("items %v", items)
	}
}

func TestTrashKeepsHumanWords(t *testing.T) {
	s := openTalk(t)
	err := s.Update(func(st *State) error {
		a := &Conn{}
		a.ID = "a"
		st.Conns = []*Conn{a}
		h := st.AddHuman(protocol.SSHHumanMessage{Origin: protocol.SSHHumanSay, Conn: "a", Text: "look at the logs"}, time.Now())
		st.EnqueueEntry(QueueEntry{Item: h.ID, Text: "look at the logs", Human: true})
		st.AddBlock(protocol.SSHBlock{ID: "b1", Conn: "a"}, time.Now())
		s.Trash(st, "a")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.View(func(st *State) {
		it := st.Item("h1")
		if it == nil || it.Human.Text != "look at the logs" {
			t.Fatalf("a human's words must stay: %+v", it)
		}
		if it.Human.Conn != "" {
			t.Fatalf("the label should go with the tab, got %q", it.Human.Conn)
		}
		if st.Item("b1") != nil {
			t.Fatal("the tab's block should go")
		}
		if len(st.Queue) != 1 {
			t.Fatalf("a queued message is still going to be delivered: %+v", st.Queue)
		}
	})
}

func TestLastTrashKeepsSessionItems(t *testing.T) {
	s := openTalk(t)
	err := s.Update(func(st *State) error {
		a := &Conn{}
		a.ID = "a"
		st.Conns = []*Conn{a}
		st.AddEvent(protocol.SSHSystemEvent{Kind: protocol.SSHEventSessionStarted, Text: "started"}, time.Now())
		st.AddHuman(protocol.SSHHumanMessage{Origin: protocol.SSHHumanInitial, Text: "fix it", Delivered: true}, time.Now())
		s.Trash(st, "a")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.View(func(st *State) {
		if len(st.Conns) != 0 || len(st.Items) != 2 {
			t.Fatalf("conns %d items %d: the last tab no longer wipes the story", len(st.Conns), len(st.Items))
		}
	})
}

func TestPruneNeverDropsQueued(t *testing.T) {
	s := openTalk(t)
	err := s.Update(func(st *State) error {
		// The oldest is still waiting, and the next is delivered.
		waiting := st.AddHuman(protocol.SSHHumanMessage{Text: "still waiting"}, time.Now())
		st.EnqueueEntry(QueueEntry{Item: waiting.ID, Text: "still waiting", Human: true})
		for i := range MaxHuman + 5 {
			st.AddHuman(protocol.SSHHumanMessage{Text: "m" + strconv.Itoa(i), Delivered: true}, time.Now())
		}
		s.Prune(st)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.View(func(st *State) {
		if st.Item(firstHuman) == nil {
			t.Fatal("a queued message was pruned")
		}
		humans := 0
		for _, it := range st.Items {
			if it.Human != nil {
				humans++
			}
		}
		if humans != MaxHuman {
			t.Fatalf("humans = %d, want %d", humans, MaxHuman)
		}
		if st.Item("h2") != nil {
			t.Fatal("the oldest delivered message should have gone first")
		}
	})
}

const firstHuman = "h1"

func TestPruneKeepsUndeliveredWithNoQueueEntry(t *testing.T) {
	s := openTalk(t)
	_ = s.Update(func(st *State) error {
		st.AddHuman(protocol.SSHHumanMessage{Text: "neither delivered nor recalled"}, time.Now())
		for range MaxHuman + 1 {
			st.AddHuman(protocol.SSHHumanMessage{Text: "x", Recalled: true}, time.Now())
		}
		s.Prune(st)
		return nil
	})
	s.View(func(st *State) {
		if st.Item(firstHuman) == nil {
			t.Fatal("only delivered or recalled messages may be pruned")
		}
	})
}

func TestNoteSentKeepsTheNewest(t *testing.T) {
	var st State
	for i := range MaxSent + 3 {
		st.NoteSent(SentBatch{Hash: strconv.Itoa(i)})
	}
	if len(st.Sent) != MaxSent || st.Sent[0].Hash != "3" {
		t.Fatalf("sent: %d, first %q", len(st.Sent), st.Sent[0].Hash)
	}
}

func TestReadEndedWithoutOpeningTheStore(t *testing.T) {
	home := t.TempDir()
	s, err := Open(home, "s1", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ReadEnded(home, "s1") != nil {
		t.Fatal("a live store has no ended record")
	}
	err = s.Update(func(st *State) error {
		st.Ended = &EndedInfo{EndedSession: protocol.EndedSession{ID: "s1", Reason: "PR #1 merged"}, Slug: "x"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	got := ReadEnded(home, "s1")
	if got == nil || got.Reason != "PR #1 merged" || got.Slug != "x" {
		t.Fatalf("ended: %+v", got)
	}
	if ReadEnded(home, "../etc") != nil {
		t.Fatal("an invalid id must read as nothing")
	}
}
