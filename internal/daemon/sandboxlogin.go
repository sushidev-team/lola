package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// A sandboxed claude can only log in through the credentials its env file
// carried at launch (runtime.sandboxEnv): a container cannot read the
// keychain, and a running process takes no new environment. A session spawned
// before the user set up a Claude login therefore sits at "Not logged in"
// forever. Once a login resolves, the observer relaunches such a session on
// its kept worktree with the credentials that exist now.
//
// The trigger is the env file lola itself wrote, never the pane, so nothing an
// agent prints can force a relaunch; after the relaunch the file carries the
// login and the condition is false for good.

// reloginRetry spaces relaunch attempts of one session, so a relaunch that
// keeps failing never turns into a loop.
const reloginRetry = 10 * time.Minute

type reloginState struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// due reports whether id may be relaunched now, and stamps it if so.
func (r *reloginState) due(id string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.last[id]; ok && now.Sub(t) < reloginRetry {
		return false
	}
	if r.last == nil {
		r.last = map[string]time.Time{}
	}
	r.last[id] = now
	return true
}

// sandboxReloginAPI is the optional runtime surface for the relaunch
// (runtime.Native implements it; test fakes need not).
type sandboxReloginAPI interface {
	SandboxLoginMissing(s session.Session) bool
	SandboxLoginAvailable(kind agent.Kind) bool
	Relogin(ctx context.Context, s session.Session) (session.Session, error)
}

// sandboxLoginReady reports whether a sandboxed agent of kind would get a
// login if it started now. True when the runtime cannot say (fail open: the
// gate only exists to avoid sessions that cannot work).
func (d *Daemon) sandboxLoginReady(kind agent.Kind) bool {
	d.mu.Lock()
	nat := d.native
	d.mu.Unlock()
	r, ok := nat.(sandboxReloginAPI)
	return !ok || r.SandboxLoginAvailable(kind)
}

// sandboxLoginMissing is the refusal when a sandboxed agent has no login.
// The desktop matches SandboxLoginMissingText to offer the login there.
func sandboxLoginMissing(kind agent.Kind) string {
	return "sandboxed " + kind.String() + " " + SandboxLoginMissingText
}

// SandboxLoginMissingText is the stable part of sandboxLoginMissing.
const SandboxLoginMissingText = "has no login yet. Log in under Settings → Sandbox."

// maybeRelogin relaunches s when it is a sandboxed agent session started
// without a login and one resolves now.
func (d *Daemon) maybeRelogin(ctx context.Context, s session.Session) {
	switch s.AgentState {
	case state.AgentDead, state.AgentExited, state.AgentShell, state.AgentOrphaned:
		return // not running: revive takes the fresh credentials itself
	}
	switch s.Delivery {
	case state.DeliveryMerged, state.DeliveryClosed:
		return
	}
	d.mu.Lock()
	nat := d.native
	d.mu.Unlock()
	r, ok := nat.(sandboxReloginAPI)
	if !ok || !r.SandboxLoginMissing(s) || !r.SandboxLoginAvailable(agent.Parse(s.Agent)) {
		return
	}
	if !d.relogin.due(s.ID, time.Now()) {
		return
	}
	if !d.beginConnWork() {
		return
	}
	defer d.connWg.Done()
	// Shielded like a spawn: a shutdown must not cut the relaunch in half.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), nativeSpawnTimeout)
	relaunched, err := r.Relogin(rctx, s)
	cancel()
	if err != nil {
		d.logf("", "relogin: %s: %v", s.ID, err)
		return
	}
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.Sandbox = relaunched.Sandbox
		cur.TmuxName = relaunched.TmuxName
		cur.AtPrompt = false
		cur.AtPromptVerified = false
		cur.SetAgentState(state.AgentStarting, "", time.Now())
		return true
	})
	d.reactSave()
	d.logf("", "relogin: %s relaunched with the %s login that now exists", s.ID, agent.Parse(s.Agent))
}
