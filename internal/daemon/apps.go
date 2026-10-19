package daemon

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitguard"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// STACK SESSIONS (internal/session/stack.go): one agent, one PR per app. The
// observer fetches each app's PR on its own branch and derives that app's
// delivery axis from it; the session's own axis is their aggregate. Every
// path that ACTS on a PR — reactions, review passes, the merge queue — then
// runs once per app on its LANE (session.LaneID), so each PR gets its own
// guards, its own review and its own reaction, while the agent they all type
// into, and its idle gate, stay one.

// appFact is one cycle's answer for one app of a stack session.
type appFact struct {
	name string
	// fetched: a PR lookup was attempted; known: it answered (pr may be nil).
	fetched, known bool
	pr             *scm.PR
	// touchedKnown: the local changed-files read answered.
	touchedKnown, touched bool
}

// observeApps asks gh for each app's PR on its branch, and — for an app with
// no PR — local git whether it has changes at all. Bounded per call like
// every observer exec; a failed fetch is logged and leaves the last known
// facts standing.
func (d *Daemon) observeApps(ctx context.Context, s session.Session) []appFact {
	var out []appFact
	for _, a := range s.Apps {
		f := appFact{name: a.Name}
		pr := a.PR
		if a.Repo != "" && a.Branch != "" {
			f.fetched = true
			cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
			p, err := d.prForBranch(cctx, a.Repo, a.Branch)
			cancel()
			if err != nil {
				d.logf("", "observe: PR check for %s app %s (branch %s in %s) failed: %v", s.ID, a.Name, a.Branch, a.Repo, err)
			} else {
				f.known, f.pr, pr = true, p, p
			}
		}
		if pr == nil && d.changedFiles != nil && a.Worktree != "" {
			cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
			files, err := d.changedFiles(cctx, a.Worktree, appBase(a))
			cancel()
			if err == nil {
				f.touchedKnown, f.touched = true, len(files) > 0
			}
		}
		out = append(out, f)
	}
	return out
}

// appBase is the branch an app's PR targets.
func appBase(a session.App) string {
	if a.Base != "" {
		return a.Base
	}
	return config.DefaultBranchName
}

// applyApps merges a cycle's app facts into cur (under the store lock) and
// re-derives the session's aggregate, reporting whether anything changed.
// The slice is replaced, never mutated in place: snapshots handed out earlier
// share its backing array.
func applyApps(cur *session.Session, facts []appFact, now time.Time) bool {
	next := slices.Clone(cur.Apps)
	changed := false
	for _, f := range facts {
		i := slices.IndexFunc(next, func(a session.App) bool { return a.Name == f.name })
		if i < 0 {
			continue
		}
		a := &next[i]
		if f.known {
			if !reflect.DeepEqual(a.PR, f.pr) {
				changed = true
			}
			a.PR, a.PRObservedAt, a.PRFetchFailures = f.pr, now, 0
			prev := a.Delivery
			if prev == "" {
				prev = state.DeliveryNone
			}
			if nd := state.DeriveDelivery(f.pr, prev); nd != a.Delivery {
				a.Delivery, a.DeliverySince = nd, now
				changed = true
			}
		} else if f.fetched {
			a.PRFetchFailures++
		}
		if f.touchedKnown && a.Touched != f.touched {
			a.Touched = f.touched
			changed = true
		}
	}
	cur.Apps = next
	before := cur.Delivery
	cur.Reaggregate(now)
	return changed || cur.Delivery != before
}

// appsInfo flattens a stack session's apps for the wire.
func (d *Daemon) appsInfo(cfg *config.Config, s session.Session) []protocol.SessionApp {
	if len(s.Apps) == 0 {
		return nil
	}
	out := make([]protocol.SessionApp, 0, len(s.Apps))
	for _, a := range s.Apps {
		ai := protocol.SessionApp{Name: a.Name, Repo: a.Repo, Branch: a.Branch, Worktree: a.Worktree, Delivery: string(a.Delivery), Touched: a.Touched && a.PR == nil}
		if cfg != nil {
			if m := cfg.ProjectByName(a.Name); m != nil {
				ai.Label = m.DisplayName()
			}
		}
		if a.PR != nil {
			ai.Number, ai.URL, ai.State, ai.Checks, ai.Review, ai.Draft = a.PR.Number, a.PR.URL, a.PR.State, a.PR.ChecksState, a.PR.ReviewDecision, a.PR.IsDraft
		}
		if d.ports != nil {
			if l, ok := d.ports.Get(session.LaneID(s.ID, a.Name)); ok {
				ai.Ports = l.Ports
			}
		}
		out = append(out, ai)
	}
	return out
}

// syncGitPins registers the trusted git layout of every SANDBOXED worktree in
// the store with internal/gitguard, so host git on it never follows metadata
// the agent can rewrite. A sandboxed worktree whose record carries no pin (a
// record rebuilt by adoption after the store lost it) is DENIED instead:
// checkpoints, diffs and dirty checks on it then fail, and teardown keeps it,
// rather than running git against a layout nobody vouched for. A stack
// session's folder is not a repository; its apps are pinned one by one.
func (d *Daemon) syncGitPins() {
	d.mu.Lock()
	cfg := d.cfg
	home := d.home
	d.mu.Unlock()
	pin := func(wt string, p *gitguard.Pin) {
		if wt == "" {
			return
		}
		if p != nil && p.Valid() {
			gitguard.Set(wt, *p)
		} else {
			gitguard.Deny(wt)
		}
	}
	for _, s := range d.sessions.Snapshot() {
		sandboxed := s.Sandbox != ""
		if p := cfg.ProjectByName(s.Project); p != nil && p.Sandboxed() {
			sandboxed = true
		}
		if !sandboxed {
			continue
		}
		if s.Stack {
			for _, a := range s.Apps {
				pin(a.Worktree, a.GitPin)
			}
			continue
		}
		wt := s.Worktree
		if wt == "" {
			wt = filepath.Join(home, "worktrees", s.Project, s.ID)
		}
		pin(wt, s.GitPin)
	}
}

// withLanes replaces every stack session in snap by its app lanes, for the
// cross-session passes that act on PRs (the merge queue): a stack session
// itself has no PR to act on.
func withLanes(snap []session.Session) []session.Session {
	out := make([]session.Session, 0, len(snap))
	for _, s := range snap {
		if s.Stack {
			out = append(out, s.Lanes()...)
			continue
		}
		out = append(out, s)
	}
	return out
}

// devCmd is one dev tab a session runs: the command line, the directory it
// runs in, and how the clients label it.
type devCmd struct {
	cmd, dir, label string
}

// devPlan is what a session's dev toggle starts: the project's dev_commands
// in its worktree — or, for a stack session, EACH APP's own dev_commands in
// that app's worktree, one tab each, labelled "<app>: <command>". parallel
// reports MULTI-ACTIVE (every project involved leases ports), and leases are
// the port-lease keys that must exist for it: the session's, or each app
// lane's.
func devPlan(cfg *config.Config, s session.Session, dir string) (plan []devCmd, parallel bool, leases []string) {
	if cfg == nil {
		return nil, false, nil
	}
	if !s.Stack {
		p := cfg.ProjectByName(s.Project)
		if p == nil {
			return nil, false, nil
		}
		for _, c := range p.DevCommands {
			plan = append(plan, devCmd{cmd: c, dir: dir, label: c})
		}
		_, _, _, parallel = p.PortLease()
		return plan, parallel, []string{s.ID}
	}
	parallel = true
	for _, a := range s.Apps {
		m := cfg.ProjectByName(a.Name)
		if m == nil || len(m.DevCommands) == 0 || a.Worktree == "" {
			continue
		}
		for _, c := range m.DevCommands {
			plan = append(plan, devCmd{cmd: c, dir: a.Worktree, label: a.Name + ": " + c})
		}
		if _, _, _, ok := m.PortLease(); !ok {
			parallel = false
		}
		leases = append(leases, session.LaneID(s.ID, a.Name))
	}
	return plan, parallel && len(plan) > 0, leases
}

// devLabels are the dev tab labels of a session, in tab order (what the
// clients show for each "<id>-dev-N").
func devLabels(plan []devCmd) []string {
	if len(plan) == 0 {
		return nil
	}
	out := make([]string, len(plan))
	for i, c := range plan {
		out[i] = c.label
	}
	return out
}

// devPlanOf is devPlan for a stored session, its directory derived the way
// the toggle derives it.
func devPlanOf(cfg *config.Config, s session.Session, home string) []devCmd {
	if cfg == nil {
		return nil
	}
	plan, _, _ := devPlan(cfg, s, devWorktreeDir(s, s.Project, home))
	return plan
}
