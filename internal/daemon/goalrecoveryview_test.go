package daemon

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/protocol"
)

func TestGoalRecoveryReadsOnlyDurableSnapshot(t *testing.T) {
	d, api, draft := goalFixture(t)
	g, err := d.createGoal(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	source, err := goal.NewBriefSource("Human clarification", "Exact Linear discussion", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err = g.ObserveBrief(source); err != nil {
		t.Fatal(err)
	}
	g, err = d.goals.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	// A canceled caller can still inspect local state without refreshing Linear.
	calls := len(api.CallLog())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	args, _ := json.Marshal(protocol.GoalArgs{ID: g.ID})
	resp := d.handle(ctx, protocol.Request{Cmd: "goalRecovery", Args: args})
	if !resp.OK {
		t.Fatal(resp.Error)
	}
	var v goal.RecoveryView
	if err = json.Unmarshal(resp.Data, &v); err != nil || v.Goal.Revision != g.Revision || v.PendingBrief == nil || v.PendingBrief.ID != source.ID || v.PendingBrief.Description != source.Description {
		t.Fatal("lost retained source", v, err)
	}
	after, _ := d.goals.Get(g.ID)
	if !reflect.DeepEqual(g, after) {
		t.Fatal("read changed goal revision or decisions")
	}
	if len(api.CallLog()) != calls {
		t.Fatal("recovery snapshot contacted Linear")
	}
	for _, raw := range []string{`{"id":"missing"}`, `{`} {
		resp = d.handle(ctx, protocol.Request{Cmd: "goalRecovery", Args: json.RawMessage(raw)})
		if resp.OK || resp.Error == "" {
			t.Fatal("invalid recovery query silently succeeded")
		}
	}
}
