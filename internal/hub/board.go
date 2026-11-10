package hub

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
)

// The org board views (hubproto/board.go): READ-ONLY display data a member's
// desktop app draws. Both are decoded with hubproto.MaxViewBody and validated
// WHOLE: a response with one bad row is refused as a whole, never shown in
// part, so a compromised hub can show nothing rather than junk. Nothing here
// is acted on: a claim re-reads the item and goes through the claim path.

// Board lists the org's items (state "" = every state, limit <= 0 = the hub's
// default).
func (c *Client) Board(ctx context.Context, state hubproto.State, limit int) (hubproto.BoardResponse, error) {
	path := hubproto.BoardPath(state, limit)
	if path == "" {
		return hubproto.BoardResponse{}, errors.New("hub: invalid board query")
	}
	var resp hubproto.BoardResponse
	if err := c.view(ctx, path, &resp); err != nil {
		return hubproto.BoardResponse{}, err
	}
	if err := resp.Validate(); err != nil {
		return hubproto.BoardResponse{}, invalidResponse("board: " + err.Error())
	}
	return resp, nil
}

// Org is the org, its devices and the stored policy's version.
func (c *Client) Org(ctx context.Context) (hubproto.OrgView, error) {
	var resp hubproto.OrgView
	if err := c.view(ctx, hubproto.PathOrg, &resp); err != nil {
		return hubproto.OrgView{}, err
	}
	if err := resp.Validate(); err != nil {
		return hubproto.OrgView{}, invalidResponse("org: " + err.Error())
	}
	return resp, nil
}

func (c *Client) view(ctx context.Context, path string, out any) error {
	raw, err := c.raw(ctx, http.MethodGet, path, nil, hubproto.MaxViewBody)
	if err != nil {
		return err
	}
	if err := hubproto.DecodeStrictLimit(bytes.NewReader(raw), hubproto.MaxViewBody, out); err != nil {
		return invalidResponse("body does not decode")
	}
	return nil
}

// ProjectsFor lists every non-stack project whose repo matches item's
// (case-insensitive), opted in or not — the candidates for a HUMAN claim,
// where the click is the opt-in.
func ProjectsFor(cfg *config.Config, item hubproto.Item) []*config.Project {
	repo := item.Repo()
	if cfg == nil || repo == "" {
		return nil
	}
	var out []*config.Project
	for i := range cfg.Projects {
		p := &cfg.Projects[i]
		if !p.IsStack() && strings.EqualFold(p.Repo, repo) {
			out = append(out, p)
		}
	}
	return out
}

// AcceptIntent is Accept for a HUMAN claim (a person pressed Claim): the click
// is the opt-in, so hub_chores is not required, and a person's intent does
// not spend [hub].max_claims_per_day. Every other rule holds — the hub is
// enabled, the item validates, the repo matches, the kind is accepted, and
// require_sandbox still refuses an unsandboxed project: a person's click does
// not make a hub item safer to run on the host.
func AcceptIntent(item hubproto.Item, p *config.Project, h config.HubConfig) (bool, string) {
	if hubproto.Validate(item) != nil {
		return false, ReasonInvalidItem
	}
	if !h.Enabled {
		return false, ReasonHubDisabled
	}
	if p == nil {
		return false, ReasonNoProject
	}
	if p.IsStack() {
		return false, ReasonStack
	}
	if p.Repo == "" || !strings.EqualFold(p.Repo, item.Repo()) {
		return false, ReasonRepoMismatch
	}
	if !h.AcceptsKind(item.Kind) {
		return false, ReasonKindRejected
	}
	if h.SandboxRequired() && !p.Sandboxed() {
		return false, ReasonNeedsSandbox
	}
	return true, ""
}
