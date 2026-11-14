package api

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hub/internal/lease"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// Snapshot is what Tally compares an item against: its state and whether it
// had an escalation before a transition.
type Snapshot struct {
	State      hubproto.State
	Escalation bool
}

// Snap records w before a transition.
func Snap(w *store.WorkItem) Snapshot {
	return Snapshot{State: w.State, Escalation: w.Escalation != nil}
}

// Tally books one transition of w (from before) into the org's daily stats
// and the event log, so every path that moves an item counts it the same way:
//
//   - entering done counts done and the time from first seen to done;
//   - entering failed counts failed;
//   - a newly opened escalation counts escalated and logs escalation_opened;
//   - entering escalated with an escalation already open (a critical item
//     that ran out of attempts) logs item_escalated, so leaving the offers
//     never happens without a line.
//
// Admin operations that move an item call it too.
func Tally(d *store.Data, w *store.WorkItem, before Snapshot, now time.Time) {
	at := now.UTC()
	if w.State != before.State {
		switch w.State {
		case hubproto.StateDone:
			day := d.StatsDay(w.Org, now)
			day.Done = bump(day.Done)
			if secs := int64(now.Sub(w.FirstSeen) / time.Second); secs > 0 {
				day.FixSecondsSum = min(day.FixSecondsSum+secs, hubproto.MaxStatsFixSeconds)
			}
		case hubproto.StateFailed:
			day := d.StatsDay(w.Org, now)
			day.Failed = bump(day.Failed)
		case hubproto.StateEscalated:
			if before.Escalation {
				d.Log(store.Event{At: at, Org: w.Org, Kind: "item_escalated", Item: w.ID, Detail: lease.ReasonAttempts})
			}
		}
	}
	if !before.Escalation && w.Escalation != nil {
		day := d.StatsDay(w.Org, now)
		day.Escalated = bump(day.Escalated)
		d.Log(store.Event{At: at, Org: w.Org, Kind: "escalation_opened", Item: w.ID, Detail: w.Escalation.Reason})
	}
}

// TallyOpened counts a newly created item of org.
func TallyOpened(d *store.Data, org string, now time.Time) {
	day := d.StatsDay(org, now)
	day.Opened = bump(day.Opened)
}

func bump(n int) int { return min(n+1, hubproto.MaxStatsCount) }

// tickItem applies the clock to one item and logs what it did: a lapsed lease
// (lease_expired; the item is now held for its holder's grace window) and a
// grace window that ran out (lease_released). It reports whether it changed w.
func tickItem(d *store.Data, w *store.WorkItem, now time.Time, cfg lease.Config) bool {
	var holder string
	if w.Lease != nil {
		holder = w.Lease.Device
	} else if w.Grace != nil {
		holder = w.Grace.Device
	}
	lapsed, ended := lease.Tick(w, now, cfg)
	if lapsed {
		d.Log(store.Event{At: now.UTC(), Org: w.Org, Kind: "lease_expired", Item: w.ID, Device: holder})
	}
	if ended {
		d.Log(store.Event{At: now.UTC(), Org: w.Org, Kind: "lease_released", Item: w.ID, Device: holder, Detail: "grace"})
	}
	return lapsed || ended
}

// tickOrg applies the clock to the org's items. It reports whether it
// changed anything.
func (s *Server) tickOrg(d *store.Data, org string, now time.Time) bool {
	cfg := s.leaseConfig(d, org)
	changed := false
	for _, w := range d.Items {
		if w.Org == org && tickItem(d, w, now, cfg) {
			changed = true
		}
	}
	return changed
}

// hasLapsed reports whether an org holds an item the clock would change (a
// lease that passed, a grace window that ran out).
func hasLapsed(d *store.Data, org string, now time.Time) bool {
	for _, w := range d.Items {
		if w.Org == org && lease.Due(w, now) {
			return true
		}
	}
	return false
}

// activeRepos maps org -> lowercased repo slugs some ACTIVE (heartbeat within
// ActiveWindow, not revoked) device of the org reports.
func activeRepos(d *store.Data, now time.Time) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, dv := range d.Devices {
		if dv.Revoked() || dv.LastSeen.IsZero() || now.Sub(dv.LastSeen) >= ActiveWindow {
			continue
		}
		m := out[dv.Org]
		if m == nil {
			m = map[string]bool{}
			out[dv.Org] = m
		}
		for _, r := range dv.Repos {
			m[strings.ToLower(r)] = true
		}
	}
	return out
}

// prunable reports whether a closed item is past retention. An item whose
// escalation is still pending is kept until it is sent.
func prunable(w *store.WorkItem, now time.Time, retention time.Duration) bool {
	if w.State != hubproto.StateDone && w.State != hubproto.StateFailed {
		return false
	}
	if w.Escalation != nil && w.Escalation.Pending {
		return false
	}
	return !w.ClosedAt.IsZero() && now.Sub(w.ClosedAt) >= retention
}

// Sweep runs the clock over every org:
//
//   - a lapsed lease goes into its holder's grace window, and a grace window
//     that ran out releases the item (with the holder's cooldown);
//   - an in_review item with no progress for hubproto.InReviewIdleReopen
//     reopens;
//   - an open item not re-reported for StaleAfter, while an active device of
//     its org still reports its repo, closes as failed (closed_stale): the
//     finding is gone. Without an active reporter silence proves nothing,
//     so the item stays;
//   - done/failed items past retention are dropped.
//
// It returns how many leases it found lapsed.
func (s *Server) Sweep() (int, error) {
	n := 0
	now := s.cfg.Now()
	err := s.cfg.Store.Update(func(d *store.Data) error {
		changed, pruned := false, 0
		cfgs := map[string]lease.Config{}
		active := activeRepos(d, now)
		for id, w := range d.Items {
			cfg, ok := cfgs[w.Org]
			if !ok {
				cfg = s.leaseConfig(d, w.Org)
				cfgs[w.Org] = cfg
			}
			wasClaimed := w.State == hubproto.StateClaimed
			if tickItem(d, w, now, cfg) {
				changed = true
				if wasClaimed && w.State != hubproto.StateClaimed {
					n++
				}
			}
			if lease.ReopenIdle(w, now) {
				changed = true
				d.Log(store.Event{At: now.UTC(), Org: w.Org, Kind: "item_reopened", Item: w.ID, Detail: "in_review_idle"})
			}
			if lease.Stale(w, now, s.cfg.StaleAfter) && active[w.Org][strings.ToLower(w.Item.Repo())] {
				before := Snap(w)
				if lease.CloseStale(w, now) {
					changed = true
					d.Log(store.Event{At: now.UTC(), Org: w.Org, Kind: "item_closed_stale", Item: w.ID})
					Tally(d, w, before, now)
				}
			}
			if prunable(w, now, s.cfg.Retention) {
				delete(d.Items, id)
				pruned++
			}
		}
		if !changed && pruned == 0 {
			return errNoChange
		}
		if pruned > 0 {
			d.Log(store.Event{At: now.UTC(), Kind: "items_pruned", Detail: strconv.Itoa(pruned)})
		}
		return nil
	})
	if errors.Is(err, errNoChange) {
		err = nil
	}
	return n, err
}

// errNoChange aborts a transaction that found nothing to write.
var errNoChange = errors.New("no change")
