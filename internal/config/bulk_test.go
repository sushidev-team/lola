package config

import (
	"errors"
	"slices"
	"testing"
)

func bp[T any](v T) *T { return &v }

func fakeResolve(p Project, ref string) (string, error) {
	if ref == "missing" {
		return "", errors.New("no such label")
	}
	return p.TeamID + ":" + ref, nil
}

func TestApplyBulkResolvesPerTeamAndClearsInherit(t *testing.T) {
	cfg := &Config{Projects: []Project{
		{Name: "a", TeamID: "t1", Inherits: ProjectInherits{MatchLabels: true, BlockedLabelID: true}},
		{Name: "b", TeamID: "t2", Agent: "claude"},
	}}
	ch, err := ApplyBulk(cfg, nil, BulkPatch{Agent: bp("codex"), MatchLabels: bp([]string{"bug", "bug"}), BlockedLabel: bp("Blocked")}, fakeResolve)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch) != 2 || ch[0].Error != "" || ch[1].Error != "" {
		t.Fatalf("changes: %+v", ch)
	}
	a, b := cfg.Projects[0], cfg.Projects[1]
	if !slices.Equal(a.MatchLabels, []string{"t1:bug"}) || !slices.Equal(b.MatchLabels, []string{"t2:bug"}) {
		t.Fatalf("labels not resolved per team: %v %v", a.MatchLabels, b.MatchLabels)
	}
	if a.Inherits.MatchLabels || a.Inherits.BlockedLabelID || a.Agent != "codex" || b.BlockedLabelID != "t2:Blocked" {
		t.Fatalf("unexpected: %+v %+v", a, b)
	}
}

func TestApplyBulkSelectionAndErrors(t *testing.T) {
	cfg := &Config{Projects: []Project{{Name: "a", TeamID: "t"}, {Name: "b", TeamID: "t"}}}
	ch, err := ApplyBulk(cfg, []string{"b"}, BulkPatch{OnSentSetLabel: bp("missing")}, fakeResolve)
	if err != nil || len(ch) != 1 || ch[0].Error == "" || cfg.Projects[1].OnSentSetLabel != "" {
		t.Fatalf("ch=%+v err=%v cfg=%+v", ch, err, cfg.Projects[1])
	}
	for _, bad := range []BulkPatch{{}, {Agent: bp("vim")}, {MatchMode: bp("some")}} {
		if _, err := ApplyBulk(cfg, nil, bad, fakeResolve); err == nil {
			t.Fatalf("%+v accepted", bad)
		}
	}
	if _, err := ApplyBulk(cfg, []string{"zzz"}, BulkPatch{Agent: bp("codex")}, fakeResolve); err == nil {
		t.Fatal("unknown project accepted")
	}
}

func TestApplyBulkClearAndNoop(t *testing.T) {
	cfg := &Config{Projects: []Project{{Name: "a", MatchLabels: []string{"x"}, MatchMode: "any", Agent: "codex"}}}
	ch, _ := ApplyBulk(cfg, nil, BulkPatch{MatchLabels: bp([]string{}), Agent: bp("codex")}, fakeResolve)
	if len(cfg.Projects[0].MatchLabels) != 0 || !slices.Equal(ch[0].Changed, []string{"match_labels"}) {
		t.Fatalf("%+v %+v", ch, cfg.Projects[0])
	}
}
