package hub

import (
	"strings"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
)

// Refusal reasons Accept returns. They are lola's own words (never hub text),
// stable so the daemon loop and status can log or count them.
const (
	ReasonHubDisabled  = "hub disabled"
	ReasonInvalidItem  = "item does not validate"
	ReasonNoProject    = "no project"
	ReasonNotOptedIn   = "project has not opted in (hub_chores)"
	ReasonRepoMismatch = "item repo does not match the project"
	ReasonKindRejected = "kind not accepted"
	ReasonDailyCap     = "daily claim cap reached"
	ReasonNeedsSandbox = "project is not sandboxed (hub.require_sandbox)"
	ReasonStack        = "stacks take no hub chores"
)

// Accept is the daemon's PURE decision whether to claim item for project p. It
// is only the policy half: the daemon still runs dispatchHold, the health gate
// and reserveSlots before it claims. Order matters only for which reason is
// reported; every check must pass.
//
//   - the item validates (hubproto.Validate) — a hub is never trusted;
//   - the hub is enabled;
//   - p opted in (hub_chores) and its repo slug matches the item's
//     (case-insensitive: GitHub slugs are);
//   - the kind is accepted ([hub].accept_kinds, empty = all known kinds);
//   - fewer than [hub].max_claims_per_day claims today;
//   - when [hub].require_sandbox (default true), p runs sandboxed.
func Accept(item hubproto.Item, p *config.Project, h config.HubConfig, claimsToday int) (bool, string) {
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
	if !p.HubChores {
		return false, ReasonNotOptedIn
	}
	if p.Repo == "" || !strings.EqualFold(p.Repo, item.Repo()) {
		return false, ReasonRepoMismatch
	}
	if !h.AcceptsKind(item.Kind) {
		return false, ReasonKindRejected
	}
	if claimsToday >= h.ClaimsPerDay() {
		return false, ReasonDailyCap
	}
	if h.SandboxRequired() && !p.Sandboxed() {
		return false, ReasonNeedsSandbox
	}
	return true, ""
}

// ProjectFor returns the first opted-in project whose repo matches item's
// (case-insensitive), or nil. Pair it with Accept.
func ProjectFor(cfg *config.Config, item hubproto.Item) *config.Project {
	repo := item.Repo()
	if cfg == nil || repo == "" {
		return nil
	}
	for i := range cfg.Projects {
		p := &cfg.Projects[i]
		if p.HubChores && !p.IsStack() && strings.EqualFold(p.Repo, repo) {
			return p
		}
	}
	return nil
}

// OptedInRepos lists the distinct repo slugs of opted-in projects, in config
// order, capped at hubproto.MaxHeartbeatRepos — the heartbeat's Repos field
// and the findings collector's input.
func OptedInRepos(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for i := range cfg.Projects {
		p := &cfg.Projects[i]
		if !p.HubChores || p.IsStack() || hubproto.ValidateRepo(p.Repo) != nil {
			continue
		}
		k := strings.ToLower(p.Repo)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, p.Repo)
		if len(out) == hubproto.MaxHeartbeatRepos {
			break
		}
	}
	return out
}
