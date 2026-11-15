package daemon

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshstore"
)

func pokeTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	return newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
}

// A poke site must be safe whatever the hub holds, and when no hub exists at all.
func TestPokeNoWatchersIsFree(t *testing.T) {
	d := pokeTestDaemon(t)
	for _, hub := range []*watchHub{d.watch, nil} {
		d.watch = hub
		d.pokeDiff("s1", protocol.DiffCauseEdit)
		d.talkReadAsync("s1")
		d.status.begin("p")
		d.status.end("p", time.Now())
		d.status.setError("p", "x")
		d.status.setLastSpawn("p", time.Now())
		d.pokeStatusOnChange()
		d.status.begin("p")
		if st, err := d.sshStore("s1", true); err != nil || st == nil {
			t.Fatalf("sshStore: %v %v", st, err)
		}
		d.ssh.mu.Lock()
		delete(d.ssh.stores, "s1")
		d.ssh.mu.Unlock()
	}
}

// takePokes returns what the hub recorded and clears it.
func takePokes(h *watchHub) (map[string]pokeBits, map[string]string, pokeBits) {
	h.pokeMu.Lock()
	defer h.pokeMu.Unlock()
	p, df, g := h.pokes, h.diffs, h.global
	h.pokes, h.diffs, h.global = map[string]pokeBits{}, map[string]string{}, 0
	return p, df, g
}

func TestPokeSitesReachTheHub(t *testing.T) {
	d := pokeTestDaemon(t)
	h := d.watch
	h.active.Store(1) // pretend something is watched
	defer h.active.Store(0)
	takePokes(h)

	d.pokeDiff("s1", protocol.DiffCauseRestore)
	if _, df, _ := takePokes(h); df["s1"] != protocol.DiffCauseRestore {
		t.Fatalf("diff poke = %v", df)
	}
	d.pokeStatusOnChange()
	d.status.begin("p")
	if _, _, g := takePokes(h); g&(pokeStatus|pokeProjects) != pokeStatus|pokeProjects {
		t.Fatalf("status change poked %b", g)
	}
	st, _ := d.sshStore("s2", true)
	takePokes(h)
	_ = st.Update(func(*sshstore.State) error { return nil })
	if p, _, _ := takePokes(h); p["s2"]&pokeTimeline == 0 {
		t.Fatalf("a store write did not poke the timeline: %v", p)
	}
}

func TestStatusTrackerNotifiesAfterUnlock(t *testing.T) {
	tr := newStatusTracker()
	var n atomic.Int32
	tr.setOnChange(func() {
		// The hook may read the tracker: the lock must be released.
		_ = tr.get("p")
		n.Add(1)
	})
	tr.begin("p")
	tr.end("p", time.Now())
	tr.setError("p", "boom")
	tr.setLastSpawn("p", time.Now())
	if n.Load() != 4 {
		t.Fatalf("onChange calls = %d, want 4", n.Load())
	}
	tr.setOnChange(nil)
	tr.begin("p")
	if n.Load() != 4 {
		t.Fatal("a cleared hook was called")
	}
	// bind never replaces a hook that is set.
	tr.bindOnChange(func() { n.Add(100) })
	tr.bindOnChange(func() { n.Add(1000) })
	tr.begin("p")
	if n.Load() != 104 {
		t.Fatalf("bind kept the wrong hook: %d", n.Load())
	}
}

func TestTimelineNotifyFromAnyWriter(t *testing.T) {
	d := pokeTestDaemon(t)
	st, err := d.sshStore("s1", true)
	if err != nil || st == nil {
		t.Fatalf("sshStore: %v", err)
	}
	// Every writer shares the one seam; none may panic or deadlock with the hub in place.
	st.Volatile(func(*sshstore.State) bool { return true })
	if err := st.Update(func(*sshstore.State) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.sshStore("s1", false); got != st {
		t.Fatal("store not cached")
	}
}

func TestDiffPokes(t *testing.T) {
	d := pokeTestDaemon(t)
	// The sites exist on the stub-free path too: an unknown session is a clean return.
	d.recordCheckpointAt(context.Background(), "nope", "stop", time.Now())
	if _, err := d.handleRestoreCheckpoint(context.Background(), protocol.CheckpointArgs{Session: "nope", Seq: 1}); err == nil {
		t.Fatal("restore of an unknown session must fail")
	}
	d.pokeDiff("s", protocol.DiffCauseCheckpoint)
	d.pokeDiff("s", protocol.DiffCauseRestore)
}

func TestFeedPinnedNotEvicted(t *testing.T) {
	d := pokeTestDaemon(t)
	h := d.feedHub()
	h.pin("pinned")
	h.entry(d, feedTarget{id: "pinned"})
	for i := 0; i < feedSessions+3; i++ {
		h.entry(d, feedTarget{id: "s" + string(rune('a'+i))})
	}
	h.mu.Lock()
	_, kept := h.entries["pinned"]
	h.mu.Unlock()
	if !kept {
		t.Fatal("a pinned reader was evicted")
	}
	h.unpin("pinned")
	h.unpin("pinned") // never below zero
	h.mu.Lock()
	n := len(h.pins)
	h.mu.Unlock()
	if n != 0 {
		t.Fatalf("pins left: %d", n)
	}
	for i := 0; i < feedSessions+2; i++ {
		h.entry(d, feedTarget{id: "t" + string(rune('a'+i))})
	}
	h.mu.Lock()
	_, kept = h.entries["pinned"]
	size := len(h.entries)
	h.mu.Unlock()
	if kept || size > feedSessions {
		t.Fatalf("unpinned reader should age out: kept=%v size=%d", kept, size)
	}
}

func TestFeedPinSurvivesAgentSwitch(t *testing.T) {
	d := pokeTestDaemon(t)
	h := d.feedHub()
	h.pin("s")
	first := h.entry(d, feedTarget{id: "s", slug: "a"})
	second := h.entry(d, feedTarget{id: "s", slug: "b"}) // the agent moved: a new entry
	if first == second {
		t.Fatal("expected a replaced entry")
	}
	for i := 0; i < feedSessions+3; i++ {
		h.entry(d, feedTarget{id: "x" + string(rune('a'+i))})
	}
	h.mu.Lock()
	e := h.entries["s"]
	h.mu.Unlock()
	if e != second {
		t.Fatal("the replacement lost its pin")
	}
}

func TestTalkReadErrFollowsTheLastRead(t *testing.T) {
	d := pokeTestDaemon(t)
	s := session.Session{ID: "p-1", Agent: "claude", Project: "p", Worktree: t.TempDir()}
	fail := true
	d.paneVisible = func(context.Context, string) (string, error) {
		if fail {
			return "", errors.New("no pane")
		}
		return "", nil
	}
	if d.talkReadErr(s.ID) {
		t.Fatal("no read yet")
	}
	d.talkPaneState(context.Background(), s, false)
	if !d.talkReadErr(s.ID) {
		t.Fatal("a failed read must be reported")
	}
	fail = false
	d.talkPaneState(context.Background(), s, false)
	if d.talkReadErr(s.ID) {
		t.Fatal("a successful read must clear it")
	}
}
