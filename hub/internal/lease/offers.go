package lease

import (
	"sort"
	"time"

	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// severityRank orders offers. red_bump_pr carries no severity and ranks with
// moderate: a red bump PR blocks merges but is not itself a vulnerability.
func severityRank(s hubproto.Severity) int {
	switch s {
	case hubproto.SeverityCritical:
		return 4
	case hubproto.SeverityHigh:
		return 3
	case hubproto.SeverityLow:
		return 1
	}
	return 2
}

// Offers returns what dev may claim at now: OPEN items of its org whose repo
// is in the device's reported repos and whose kind is in kinds (nil = every
// kind), most severe first, then oldest first, capped at limit (<=0 means
// hubproto.MaxOfferItems). An item held in another device's grace window, or
// in dev's own cooldown, is left out. A revoked device is offered nothing.
// items is not mutated; expire leases before calling.
func Offers(items map[string]*store.WorkItem, dev *store.Device, kinds map[hubproto.Kind]bool, limit int, now time.Time) []hubproto.Item {
	if dev == nil || dev.Revoked() {
		return nil
	}
	if limit <= 0 || limit > hubproto.MaxOfferItems {
		limit = hubproto.MaxOfferItems
	}
	var cand []*store.WorkItem
	for _, w := range items {
		if w.Org != dev.Org || w.State != hubproto.StateOpen {
			continue
		}
		if kinds != nil && !kinds[w.Item.Kind] {
			continue
		}
		if !dev.HasRepo(w.Item.Repo()) {
			continue
		}
		if HeldForOther(w, dev.ID, now) || w.CoolingFor(dev.ID, now) {
			continue
		}
		// Defense in depth: never offer what a daemon would refuse.
		if hubproto.Validate(w.Item) != nil {
			continue
		}
		cand = append(cand, w)
	}
	sort.Slice(cand, func(i, j int) bool {
		a, b := cand[i], cand[j]
		if ra, rb := severityRank(a.Item.Severity()), severityRank(b.Item.Severity()); ra != rb {
			return ra > rb
		}
		if !a.FirstSeen.Equal(b.FirstSeen) {
			return a.FirstSeen.Before(b.FirstSeen)
		}
		return a.ID < b.ID
	})
	if len(cand) > limit {
		cand = cand[:limit]
	}
	out := make([]hubproto.Item, 0, len(cand))
	for _, w := range cand {
		it := w.Item
		it.ID = w.ID
		out = append(out, it)
	}
	return out
}
