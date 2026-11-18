package daemon

import (
	"context"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
)

func TestResolveLabelRefsPerTeam(t *testing.T) {
	f := &linear.Fake{}
	f.LabelsByTeam = map[string][]linear.Label{
		"t1": {{ID: "11111111-1111-1111-1111-111111111111", Name: "website", Parent: &linear.Label{Name: "apps"}}},
		"t2": {{ID: "22222222-2222-2222-2222-222222222222", Name: "website", Parent: &linear.Label{Name: "apps"}}},
	}
	cfg := &config.Config{Projects: []config.Project{
		{Name: "a", TeamID: "t1", MatchLabels: []string{"apps/website"}},
		{Name: "b", TeamID: "t2", MatchLabels: []string{"Apps/Website"}},
	}}
	if notes := resolveLabelRefs(context.Background(), cfg, f); len(notes) != 0 {
		t.Fatalf("notes: %v", notes)
	}
	if cfg.Projects[0].MatchLabels[0] != "11111111-1111-1111-1111-111111111111" || cfg.Projects[1].MatchLabels[0] != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("got %v %v", cfg.Projects[0].MatchLabels, cfg.Projects[1].MatchLabels)
	}
	if err := cfg.Save(t.TempDir() + "/c.toml"); err != config.ErrLabelRefsResolved {
		t.Fatalf("save err = %v", err)
	}
}

func TestResolveLabelRefsGlob(t *testing.T) {
	f := &linear.Fake{LabelsByTeam: map[string][]linear.Label{
		"t1": {
			{ID: "11111111-1111-1111-1111-111111111111", Name: "agent-ready", Parent: &linear.Label{Name: "agent"}},
			{ID: "22222222-2222-2222-2222-222222222222", Name: "agent-ready", Parent: &linear.Label{Name: "ai"}},
			{ID: "33333333-3333-3333-3333-333333333333", Name: "bug"},
		},
	}}
	cfg := &config.Config{Projects: []config.Project{
		{Name: "a", TeamID: "t1", MatchLabels: []string{"*/Agent-Ready"}, OnSentSetLabel: "bug"},
		{Name: "b", TeamID: "t1", MatchLabels: []string{"*/nope"}, OnSentSetLabel: "*/agent-ready"},
	}}
	notes := resolveLabelRefs(context.Background(), cfg, f)
	if got := cfg.Projects[0].MatchLabels; len(got) != 2 || got[0] != "11111111-1111-1111-1111-111111111111" || got[1] != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("glob expansion = %v", got)
	}
	if cfg.Projects[0].OnSentSetLabel != "33333333-3333-3333-3333-333333333333" {
		t.Fatalf("on_sent = %q", cfg.Projects[0].OnSentSetLabel)
	}
	// A glob matching nothing stays as written; a single-label setting refuses a multi-match.
	if cfg.Projects[1].MatchLabels[0] != "*/nope" || cfg.Projects[1].OnSentSetLabel != "*/agent-ready" || len(notes) != 2 {
		t.Fatalf("b = %+v notes=%v", cfg.Projects[1], notes)
	}
}
