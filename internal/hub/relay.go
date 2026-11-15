package hub

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
)

// RelayClient speaks the relay's WebSocket endpoints for one identity. It
// holds no daemon state: the caller owns the loop, the backoff and the status.
type RelayClient struct {
	BaseURL string
	Key     *Key
	Dialer  hubproto.WSDialer // seam; tests inject a loopback dialer
}

// SyncRendezvous replaces the hub's whole rendezvous set for this device. The
// request is validated before it is sent; the answer is bounded, and a
// conflict naming an rv that was not in the request is dropped (a hub never
// gets to tell the daemon about entries it did not ask about).
func (c *Client) SyncRendezvous(ctx context.Context, entries []hubproto.RelayRendezvous) (hubproto.RendezvousResponse, error) {
	if entries == nil {
		entries = []hubproto.RelayRendezvous{}
	}
	req := hubproto.RendezvousRequest{Entries: entries}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	if err := req.Validate(now()); err != nil {
		return hubproto.RendezvousResponse{}, err
	}
	var resp hubproto.RendezvousResponse
	if err := c.do(ctx, http.MethodPost, hubproto.PathRelayRendezvous, req, &resp); err != nil {
		return hubproto.RendezvousResponse{}, err
	}
	if resp.Registered < 0 || resp.Registered > len(entries) {
		return hubproto.RendezvousResponse{}, invalidResponse("rendezvous counts")
	}
	asked := make(map[string]bool, len(entries))
	for _, e := range entries {
		asked[e.RV] = true
	}
	var kept []string
	for _, rv := range resp.Conflicts {
		if asked[rv] {
			kept = append(kept, rv)
			delete(asked, rv) // one report per rv
		}
	}
	resp.Conflicts = kept
	return resp, nil
}

// DialControl opens the control channel (GET /v1/relay/daemon?v=1, signed).
func (rc *RelayClient) DialControl(ctx context.Context) (*hubproto.WSConn, error) {
	q := url.Values{hubproto.QueryRelayVersion: {"1"}}
	return rc.dial(ctx, hubproto.PathRelayDaemon+"?"+q.Encode(), hubproto.WSConfig{
		PingInterval: hubproto.RelayPingInterval,
		DeadAfter:    hubproto.RelayDeadAfter,
		WriteStall:   hubproto.RelayWriteStall,
	})
}

// DialAccept opens the daemon's data leg for conn (GET /v1/relay/accept, signed).
func (rc *RelayClient) DialAccept(ctx context.Context, conn string) (*hubproto.WSConn, error) {
	if !hubproto.ValidRelayID(conn) {
		return nil, errors.New("hub: invalid relay conn id")
	}
	q := url.Values{hubproto.QueryRelayConn: {conn}}
	return rc.dial(ctx, hubproto.PathRelayAccept+"?"+q.Encode(), hubproto.WSConfig{
		DeadAfter:  hubproto.RelayDeadAfter, // the hub pings accept legs
		WriteStall: hubproto.RelayWriteStall,
	})
}

func (rc *RelayClient) dial(ctx context.Context, requestURI string, cfg hubproto.WSConfig) (*hubproto.WSConn, error) {
	if rc.Key == nil || len(rc.Key.priv) != ed25519.PrivateKeySize {
		return nil, errors.New("hub: relay client needs a device key")
	}
	if err := config.ValidateHubURL(rc.BaseURL); err != nil {
		return nil, err
	}
	base := rc.BaseURL
	for len(base) > 0 && base[len(base)-1] == '/' {
		base = base[:len(base)-1]
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+requestURI, nil)
	if err != nil {
		return nil, err
	}
	if err := hubproto.Sign(req, rc.Key.DeviceID(), rc.Key.priv, nil); err != nil {
		return nil, err
	}
	d := rc.Dialer
	d.Config = cfg
	conn, err := d.Dial(ctx, req)
	if err != nil {
		var he *hubproto.WSHandshakeError
		if errors.As(err, &he) {
			// Hub-supplied values are filtered here, once: nothing downstream
			// sees a raw code or text.
			return nil, &APIError{Status: he.Status, Code: cleanCode(he.Code), RetryAfter: clampRetryAfter(he.RetryAfter)}
		}
		return nil, err
	}
	return conn, nil
}

// clampRetryAfter bounds a hub-supplied Retry-After to a day.
func clampRetryAfter(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	if d > 24*time.Hour {
		return 24 * time.Hour
	}
	return d
}

// RelayStatusFile is the relay status file in the state dir (0600, atomic),
// read by doctor without the daemon.
const RelayStatusFile = "hub-relay.json"

// RelayStatus is what the relay loop last knew about itself. Every string is
// written by the daemon from a closed vocabulary or a filtered error.
type RelayStatus struct {
	State            string    `json:"state"`
	Reason           string    `json:"reason,omitempty"`
	Since            time.Time `json:"since"`
	LastError        string    `json:"last_error,omitempty"`
	LastErrAt        time.Time `json:"last_error_at,omitzero"`
	Registered       int       `json:"registered"`                  // entries the hub holds for us, as far as we know
	RegisteredURL    string    `json:"registered_url,omitempty"`    // hub those entries live on
	RegisteredDevice string    `json:"registered_device,omitempty"` // hub device id that registered them
}

// SaveRelayStatus writes dir/hub-relay.json atomically (0600).
func SaveRelayStatus(dir string, st RelayStatus) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return writeAtomic(dir, RelayStatusFile, append(data, '\n'))
}

// LoadRelayStatus reads the status file; false when it is absent or unreadable.
func LoadRelayStatus(dir string) (RelayStatus, bool) {
	data, err := os.ReadFile(filepath.Join(dir, RelayStatusFile))
	if err != nil {
		return RelayStatus{}, false
	}
	var st RelayStatus
	if json.Unmarshal(data, &st) != nil {
		return RelayStatus{}, false
	}
	return st, true
}
