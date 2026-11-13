package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/protocol"
)

func TestGoalOverviewIsCachedAndProjectScoped(t *testing.T) {
	d, _, draft := goalFixture(t)
	g, err := d.createGoal(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	// Rendering is independent of Linear and has no write-back side effects.
	d.lin = nil
	res := d.handleGoals(context.Background(), protocol.Request{Cmd: "goalOverview", Args: json.RawMessage(`{"project":"p1"}`)})
	var rows []goal.Overview
	if !res.OK || json.Unmarshal(res.Data, &rows) != nil || len(rows) != 1 || rows[0].ID != g.ID {
		t.Fatalf("overview: %+v", res)
	}
	after, _ := d.goals.Get(g.ID)
	if after.Revision != g.Revision {
		t.Fatal("read wrote goal")
	}
	res = d.handleGoals(context.Background(), protocol.Request{Cmd: "goalOverview", Args: json.RawMessage(`{"project":"other"}`)})
	if !res.OK || string(res.Data) != "[]" {
		t.Fatalf("scope leaked: %s", res.Data)
	}
}
