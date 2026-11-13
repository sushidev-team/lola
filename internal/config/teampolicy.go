package config

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/sushidev-team/lola/hubproto"
)

// The TEAM POLICY layer (Phase 2 of the hub). A policy verified against
// [hub].trusted_policy_keys LOCKS every key it carries: the daemon overlays
// those values onto its in-memory, already-resolved config after every Load
// and reload, and keys the policy does not carry are left exactly as the file
// says. The overlay is RUNTIME ONLY:
//
//   - It runs AFTER ResolveInheritance and touches no Inherits bit: it rewrites
//     resolved values, never where they came from.
//   - ApplyTeamPolicy marks the Config, and Save REFUSES a marked Config
//     (ErrTeamPolicyOverlay). A locked value can therefore never be frozen into
//     config.toml by a daemon path that happened to save its live config; such
//     a path must re-Load the file and edit that. The TUI and the app never
//     apply the overlay at all — they read and write the file.
//   - Validate is NOT re-run on the overlaid config: the file was validated
//     before the overlay, and the overlay is built so it cannot introduce what
//     validation rejects at runtime (a fallback to a now-disabled kind is
//     dropped, a provider that cannot run stays off).
//
// hubproto.Settings is a closed set of NON-executing keys, so nothing here can
// ever install a command. Keep it that way: a new key maps to a plain field.

// ErrTeamPolicyOverlay is returned by Save for a Config that carries the team
// policy overlay. Re-Load the file and save that instead.
var ErrTeamPolicyOverlay = errors.New("config: refusing to save a config with the team policy applied (re-load the file and edit that)")

// AppliedKey is one policy key the overlay enforced: Value is the team's
// value, Local what the file said before the overlay (both rendered for a
// human; neither is parsed back).
type AppliedKey struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Local string `json:"local"`
}

// TeamPolicyApplied reports whether ApplyTeamPolicy ran on c.
func (c *Config) TeamPolicyApplied() bool { return c.teamPolicy }

// ApplyTeamPolicy overlays the LOCKED keys of a verified policy onto c and
// returns what it enforced, in hubproto.Settings.Keys order. A key it cannot
// map cleanly is skipped with a notice (Notices, `lola doctor`), never
// guessed. Call it only with settings from a policy that verified against
// [hub].trusted_policy_keys; c must already be loaded (resolved).
func ApplyTeamPolicy(c *Config, s hubproto.Settings) []AppliedKey {
	c.teamPolicy = true
	if err := s.Validate(); err != nil {
		// A verified bundle already passed this; refusing is the only safe answer
		// to one that somehow did not.
		c.notices = append(c.notices, "hub policy: settings do not validate; nothing applied")
		return nil
	}
	var out []AppliedKey
	for _, key := range s.Keys() {
		switch key {
		case hubproto.KeyReviewProviders:
			if a, ok := c.applyPolicyReview(s.ReviewProviders); ok {
				out = append(out, a)
			}
		case hubproto.KeyBudgetDaily:
			out = append(out, AppliedKey{Key: key, Value: strconv.FormatInt(s.BudgetDailyWeightedTokens, 10), Local: strconv.FormatInt(c.Budget.DailyTokens, 10)})
			c.Budget.DailyTokens = s.BudgetDailyWeightedTokens
		case hubproto.KeyMergeQueue:
			out = append(out, AppliedKey{Key: key, Value: strconv.FormatBool(*s.MergeQueueEnabled), Local: strconv.FormatBool(c.MergeQueue.Enabled)})
			c.MergeQueue.Enabled = *s.MergeQueueEnabled
		case hubproto.KeyMaxConcurrency:
			// A CEILING, never a raise: capacity is the member's machine.
			local := c.Defaults.GlobalCap
			eff := min(local, s.MaxConcurrency)
			out = append(out, AppliedKey{Key: key, Value: strconv.Itoa(s.MaxConcurrency), Local: strconv.Itoa(local)})
			c.Defaults.GlobalCap = eff
		case hubproto.KeyAcceptKinds:
			kinds := make([]string, len(s.AcceptKinds))
			for i, k := range s.AcceptKinds {
				kinds[i] = string(k)
			}
			local := "all kinds"
			if len(c.Hub.AcceptKinds) > 0 {
				local = strings.Join(c.Hub.AcceptKinds, ", ")
			}
			out = append(out, AppliedKey{Key: key, Value: strings.Join(kinds, ", "), Local: local})
			c.Hub.AcceptKinds = kinds
		case hubproto.KeyEscalateAfter:
			// Read by the hub only: there is no local field to lock. Shown in the
			// policy summary, deliberately not reported as applied here.
		default:
			c.notices = append(c.notices, fmt.Sprintf("hub policy: key %q has no local setting in this lola version; ignored", key))
		}
	}
	return out
}

// applyPolicyReview enables EXACTLY the listed kinds. Each listed kind keeps
// the member's own provider entry (or that kind's defaults when there is
// none); every other kind is disabled. A kind the local config cannot run (a
// bot-watch with no author) stays off and is reported. Fallbacks to a kind
// that ends up disabled are dropped, so the chain never names a kind that is
// off. The legacy [review]/[coderabbit] tables are folded into the catalog
// first (as MigrateLegacyReview does) — the overlay never reaches the file.
func (c *Config) applyPolicyReview(kinds []string) (AppliedKey, bool) {
	cat := c.EffectiveReviewProviders()
	var localOn []string
	for _, p := range cat {
		if p.Enabled {
			localOn = append(localOn, string(p.Provider))
		}
	}
	want := map[provKind]bool{}
	for _, k := range kinds {
		if !provKind(k).valid() {
			c.notices = append(c.notices, fmt.Sprintf("hub policy: review provider kind %q is unknown to this lola version; ignored", k))
			continue
		}
		want[provKind(k)] = true
	}
	present := map[provKind]bool{}
	for i := range cat {
		present[cat[i].Provider] = true
		cat[i].Enabled = want[cat[i].Provider]
	}
	for _, k := range kinds {
		pk := provKind(k)
		if !want[pk] || present[pk] {
			continue
		}
		p, ok := NewReviewProvider(k)
		if !ok {
			continue
		}
		p.Enabled = true
		cat = append(cat, p)
		present[pk] = true
	}
	var on []string
	for i := range cat {
		p := &cat[i]
		if !p.Enabled {
			continue
		}
		if ReviewKindRequiresAuthor(string(p.Provider)) && strings.TrimSpace(p.Author) == "" {
			p.Enabled = false
			c.notices = append(c.notices, fmt.Sprintf("hub policy: review provider %q is locked on, but has no author in [[review.provider]]; it stays off", p.Provider))
			continue
		}
		if ReviewKindRequiresCommand(string(p.Provider)) && strings.TrimSpace(p.Command) == "" {
			p.Enabled = false
			c.notices = append(c.notices, fmt.Sprintf("hub policy: review provider %q is locked on, but has no command; it stays off", p.Provider))
			continue
		}
		on = append(on, string(p.Provider))
	}
	enabled := map[provKind]bool{}
	for _, p := range cat {
		if p.Enabled {
			enabled[p.Provider] = true
		}
	}
	for i := range cat {
		cat[i].Fallback = slices.DeleteFunc(slices.Clone(cat[i].Fallback), func(k provKind) bool { return !enabled[k] })
	}
	c.ReviewProviders = cat
	c.Review = ReviewConfig{}
	c.CodeRabbit = CodeRabbitConfig{}
	local := "none"
	if len(localOn) > 0 {
		local = strings.Join(localOn, ", ")
	}
	value := strings.Join(kinds, ", ")
	if len(on) != len(kinds) {
		value += " (running: " + orNone(on) + ")"
	}
	return AppliedKey{Key: hubproto.KeyReviewProviders, Value: value, Local: local}, true
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

// TeamPolicyPreview reports what ApplyTeamPolicy would enforce on c, and the
// notices it would raise, WITHOUT changing c (it works on a shallow copy:
// every field the overlay writes is a value or is reassigned, never mutated
// in place). For `lola doctor`, which reads the file's config.
func TeamPolicyPreview(c *Config, s hubproto.Settings) (applied []AppliedKey, notices []string) {
	cp := *c
	cp.notices = nil
	applied = ApplyTeamPolicy(&cp, s)
	return applied, cp.notices
}
