package daemon

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
)

func TestResolveInheritedWorkspaceLabelRefs(t *testing.T) {
	ready := linear.Label{ID: "11111111-1111-1111-1111-111111111111", Name: "agent-ready", Parent: &linear.Label{Name: "agent"}}
	working := linear.Label{ID: "22222222-2222-2222-2222-222222222222", Name: "agent-working", Parent: ready.Parent}
	blocked := linear.Label{ID: "33333333-3333-3333-3333-333333333333", Name: "agent-blocked", Parent: ready.Parent}
	f := &linear.Fake{
		WorkspaceLabelSet: []linear.Label{ready, working, blocked},
		// A label returned in both scopes must not become ambiguous.
		LabelsByTeam: map[string][]linear.Label{"t1": {ready}},
	}
	cfg := &config.Config{
		Defaults: config.Defaults{MatchLabels: []string{"agent/agent-ready"}, OnSentSetLabel: "agent/agent-working", BlockedLabelID: "agent/agent-blocked"},
		Projects: []config.Project{
			{Name: "a", TeamID: "t1", MatchMode: "all", Inherits: config.ProjectInherits{OnSentSetLabel: true, BlockedLabelID: true}},
			{Name: "b", TeamID: "t2", MatchMode: "all", Inherits: config.ProjectInherits{OnSentSetLabel: true, BlockedLabelID: true}},
		},
	}
	cfg.ResolveInheritance()
	if notes, _ := resolveLabelRefs(context.Background(), cfg, f, nil); len(notes) != 0 {
		t.Fatalf("notes: %v", notes)
	}
	for _, p := range cfg.Projects {
		if !slices.Equal(p.MatchLabels, []string{ready.ID}) || p.OnSentSetLabel != working.ID || p.BlockedLabelID != blocked.ID || !p.Inherits.MatchLabels {
			t.Fatalf("resolved project: %+v", p)
		}
		want := []map[string]any{{"labels": map[string]any{"some": map[string]any{"id": map[string]any{"eq": ready.ID}}}}}
		if got := linear.BuildIssueFilter(p, "", "")["and"]; !reflect.DeepEqual(got, want) {
			t.Fatalf("filter = %v, want %v", got, want)
		}
	}
	if !slices.Equal(cfg.Defaults.MatchLabels, []string{"agent/agent-ready"}) {
		t.Fatal("resolution changed the shared defaults")
	}
	if calls := f.CallNames(); !slices.Equal(calls, []string{"Labels", "WorkspaceLabels", "Labels"}) {
		t.Fatalf("calls = %v", calls)
	}
}

func TestResolveLabelRefsCombinedCatalog(t *testing.T) {
	team := linear.Label{ID: "11111111-1111-1111-1111-111111111111", Name: "ready", Parent: &linear.Label{Name: "team"}}
	workspace := linear.Label{ID: "22222222-2222-2222-2222-222222222222", Name: "ready", Parent: &linear.Label{Name: "workspace"}}
	f := &linear.Fake{LabelsByTeam: map[string][]linear.Label{"t1": {team}}, WorkspaceLabelSet: []linear.Label{workspace}}
	for _, tc := range []struct {
		ref   string
		want  []string
		notes int
	}{
		{"*/ready", []string{team.ID, workspace.ID}, 0},
		{"ready", []string{"ready"}, 1}, // no guessing across scopes
		{"workspace/ready", []string{workspace.ID}, 0},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			cfg := &config.Config{Projects: []config.Project{{Name: "a", TeamID: "t1", MatchLabels: []string{tc.ref}}}}
			notes, _ := resolveLabelRefs(context.Background(), cfg, f, nil)
			if !slices.Equal(cfg.Projects[0].MatchLabels, tc.want) || len(notes) != tc.notes {
				t.Fatalf("labels = %v, notes = %v", cfg.Projects[0].MatchLabels, notes)
			}
		})
	}
	// A workspace lookup failure must not silently narrow a wildcard to the
	// team half of the catalog, even across repeated reloads.
	f.Errs = map[string]error{"WorkspaceLabels": errors.New("offline")}
	memo := labelMemo{memoKey("t1", "*/ready"): {team.ID, workspace.ID}}
	for range 2 {
		cfg := &config.Config{Projects: []config.Project{
			{Name: "a", TeamID: "t1", MatchLabels: []string{"*/ready"}},
			{Name: "b", TeamID: "t2", MatchLabels: []string{"workspace/ready"}},
		}}
		f.Calls = nil
		var notes []string
		notes, memo = resolveLabelRefs(context.Background(), cfg, f, memo)
		if !slices.Equal(cfg.Projects[0].MatchLabels, []string{team.ID, workspace.ID}) || cfg.Projects[1].MatchLabels[0] != "workspace/ready" || len(notes) != 1 {
			t.Fatalf("projects = %+v, notes = %v", cfg.Projects, notes)
		}
		if calls := f.CallNames(); !slices.Equal(calls, []string{"Labels", "WorkspaceLabels", "Labels"}) {
			t.Fatalf("calls = %v", calls)
		}
	}
}

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
	if notes, _ := resolveLabelRefs(context.Background(), cfg, f, nil); len(notes) != 0 {
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
	notes, _ := resolveLabelRefs(context.Background(), cfg, f, nil)
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

func TestResolveLabelRefsLookupFailureCachedAndMemoKept(t *testing.T) {
	good := "11111111-1111-1111-1111-111111111111"
	f := &linear.Fake{LabelsByTeam: map[string][]linear.Label{"t1": {{ID: good, Name: "bug"}}}}
	mk := func() *config.Config {
		return &config.Config{Projects: []config.Project{
			{Name: "a", TeamID: "t1", MatchLabels: []string{"bug"}, OnSentSetLabel: "bug2"},
			{Name: "b", TeamID: "t1", MatchLabels: []string{"bug"}},
		}}
	}
	_, memo := resolveLabelRefs(context.Background(), mk(), f, nil)
	if len(memo) == 0 {
		t.Fatal("no memo after a good lookup")
	}
	// Linear now fails: the last good id holds, and the team is asked once.
	f.Errs = map[string]error{"Labels": errors.New("boom")}
	f.Calls = nil
	cfg := mk()
	notes, _ := resolveLabelRefs(context.Background(), cfg, f, memo)
	if cfg.Projects[0].MatchLabels[0] != good || cfg.Projects[1].MatchLabels[0] != good {
		t.Fatalf("memo not kept: %v %v", cfg.Projects[0].MatchLabels, cfg.Projects[1].MatchLabels)
	}
	if n := len(f.CallNames()); n != 1 || len(notes) != 1 {
		t.Fatalf("Labels calls=%d notes=%v, want one each", n, notes)
	}
	// A different reference has no memo: it stays a name.
	cfg = &config.Config{Projects: []config.Project{{Name: "c", TeamID: "t1", MatchLabels: []string{"other"}}}}
	resolveLabelRefs(context.Background(), cfg, f, memo)
	if cfg.Projects[0].MatchLabels[0] != "other" {
		t.Fatalf("got %v", cfg.Projects[0].MatchLabels)
	}
}

func TestResolveLabelRefsRefusesTriggerEqualsSent(t *testing.T) {
	id := "11111111-1111-1111-1111-111111111111"
	f := &linear.Fake{LabelsByTeam: map[string][]linear.Label{"t1": {{ID: id, Name: "agent-ready", Parent: &linear.Label{Name: "agent"}}}}}
	cfg := &config.Config{Projects: []config.Project{
		{Name: "a", TeamID: "t1", DedupMode: "label", MatchLabels: []string{"*/agent-ready"}, OnSentSetLabel: "agent/agent-ready"},
	}}
	notes, _ := resolveLabelRefs(context.Background(), cfg, f, nil)
	p := cfg.Projects[0]
	if p.MatchLabels[0] != "*/agent-ready" || p.OnSentSetLabel != "agent/agent-ready" || len(notes) != 1 {
		t.Fatalf("project = %+v notes=%v", p, notes)
	}
}
