package main

import (
	"encoding/json"
	"testing"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/protocol"
)

func TestGoalService(t *testing.T) {
	f := startFakeDaemon(t, func(req protocol.Request) protocol.Response {
		if req.Cmd == "goalOverview" {
			var args map[string]string
			_ = json.Unmarshal(req.Args, &args)
			if args["project"] != "nori" {
				t.Error("lost project scope")
			}
			body, _ := json.Marshal([]goal.Overview{{ID: "g", Revision: 7}})
			return protocol.Response{OK: true, Data: body}
		}
		var a goal.ControlRequest
		_ = json.Unmarshal(req.Args, &a)
		if req.Cmd != "goalControl" || a.ID != "g" || a.Revision != 7 || a.Action != "pause" || a.Reason != "review output" {
			t.Errorf("wrong control: %+v", a)
		}
		return protocol.Response{Error: "goal changed; reload before steering"}
	})
	s := &DaemonService{}
	result, err := s.GoalOverview("nori")
	if err != nil || len(result) != 1 || result[0].Revision != 7 {
		t.Fatalf("read: %+v %v", result, err)
	}
	if err = s.GoalControl("g", 7, "pause", "review output"); err == nil {
		t.Fatal("stale error swallowed")
	}
	if len(f.requests()) != 2 {
		t.Fatal("automatically retried stale action")
	}
}
