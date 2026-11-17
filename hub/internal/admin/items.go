package admin

import (
	"errors"
	"fmt"
	"time"

	"github.com/sushidev-team/lola/hub/internal/api"
	"github.com/sushidev-team/lola/hub/internal/lease"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// ErrNotEscalated is returned by resolve and reopen for an item that is not
// escalated: only an item that belongs to a person can be handed back.
var ErrNotEscalated = errors.New("only an escalated item can be resolved or reopened")

// ResolveItem closes an escalated item as done: a person fixed it. When the
// item's Linear issue was sent, the escalator comments on and closes it.
// When org is not "", the item must belong to it.
func ResolveItem(st store.Store, org, id string, now time.Time) error {
	return moveEscalated(st, org, id, now, "item_resolved", lease.Resolve)
}

// ReopenItem puts an escalated item back on offer with its attempts reset.
// Its escalation record stays, so it never files a second Linear issue.
func ReopenItem(st store.Store, org, id string, now time.Time) error {
	return moveEscalated(st, org, id, now, "item_reopened", lease.Reopen)
}

func moveEscalated(st store.Store, org, id string, now time.Time, kind string, move func(*store.WorkItem, time.Time) error) error {
	if !hubproto.ValidItemID(id) {
		return fmt.Errorf("no item %s", id)
	}
	return st.Update(func(d *store.Data) error {
		w := d.Items[id]
		if w == nil || (org != "" && w.Org != org) {
			return fmt.Errorf("no item %s", id)
		}
		before := api.Snap(w)
		if err := move(w, now); err != nil {
			if errors.Is(err, lease.ErrConflict) {
				return ErrNotEscalated
			}
			return err
		}
		api.Tally(d, w, before, now)
		d.Log(store.Event{At: now.UTC(), Org: w.Org, Kind: kind, Item: id, Detail: Reporter})
		return nil
	})
}
