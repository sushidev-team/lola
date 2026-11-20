package api

import (
	"net/http"
	"time"

	"github.com/sushidev-team/lola/hub/internal/agentclaim"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// The Linear agent-session claim endpoints (hubproto/agentclaim.go). They carry
// opaque ids and a counter only; the rules are internal/agentclaim. The org is
// always the device's, never a request field.

func (s *Server) handleAgentClaim(w http.ResponseWriter, r *http.Request, auth store.Device, body []byte) {
	var req hubproto.AgentClaimRequest
	if err := decode(body, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if req.Validate() != nil {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	now := s.cfg.Now().UTC()
	var res agentclaim.Result
	err := s.cfg.Store.Update(func(d *store.Data) error {
		dev, err := liveDevice(d, auth.ID)
		if err != nil {
			return err
		}
		o := d.Orgs[dev.Org]
		if o == nil {
			return fail(http.StatusUnauthorized, hubproto.CodeUnauthorized)
		}
		if o.AgentClaims == nil {
			o.AgentClaims = map[string]*store.AgentClaim{}
		}
		agentclaim.Prune(o.AgentClaims, now)
		res = agentclaim.Claim(o.AgentClaims, req.Session, dev.ID, ownerOnline(d, dev, req.Owner, now), now)
		return nil
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hubproto.AgentClaimResponse{Granted: res.Granted, Epoch: res.Epoch, Reason: res.Reason})
}

// ownerOnline reports whether the session's owner has a live daemon other than
// the claimant. It defers nothing when the owner is unknown or is the claimant's
// own user. A claimant that names no owner (or a false one) merely skips the
// deference: every device of an org is a trusted member, so this is fairness,
// not a security boundary.
func ownerOnline(d *store.Data, claimant *store.Device, owner string, now time.Time) bool {
	if owner == "" || claimant.LinearUserID == owner {
		return false
	}
	for _, o := range d.Devices {
		if o.Org != claimant.Org || o.ID == claimant.ID || o.Revoked() || o.LinearUserID != owner {
			continue
		}
		if now.Sub(o.LastSeen) <= hubproto.AgentOwnerOnlineWindow {
			return true
		}
	}
	return false
}

func (s *Server) handleAgentAuthorize(w http.ResponseWriter, r *http.Request, auth store.Device, body []byte) {
	var req hubproto.AgentAuthorizeRequest
	if err := decode(body, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if req.Validate() != nil {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	now := s.cfg.Now().UTC()
	var ok bool
	var why string
	err := s.cfg.Store.Update(func(d *store.Data) error {
		dev, err := liveDevice(d, auth.ID)
		if err != nil {
			return err
		}
		o := d.Orgs[dev.Org]
		if o == nil {
			return fail(http.StatusUnauthorized, hubproto.CodeUnauthorized)
		}
		ok, why = agentclaim.Authorize(o.AgentClaims, req.Session, dev.ID, req.Epoch, now)
		return nil
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hubproto.AgentAuthorizeResponse{Authorized: ok, Reason: why})
}

func (s *Server) handleAgentSettle(w http.ResponseWriter, r *http.Request, auth store.Device, body []byte) {
	var req hubproto.AgentSettleRequest
	if err := decode(body, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if req.Validate() != nil {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	now := s.cfg.Now().UTC()
	var ok bool
	var why string
	err := s.cfg.Store.Update(func(d *store.Data) error {
		dev, err := liveDevice(d, auth.ID)
		if err != nil {
			return err
		}
		o := d.Orgs[dev.Org]
		if o == nil {
			return fail(http.StatusUnauthorized, hubproto.CodeUnauthorized)
		}
		ok, why = agentclaim.Settle(o.AgentClaims, req.Session, dev.ID, req.Epoch, req.Outcome, now)
		return nil
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hubproto.AgentSettleResponse{Settled: ok, Reason: why})
}
