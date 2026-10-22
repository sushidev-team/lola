package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/protocol"
)

func TestGoalRecoveryBridge(t *testing.T) {
	brief := goal.BriefAcceptance{ID: "g", Revision: 17, SourceID: "reviewed-source", Contract: goal.BriefContract{Title: "Clarified goal", Outcome: "Existing scope", Acceptance: []string{"Existing tasks cover this clarification"}}, Reason: "Reviewed retained plan"}
	progress := goal.ProgressResolution{ID: "g", Revision: 18, UpdateID: "pending-update", Reason: "Checked remote; retain unknown outcome"}
	evidence := goal.DeliveryEvidenceRequest{ID: "g", Revision: 19, URL: "https://artifacts.example/run"}
	f := startFakeDaemon(t, func(req protocol.Request) protocol.Response {
		if req.Cmd == "goalRecovery" || req.Cmd == "goalSyncBrief" {
			var args protocol.GoalArgs
			if err := json.Unmarshal(req.Args, &args); err != nil || args.ID != "g" {
				t.Fatal("lost goal identity", err)
			}
			if req.Cmd == "goalRecovery" {
				body, _ := json.Marshal(goal.RecoveryView{Goal: goal.Overview{ID: "g", Revision: 17}, PendingBrief: &goal.BriefSource{ID: brief.SourceID, Description: "Exact human text"}})
				return protocol.Response{OK: true, Data: body}
			}
		} else {
			var want any
			switch req.Cmd {
			case "goalAcceptBrief":
				want = brief
			case "goalDismissProgress":
				want = progress
			case "goalDeliveryEvidence":
				want = evidence
			default:
				t.Fatalf("unexpected command %s", req.Cmd)
			}
			b, _ := json.Marshal(want)
			var actual, expected any
			_ = json.Unmarshal(req.Args, &actual)
			_ = json.Unmarshal(b, &expected)
			if !reflect.DeepEqual(actual, expected) {
				t.Fatal("changed reviewed request", actual, expected)
			}
		}
		return protocol.Response{Error: "response uncertain; reload before deciding again"}
	})
	s := &DaemonService{}
	v, err := s.GoalRecovery("g")
	if err != nil || v.Goal.Revision != 17 || v.PendingBrief.Description != "Exact human text" {
		t.Fatal("recovery view lost", v, err)
	}
	for _, err := range []error{s.GoalSyncBrief("g"), s.GoalAcceptBrief(brief), s.GoalDismissProgress(progress), s.GoalDeliveryEvidence(evidence)} {
		if err == nil {
			t.Fatal("uncertain effect reported as success")
		}
	}
	if len(f.requests()) != 5 {
		t.Fatal("bridge retried an uncertain effect")
	}
}
