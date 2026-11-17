package daemon

import (
	"context"
	"fmt"
	"strings"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

// Settings are immediate human actions, never queued. Sending a command is not
// proof the provider accepted it, so neither the launch model nor config changes.
func (d *Daemon) handleAgentSetting(ctx context.Context, a protocol.AgentSettingArgs) (protocol.AgentSettingData, error) {
	id := session.BaseID(strings.TrimSpace(a.Session))
	s, ok := d.sessions.Get(id)
	if !ok {
		return protocol.AgentSettingData{}, fmt.Errorf("unknown session %s", id)
	}
	if s.GoalManaged() || s.IsAgentless() || (s.Agent != "" && !agent.Valid(s.Agent)) {
		return protocol.AgentSettingData{}, fmt.Errorf("model settings are unavailable for this session")
	}
	k := agent.Parse(s.Agent)
	// Pin the provider the human saw: a switch while the request was in flight
	// must not apply the same-looking setting to a different agent.
	if a.Agent != string(k) {
		return protocol.AgentSettingData{}, fmt.Errorf("the active agent changed; choose the setting again")
	}
	command, err := agent.SettingCommand(k, a.Setting, a.Value)
	if err != nil {
		return protocol.AgentSettingData{}, err
	}
	unlock, why := d.lockHumanPrompt(ctx, s)
	if unlock == nil {
		return protocol.AgentSettingData{}, fmt.Errorf("model settings were not sent (%s); wait for an idle, empty agent prompt and try again", why)
	}
	defer unlock()
	cur, ok := d.sessions.Get(id)
	if !ok || cur.GoalManaged() || cur.IsAgentless() || agent.Parse(cur.Agent) != k || paneTarget(cur) != paneTarget(s) {
		return protocol.AgentSettingData{}, fmt.Errorf("the active agent changed; choose the setting again")
	}
	label := "You requested " + agentWord(cur) + " " + a.Setting + ": " + a.Value
	if k == agent.OpenCode {
		label = "You requested opencode's " + a.Setting + " picker"
	}
	note := noteFor(cur, label)
	if err := d.typeToAgentLocked(ctx, note, paneTarget(cur), command, reactExecTimeout); err != nil {
		return protocol.AgentSettingData{}, fmt.Errorf("could not send model setting: %w", err)
	}
	return protocol.AgentSettingData{Sent: true}, nil
}
