package daemon

import (
	"context"

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
