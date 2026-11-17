package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/protocol"
)

func deliveredAcceptanceFixture(t *testing.T) (*Daemon, goal.Goal) {
	t.Helper()
	d, g := readyPublicationFixture(t, func(p *goalpolicy.Policy) { p.RequireHumanAcceptance = true })
	observed := goal.Delivery{Operation: g.Execution.Final.ID, State: "merged", CheckedAt: time.Now().UTC(), Members: map[string]goal.DeliveryMember{}}
	for _, member := range g.Execution.Policy.Members {
		observed.Members[member.ID] = goal.DeliveryMember{Repo: member.Repo, Target: member.Target, Head: g.Execution.Manifest[member.ID], State: "merged", Number: 7, URL: "https://github.com/" + member.Repo + "/pull/7"}
	}
	if err := g.ObserveDelivery(observed); err != nil {
		t.Fatal(err)
	}
	var err error
	g, err = d.goals.Save(g)
	if err != nil || g.State != "ready" {
		t.Fatal("merged observation completed goal without acceptance", err)
	}
	d.goalPublishRun = func(context.Context, string, string, []string, []byte) ([]byte, error) {
		t.Fatal("acceptance or awaiting acceptance attempted publication")
		return nil, nil
	}
	return d, g
}

func TestGoalHumanAcceptancePinsEvidenceAndRecoversLostReply(t *testing.T) {
	d, g := deliveredAcceptanceFixture(t)
	ctx := context.Background()
	if err := d.runGoal(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	request := goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "accept-delivery", Reason: "Verified the delivered invitation flow with a fresh recipient"}
	stale := request
	stale.Revision--
	if _, err := d.controlGoal(ctx, stale); err == nil {
		t.Fatal("accepted stale review")
	}
	args, _ := json.Marshal(request)
	response := d.handle(ctx, protocol.Request{Cmd: "goalControl", Args: args})
	if !response.OK {
		t.Fatal(response.Error)
	}
	accepted, _ := d.goals.Get(g.ID)
	if accepted.State != "accepted" || accepted.Execution.HumanAcceptance == nil || accepted.Execution.HumanAcceptance.Revision != request.Revision {
		t.Fatal("operator acceptance was not recorded")
	}
	// Lose the successful response and reopen the authoritative store. An exact
	// retry acknowledges the existing decision without rewriting its timestamp.
	var err error
	d.goals, err = goal.Open(filepath.Join(d.home, "state", "goals.json"))
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := d.controlGoal(ctx, request)
	if err != nil || !reflect.DeepEqual(accepted, replayed) {
		t.Fatal("lost-reply retry changed acceptance", err)
	}
	request.Reason = "Different decision"
	if _, err := d.controlGoal(ctx, request); err == nil {
		t.Fatal("retry changed decision evidence")
	}
	if err := d.runGoal(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
}

func TestGoalHumanAcceptanceRechecksIndependentEvidenceAndSteering(t *testing.T) {
	for _, mode := range []string{"rubric", "invalidated-review", "partial-delivery", "pause", "missing-reason"} {
		t.Run(mode, func(t *testing.T) {
			d, g := deliveredAcceptanceFixture(t)
			reason := "Manual acceptance against the delivered requirements"
			switch mode {
			case "rubric":
				d.cfg.ReviewProfiles[0].Version = "changed-after-delivery"
			case "invalidated-review":
				for _, r := range d.profileReviews.List() {
					if err := d.profileReviews.Invalidate(r.Key, "counterexample found"); err != nil {
						t.Fatal(err)
					}
				}
			case "partial-delivery":
				g.Execution.Delivery.State = "published"
				for id, m := range g.Execution.Delivery.Members {
					m.State = "published"
					g.Execution.Delivery.Members[id] = m
				}
			case "pause":
				g.State = "paused"
			case "missing-reason":
				reason = ""
			}
			var err error
			g, err = d.goals.Save(g)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "accept-delivery", Reason: reason}); err == nil {
				t.Fatal("acceptance ignored changed proof or human steering")
			}
			current, _ := d.goals.Get(g.ID)
			if current.State == "accepted" || current.Execution.HumanAcceptance != nil {
				t.Fatal("failed decision persisted an acceptance")
			}
		})
	}
}
