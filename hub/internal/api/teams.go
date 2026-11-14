package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hub/internal/escalate"
	"github.com/sushidev-team/lola/hub/internal/secret"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// Self-service teams. Two kinds of request live here:
//
//   - POST /v1/orgs creates an org and enrolls its first device. It is gated
//     by a one-time CREATE CODE the hub operator minted, so a public hub is
//     not open signup. Only when the operator started the hub with
//     --open-teams does a request without a code pass, rate limited hub-wide
//     and capped at Config.MaxOrgs.
//   - The org-admin requests (/v1/org/settings, /v1/org/invites,
//     /v1/org/devices/revoke) are ordinary device-signed requests that carry
//     a second signature by a key registered in the org's admin keys.
//
// These are NOT hub-operator endpoints. An org admin acts on the signer's
// own org only (the org comes from the enrolled device, never from the
// request), and what can be set is a closed set, none of it executing: a
// Linear key and team, a digest webhook, enroll tokens, a device revocation.
// No request here registers an admin key or publishes a policy, and none
// returns a secret.

// OrgAdmin is the org-scoped operations an org admin may run. internal/admin
// implements it (admin.Ops) with the same functions the CLI and the console
// call, so the three surfaces cannot drift.
type OrgAdmin interface {
	Settings(d *store.Data, org string) hubproto.OrgSettingsView
	LinearTeams(ctx context.Context, key string) ([]hubproto.LinearTeam, error)
	StoredLinearTeams(ctx context.Context, org string) ([]hubproto.LinearTeam, error)
	SetLinearKey(org, key string, now time.Time) error
	SetLinearTeam(org, team string, now time.Time) error
	SetDigestWebhook(org, hook string, now time.Time) error
	CreateInvite(org, label string, ttl time.Duration, now time.Time) (string, time.Time, error)
	RevokeDeviceNamed(org, name string, now time.Time) (bool, error)
}

// Org-admin requests are rare (a person clicking), and some of them make the
// hub call Linear: one small bucket per org.
const (
	adminRatePerSec = 0.2
	adminBurst      = 20
)

// ---- what the hub says about itself -----------------------------------------

// handleHubInfo answers the one question a person has before they hold a
// key: does creating a team here need a create code? It is unsigned, takes
// no input and says the same to everyone. A hub that reached its team cap
// answers false, since a code is what creates a team there now.
func (s *Server) handleHubInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		writeError(w, http.StatusMethodNotAllowed, hubproto.CodeBadRequest)
		return
	}
	info := hubproto.HubInfo{}
	if s.cfg.OpenTeams {
		_ = s.cfg.Store.View(func(d *store.Data) error {
			info.OpenTeams = len(d.Orgs) < s.cfg.MaxOrgs
			return nil
		})
	}
	writeJSON(w, http.StatusOK, info)
}

// ---- create a team ---------------------------------------------------------

func (s *Server) handleCreateOrg(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, hubproto.CodeBadRequest)
		return
	}
	body, err := readBody(w, r)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	now := s.cfg.Now()
	var req hubproto.CreateOrgRequest
	if err := decode(body, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if req.Validate() != nil || r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	// No code: allowed only on a hub the operator opened. A code that is
	// sent is always checked, open hub or not.
	open := req.Code == ""
	if open && !s.cfg.OpenTeams {
		writeError(w, http.StatusUnauthorized, hubproto.CodeUnauthorized)
		return
	}
	if !open {
		// As with enroll: the cheap code lookup runs BEFORE the shared bucket
		// is charged, so a request without a usable code cannot drain it.
		if !s.codeUsable(req.Code, now) {
			writeError(w, http.StatusUnauthorized, hubproto.CodeUnauthorized)
			return
		}
		s.enMu.Lock()
		ok := s.enroll.take(now, enrollRatePerSec, enrollBurst)
		s.enMu.Unlock()
		if !ok {
			writeError(w, http.StatusTooManyRequests, hubproto.CodeRateLimited)
			return
		}
	}
	// Both signatures: the device key being enrolled, and the admin key
	// being registered (proof the creator holds it).
	pub, _ := hubproto.DecodePublicKey(req.PublicKey)
	sh, err := hubproto.Verify(r, body, pub, now)
	if err != nil {
		s.writeErr(w, verifyErr(err))
		return
	}
	adminPub, _ := hubproto.DecodePublicKey(req.AdminPublicKey)
	if id, err := hubproto.AdminKeyID(r.Header); err != nil || id != req.AdminKeyID ||
		hubproto.VerifyAdmin(r, body, req.Org, adminPub) != nil {
		writeError(w, http.StatusUnauthorized, hubproto.CodeUnauthorized)
		return
	}
	if err := s.replay.check(sh.Device, sh.Nonce, now); err != nil {
		s.writeErr(w, err)
		return
	}
	if open {
		// Charged only once both signatures verified, and never from the
		// enroll bucket: code-less requests cannot starve joins or creations
		// that carry a code.
		s.enMu.Lock()
		ok := s.open.take(now, openCreatePerSec, openCreateBurst)
		s.enMu.Unlock()
		if !ok {
			writeError(w, http.StatusTooManyRequests, hubproto.CodeRateLimited)
			return
		}
	}
	devID := hubproto.DeviceID(pub)
	resp := hubproto.CreateOrgResponse{DeviceID: devID, Org: req.Org, DeviceName: req.DeviceName}
	how := "create code"
	if open {
		how = "open creation"
	}
	err = s.cfg.Store.Update(func(d *store.Data) error {
		var code *store.CreateCode
		if !open {
			if code = d.CreateCodes[store.HashToken(req.Code)]; !code.Usable(now) {
				return fail(http.StatusUnauthorized, hubproto.CodeUnauthorized)
			}
		}
		// A taken name or an enrolled key leaves the code unused.
		if d.Orgs[req.Org] != nil || d.Devices[devID] != nil {
			return fail(http.StatusConflict, hubproto.CodeConflict)
		}
		// The hub is full: only the operator (a create code, the CLI, the
		// console) adds a team now.
		if open && len(d.Orgs) >= s.cfg.MaxOrgs {
			return fail(http.StatusServiceUnavailable, hubproto.CodeUnavailable)
		}
		at := now.UTC()
		if code != nil {
			code.UsedAt, code.UsedBy = &at, req.Org
		}
		d.Orgs[req.Org] = &store.Org{Name: req.Org, CreatedAt: at,
			AdminKeys: map[string]string{req.AdminKeyID: hubproto.EncodePublicKey(adminPub)}}
		d.Devices[devID] = &store.Device{ID: devID, Org: req.Org, Name: req.DeviceName,
			PublicKey: hubproto.EncodePublicKey(pub), CreatedAt: at}
		d.Log(store.Event{At: at, Org: req.Org, Kind: "org_created", Detail: how})
		d.Log(store.Event{At: at, Org: req.Org, Kind: "policy_key_added", Detail: req.AdminKeyID})
		d.Log(store.Event{At: at, Org: req.Org, Kind: "device_enrolled", Device: devID})
		return nil
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// codeUsable reports whether code names an unused, unexpired create code.
func (s *Server) codeUsable(code string, now time.Time) bool {
	ok := false
	_ = s.cfg.Store.View(func(d *store.Data) error {
		ok = d.CreateCodes[store.HashToken(code)].Usable(now)
		return nil
	})
	return ok
}

// ---- org-admin auth ---------------------------------------------------------

// adminAuthed is authed plus the admin signature: the request must also be
// signed by a key registered in the device's org. Everything a member is
// refused for answers alike (forbidden).
func (s *Server) adminAuthed(method string, h authedHandler) http.HandlerFunc {
	return s.authed(method, func(w http.ResponseWriter, r *http.Request, dev store.Device, body []byte) {
		if s.cfg.Admin == nil {
			writeError(w, http.StatusNotFound, hubproto.CodeNotFound)
			return
		}
		keyID, err := hubproto.AdminKeyID(r.Header)
		if err != nil {
			writeError(w, http.StatusForbidden, hubproto.CodeForbidden)
			return
		}
		var enc string
		_ = s.cfg.Store.View(func(d *store.Data) error {
			if o := d.Orgs[dev.Org]; o != nil {
				enc = o.AdminKeys[keyID]
			}
			return nil
		})
		pub, err := hubproto.DecodePublicKey(enc)
		if err != nil || hubproto.VerifyAdmin(r, body, dev.Org, pub) != nil {
			writeError(w, http.StatusForbidden, hubproto.CodeForbidden)
			return
		}
		// Charged only once the admin signature verified: a member without
		// the key cannot spend the admins' budget.
		if !s.admin.allow(dev.Org, s.cfg.Now()) {
			writeError(w, http.StatusTooManyRequests, hubproto.CodeRateLimited)
			return
		}
		h(w, r, dev, body)
	})
}

// adminErr maps an admin operation's error onto an answer. The message is
// the code's fixed one; the operation's own text goes to the log only when
// it is not a known class.
func (s *Server) adminErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, secret.ErrNoKey), errors.Is(err, secret.ErrOtherKey), errors.Is(err, secret.ErrCorrupt):
		writeError(w, http.StatusServiceUnavailable, hubproto.CodeUnavailable)
	case errors.Is(err, escalate.ErrAuth):
		writeError(w, http.StatusUnprocessableEntity, hubproto.CodeInvalidSecret)
	case errors.Is(err, escalate.ErrNetwork), errors.Is(err, escalate.ErrHTTP), errors.Is(err, escalate.ErrGraphQL),
		errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusServiceUnavailable, hubproto.CodeUnavailable)
	case errors.Is(err, store.ErrLimit):
		writeError(w, http.StatusConflict, hubproto.CodeConflict)
	default:
		s.writeErr(w, err)
	}
}

// ---- settings ---------------------------------------------------------------

func (s *Server) settingsView(org string) (v hubproto.OrgSettingsView, err error) {
	err = s.cfg.Store.View(func(d *store.Data) error {
		v = s.cfg.Admin.Settings(d, org)
		return nil
	})
	return
}

func (s *Server) writeSettings(w http.ResponseWriter, org string, teams []hubproto.LinearTeam) {
	v, err := s.settingsView(org)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	v.Teams = teams
	if v.Validate() != nil {
		s.logf("api: settings view failed validation")
		writeError(w, http.StatusInternalServerError, hubproto.CodeInternal)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request, dev store.Device, body []byte) {
	if len(body) != 0 || (r.URL.RawQuery != "" && r.URL.RawQuery != "teams=1") {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	var teams []hubproto.LinearTeam
	if r.URL.RawQuery != "" {
		v, err := s.settingsView(dev.Org)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		if !v.LinearKeySet {
			writeError(w, http.StatusConflict, hubproto.CodeConflict)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if teams, err = s.cfg.Admin.StoredLinearTeams(ctx, dev.Org); err != nil {
			s.adminErr(w, err)
			return
		}
	}
	s.writeSettings(w, dev.Org, teams)
}

func (s *Server) handleSettingsPost(w http.ResponseWriter, r *http.Request, dev store.Device, body []byte) {
	var req hubproto.OrgSettingsRequest
	if err := decode(body, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if req.Validate() != nil || r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	set := func(p *string) bool { return p != nil && *p != "" }
	cur, err := s.settingsView(dev.Org)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if (set(req.LinearKey) || set(req.DigestWebhook)) && !cur.SecretsAvailable {
		writeError(w, http.StatusServiceUnavailable, hubproto.CodeUnavailable)
		return
	}
	// A new key is checked against Linear BEFORE anything is stored, and the
	// answer lists the workspace's teams.
	var teams []hubproto.LinearTeam
	if set(req.LinearKey) {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		teams, err = s.cfg.Admin.LinearTeams(ctx, *req.LinearKey)
		cancel()
		if err != nil {
			s.adminErr(w, err)
			return
		}
		// A team sent along with the key must be one of that workspace's.
		if set(req.LinearTeam) && !hasTeam(teams, *req.LinearTeam) {
			writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
			return
		}
	}
	now := s.cfg.Now()
	if req.LinearKey != nil {
		if err := s.cfg.Admin.SetLinearKey(dev.Org, *req.LinearKey, now); err != nil {
			s.adminErr(w, err)
			return
		}
	}
	if req.LinearTeam != nil {
		if err := s.cfg.Admin.SetLinearTeam(dev.Org, *req.LinearTeam, now); err != nil {
			s.adminErr(w, err)
			return
		}
	}
	if req.DigestWebhook != nil {
		if err := s.cfg.Admin.SetDigestWebhook(dev.Org, *req.DigestWebhook, now); err != nil {
			s.adminErr(w, err)
			return
		}
	}
	s.writeSettings(w, dev.Org, teams)
}

func hasTeam(teams []hubproto.LinearTeam, id string) bool {
	for _, t := range teams {
		if strings.EqualFold(t.ID, id) {
			return true
		}
	}
	return false
}

// ---- invites and devices ----------------------------------------------------

func (s *Server) handleInvite(w http.ResponseWriter, r *http.Request, dev store.Device, body []byte) {
	var req hubproto.InviteRequest
	if err := decode(body, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if req.Validate() != nil || r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	if ttl == 0 {
		ttl = hubproto.DefaultInviteTTL * time.Second
	}
	tok, exp, err := s.cfg.Admin.CreateInvite(dev.Org, req.Label, ttl, s.cfg.Now())
	if err != nil {
		s.adminErr(w, err)
		return
	}
	// The token only: the admin's app adds the hub URL and the key members
	// pin. The hub never names that key.
	writeJSON(w, http.StatusOK, hubproto.InviteResponse{Token: tok, ExpiresAt: exp})
}

func (s *Server) handleRevoke(w http.ResponseWriter, r *http.Request, dev store.Device, body []byte) {
	var req hubproto.DeviceRevokeRequest
	if err := decode(body, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if req.Validate() != nil || r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	found, err := s.cfg.Admin.RevokeDeviceNamed(dev.Org, req.Device, s.cfg.Now())
	if err != nil {
		s.adminErr(w, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, hubproto.CodeNotFound)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}
