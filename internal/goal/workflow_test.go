package goal

import (
	"path/filepath"
	"testing"
	"time"
)

func TestWorkflowProjectsFactsWithoutGrantingThem(t *testing.T) {
	g := executable(t)
	id := g.Tasks[0].ID
	if g.WorkflowStage(id) != "" {
		t.Fatal("pending task was started")
	}
	a, err := g.ReserveAttempt(id, "a", "s", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if g.WorkflowStage(id) != "" {
		t.Fatal("reservation mistaken for a running worker")
	}
	a.State = "running"
	g.Execution.Attempts[a.ID] = a
	if g.WorkflowStage(id) != "started" {
		t.Fatal("running task missing")
	}
	for _, state := range []string{"paused", "canceled", "blocked"} {
		g.State = state
		if g.WorkflowStage(id) != "" {
			t.Fatal("steering did not hold writes")
		}
	}
	g.State = "running"
	p := g.Execution.Tasks[id]
	p.State = "integrated"
	g.Execution.Tasks[id] = p
	if g.WorkflowStage(id) != "integrated" || g.TaskSatisfied(g.Tasks[1].ID) {
		t.Fatal("integration confused with delivery/dependency proof")
	}
	g.State = "accepted"
	if g.WorkflowStage(id) != "delivered" {
		t.Fatal("accepted delivery missing")
	}
	p.ReplacedBy = []string{"replacement"}
	g.Execution.Tasks[id] = p
	if g.WorkflowStage(id) != "" {
		t.Fatal("superseded task marked delivered")
	}
}
func TestWorkflowIntentSurvivesRestartAndCannotBeRewritten(t *testing.T) {
	g := readyRefreshGoal(t)
	store, _ := Open(filepath.Join(t.TempDir(), "goals.json"))
	g, err := store.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err = g.ReserveWorkflow(g.Tasks[0].ID, "integrated", "todo", "review", now); err != nil {
		t.Fatal(err)
	}
	g, err = store.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	g.Workflow[0].SentAt = now
	g, err = store.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(store.path)
	if err != nil {
		t.Fatal(err)
	}
	g, _ = reopened.Get(g.ID)
	if err = g.ReserveWorkflow(g.Tasks[0].ID, "integrated", "todo", "different", now); err == nil {
		t.Fatal("uncertain write replaced")
	}
	changed := copyGoal(g)
	changed.Workflow[0].To = "done"
	if _, err = store.Save(changed); err == nil {
		t.Fatal("target silently changed")
	}
	g.Workflow[0].ConfirmedAt = now
	g, err = store.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	g.Workflow[0].ConfirmedAt = time.Time{}
	if _, err = store.Save(g); err == nil {
		t.Fatal("confirmation removed")
	}
}
