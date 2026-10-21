package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/reviewprofile"
)

func TestGoalStartOptionsAreReadOnly(t *testing.T) {
	d, f, g, policy, launched := goalExecutionFixture(t)
	d.cfg.ReviewProfiles = []reviewprofile.Profile{
		{ID: "backend", Kind: "backend", Enabled: true, Required: true},
		{ID: "uiux", Kind: "uiux", Enabled: true},
		{ID: "other", Kind: "security", Enabled: true, Required: true, Projects: []string{"other"}},
		{ID: "disabled", Kind: "backend", Required: true},
	}
	before := len(f.Created)
	args, _ := json.Marshal(protocol.GoalArgs{ID: g.ID})
	response := d.handle(context.Background(), protocol.Request{Cmd: "goalStartOptions", Args: args})
	if response.Error != "" {
		t.Fatal(response.Error)
	}
	var out goal.StartOptions
	if err := json.Unmarshal(response.Data, &out); err != nil {
		t.Fatal(err)
	}
	a := out.Authorization
	if a.ID != g.ID || a.Revision != g.Revision || len(a.Policy.Members) != 1 {
		t.Fatal("wrong goal/revision/members")
	}
	if a.Policy.Members[0].Source != policy.Members[0].Source || a.Policy.Members[0].Target != "main" {
		t.Fatal("lost configured source or target")
	}
	if len(a.Policy.Checks) != 0 || a.Policy.PublishPR || a.Policy.Replan || len(a.Policy.Profiles) != 1 || a.Policy.Profiles[0] != "backend" || len(out.Profiles) != 2 {
		t.Fatal("invented authority or lost required reviewers")
	}
	if out.Profiles[1].ID != "uiux" || len(out.Profiles[1].Artifacts) == 0 {
		t.Fatal("missing specialist evidence requirements")
	}
	after, _ := d.goals.Get(g.ID)
	if after.Execution != nil || after.Revision != g.Revision || len(f.Created) != before || *launched != 0 {
		t.Fatal("options triggered work")
	}
	d.cfg.ProjectByName(g.Project).GoalMode = false
	if _, err := d.goalStartOptions(g.ID); err == nil {
		t.Fatal("ignored goal availability")
	}
}
