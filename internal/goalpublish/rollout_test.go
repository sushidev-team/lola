package goalpublish

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/goalpolicy"
)

func TestPublicationRecoversReviewedRolloutAndRejectsItsReplacement(t *testing.T) {
	p, want, f := fixture(t, func(policy *goalpolicy.Policy) {
		policy.Rollout = &goalpolicy.Rollout{Compatibility: []string{"Preserve the previous API until clients migrate."}, MergeOrder: []string{"api"}}
	})
	f.losePR = true
	if _, err := p.Publish(context.Background(), want); err == nil {
		t.Fatal("lost PR reply acknowledged")
	}
	restarted := &Publisher{Workspaces: p.Workspaces, Run: p.Run, Guard: p.Guard}
	r, err := restarted.Publish(context.Background(), want)
	if err != nil || !r.Complete || !reflect.DeepEqual(r.Request.Policy.Rollout, want.Policy.Rollout) || f.pushes != 1 || f.creates != 1 {
		t.Fatal("restart lost contract or duplicated publication", err)
	}
	for _, remove := range []bool{false, true} {
		changed := want
		changed.Policy.Rollout = nil
		if !remove {
			changed.Policy.Rollout = &goalpolicy.Rollout{Compatibility: []string{"Allow breaking the previous API."}, MergeOrder: []string{"api"}}
		}
		if _, err := restarted.Publish(context.Background(), changed); err == nil {
			t.Fatal("changed rollout reused final proof")
		}
		if f.pushes != 1 || f.creates != 1 {
			t.Fatal("changed rollout reached remote mutation")
		}
	}
}

func TestPublicationTemplateUpgradeRetainsReservedProseAndEffects(t *testing.T) {
	for _, phase := range []string{"push", "PR"} {
		t.Run(phase, func(t *testing.T) {
			p, want, f := fixture(t) // a legacy policy without a rollout contract
			originalTitle, originalBody := want.Title, want.Body
			f.losePush, f.losePR = phase == "push", phase == "PR"
			if _, err := p.Publish(context.Background(), want); err == nil {
				t.Fatal("lost response acknowledged")
			}
			want.Title = "feat(goal): revised title template"
			want.Body = "No reviewed rollout contract was recorded. New description template."
			restarted := &Publisher{Workspaces: p.Workspaces, Run: p.Run, Guard: p.Guard}
			r, err := restarted.Publish(context.Background(), want)
			if err != nil || !r.Complete || r.Request.Title != originalTitle || r.Request.Body != originalBody || f.pushes != 1 || f.creates != 1 {
				t.Fatal("template upgrade lost reserved publication or repeated effects", err)
			}
			if !strings.Contains(f.pr.Body, originalBody) || strings.Contains(f.pr.Body, want.Body) {
				t.Fatal("template upgrade replaced original PR description")
			}
		})
	}
}
