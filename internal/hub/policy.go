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

// The team policy is DISPLAY-ONLY in phase 1: the daemon verifies it against
// the admin keys pinned in [hub].trusted_policy_keys, stores the verified
// bundle and shows it in `lola hub status`. Nothing here applies it to config;
// that is phase 2's third inheritance layer.

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
)

// StoredPolicy is state/hub-policy.json: the verified bundle, signature
// included, so every read re-verifies it against the CURRENT pinned keys (a
// key removed from config stops vouching for what it signed).
type StoredPolicy struct {
	Policy     hubproto.Policy `json:"policy"`
	VerifiedAt time.Time       `json:"verified_at"`
}

// TrustedKeys parses [hub].trusted_policy_keys.
func TrustedKeys(h config.HubConfig) (map[string]ed25519.PublicKey, error) {
	if len(h.TrustedPolicyKeys) == 0 {
		return nil, ErrNoTrustedKeys
	}
	return hubproto.ParseTrustedKeys(h.TrustedKeyEntries())
}

// LoadPolicy reads and RE-VERIFIES the stored policy.
func LoadPolicy(dir string, trusted map[string]ed25519.PublicKey) (StoredPolicy, error) {
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
	prev, err := LoadPolicy(dir, trusted)
	switch {
	case err == nil:
		if p.Version < prev.Policy.Version {
			return prev, ErrPolicyRollback
		}
		if p.Version == prev.Policy.Version {
			if !samePolicy(p, prev.Policy) {
				return prev, ErrPolicyConflict
			}
			return prev, nil
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
