package main

import (
	"encoding/json"

	"github.com/sushidev-team/lola/internal/protocol"
)

// SSHService is the Wails-bound bridge for a HUMAN's side of agent-assisted ssh:
// going, approving, taking the wheel, talking, reading the timeline. Every method
// is one socket round-trip, the same shape as DaemonService, and it holds no
// state. The agent-side sshAgent* commands are deliberately absent: the app is
// never an agent, and nothing here may be reachable through the hook bridge or by
// a phone (internal/remote refuses every ssh command anyway).
//
// Reads answer from the daemon's memory and files, so they take the short
// timeout. Open and Go also touch tmux and `ssh -G`, and Close waits on a bounded
// `ssh -O exit`, so those take the long one. PromptAnswer is the one method that carries
// a secret, from the card to the daemon and no further. Wails logs a bound call's
// arguments at debug level, so desktop/main.go must not lower its log level.
type SSHService struct{}

func sshCall(cmd string, args, out any, slow bool) error {
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	timeout := shortTimeout
	if slow {
		timeout = longTimeout
	}
	return call(protocol.Request{Cmd: cmd, Args: raw}, timeout, out)
}

// Hosts lists the [[ssh_host]] catalog, resolved for display.
func (s *SSHService) Hosts() (protocol.SSHHostsData, error) {
	var d protocol.SSHHostsData
	err := call(protocol.Request{Cmd: protocol.CmdSSHHosts}, shortTimeout, &d)
	return d, err
}

// Open starts an agent-assisted session on a host. Starting it is the go for
// connecting; command approval still applies.
func (s *SSHService) Open(a protocol.SSHOpenArgs) (protocol.SSHOpenData, error) {
	var d protocol.SSHOpenData
	err := sshCall(protocol.CmdSSHOpen, a, &d, true)
	return d, err
}

// Connect connects an existing session to a host. The call is the go.
func (s *SSHService) Connect(a protocol.SSHConnectArgs) (protocol.SSHConnectData, error) {
	var d protocol.SSHConnectData
	err := sshCall(protocol.CmdSSHConnect, a, &d, true)
	return d, err
}

// RunCommand runs a command a human typed in the timeline. Private until shared.
func (s *SSHService) RunCommand(a protocol.SSHHumanRunArgs) (protocol.SSHBlock, error) {
	var d protocol.SSHBlock
	err := sshCall(protocol.CmdSSHHumanRun, a, &d, true)
	return d, err
}

// ShareBlock lets the agent read a command the human ran.
func (s *SSHService) ShareBlock(a protocol.SSHShareBlockArgs) (protocol.SSHBlock, error) {
	var d protocol.SSHBlock
	err := sshCall(protocol.CmdSSHShareBlock, a, &d, true)
	return d, err
}

// TerminalCd moves the human's shell to the connection's shared folder.
func (s *SSHService) TerminalCd(conn string) (protocol.SSHConnInfo, error) {
	var d protocol.SSHConnInfo
	err := sshCall(protocol.CmdSSHTerminalCd, protocol.SSHConnArgs{Conn: conn}, &d, true)
	return d, err
}

// Go is a human's go for a requested connection, or a reconnect of a dropped or
// failed one.
func (s *SSHService) Go(conn string) (protocol.SSHConnInfo, error) {
	var d protocol.SSHConnInfo
	err := sshCall(protocol.CmdSSHGo, protocol.SSHGoArgs{Conn: conn}, &d, true)
	return d, err
}

// Deny says no to a requested connection. The reason goes back to the agent.
func (s *SSHService) Deny(conn, reason string) (protocol.SSHConnInfo, error) {
	var d protocol.SSHConnInfo
	err := sshCall(protocol.CmdSSHDeny, protocol.SSHDenyArgs{Conn: conn, Reason: reason}, &d, false)
	return d, err
}

// Approve decides a command waiting for approval: approve or deny, and an approve
// may also open a time-limited grant where the host allows one.
func (s *SSHService) Approve(a protocol.SSHApproveArgs) (protocol.SSHBlock, error) {
	var d protocol.SSHBlock
	err := sshCall(protocol.CmdSSHApprove, a, &d, false)
	return d, err
}

// RevokeGrant ends a connection's live grant.
func (s *SSHService) RevokeGrant(conn string) (protocol.SSHConnInfo, error) {
	var d protocol.SSHConnInfo
	err := sshCall(protocol.CmdSSHRevokeGrant, protocol.SSHConnArgs{Conn: conn}, &d, false)
	return d, err
}

// TakeWheel makes the connection the human's, at once.
func (s *SSHService) TakeWheel(a protocol.SSHTakeWheelArgs) (protocol.SSHTakeWheelData, error) {
	var d protocol.SSHTakeWheelData
	err := sshCall(protocol.CmdSSHTakeWheel, a, &d, false)
	return d, err
}

// HandbackPreview reads what a handback could send. Nothing is captured until the
// human opens the handback panel, and nothing is sent by this call.
func (s *SSHService) HandbackPreview(conn string) (protocol.SSHHandbackPreviewData, error) {
	var d protocol.SSHHandbackPreviewData
	err := sshCall(protocol.CmdSSHHandbackPreview, protocol.SSHConnArgs{Conn: conn}, &d, false)
	return d, err
}

// Handback returns the wheel. Transcript is exactly what the human left in the
// panel's text field, and it is sent only when non-empty.
func (s *SSHService) Handback(a protocol.SSHHandbackArgs) (protocol.SSHHandbackData, error) {
	var d protocol.SSHHandbackData
	err := sshCall(protocol.CmdSSHHandback, a, &d, false)
	return d, err
}

// Stop kills the local ssh of a running or queued command.
func (s *SSHService) Stop(conn, block string) (protocol.SSHStopData, error) {
	var d protocol.SSHStopData
	err := sshCall(protocol.CmdSSHStop, protocol.SSHStopArgs{Conn: conn, Block: block}, &d, false)
	return d, err
}

// Close ends a connection: the master, the tab and every open command.
// Trash closes a connection and deletes its tab's record for good.
func (s *SSHService) Trash(conn string) (protocol.SSHCloseData, error) {
	var d protocol.SSHCloseData
	err := sshCall(protocol.CmdSSHTrash, protocol.SSHConnArgs{Conn: conn}, &d, true)
	return d, err
}

func (s *SSHService) Close(conn string) (protocol.SSHCloseData, error) {
	var d protocol.SSHCloseData
	err := sshCall(protocol.CmdSSHClose, protocol.SSHConnArgs{Conn: conn}, &d, true)
	return d, err
}

// Terminal opens a new shell on a connection whose master is still up.
func (s *SSHService) Terminal(conn string) (protocol.SSHConnInfo, error) {
	var d protocol.SSHConnInfo
	err := sshCall(protocol.CmdSSHTerminal, protocol.SSHConnArgs{Conn: conn}, &d, true)
	return d, err
}

// Timeline fetches what changed in a session's timeline since a sequence number.
func (s *SSHService) Timeline(a protocol.SSHTimelineArgs) (protocol.SSHTimelineData, error) {
	var d protocol.SSHTimelineData
	err := sshCall(protocol.CmdSSHTimeline, a, &d, false)
	return d, err
}

// Output fetches stored output lazily.
func (s *SSHService) Output(a protocol.SSHOutputArgs) (protocol.SSHOutputData, error) {
	var d protocol.SSHOutputData
	err := sshCall(protocol.CmdSSHOutput, a, &d, false)
	return d, err
}

// Say queues something the human said to the agent. It defers, and never types
// into a pane that is mid-turn.
func (s *SSHService) Say(session, conn, text string) (protocol.SSHSayData, error) {
	var d protocol.SSHSayData
	err := sshCall(protocol.CmdSSHSay, protocol.SSHSayArgs{Session: session, Conn: conn, Text: text}, &d, false)
	return d, err
}

// Answer answers a question element, pinned to the revision the human read.
func (s *SSHService) Answer(a protocol.SSHAnswerArgs) (protocol.SSHSayData, error) {
	var d protocol.SSHSayData
	err := sshCall(protocol.CmdSSHAnswer, a, &d, false)
	return d, err
}

// WidgetStatus reports that a running widget failed, so its agent can fix it.
func (s *SSHService) WidgetStatus(a protocol.SSHWidgetStatusArgs) (protocol.SSHWidgetStatusData, error) {
	var d protocol.SSHWidgetStatusData
	err := sshCall(protocol.CmdSSHWidgetStatus, a, &d, false)
	return d, err
}

// Proposal runs or declines a proposal element, pinned to the revision the human
// read. A mismatch runs nothing.
func (s *SSHService) Proposal(a protocol.SSHProposalArgs) (protocol.SSHProposalData, error) {
	var d protocol.SSHProposalData
	err := sshCall(protocol.CmdSSHProposal, a, &d, false)
	return d, err
}

// Recall takes back a message that has not been delivered. Recalled false means
// delivery won the race.
func (s *SSHService) Recall(session, item string) (protocol.SSHRecallData, error) {
	var d protocol.SSHRecallData
	err := sshCall(protocol.CmdSSHRecall, protocol.SSHRecallArgs{Session: session, Item: item}, &d, false)
	return d, err
}

// Resolve asks what an alias means to the local ssh config. It runs `ssh -G`,
// which never connects.
func (s *SSHService) Resolve(alias, user string) (protocol.SSHResolveData, error) {
	var d protocol.SSHResolveData
	err := sshCall(protocol.CmdSSHResolve, protocol.SSHResolveArgs{Alias: alias, User: user}, &d, false)
	return d, err
}

// PromptAnswer answers a question the human's shell is waiting on: a password, a code or a
// yes/no. The secret stays in this one call. It is not kept and not returned, and an error
// never includes it.
func (s *SSHService) PromptAnswer(a protocol.SSHPromptAnswerArgs) (protocol.SSHPromptAnswerData, error) {
	var d protocol.SSHPromptAnswerData
	err := sshCall(protocol.CmdSSHPromptAnswer, a, &d, true)
	return d, err
}
