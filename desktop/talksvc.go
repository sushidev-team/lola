package main

import "github.com/sushidev-team/lola/internal/protocol"

// TalkService is the Wails-bound bridge for the conversation in a session view: reading
// the agent's log, speaking, stopping a turn, answering a prompt on a card. Every method
// is one socket round-trip and it holds no state, the same shape as SSHService. It uses
// that service's sshCall on purpose, since these commands sit in the same human-side list
// and the same refusals apply.
//
// A daemon older than the app answers "unknown cmd". This service passes that through
// untouched, and the frontend reads it as a session with no capabilities.
//
// Reads answer from the daemon's memory and files, and talkState is called once a second,
// so they take the short timeout. The three that may type take the long one: Stop & send
// waits for a pane to come to rest, and a send waits out a checkpoint restore.
type TalkService struct{}

// Setting sends a model setting only at a freshly verified empty, idle prompt.
func (s *TalkService) Setting(a protocol.AgentSettingArgs) (protocol.AgentSettingData, error) {
	var d protocol.AgentSettingData
	err := sshCall(protocol.CmdAgentSetting, a, &d, true)
	return d, err
}

// Feed reads part of the agent's log. Every field of it is a claim.
func (s *TalkService) Feed(a protocol.AgentFeedArgs) (protocol.AgentFeedData, error) {
	var d protocol.AgentFeedData
	err := sshCall(protocol.CmdAgentFeed, a, &d, false)
	return d, err
}

// FeedOutput reads one tool call's output, sanitized and capped, on a human's request.
func (s *TalkService) FeedOutput(a protocol.AgentFeedOutputArgs) (protocol.AgentFeedOutputData, error) {
	var d protocol.AgentFeedOutputData
	err := sshCall(protocol.CmdAgentFeedOutput, a, &d, false)
	return d, err
}

// State is the viewed session's live state, from a fresh look at its pane.
func (s *TalkService) State(a protocol.TalkStateArgs) (protocol.TalkStateData, error) {
	var d protocol.TalkStateData
	err := sshCall(protocol.CmdTalkState, a, &d, false)
	return d, err
}

// Say speaks to the agent: delivered now if it is provably resting, queued otherwise.
func (s *TalkService) Say(a protocol.AgentSayArgs) (protocol.AgentSayData, error) {
	var d protocol.AgentSayData
	err := sshCall(protocol.CmdAgentSay, a, &d, true)
	return d, err
}

// Stop ends this turn. It stops nothing running on a host and holds what is queued.
func (s *TalkService) Stop(a protocol.AgentStopArgs) (protocol.AgentStopData, error) {
	var d protocol.AgentStopData
	err := sshCall(protocol.CmdAgentStop, a, &d, true)
	return d, err
}

// Choose presses one key on a prompt, pinned to the hash of what the human read.
func (s *TalkService) Choose(a protocol.AgentChooseArgs) (protocol.AgentChooseData, error) {
	var d protocol.AgentChooseData
	err := sshCall(protocol.CmdAgentChoose, a, &d, true)
	return d, err
}

// QueueRelease is "Send them": it ends the hold after a Stop and tries the next batch.
func (s *TalkService) QueueRelease(a protocol.AgentQueueReleaseArgs) (protocol.AgentSayData, error) {
	var d protocol.AgentSayData
	err := sshCall(protocol.CmdAgentQueueRelease, a, &d, true)
	return d, err
}

// Ended lists the sessions whose story is still readable after they left the list.
func (s *TalkService) Ended() (protocol.EndedSessionsData, error) {
	var d protocol.EndedSessionsData
	err := call(protocol.Request{Cmd: protocol.CmdEndedSessions}, shortTimeout, &d)
	return d, err
}
