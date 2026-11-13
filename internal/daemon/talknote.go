package daemon

import (
	"fmt"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/agentfeed"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshstore"
)

// typedNote says what a send is, for the record it leaves behind. Every path that
// types into an agent passes one through typeToAgentLocked, so the compiler enforces
// that an agent never visibly responds to something nobody can see.
type typedNote struct {
	Session string // base id
	Label   string // lola's sentence for a handoff row; "" for a human's own send
	Items   []string
}

// noteFor builds the note for a send to s. A stack session's lane id is folded to its
// base, because there is one agent and one story however many PRs it holds.
func noteFor(s session.Session, label string) typedNote {
	return typedNote{Session: session.BaseID(s.ID), Label: label}
}

// noteTyped records a send that succeeded: a SentBatch, and a handoff row when the
// note has a label. It runs after the send and must never fail it.
func (d *Daemon) noteTyped(n typedNote, text string) {
	if n.Session == "" {
		return
	}
	ev := timelineEvent{Session: n.Session, Sent: &sshstore.SentBatch{Hash: sentHash(text), Items: n.Items, At: time.Now()}}
	if n.Label != "" {
		ev.Event = protocol.SSHSystemEvent{Kind: protocol.SSHEventHandoff, Text: n.Label}
	}
	d.emitSystem(ev)
}

// sentHash is a send's identity in SentBatch. It is the one use the send path has for
// agentfeed, whose import is otherwise kept out of everything that types.
func sentHash(text string) string { return agentfeed.Hash(text) }

// agentWord is the agent's name as a person reads it in lola's sentences.
func agentWord(s session.Session) string {
	switch agent.Parse(s.Agent) {
	case agent.Codex:
		return "Codex"
	case agent.OpenCode:
		return "opencode"
	default:
		return "Claude"
	}
}

// reactionLabel names what the reaction engine typed. The switch mirrors the keys
// reactSendAgent is called with.
func reactionLabel(key string, s session.Session) string {
	who := agentWord(s)
	switch key {
	case "ci_failed":
		return "lola told " + who + " to fix failing CI"
	case "changes_requested":
		return "lola sent the requested changes to " + who
	case "merge_conflict":
		return "lola asked " + who + " to resolve merge conflicts"
	default:
		return "lola sent " + who + " a reaction"
	}
}

// reviewHandoffLabel names a review hand-off by the provider that wrote the findings.
// The findings themselves never reach this row.
func reviewHandoffLabel(p reviewProvider, s session.Session) string {
	name := labelsFor(p).who
	if p.Kind == kindCoderabbitWatch {
		name = "CodeRabbit" // labelsFor names the watch by the bot's login
	}
	return "lola sent " + name + "'s findings to " + agentWord(s)
}

// answerLabel is the row for a reply typed through cmd=answer. That path has no store
// item of its own, so without a label the send would match its log record, hide it as
// an echo and leave nothing on screen.
func answerLabel(s session.Session) string {
	return "You answered " + agentWord(s)
}

// feedbackLabel counts the line comments in a rendered batch. renderFeedback writes
// each one, and the general note, as a block starting with "- " at the left margin,
// and indents everything inside a block, so counting those lines cannot be fooled by
// the text of a comment.
func feedbackLabel(s session.Session, text string) string {
	n := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "- General note:") {
			n++
		}
	}
	who := agentWord(s)
	switch n {
	case 0:
		return "Your note went to " + who
	case 1:
		return "Your diff comment went to " + who
	default:
		return fmt.Sprintf("Your %d diff comments went to %s", n, who)
	}
}

func broadcastLabel(s session.Session, _ string) string {
	return "Your broadcast went to " + agentWord(s)
}

func resolveConflictLabel(s session.Session) string {
	return "You asked " + agentWord(s) + " to resolve conflicts"
}

func mergeQueueSyncLabel(s session.Session) string {
	return "lola asked " + agentWord(s) + " to catch up for the merge queue"
}
