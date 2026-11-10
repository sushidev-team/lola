package hub

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
)

// DefaultRequestTimeout bounds one request end to end (connect, send, read)
// when Client.Timeout is zero. The hub loop runs on the shutdown-shielded
// context, so this bound is what keeps a wedged hub from hanging shutdown.
const DefaultRequestTimeout = 20 * time.Second

// maxErrorText caps hub-provided text (error messages, rejection reasons)
// carried into an error or a status line.
const maxErrorText = 200

// Doer is the HTTP seam: *http.Client satisfies it, tests inject their own.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client talks to one hub as one device. Every request is signed with the
// device key (hubproto.Sign), every body is bounded (hubproto.MaxBody) and
// strictly decoded, and every response is validated before it is returned —
// a hub is never trusted to send well-formed data.
type Client struct {
	base string // scheme://host[:port], no trailing slash
	key  *Key

	// HTTP is the transport seam. The default refuses redirects (a redirect
	// would land a signed request somewhere the operator never configured)
	// and carries its own overall timeout.
	HTTP Doer
	// Timeout bounds each call (0 = DefaultRequestTimeout).
	Timeout time.Duration
	// Now and Nonce are the clock / nonce seams (nil = real).
	Now   func() time.Time
	Nonce func() (string, error)
}

// NewClient builds a client for baseURL (validated with config.ValidateHubURL)
// signing as key.
func NewClient(baseURL string, key *Key) (*Client, error) {
	if key == nil || len(key.priv) != ed25519.PrivateKeySize {
		return nil, errors.New("hub: client needs a device key")
	}
	if err := config.ValidateHubURL(baseURL); err != nil {
		return nil, err
	}
	return &Client{
		base: strings.TrimSuffix(baseURL, "/"),
		key:  key,
		HTTP: &http.Client{
			Timeout: DefaultRequestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// DeviceID is the id this client signs as.
func (c *Client) DeviceID() string { return c.key.DeviceID() }

// APIError is a non-2xx answer. Code is the hub's machine code when it sent a
// well-formed hubproto.ErrorResponse, Message its (sanitized, clipped) text.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	s := fmt.Sprintf("hub: HTTP %d", e.Status)
	if e.Code != "" {
		s += " " + e.Code
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	return s
}

// IsCode reports whether err is an APIError carrying code.
func IsCode(err error, code string) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == code
}

// ErrInvalidResponse wraps every response that failed validation.
var ErrInvalidResponse = errors.New("hub: invalid response")

func invalidResponse(why string) error { return fmt.Errorf("%w: %s", ErrInvalidResponse, why) }

// Enroll trades a one-time token for membership. The request is signed with
// the key being enrolled, which proves possession of it.
func (c *Client) Enroll(ctx context.Context, token, deviceName string) (hubproto.EnrollResponse, error) {
	req := hubproto.EnrollRequest{Token: token, DeviceName: deviceName, PublicKey: c.key.PublicString()}
	if err := req.Validate(); err != nil {
		return hubproto.EnrollResponse{}, err
	}
	var resp hubproto.EnrollResponse
	if err := c.do(ctx, http.MethodPost, hubproto.PathEnroll, req, &resp); err != nil {
		return hubproto.EnrollResponse{}, err
	}
	if resp.DeviceID != c.DeviceID() {
		return hubproto.EnrollResponse{}, invalidResponse("device_id does not match this key")
	}
	if !hubproto.ValidOrg(resp.Org) {
		return hubproto.EnrollResponse{}, invalidResponse("org")
	}
	return resp, nil
}

// Heartbeat reports capacity and renews the leases named in LiveLeases.
func (c *Client) Heartbeat(ctx context.Context, hb hubproto.HeartbeatRequest) (hubproto.HeartbeatResponse, error) {
	if err := hb.Validate(); err != nil {
		return hubproto.HeartbeatResponse{}, err
	}
	var resp hubproto.HeartbeatResponse
	if err := c.do(ctx, http.MethodPost, hubproto.PathHeartbeat, hb, &resp); err != nil {
		return hubproto.HeartbeatResponse{}, err
	}
	if len(resp.Renewed) > hubproto.MaxLiveLeases || len(resp.Lost) > hubproto.MaxLiveLeases {
		return hubproto.HeartbeatResponse{}, invalidResponse("too many leases")
	}
	for _, l := range resp.Renewed {
		if err := l.Validate(); err != nil {
			return hubproto.HeartbeatResponse{}, invalidResponse("renewed lease")
		}
		if l.Device != c.DeviceID() {
			return hubproto.HeartbeatResponse{}, invalidResponse("renewed lease names another device")
		}
	}
	for _, id := range resp.Lost {
		if !hubproto.ValidItemID(id) {
			return hubproto.HeartbeatResponse{}, invalidResponse("lost item id")
		}
	}
	return resp, nil
}

// ReportFindings posts validated findings (items carry no ID). An empty batch
// is not sent.
func (c *Client) ReportFindings(ctx context.Context, items []hubproto.Item) (hubproto.FindingsResponse, error) {
	if len(items) == 0 {
		return hubproto.FindingsResponse{}, nil
	}
	req := hubproto.FindingsRequest{Items: items}
	if err := req.Validate(); err != nil {
		return hubproto.FindingsResponse{}, err
	}
	var resp hubproto.FindingsResponse
	if err := c.do(ctx, http.MethodPost, hubproto.PathFindings, req, &resp); err != nil {
		return hubproto.FindingsResponse{}, err
	}
	if resp.Accepted < 0 || resp.Accepted > len(items) || len(resp.Rejected) > len(items) {
		return hubproto.FindingsResponse{}, invalidResponse("findings counts")
	}
	for i := range resp.Rejected {
		if resp.Rejected[i].Index < 0 || resp.Rejected[i].Index >= len(items) {
			return hubproto.FindingsResponse{}, invalidResponse("rejection index")
		}
		resp.Rejected[i].Reason = cleanText(resp.Rejected[i].Reason)
	}
	return resp, nil
}

// Offers lists the work items offered to this device. Every item is checked
// with hubproto.Validate and must carry a valid, unique ID; the ones that do
// not are DROPPED (never claimed) and counted in dropped.
func (c *Client) Offers(ctx context.Context) (items []hubproto.Item, dropped int, err error) {
	var resp hubproto.OffersResponse
	if err := c.do(ctx, http.MethodGet, hubproto.PathOffers, nil, &resp); err != nil {
		return nil, 0, err
	}
	if len(resp.Items) > hubproto.MaxOfferItems {
		return nil, 0, invalidResponse("too many offers")
	}
	seen := map[string]bool{}
	for _, it := range resp.Items {
		if !hubproto.ValidItemID(it.ID) || seen[it.ID] || hubproto.Validate(it) != nil {
			dropped++
			continue
		}
		seen[it.ID] = true
		items = append(items, it)
	}
	return items, dropped, nil
}

// Claim takes a lease on item id.
func (c *Client) Claim(ctx context.Context, id string) (hubproto.Lease, error) {
	path := hubproto.ClaimPath(id)
	if path == "" {
		return hubproto.Lease{}, errors.New("hub: invalid item id")
	}
	var l hubproto.Lease
	if err := c.do(ctx, http.MethodPost, path, nil, &l); err != nil {
		return hubproto.Lease{}, err
	}
	if l.Validate() != nil || l.ItemID != id || l.Device != c.DeviceID() {
		return hubproto.Lease{}, invalidResponse("lease")
	}
	return l, nil
}

// Complete reports a finished item with its PR URL and delivery state.
func (c *Client) Complete(ctx context.Context, id string, req hubproto.CompleteRequest) error {
	path := hubproto.CompletePath(id)
	if path == "" {
		return errors.New("hub: invalid item id")
	}
	if err := req.Validate(); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, path, req, nil)
}

// Fail reports a failed item with a reason from the closed set.
func (c *Client) Fail(ctx context.Context, id string, reason hubproto.FailReason) error {
	path := hubproto.FailPath(id)
	if path == "" {
		return errors.New("hub: invalid item id")
	}
	req := hubproto.FailRequest{Reason: reason}
	if err := req.Validate(); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, path, req, nil)
}

// Release gives a claimed item back without counting a failure.
func (c *Client) Release(ctx context.Context, id string) error {
	path := hubproto.ReleasePath(id)
	if path == "" {
		return errors.New("hub: invalid item id")
	}
	return c.do(ctx, http.MethodPost, path, nil, nil)
}

// Policy fetches the team policy bundle, strictly decoded but NOT verified:
// FetchPolicy (policy.go) is the verifying caller. A hub with no policy
// answers 404 no_policy (IsCode(err, hubproto.CodeNoPolicy)).
func (c *Client) Policy(ctx context.Context) (hubproto.Policy, error) {
	raw, err := c.raw(ctx, http.MethodGet, hubproto.PathPolicy, nil)
	if err != nil {
		return hubproto.Policy{}, err
	}
	return hubproto.DecodePolicy(raw)
}

// do sends one signed request and strictly decodes the 2xx body into out
// (nil = the body is drained and ignored).
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	raw, err := c.raw(ctx, method, path, in)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := hubproto.DecodeStrict(bytes.NewReader(raw), out); err != nil {
		return invalidResponse("body does not decode")
	}
	return nil
}

func (c *Client) raw(ctx context.Context, method, path string, in any) ([]byte, error) {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return nil, err
		}
		if len(body) > hubproto.MaxBody {
			return nil, hubproto.ErrTooLarge
		}
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	nonceFn := hubproto.NewNonce
	if c.Nonce != nil {
		nonceFn = c.Nonce
	}
	nonce, err := nonceFn()
	if err != nil {
		return nil, err
	}
	if err := hubproto.SignAt(req, c.DeviceID(), c.key.priv, body, now(), nonce); err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hub: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, hubproto.MaxBody+1))
	if err != nil {
		return nil, fmt.Errorf("hub: read %s: %w", path, err)
	}
	if len(data) > hubproto.MaxBody {
		return nil, hubproto.ErrTooLarge
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		ae := &APIError{Status: resp.StatusCode}
		var er hubproto.ErrorResponse
		if json.Unmarshal(data, &er) == nil {
			ae.Code = cleanCode(er.Code)
			ae.Message = cleanText(er.Error)
		}
		return nil, ae
	}
	return data, nil
}

// cleanText strips control characters and clips hub-provided text.
func cleanText(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n >= maxErrorText {
			b.WriteString("…")
			break
		}
		if unicode.IsControl(r) || r == unicode.ReplacementChar {
			r = ' '
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

// cleanCode keeps a machine code only when it is plain identifier text.
func cleanCode(s string) string {
	if len(s) == 0 || len(s) > 32 {
		return ""
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r == '_') {
			return ""
		}
	}
	return s
}
