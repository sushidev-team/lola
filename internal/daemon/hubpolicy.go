package daemon

import (
	"context"
	"errors"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/protocol"
)

// The team policy LAYER (Phase 2). Every config the daemon puts live — at
// startup and on every reload — first passes through overlayTeamPolicy, which
// overlays the LOCKED keys of the stored, RE-VERIFIED policy onto it
// (config.ApplyTeamPolicy). The overlay lives only in d.cfg: config.Save
// refuses an overlaid Config, and every daemon path that writes config.toml
// re-Loads the file first. A newer verified policy is put live by a reload
// (hubPolicy below), so there is exactly one place a policy takes effect.

// teamPolicyState is what the live config's overlay enforced. Guarded by d.mu.
type teamPolicyState struct {
	// version is the stored policy's version (0 = none). It is recorded even
	// when the policy is not applied, so a refetch of the same version does not
	// reload again.
	version int
	// applied: the overlay ran on the live config.
	applied bool
	// keys are what it enforced (team value + local value).
	keys []config.AppliedKey
	// note says why a stored policy is not applied ("" when it is, or none).
	note string
}

// overlayTeamPolicy applies the stored policy to cfg, a freshly loaded config
// that is about to go live. It applies only when the hub is enabled, a joined
// identity exists for [hub].url, the policy re-verifies against the CURRENT
// [hub].trusted_policy_keys and names the identity's org, and
// [hub].apply_policy is not false. Any unknown applies nothing.
func overlayTeamPolicy(cfg *config.Config, stateDir string) teamPolicyState {
	sp, apply, note := hub.ApplicablePolicy(cfg.Hub, stateDir)
	out := teamPolicyState{note: note}
	if sp == nil {
		return out
	}
	out.version = sp.Policy.Version
	if !apply {
		return out
	}
	out.applied = true
	out.keys = config.ApplyTeamPolicy(cfg, sp.Policy.Settings)
	if out.keys == nil {
		out.keys = []config.AppliedKey{}
	}
	return out
}

// policyInfoLocked fills the applied half of a status policy block. The
// caller holds d.mu.
func (d *Daemon) policyInfoLocked(pi *protocol.HubPolicyInfo) {
	tp := d.teamPolicy
	if tp.version != pi.Version {
		// The live config predates this policy: the reload that applies it is
		// on its way (or the policy cannot apply; then note says why).
		pi.ApplyNote = "applies on the next reload"
		return
	}
	pi.Applied = tp.applied
	pi.ApplyNote = tp.note
	for _, k := range tp.keys {
		pi.AppliedKeys = append(pi.AppliedKeys, protocol.HubAppliedKey{Key: k.Key, Value: k.Value, Local: k.Local})
	}
}

// hubPolicyReload puts a newly stored policy live: a reload re-reads the file
// and overlays the new version. At most one reload per version, so a policy
// that cannot apply (note set) does not reload every fetch.
func (d *Daemon) hubPolicyReload(ctx context.Context, version int) {
	d.mu.Lock()
	cur := d.teamPolicy.version
	d.mu.Unlock()
	w := d.hub
	w.mu.Lock()
	due := version > cur && version != w.policyReloadedV
	if due {
		w.policyReloadedV = version
	}
	w.mu.Unlock()
	if !due {
		return
	}
	reload := w.reload
	if reload == nil {
		reload = d.handleReload
	}
	if err := reload(ctx); err != nil {
		d.logf("", "hub: policy v%d stored, but the reload that applies it failed: %v", version, err)
		return
	}
	d.logf("", "hub: team policy v%d verified and applied on reload", version)
}

// hubForceReload puts a change to the stored policy or identity live: the
// reload re-runs overlayTeamPolicy on the file's config, which no longer finds
// an applicable policy. Unlike hubPolicyReload it is not keyed on a newer
// version — dropping the overlay never raises one.
func (d *Daemon) hubForceReload(ctx context.Context, why string) {
	w := d.hub
	w.mu.Lock()
	// A later fetch of the same version must be able to reload again.
	w.policyReloadedV = 0
	reload := w.reload
	w.mu.Unlock()
	if reload == nil {
		reload = d.handleReload
	}
	if err := reload(ctx); err != nil {
		d.logf("", "hub: %s, but the reload that drops the team policy failed: %v", why, err)
		return
	}
	d.logf("", "hub: %s; the team policy no longer applies", why)
}

// hubPolicyWithdrawn handles a hub that answers no_policy: the org removed
// its policy. The stored bundle is marked withdrawn (it stays the rollback
// floor) and a reload drops the overlay. A hub can only WITHHOLD a policy
// this way, never forge or roll one back: what applies is still only a
// bundle signed by a pinned key. Nothing happens when nothing was applied.
func (d *Daemon) hubPolicyWithdrawn(ctx context.Context, h config.HubConfig) {
	trusted, err := hub.TrustedKeys(h)
	if err != nil {
		return
	}
	changed, err := hub.WithdrawPolicy(d.hubDir(), trusted, d.hub.now())
	if err != nil {
		d.logf("", "hub: mark the team policy withdrawn: %v", err)
	}
	if !changed {
		return
	}
	w := d.hub
	w.mu.Lock()
	w.policy, w.policyV, w.policyRead = nil, 0, false
	w.mu.Unlock()
	d.hubForceReload(ctx, "the hub has no team policy any more")
}

// hubRevoked reports whether err is the hub refusing this device's
// signature: HTTP 401 with hubproto.CodeUnauthorized on a signed call (an
// admin revoked the device, or the hub forgot it). Skew and replay have
// their own codes and are retried, never read as a revocation.
func hubRevoked(err error) bool {
	var ae *hub.APIError
	return errors.As(err, &ae) && ae.Status == 401 && ae.Code == hubproto.CodeUnauthorized
}

// errHubRevoked is status's fixed text for a revoked device, from the
// revocation itself and from every later cycle that finds no identity.
var errHubRevoked = errors.New("revoked: the hub no longer accepts this device; ask an admin for a new token and run `lola hub join`")

// hubRevokedIf handles a signed call's error: when the hub refused this
// device (hubRevoked), the identity is replaced by a revocation record
// (hub.MarkRevoked: the device key stays), the loop forgets who it was, the
// team policy overlay is dropped by a reload, and the status says "revoked"
// until a new join. It reports whether it did so.
func (d *Daemon) hubRevokedIf(ctx context.Context, err error) bool {
	if !hubRevoked(err) {
		return false
	}
	r, merr := hub.MarkRevoked(d.hubDir(), d.hub.now())
	if merr != nil {
		if !errors.Is(merr, hub.ErrNotJoined) {
			d.logf("", "hub: record the revocation: %v", merr)
		}
		return true
	}
	d.hubResetIdentity()
	d.hubSetErr("identity", errHubRevoked)
	d.logf("", "hub: %s (org %s) refused this device's signature; identity dropped, device key kept", r.URL, r.Org)
	d.hubForceReload(ctx, "this device was revoked by the hub")
	return true
}
