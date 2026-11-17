package api

import (
	"net/http"
	"sort"

	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// Team session sharing (docs/team-sessions.md in the lola repo). The hub keeps
// what a device published and routes a member's dial to the owner. It grants
// nothing: the owner's daemon pins the member's key and re-checks the share on
// every attach, so a hub that lies can deny service, not gain access. No log
// line below names a share id, an rv or a title.

// handleShares serves POST (replace-all) and GET (the asking device's view).
func (s *Server) handleShares(w http.ResponseWriter, r *http.Request, auth store.Device, body []byte) {
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPost:
		s.postShares(w, auth, body)
	case http.MethodGet:
		s.getShares(w, auth, body)
	default:
		writeError(w, http.StatusMethodNotAllowed, hubproto.CodeBadRequest)
	}
}

func (s *Server) postShares(w http.ResponseWriter, auth store.Device, body []byte) {
	var req hubproto.ShareSet
	if err := decode(body, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if req.Validate() != nil {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	now := s.cfg.Now()
	keep := map[string]bool{}
	err := s.cfg.Store.Update(func(d *store.Data) error {
		dev, err := liveDevice(d, auth.ID)
		if err != nil {
			return err
		}
		for _, e := range req.Shares {
			// An rv or share id another device holds is a conflict for the
			// whole set: a device must never publish a rendezvous it does not
			// own, or the hub would route a member to someone else's daemon.
			if o := d.RVOwner(e.RV); o != "" && o != dev.ID {
				return fail(http.StatusConflict, hubproto.CodeConflict)
			}
			if hit, ok := d.FindShare(e.ID); ok && hit.Owner.ID != dev.ID {
				return fail(http.StatusConflict, hubproto.CodeConflict)
			}
			keep[e.RV] = true
		}
		rec := d.SharesOf(dev.ID)
		rec.Entries = req.Shares
		rec.UpdatedAt = now.UTC()
		if len(rec.Entries) == 0 && rec.Identity == nil {
			delete(d.Shares, dev.ID)
		}
		return nil
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	// What the set no longer holds loses its connections.
	if s.cfg.Relay != nil {
		kept := map[string]bool{}
		_ = s.cfg.Store.View(func(d *store.Data) error {
			for _, e := range d.RelayEntriesOf(auth.ID) {
				kept[e.RV] = true
			}
			return nil
		})
		for rv := range keep {
			kept[rv] = true
		}
		s.cfg.Relay.Retain(auth.ID, kept)
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) getShares(w http.ResponseWriter, auth store.Device, body []byte) {
	if len(body) != 0 {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	resp := hubproto.ShareList{Shares: []hubproto.ShareView{}}
	err := s.cfg.Store.View(func(d *store.Data) error {
		me, err := liveDevice(d, auth.ID)
		if err != nil {
			return err
		}
		for devID, rec := range d.Shares {
			owner := d.Devices[devID]
			for _, e := range rec.Entries {
				if !store.Audience(e, owner, me) {
					continue
				}
				resp.Shares = append(resp.Shares, hubproto.ShareView{
					ID: e.ID, Title: e.Title, Agent: e.Agent, Level: e.Level,
					OwnerDevice: owner.ID, OwnerName: owner.Name, OwnerMember: owner.Member,
					Online: s.cfg.Relay != nil && s.cfg.Relay.Online(owner.ID),
				})
			}
		}
		return nil
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	sort.Slice(resp.Shares, func(i, j int) bool {
		a, b := resp.Shares[i], resp.Shares[j]
		if a.OwnerName != b.OwnerName {
			return a.OwnerName < b.OwnerName
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return a.ID < b.ID
	})
	if len(resp.Shares) > hubproto.MaxShareViews {
		resp.Shares = resp.Shares[:hubproto.MaxShareViews]
	}
	if resp.Validate() != nil {
		s.logf("api: share list failed validation")
		writeError(w, http.StatusInternalServerError, hubproto.CodeInternal)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleShareIdentity stores the device's binding of its hub key to its inner
// TLS key. The hub verifies the signature with the key it enrolled, so it
// never stores a binding the device did not make.
func (s *Server) handleShareIdentity(w http.ResponseWriter, r *http.Request, auth store.Device, body []byte) {
	var req hubproto.MemberIdentity
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	if err := decode(body, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	pub, err := hubproto.DecodePublicKey(auth.PublicKey)
	if err != nil || req.Validate() != nil || req.Verify(pub) != nil {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	now := s.cfg.Now()
	err = s.cfg.Store.Update(func(d *store.Data) error {
		dev, err := liveDevice(d, auth.ID)
		if err != nil {
			return err
		}
		rec := d.SharesOf(dev.ID)
		rec.Identity = &req
		rec.UpdatedAt = now.UTC()
		return nil
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

// handleShareMembers lists the org's other devices that published an identity.
func (s *Server) handleShareMembers(w http.ResponseWriter, r *http.Request, auth store.Device, body []byte) {
	if len(body) != 0 || r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	resp := hubproto.MemberList{Members: []hubproto.MemberView{}}
	err := s.cfg.Store.View(func(d *store.Data) error {
		me, err := liveDevice(d, auth.ID)
		if err != nil {
			return err
		}
		for devID, rec := range d.Shares {
			dev := d.Devices[devID]
			if rec.Identity == nil || dev == nil || dev.Revoked() || dev.Org != me.Org || dev.ID == me.ID {
				continue
			}
			resp.Members = append(resp.Members, hubproto.MemberView{
				DeviceID: dev.ID, Name: dev.Name, Member: dev.Member,
				PublicKey: dev.PublicKey, Identity: *rec.Identity,
			})
		}
		return nil
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	sort.Slice(resp.Members, func(i, j int) bool { return resp.Members[i].DeviceID < resp.Members[j].DeviceID })
	if resp.Validate() != nil {
		s.logf("api: member list failed validation")
		writeError(w, http.StatusInternalServerError, hubproto.CodeInternal)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleRelayMember is a teammate's dial to a share's owner. It is signed by
// the member's device key (admission only: the owner's daemon decides by the
// inner mutual TLS key), then follows the phone leg's steps from the dial
// bucket on. Every refusal answers alike: unknown share, not in the audience,
// another org.
func (s *Server) handleRelayMember(w http.ResponseWriter, r *http.Request, dev store.Device, body []byte) {
	q := r.URL.Query()
	id := q.Get(hubproto.QueryShare)
	if len(body) != 0 || len(q) != 1 || len(q[hubproto.QueryShare]) != 1 || !hubproto.ValidShareID(id) {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	var owner, org, rv string
	if err := s.cfg.Store.View(func(d *store.Data) error {
		me := d.Devices[dev.ID]
		hit, ok := d.FindShare(id)
		if me == nil || !ok || !store.Audience(hit.Entry, hit.Owner, me) {
			return nil
		}
		owner, org, rv = hit.Owner.ID, hit.Owner.Org, hit.Entry.RV
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, hubproto.CodeInternal)
		return
	}
	if rv == "" {
		writeError(w, http.StatusNotFound, hubproto.CodeNotFound)
		return
	}
	// The member's own dial bucket, so one teammate cannot spend the share's.
	s.spliceDial(w, r, owner, org, rv, false, "member:"+dev.ID)
}

// sharesRoute sends GET and POST /v1/shares each through its own method-checked
// signature path.
func (s *Server) sharesRoute() http.HandlerFunc {
	get := s.authed(http.MethodGet, s.handleShares)
	post := s.authed(http.MethodPost, s.handleShares)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			get(w, r)
			return
		}
		post(w, r)
	}
}
