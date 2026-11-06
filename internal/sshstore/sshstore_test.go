package sshstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/sshelement"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	home := t.TempDir()
	s, err := Open(home, "lola-x-1", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, home
}

func TestFilesAreOwnerOnlyAndReload(t *testing.T) {
	s, home := open(t)
	if err := s.Update(func(st *State) error {
		st.AddBlock(protocol.SSHBlock{ID: st.NewBlockID(), Command: "uptime", Status: protocol.SSHBlockQueued}, time.Now())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(s.Dir(), "state.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("state.json mode: %v %v", fi.Mode(), err)
	}
	di, _ := os.Stat(s.Dir())
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode: %v", di.Mode())
	}
	again, err := Open(home, "lola-x-1", false, nil)
	if err != nil || again == nil {
		t.Fatal(err)
	}
	again.View(func(st *State) {
		if b := st.Block("b1"); b == nil || b.Command != "uptime" {
			t.Fatalf("not reloaded: %+v", st.Items)
		}
	})
}

func TestOpenRefusesPathShapedSession(t *testing.T) {
	for _, id := range []string{"", ".", "..", "../x", "a/b", ".hidden"} {
		if _, err := Open(t.TempDir(), id, true, nil); err == nil {
			t.Errorf("%q accepted", id)
		}
	}
}

func TestOpenCorruptIsSetAsideNeverOverwritten(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(Root(home), "s1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(home, "s1", false, nil); err != nil {
		t.Fatal(err)
	}
	m, _ := filepath.Glob(filepath.Join(dir, "state.json.corrupt-*"))
	if len(m) != 1 {
		t.Fatalf("corrupt file not kept: %v", m)
	}
}

func TestOrderFixedSeqMoves(t *testing.T) {
	s, _ := open(t)
	_ = s.Update(func(st *State) error {
		st.AddBlock(protocol.SSHBlock{ID: st.NewBlockID(), Status: protocol.SSHBlockQueued}, time.Now())
		st.AddEvent(protocol.SSHSystemEvent{Kind: "go", Text: "x"}, time.Now())
		return nil
	})
	var seq int64
	s.View(func(st *State) { seq = st.Seq })
	_ = s.Update(func(st *State) error {
		it := st.Item("b1")
		it.Block.Status = protocol.SSHBlockRunning
		st.Touch(it)
		return nil
	})
	tl := s.Timeline("", seq, 0)
	if len(tl.Items) != 1 || tl.Items[0].ID != "b1" || tl.Items[0].Order != 1 || tl.Items[0].Seq <= seq {
		t.Fatalf("upsert view wrong: %+v", tl.Items)
	}
	tl = s.Timeline("", 0, 1)
	if !tl.More || len(tl.Items) != 1 || tl.LastSeq != tl.Items[0].Seq {
		t.Fatalf("paging wrong: %+v", tl)
	}
}

func TestSinkSanitizesCapsKeepsTailAndDrains(t *testing.T) {
	s, _ := open(t)
	k := s.NewSink("b1", protocol.SSHStreamStdout, 10)
	_, _ = k.Write([]byte("ab\x1b[31mcd\x1b[0m\r\n"))
	big := strings.Repeat("x", 2*TailBytes)
	_, _ = k.Write([]byte(big))
	_, _ = k.Write([]byte("END"))
	k.Close()
	head, total, err := s.ReadRange("b1", "stdout", 0, 0)
	if err != nil || total != 10 {
		t.Fatalf("total=%d err=%v", total, err)
	}
	if !strings.HasPrefix(string(head), "abcd\n") || strings.Contains(string(head), "\x1b") {
		t.Fatalf("head not sanitized: %q", head)
	}
	tail, _ := s.ReadTail("b1", "stdout")
	if len(tail) != TailBytes || !strings.HasSuffix(tail, "END") {
		t.Fatalf("tail len=%d", len(tail))
	}
	st := k.Stats()
	if st.Dropped == 0 || st.Stored != 10 {
		t.Fatalf("stats: %+v", st)
	}
	if fi, _ := os.Stat(outPath(s.Dir(), "b1", "stdout")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("output mode %v", fi.Mode())
	}
}

func TestSinkNeverSplitsARune(t *testing.T) {
	s, _ := open(t)
	k := s.NewSink("b1", protocol.SSHStreamStdout, 4)
	_, _ = k.Write([]byte("aéééé"))
	k.Close()
	head, _, _ := s.ReadRange("b1", "stdout", 0, 0)
	if !strings.HasPrefix("aéééé", string(head)) || string(head) == "" {
		t.Fatalf("head = %q", head)
	}
	if st := string(head); strings.ContainsRune(st, '�') {
		t.Fatalf("split rune: %q", st)
	}
}

func TestBinaryFlag(t *testing.T) {
	s, _ := open(t)
	k := s.NewSink("b1", protocol.SSHStreamStderr, 1024)
	_, _ = k.Write([]byte{'a', 0, 'b'})
	k.Close()
	if !k.Stats().Binary {
		t.Fatal("NUL did not set Binary")
	}
}

func TestReadPollAddsTailOnceTerminal(t *testing.T) {
	s, _ := open(t)
	k := s.NewSink("b1", protocol.SSHStreamStdout, 4)
	_, _ = k.Write([]byte("headMIDDLETAIL"))
	k.Close()
	data, next, end, err := s.ReadPoll("b1", "stdout", 0, 0, 3, true)
	if err != nil || string(data) != "head" || next != 4 {
		t.Fatalf("%q %d %v", data, next, err)
	}
	var all strings.Builder
	all.Write(data)
	for next < end {
		data, next, end, err = s.ReadPoll("b1", "stdout", next, 0, 3, true)
		if err != nil || len(data) == 0 {
			t.Fatalf("stuck at %d/%d: %v", next, end, err)
		}
		all.Write(data)
	}
	if !strings.Contains(all.String(), "dropped") || !strings.HasSuffix(all.String(), "TAIL") {
		t.Fatalf("view = %q", all.String())
	}
	// While running there is no tail in the view.
	_, _, end, _ = s.ReadPoll("b1", "stdout", 0, 0, 0, false)
	if end != 4 {
		t.Fatalf("running end = %d", end)
	}
}

func TestPruneBlocksEventsElementsAndOutput(t *testing.T) {
	s, _ := open(t)
	_ = s.Update(func(st *State) error {
		for i := 0; i < MaxBlocks+5; i++ {
			st.AddBlock(protocol.SSHBlock{ID: st.NewBlockID(), Status: protocol.SSHBlockSucceeded}, time.Now())
		}
		for i := 0; i < MaxEvents+3; i++ {
			st.AddEvent(protocol.SSHSystemEvent{Kind: "go"}, time.Now())
		}
		st.PutElement(protocol.SSHElement{ID: st.NewElementID(), Kind: "question", Title: "keep"}, time.Now())
		for i := 0; i < sshelement.MaxElements+2; i++ {
			st.PutElement(protocol.SSHElement{ID: st.NewElementID(), Kind: "narration"}, time.Now())
		}
		s.Prune(st)
		var b, e, el int
		for _, it := range st.Items {
			switch it.Kind {
			case protocol.SSHItemBlock:
				b++
			case protocol.SSHItemSystem:
				e++
			case protocol.SSHItemElement:
				el++
			}
		}
		if b != MaxBlocks || e != MaxEvents || el != sshelement.MaxElements {
			t.Fatalf("b=%d e=%d el=%d", b, e, el)
		}
		if st.Block("b1") != nil {
			t.Fatal("oldest block kept")
		}
		if st.Item("e1") == nil {
			t.Fatal("an open question was pruned before closed elements")
		}
		return nil
	})
}

func TestPruneOutputKeepsTheRow(t *testing.T) {
	s, _ := open(t)
	k := s.NewSink("b1", "stdout", 100)
	_, _ = k.Write([]byte("x"))
	k.Close()
	_ = s.Update(func(st *State) error {
		st.AddBlock(protocol.SSHBlock{ID: st.NewBlockID(), Status: protocol.SSHBlockSucceeded, StdoutBytes: MaxOutputBytes + 1}, time.Now())
		st.OutputBytes = MaxOutputBytes + 1
		s.Prune(st)
		b := st.Block("b1")
		if b == nil || !b.OutputPruned {
			t.Fatalf("row: %+v", b)
		}
		return nil
	})
	if _, err := os.Stat(outPath(s.Dir(), "b1", "stdout")); err == nil {
		t.Fatal("output still on disk")
	}
}

func TestQueueDeliverAndClaim(t *testing.T) {
	s, _ := open(t)
	_ = s.Update(func(st *State) error {
		it := st.AddHuman(protocol.SSHHumanMessage{Origin: "say", Text: "hi"}, time.Now())
		st.Enqueue(it.ID, "[lola] hi")
		st.Enqueue("", "[lola] nudge")
		if st.PendingText() != "[lola] hi\n\n[lola] nudge" {
			t.Fatalf("join: %q", st.PendingText())
		}
		st.Queue[0].Claimed = true
		if st.PendingText() != "[lola] nudge" {
			t.Fatalf("claimed still pending: %q", st.PendingText())
		}
		got := st.Deliver(protocol.SSHViaReply, time.Now(), func(q QueueEntry) bool { return !q.Claimed })
		if len(got) != 1 || got[0].Text != "[lola] nudge" {
			t.Fatalf("delivered %v", got)
		}
		got = st.Deliver(protocol.SSHViaPrompt, time.Now(), func(q QueueEntry) bool { return q.Claimed })
		if len(got) != 1 || st.Item("h1").Human.Via != protocol.SSHViaPrompt || !st.Item("h1").Human.Delivered {
			t.Fatalf("item: %+v", st.Item("h1").Human)
		}
		return nil
	})
}

func TestConnForHostNewestAndClosedIsNone(t *testing.T) {
	var st State
	a := &Conn{}
	a.ID, a.Host, a.State = "a", "h", protocol.SSHConnConnected
	b := &Conn{}
	b.ID, b.Host, b.State = "b", "h", protocol.SSHConnClosed
	st.Conns = []*Conn{a}
	if st.ConnForHost("h") != a {
		t.Fatal("live conn not found")
	}
	st.Conns = []*Conn{a, b}
	if st.ConnForHost("h") != a {
		t.Fatal("a closed tab must not hide a live one on the same host")
	}
	st.Conns = []*Conn{b}
	if st.ConnForHost("h") != nil {
		t.Fatal("closed only should read as none")
	}
}

func TestConnForHostPrefersTheTabTalkedTo(t *testing.T) {
	var st State
	a, b := &Conn{}, &Conn{}
	a.ID, a.Host, a.State = "a", "h", protocol.SSHConnConnected
	b.ID, b.Host, b.State = "b", "h", protocol.SSHConnConnected
	st.Conns = []*Conn{a, b}
	if st.ConnForHost("h") != b {
		t.Fatal("newest should win with no talk")
	}
	st.TalkConn = "a"
	if st.ConnForHost("h") != a {
		t.Fatal("the tab talked to should win")
	}
	a.State = protocol.SSHConnClosed
	if st.ConnForHost("h") != b {
		t.Fatal("a closed tab cannot be the answer")
	}
}

func TestTrashRemovesOneTabsRecord(t *testing.T) {
	home := t.TempDir()
	s, err := Open(home, "s1", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, b := &Conn{}, &Conn{}
	a.ID, b.ID = "a", "b"
	err = s.Update(func(st *State) error {
		st.Conns = []*Conn{a, b}
		st.AddBlock(protocol.SSHBlock{ID: "b1", Conn: "a", StdoutBytes: 5}, time.Now())
		st.AddBlock(protocol.SSHBlock{ID: "b2", Conn: "b", StdoutBytes: 7}, time.Now())
		st.AddEvent(protocol.SSHSystemEvent{Text: "everywhere"}, time.Now())
		st.AddEvent(protocol.SSHSystemEvent{Text: "on a", Conn: "a"}, time.Now())
		back := st.AddHuman(protocol.SSHHumanMessage{Origin: protocol.SSHHumanHandback, Conn: "a", Text: "over to you"}, time.Now())
		st.Enqueue(back.ID, "[lola] handback")
		st.Enqueue("", "nudge")
		st.OutputBytes, st.TalkConn = 12, "a"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(st *State) error { s.Trash(st, "a"); return nil }); err != nil {
		t.Fatal(err)
	}
	s.View(func(st *State) {
		if st.Conn("a") != nil || st.Conn("b") == nil {
			t.Fatal("wrong connection went")
		}
		if st.Item("b1") != nil || st.Item("b2") == nil {
			t.Fatal("wrong block went")
		}
		if len(st.Items) != 2 {
			t.Fatalf("the tab's event and handback should go and the session-wide event stay, got %d items", len(st.Items))
		}
		if len(st.Queue) != 1 || st.Queue[0].Text != "nudge" {
			t.Fatalf("queue: %+v", st.Queue)
		}
		if st.OutputBytes != 7 || st.TalkConn != "" {
			t.Fatalf("bytes %d talk %q", st.OutputBytes, st.TalkConn)
		}
	})
}
