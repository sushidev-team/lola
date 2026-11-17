package tui

import (
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
)

func TestTUIHidesNewEventKinds(t *testing.T) {
	sys := func(id, kind string) protocol.SSHTimelineItem {
		return protocol.SSHTimelineItem{ID: id, Kind: protocol.SSHItemSystem, System: &protocol.SSHSystemEvent{Kind: kind, Text: kind}}
	}
	hidden := []string{
		protocol.SSHEventSessionStarted, protocol.SSHEventSessionEnded, protocol.SSHEventAgentExited,
		protocol.SSHEventStopped, protocol.SSHEventCheckpoint, protocol.SSHEventPR, protocol.SSHEventCI,
		protocol.SSHEventReview, protocol.SSHEventHandoff,
	}
	shown := []string{
		protocol.SSHEventRequested, protocol.SSHEventGo, protocol.SSHEventDenied, protocol.SSHEventConnected,
		protocol.SSHEventDisconnected, protocol.SSHEventFailed, protocol.SSHEventWheelTaken, protocol.SSHEventHandedBack,
		protocol.SSHEventGrantCreated, protocol.SSHEventGrantEnded, protocol.SSHEventClosed,
	}
	s := &sshModel{}
	var in []protocol.SSHTimelineItem
	for _, k := range hidden {
		in = append(in, sys("hide-"+k, k))
	}
	for _, k := range shown {
		in = append(in, sys("show-"+k, k))
	}
	in = append(in, protocol.SSHTimelineItem{ID: "h1", Kind: protocol.SSHItemHuman, Human: &protocol.SSHHumanMessage{Text: "words"}})
	added := s.upsert(in)
	if added != len(shown)+1 || len(s.items) != len(shown)+1 {
		t.Fatalf("added %d, kept %d, want %d", added, len(s.items), len(shown)+1)
	}
	for _, it := range s.items {
		if it.System != nil && sshHiddenInTUI(it) {
			t.Fatalf("%s reached the pager", it.System.Kind)
		}
	}
}
