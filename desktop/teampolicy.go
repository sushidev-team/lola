package main

import (
	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/protocol"
)

// The team policy, for the settings UI. A key the daemon's verified team
// policy carries is LOCKED: the daemon overlays it onto its runtime config,
// so a value typed into the form for it has no effect while the policy holds.
// The form reads and writes config.toml exactly as before (the overlay never
// reaches the file); this only tells it which controls to show as set by the
// team, with the team's value beside the local one.
//
// It asks the RUNNING daemon (cmd=hubStatus) because the overlay is the
// daemon's decision: it re-verifies the bundle against the pinned keys and
// checks the identity. An unreachable or older daemon answers Available=false
// and nothing is locked, never an error that could blank the settings view.

// LockedSettingDTO is one key the team policy locks.
type LockedSettingDTO struct {
	// Key is the hubproto wire key (hubproto.KeyBudgetDaily, ...).
	Key string `json:"key"`
	// Label is a Sentence-case name for the control.
	Label string `json:"label"`
	// Value is the team's value, Local what config.toml says (human text).
	Value string `json:"value"`
	Local string `json:"local"`
	// ConfigKey is the config.toml key the policy overrides.
	ConfigKey string `json:"configKey"`
	// Field is the SettingsDTO json field the key covers ("" when the settings
	// form has no field for it yet).
	Field string `json:"field"`
}

// TeamPolicyDTO is ConfigService.TeamPolicy's answer.
type TeamPolicyDTO struct {
	// Available: a daemon answered hubStatus.
	Available bool   `json:"available"`
	Joined    bool   `json:"joined"`
	Org       string `json:"org"`
	// Version is the verified policy's version (0 = none).
	Version int `json:"version"`
	// Applied: the daemon's live config enforces Locked.
	Applied bool `json:"applied"`
	// Note says why a verified policy is not applied.
	Note string `json:"note"`
	// Keys are every key the policy carries (escalate_after included, which
	// only the hub reads); Summary is the daemon's human rendering.
	Keys    []string `json:"keys"`
	Summary []string `json:"summary"`
	// Locked is what the daemon enforces. Never null.
	Locked []LockedSettingDTO `json:"locked"`
}

// policyKeyMeta maps a policy key onto the settings it locks.
var policyKeyMeta = map[string]struct{ label, configKey, field string }{
	hubproto.KeyReviewProviders: {"Review providers", "review.provider[].enabled", "reviewProviders"},
	hubproto.KeyBudgetDaily:     {"Daily token budget", "budget.daily_tokens", ""},
	hubproto.KeyMergeQueue:      {"Merge queue", "merge_queue.enabled", ""},
	// A ceiling, not a lock: the daemon runs min(local, team), so the form keeps
	// globalCap editable and shows the team value as a hint.
	hubproto.KeyMaxConcurrency: {"Global concurrency cap", "defaults.global_cap", "globalCap"},
	hubproto.KeyAcceptKinds:    {"Accepted chore kinds", "hub.accept_kinds", "hubAcceptKinds"},
	hubproto.KeyEscalateAfter:  {"Escalate after failures", "", ""},
}

// hubStatusCall is the seam over the hubStatus round-trip.
var hubStatusCall = func(out *protocol.HubStatusData) error {
	return call(protocol.Request{Cmd: protocol.CmdHubStatus}, shortTimeout, out)
}

// TeamPolicy reports which settings the team policy locks.
func (s *ConfigService) TeamPolicy() TeamPolicyDTO {
	out := TeamPolicyDTO{Keys: []string{}, Summary: []string{}, Locked: []LockedSettingDTO{}}
	var st protocol.HubStatusData
	if err := hubStatusCall(&st); err != nil {
		return out
	}
	out.Available, out.Joined, out.Org = true, st.Joined, st.Org
	p := st.Policy
	if p == nil {
		return out
	}
	out.Version, out.Applied, out.Note = p.Version, p.Applied, p.ApplyNote
	out.Keys = append(out.Keys, p.Keys...)
	out.Summary = append(out.Summary, p.Summary...)
	if !p.Applied {
		return out
	}
	for _, k := range p.AppliedKeys {
		m, ok := policyKeyMeta[k.Key]
		if !ok {
			m.label = k.Key
		}
		out.Locked = append(out.Locked, LockedSettingDTO{Key: k.Key, Label: m.label, Value: k.Value, Local: k.Local, ConfigKey: m.configKey, Field: m.field})
	}
	return out
}
