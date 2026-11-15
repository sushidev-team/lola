package store

import (
	"crypto/subtle"
	"sort"
	"time"
)

// RelayEntry is one rendezvous of one device. Only hashes are stored.
type RelayEntry struct {
	RV         string    `json:"rv"`
	Device     string    `json:"device"` // device id that registered it
	Org        string    `json:"org"`
	BearerHash string    `json:"bearer_hash"`
	PrevHash   string    `json:"prev_hash,omitempty"`
	PrevUntil  time.Time `json:"prev_until,omitzero"`
	ExpiresAt  time.Time `json:"expires_at,omitzero"` // non-zero = pairing window
	UpdatedAt  time.Time `json:"updated_at"`
}

// Window reports whether e is a pairing-window entry.
func (e *RelayEntry) Window() bool { return !e.ExpiresAt.IsZero() }

// Live reports whether e can authenticate a phone at now (a window: now <
// ExpiresAt; a paired entry is live until it is removed).
func (e *RelayEntry) Live(now time.Time) bool {
	return !e.Window() || now.Before(e.ExpiresAt)
}

// Matches compares bearerHash against BearerHash and, while PrevUntil > now,
// PrevHash, each with subtle.ConstantTimeCompare. Both comparisons always
// run, so timing does not reveal which hash (or whether the previous one) was
// close. An entry that is not Live never matches.
func (e *RelayEntry) Matches(bearerHash string, now time.Time) bool {
	cur := subtle.ConstantTimeCompare([]byte(bearerHash), []byte(e.BearerHash))
	prev := subtle.ConstantTimeCompare([]byte(bearerHash), []byte(e.PrevHash))
	if e.PrevHash == "" || !e.PrevUntil.After(now) {
		prev = 0
	}
	// An empty presented hash never matches, even an entry stored without one.
	return bearerHash != "" && e.BearerHash != "" && e.Live(now) && (cur|prev) == 1
}

// RelayEntriesOf returns the entries registered by device, ordered by rv.
func (d *Data) RelayEntriesOf(device string) []*RelayEntry {
	var out []*RelayEntry
	for _, e := range d.Relay {
		if e.Device == device {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RV < out[j].RV })
	return out
}
