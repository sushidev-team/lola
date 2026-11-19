package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// The Linear webhook doorbell. Linear's agent app can post its events to a
// public address; a developer's laptop has none, the hub does. The hub is only
// the BELL: a delivery whose HMAC (and timestamp) verifies against the org's
// sealed webhook secret bumps the org's counter, the daemons see the counter
// change on their next heartbeat and poll Linear at once. Nothing in the
// payload is parsed for routing, stored or forwarded — a compromised hub or a
// forged delivery can make a daemon poll early, never make it do anything.
const (
	linearHookMaxBody = 64 << 10
	linearHookMaxSkew = 2 * time.Minute
	linearHookSigHdr  = "Linear-Signature"

	// A burst of agent events (one per activity) must not be refused, but one
	// org cannot be rung faster than this for long.
	linearHookRatePerSec = 5.0
	linearHookBurst      = 50
)

func (s *Server) handleLinearHook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" {
		writeError(w, http.StatusMethodNotAllowed, hubproto.CodeBadRequest)
		return
	}
	org := strings.TrimPrefix(r.URL.Path, hubproto.PathLinearHook)
	// Every refusal is the same answer: the endpoint never says whether an org
	// exists or has a secret.
	deny := func() { writeError(w, http.StatusUnauthorized, hubproto.CodeUnauthorized) }
	if !hubproto.ValidOrg(org) || s.cfg.Admin == nil {
		deny()
		return
	}
	known := false
	_ = s.cfg.Store.View(func(d *store.Data) error { known = d.Orgs[org] != nil; return nil })
	// Buckets are keyed only by orgs that exist, so the map stays bounded.
	key := "-"
	if known {
		key = org
	}
	now := s.cfg.Now()
	if !s.hook.allow(key, now) {
		writeError(w, http.StatusTooManyRequests, hubproto.CodeRateLimited)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, linearHookMaxBody+1))
	if err != nil || len(body) > linearHookMaxBody {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	secret, ok := s.cfg.Admin.LinearWebhookSecret(org)
	if !known || !ok || !verifyLinearDelivery(body, r.Header.Get(linearHookSigHdr), secret, now) {
		deny()
		return
	}
	_ = s.cfg.Store.Update(func(d *store.Data) error {
		if o := d.Orgs[org]; o != nil {
			o.Doorbell++
		}
		return nil
	})
	s.bell.ring(org)
	writeJSON(w, http.StatusOK, struct{}{})
}

// verifyLinearDelivery checks Linear's HMAC-SHA256 (hex) over the raw body and,
// when the body carries one, its delivery timestamp (ms since the epoch).
func verifyLinearDelivery(body []byte, sigHex, secret string, now time.Time) bool {
	got, err := hex.DecodeString(strings.TrimSpace(sigHex))
	if err != nil || len(got) == 0 {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return false
	}
	var ts struct {
		WebhookTimestamp int64 `json:"webhookTimestamp"`
	}
	if json.Unmarshal(body, &ts) == nil && ts.WebhookTimestamp > 0 {
		sent := time.UnixMilli(ts.WebhookTimestamp)
		if now.Sub(sent) > linearHookMaxSkew || sent.Sub(now) > linearHookMaxSkew {
			return false
		}
	}
	return true
}

// bells wakes the long-polls held on an org's doorbell: ring closes the org's
// current channel (everyone waiting wakes) and installs a fresh one.
type bells struct {
	mu sync.Mutex
	ch map[string]chan struct{}
}

func (b *bells) wait(org string) <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ch == nil {
		b.ch = map[string]chan struct{}{}
	}
	c := b.ch[org]
	if c == nil {
		c = make(chan struct{})
		b.ch[org] = c
	}
	return c
}

func (b *bells) ring(org string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c := b.ch[org]; c != nil {
		close(c)
		delete(b.ch, org)
	}
}

func (s *Server) doorbell(org string) uint64 {
	var n uint64
	_ = s.cfg.Store.View(func(d *store.Data) error {
		if o := d.Orgs[org]; o != nil {
			n = o.Doorbell
		}
		return nil
	})
	return n
}

// handleDoorbell is the signed long-poll behind a daemon's Linear doorbell:
// it answers at once when the org's counter differs from ?since=, else holds
// for hubproto.DoorbellHold (or until the client goes away) and answers with
// whatever the counter is then.
func (s *Server) handleDoorbell(w http.ResponseWriter, r *http.Request, dev store.Device, body []byte) {
	q := r.URL.Query()
	if len(body) != 0 || len(q) > 1 {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	var since uint64
	if v := q.Get("since"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
			return
		}
		since = n
	} else if len(q) != 0 {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	// Subscribe BEFORE reading the counter, so a ring between the two is not lost.
	wake := s.bell.wait(dev.Org)
	if cur := s.doorbell(dev.Org); cur != since {
		writeJSON(w, http.StatusOK, hubproto.DoorbellResponse{Doorbell: cur})
		return
	}
	t := time.NewTimer(hubproto.DoorbellHold)
	defer t.Stop()
	select {
	case <-wake:
	case <-t.C:
	case <-r.Context().Done():
		return
	}
	writeJSON(w, http.StatusOK, hubproto.DoorbellResponse{Doorbell: s.doorbell(dev.Org)})
}
