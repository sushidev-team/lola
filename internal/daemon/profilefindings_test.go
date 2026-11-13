package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/reviewprofile"
)

func TestProfileFindingDecisionsDoNotOpenDeliveryGate(t *testing.T) {
	d, s := profileFixture(t)
	d.profileRun = func(context.Context, string, reviewprofile.Profile, reviewprofile.Inputs) (reviewprofile.Result, error) {
		return reviewprofile.Result{Complete: true, Evidence: []reviewprofile.Evidence{{Criterion: "Atomic save", Observation: "Read and write are not serialized"}}, Findings: []reviewprofile.Finding{{Requirement: "No lost writes", Location: "store.go:10", Reproduction: "Run concurrent saves", Evidence: "One update disappears", Severity: "major", Confidence: "verified", Fix: "Serialize read and write"}}}, nil
	}
	record, err := d.runProfile(context.Background(), profileArgs{Session: s.ID, Profile: "backend", Contract: "No lost writes"})
	if err != nil {
		t.Fatal(err)
	}
	request := func(cmd string, args any) protocol.Response {
		t.Helper()
		body, _ := json.Marshal(args)
		return d.handle(context.Background(), protocol.Request{Cmd: cmd, Args: body})
	}
	if resp := request("profileFindings", nil); !resp.OK {
		t.Fatalf("findings: %+v", resp)
	}
	findings := d.profileReviews.Findings()
	if len(findings) != 1 {
		t.Fatal("missing durable finding")
	}
	f := findings[0]
	a := reviewprofile.FindingDecision{ID: f.ID, Revision: f.Revision, State: "dismissed", Evidence: "Human reviewed the unsupported reproduction setup"}
	if resp := request("profileDecideFinding", a); !resp.OK {
		t.Fatalf("decision: %+v", resp)
	}
	if d.profileGatesOpen(s) {
		t.Fatal("dismissal changed independent delivery gate")
	}
	old, _ := d.profileReviews.Get(record.Key)
	if old.State != "findings" {
		t.Fatal("decision changed original verdict")
	}
	if resp := request("profileCalibration", nil); !resp.OK {
		t.Fatalf("calibration: %+v", resp)
	}
	if got := d.profileReviews.Calibration(); len(got) != 1 || got[0].Dismissed != 1 {
		t.Fatalf("calibration: %+v", got)
	}
	a.State = "accepted"
	if resp := request("profileDecideFinding", a); resp.OK {
		t.Fatal("stale decision silently retried")
	}
}
