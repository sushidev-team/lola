package hub

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/sushidev-team/lola/hubproto"
)

// Team session sharing (docs/team-sessions.md). The hub routes and lists; every
// response is validated whole before it is returned, and nothing here grants
// access: the owner's daemon decides by the pinned inner-TLS key.

// PublishShares replaces this device's whole share set (empty clears it). A
// hub that predates sharing answers 404, which callers treat as "off".
func (c *Client) PublishShares(ctx context.Context, set hubproto.ShareSet) error {
	if set.Shares == nil {
		set.Shares = []hubproto.ShareEntry{}
	}
	if err := set.Validate(); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, hubproto.PathShares, set, nil)
}

// ListShares returns the shares other devices of the org offer this one.
func (c *Client) ListShares(ctx context.Context) (hubproto.ShareList, error) {
	var out hubproto.ShareList
	if err := c.view(ctx, hubproto.PathShares, &out); err != nil {
		return hubproto.ShareList{}, err
	}
	if err := out.Validate(); err != nil {
		return hubproto.ShareList{}, invalidResponse("shares: " + err.Error())
	}
	return out, nil
}

// PublishIdentity publishes this device's binding of its hub key to its inner
// TLS key (spki, std base64 of the SHA-256 of its SubjectPublicKeyInfo).
func (c *Client) PublishIdentity(ctx context.Context, spki string) error {
	id, err := hubproto.SignBinding(c.key.priv, spki)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, hubproto.PathShareIdentity, id, nil)
}

// Members lists the org's other devices that published an identity. Each
// row's binding is verified against its own key before the list is returned.
func (c *Client) Members(ctx context.Context) (hubproto.MemberList, error) {
	var out hubproto.MemberList
	if err := c.view(ctx, hubproto.PathShareMembers, &out); err != nil {
		return hubproto.MemberList{}, err
	}
	if err := out.Validate(); err != nil {
		return hubproto.MemberList{}, invalidResponse("members: " + err.Error())
	}
	return out, nil
}

// DialMember opens a teammate's leg to a share's owner (GET /v1/relay/member,
// signed). The stream is the start of an inner mutual TLS session: the caller
// wraps it in tls.Client and pins the owner's key.
func (rc *RelayClient) DialMember(ctx context.Context, share string) (*hubproto.WSConn, error) {
	if !hubproto.ValidShareID(share) {
		return nil, errors.New("hub: invalid share id")
	}
	q := url.Values{hubproto.QueryShare: {share}}
	return rc.dial(ctx, hubproto.PathRelayMember+"?"+q.Encode(), hubproto.WSConfig{
		PingInterval: hubproto.RelayPingInterval,
		DeadAfter:    hubproto.RelayDeadAfter,
		WriteStall:   hubproto.RelayWriteStall,
	})
}
