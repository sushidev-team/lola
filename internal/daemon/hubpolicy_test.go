package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// Leave removes the stored policy (the reload drops the overlay to v0); a
// rejoin that fetches the SAME version must reload again, or the verified
// policy is stored but never enforced.
func TestTeamPolicyReloadsAfterLeaveAndRejoin(t *testing.T) {
	f := newPolicyDaemon(t, hubproto.Settings{BudgetDailyWeightedTokens: 500}, 1, false, nil)
	d := f.d
	reloads := 0
	d.hub.reload = func(ctx context.Context) error { reloads++; return d.handleReload(ctx) }
	d.hubPolicyReload(context.Background(), 1)
	if reloads != 1 || d.teamPolicy.version != 1 {
		t.Fatalf("first apply: reloads %d, %+v", reloads, d.teamPolicy)
	}

	// Leave: the policy file goes, the identity is reset, the daemon reloads.
	if err := os.Remove(filepath.Join(d.hubDir(), hub.PolicyFile)); err != nil {
		t.Fatal(err)
	}
	d.hubResetIdentity()
	if err := d.handleReload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d.teamPolicy.version != 0 || d.cfg.Budget.DailyTokens != 1000 {
		t.Fatalf("after leave: budget %d, %+v", d.cfg.Budget.DailyTokens, d.teamPolicy)
	}

	// Rejoin: the fetch stores v1 again and asks for its reload.
	writeStoredPolicy(t, d.hubDir(), f.priv, f.keyID, 1, hubproto.Settings{BudgetDailyWeightedTokens: 500})
	d.hubPolicyReload(context.Background(), 1)
	if reloads != 2 || d.teamPolicy.version != 1 || d.cfg.Budget.DailyTokens != 500 {
		t.Errorf("after rejoin: reloads %d budget %d, %+v", reloads, d.cfg.Budget.DailyTokens, d.teamPolicy)
	}
}

// A reload that unpins the policy's key drops status's cached bundle: status
// must not keep showing it as verified.
func TestHubStatusPolicyReReadAfterKeysChange(t *testing.T) {
	f := newPolicyDaemon(t, hubproto.Settings{BudgetDailyWeightedTokens: 500}, 1, false, nil)
	d := f.d
	if err := d.handleReload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := d.hubStatusData(); st.Policy == nil || st.Policy.Version != 1 || !st.Policy.Applied {
		t.Fatalf("status policy = %+v", st.Policy)
	}
	fc, err := config.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := hubproto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	fc.Hub.TrustedPolicyKeys = config.TrustedKeyMap{"kother": hubproto.EncodePublicKey(other)}
	if err := fc.Save(f.path); err != nil {
		t.Fatal(err)
	}
	if err := d.handleReload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d.teamPolicy.applied {
		t.Fatalf("policy still applied: %+v", d.teamPolicy)
	}
	if st := d.hubStatusData(); st.Policy != nil || st.PolicyVersion != 0 {
		t.Errorf("status still shows the unpinned policy: %+v (v%d)", st.Policy, st.PolicyVersion)
	}
}

// A hub answering no_policy drops the overlay (one reload), keeps the bundle
// as the rollback floor, and a re-served same version applies again.
func TestTeamPolicyWithdrawnDropsTheOverlay(t *testing.T) {
	f := newPolicyDaemon(t, hubproto.Settings{BudgetDailyWeightedTokens: 500}, 2, false, nil)
	d := f.d
	reloads := 0
	d.hub.reload = func(ctx context.Context) error { reloads++; return d.handleReload(ctx) }
	d.hubPolicyReload(context.Background(), 2)
	if !d.teamPolicy.applied || d.cfg.Budget.DailyTokens != 500 {
		t.Fatalf("not applied: %+v", d.teamPolicy)
	}
	d.hubPolicyWithdrawn(context.Background(), d.cfg.Hub)
	if reloads != 2 || d.teamPolicy.applied || d.cfg.Budget.DailyTokens != 1000 {
		t.Fatalf("after withdraw: reloads %d budget %d %+v", reloads, d.cfg.Budget.DailyTokens, d.teamPolicy)
	}
	if d.teamPolicy.note != "the team removed its policy" {
		t.Errorf("note = %q", d.teamPolicy.note)
	}
	if st := d.hubStatusData(); st.Policy != nil {
		t.Errorf("status still shows a withdrawn policy: %+v", st.Policy)
	}
	d.hubPolicyWithdrawn(context.Background(), d.cfg.Hub)
	if reloads != 2 {
		t.Errorf("a second no_policy reloaded again: %d", reloads)
	}
	if _, err := os.Stat(filepath.Join(d.hubDir(), hub.PolicyFile)); err != nil {
		t.Fatalf("the floor must stay on disk: %v", err)
	}
	f.assertFileUntouched(t)
}

// A 401 unauthorized on a signed call is a revocation: the identity goes,
// the overlay is dropped, the key stays; other refusals are not.
func TestHubRevokedDropsIdentityAndOverlay(t *testing.T) {
	f := newPolicyDaemon(t, hubproto.Settings{BudgetDailyWeightedTokens: 500}, 1, false, nil)
	d := f.d
	d.hub.reload = func(ctx context.Context) error { return d.handleReload(ctx) }
	if err := d.handleReload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !d.teamPolicy.applied {
		t.Fatal("not applied")
	}
	for _, err := range []error{
		errors.New("connection refused"),
		&hub.APIError{Status: 401, Code: hubproto.CodeSkew},
		&hub.APIError{Status: 403, Code: hubproto.CodeForbidden},
	} {
		if d.hubRevokedIf(context.Background(), err) {
			t.Fatalf("%v read as a revocation", err)
		}
	}
	if !d.hubRevokedIf(context.Background(), &hub.APIError{Status: 401, Code: hubproto.CodeUnauthorized}) {
		t.Fatal("401 unauthorized must read as a revocation")
	}
	if _, err := hub.LoadIdentity(d.hubDir()); !errors.Is(err, hub.ErrNotJoined) {
		t.Fatalf("identity after revoke: %v", err)
	}
	if r, ok := hub.LoadRevocation(d.hubDir()); !ok || r.Org != "acme" {
		t.Fatalf("revocation = %+v, %v", r, ok)
	}
	if d.teamPolicy.applied || d.cfg.Budget.DailyTokens != 1000 {
		t.Fatalf("overlay kept after revoke: %+v", d.teamPolicy)
	}
	if st := d.hubStatusData(); st.Joined || !strings.Contains(st.LastError, "revoked") {
		t.Fatalf("status = joined %v err %q", st.Joined, st.LastError)
	}
	f.assertFileUntouched(t)
}

// saveRevocableIdentity writes an enrolled identity (and its key) for d's
// hub, so a revocation has one to replace.
func saveRevocableIdentity(t *testing.T, d *Daemon) {
	t.Helper()
	dir := d.hubDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := hub.SaveIdentity(dir, hub.Identity{URL: d.cfg.Hub.URL, DeviceID: strings.Repeat("a", 32), Org: "acme"}); err != nil {
		t.Fatal(err)
	}
}

var errUnauthorized = &hub.APIError{Status: 401, Code: hubproto.CodeUnauthorized}

// A heartbeat the hub refuses as unauthorized is a revocation: the cycle
// claims nothing, the identity goes, and the status keeps saying "revoked"
// on the next cycles (which find no identity) instead of "not joined".
func TestHubCycleRevokedOnHeartbeat(t *testing.T) {
	d, fh, nat := hubFixture(t)
	saveRevocableIdentity(t, d)
	fh.beatErr = errUnauthorized
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	if c := fh.snapshot(); len(c.claims) != 0 || c.offerCalls != 0 || len(nat.manualAgents) != 0 {
		t.Fatalf("a revoked device acted: %+v", c)
	}
	if _, err := hub.LoadIdentity(d.hubDir()); !errors.Is(err, hub.ErrNotJoined) {
		t.Fatalf("identity kept: %v", err)
	}
	st := d.hubStatusData()
	if st.Joined || st.Membership != "revoked" || !strings.HasPrefix(st.LastError, "identity: revoked") {
		t.Fatalf("status = membership %q joined %v err %q", st.Membership, st.Joined, st.LastError)
	}
	d.hub.newClient = newRealHubClient // what the loop does with no identity
	d.hubCycle(context.Background())
	if st := d.hubStatusData(); st.Membership != "revoked" || !strings.Contains(st.LastError, "revoked") {
		t.Fatalf("next cycle = membership %q err %q", st.Membership, st.LastError)
	}
}

// Membership reads joined / not joined from the identity on disk.
func TestHubStatusMembership(t *testing.T) {
	d, _, _ := hubFixture(t)
	if m := d.hubStatusData().Membership; m != "not joined" {
		t.Fatalf("membership = %q", m)
	}
	saveRevocableIdentity(t, d)
	if m := d.hubStatusData().Membership; m != "joined" {
		t.Fatalf("membership = %q", m)
	}
}

// An unauthorized answer on any signed call is read the same way.
func TestHubRevokedOnOffersAndPolicy(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*fakeHub)
		keys bool
	}{
		{"offers", func(f *fakeHub) { f.offerErr = errUnauthorized }, false},
		{"policy", func(f *fakeHub) { f.policyErr = errUnauthorized }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, fh, _ := hubFixture(t)
			saveRevocableIdentity(t, d)
			tc.set(fh)
			if tc.keys {
				d.cfg.Hub.TrustedPolicyKeys = map[string]string{"k1": "x"}
			}
			d.hubCycle(context.Background())
			if st := d.hubStatusData(); st.Membership != "revoked" || !strings.Contains(st.LastError, "revoked") {
				t.Fatalf("status = membership %q err %q", st.Membership, st.LastError)
			}
		})
	}
}

// A revocation met while flushing the outbox is not the outcome being
// refused: the queue is kept whole and nothing more is sent.
func TestHubOutboxKeptOnRevocation(t *testing.T) {
	d, fh, _ := hubFixture(t)
	saveRevocableIdentity(t, d)
	fh.outErr = errUnauthorized
	now := time.Now()
	d.hubEnqueue(hubOutcome{ItemID: "it8", Action: hubActFail, Reason: string(hubproto.FailAgent), Since: now})
	d.hubEnqueue(hubOutcome{ItemID: "it9", Action: hubActFail, Reason: string(hubproto.FailAgent), Since: now})
	d.hubOutcomeCycle(context.Background())
	if n := len(fh.snapshot().failReqs); n != 1 {
		t.Errorf("sent %d outcomes after the revocation, want 1", n)
	}
	if st := d.hubStatusData(); st.PendingOutcomes != 2 || st.Membership != "revoked" {
		t.Fatalf("pending %d membership %q", st.PendingOutcomes, st.Membership)
	}
}

// A hub that answers no_policy during a cycle withdraws an applied policy:
// the overlay is dropped and the stored bundle stays as the floor.
func TestHubCycleNoPolicyWithdraws(t *testing.T) {
	f := newPolicyDaemon(t, hubproto.Settings{BudgetDailyWeightedTokens: 500}, 2, false, nil)
	d := f.d
	d.hub.reload = func(ctx context.Context) error { return d.handleReload(ctx) }
	if err := d.handleReload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !d.teamPolicy.applied {
		t.Fatal("not applied")
	}
	fh := &fakeHub{policyErr: &hub.APIError{Status: 404, Code: hubproto.CodeNoPolicy}}
	d.hub.newClient = func(url, _ string) (hubAPI, hub.Identity, error) {
		return fh, hub.Identity{URL: url, DeviceID: "dev", Org: "acme"}, nil
	}
	d.hubCycle(context.Background())
	if fh.snapshot().policies != 1 {
		t.Fatal("the policy was not fetched")
	}
	if d.teamPolicy.applied || d.cfg.Budget.DailyTokens != 1000 {
		t.Fatalf("overlay kept after no_policy: %+v", d.teamPolicy)
	}
	if st := d.hubStatusData(); st.LastError != "" {
		t.Errorf("no_policy is no error: %q", st.LastError)
	}
	if _, err := os.Stat(filepath.Join(d.hubDir(), hub.PolicyFile)); err != nil {
		t.Fatalf("the floor must stay on disk: %v", err)
	}
	f.assertFileUntouched(t)
}
