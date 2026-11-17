package store

import (
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
)

func TestAudienceRefusesARevokedOwner(t *testing.T) {
	owner := &Device{ID: "o", Org: "org"}
	mate := &Device{ID: "m", Org: "org"}
	e := hubproto.ShareEntry{}
	if !Audience(e, owner, mate) {
		t.Fatal("an org-wide share is visible to a teammate")
	}
	now := time.Now()
	owner.RevokedAt = &now
	if Audience(e, owner, mate) {
		t.Fatal("a revoked owner's share is still listed")
	}
}
