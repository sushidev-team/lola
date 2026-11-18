package remote

import (
	"encoding/json"
	"strings"

	"github.com/sushidev-team/lola/internal/protocol"
)

// commandTiers is the ONLY place a command becomes reachable from a phone. A
// command in no entry is tier never, so adding a case to the daemon's handler
// grants a phone nothing until a human writes it here. The never entries are
// written out (value protocol.TierNever) so a test can tell "classified as never"
// from "forgotten". Tier meaning: docs/mobile/01-pairing-and-tiers.md §8.2.
var commandTiers = buildCommandTiers()

func buildCommandTiers() map[string]string {
	m := map[string]string{}
	set := func(tier string, cmds ...string) {
		for _, c := range cmds {
			if _, dup := m[c]; dup {
				panic("remote: command classified twice: " + c)
			}
			m[c] = tier
		}
	}

	set(protocol.TierRead,
		"status", "sessions", "projects", protocol.CmdHubStatus,
		"panes", "pane",
		"diff", "checkpoints", "checkpointDiff",
		"prReadiness", "prs", "tickets",
		"bestOfCompare",
		protocol.CmdReviewPackages, protocol.CmdReviewImage, protocol.CmdReviewDiff, protocol.CmdReviewChecks,
		protocol.CmdAgentFeed, protocol.CmdAgentFeedOutput, protocol.CmdTalkState, protocol.CmdEndedSessions,
		protocol.CmdSessionTimeline,
		protocol.CmdAttachmentImage,
		protocol.CmdRelayCred,
	)
	set(protocol.TierRead, TransportCommands...)

	set(protocol.TierAnswer,
		protocol.CmdAgentSay, protocol.CmdAgentChoose, protocol.CmdAgentStop,
		protocol.CmdAgentQueueRelease, protocol.CmdAgentSetting,
		"answer", "feedback",
		protocol.CmdReviewMark, protocol.CmdReviewFeedback,
		protocol.CmdAttachImage,
		protocol.CmdSSHRecall, protocol.CmdSSHAnswer,
	)

	set(protocol.TierControl,
		"addressPRFeedback", "mergePR", "openPr", "openTicket", "openManual", "openExisting",
		"revive", "review", "coderabbit", "resolveConflict", "switchAgent",
		"dev", "devFreePort", "sessionTask", "renameSession", "broadcast", "bestOf",
		"restoreCheckpoint", "forkCheckpoint", "pollOnce", "enable", "disable",
		protocol.CmdReviewRecover,
	)

	set(protocol.TierDestructive, "kill", "closeExitedTemporary", "bestOfPick")

	set(protocol.TierShell,
		"shellCreate", "paneClose", "paneResize", "open",
		protocol.CmdReviewVerify, protocol.CmdReviewCapture,
	)

	set(protocol.TierNever,
		// Daemon lifecycle and config.
		"stop", "reload", "renameProject", "bulkProjects",
		// The agent's in-pane channels.
		"hookEvent", "agentReport", protocol.CmdReviewPublish, protocol.CmdReviewComplete,
		// Asked from the Mac's branch graph, which a phone does not have.
		"askMerge",
		// Asked from the Mac's session menu; it can restart a sandboxed agent.
		"addApps",
		// Transport-internal, and the host browser.
		protocol.CmdRemotePair, "openURL",
		// Planning, goals, profiles, knowledge, comparison: not a phone's work.
		"plan", "planApply",
		"goalPropose", "goalProposal", "goalProposals", "goalAcceptPlan", "goalEditPlan",
		"goalRecovery", "goalDismissProgress", "goalSyncBrief", "goalAcceptBrief", "goalOverview",
		"goals", "goalCreate", "goalShow", "goalUsage", "goalPublish", "goalPublishReviewed",
		"goalStartOptions", "goalStart", protocol.GoalStartV2, "goalControl", "goalDeliveryEvidence",
		"reviewProfiles", "profileResults", "profileReview", "profileFindings",
		"profileDecideFinding", "profileCalibration",
		"knowledgeVersion", "knowledgeList", "knowledgeLookup", "knowledgeExport", "knowledgeCapture",
		"knowledgeCorrect", "knowledgeAccept", "knowledgeDispute", "knowledgeMaintain",
		"knowledgeImprovementCatalog", "knowledgeImprovementSources", "knowledgeImprovementDecisions",
		"knowledgeImprovementHistory", "knowledgeImprovements", "knowledgeImprovement",
		"knowledgeDecideImprovement", "knowledgeExportImprovement",
		"comparisonTemplate", "comparisonPrepare", "comparisonReport",
	)
	// Whole protocol lists: a name added to one of them is never by construction.
	set(protocol.TierNever, protocol.LocalOnlyCommands...)
	set(protocol.TierNever, protocol.HubHumanCommands...)
	set(protocol.TierNever, protocol.ShareHumanCommands...)
	set(protocol.TierNever, protocol.SSHAgentCommands...)
	set(protocol.TierNever, protocol.SSHHumanCommands...)
	return m
}

// remoteNamespace marks the commands the transport speaks to itself
// (remote.pair, ...). On a paired connection every one is tier never.
const remoteNamespace = "remote."

// TierFor returns the tier of a req frame's cmd. Unknown, empty and
// "remote."-prefixed commands are TierNever.
func TierFor(cmd string) string {
	if cmd == "" || strings.HasPrefix(cmd, remoteNamespace) {
		return protocol.TierNever
	}
	if t, ok := commandTiers[cmd]; ok {
		return t
	}
	return protocol.TierNever
}

// TransportCommands are tiered req cmds that internal/remote answers itself and
// never passes to Options.Handle: watch, unwatch. remote.pair is NOT here: it is
// consumed by Authenticate before any tier applies. relayCred is an ordinary
// handled command.
var TransportCommands = []string{protocol.CmdWatch, protocol.CmdUnwatch}

// CommandTiers returns a copy of the tier table (tests, and the tier golden
// file the phone client reads).
func CommandTiers() map[string]string {
	out := make(map[string]string, len(commandTiers))
	for k, v := range commandTiers {
		out[k] = v
	}
	return out
}

// frameTier classifies any inbound frame. req is its command's tier; sub and
// unsub read; pty by action (write needs shell, scroll and resize read, anything
// else or a malformed payload never); an inbound err is read; every other type
// is never.
func frameTier(f *protocol.Frame) string {
	if f == nil {
		return protocol.TierNever
	}
	switch f.Type {
	case protocol.FrameReq:
		return TierFor(f.Cmd)
	case protocol.FrameSub, protocol.FrameUnsub, protocol.FrameErr:
		return protocol.TierRead
	case protocol.FramePTY:
		var in protocol.PTYInputPayload
		if err := json.Unmarshal(f.Payload, &in); err != nil {
			return protocol.TierNever
		}
		switch in.Action {
		case protocol.PTYActionWrite:
			return protocol.TierShell
		case protocol.PTYActionScroll, protocol.PTYActionResize:
			return protocol.TierRead
		}
	}
	return protocol.TierNever
}
