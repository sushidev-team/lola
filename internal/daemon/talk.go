package daemon

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/sushidev-team/lola/internal/protocol"
)

// handleTalk serves the conversation commands. They are all human-side: the bridge's
// allowlist and internal/remote's denial both run before this, because every one of
// them is in protocol.SSHHumanCommands.
func (d *Daemon) handleTalk(ctx context.Context, req protocol.Request) protocol.Response {
	if req.Cmd != protocol.CmdEndedSessions {
		// Every other command names a session. Refuse a request that does not here, so no
		// handler reads an empty id as a session that is merely unknown.
		var named struct {
			Session string `json:"session"`
		}
		if err := json.Unmarshal(req.Args, &named); err != nil || strings.TrimSpace(named.Session) == "" {
			return protocol.Response{OK: false, Error: "session id required"}
		}
	}
	switch req.Cmd {
	case protocol.CmdAgentFeed:
		return sshRoute(req, func(a protocol.AgentFeedArgs) (protocol.AgentFeedData, error) { return d.handleAgentFeed(ctx, a) })
	case protocol.CmdAgentFeedOutput:
		return sshRoute(req, func(a protocol.AgentFeedOutputArgs) (protocol.AgentFeedOutputData, error) {
			return d.handleAgentFeedOutput(ctx, a)
		})
	case protocol.CmdTalkState:
		return sshRoute(req, func(a protocol.TalkStateArgs) (protocol.TalkStateData, error) { return d.handleTalkState(ctx, a) })
	case protocol.CmdAgentSay:
		return sshRoute(req, func(a protocol.AgentSayArgs) (protocol.AgentSayData, error) { return d.handleAgentSay(ctx, a) })
	case protocol.CmdAgentStop:
		return sshRoute(req, func(a protocol.AgentStopArgs) (protocol.AgentStopData, error) { return d.handleAgentStop(ctx, a) })
	case protocol.CmdAgentChoose:
		return sshRoute(req, func(a protocol.AgentChooseArgs) (protocol.AgentChooseData, error) { return d.handleAgentChoose(ctx, a) })
	case protocol.CmdAgentQueueRelease:
		return sshRoute(req, func(a protocol.AgentQueueReleaseArgs) (protocol.AgentSayData, error) {
			return d.handleAgentQueueRelease(ctx, a)
		})
	case protocol.CmdEndedSessions:
		return sshReply(d.handleEndedSessions(ctx))
	}
	return protocol.Response{OK: false, Error: "unknown talk command " + req.Cmd}
}

// hookTally counts a session's lifecycle hooks since its feed scanner started.
type hookTally struct {
	UserPrompt, ToolUse, Stop int
}

// bumpHook counts one hook event. The events it does not know are not its business.
func (d *Daemon) bumpHook(id, event string) {
	id = strings.TrimSpace(id)
	d.hookCountsMu.Lock()
	defer d.hookCountsMu.Unlock()
	if d.hookCounts == nil {
		d.hookCounts = map[string]*hookTally{}
	}
	t := d.hookCounts[id]
	if t == nil {
		t = &hookTally{}
		d.hookCounts[id] = t
	}
	switch event {
	case "user_prompt":
		t.UserPrompt++
	case "tool_use":
		t.ToolUse++
	case "stop":
		t.Stop++
	}
}

// hookTallyFor is a copy of a session's counts, zero when it has none.
func (d *Daemon) hookTallyFor(id string) hookTally {
	d.hookCountsMu.Lock()
	defer d.hookCountsMu.Unlock()
	if t := d.hookCounts[id]; t != nil {
		return *t
	}
	return hookTally{}
}
