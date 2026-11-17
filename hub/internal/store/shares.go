package store

import (
	"time"

	"github.com/sushidev-team/lola/hubproto"
)

// DeviceShares is what one device published for team session sharing: the
// shares its owner offers and the binding of its hub key to its inner TLS key.
// The hub holds both as routing and display data; it never grants anything.
type DeviceShares struct {
	Entries   []hubproto.ShareEntry    `json:"entries,omitempty"`
	Identity  *hubproto.MemberIdentity `json:"identity,omitempty"`
	UpdatedAt time.Time                `json:"updated_at"`
}

// SharesOf returns the device's record, creating it in a private copy.
func (d *Data) SharesOf(device string) *DeviceShares {
	if d.Shares == nil {
		d.Shares = map[string]*DeviceShares{}
	}
	s := d.Shares[device]
	if s == nil {
		s = &DeviceShares{}
		d.Shares[device] = s
	}
	return s
}

// ShareHit is a share found by id, with its owner.
type ShareHit struct {
	Entry hubproto.ShareEntry
	Owner *Device
}

// FindShare returns the share with this id when its owner is a live device.
func (d *Data) FindShare(id string) (ShareHit, bool) {
	for dev, s := range d.Shares {
		owner := d.Devices[dev]
		if owner == nil || owner.Revoked() {
			continue
		}
		for _, e := range s.Entries {
			if e.ID == id {
				return ShareHit{Entry: e, Owner: owner}, true
			}
		}
	}
	return ShareHit{}, false
}

// Audience reports whether device may see e, shared by owner: same org, not
// the owner, and either listed or the share is open to the whole org.
func Audience(e hubproto.ShareEntry, owner, device *Device) bool {
	if owner == nil || owner.Revoked() || device == nil || owner.ID == device.ID || owner.Org != device.Org || device.Revoked() {
		return false
	}
	if len(e.Members) == 0 {
		return true
	}
	for _, m := range e.Members {
		if m == device.ID {
			return true
		}
	}
	return false
}

// RVOwner reports which device holds rv, as a share or a phone rendezvous ("" = none).
func (d *Data) RVOwner(rv string) string {
	for dev, s := range d.Shares {
		for _, e := range s.Entries {
			if e.RV == rv {
				return dev
			}
		}
	}
	if e := d.Relay[rv]; e != nil {
		return e.Device
	}
	return ""
}
