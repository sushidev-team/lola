package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
)

const policyHubURL = "https://hub.example.com"

type policyFixture struct {
	d       *Daemon
	path    string
	fileSrc []byte
	keyID   string
	priv    []byte
}

// newPolicyDaemon saves a config (hub on, one trusted admin key) to
// <home>/config.toml, a joined identity for org acme, and a stored policy
// signed by signer (the trusted admin unless untrusted is set).
func newPolicyDaemon(t *testing.T, s hubproto.Settings, version int, untrusted bool, mutate func(*config.Config)) policyFixture {
	t.Helper()
	pub, priv, err := hubproto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	const keyID = "k0123456789abcdef"
	cfg := testConfig(labelPoll("p1"))
	cfg.Defaults.GlobalCap = 6
	cfg.Budget.DailyTokens = 1000
	cfg.Hub = config.HubConfig{Enabled: true, URL: policyHubURL, TrustedPolicyKeys: config.TrustedKeyMap{keyID: hubproto.EncodePublicKey(pub)}}
	if mutate != nil {
		mutate(cfg)
	}
	d := newTestDaemon(t, cfg, &linear.Fake{}, &fakeNative{})
	t.Cleanup(d.stopAllWorkers)
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile(path)
	dir := d.hubDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := hub.SaveIdentity(dir, hub.Identity{URL: policyHubURL, DeviceID: hubproto.DeviceID(pub), Org: "acme"}); err != nil {
		t.Fatal(err)
	}
	signKey := priv
	if untrusted {
		_, signKey, _ = hubproto.GenerateKey()
	}
	writeStoredPolicy(t, dir, signKey, keyID, version, s)
	return policyFixture{d: d, path: path, fileSrc: src, keyID: keyID, priv: priv}
}

func writeStoredPolicy(t *testing.T, dir string, priv []byte, keyID string, version int, s hubproto.Settings) {
	t.Helper()
	p, err := hubproto.SignPolicy(priv, keyID, hubproto.Policy{Org: "acme", Version: version, IssuedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), Settings: s})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(hub.StoredPolicy{Policy: p, VerifiedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, hub.PolicyFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f policyFixture) assertFileUntouched(t *testing.T) {
	t.Helper()
	got, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, f.fileSrc) {
		t.Errorf("config.toml changed by the overlay:\n%s", got)
	}
}

func TestTeamPolicyAppliedOnReload(t *testing.T) {
	f := newPolicyDaemon(t, hubproto.Settings{BudgetDailyWeightedTokens: 500, MaxConcurrency: 2, EscalateAfter: 3}, 1, false, nil)
	if err := f.d.handleReload(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := f.d
	if d.cfg.Budget.DailyTokens != 500 || d.cfg.Defaults.GlobalCap != 2 {
		t.Errorf("live config not overlaid: budget %d cap %d", d.cfg.Budget.DailyTokens, d.cfg.Defaults.GlobalCap)
	}
	if !d.teamPolicy.applied || d.teamPolicy.version != 1 {
		t.Errorf("teamPolicy = %+v", d.teamPolicy)
	}
	f.assertFileUntouched(t)

	st := d.hubStatusData()
	if st.Policy == nil || !st.Policy.Applied {
		t.Fatalf("status policy = %+v", st.Policy)
	}
	want := []protocol.HubAppliedKey{
		{Key: hubproto.KeyBudgetDaily, Value: "500", Local: "1000"},
		{Key: hubproto.KeyMaxConcurrency, Value: "2", Local: "6"},
	}
	if len(st.Policy.AppliedKeys) != len(want) {
		t.Fatalf("applied keys = %+v", st.Policy.AppliedKeys)
	}
	for i := range want {
		if st.Policy.AppliedKeys[i] != want[i] {
			t.Errorf("applied[%d] = %+v, want %+v", i, st.Policy.AppliedKeys[i], want[i])
		}
	}

	// A daemon path that writes config.toml edits the FILE: enabling a poll
	// leaves the locked values out of it.
	if err := d.handleEnable(context.Background(), "p1", false); err != nil {
		t.Fatal(err)
	}
	fc, err := config.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if fc.Budget.DailyTokens != 1000 || fc.Defaults.GlobalCap != 6 || fc.PollByName("p1").Enabled {
		t.Errorf("file after enable: budget %d cap %d enabled %v", fc.Budget.DailyTokens, fc.Defaults.GlobalCap, fc.PollByName("p1").Enabled)
	}
	if d.cfg.PollByName("p1").Enabled || d.cfg.Budget.DailyTokens != 500 {
		t.Error("live config lost the enable flip or the overlay")
	}
}

func TestTeamPolicyUntrustedNotApplied(t *testing.T) {
	f := newPolicyDaemon(t, hubproto.Settings{BudgetDailyWeightedTokens: 500}, 1, true, nil)
	if err := f.d.handleReload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.d.cfg.Budget.DailyTokens != 1000 || f.d.teamPolicy.applied {
		t.Errorf("a policy signed by an unpinned key was applied: %+v", f.d.teamPolicy)
	}
	if f.d.cfg.TeamPolicyApplied() {
		t.Error("config marked overlaid")
	}
}

func TestTeamPolicyApplyPolicyFalse(t *testing.T) {
	off := false
	f := newPolicyDaemon(t, hubproto.Settings{BudgetDailyWeightedTokens: 500}, 4, false, func(c *config.Config) { c.Hub.ApplyPolicy = &off })
	if err := f.d.handleReload(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := f.d
	if d.cfg.Budget.DailyTokens != 1000 || d.teamPolicy.applied {
		t.Errorf("apply_policy = false still applied: %+v", d.teamPolicy)
	}
	if d.teamPolicy.version != 4 {
		t.Errorf("version not recorded (a refetch would reload forever): %+v", d.teamPolicy)
	}
	st := d.hubStatusData()
	if st.Policy == nil || st.Policy.Applied || st.Policy.ApplyNote != "[hub].apply_policy = false" || len(st.Policy.AppliedKeys) != 0 {
		t.Errorf("status policy = %+v", st.Policy)
	}
}

func TestTeamPolicyHubDisabledNotApplied(t *testing.T) {
	f := newPolicyDaemon(t, hubproto.Settings{BudgetDailyWeightedTokens: 500}, 1, false, func(c *config.Config) { c.Hub.Enabled = false })
	if err := f.d.handleReload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.d.cfg.Budget.DailyTokens != 1000 {
		t.Error("policy applied with the hub disabled")
	}
}

// A newer stored policy triggers ONE reload; the same version, or an older
// one, does not.
func TestTeamPolicyReloadOnNewVersion(t *testing.T) {
	f := newPolicyDaemon(t, hubproto.Settings{BudgetDailyWeightedTokens: 500}, 1, false, nil)
	d := f.d
	if err := d.handleReload(context.Background()); err != nil {
		t.Fatal(err)
	}
	reloads := 0
	d.hub.reload = func(ctx context.Context) error { reloads++; return d.handleReload(ctx) }

	d.hubPolicyReload(context.Background(), 1)
	if reloads != 0 {
		t.Fatalf("same version reloaded")
	}
	writeStoredPolicy(t, d.hubDir(), f.priv, f.keyID, 2, hubproto.Settings{BudgetDailyWeightedTokens: 250})
	d.hubPolicyReload(context.Background(), 2)
	d.hubPolicyReload(context.Background(), 2)
	if reloads != 1 {
		t.Fatalf("reloads = %d, want 1", reloads)
	}
	if d.cfg.Budget.DailyTokens != 250 || d.teamPolicy.version != 2 {
		t.Errorf("v2 not live: budget %d, %+v", d.cfg.Budget.DailyTokens, d.teamPolicy)
	}
	f.assertFileUntouched(t)

	// The fetch path calls it: a fetched v3 asks for a reload.
	fh := &fakeHub{}
	d.hubPolicy(context.Background(), fh, hubSnapshot{h: d.cfg.Hub}, hub.Identity{Org: "acme"})
	if fh.snapshot().policies != 1 || reloads != 2 {
		t.Errorf("fetch path: policies %d reloads %d", fh.snapshot().policies, reloads)
	}
}

// cmd=hubSync makes the next cycle fetch the policy at once.
func TestHubSyncMakesPolicyDue(t *testing.T) {
	f := newPolicyDaemon(t, hubproto.Settings{BudgetDailyWeightedTokens: 500}, 1, false, nil)
	d := f.d
	d.hub.reload = func(context.Context) error { return nil }
	fh := &fakeHub{}
	snap := hubSnapshot{h: d.cfg.Hub}
	d.hubPolicy(context.Background(), fh, snap, hub.Identity{Org: "acme"})
	d.hubPolicy(context.Background(), fh, snap, hub.Identity{Org: "acme"})
	if n := fh.snapshot().policies; n != 1 {
		t.Fatalf("policies = %d, want 1 (rate-limited)", n)
	}
	d.handleHub(context.Background(), protocol.Request{Cmd: protocol.CmdHubSync})
	d.hubPolicy(context.Background(), fh, snap, hub.Identity{Org: "acme"})
	if n := fh.snapshot().policies; n != 2 {
		t.Errorf("policies after hubSync = %d, want 2", n)
	}
}
