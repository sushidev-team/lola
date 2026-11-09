package daemon

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshstore"
	"github.com/sushidev-team/lola/internal/state"
)

func TestAdoptionKeepsEndedWorktreesOutOfSessions(t *testing.T) {
	for _, polluted := range []bool{false, true} {
		t.Run(map[bool]string{false: "ended", true: "previously resurrected"}[polluted], func(t *testing.T) {
			d, _ := talkFakeDaemon(t)
			s := talkSession(d, "finished", state.AgentIdle)
			s.Title = "Finished work"
			d.dropSession(s)
			want := sshstore.ReadEnded(d.home, s.ID)
			if want == nil {
				t.Fatal("missing ended history")
			}
			leftover := session.Session{ID: s.ID, Source: "native", Status: "dead"}
			if polluted {
				d.sessions.Upsert(leftover)
			}
			d.native = &fakeNative{adopted: []session.Session{leftover}}
			for restart := 0; restart < 2; restart++ {
				d.adoptNativeSessions(context.Background())
				d.sessions = session.NewStore(filepath.Join(d.home, "state"))
				if _, ok := d.sessions.Get(s.ID); ok {
					t.Fatal("ended worktree reappeared in persisted sessions")
				}
				if got := sshstore.ReadEnded(d.home, s.ID); !reflect.DeepEqual(got, want) {
					t.Fatalf("adoption changed ended history: %+v", got)
				}
			}
		})
	}
}

func TestAdoptionStillRecoversLiveAndUnknownWorktrees(t *testing.T) {
	d, _ := talkFakeDaemon(t)
	s := talkSession(d, "live", state.AgentIdle)
	d.dropSession(s)
	d.native = &fakeNative{adopted: []session.Session{
		{ID: "live", Source: "native", Status: "working"},
		{ID: "unknown", Source: "native", Status: "dead"},
		{ID: "orphan", Source: "native", Status: "orphaned"},
	}}
	d.adoptNativeSessions(context.Background())
	for _, id := range []string{"live", "unknown", "orphan"} {
		if _, ok := d.sessions.Get(id); !ok {
			t.Fatalf("adoption hid recoverable session %s", id)
		}
	}
}

func TestCleanupRediscoveredSessionPreservesHistory(t *testing.T) {
	d, _ := talkFakeDaemon(t)
	s := talkSession(d, "s1", state.AgentIdle)
	s.Title, s.Issue, s.Worktree = "Finished work", "FE-1", "/tmp/wt/s1"
	s.TranscriptSegments = []string{"segment.jsonl"}
	s.PR = openPR(412, "MERGEABLE", "", "pass")
	s.SetDelivery(state.DeliveryMerged, time.Now())
	d.dropSession(s)
	want := sshstore.ReadEnded(d.home, s.ID)
	if want == nil {
		t.Fatal("missing original history")
	}
	// Restart found a leftover folder after the full record had been dropped.
	leftover := session.Session{ID: s.ID, Source: "native", Issue: s.Issue}
	leftover.SetAgentState(state.AgentDead, "", time.Now())
	d.sessions.Upsert(leftover)
	d.dropSession(leftover)
	if got := sshstore.ReadEnded(d.home, s.ID); !reflect.DeepEqual(got, want) {
		t.Fatalf("history overwritten: got %+v, want %+v", got, want)
	}
	if events := talkEvents(t, d, s.ID, protocol.SSHEventSessionEnded); len(events) != 1 {
		t.Fatalf("duplicate ended events: %+v", events)
	}
}
