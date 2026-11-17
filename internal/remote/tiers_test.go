package remote

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
)

func TestTierTableIsClosed(t *testing.T) {
	ok := map[string]bool{protocol.TierNever: true}
	for _, tier := range protocol.Tiers {
		ok[tier] = true
	}
	for cmd, tier := range CommandTiers() {
		if !ok[tier] {
			t.Errorf("%s has tier %q, outside protocol.Tiers and never", cmd, tier)
		}
	}
}

func TestCommandTiersReturnsACopy(t *testing.T) {
	m := CommandTiers()
	m["status"] = protocol.TierShell
	delete(m, "sessions")
	if TierFor("status") != protocol.TierRead || TierFor("sessions") != protocol.TierRead {
		t.Fatal("mutating the exported map changed the live table")
	}
}

// The handled-command split pinned by docs/mobile/01 §8.3: 175 commands.
func TestTierCountsMatchTheSpec(t *testing.T) {
	skip := map[string]bool{protocol.CmdRemotePair: true}
	for _, c := range TransportCommands {
		skip[c] = true
	}
	got := map[string]int{}
	for cmd, tier := range CommandTiers() {
		if !skip[cmd] {
			got[tier]++
		}
	}
	want := map[string]int{
		protocol.TierRead: 24, protocol.TierAnswer: 12, protocol.TierControl: 23,
		protocol.TierDestructive: 3, protocol.TierShell: 6, protocol.TierNever: 113,
	}
	for tier, n := range want {
		if got[tier] != n {
			t.Errorf("tier %s: %d commands, want %d", tier, got[tier], n)
		}
	}
	for _, c := range TransportCommands {
		if TierFor(c) != protocol.TierRead {
			t.Errorf("transport command %s is %s, want read", c, TierFor(c))
		}
	}
}

// Named TestTier... only while policy_test.go still declares the spec name (B2-1 removes it).
func TestTierEverySSHCommandIsDenied(t *testing.T) {
	for _, list := range [][]string{protocol.SSHAgentCommands, protocol.SSHHumanCommands} {
		for _, cmd := range list {
			if TierFor(cmd) != protocol.TierNever {
				t.Errorf("%s is %s; a phone gets no ssh-host command", cmd, TierFor(cmd))
			}
		}
	}
}

func TestTalkCommandsAreNotSSHCommands(t *testing.T) {
	for _, cmd := range protocol.TalkCommands {
		if slices.Contains(protocol.SSHAgentCommands, cmd) || slices.Contains(protocol.SSHHumanCommands, cmd) {
			t.Errorf("%s is both a Conversation and an ssh command", cmd)
		}
	}
}

func TestTierHubHumanCommandsAreDenied(t *testing.T) {
	for _, cmd := range protocol.HubHumanCommands {
		if TierFor(cmd) != protocol.TierNever {
			t.Errorf("%s is %s", cmd, TierFor(cmd))
		}
	}
	if TierFor(protocol.CmdHubStatus) != protocol.TierRead {
		t.Error("hubStatus must stay a read")
	}
}

func TestLocalOnlyCommandsAreNever(t *testing.T) {
	for _, cmd := range protocol.LocalOnlyCommands {
		if TierFor(cmd) != protocol.TierNever {
			t.Errorf("%s is %s", cmd, TierFor(cmd))
		}
	}
}

func TestTalkCommandsAreTiered(t *testing.T) {
	for _, cmd := range []string{
		protocol.CmdAgentFeed, protocol.CmdAgentFeedOutput, protocol.CmdTalkState, protocol.CmdEndedSessions,
	} {
		if TierFor(cmd) != protocol.TierRead {
			t.Errorf("%s = %s, want read", cmd, TierFor(cmd))
		}
	}
	for _, cmd := range []string{
		protocol.CmdAgentSay, protocol.CmdAgentStop, protocol.CmdAgentChoose, protocol.CmdAgentSetting,
		protocol.CmdAgentQueueRelease, protocol.CmdSSHRecall, protocol.CmdSSHAnswer,
	} {
		if TierFor(cmd) != protocol.TierAnswer {
			t.Errorf("%s = %s, want answer", cmd, TierFor(cmd))
		}
	}
	for _, cmd := range protocol.TalkCommands {
		if TierFor(cmd) == protocol.TierNever {
			t.Errorf("%s is never", cmd)
		}
	}
}

func TestUnknownAndEmptyAreNever(t *testing.T) {
	for _, cmd := range []string{"", "nonsense", "Status", " status"} {
		if TierFor(cmd) != protocol.TierNever {
			t.Errorf("TierFor(%q) = %s", cmd, TierFor(cmd))
		}
	}
}

func TestRemotePrefixIsNever(t *testing.T) {
	for _, cmd := range []string{"remote.pair", "remote.hello", "remote.", "remote.status"} {
		if TierFor(cmd) != protocol.TierNever {
			t.Errorf("TierFor(%q) = %s", cmd, TierFor(cmd))
		}
	}
}

func ptyFrame(t *testing.T, in any) *protocol.Frame {
	t.Helper()
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return &protocol.Frame{Type: protocol.FramePTY, Pane: "p", Payload: b}
}

func TestFrameTierPTYActions(t *testing.T) {
	for action, want := range map[string]string{
		protocol.PTYActionWrite:  protocol.TierShell,
		protocol.PTYActionScroll: protocol.TierRead,
		protocol.PTYActionResize: protocol.TierRead,
		"":                       protocol.TierNever,
		"kill":                   protocol.TierNever,
	} {
		if got := frameTier(ptyFrame(t, protocol.PTYInputPayload{Action: action})); got != want {
			t.Errorf("pty %q = %s, want %s", action, got, want)
		}
	}
	bad := &protocol.Frame{Type: protocol.FramePTY, Payload: json.RawMessage(`[1]`)}
	if frameTier(bad) != protocol.TierNever {
		t.Error("a malformed pty payload must be never")
	}
}

func TestFrameTierByType(t *testing.T) {
	cases := map[string]string{
		protocol.FrameSub:    protocol.TierRead,
		protocol.FrameUnsub:  protocol.TierRead,
		protocol.FrameErr:    protocol.TierRead,
		protocol.FrameResp:   protocol.TierNever,
		protocol.FrameResync: protocol.TierNever,
		protocol.FrameEvent:  protocol.TierNever,
		protocol.FrameReady:  protocol.TierNever,
		"bogus":              protocol.TierNever,
		"":                   protocol.TierNever,
	}
	for typ, want := range cases {
		if got := frameTier(&protocol.Frame{Type: typ}); got != want {
			t.Errorf("frame %q = %s, want %s", typ, got, want)
		}
	}
	if got := frameTier(&protocol.Frame{Type: protocol.FrameReq, Cmd: "kill"}); got != protocol.TierDestructive {
		t.Errorf("req kill = %s", got)
	}
	if got := frameTier(&protocol.Frame{Type: protocol.FrameReq, Cmd: "stop"}); got != protocol.TierNever {
		t.Errorf("req stop = %s", got)
	}
	if frameTier(nil) != protocol.TierNever {
		t.Error("nil frame must be never")
	}
}

func TestBestOfPickIsDestructive(t *testing.T) {
	if TierFor("bestOfPick") != protocol.TierDestructive {
		t.Fatalf("bestOfPick = %s", TierFor("bestOfPick"))
	}
	for _, cmd := range []string{"kill", "closeExitedTemporary"} {
		if TierFor(cmd) != protocol.TierDestructive {
			t.Errorf("%s = %s", cmd, TierFor(cmd))
		}
	}
}

func TestCommandsThatExecAreShell(t *testing.T) {
	for _, cmd := range []string{"open", protocol.CmdReviewVerify, protocol.CmdReviewCapture, "shellCreate"} {
		if TierFor(cmd) != protocol.TierShell {
			t.Errorf("%s = %s, want shell", cmd, TierFor(cmd))
		}
	}
}

func TestRelayCredIsReadTier(t *testing.T) {
	if TierFor(protocol.CmdRelayCred) != protocol.TierRead {
		t.Fatalf("relayCred = %s", TierFor(protocol.CmdRelayCred))
	}
}
