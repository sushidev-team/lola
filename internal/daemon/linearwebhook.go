package daemon

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/secrets"
)

// The Linear agent's WEBHOOK DOORBELL ([linear_agent].webhook_listen).
//
// Linear pushes Agent Session events only to a public HTTPS URL, which a
// laptop does not have — so the agent loop POLLS, and this optional receiver
// exists for operators who run their own tunnel to it. It is deliberately a
// doorbell and not a data path: a delivery whose signature verifies does
// exactly one thing, ring the loop so it polls NOW. The payload is never
// parsed for routing, so a replayed or reordered delivery can at worst cost
// one extra poll, and the poll — authenticated as the agent, against Linear's
// API — stays the only source of truth.
//
// Rules: POST only; the body is bounded; the `Linear-Signature` header must be
// the hex HMAC-SHA256 of the raw body under the signing secret (compared in
// constant time); a `webhookTimestamp` in the body older than a minute is
// refused; no secret resolvable means every delivery is refused, never
// accepted unsigned. The listener answers within milliseconds — Linear expects
// a reply in seconds and retries otherwise.

const (
	webhookMaxBody  = 1 << 20
	webhookMaxSkew  = time.Minute
	webhookSigHdr   = "Linear-Signature"
	webhookReadTime = 10 * time.Second
)

// startLinearAgentWebhook binds the doorbell when configured. A bind failure
// is logged, never fatal: the poll covers for it.
func (d *Daemon) startLinearAgentWebhook(ctx context.Context) {
	d.mu.Lock()
	cfg := d.cfg.LinearAgent
	d.mu.Unlock()
	if !cfg.Enabled || cfg.WebhookListen == "" {
		return
	}
	ln, err := net.Listen("tcp", cfg.WebhookListen)
	if err != nil {
		d.logf("", "linear agent: webhook listener on %s failed (polling continues): %v", cfg.WebhookListen, err)
		return
	}
	srv := &http.Server{
		Handler:           d.linearWebhookHandler(func() config.LinearAgentConfig { d.mu.Lock(); defer d.mu.Unlock(); return d.cfg.LinearAgent }),
		ReadHeaderTimeout: webhookReadTime,
		ReadTimeout:       webhookReadTime,
		WriteTimeout:      webhookReadTime,
	}
	d.agent.mu.Lock()
	d.agent.webhookOn = ln.Addr().String()
	d.agent.mu.Unlock()
	d.logf("", "linear agent: webhook doorbell listening on %s", ln.Addr())
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			d.logf("", "linear agent: webhook listener stopped: %v", err)
		}
	}()
}

// linearWebhookHandler verifies a delivery and rings the agent loop.
func (d *Daemon) linearWebhookHandler(cfg func() config.LinearAgentConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, webhookMaxBody+1))
		if err != nil || len(body) > webhookMaxBody {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		c := cfg()
		secret, err := d.webhookSecret(c)
		if err != nil || secret == "" {
			http.Error(w, "webhook secret not configured", http.StatusServiceUnavailable)
			return
		}
		if !verifyLinearSignature(body, r.Header.Get(webhookSigHdr), secret, time.Now()) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
		d.wakeLinearAgent()
		w.WriteHeader(http.StatusOK)
	})
}

// webhookSecret resolves the signing secret (seam for tests).
func (d *Daemon) webhookSecret(c config.LinearAgentConfig) (string, error) {
	if d.agentWebhookSecret != nil {
		return d.agentWebhookSecret(c)
	}
	return secrets.Resolve("linear agent webhook secret", c.WebhookSecretKeychain, c.WebhookSecretEnv)
}

// verifyLinearSignature checks the HMAC and, when the body carries one, the
// delivery timestamp (milliseconds since the epoch).
func verifyLinearSignature(body []byte, sigHex, secret string, now time.Time) bool {
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
		if now.Sub(sent) > webhookMaxSkew || sent.Sub(now) > webhookMaxSkew {
			return false
		}
	}
	return true
}
