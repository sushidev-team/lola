// Package api is the hub's HTTP surface: every /v1 path of hubproto plus an
// unauthenticated /healthz. There are NO hub-operator endpoints on this
// listener; operating the hub runs on the host (cmd/lola-hub admin ...) or
// through the loopback-only console (internal/console) on its own listener.
// An ORG's own admins reach a closed set of their org's settings here, with
// a second signature (see teams.go).
//
// Every /v1 request except enroll is authenticated by the device signature
// (hubproto.Verify against the enrolled public key), checked against a nonce
// replay cache and a per-device token bucket, and bounded at hubproto.MaxBody.
// Enroll is signed with the key being enrolled and verified against the
// body's public_key. Error bodies carry a code and a FIXED message per code:
// nothing from the request is ever echoed back.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/hub/internal/lease"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// Defaults.
const (
	DefaultRatePerSec     = 2.0
	DefaultBurst          = 60
	DefaultMaxItemsPerOrg = 10000
	// DefaultNewItemsPerHour caps the NEW items one device may create per
	// hour (a token bucket with an hour's worth of burst). Re-reports of
	// known items are free.
	DefaultNewItemsPerHour = 500
	// DefaultRetention is how long a done/failed item is kept after it
	// closed. It must exceed lease.ReopenAfter: until then a re-report must
	// find the item (and not reopen it); after it, a re-report would reopen
	// it anyway, so dropping it loses no dedup.
	DefaultRetention = 7 * 24 * time.Hour
	// enroll is unauthenticated until the token is checked, so it shares one
	// global bucket.
	enrollRatePerSec = 0.5
	enrollBurst      = 10
	// Creating a team WITHOUT a create code (Config.OpenTeams) is open to
	// anyone who can reach the hub, so it has one small global bucket of its
	// own (one team per ten minutes after the burst) and a cap on the teams
	// the hub holds.
	openCreatePerSec = 1.0 / 600
	openCreateBurst  = 5
	// DefaultMaxOrgs caps the orgs a hub holds once creation is open. A
	// create code from the operator is not bound by it.
	DefaultMaxOrgs = 50
	// replayWindow covers every timestamp Verify accepts (±MaxSkew).
	replayWindow   = 2 * hubproto.MaxSkew
	maxReplayCache = 200_000
)

// Config wires a Server.
type Config struct {
	Store           store.Store
	Now             func() time.Time // nil = time.Now
	LeaseTTL        time.Duration    // 0 = lease.DefaultTTL
	EscalateAfter   int              // default when the org policy sets none; 0 = lease default
	RatePerSec      float64          // per device; 0 = DefaultRatePerSec
	Burst           int              // per device; 0 = DefaultBurst
	MaxItemsPerOrg  int              // live (non-terminal) items; 0 = DefaultMaxItemsPerOrg
	NewItemsPerHour int              // per device; 0 = DefaultNewItemsPerHour
	Retention       time.Duration    // done/failed kept this long; 0 = DefaultRetention
	// MaxLeaseAge bounds how long renewals may keep one claim alive (flag
	// --max-lease-age); 0 = hubproto.DefaultMaxLeaseAge.
	MaxLeaseAge time.Duration
	// StaleAfter: an open item not re-reported this long, while an active
	// device still reports its repo, closes as failed (flag --stale-after);
	// 0 = hubproto.DefaultStaleAfter.
	StaleAfter time.Duration
	// Admin runs the org-admin requests (admin.Ops). nil turns them off:
	// they answer 404.
	Admin OrgAdmin
	// OpenTeams lets POST /v1/orgs create an org WITHOUT a create code (flag
	// --open-teams). Off by default: on a hub strangers can reach, it is
	// open signup. Meant for a hub on a private network.
	OpenTeams bool
	// MaxOrgs caps the orgs a code-less creation may bring the hub to;
	// 0 = DefaultMaxOrgs.
	MaxOrgs int
	Logf    func(format string, args ...any)
}

// Server serves the hub API.
type Server struct {
	cfg    Config
	replay *replayCache
	limit  *limiter
	create *limiter // new items per device
	admin  *limiter // org-admin requests per org
	enroll *bucket
	open   *bucket // code-less team creations, hub-wide
	enMu   sync.Mutex
}

// perDeviceNonces is the most live nonces a device can legitimately hold:
// what its bucket admits over one replay window.
func perDeviceNonces(rate float64, burst int) int {
	return int(rate*replayWindow.Seconds()) + burst + 1
}

// New builds a Server.
func New(cfg Config) *Server {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.RatePerSec <= 0 {
		cfg.RatePerSec = DefaultRatePerSec
	}
	if cfg.Burst <= 0 {
		cfg.Burst = DefaultBurst
	}
	if cfg.MaxOrgs <= 0 {
		cfg.MaxOrgs = DefaultMaxOrgs
	}
	if cfg.MaxItemsPerOrg <= 0 {
		cfg.MaxItemsPerOrg = DefaultMaxItemsPerOrg
	}
	if cfg.NewItemsPerHour <= 0 {
		cfg.NewItemsPerHour = DefaultNewItemsPerHour
	}
	if cfg.Retention <= lease.ReopenAfter {
		cfg.Retention = DefaultRetention
	}
	if cfg.MaxLeaseAge <= 0 {
		cfg.MaxLeaseAge = hubproto.DefaultMaxLeaseAge
	}
	if cfg.StaleAfter <= 0 {
		cfg.StaleAfter = hubproto.DefaultStaleAfter
	}
	return &Server{
		cfg:    cfg,
		replay: newReplayCache(maxReplayCache, perDeviceNonces(cfg.RatePerSec, cfg.Burst)),
		limit:  newLimiter(cfg.RatePerSec, cfg.Burst),
		create: newLimiter(float64(cfg.NewItemsPerHour)/3600, cfg.NewItemsPerHour),
		admin:  newLimiter(adminRatePerSec, adminBurst),
		enroll: &bucket{tokens: enrollBurst},
		open:   &bucket{tokens: openCreateBurst},
	}
}

func (s *Server) logf(format string, args ...any) {
	if s.cfg.Logf != nil {
		s.cfg.Logf(format, args...)
	}
}

// Handler returns the routed handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusMethodNotAllowed, hubproto.CodeBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc(hubproto.PathEnroll, s.handleEnroll)
	mux.HandleFunc(hubproto.PathHeartbeat, s.authed(http.MethodPost, s.handleHeartbeat))
	mux.HandleFunc(hubproto.PathFindings, s.authed(http.MethodPost, s.handleFindings))
	mux.HandleFunc(hubproto.PathOffers, s.authed(http.MethodGet, s.handleOffers))
	mux.HandleFunc(hubproto.PathPolicy, s.authed(http.MethodGet, s.handlePolicy))
	mux.HandleFunc(hubproto.PathBoard, s.authed(http.MethodGet, s.handleBoard))
	mux.HandleFunc(hubproto.PathOrg, s.authed(http.MethodGet, s.handleOrg))
	mux.HandleFunc(hubproto.PathStats, s.authed(http.MethodGet, s.handleStats))
	mux.HandleFunc(hubproto.PathItemsPrefix, s.authed(http.MethodPost, s.handleItem))
	mux.HandleFunc(hubproto.PathHub, s.handleHubInfo)
	mux.HandleFunc(hubproto.PathOrgs, s.handleCreateOrg)
	settingsGet := s.adminAuthed(http.MethodGet, s.handleSettingsGet)
	settingsPost := s.adminAuthed(http.MethodPost, s.handleSettingsPost)
	mux.HandleFunc(hubproto.PathOrgSettings, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			settingsGet(w, r)
			return
		}
		settingsPost(w, r)
	})
	mux.HandleFunc(hubproto.PathOrgInvites, s.adminAuthed(http.MethodPost, s.handleInvite))
	mux.HandleFunc(hubproto.PathOrgRevoke, s.adminAuthed(http.MethodPost, s.handleRevoke))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, hubproto.CodeNotFound)
	})
	return mux
}

// ---- responses ----------------------------------------------------------

var messages = map[string]string{
	hubproto.CodeBadRequest:    "bad request",
	hubproto.CodeUnauthorized:  "unauthorized",
	hubproto.CodeSkew:          "timestamp outside the allowed window",
	hubproto.CodeReplay:        "nonce already used",
	hubproto.CodeForbidden:     "forbidden",
	hubproto.CodeNotFound:      "not found",
	hubproto.CodeConflict:      "conflict",
	hubproto.CodeLeaseLost:     "lease not held",
	hubproto.CodeTooLarge:      "body too large",
	hubproto.CodeRateLimited:   "rate limited",
	hubproto.CodeInvalidItem:   "invalid item",
	hubproto.CodeNoPolicy:      "no policy published",
	hubproto.CodeInternal:      "internal error",
	hubproto.CodeUnavailable:   "unavailable",
	hubproto.CodeInvalidSecret: "the key was refused",
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, hubproto.ErrorResponse{Error: messages[code], Code: code})
}

// apiError aborts a store transaction with an HTTP answer.
type apiError struct {
	status int
	code   string
}

func (e *apiError) Error() string { return e.code }

func fail(status int, code string) error { return &apiError{status, code} }

func (s *Server) writeErr(w http.ResponseWriter, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		writeError(w, ae.status, ae.code)
		return
	}
	s.logf("api: internal error: %v", err)
	writeError(w, http.StatusInternalServerError, hubproto.CodeInternal)
}

// ---- auth -----------------------------------------------------------------

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, hubproto.MaxBody))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, fail(http.StatusRequestEntityTooLarge, hubproto.CodeTooLarge)
		}
		return nil, fail(http.StatusBadRequest, hubproto.CodeBadRequest)
	}
	return body, nil
}

func verifyErr(err error) error {
	if errors.Is(err, hubproto.ErrSkew) {
		return fail(http.StatusUnauthorized, hubproto.CodeSkew)
	}
	return fail(http.StatusUnauthorized, hubproto.CodeUnauthorized)
}

type authedHandler func(w http.ResponseWriter, r *http.Request, dev store.Device, body []byte)

// authed wraps h with method check, bounded body, signature, replay and rate
// checks. h receives a COPY of the device as of authentication; handlers that
// mutate re-read it inside their transaction.
func (s *Server) authed(method string, h authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			writeError(w, http.StatusMethodNotAllowed, hubproto.CodeBadRequest)
			return
		}
		body, err := readBody(w, r)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		sh, err := hubproto.ParseHeaders(r.Header)
		if err != nil {
			writeError(w, http.StatusUnauthorized, hubproto.CodeUnauthorized)
			return
		}
		var dev store.Device
		var found bool
		if err := s.cfg.Store.View(func(d *store.Data) error {
			if p := d.Devices[sh.Device]; p != nil {
				dev, found = *p, true
				dev.Repos = append([]string(nil), p.Repos...)
			}
			return nil
		}); err != nil {
			s.writeErr(w, err)
			return
		}
		// Unknown and revoked answer alike: nothing to learn by probing.
		if !found || dev.Revoked() {
			writeError(w, http.StatusUnauthorized, hubproto.CodeUnauthorized)
			return
		}
		pub, err := hubproto.DecodePublicKey(dev.PublicKey)
		if err != nil {
			writeError(w, http.StatusUnauthorized, hubproto.CodeUnauthorized)
			return
		}
		now := s.cfg.Now()
		if _, err := hubproto.Verify(r, body, pub, now); err != nil {
			s.writeErr(w, verifyErr(err))
			return
		}
		// Rate limit BEFORE the replay cache: a throttled request must not
		// insert a nonce, or one device could fill the cache every org
		// shares.
		if !s.limit.allow(dev.ID, now) {
			writeError(w, http.StatusTooManyRequests, hubproto.CodeRateLimited)
			return
		}
		if err := s.replay.check(dev.ID, sh.Nonce, now); err != nil {
			s.writeErr(w, err)
			return
		}
		h(w, r, dev, body)
	}
}

func decode(body []byte, v any) error {
	if err := hubproto.DecodeStrict(bytes.NewReader(body), v); err != nil {
		if errors.Is(err, hubproto.ErrTooLarge) {
			return fail(http.StatusRequestEntityTooLarge, hubproto.CodeTooLarge)
		}
		return fail(http.StatusBadRequest, hubproto.CodeBadRequest)
	}
	return nil
}

// liveDevice re-reads the device inside a transaction (it may have been
// revoked since authentication).
func liveDevice(d *store.Data, id string) (*store.Device, error) {
	dev := d.Devices[id]
	if dev == nil || dev.Revoked() {
		return nil, fail(http.StatusUnauthorized, hubproto.CodeUnauthorized)
	}
	return dev, nil
}

func (s *Server) leaseConfig(d *store.Data, org string) lease.Config {
	cfg := lease.Config{TTL: s.cfg.LeaseTTL, EscalateAfter: s.cfg.EscalateAfter, MaxLeaseAge: s.cfg.MaxLeaseAge}
	if o := d.Orgs[org]; o != nil && o.Policy != nil && o.Policy.Settings.EscalateAfter > 0 {
		cfg.EscalateAfter = o.Policy.Settings.EscalateAfter
	}
	return cfg
}

// ---- enroll ---------------------------------------------------------------

func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
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
	var req hubproto.EnrollRequest
	if err := decode(body, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if req.Validate() != nil {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	// The cheap token lookup runs BEFORE the global bucket is charged: a
	// request without a usable token must not be able to drain the bucket
	// every legitimate enrollment shares. The token is re-checked inside the
	// transaction below.
	if !s.tokenUsable(req.Token, now) {
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
	pub, _ := hubproto.DecodePublicKey(req.PublicKey)
	sh, err := hubproto.Verify(r, body, pub, now)
	if err != nil {
		s.writeErr(w, verifyErr(err))
		return
	}
	if err := s.replay.check(sh.Device, sh.Nonce, now); err != nil {
		s.writeErr(w, err)
		return
	}
	devID := hubproto.DeviceID(pub)
	var resp hubproto.EnrollResponse
	err = s.cfg.Store.Update(func(d *store.Data) error {
		tok := d.Tokens[store.HashToken(req.Token)]
		if tok == nil || tok.UsedAt != nil || !now.Before(tok.ExpiresAt) {
			return fail(http.StatusUnauthorized, hubproto.CodeUnauthorized)
		}
		if d.Orgs[tok.Org] == nil {
			return fail(http.StatusUnauthorized, hubproto.CodeUnauthorized)
		}
		if d.Devices[devID] != nil {
			// A key enrolls once; a revoked key stays revoked.
			return fail(http.StatusConflict, hubproto.CodeConflict)
		}
		// Display names are unique per org (revoked devices included: their
		// names stay on item histories), so a board never shows two
		// "laptop"s. A taken name gets the first free "-2", "-3", ….
		name := hubproto.UniqueDeviceName(req.DeviceName, func(c string) bool {
			for _, dv := range d.Devices {
				if dv.Org == tok.Org && strings.EqualFold(dv.Name, c) {
					return true
				}
			}
			return false
		})
		if name == "" {
			return fail(http.StatusConflict, hubproto.CodeConflict)
		}
		// The member is the admin's label on the token; a label that does
		// not validate (an older store, a hand edit) is dropped, not sent.
		member := tok.Label
		if !hubproto.ValidMemberLabel(member) {
			member = ""
		}
		at := now.UTC()
		tok.UsedAt, tok.UsedBy = &at, devID
		d.Devices[devID] = &store.Device{
			ID: devID, Org: tok.Org, Name: name, Member: member,
			PublicKey: hubproto.EncodePublicKey(pub), CreatedAt: at,
		}
		d.Log(store.Event{At: at, Org: tok.Org, Kind: "device_enrolled", Device: devID})
		resp = hubproto.EnrollResponse{DeviceID: devID, Org: tok.Org, Member: member, DeviceName: name}
		return nil
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// tokenUsable reports whether token names an unused, unexpired enroll token
// of an existing org. A store error answers false.
func (s *Server) tokenUsable(token string, now time.Time) bool {
	ok := false
	_ = s.cfg.Store.View(func(d *store.Data) error {
		tok := d.Tokens[store.HashToken(token)]
		ok = tok != nil && tok.UsedAt == nil && now.Before(tok.ExpiresAt) && d.Orgs[tok.Org] != nil
		return nil
	})
	return ok
}

// ---- heartbeat ------------------------------------------------------------

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request, auth store.Device, body []byte) {
	var req hubproto.HeartbeatRequest
	if err := decode(body, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if req.Validate() != nil {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	now := s.cfg.Now()
	resp := hubproto.HeartbeatResponse{Renewed: []hubproto.Lease{}, Lost: []string{}}
	err := s.cfg.Store.Update(func(d *store.Data) error {
		dev, err := liveDevice(d, auth.ID)
		if err != nil {
			return err
		}
		dev.FreeSlots = req.FreeSlots
		dev.Agents = append([]string(nil), req.Agents...)
		dev.Repos = dedupFold(req.Repos)
		dev.LastSeen = now.UTC()
		dev.PolicyVersion = req.PolicyVersion
		cfg := s.leaseConfig(d, dev.Org)
		seen := map[string]bool{}
		for _, id := range req.LiveLeases {
			if seen[id] {
				continue
			}
			seen[id] = true
			it := d.Items[id]
			if it == nil {
				resp.Lost = append(resp.Lost, id)
				continue
			}
			// An item the device owns in review has no lease to renew, but
			// it is not lost either: the session goes on with its PR.
			if it.Org == dev.Org && it.State == hubproto.StateInReview && it.Owner == dev.ID {
				continue
			}
			before := Snap(it)
			l, err := lease.Renew(it, dev, now, cfg)
			if errors.Is(err, lease.ErrMaxAge) {
				d.Log(store.Event{At: now.UTC(), Org: dev.Org, Kind: "lease_timeout", Item: id, Device: dev.ID})
				Tally(d, it, before, now)
			}
			if err != nil {
				resp.Lost = append(resp.Lost, id)
				continue
			}
			resp.Renewed = append(resp.Renewed, l)
		}
		s.tickOrg(d, dev.Org, now)
		return nil
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func dedupFold(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		k := strings.ToLower(s)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, s)
	}
	return out
}

// ---- findings -------------------------------------------------------------

// Rejection reasons (hub-generated, never request text).
const (
	RejectHasID        = "has_id"
	RejectInvalid      = "invalid_item"
	RejectRepoNotOptIn = "repo_not_reported"
	RejectOrgFull      = "org_full"
	RejectQuota        = "device_quota"
)

func (s *Server) handleFindings(w http.ResponseWriter, r *http.Request, auth store.Device, body []byte) {
	var req hubproto.FindingsRequest
	if err := decode(body, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if len(req.Items) > hubproto.MaxFindingsItems {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	now := s.cfg.Now()
	resp := hubproto.FindingsResponse{Rejected: []hubproto.Rejection{}}
	err := s.cfg.Store.Update(func(d *store.Data) error {
		dev, err := liveDevice(d, auth.ID)
		if err != nil {
			return err
		}
		count := d.OrgLiveItems(dev.Org)
		for i, it := range req.Items {
			reject := func(reason string) {
				resp.Rejected = append(resp.Rejected, hubproto.Rejection{Index: i, Reason: reason})
			}
			if it.ID != "" {
				reject(RejectHasID)
				continue
			}
			if hubproto.Validate(it) != nil {
				reject(RejectInvalid)
				continue
			}
			// A device reports only for repos it opted in (its heartbeat).
			if !dev.HasRepo(it.Repo()) {
				reject(RejectRepoNotOptIn)
				continue
			}
			key := hubproto.DedupKey(it)
			if w := d.ItemByKey(dev.Org, key); w != nil {
				// Observe FIRST: it may reopen a long-closed item, and the
				// report that reopens it must also refresh it.
				if lease.Observe(w, dev.ID, now) {
					d.Log(store.Event{At: now.UTC(), Org: w.Org, Kind: "item_reopened", Item: w.ID, Device: dev.ID})
				}
				if w.State == hubproto.StateOpen {
					// The newest report of an unclaimed item wins (a fixed
					// version may have appeared, severity may have risen).
					it.ID = w.ID
					w.Item = it
					if it.Severity() == hubproto.SeverityCritical {
						before := Snap(w)
						lease.OpenEscalation(w, lease.ReasonCritical, now)
						Tally(d, w, before, now)
					}
				}
				resp.Accepted++
				continue
			}
			if count >= s.cfg.MaxItemsPerOrg {
				reject(RejectOrgFull)
				continue
			}
			// Charged only for an item that would be created; a refused
			// transaction may have spent a token, which errs on the safe
			// side.
			if !s.create.allow(dev.ID, now) {
				reject(RejectQuota)
				continue
			}
			id, err := store.NewItemID()
			if err != nil {
				return err
			}
			nw := lease.NewItem(id, dev.Org, it, dev.ID, now)
			d.Items[id] = nw
			count++
			resp.Accepted++
			d.Log(store.Event{At: now.UTC(), Org: dev.Org, Kind: "item_created", Item: id, Device: dev.ID, Detail: string(it.Kind)})
			TallyOpened(d, dev.Org, now)
			Tally(d, nw, Snapshot{State: nw.State}, now)
		}
		return nil
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---- offers ---------------------------------------------------------------

func (s *Server) handleOffers(w http.ResponseWriter, r *http.Request, auth store.Device, body []byte) {
	if len(body) != 0 {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	// Optional ?kind=<kind> (repeatable) narrows the offer to the kinds the
	// daemon accepts. The query is part of the signed path.
	var kinds map[hubproto.Kind]bool
	for k, vs := range r.URL.Query() {
		if k != "kind" {
			writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
			return
		}
		kinds = map[hubproto.Kind]bool{}
		for _, v := range vs {
			if !hubproto.Kind(v).Valid() {
				writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
				return
			}
			kinds[hubproto.Kind(v)] = true
		}
	}
	now := s.cfg.Now()
	resp := hubproto.OffersResponse{Items: []hubproto.Item{}}
	// A read: answered from View. Only when the org holds a lapsed lease is
	// an Update (and a snapshot write) spent to reopen it first.
	var lapsed bool
	offer := func(d *store.Data, dev *store.Device) {
		if got := lease.Offers(d.Items, dev, kinds, hubproto.MaxOfferItems, now); got != nil {
			resp.Items = got
		}
	}
	err := s.cfg.Store.View(func(d *store.Data) error {
		dev, err := liveDevice(d, auth.ID)
		if err != nil {
			return err
		}
		if lapsed = hasLapsed(d, dev.Org, now); !lapsed {
			offer(d, dev)
		}
		return nil
	})
	if err == nil && lapsed {
		err = s.cfg.Store.Update(func(d *store.Data) error {
			dev, err := liveDevice(d, auth.ID)
			if err != nil {
				return err
			}
			changed := s.tickOrg(d, dev.Org, now)
			offer(d, dev)
			if !changed {
				return errNoChange // raced with Sweep: nothing to write
			}
			return nil
		})
		if errors.Is(err, errNoChange) {
			err = nil
		}
	}
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---- policy ---------------------------------------------------------------

func (s *Server) handlePolicy(w http.ResponseWriter, r *http.Request, auth store.Device, body []byte) {
	if len(body) != 0 || r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	var p *hubproto.Policy
	if err := s.cfg.Store.View(func(d *store.Data) error {
		if o := d.Orgs[auth.Org]; o != nil && o.Policy != nil {
			cp := *o.Policy
			p = &cp
		}
		return nil
	}); err != nil {
		s.writeErr(w, err)
		return
	}
	if p == nil {
		writeError(w, http.StatusNotFound, hubproto.CodeNoPolicy)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// ---- items ----------------------------------------------------------------

func leaseErr(err error) error {
	switch {
	case errors.Is(err, lease.ErrConflict):
		return fail(http.StatusConflict, hubproto.CodeConflict)
	case errors.Is(err, lease.ErrLeaseLost):
		return fail(http.StatusConflict, hubproto.CodeLeaseLost)
	case errors.Is(err, lease.ErrRevoked):
		return fail(http.StatusUnauthorized, hubproto.CodeUnauthorized)
	case errors.Is(err, lease.ErrWrongOrg):
		return fail(http.StatusNotFound, hubproto.CodeNotFound)
	}
	return err
}

// prMatches reports whether a reported PR URL belongs to the item: same repo,
// and for a red_bump_pr the bot's own PR number.
func prMatches(it hubproto.Item, prURL string) bool {
	repo, num, err := hubproto.ParsePRURL(prURL)
	if err != nil || !strings.EqualFold(repo, it.Repo()) {
		return false
	}
	return it.Kind != hubproto.KindRedBumpPR || it.RedBumpPR.PR == num
}

func (s *Server) handleItem(w http.ResponseWriter, r *http.Request, auth store.Device, body []byte) {
	id, action, ok := hubproto.ParseItemPath(r.URL.Path)
	if !ok || r.URL.RawQuery != "" {
		writeError(w, http.StatusNotFound, hubproto.CodeNotFound)
		return
	}
	var complete hubproto.CompleteRequest
	var progress hubproto.ProgressRequest
	var failReq hubproto.FailRequest
	var valid error
	switch action {
	case hubproto.ActionClaim, hubproto.ActionRelease:
		if len(body) != 0 {
			writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
			return
		}
	case hubproto.ActionComplete:
		if err := decode(body, &complete); err != nil {
			s.writeErr(w, err)
			return
		}
		valid = complete.Validate()
	case hubproto.ActionProgress:
		if err := decode(body, &progress); err != nil {
			s.writeErr(w, err)
			return
		}
		valid = progress.Validate()
	case hubproto.ActionFail:
		if err := decode(body, &failReq); err != nil {
			s.writeErr(w, err)
			return
		}
		valid = failReq.Validate()
	}
	if valid != nil {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	now := s.cfg.Now()
	var out any = struct{}{}
	// answer is an error to send AFTER the transaction commits: a take-back
	// past the max lease age changes the item (a timeout attempt) and still
	// refuses the claim.
	var answer error
	err := s.cfg.Store.Update(func(d *store.Data) error {
		dev, err := liveDevice(d, auth.ID)
		if err != nil {
			return err
		}
		it := d.Items[id]
		if it == nil || it.Org != dev.Org {
			return fail(http.StatusNotFound, hubproto.CodeNotFound)
		}
		cfg := s.leaseConfig(d, dev.Org)
		ev := store.Event{At: now.UTC(), Org: dev.Org, Item: id, Device: dev.ID}
		before := Snap(it)
		switch action {
		case hubproto.ActionClaim:
			if !dev.HasRepo(it.Item.Repo()) {
				return fail(http.StatusForbidden, hubproto.CodeForbidden)
			}
			if held(d, dev.ID, now) >= hubproto.MaxLiveLeases {
				return fail(http.StatusConflict, hubproto.CodeConflict)
			}
			l, err := lease.Claim(it, dev, now, cfg)
			if errors.Is(err, lease.ErrMaxAge) {
				answer = fail(http.StatusConflict, hubproto.CodeLeaseLost)
				ev.Kind = "lease_timeout"
				break
			}
			if err != nil {
				return leaseErr(err)
			}
			out, ev.Kind = l, "claimed"
		case hubproto.ActionProgress:
			if !prMatches(it.Item, progress.PRURL) {
				return fail(http.StatusBadRequest, hubproto.CodeBadRequest)
			}
			if err := lease.Progress(it, dev, store.Outcome{PRURL: progress.PRURL, Delivery: progress.Delivery}, now, cfg); err != nil {
				return leaseErr(err)
			}
			ev.Kind, ev.Detail = "progress", string(progress.Delivery)
		case hubproto.ActionComplete:
			if !prMatches(it.Item, complete.PRURL) {
				return fail(http.StatusBadRequest, hubproto.CodeBadRequest)
			}
			if err := lease.Complete(it, dev, store.Outcome{PRURL: complete.PRURL, Delivery: complete.Delivery}, now, cfg); err != nil {
				return leaseErr(err)
			}
			ev.Kind, ev.Detail = "completed", string(complete.Delivery)
		case hubproto.ActionFail:
			if failReq.PRURL != "" && !prMatches(it.Item, failReq.PRURL) {
				return fail(http.StatusBadRequest, hubproto.CodeBadRequest)
			}
			if _, err := lease.Fail(it, dev, failReq.Reason, failReq.PRURL, now, cfg); err != nil {
				return leaseErr(err)
			}
			// The latest fail's group: items failed under one group
			// escalate as one Linear issue (internal/escalate).
			it.FailGroup = failReq.Group
			ev.Kind, ev.Detail = "failed", string(failReq.Reason)
		case hubproto.ActionRelease:
			if err := lease.Release(it, dev, now, cfg); err != nil {
				return leaseErr(err)
			}
			ev.Kind = "released"
		}
		d.Log(ev)
		Tally(d, it, before, now)
		return nil
	})
	if err == nil {
		err = answer
	}
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// held counts live leases of a device.
func held(d *store.Data, device string, now time.Time) int {
	n := 0
	for _, w := range d.Items {
		if w.State == hubproto.StateClaimed && w.Lease != nil && w.Lease.Device == device && now.Before(w.Lease.Expires) {
			n++
		}
	}
	return n
}
