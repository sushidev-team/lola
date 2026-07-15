package linear

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
)

// The fake must remain a drop-in for the real client everywhere the daemon
// takes a linear.API.
var _ API = (*Fake)(nil)

func TestFakeRecordsSetIssueLabelsCalls(t *testing.T) {
	ctx := context.Background()
	f := &Fake{
		LabelIDsByIssue: map[string][]string{
			"uuid-1": {"lbl-trigger", "lbl-keep"},
		},
	}

	labels := []string{"lbl-keep", "lbl-agent"}
	if err := f.SetIssueLabels(ctx, "uuid-1", labels); err != nil {
		t.Fatalf("SetIssueLabels: %v", err)
	}

	log := f.CallLog()
	if len(log) != 1 {
		t.Fatalf("CallLog len = %d, want 1", len(log))
	}
	call := log[0]
	if call.Method != "SetIssueLabels" {
		t.Errorf("Method = %q, want SetIssueLabels", call.Method)
	}
	if len(call.Args) != 2 {
		t.Fatalf("Args len = %d, want 2 (uuid, labelIDs)", len(call.Args))
	}
	if call.Args[0] != "uuid-1" {
		t.Errorf("Args[0] = %v, want uuid-1", call.Args[0])
	}
	if got, ok := call.Args[1].([]string); !ok || !reflect.DeepEqual(got, []string{"lbl-keep", "lbl-agent"}) {
		t.Errorf("Args[1] = %#v, want [lbl-keep lbl-agent]", call.Args[1])
	}

	// The recorded args must be a snapshot: mutating the caller's slice
	// afterwards must not rewrite history.
	labels[0] = "mutated"
	if got := f.CallLog()[0].Args[1].([]string); got[0] != "lbl-keep" {
		t.Errorf("recorded args aliased caller slice: %v", got)
	}

	// The fake's label store observes the delta so a follow-up
	// IssueLabelIDs read returns the new state.
	got, err := f.IssueLabelIDs(ctx, "uuid-1")
	if err != nil {
		t.Fatalf("IssueLabelIDs: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"lbl-keep", "lbl-agent"}) {
		t.Errorf("labels after set = %v, want [lbl-keep lbl-agent]", got)
	}

	if names := f.CallNames(); !reflect.DeepEqual(names, []string{"SetIssueLabels", "IssueLabelIDs"}) {
		t.Errorf("CallNames = %v", names)
	}
}

func TestFakeInjectedErrorStillRecordsCall(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	f := &Fake{
		LabelIDsByIssue: map[string][]string{"uuid-1": {"lbl-old"}},
		Errs:            map[string]error{"SetIssueLabels": boom},
	}

	err := f.SetIssueLabels(ctx, "uuid-1", []string{"lbl-new"})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want injected boom", err)
	}

	// Failed calls are still logged...
	if names := f.CallNames(); !reflect.DeepEqual(names, []string{"SetIssueLabels"}) {
		t.Errorf("CallNames = %v, want the failed call recorded", names)
	}
	// ...but the label store must not change on failure.
	delete(f.Errs, "SetIssueLabels")
	got, err := f.IssueLabelIDs(ctx, "uuid-1")
	if err != nil {
		t.Fatalf("IssueLabelIDs: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"lbl-old"}) {
		t.Errorf("labels after failed set = %v, want unchanged [lbl-old]", got)
	}
}

func TestFakeRecordsCommentAndStateCalls(t *testing.T) {
	ctx := context.Background()
	f := &Fake{}

	if err := f.CreateComment(ctx, "uuid-1", "working"); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if err := f.SetIssueState(ctx, "uuid-1", "state-progress"); err != nil {
		t.Fatalf("SetIssueState: %v", err)
	}
	if err := f.CreateComment(ctx, "uuid-1", "approved"); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}

	log := f.CallLog()
	if len(log) != 3 {
		t.Fatalf("CallLog len = %d, want 3", len(log))
	}

	// CreateComment records method + (uuid, body) in order.
	if log[0].Method != "CreateComment" ||
		log[0].Args[0] != "uuid-1" || log[0].Args[1] != "working" {
		t.Errorf("log[0] = %+v, want CreateComment(uuid-1, working)", log[0])
	}
	// SetIssueState records method + (uuid, stateId).
	if log[1].Method != "SetIssueState" ||
		log[1].Args[0] != "uuid-1" || log[1].Args[1] != "state-progress" {
		t.Errorf("log[1] = %+v, want SetIssueState(uuid-1, state-progress)", log[1])
	}
	if log[2].Method != "CreateComment" || log[2].Args[1] != "approved" {
		t.Errorf("log[2] = %+v, want CreateComment(..., approved)", log[2])
	}

	// Observation stores let tests assert one-comment-per-transition and the
	// exact stateId without replaying the call log.
	if got := f.CommentsByIssue["uuid-1"]; !reflect.DeepEqual(got, []string{"working", "approved"}) {
		t.Errorf("CommentsByIssue = %v, want [working approved]", got)
	}
	if got := f.StateByIssue["uuid-1"]; got != "state-progress" {
		t.Errorf("StateByIssue = %q, want state-progress", got)
	}
}

func TestFakeCommentAndStateInjectedErrors(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	f := &Fake{Errs: map[string]error{
		"CreateComment": boom,
		"SetIssueState": boom,
	}}

	if err := f.CreateComment(ctx, "uuid-1", "body"); !errors.Is(err, boom) {
		t.Fatalf("CreateComment err = %v, want injected boom", err)
	}
	if err := f.SetIssueState(ctx, "uuid-1", "state-x"); !errors.Is(err, boom) {
		t.Fatalf("SetIssueState err = %v, want injected boom", err)
	}

	// Failed calls are still logged...
	if names := f.CallNames(); !reflect.DeepEqual(names, []string{"CreateComment", "SetIssueState"}) {
		t.Errorf("CallNames = %v, want both failed calls recorded", names)
	}
	// ...but the observation stores must not change on failure.
	if len(f.CommentsByIssue["uuid-1"]) != 0 {
		t.Errorf("CommentsByIssue = %v, want empty after failed CreateComment", f.CommentsByIssue["uuid-1"])
	}
	if _, ok := f.StateByIssue["uuid-1"]; ok {
		t.Errorf("StateByIssue mutated on failed SetIssueState: %v", f.StateByIssue)
	}
}

func TestFakeMatchingIssuesFixtures(t *testing.T) {
	ctx := context.Background()

	t.Run("static issues", func(t *testing.T) {
		f := &Fake{Issues: []Issue{{ID: "uuid-1", Identifier: "FE-1"}}}
		got, err := f.MatchingIssues(ctx, basePoll(), "cyc", "viewer")
		if err != nil {
			t.Fatalf("MatchingIssues: %v", err)
		}
		if len(got) != 1 || got[0].Identifier != "FE-1" {
			t.Errorf("issues = %#v", got)
		}
	})

	t.Run("IssuesFunc wins and receives args", func(t *testing.T) {
		var gotCycle, gotViewer string
		f := &Fake{
			Issues: []Issue{{Identifier: "STATIC-IGNORED"}},
			IssuesFunc: func(p config.Poll, activeCycleID, viewerID string) ([]Issue, error) {
				gotCycle, gotViewer = activeCycleID, viewerID
				return []Issue{{Identifier: "FN-1"}}, nil
			},
		}
		got, err := f.MatchingIssues(ctx, basePoll(), "cyc-9", "viewer-9")
		if err != nil {
			t.Fatalf("MatchingIssues: %v", err)
		}
		if len(got) != 1 || got[0].Identifier != "FN-1" {
			t.Errorf("issues = %#v, want IssuesFunc result to win over static", got)
		}
		if gotCycle != "cyc-9" || gotViewer != "viewer-9" {
			t.Errorf("IssuesFunc args = (%q,%q), want (cyc-9,viewer-9)", gotCycle, gotViewer)
		}
	})
}
