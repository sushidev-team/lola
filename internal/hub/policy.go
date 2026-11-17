package hub

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
)

// The team policy: the daemon verifies it against the admin keys pinned in
// [hub].trusted_policy_keys and stores the verified bundle. ApplicablePolicy
// decides whether it is ENFORCED; the overlay itself is config.ApplyTeamPolicy,
// applied by the daemon to its in-memory config only (never to the file).

var (
	// ErrNoTrustedKeys: [hub].trusted_policy_keys is empty, so no policy can
	// be verified (and none is fetched).
	ErrNoTrustedKeys = errors.New("hub: no trusted policy keys configured")
	// ErrNoPolicy: no verified policy is stored.
	ErrNoPolicy = errors.New("hub: no verified policy stored")
	// ErrPolicyRollback: the hub offered a version older than the stored one.
	ErrPolicyRollback = errors.New("hub: policy version is older than the stored one")
	// ErrPolicyConflict: same version as the stored one, different content.
	ErrPolicyConflict = errors.New("hub: policy differs from the stored one at the same version")
	// ErrPolicyOrg: the policy names another org than this device joined.
	ErrPolicyOrg = errors.New("hub: policy belongs to another org")
	// ErrPolicyWithdrawn: the stored policy was withdrawn (the hub answered
	// no_policy); it is kept only as the rollback floor and enforces nothing.
	ErrPolicyWithdrawn = errors.New("hub: the team removed its policy")
)

// StoredPolicy is state/hub-policy.json: the verified bundle, signature
// included, so every read re-verifies it against the CURRENT pinned keys (a
// key removed from config stops vouching for what it signed).
//
// WithdrawnAt is set when the hub later answered that the org has no policy:
// the bundle then enforces nothing (LoadPolicy refuses it with
// ErrPolicyWithdrawn), but it stays on disk as the ROLLBACK FLOOR, so a hub
// that withdraws v5 cannot then serve an older signed v1 as if it were new.
type StoredPolicy struct {
	Policy      hubproto.Policy `json:"policy"`
	VerifiedAt  time.Time       `json:"verified_at"`
	WithdrawnAt time.Time       `json:"withdrawn_at,omitzero"`
}

// TrustedKeys parses [hub].trusted_policy_keys.
func TrustedKeys(h config.HubConfig) (map[string]ed25519.PublicKey, error) {
	if len(h.TrustedPolicyKeys) == 0 {
		return nil, ErrNoTrustedKeys
	}
	return hubproto.ParseTrustedKeys(h.TrustedKeyEntries())
}

// LoadPolicy reads and RE-VERIFIES the stored policy. A withdrawn one is
// refused with ErrPolicyWithdrawn: nothing may enforce or show it as live.
func LoadPolicy(dir string, trusted map[string]ed25519.PublicKey) (StoredPolicy, error) {
	sp, err := loadStoredPolicy(dir, trusted)
	if err == nil && !sp.WithdrawnAt.IsZero() {
		return StoredPolicy{}, ErrPolicyWithdrawn
	}
	return sp, err
}

// loadStoredPolicy is LoadPolicy without the withdrawn check: the rollback
// guard needs the floor a withdrawn bundle keeps.
func loadStoredPolicy(dir string, trusted map[string]ed25519.PublicKey) (StoredPolicy, error) {
	data, err := os.ReadFile(filepath.Join(dir, PolicyFile))
	if errors.Is(err, fs.ErrNotExist) {
		return StoredPolicy{}, ErrNoPolicy
	}
	if err != nil {
		return StoredPolicy{}, err
	}
	var sp StoredPolicy
	if err := json.Unmarshal(data, &sp); err != nil {
		return StoredPolicy{}, fmt.Errorf("hub: parse %s: %w", PolicyFile, err)
	}
	if err := hubproto.VerifyPolicy(sp.Policy, trusted); err != nil {
		return StoredPolicy{}, fmt.Errorf("hub: stored policy no longer verifies: %w", err)
	}
	return sp, nil
}

// FetchPolicy fetches the org's policy, verifies it against the pinned keys,
// refuses another org's bundle and any rollback, and stores it. It returns the
// policy now held — the stored one unchanged when the hub sent the same
// version again. A hub with no policy yields an APIError with
// hubproto.CodeNoPolicy, which callers treat as "nothing to show".
func FetchPolicy(ctx context.Context, c *Client, h config.HubConfig, org, dir string, now time.Time) (StoredPolicy, error) {
	trusted, err := TrustedKeys(h)
	if err != nil {
		return StoredPolicy{}, err
	}
	p, err := c.Policy(ctx)
	if err != nil {
		return StoredPolicy{}, err
	}
	if err := hubproto.VerifyPolicy(p, trusted); err != nil {
		return StoredPolicy{}, err
	}
	if p.Org != org {
		return StoredPolicy{}, ErrPolicyOrg
	}
	prev, err := loadStoredPolicy(dir, trusted)
	switch {
	case err == nil:
		withdrawn := !prev.WithdrawnAt.IsZero()
		if p.Version < prev.Policy.Version {
			if withdrawn {
				return StoredPolicy{}, ErrPolicyRollback
			}
			return prev, ErrPolicyRollback
		}
		if p.Version == prev.Policy.Version {
			if !samePolicy(p, prev.Policy) {
				if withdrawn {
					return StoredPolicy{}, ErrPolicyConflict
				}
				return prev, ErrPolicyConflict
			}
			if !withdrawn {
				return prev, nil
			}
			// The hub serves the withdrawn version again: it is live once
			// more, re-stored below without the mark.
		}
	case errors.Is(err, ErrNoPolicy):
	default:
		// An unreadable or no-longer-verifying stored bundle is replaced by a
		// freshly verified one; the rollback guard then starts over from it.
	}
	sp := StoredPolicy{Policy: p, VerifiedAt: now.UTC()}
	data, err := json.MarshalIndent(sp, "", "  ")
	if err != nil {
		return StoredPolicy{}, err
	}
	if err := writeAtomic(dir, PolicyFile, append(data, '\n')); err != nil {
		return StoredPolicy{}, err
	}
	return sp, nil
}

// WithdrawPolicy marks the stored policy withdrawn (the hub answered that the
// org has no policy). It reports whether anything changed: false when there
// is no stored policy or it is already withdrawn. A stored bundle that no
// longer verifies is removed instead: it vouches for nothing, not even a
// floor.
func WithdrawPolicy(dir string, trusted map[string]ed25519.PublicKey, now time.Time) (bool, error) {
	sp, err := loadStoredPolicy(dir, trusted)
	switch {
	case errors.Is(err, ErrNoPolicy):
		return false, nil
	case err != nil:
		return true, removeIfExists(filepath.Join(dir, PolicyFile))
	case !sp.WithdrawnAt.IsZero():
		return false, nil
	}
	sp.WithdrawnAt = now.UTC()
	data, err := json.MarshalIndent(sp, "", "  ")
	if err != nil {
		return false, err
	}
	return true, writeAtomic(dir, PolicyFile, append(data, '\n'))
}

func samePolicy(a, b hubproto.Policy) bool {
	ab, err1 := hubproto.CanonicalPolicyBytes(a)
	bb, err2 := hubproto.CanonicalPolicyBytes(b)
	return err1 == nil && err2 == nil && bytes.Equal(ab, bb)
}

// PolicySummary renders a verified policy as status lines. Every value comes
// from a verified bundle whose settings passed hubproto's closed validation.
func PolicySummary(p hubproto.Policy) []string {
	lines := []string{
		fmt.Sprintf("org %s, version %d, issued %s, signed by %s", p.Org, p.Version, p.IssuedAt.UTC().Format(time.RFC3339), p.KeyID),
	}
	s := p.Settings
	if len(s.ReviewProviders) > 0 {
		lines = append(lines, "review providers: "+strings.Join(s.ReviewProviders, ", "))
	}
	if s.BudgetDailyWeightedTokens > 0 {
		lines = append(lines, fmt.Sprintf("daily budget: %d weighted tokens", s.BudgetDailyWeightedTokens))
	}
	if s.MergeQueueEnabled != nil {
		lines = append(lines, fmt.Sprintf("merge queue enabled: %v", *s.MergeQueueEnabled))
	}
	if s.MaxConcurrency > 0 {
		lines = append(lines, fmt.Sprintf("max concurrency: %d", s.MaxConcurrency))
	}
	if len(s.AcceptKinds) > 0 {
		ks := make([]string, len(s.AcceptKinds))
		for i, k := range s.AcceptKinds {
			ks[i] = string(k)
		}
		lines = append(lines, "accept kinds: "+strings.Join(ks, ", "))
	}
	if s.EscalateAfter > 0 {
		lines = append(lines, fmt.Sprintf("escalate after: %d failures", s.EscalateAfter))
	}
	return lines
}

// ApplicablePolicy decides whether the stored policy in dir is ENFORCED for h
// (Phase 2: every key it carries is locked, config.ApplyTeamPolicy). It
// applies only when the hub is enabled, the policy re-verifies against the
// CURRENT [hub].trusted_policy_keys, a joined identity exists for [hub].url
// and names the policy's org, and [hub].apply_policy is not false. sp is the
// verified policy (nil when none is stored or it no longer verifies); note
// says why it does not apply ("" when it does, or when there is none). Every
// unknown applies nothing.
func ApplicablePolicy(h config.HubConfig, dir string) (sp *StoredPolicy, apply bool, note string) {
	if !h.Enabled || len(h.TrustedPolicyKeys) == 0 {
		return nil, false, ""
	}
	trusted, err := TrustedKeys(h)
	if err != nil {
		return nil, false, "trusted policy keys do not parse"
	}
	if r, ok := LoadRevocation(dir); ok && strings.TrimSuffix(r.URL, "/") == strings.TrimSuffix(h.URL, "/") {
		return nil, false, "this device was revoked by the hub"
	}
	got, err := LoadPolicy(dir, trusted)
	if errors.Is(err, ErrNoPolicy) {
		return nil, false, ""
	}
	if errors.Is(err, ErrPolicyWithdrawn) {
		return nil, false, "the team removed its policy"
	}
	if err != nil {
		return nil, false, "the stored policy no longer verifies"
	}
	id, err := LoadIdentity(dir)
	switch {
	case err != nil:
		return &got, false, "not joined"
	case strings.TrimSuffix(id.URL, "/") != strings.TrimSuffix(h.URL, "/"):
		return &got, false, "joined identity is for another hub url"
	case id.Org != got.Policy.Org:
		return &got, false, "the policy belongs to another org"
	case !h.PolicyApplies():
		return &got, false, "[hub].apply_policy = false"
	}
	return &got, true, ""
}
