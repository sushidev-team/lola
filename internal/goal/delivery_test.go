package goal

import (
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goalpolicy"
)

func TestDeliveryRequiresCompleteExactStackTargets(t *testing.T) {
	g := Goal{State: "ready", Execution: &Execution{Policy: goalpolicy.Policy{PublishPR: true, Members: []goalpolicy.Member{{ID: "api", Repo: "owner/api", Target: "main"}, {ID: "web", Repo: "owner/web", Target: "release"}}}, Manifest: map[string]string{"api": "api-head", "web": "web-head"}, Final: &FinalVerification{ID: "final", Receipt: "proof.json"}}}
	d := Delivery{Operation: "final", State: "merged", CheckedAt: time.Now(), Members: map[string]DeliveryMember{"api": {Repo: "owner/api", Target: "main", Head: "api-head", State: "merged", Number: 1, URL: "https://github.com/owner/api/pull/1"}}}
	if err := g.ObserveDelivery(d); err == nil || g.State != "ready" {
		t.Fatal("partial stack completed goal")
	}
	d.State = "published"
	d.Members["web"] = DeliveryMember{Repo: "owner/web", Target: "release", Head: "web-head", State: "published", Number: 2, URL: "https://github.com/owner/web/pull/2"}
	if err := g.ObserveDelivery(d); err != nil || g.State != "ready" {
		t.Fatal("open PR mistaken for completed delivery")
	}
	web := d.Members["web"]
	web.State = "merged"
	web.Head = "unverified"
	d.Members["web"] = web
	d.State = "merged"
	if err := g.ObserveDelivery(d); err == nil {
		t.Fatal("unverified remote head accepted")
	}
	web.Head = "web-head"
	web.Target = "main"
	d.Members["web"] = web
	if err := g.ObserveDelivery(d); err == nil {
		t.Fatal("wrong delivery target accepted")
	}
	web.Target = "release"
	d.Members["web"] = web
	g.State = "paused"
	if err := g.ObserveDelivery(d); err != nil || g.State != "paused" {
		t.Fatal("observation overwrote human steering")
	}
	g.State = "ready"
	if err := g.ObserveDelivery(d); err != nil || g.State != "accepted" {
		t.Fatal("complete verified stack delivery not acknowledged")
	}
}
