package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/attention"
	"github.com/sushidev-team/lola/internal/eventbox"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/state"
)

// Tests of the watch hub against the REAL producers' inputs: the talk probe is
// handleTalkState over a faked pane, the timeline is handleSessionTimeline over
// a real ssh store. (watch_test.go fakes both seams.)

// manualHub puts d's hub on a fake clock the test steps by hand.
func manualHub(t *testing.T, d *Daemon) (*wtest, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	d.watch.now = func() time.Time { return now }
	return &wtest{t: t, d: d, h: d.watch, ctx: context.Background()}, &now
}

// drain returns every event already in b.
func drain(t *testing.T, b *eventbox.Box) []eventbox.Item {
	t.Helper()
	var out []eventbox.Item
	for {
		it, ok := next(t, b, 30*time.Millisecond)
		if !ok {
			return out
		}
		out = append(out, it)
	}
}

// forbidTyping makes every seam that puts a byte into a pane a test failure.
// It counts rather than panics: the hub recovers a producer's panic, which
// would hide one.
func forbidTyping(t *testing.T, d *Daemon) *atomic.Int32 {
	t.Helper()
	var typed atomic.Int32
	hit := func(what string) {
		typed.Add(1)
		t.Errorf("a watched probe reached %s", what)
	}
	d.sendKeys = func(context.Context, string, string) error { hit("sendKeys"); return nil }
	d.sendKeysTyped = func(context.Context, string, string) error { hit("sendKeysTyped"); return nil }
	d.sendEscape = func(context.Context, string) error { hit("sendEscape"); return nil }
	d.sendKey = func(context.Context, string, string) error { hit("sendKey"); return nil }
	d.sendBackground = func(context.Context, string) error { hit("sendBackground"); return nil }
	return &typed
}

func TestWatchedProbeNeverTypes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		agent state.AgentState
		setup func(d *Daemon, f *talkPaneFake)
		want  string
	}{
		{"resting prompt", state.AgentIdle, func(_ *Daemon, f *talkPaneFake) { f.show(talkResting) }, protocol.ComposerResting},
		{"draft", state.AgentIdle, func(_ *Daemon, f *talkPaneFake) { f.show(talkDraft) }, protocol.ComposerDraft},
		{"question", state.AgentWaitingInput, func(d *Daemon, f *talkPaneFake) {
			d.readPrompt = func(string, attention.Cursor, agent.Kind) (attention.Prompt, bool) {
				return attention.Prompt{Kind: attention.PromptPermission, Hash: "h", Lines: []string{"Allow?"}, Choices: []attention.Choice{{Key: "1", Label: "Yes"}}}, true
			}
			f.hideCursor()
		}, protocol.ComposerPrompt},
		{"modal", state.AgentIdle, func(d *Daemon, f *talkPaneFake) {
			d.readPrompt = func(string, attention.Cursor, agent.Kind) (attention.Prompt, bool) {
				return attention.Prompt{Kind: attention.PromptModal, Hash: "m"}, true
			}
			f.hideCursor()
		}, protocol.ComposerModal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, f := talkFakeDaemon(t)
			talkSession(d, "s1", tc.agent)
			tc.setup(d, f)
			typed := forbidTyping(t, d)
			w, now := manualHub(t, d)
			b := w.box()
			stop, res := w.watch(b, "talk:s1", "w")
			if !res.OK {
				t.Fatalf("refused: %+v", res)
			}
			defer stop()
			var last *protocol.TalkEvent
			for i := 0; i < 5; i++ {
				w.step()
				for _, it := range drain(t, b) {
					last = it.Event.(*protocol.TalkEvent)
				}
				*now = now.Add(talkProbeResting)
			}
			if last == nil || last.Composer != tc.want {
				t.Fatalf("composer %+v, want %s", last, tc.want)
			}
			if typed.Load() != 0 {
				t.Fatalf("%d typing calls", typed.Load())
			}
		})
	}
}

func TestWatchedProbeNeedsPaneProofForAtPrompt(t *testing.T) {
	for _, pane := range []string{talkWorking, ""} {
		d, f := talkFakeDaemon(t)
		talkSession(d, "s1", state.AgentWorking)
		f.show(pane)
		forbidTyping(t, d)
		w, now := manualHub(t, d)
		b := w.box()
		stop, res := w.watch(b, "talk:s1", "w")
		if !res.OK {
			t.Fatalf("refused: %+v", res)
		}
		for i := 0; i < 10; i++ {
			w.step()
			drain(t, b)
			if s, _ := d.sessions.Get("s1"); s.AtPrompt {
				t.Fatalf("pane %q: probe %d set AtPrompt without ActivityWaiting", pane, i+1)
			}
			*now = now.Add(talkProbeResting)
		}
		stop()
	}
}

func TestFeedResetOnEpochChange(t *testing.T) {
	w, now := newManualWatchTest(t)
	w.session("s1")
	f := &fakeFeed{epoch: "e1"}
	f.add(1, "a")
	f.add(2, "b")
	f.install(w.h)
	b := w.box()
	stop, _ := w.watch(b, "feed:s1", "w")
	defer stop()
	w.step()
	if ev := mustNext(t, b).Event.(*protocol.FeedEvent); !ev.Reset || ev.Epoch != "e1" {
		t.Fatalf("snapshot %+v", ev)
	}
	// A new reader (restart, agent switch, rotation) restarts its sequence; a
	// delta continuing from the old cursor would skip everything below it.
	f.mu.Lock()
	f.epoch, f.items, f.lastSeq = "e2", nil, 0
	f.mu.Unlock()
	f.add(1, "c")
	*now = now.Add(time.Second)
	w.h.PokeSession("s1", pokeFeed)
	w.step()
	ev := mustNext(t, b).Event.(*protocol.FeedEvent)
	if !ev.Reset || ev.Epoch != "e2" || len(ev.Items) != 1 || ev.Items[0].ID != "c" {
		t.Fatalf("an epoch change must be a full reset: %+v", ev)
	}
}

func TestFeedDeltaAfterSnapshotNoLoss(t *testing.T) {
	w, now := newManualWatchTest(t)
	w.session("s1")
	f := &fakeFeed{epoch: "e"}
	f.add(1, "a")
	f.install(w.h)
	// The reader advances while the snapshot is being taken: after the
	// producer read LastSeq (1) and before Page answered.
	open := w.h.openFeed
	advanced := false
	w.h.openFeed = func(ctx context.Context, id string) (feedView, feedAvail) {
		v, a := open(ctx, id)
		page := v.Page
		v.Page = func(ctx context.Context, args protocol.AgentFeedArgs) protocol.AgentFeedData {
			if !advanced && args.After == 0 {
				advanced = true
				f.add(2, "b")
			}
			return page(ctx, args)
		}
		return v, a
	}
	b := w.box()
	stop, _ := w.watch(b, "feed:s1", "w")
	defer stop()
	w.step()
	snap := mustNext(t, b).Event.(*protocol.FeedEvent)
	if !snap.Reset || snap.LastSeq != 2 || len(snap.Items) != 2 {
		t.Fatalf("snapshot %+v", snap)
	}
	// Then it advances between the snapshot and the next publish.
	f.add(3, "c")
	*now = now.Add(time.Second)
	w.h.PokeSession("s1", pokeFeed)
	w.step()
	seen := map[string]int{}
	for _, it := range snap.Items {
		seen[it.ID]++
	}
	for _, it := range drain(t, b) {
		ev := it.Event.(*protocol.FeedEvent)
		if ev.Reset {
			t.Fatalf("an advance in the same epoch is a delta: %+v", ev)
		}
		for _, x := range ev.Items {
			seen[x.ID]++
		}
	}
	for _, id := range []string{"a", "b", "c"} {
		if seen[id] != 1 {
			t.Fatalf("item %s delivered %d times, want exactly once (%v)", id, seen[id], seen)
		}
	}
}

func TestTimelineRedacted(t *testing.T) {
	r, conn, tap, fp := promptRig(t)
	// A pending prompt card on the connection.
	tap.start("sudo ls")
	tap.output([]byte("[sudo] password for m: "))
	tap.promptRead()
	fp.show("$ sudo ls\n[sudo] password for m: ", 3)
	if r.wirePrompt(t, conn) == nil {
		t.Fatal("fixture: no prompt card")
	}
	exit := 0
	seedTimeline(t, r,
		protocol.SSHTimelineItem{ID: "bp", Kind: protocol.SSHItemBlock, Block: &protocol.SSHBlock{ID: "bp", Conn: conn, Command: "cat PRIVATECMD", Private: true}},
		protocol.SSHTimelineItem{ID: "bs", Kind: protocol.SSHItemBlock, Block: &protocol.SSHBlock{ID: "bs", Conn: conn, Command: "ls", Host: "HOSTNAME", User: "USERNAME", Cwd: "/CWDPATH", ExitCode: &exit}},
		protocol.SSHTimelineItem{ID: "h", Kind: protocol.SSHItemHuman, Human: &protocol.SSHHumanMessage{Text: "hi", Transcript: "HANDBACKTRANSCRIPT"}},
	)
	// Filler over the snapshot window, so the snapshot arrives in parts.
	var filler []protocol.SSHTimelineItem
	for i := 0; i < 12; i++ {
		filler = append(filler, protocol.SSHTimelineItem{ID: "f" + string(rune('a'+i)), Kind: protocol.SSHItemSystem, System: &protocol.SSHSystemEvent{Kind: "note", Text: strings.Repeat("y", 200<<10)}})
	}
	seedTimeline(t, r, filler...)
	seedTimeline(t, r, protocol.SSHTimelineItem{ID: "bp2", Kind: protocol.SSHItemBlock, Block: &protocol.SSHBlock{ID: "bp2", Conn: conn, Command: "cat PRIVATECMD", Private: true}})

	w, now := manualHub(t, r.d)
	b := w.box()
	stop, res := w.watch(b, "timeline:s1", "w")
	if !res.OK {
		t.Fatalf("refused: %+v", res)
	}
	defer stop()
	w.step()
	events := drain(t, b)
	parts := 0
	for _, it := range events {
		if ev := it.Event.(*protocol.TimelineEvent); ev.More {
			parts++
		}
	}
	if parts == 0 {
		t.Fatalf("fixture: the snapshot did not split into parts (%d events)", len(events))
	}

	// A delta carrying the same kinds of rows.
	seedTimeline(t, r,
		protocol.SSHTimelineItem{ID: "bp3", Kind: protocol.SSHItemBlock, Block: &protocol.SSHBlock{ID: "bp3", Conn: conn, Command: "cat PRIVATECMD", Private: true}},
		protocol.SSHTimelineItem{ID: "bs2", Kind: protocol.SSHItemBlock, Block: &protocol.SSHBlock{ID: "bs2", Conn: conn, Command: "pwd", Host: "HOSTNAME", User: "USERNAME", Cwd: "/CWDPATH"}},
	)
	*now = now.Add(2 * time.Second)
	r.d.watch.PokeSession("s1", pokeTimeline)
	w.step()
	*now = now.Add(2 * time.Second)
	w.step()
	delta := drain(t, b)
	sawDelta := false
	for _, it := range delta {
		ev := it.Event.(*protocol.TimelineEvent)
		for _, x := range ev.Items {
			sawDelta = sawDelta || x.ID == "bs2"
		}
	}
	if !sawDelta {
		t.Fatalf("fixture: no delta with the new shared block (%d events)", len(delta))
	}

	for _, it := range append(events, delta...) {
		raw, _ := json.Marshal(it.Event)
		for _, leak := range []string{"PRIVATECMD", "HOSTNAME", "USERNAME", "/CWDPATH", "HANDBACKTRANSCRIPT", "password for m", "sudo ls", `"prompt"`, "termElevated"} {
			if strings.Contains(string(raw), leak) {
				t.Errorf("%q reached a TimelineEvent", leak)
			}
		}
		for _, x := range it.Event.(*protocol.TimelineEvent).Items {
			if x.ID == "bp" || x.ID == "bp2" || x.ID == "bp3" {
				t.Errorf("private block %s published", x.ID)
			}
		}
	}
}
