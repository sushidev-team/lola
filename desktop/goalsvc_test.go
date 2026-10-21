package main

import (
	"encoding/json"
	"testing"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalplan"
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

func TestGoalPlanBridge(t *testing.T) {
	f := startFakeDaemon(t, func(req protocol.Request) protocol.Response {
		var a struct {
			ID       string `json:"id"`
			Project  string `json:"project"`
			Revision uint64 `json:"revision"`
		}
		if err := json.Unmarshal(req.Args, &a); err != nil {
			t.Fatal(err)
		}
		switch req.Cmd {
		case "goalProposals":
			if a.Project != "nori" {
				t.Fatal("lost project scope")
			}
			return protocol.Response{OK: true, Data: json.RawMessage(`[]`)}
		case "goalPropose", "goalProposal":
			if a.ID != "retained-id" {
				t.Fatal("lost proposal identity")
			}
			return protocol.Response{Error: "response uncertain"}
		case "goalAcceptPlan":
			if a.ID != "retained-id" || a.Revision != 9 {
				t.Fatal("lost reviewed revision")
			}
			return protocol.Response{Error: "stale proposal"}
		default:
			t.Fatalf("unexpected command %s", req.Cmd)
		}
		return protocol.Response{}
	})
	s := &DaemonService{}
	if _, err := s.GoalProposals("nori"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GoalPropose(goalplan.Request{ID: "retained-id", Project: "nori"}); err == nil {
		t.Fatal("lost uncertain failure")
	}
	if _, err := s.GoalProposal("retained-id"); err == nil {
		t.Fatal("lost read failure")
	}
	if _, err := s.GoalAcceptPlan("retained-id", 9); err == nil {
		t.Fatal("lost stale error")
	}
	if len(f.requests()) != 4 {
		t.Fatal("unexpected retries")
	}
}

func TestGoalStartBridge(t *testing.T) {
	f := startFakeDaemon(t, func(req protocol.Request) protocol.Response {
		switch req.Cmd {
		case "goalStartOptions":
			var a protocol.GoalArgs
			if err := json.Unmarshal(req.Args, &a); err != nil || a.ID != "g" {
				t.Fatal("lost goal identity")
			}
			body, _ := json.Marshal(goal.StartOptions{Authorization: goal.Authorization{ID: "g", Revision: 12}})
			return protocol.Response{OK: true, Data: body}
		case "goalStart":
			var a goal.Authorization
			if err := json.Unmarshal(req.Args, &a); err != nil || a.ID != "g" || a.Revision != 12 || a.Policy.Limits.Tokens != 12345 || a.Policy.PublishPR {
				t.Fatal("changed reviewed authorization")
			}
			return protocol.Response{Error: "uncertain start"}
		case "goalPublishReviewed":
			var a goal.Authorization
			if err := json.Unmarshal(req.Args, &a); err != nil || a.ID != "g" || a.Revision != 12 {
				t.Fatal("changed publication revision")
			}
			return protocol.Response{Error: "stale goal"}
		}
		t.Fatal("unexpected command")
		return protocol.Response{}
	})
	s := &DaemonService{}
	options, err := s.GoalStartOptions("g")
	if err != nil {
		t.Fatal(err)
	}
	options.Authorization.Policy.Limits.Tokens = 12345
	if err = s.GoalStart(options.Authorization); err == nil {
		t.Fatal("lost start error")
	}
	if err = s.GoalPublish("g", 12); err == nil {
		t.Fatal("lost publication error")
	}
	if len(f.requests()) != 3 {
		t.Fatal("automatically repeated operation")
	}
}
