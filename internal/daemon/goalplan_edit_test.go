package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalplan"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
)

func TestGoalPlanEditIsLocalAndPinsAcceptance(t *testing.T) {
	d, f, request, calls := goalPlanFixture(t)
	ctx := context.Background()
	r, err := d.proposeGoal(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	a := goalplan.EditRequest{ID: r.ID, Project: request.Project, Revision: r.Revision, EditID: goal.NewID(), Reason: "Separate invitation delivery from the acceptance regression", Output: *r.Output}
	task := a.Output.Tasks[0]
	task.Title = "Implement invitation API"
	task.Paths = map[string][]string{"p1": {"src/invitations"}}
	regression := task
	regression.Title = "Test invitation acceptance"
	regression.Paths = map[string][]string{"p1": {"tests/invitations"}}
	regression.BlockedBy = []int{1}
	a.Output.Tasks = []goalplan.ProposedTask{task, regression}
	body, _ := json.Marshal(a)
	resp := d.handle(ctx, protocol.Request{Cmd: "goalEditPlan", Args: body})
	if !resp.OK {
		t.Fatal(resp.Error)
	}
	var edited goalplan.Record
	if err = json.Unmarshal(resp.Data, &edited); err != nil {
		t.Fatal(err)
	}
	if len(edited.Output.Tasks) != 2 || len(edited.ModelOutput.Tasks) != 1 || len(d.goals.List()) != 0 || len(f.Created) != 0 || *calls != 1 {
		t.Fatal("edit exceeded local proposal authority")
	}
	if _, err = d.acceptGoalPlan(ctx, goalPlanArgs{r.ID, r.Revision}); err == nil {
		t.Fatal("pre-edit review accepted")
	}
	g, err := d.acceptGoalPlan(ctx, goalPlanArgs{edited.ID, edited.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Tasks) != 2 || g.State != "draft" || g.Execution != nil || len(f.Created) != 0 || *calls != 1 {
		t.Fatal("acceptance changed intended authority")
	}
	if replay := d.handle(ctx, protocol.Request{Cmd: "goalEditPlan", Args: body}); !replay.OK {
		t.Fatal("lost-response replay failed after acceptance", replay.Error)
	}
	a.EditID = goal.NewID()
	a.Revision = edited.Revision + 1
	body, _ = json.Marshal(a)
	if resp = d.handle(ctx, protocol.Request{Cmd: "goalEditPlan", Args: body}); resp.OK {
		t.Fatal("accepted plan editable")
	}
}
func TestGoalPlanEditRejectsAcceptanceCrashAndRoutingDrift(t *testing.T) {
	for _, mode := range []string{"accepted crash", "labels", "project", "repair"} {
		t.Run(mode, func(t *testing.T) {
			d, f, request, calls := goalPlanFixture(t)
			ctx := context.Background()
			r, err := d.proposeGoal(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			a := goalplan.EditRequest{ID: r.ID, Project: request.Project, Revision: r.Revision, EditID: goal.NewID(), Reason: "Clarify the task before accepting its graph", Output: *r.Output}
			a.Output.Tasks = append([]goalplan.ProposedTask(nil), a.Output.Tasks...)
			a.Output.Tasks[0].Title = "Revised invitation task"
			switch mode {
			case "accepted crash":
				if _, err = d.acceptGoalPlan(ctx, goalPlanArgs{r.ID, r.Revision}); err != nil {
					t.Fatal(err)
				}
				// Persist the pre-acknowledgment proposal beside the already durable goal.
				b, _ := json.Marshal(struct {
					Version int                        `json:"version"`
					Records map[string]goalplan.Record `json:"records"`
				}{1, map[string]goalplan.Record{r.ID: r}})
				file := filepath.Join(d.home, "state", "goal-plans.json")
				if err = os.WriteFile(file, b, 0600); err != nil {
					t.Fatal(err)
				}
				d.goalPlans, err = goalplan.Open(file)
				if err != nil {
					t.Fatal(err)
				}
			case "labels":
				f.LabelsByTeam[r.Input.TeamID] = []linear.Label{{ID: "unrelated", Name: "Unrelated"}}
			case "project":
				a.Project = "p2"
			case "repair":
				r.Input.Repair = &goalplan.RepairContext{GoalID: r.GoalID}
				r.InputSHA = goalplan.Digest(r.Input)
				b, _ := json.Marshal(struct {
					Version int                        `json:"version"`
					Records map[string]goalplan.Record `json:"records"`
				}{1, map[string]goalplan.Record{r.ID: r}})
				file := filepath.Join(d.home, "state", "goal-plans.json")
				_ = os.WriteFile(file, b, 0600)
				d.goalPlans, err = goalplan.Open(file)
				if err != nil {
					t.Fatal(err)
				}
			}
			before, _ := d.goalPlans.Get(r.ID)
			if _, err = d.editGoalPlan(ctx, a); err == nil {
				t.Fatal("unsafe edit accepted")
			}
			after, _ := d.goalPlans.Get(r.ID)
			if after.Revision != before.Revision || len(after.Edits) != 0 || *calls != 1 || len(f.Created) != 0 {
				t.Fatal("rejected edit changed authoritative state")
			}
		})
	}
}
