package linear

import (
	"reflect"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
)

// basePoll returns the minimal poll every filter test starts from:
// team only, no project, no cycle, no states, no labels, assignee anyone.
func basePoll() config.Poll {
	return config.Poll{
		Name:         "p",
		TeamID:       "team-1",
		CycleMode:    "none",
		MatchMode:    "any",
		AssigneeMode: "anyone",
	}
}

func idEq(id string) map[string]any {
	return map[string]any{"id": map[string]any{"eq": id}}
}

func TestBuildIssueFilterMatrix(t *testing.T) {
	const (
		activeCycleID = "cycle-active"
		viewerID      = "user-viewer"
	)

	tests := []struct {
		name string
		mut  func(*config.Poll)
		want map[string]any
	}{
		{
			name: "team only",
			mut:  func(p *config.Poll) {},
			want: map[string]any{"team": idEq("team-1")},
		},
		{
			name: "project set",
			mut:  func(p *config.Poll) { p.ProjectID = "proj-9" },
			want: map[string]any{
				"team":    idEq("team-1"),
				"project": idEq("proj-9"),
			},
		},
		{
			name: "cycle active uses activeCycleID not p.CycleID",
			mut: func(p *config.Poll) {
				p.CycleMode = "active"
				p.CycleID = "cycle-pinned" // must be ignored in active mode
			},
			want: map[string]any{
				"team":  idEq("team-1"),
				"cycle": idEq(activeCycleID),
			},
		},
		{
			name: "cycle pinned uses p.CycleID",
			mut: func(p *config.Poll) {
				p.CycleMode = "pinned"
				p.CycleID = "cycle-pinned"
			},
			want: map[string]any{
				"team":  idEq("team-1"),
				"cycle": idEq("cycle-pinned"),
			},
		},
		{
			name: "states non-empty",
			mut:  func(p *config.Poll) { p.StateIDs = []string{"st-1", "st-2"} },
			want: map[string]any{
				"team":  idEq("team-1"),
				"state": map[string]any{"id": map[string]any{"in": []string{"st-1", "st-2"}}},
			},
		},
		{
			name: "labels match_mode any",
			mut: func(p *config.Poll) {
				p.MatchLabels = []string{"lbl-a", "lbl-b"}
				p.MatchMode = "any"
			},
			want: map[string]any{
				"team": idEq("team-1"),
				"labels": map[string]any{
					"some": map[string]any{"id": map[string]any{"in": []string{"lbl-a", "lbl-b"}}},
				},
			},
		},
		{
			name: "labels match_mode all",
			mut: func(p *config.Poll) {
				p.MatchLabels = []string{"lbl-a", "lbl-b"}
				p.MatchMode = "all"
			},
			want: map[string]any{
				"team": idEq("team-1"),
				"and": []map[string]any{
					{"labels": map[string]any{"some": idEq("lbl-a")}},
					{"labels": map[string]any{"some": idEq("lbl-b")}},
				},
			},
		},
		{
			name: "assignee me uses viewerID",
			mut:  func(p *config.Poll) { p.AssigneeMode = "me" },
			want: map[string]any{
				"team":     idEq("team-1"),
				"assignee": idEq(viewerID),
			},
		},
		{
			name: "assignee user uses AssigneeUserID",
			mut: func(p *config.Poll) {
				p.AssigneeMode = "user"
				p.AssigneeUserID = "user-42"
			},
			want: map[string]any{
				"team":     idEq("team-1"),
				"assignee": idEq("user-42"),
			},
		},
		{
			name: "everything combined",
			mut: func(p *config.Poll) {
				p.ProjectID = "proj-9"
				p.CycleMode = "active"
				p.StateIDs = []string{"st-1"}
				p.MatchLabels = []string{"lbl-a"}
				p.MatchMode = "any"
				p.AssigneeMode = "me"
			},
			want: map[string]any{
				"team":    idEq("team-1"),
				"project": idEq("proj-9"),
				"cycle":   idEq(activeCycleID),
				"state":   map[string]any{"id": map[string]any{"in": []string{"st-1"}}},
				"labels": map[string]any{
					"some": map[string]any{"id": map[string]any{"in": []string{"lbl-a"}}},
				},
				"assignee": idEq(viewerID),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := basePoll()
			tt.mut(&p)
			got := BuildIssueFilter(p, activeCycleID, viewerID)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("BuildIssueFilter mismatch\n got:  %#v\n want: %#v", got, tt.want)
			}
		})
	}
}

// Omission cases the matrix above implies but should fail loudly on their own.
func TestBuildIssueFilterOmissions(t *testing.T) {
	p := basePoll() // cycle none, no project, no states, no labels, anyone
	f := BuildIssueFilter(p, "cycle-active", "user-viewer")

	for _, key := range []string{"project", "cycle", "state", "labels", "and", "assignee"} {
		if _, ok := f[key]; ok {
			t.Errorf("filter must omit %q for the base poll, got %#v", key, f[key])
		}
	}
	if len(f) != 1 {
		t.Errorf("base poll filter should contain only team, got %d keys: %#v", len(f), f)
	}
}
