package main

import (
	"errors"
	"regexp"
	"strconv"

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
	// Field is the settings form's id for the control the key covers: a
	// SettingsDTO json field, or "budgetDaily" / "mergeQueue" for the two
	// controls TeamLimits reads and SaveTeamLimits writes ("" = none).
	Field string `json:"field"`
	// Ceiling: the daemon runs min(local, team) for this key, so the control
	// stays editable and a lower local value still counts. The UI reads it as
	// "Team ceiling N", not as a lock.
	Ceiling bool `json:"ceiling"`
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
	// EscalateAfter is the policy's escalate_after (0 = not set). The hub
	// alone acts on it, so it is shown whenever the policy carries it, applied
	// or not, and never locks anything here.
	EscalateAfter int `json:"escalateAfter"`
	// Locked is what the daemon enforces. Never null.
	Locked []LockedSettingDTO `json:"locked"`
}

// policyKeyMeta maps a policy key onto the settings it locks.
var policyKeyMeta = map[string]struct {
	label, configKey, field string
	ceiling                 bool
}{
	hubproto.KeyReviewProviders: {"Review providers", "review.provider[].enabled", "reviewProviders", false},
	// Budget and concurrency are CEILINGS, not locks: the daemon runs
	// min(local, team) (config.ApplyTeamPolicy), so the form keeps them
	// editable and shows the team value as the ceiling.
	hubproto.KeyBudgetDaily:    {"Daily token budget", "budget.daily_tokens", "budgetDaily", true},
	hubproto.KeyMergeQueue:     {"Merge queue", "merge_queue.enabled", "mergeQueue", false},
	hubproto.KeyMaxConcurrency: {"Global concurrency cap", "defaults.global_cap", "globalCap", true},
	hubproto.KeyAcceptKinds:    {"Accepted chore kinds", "hub.accept_kinds", "hubAcceptKinds", false},
	hubproto.KeyEscalateAfter:  {"Escalate after failures", "", "", false},
}

// escalateSummaryRe reads escalate_after back out of the daemon's summary line
// (internal/hub/policy.go: "escalate after: N failures"). It is the only
// place the value reaches a client, because only the hub acts on it.
var escalateSummaryRe = regexp.MustCompile(`^escalate after: (\d{1,3}) failures?$`)

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
	for _, line := range p.Summary {
		if m := escalateSummaryRe.FindStringSubmatch(line); m != nil {
			out.EscalateAfter, _ = strconv.Atoi(m[1])
		}
	}
	if !p.Applied {
		return out
	}
	for _, k := range p.AppliedKeys {
		m, ok := policyKeyMeta[k.Key]
		if !ok {
			m.label = k.Key
		}
		out.Locked = append(out.Locked, LockedSettingDTO{Key: k.Key, Label: m.label, Value: k.Value, Local: k.Local, ConfigKey: m.configKey, Field: m.field, Ceiling: m.ceiling})
	}
	return out
}

// TeamLimitsDTO is the two local settings a team policy can govern that the
// settings form has no SettingsDTO field for: [budget].daily_tokens and
// [merge_queue].enabled. They save AT ONCE (SaveTeamLimits), so a policy
// lock is drawn over a real control and no overlay Save can drop them.
type TeamLimitsDTO struct {
	// BudgetDaily is the daily limit in weighted tokens (0 = no limit).
	BudgetDaily int64 `json:"budgetDaily"`
	MergeQueue  bool  `json:"mergeQueue"`
}

// TeamLimits reads the local values (config.toml, never the policy overlay).
func (s *ConfigService) TeamLimits() (TeamLimitsDTO, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return TeamLimitsDTO{}, err
	}
	return TeamLimitsDTO{BudgetDaily: cfg.Budget.DailyTokens, MergeQueue: cfg.MergeQueue.Enabled}, nil
}

// SaveTeamLimits writes both values. A locked key is still written (the file
// keeps the person's own value; the overlay wins while the policy holds), so
// the UI disables a locked control rather than relying on a refusal here.
func (s *ConfigService) SaveTeamLimits(d TeamLimitsDTO) error {
	if d.BudgetDaily < 0 {
		return errors.New("the daily token budget is 0 (no limit) or more")
	}
	cfg, path, err := loadConfig()
	if err != nil {
		return err
	}
	cfg.Budget.DailyTokens = d.BudgetDaily
	cfg.MergeQueue.Enabled = d.MergeQueue
	return saveConfig(cfg, path)
}
