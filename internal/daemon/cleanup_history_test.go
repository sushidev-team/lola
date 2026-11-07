package daemon

import (
	"reflect"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshstore"
	"github.com/sushidev-team/lola/internal/state"
)

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
