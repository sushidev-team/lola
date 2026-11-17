package api

import (
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// ActiveWindow is how recently a device must have heartbeat to read as
// active on the org view. A daemon heartbeats every poll (300s by default),
// so three missed beats are well past it.
const ActiveWindow = 15 * time.Minute

// The board views (GET /v1/board, GET /v1/org) are READ-ONLY and scoped to
// the asking device's org. They carry display data only: device NAMES of the
// org (never ids, public keys or repos of other devices), each item's
// validated fields, lease holder name, outcome and escalation identifier.
// Every row is checked with hubproto's own Validate before it is sent, so the
// hub never ships a row the daemon would refuse; a row that fails is dropped.

// readOrg runs fn against the store as a read. When the org holds a lapsed
// lease it first spends an Update to reopen it, so a view never shows a
// lease that is already gone (the same pattern as offers).
func (s *Server) readOrg(auth store.Device, now time.Time, fn func(d *store.Data, dev *store.Device)) error {
	var lapsed bool
	err := s.cfg.Store.View(func(d *store.Data) error {
		dev, err := liveDevice(d, auth.ID)
		if err != nil {
			return err
		}
		if lapsed = hasLapsed(d, dev.Org, now); !lapsed {
			fn(d, dev)
		}
		return nil
	})
	if err != nil || !lapsed {
		return err
	}
	err = s.cfg.Store.Update(func(d *store.Data) error {
		dev, err := liveDevice(d, auth.ID)
		if err != nil {
			return err
		}
		changed := s.tickOrg(d, dev.Org, now)
		fn(d, dev)
		if !changed {
			return errNoChange
		}
		return nil
	})
	if errors.Is(err, errNoChange) {
		err = nil
	}
	return err
}

func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request, auth store.Device, body []byte) {
	if len(body) != 0 {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	state, limit, err := hubproto.ParseBoardQuery(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	now := s.cfg.Now()
	resp := hubproto.BoardResponse{Items: []hubproto.ItemView{}}
	err = s.readOrg(auth, now, func(d *store.Data, dev *store.Device) {
		resp.Items = BoardItems(d, dev.Org, dev.ID, state, limit)
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleOrg(w http.ResponseWriter, r *http.Request, auth store.Device, body []byte) {
	if len(body) != 0 || r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	now := s.cfg.Now()
	var resp hubproto.OrgView
	err := s.cfg.Store.View(func(d *store.Data) error {
		dev, err := liveDevice(d, auth.ID)
		if err != nil {
			return err
		}
		resp = OrgView(d, dev.Org, dev.ID, now)
		return nil
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if resp.Validate() != nil {
		// Cannot happen with stored data that passed enrollment and
		// heartbeat validation; refuse rather than ship it.
		s.logf("api: org view failed validation")
		writeError(w, http.StatusInternalServerError, hubproto.CodeInternal)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// activity is an item's most recent change, for "newest activity first".
func activity(w *store.WorkItem) time.Time {
	t := w.LastSeen
	if w.Outcome != nil && w.Outcome.At.After(t) {
		t = w.Outcome.At
	}
	if !w.ClosedAt.IsZero() && w.ClosedAt.After(t) {
		t = w.ClosedAt
	}
	if w.ProgressAt.After(t) {
		t = w.ProgressAt
	}
	if n := len(w.History); n > 0 && w.History[n-1].At.After(t) {
		t = w.History[n-1].At
	}
	return t
}

// BoardItems builds the org's board rows as seen by device me ("" = nobody,
// e.g. an admin): state filter ("" = all), newest activity first, at most
// limit rows, every row valid by hubproto.ItemView.Validate.
func BoardItems(d *store.Data, org, me string, state hubproto.State, limit int) []hubproto.ItemView {
	var items []*store.WorkItem
	for _, w := range d.Items {
		if w.Org == org && (state == "" || w.State == state) {
			items = append(items, w)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		ai, aj := activity(items[i]), activity(items[j])
		if !ai.Equal(aj) {
			return ai.After(aj)
		}
		return items[i].ID < items[j].ID
	})
	out := make([]hubproto.ItemView, 0, min(len(items), limit))
	for _, w := range items {
		if len(out) >= limit {
			break
		}
		v := ItemView(d, w, me)
		if v.Validate() != nil {
			continue
		}
		out = append(out, v)
	}
	return out
}

// ItemView projects one stored item onto the wire view for device me. Every
// optional part is added only when it passes its own hubproto check, so one
// odd stored value costs that part, not the whole row.
func ItemView(d *store.Data, w *store.WorkItem, me string) hubproto.ItemView {
	it := w.Item
	it.ID = w.ID
	v := hubproto.ItemView{
		Item:      it,
		State:     w.State,
		Attempts:  min(max(w.Attempts, 0), hubproto.MaxItemAttempts),
		Reporters: min(max(w.Reporters, 0), hubproto.MaxItemReporters),
		FirstSeen: w.FirstSeen,
		LastSeen:  w.LastSeen,
		Reopened:  min(max(w.Reopened, 0), hubproto.MaxItemAttempts),
	}
	if v.LastSeen.Before(v.FirstSeen) {
		v.LastSeen = v.FirstSeen
	}
	if w.State == hubproto.StateClaimed && w.Lease != nil {
		if holder := d.Devices[w.Lease.Device]; holder != nil && hubproto.ValidDeviceName(holder.Name) {
			v.Lease = &hubproto.LeaseView{
				Mine:       me != "" && w.Lease.Device == me,
				DeviceName: holder.Name,
				Member:     memberOf(holder.Member),
				Expires:    w.Lease.Expires,
			}
		}
	}
	// claimed_at only on claimed / in_review, an owner never on open /
	// claimed: hubproto refuses the row otherwise.
	if (w.State == hubproto.StateClaimed || w.State == hubproto.StateInReview) && !w.ClaimedAt.IsZero() {
		v.ClaimedAt = w.ClaimedAt
	}
	if w.Owner != "" && w.State != hubproto.StateOpen && w.State != hubproto.StateClaimed {
		if owner := d.Devices[w.Owner]; owner != nil && hubproto.ValidDeviceName(owner.Name) {
			v.Owner = &hubproto.OwnerView{
				Mine:       me != "" && w.Owner == me,
				DeviceName: owner.Name,
				Member:     memberOf(owner.Member),
			}
		}
	}
	if w.LastFail.Valid() {
		v.LastFail = w.LastFail
	}
	if o := w.Outcome; o != nil && hubproto.ValidatePRURL(o.PRURL) == nil && o.Delivery.Valid() {
		v.Outcome = &hubproto.OutcomeView{PRURL: o.PRURL, Delivery: o.Delivery}
	}
	if x := w.Escalation; x != nil {
		ev := &hubproto.EscalationView{Pending: x.Pending}
		if hubproto.ValidIssueIdentifier(x.LinearID) {
			ev.Identifier = x.LinearID
		}
		if x.URL != "" && hubproto.ValidateLinearURL(x.URL) == nil {
			ev.URL = x.URL
		}
		v.Escalation = ev
	}
	v.History = history(d, w.History)
	return v
}

// memberOf passes a member label only when it validates.
func memberOf(m string) string {
	if hubproto.ValidMemberLabel(m) {
		return m
	}
	return ""
}

// history projects the stored attempt log: device NAMES only (the id stays
// on the hub), the copied name first, else the device's current one. An
// entry that does not validate, or would break newest-last order, is
// dropped; at most hubproto.MaxItemHistory, the newest.
func history(d *store.Data, in []store.Attempt) []hubproto.Attempt {
	var out []hubproto.Attempt
	for _, a := range in {
		h := hubproto.Attempt{Action: a.Action, Reason: a.Reason, PRURL: a.PRURL, At: a.At}
		name, member := a.DeviceName, a.Member
		if name == "" && a.Device != "" {
			if dv := d.Devices[a.Device]; dv != nil {
				name, member = dv.Name, dv.Member
			}
		}
		if name != "" && hubproto.ValidDeviceName(name) {
			h.DeviceName, h.Member = name, memberOf(member)
		}
		if h.Validate() != nil {
			continue
		}
		if n := len(out); n > 0 && h.At.Before(out[n-1].At) {
			continue
		}
		out = append(out, h)
	}
	if over := len(out) - hubproto.MaxItemHistory; over > 0 {
		out = out[over:]
	}
	return out
}

// OrgView builds the org view as seen by device me. Revoked devices are
// left out; the list is the most recently seen MaxOrgDevices, by name.
func OrgView(d *store.Data, org, me string, now time.Time) hubproto.OrgView {
	v := hubproto.OrgView{Org: org, Devices: []hubproto.DeviceView{}}
	if o := d.Orgs[org]; o != nil && o.Policy != nil && o.Policy.Version > 0 {
		v.PolicyVersion = o.Policy.Version
	}
	var devs []*store.Device
	for _, dv := range d.Devices {
		if dv.Org == org && !dv.Revoked() && hubproto.ValidDeviceName(dv.Name) {
			devs = append(devs, dv)
		}
	}
	if len(devs) > hubproto.MaxOrgDevices {
		sort.Slice(devs, func(i, j int) bool { return devs[i].LastSeen.After(devs[j].LastSeen) })
		devs = devs[:hubproto.MaxOrgDevices]
	}
	sort.Slice(devs, func(i, j int) bool {
		if devs[i].Name != devs[j].Name {
			return devs[i].Name < devs[j].Name
		}
		return devs[i].ID < devs[j].ID
	})
	for _, dv := range devs {
		agents := []string{}
		for _, a := range dv.Agents {
			if len(agents) < hubproto.MaxHeartbeatAgents {
				agents = append(agents, a)
			}
		}
		v.Devices = append(v.Devices, hubproto.DeviceView{
			Name:          dv.Name,
			Me:            dv.ID == me,
			LastSeen:      dv.LastSeen,
			FreeSlots:     min(max(dv.FreeSlots, 0), hubproto.MaxFreeSlots),
			Agents:        agents,
			Active:        !dv.LastSeen.IsZero() && now.Sub(dv.LastSeen) < ActiveWindow,
			Member:        memberOf(dv.Member),
			PolicyVersion: max(dv.PolicyVersion, 0),
		})
	}
	return v
}
