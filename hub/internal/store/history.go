package store

import (
	"time"

	"github.com/sushidev-team/lola/hubproto"
)

// Attempt is one stored history entry of an item. The device's display name
// and member are copied at the time of the entry when the caller knows them
// (the board resolves Device otherwise), so the board can still show who did
// it after the device was revoked; Device (the id) never leaves the hub.
type Attempt struct {
	Device     string                 `json:"device,omitempty"`
	DeviceName string                 `json:"device_name,omitempty"`
	Member     string                 `json:"member,omitempty"`
	Action     hubproto.AttemptAction `json:"action"`
	Reason     hubproto.FailReason    `json:"reason,omitempty"`
	PRURL      string                 `json:"pr_url,omitempty"`
	At         time.Time              `json:"at"`
}

// Grace holds an item whose lease lapsed for its previous holder (Device)
// until Until: it is offered to nobody else, and the holder's renew or claim
// takes it back.
type Grace struct {
	Device string    `json:"device"`
	Until  time.Time `json:"until"`
}

// AddHistory appends a to the item's history, keeping the newest
// hubproto.MaxItemHistory entries.
func (w *WorkItem) AddHistory(a Attempt) {
	w.History = append(w.History, a)
	if over := len(w.History) - hubproto.MaxItemHistory; over > 0 {
		w.History = append([]Attempt(nil), w.History[over:]...)
	}
}

// CoolDown keeps the item from being offered to device until until, and
// drops entries that already ran out (the map never outgrows the devices
// that touched the item recently).
func (w *WorkItem) CoolDown(device string, now, until time.Time) {
	for id, t := range w.Cooldown {
		if !now.Before(t) {
			delete(w.Cooldown, id)
		}
	}
	if w.Cooldown == nil {
		w.Cooldown = map[string]time.Time{}
	}
	w.Cooldown[device] = until.UTC()
}

// CoolingFor reports whether the item is in cooldown for device at now.
func (w *WorkItem) CoolingFor(device string, now time.Time) bool {
	t, ok := w.Cooldown[device]
	return ok && now.Before(t)
}
