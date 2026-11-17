package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/sushidev-team/lola/hub/internal/relay"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// The relay endpoints: rendezvous registration, the daemon's control channel,
// the phone leg and the daemon's accept leg. The hub authenticates and routes
// here; everything stateful about a connection lives in internal/relay. No log
// line below names a rendezvous id, a bearer, a header value or a client
// address.

// relayOn answers 404 not_found while the hub has no relay core.
func (s *Server) relayOn(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Relay == nil {
			writeError(w, http.StatusNotFound, hubproto.CodeNotFound)
			return
		}
		h(w, r)
	}
}

// upgradeErr answers a request that is not a valid WebSocket upgrade: 403
// when the cause is an Origin header (a browser), else 400. Reports whether it
// answered.
func upgradeErr(w http.ResponseWriter, r *http.Request, header func(code string)) bool {
	if err := hubproto.CheckUpgrade(r); err != nil {
		if _, browser := r.Header["Origin"]; browser {
			header(hubproto.CodeForbidden)
			writeError(w, http.StatusForbidden, hubproto.CodeForbidden)
		} else {
			header(hubproto.CodeBadRequest)
			writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		}
		return true
	}
	return false
}

// authedUpgrade is authed(GET, h) behind the upgrade check. The signature
// (and so a 401) is settled before any upgrade is written.
func (s *Server) authedUpgrade(h authedHandler) http.HandlerFunc {
	inner := s.authed(http.MethodGet, h)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, hubproto.CodeBadRequest)
			return
		}
		if upgradeErr(w, r, func(string) {}) {
			return
		}
		inner(w, r)
	}
}

func (s *Server) wsConfig(onFrame func(int)) hubproto.WSConfig {
	return hubproto.WSConfig{
		PingInterval: hubproto.RelayPingInterval,
		DeadAfter:    hubproto.RelayDeadAfter,
		WriteStall:   hubproto.RelayWriteStall,
		OnFrame:      onFrame,
	}
}

// ---- POST /v1/relay/rendezvous ----------------------------------------------

func (s *Server) handleRendezvous(w http.ResponseWriter, r *http.Request, auth store.Device, body []byte) {
	var req hubproto.RendezvousRequest
	if err := decode(body, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	now := s.cfg.Now()
	if req.Validate(now) != nil || r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	resp := hubproto.RendezvousResponse{}
	s.regMu.Lock()
	defer s.regMu.Unlock()
	err := s.cfg.Store.Update(func(d *store.Data) error {
		dev, err := liveDevice(d, auth.ID)
		if err != nil {
			return err
		}
		want := map[string]bool{}
		var accepted []hubproto.RelayRendezvous
		for _, e := range req.Entries {
			// An rv another device owns is skipped, not fatal: one bad entry
			// must not block the rest (the removal of a revoked phone).
			if cur := d.Relay[e.RV]; cur != nil && cur.Device != dev.ID {
				resp.Conflicts = append(resp.Conflicts, e.RV)
				continue
			}
			want[e.RV] = true
			accepted = append(accepted, e)
		}
		for _, cur := range d.RelayEntriesOf(dev.ID) {
			if !want[cur.RV] {
				delete(d.Relay, cur.RV)
			}
		}
		for _, e := range accepted {
			d.Relay[e.RV] = &store.RelayEntry{
				RV: e.RV, Device: dev.ID, Org: dev.Org,
				BearerHash: e.BearerSHA256,
				PrevHash:   e.PrevSHA256, PrevUntil: e.PrevUntil.UTC(),
				ExpiresAt: e.ExpiresAt.UTC(),
				UpdatedAt: now.UTC(),
			}
		}
		resp.Registered = len(accepted)
		return nil
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	// After the commit: what the device no longer holds loses its connections.
	s.retainHeld(auth.ID)
	writeJSON(w, http.StatusOK, resp)
}

// ---- GET /v1/relay/daemon ----------------------------------------------------

func (s *Server) handleRelayDaemon(w http.ResponseWriter, r *http.Request, dev store.Device, body []byte) {
	q := r.URL.Query()
	if len(body) != 0 || len(q) != 1 || len(q[hubproto.QueryRelayVersion]) != 1 ||
		q.Get(hubproto.QueryRelayVersion) != strconv.Itoa(hubproto.RelayControlVersion) {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	ws, err := hubproto.AcceptWebSocket(w, r, s.wsConfig(nil))
	if err != nil {
		s.logf("relay: control upgrade failed device=%s", short(dev.ID))
		return
	}
	run := s.cfg.Relay.AttachControl(dev.ID, dev.Org, ws)
	s.logf("relay: control up device=%s org=%s", short(dev.ID), dev.Org)
	run()
	s.logf("relay: control down device=%s org=%s", short(dev.ID), dev.Org)
}

// ---- GET /v1/relay/accept ----------------------------------------------------

func (s *Server) handleRelayAccept(w http.ResponseWriter, r *http.Request, dev store.Device, body []byte) {
	q := r.URL.Query()
	conn := q.Get(hubproto.QueryRelayConn)
	if len(body) != 0 || len(q) != 1 || len(q[hubproto.QueryRelayConn]) != 1 || !hubproto.ValidRelayID(conn) {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	// One answer for unknown, expired, claimed, abandoned and another
	// device's conn.
	p, ok := s.cfg.Relay.Claim(conn, dev.ID)
	if !ok {
		writeError(w, http.StatusNotFound, hubproto.CodeNotFound)
		return
	}
	ws, err := hubproto.AcceptWebSocket(w, r, s.wsConfig(p.OnDaemonFrame))
	if err != nil {
		// The entry stays claimed with nothing in ready; the phone handler's
		// accept timeout abandons it.
		return
	}
	if !s.cfg.Relay.Deliver(p, ws) {
		_ = ws.CloseNow(hubproto.RelayClosePeerGone)
	}
}

// ---- GET /v1/relay/phone -----------------------------------------------------

// phoneFail answers a refused phone dial: an ordinary HTTP response with the
// ErrorResponse body and the code also in Lola-Relay-Code, because the phone's
// URL loading system exposes a refused upgrade's headers but not its body.
func phoneFail(w http.ResponseWriter, status int, code string, retryAfter int) {
	w.Header().Set(hubproto.HeaderRelayCode, code)
	if retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	}
	writeError(w, status, code)
}

// one returns the only value of header name, or false.
func one(r *http.Request, name string) (string, bool) {
	v := r.Header.Values(name)
	if len(v) != 1 {
		return "", false
	}
	return v[0], true
}

// dialRetryAfter is the Retry-After of a throttled dial: the slowest bucket's
// refill time, rounded up.
const dialRetryAfter = 5

func (s *Server) handleRelayPhone(w http.ResponseWriter, r *http.Request) {
	rh := s.cfg.Relay
	if r.Method != http.MethodGet {
		phoneFail(w, http.StatusMethodNotAllowed, hubproto.CodeBadRequest, 0)
		return
	}
	// 1. Not an upgrade (or a browser's).
	if upgradeErr(w, r, func(c string) { w.Header().Set(hubproto.HeaderRelayCode, c) }) {
		return
	}
	// 2. Per client network, only with a verified source.
	if s.cfg.ClientIP != nil {
		if pfx, ok := s.cfg.ClientIP(r); ok && !rh.AllowIP(pfx) {
			phoneFail(w, http.StatusTooManyRequests, hubproto.CodeRateLimited, dialRetryAfter)
			return
		}
	}
	// 3 and 4. Credential shape, then the store. Every failure below answers
	// alike: unknown rv, wrong bearer, expired window, revoked device.
	var (
		device, org string
		window      bool
		valid       bool
	)
	rv, okRV := one(r, hubproto.HeaderRelayRV)
	bearer, okB := one(r, hubproto.HeaderRelayBearer)
	rv = onlyIf(okRV && hubproto.ValidRelayID(rv), rv)
	bearer = onlyIf(okB && hubproto.ValidRelayBearer(bearer), bearer)
	if rv != "" && bearer != "" {
		now := s.cfg.Now()
		if err := s.cfg.Store.View(func(d *store.Data) error {
			e := d.Relay[rv]
			if e == nil || !e.Live(now) || !e.Matches(hubproto.RelayBearerHash(bearer), now) {
				return nil
			}
			dev := d.Devices[e.Device]
			if dev == nil || dev.Revoked() {
				return nil
			}
			device, org, window, valid = dev.ID, dev.Org, e.Window(), true
			return nil
		}); err != nil {
			s.logf("relay: store view failed: %v", err)
			phoneFail(w, http.StatusInternalServerError, hubproto.CodeInternal, 0)
			return
		}
	}
	// 5. A failed probe spends only the unauthenticated bucket.
	if !valid {
		if !rh.AllowUnauth() {
			phoneFail(w, http.StatusTooManyRequests, hubproto.CodeRateLimited, dialRetryAfter)
			return
		}
		phoneFail(w, http.StatusUnauthorized, hubproto.CodeUnauthorized, 0)
		return
	}
	s.spliceDial(w, r, device, org, rv, window, rv)
}

// spliceDial is steps 6 to 9 of a relay dial, shared by the phone leg and the
// member leg: the dial bucket (keyed by bucketKey), a connection slot under the
// OWNER device, the offer to its control channel, the wait for its accept leg
// and the splice. Every failure is an HTTP status before any upgrade.
func (s *Server) spliceDial(w http.ResponseWriter, r *http.Request, device, org, rv string, window bool, bucketKey string) {
	rh := s.cfg.Relay
	// 6.
	if !rh.AllowAuth(bucketKey) {
		phoneFail(w, http.StatusTooManyRequests, hubproto.CodeRateLimited, dialRetryAfter)
		return
	}
	// 7.
	release, err := rh.Reserve(device, org, rv)
	if err != nil {
		if errors.Is(err, relay.ErrBusy) {
			phoneFail(w, http.StatusServiceUnavailable, hubproto.CodeBusy, 0)
		} else {
			phoneFail(w, http.StatusInternalServerError, hubproto.CodeInternal, 0)
		}
		return
	}
	// 8. Offer the dial and wait for the daemon's leg. The phone gets its 101
	// only once that leg exists, so every failure it sees is an HTTP status.
	p, err := rh.Offer(device, org, rv, release)
	if err != nil {
		release()
		if errors.Is(err, relay.ErrOffline) {
			phoneFail(w, http.StatusServiceUnavailable, hubproto.CodeOffline, 0)
		} else {
			phoneFail(w, http.StatusInternalServerError, hubproto.CodeInternal, 0)
		}
		return
	}
	if window {
		p.MarkWindow()
	}
	timeout := s.acceptTimeout
	if timeout <= 0 {
		timeout = hubproto.RelayAcceptTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var daemon *hubproto.WSConn
	select {
	case daemon = <-p.Ready():
	case reason := <-p.Rejected():
		rh.Abandon(p)
		if reason == "unknown" {
			phoneFail(w, http.StatusUnauthorized, hubproto.CodeUnauthorized, 0)
		} else {
			phoneFail(w, http.StatusServiceUnavailable, hubproto.CodeBusy, 0)
		}
		return
	case <-p.Gone():
		rh.Abandon(p)
		phoneFail(w, http.StatusServiceUnavailable, hubproto.CodeOffline, 0)
		return
	case <-timer.C:
		rh.Abandon(p)
		phoneFail(w, http.StatusGatewayTimeout, hubproto.CodeRelayTimeout, 0)
		return
	case <-r.Context().Done():
		rh.Abandon(p)
		return
	}
	// 9. The phone's upgrade. A failure closes the daemon's leg.
	phone, err := hubproto.AcceptWebSocket(w, r, s.wsConfig(p.OnPhoneFrame))
	if err != nil {
		_ = daemon.CloseNow(hubproto.RelayClosePeerGone)
		rh.Abandon(p)
		return
	}
	if !rh.Take(p, daemon) {
		_ = phone.CloseNow(hubproto.RelayClosePeerGone)
		_ = daemon.CloseNow(hubproto.RelayClosePeerGone)
		rh.Abandon(p)
		return
	}
	rh.Splice(p, phone)
}

func onlyIf(ok bool, v string) string {
	if ok {
		return v
	}
	return ""
}

// ---- sweep and recheck ---------------------------------------------------------

// RelaySweep drops what the relay table no longer needs (expired pairing
// windows, previous bearers past their grace, every entry of a revoked or
// deleted device) and closes the relay connections of dead devices. It is safe
// to call often and without a relay core.
func (s *Server) RelaySweep() {
	if s.cfg.Relay == nil {
		return
	}
	now := s.cfg.Now()
	err := s.cfg.Store.Update(func(d *store.Data) error {
		changed := false
		for rv, e := range d.Relay {
			dev := d.Devices[e.Device]
			switch {
			case dev == nil || dev.Revoked(), e.Window() && !now.Before(e.ExpiresAt):
				delete(d.Relay, rv)
				changed = true
			case e.PrevHash != "" && !e.PrevUntil.After(now):
				e.PrevHash, e.PrevUntil = "", time.Time{}
				changed = true
			}
		}
		if !changed {
			return errNoChange
		}
		return nil
	})
	if err != nil && !errors.Is(err, errNoChange) {
		s.logf("relay: sweep failed: %v", err)
	}
	s.RelayRecheck()
}

// RelayRecheck closes the control link and splices of every device the store
// reports revoked or gone. A store that cannot answer closes nothing. serve.go
// runs it every RelayRecheckInterval; an API revoke runs it after the commit.
func (s *Server) RelayRecheck() {
	if s.cfg.Relay == nil {
		return
	}
	s.cfg.Relay.Recheck(func(device string) bool {
		live := true
		if err := s.cfg.Store.View(func(d *store.Data) error {
			dev := d.Devices[device]
			live = dev != nil && !dev.Revoked()
			return nil
		}); err != nil {
			return true
		}
		return live
	})
}

// short is the first 8 characters of an id: all a log line may carry of one.
func short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
