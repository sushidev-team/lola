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
