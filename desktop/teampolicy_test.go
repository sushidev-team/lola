package main

import (
	"errors"
	"testing"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
)

func withHubStatus(t *testing.T, st protocol.HubStatusData, err error) {
	t.Helper()
	prev := hubStatusCall
	hubStatusCall = func(out *protocol.HubStatusData) error {
		*out = st
		return err
	}
	t.Cleanup(func() { hubStatusCall = prev })
}

func TestTeamPolicyLockedKeys(t *testing.T) {
	withHubStatus(t, protocol.HubStatusData{Joined: true, Org: "acme", Policy: &protocol.HubPolicyInfo{
		Version: 3, Applied: true, Keys: []string{hubproto.KeyMaxConcurrency, hubproto.KeyEscalateAfter},
		AppliedKeys: []protocol.HubAppliedKey{{Key: hubproto.KeyMaxConcurrency, Value: "2", Local: "6"}},
	}}, nil)
	got := (&ConfigService{}).TeamPolicy()
	if !got.Available || !got.Applied || got.Version != 3 || len(got.Locked) != 1 {
		t.Fatalf("TeamPolicy = %+v", got)
	}
	l := got.Locked[0]
	if l.Field != "globalCap" || l.ConfigKey != "defaults.global_cap" || l.Label != "Global concurrency cap" || l.Value != "2" || l.Local != "6" {
		t.Errorf("locked = %+v", l)
	}
}

// Every key hubproto can lock has a label, so the UI never shows a wire key.
func TestTeamPolicyKeyMetaCoversEveryKey(t *testing.T) {
	on := true
	all := hubproto.Settings{ReviewProviders: []string{"claude-session"}, BudgetDailyWeightedTokens: 1, MergeQueueEnabled: &on, MaxConcurrency: 1, AcceptKinds: []hubproto.Kind{hubproto.KindDepAdvisory}, EscalateAfter: 1}
	for _, k := range all.Keys() {
		if m, ok := policyKeyMeta[k]; !ok || m.label == "" {
			t.Errorf("no settings label for policy key %q", k)
		}
	}
}

// A down or older daemon locks nothing and never errors.
func TestTeamPolicyDaemonDown(t *testing.T) {
	withHubStatus(t, protocol.HubStatusData{}, errors.New(`unknown cmd "hubStatus"`))
	got := (&ConfigService{}).TeamPolicy()
	if got.Available || got.Locked == nil || len(got.Locked) != 0 || got.Keys == nil {
		t.Errorf("TeamPolicy = %+v", got)
	}
}

// Not applied (apply_policy = false): keys are shown, nothing is locked.
func TestTeamPolicyNotApplied(t *testing.T) {
	withHubStatus(t, protocol.HubStatusData{Joined: true, Policy: &protocol.HubPolicyInfo{
		Version: 2, Keys: []string{hubproto.KeyBudgetDaily}, ApplyNote: "[hub].apply_policy = false",
	}}, nil)
	got := (&ConfigService{}).TeamPolicy()
	if got.Applied || len(got.Locked) != 0 || got.Note != "[hub].apply_policy = false" || len(got.Keys) != 1 {
		t.Errorf("TeamPolicy = %+v", got)
	}
}

// Budget and concurrency are ceilings with a form field; the merge queue is a
// lock with one. escalate_after is read back from the summary whether or not
// the policy is applied.
func TestTeamPolicyCeilingsAndEscalate(t *testing.T) {
	withHubStatus(t, protocol.HubStatusData{Joined: true, Policy: &protocol.HubPolicyInfo{
		Version: 4, Applied: true,
		Keys:    []string{hubproto.KeyBudgetDaily, hubproto.KeyMergeQueue, hubproto.KeyEscalateAfter},
		Summary: []string{"daily budget: 100 weighted tokens", "escalate after: 3 failures"},
		AppliedKeys: []protocol.HubAppliedKey{
			{Key: hubproto.KeyBudgetDaily, Value: "100", Local: "0"},
			{Key: hubproto.KeyMergeQueue, Value: "false", Local: "true"},
		},
	}}, nil)
	got := (&ConfigService{}).TeamPolicy()
	if got.EscalateAfter != 3 || len(got.Locked) != 2 {
		t.Fatalf("TeamPolicy = %+v", got)
	}
	if b := got.Locked[0]; b.Field != "budgetDaily" || !b.Ceiling {
		t.Errorf("budget = %+v", b)
	}
	if m := got.Locked[1]; m.Field != "mergeQueue" || m.Ceiling {
		t.Errorf("merge queue = %+v", m)
	}

	withHubStatus(t, protocol.HubStatusData{Joined: true, Policy: &protocol.HubPolicyInfo{
		Version: 4, Keys: []string{hubproto.KeyEscalateAfter}, Summary: []string{"escalate after: 1 failure"},
	}}, nil)
	if got := (&ConfigService{}).TeamPolicy(); got.EscalateAfter != 1 || got.Applied {
		t.Errorf("not applied: %+v", got)
	}
}

func TestTeamLimitsRoundTrip(t *testing.T) {
	path := writeTestConfig(t, minimalConfig)
	s := &ConfigService{}
	if got, err := s.TeamLimits(); err != nil || got.BudgetDaily != 0 || got.MergeQueue {
		t.Fatalf("TeamLimits = %+v, %v", got, err)
	}
	if err := s.SaveTeamLimits(TeamLimitsDTO{BudgetDaily: -1}); err == nil {
		t.Fatal("accepted a negative budget")
	}
	if err := s.SaveTeamLimits(TeamLimitsDTO{BudgetDaily: 5000, MergeQueue: true}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Budget.DailyTokens != 5000 || !cfg.MergeQueue.Enabled {
		t.Errorf("saved budget=%d mq=%v", cfg.Budget.DailyTokens, cfg.MergeQueue.Enabled)
	}
}
