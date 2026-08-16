package daemon

// dev.go owns the ACTIVE session: the one session of a project that runs the
// project's long-running dev processes ([[project]].dev_commands — `composer
// dev`, `npm run dev`) in its own worktree.
//
// The problem it solves is a port, not a preference. Every worktree of a project
// wants to serve on the same port, so two sessions running the dev command at
// once means the second one silently talks to the first one's checkout. Making
// it a per-project TOGGLE turns "find the other session, kill it, restart here"
// into one click: activating a session kills the tabs of whichever session held
// them and starts its own.
//
// Each command runs in its own tmux session named "<sessionID>-dev-N"
// (internal/devtab), beside the agent pane and the "-shell-N" tabs, so both
// surfaces discover it as a terminal tab with no extra wiring. The tabs are
// created with remain-on-exit: a command that dies keeps its pane and its error
// message, and the DEAD pane is what the observer reads as "no longer active"
// (observer.go's reconcileDevTabs). Nothing here persists intent — tmux is the
// single source of truth for who is active, so a closed tab, a crash and a
// daemon restart all agree without a reconciliation pass.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/devtab"
	"github.com/sushidev-team/lola/internal/devurl"
	"github.com/sushidev-team/lola/internal/lolaenv"
	"github.com/sushidev-team/lola/internal/portproc"
	"github.com/sushidev-team/lola/internal/proctree"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/tmux"
)

// devExecTimeout bounds each tmux exec a dev toggle drives. Starting a detached
// session returns as soon as tmux has forked it — the dev command itself runs
// for hours — so this only has to cover tmux's own round trip.
const devExecTimeout = 10 * time.Second

// handleDev switches a session's dev tabs on or off (cmd=dev).
//
// Activation is a MOVE, not a copy: every other session of the same project
// loses its tabs first, and only then are this session's started. The order
// matters — the commands bind ports, so starting before killing would leave the
// new tabs dead on arrival with an "address already in use" nobody reads.
func (d *Daemon) handleDev(ctx context.Context, sessionID string, on bool) (protocol.DevData, error) {
	if sessionID == "" {
		return protocol.DevData{}, errors.New("session id required")
	}
	s, ok := d.sessions.Get(sessionID)
	if !ok {
		return protocol.DevData{}, fmt.Errorf("unknown session %s", sessionID)
	}

	d.mu.Lock()
	p := d.cfg.ProjectByName(s.Project)
	home := d.home
	d.mu.Unlock()

	tm := d.devTmuxClient()
	if tm == nil || !tm.Available() {
		return protocol.DevData{}, errors.New("tmux is not available")
	}

	if !on {
		n := d.stopDevTabs(ctx, tm, s.ID)
		d.markDev(s.ID, false, 0)
		msg := fmt.Sprintf("session %s: dev processes stopped", s.ID)
		if n == 0 {
			msg = fmt.Sprintf("session %s: no dev processes were running", s.ID)
		}
		d.logf("", "dev: %s", msg)
		return protocol.DevData{Active: false, Message: msg}, nil
	}

	if p == nil {
		return protocol.DevData{}, fmt.Errorf("project %q is no longer in config", s.Project)
	}
	if len(p.DevCommands) == 0 {
		return protocol.DevData{}, fmt.Errorf("project %q configures no dev_commands", p.DisplayName())
	}
	dir := devWorktreeDir(s, p.Name, home)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return protocol.DevData{}, fmt.Errorf("session %s has no worktree at %s", s.ID, dir)
	}

	// Everything that could hold a port goes down BEFORE the first NewSession,
	// in widening order: this session's own tabs (a re-activation), the tabs of
	// whichever session held them, and finally whatever is still listening from
	// inside the project's worktrees without a pane to own it.
	d.stopDevTabs(ctx, tm, s.ID)
	stopped := d.takeOverProject(ctx, tm, s)
	freed := d.sweepPortSquatters(ctx, tm, p.Name, home)

	started := 0
	var errs []error
	for i, cmd := range p.DevCommands {
		name := devtab.Name(s.ID, i+1)
		// One tab per command: a re-activation REPLACES the previous pane
		// (including a dead one still showing the last crash) rather than
		// leaving a second session behind under the same name.
		ectx, cancel := context.WithTimeout(ctx, devExecTimeout)
		if tm.Has(ectx, name) {
			_ = tm.KillSessionTree(ectx, name)
		}
		err := tm.NewSession(ectx, name, dir, lolaenv.CommandLine(cmd))
		if err == nil {
			// Best-effort: without it the tab simply vanishes when the command
			// exits, which still reads as inactive — just without the output
			// that says why.
			if kerr := tm.KeepDeadPane(ectx, name); kerr != nil {
				d.logf("", "dev: %s could not keep the dead pane of %s: %v", s.ID, name, kerr)
			}
			started++
		} else {
			errs = append(errs, fmt.Errorf("%q: %w", cmd, err))
		}
		cancel()
	}
	d.markDev(s.ID, started > 0, started)

	if started == 0 {
		return protocol.DevData{}, fmt.Errorf("session %s: no dev process could be started: %w", s.ID, errors.Join(errs...))
	}
	msg := fmt.Sprintf("session %s: %d dev process(es) running", s.ID, started)
	if stopped != "" {
		msg += fmt.Sprintf(" (taken over from %s)", stopped)
	}
	if len(freed) > 0 {
		msg += fmt.Sprintf("; freed %s", strings.Join(freed, ", "))
	}
	if len(errs) > 0 {
		msg += fmt.Sprintf("; %d failed to start", len(errs))
		d.logf("", "dev: %s: %v", s.ID, errors.Join(errs...))
	}
	d.logf("", "dev: %s", msg)
	return protocol.DevData{Active: true, Commands: p.DevCommands, Stopped: stopped, Message: msg}, nil
}

// takeOverProject stops the dev tabs of every OTHER session of s's project and
// returns the id of the one that held them ("" when none did). Only one is
// expected, but all are swept: a daemon that crashed mid-toggle, or a project
// whose dev_commands list shrank, can leave more than one behind and a stray
// tab still holds the port.
func (d *Daemon) takeOverProject(ctx context.Context, tm devTmux, s session.Session) string {
	var stopped string
	for _, other := range d.sessions.Snapshot() {
		if other.ID == s.ID || other.Project != s.Project {
			continue
		}
		if n := d.stopDevTabs(ctx, tm, other.ID); n > 0 {
			stopped = other.ID
		}
		d.markDev(other.ID, false, 0)
	}
	return stopped
}

// squatterGrace is how long a reclaimed port's process group gets to shut down
// cleanly before it is killed. It matches internal/tmux's own kill grace: a dev
// server asked to stop closes its listening socket, one that is SIGKILLed can
// leave the port in the kernel for a moment.
const squatterGrace = 3 * time.Second

// sweepPortSquatters kills whatever is still LISTENING from inside this
// project's worktrees without a live tmux pane behind it, and returns one short
// description per port reclaimed (for the toggle's message).
//
// It exists because killing the previous holder's dev tabs is not enough. The
// coding agent working in a worktree starts servers of its own — `php artisan
// serve --port=8000` through its Bash tool, to look at the page it just changed
// — and Claude Code puts every such command in its OWN process group, so it is
// neither a tmux session nor part of the pane's tree. It outlives the agent's
// turn, keeps :8000, and the session taking over silently lands on :8001
// against a checkout that is not the one being reviewed. lola cannot find that
// process by port (the port lives inside `composer dev`, not in config), so it
// finds it by WHERE it runs.
//
// The rails, in order of how much damage each prevents:
//   - Only ~/.lola/worktrees/<project>/ is ever swept. The project's own
//     checkout is off limits — a dev server the user started there by hand is
//     theirs, not lola's to reclaim — and so is any session whose worktree was
//     configured outside lola's home.
//   - A process group that OWNS a live tmux pane is never signalled. That is
//     what keeps the sweep from killing an agent (whose cwd is its worktree),
//     a shell tab, or a dev tab that was just started. The tmux server's own
//     group is protected the same way.
//   - It FAILS CLOSED. No lsof, a `ps` that will not answer, a tmux that cannot
//     list its panes: nothing is killed. Every one of those costs the protect
//     set, and a sweep without the protect set could kill an agent mid-turn.
func (d *Daemon) sweepPortSquatters(ctx context.Context, tm devTmux, projectName, home string) []string {
	d.mu.Lock()
	listeners := d.portListeners
	d.mu.Unlock()
	if listeners == nil || projectName == "" || home == "" {
		return nil
	}
	root := filepath.Join(home, "worktrees", projectName)

	lctx, cancel := context.WithTimeout(ctx, devExecTimeout)
	found, err := listeners(lctx)
	cancel()
	if err != nil {
		d.logf("", "dev: could not check %s's worktrees for stray dev servers: %v", projectName, err)
		return nil
	}
	var candidates []portproc.Listener
	for _, l := range found {
		if underDir(root, l.Dir) {
			candidates = append(candidates, l)
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	tctx, tcancel := context.WithTimeout(ctx, devExecTimeout)
	table, terr := proctree.Read(tctx)
	tcancel()
	pctx, pcancel := context.WithTimeout(ctx, devExecTimeout)
	panePIDs, perr := tm.PanePIDs(pctx)
	pcancel()
	if terr != nil || perr != nil {
		d.logf("", "dev: leaving %d stray listener(s) in %s alone — cannot tell them from a live pane (ps: %v, tmux: %v)",
			len(candidates), root, terr, perr)
		return nil
	}

	// Everything a live pane leads is off limits, and so is the tmux server
	// above it: those groups hold the agents, the shell tabs and the dev tabs.
	protected := map[int]bool{}
	for _, pid := range panePIDs {
		if pgid := table.Group(pid); pgid > 1 {
			protected[pgid] = true
		}
		if server := table.Parent(pid); server > 1 {
			if pgid := table.Group(server); pgid > 1 {
				protected[pgid] = true
			}
		}
	}

	var freed []string
	kill := map[int]bool{}
	for _, l := range candidates {
		var wanted []int
		if pgid := table.Group(l.PID); pgid > 1 {
			wanted = append(wanted, pgid)
		}
		wanted = append(wanted, table.TreeGroups(l.PID)...)
		if len(wanted) == 0 {
			continue // it exited between lsof and ps; its port is already free
		}
		blocked := false
		for _, pgid := range wanted {
			if protected[pgid] {
				blocked = true
				break
			}
		}
		if blocked {
			d.logf("", "dev: :%d is held by %s (pid %d) in %s, which a live tmux pane owns — left running",
				l.Port, l.Command, l.PID, l.Dir)
			continue
		}
		for _, pgid := range wanted {
			kill[pgid] = true
		}
		desc := fmt.Sprintf(":%d (%s, pid %d", l.Port, l.Command, l.PID)
		if owner := ownerOfWorktree(root, l.Dir); owner != "" {
			desc += ", " + owner
		}
		freed = append(freed, desc+")")
	}
	if len(kill) == 0 {
		return nil
	}

	pgids := make([]int, 0, len(kill))
	for pgid := range kill {
		pgids = append(pgids, pgid)
	}
	sort.Ints(pgids)
	if err := proctree.KillGroups(pgids, squatterGrace); err != nil {
		d.logf("", "dev: reclaiming stray ports in %s: %v", root, err)
	}
	d.logf("", "dev: reclaimed %s in %s", strings.Join(freed, ", "), root)
	return freed
}

// underDir reports whether dir is root or sits below it, on a PATH BOUNDARY —
// "…/worktrees/nori" must not match "…/worktrees/nori-app/x".
func underDir(root, dir string) bool {
	if root == "" || dir == "" {
		return false
	}
	root = filepath.Clean(root)
	dir = filepath.Clean(dir)
	return dir == root || strings.HasPrefix(dir, root+string(filepath.Separator))
}

// ownerOfWorktree names the session a squatter was running in: the first path
// segment below the project's worktrees root, which IS the session id.
func ownerOfWorktree(root, dir string) string {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(dir))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	if i := strings.IndexRune(rel, filepath.Separator); i > 0 {
		return rel[:i]
	}
	return rel
}

// stopDevTabs kills every dev tab of one session and reports how many it killed.
// Discovery is by LISTING (not by the project's current dev_commands length), so
// a tab whose command has since been removed from config is still torn down.
//
// It kills the pane's whole process GROUP (KillSessionTree), not just the tmux
// session: `composer dev` spawns `php artisan serve`, which ignores the SIGHUP
// tmux sends and would keep :8000 bound as an orphan of pid 1 — so the session
// taking over would start on 8001 and the whole feature would have moved the
// problem rather than solved it.
func (d *Daemon) stopDevTabs(ctx context.Context, tm devTmux, sessionID string) int {
	lctx, cancel := context.WithTimeout(ctx, devExecTimeout)
	sessions, err := tm.ListSessions(lctx)
	cancel()
	if err != nil {
		d.logf("", "dev: could not list tmux sessions to stop %s's dev tabs: %v", sessionID, err)
		return 0
	}
	killed := 0
	for _, ts := range sessions {
		if devtab.Index(sessionID, ts.Name) == 0 {
			continue
		}
		// A fresh deadline PER TAB: each tree kill spends up to the SIGTERM
		// grace waiting for its dev server to release its port, so a shared
		// budget would start SIGKILLing later tabs to make the clock.
		kctx, kcancel := context.WithTimeout(ctx, devExecTimeout)
		err := tm.KillSessionTree(kctx, ts.Name)
		kcancel()
		if err != nil {
			d.logf("", "dev: could not kill %s: %v", ts.Name, err)
			continue
		}
		killed++
	}
	return killed
}

// reconcileDevTabs re-derives every session's dev state from the cycle's tmux
// facts and reports whether any record changed. aliveNames is the observer's
// one-per-cycle `tmux ls` result, so the common case (no dev tabs anywhere)
// costs a map scan and no exec at all.
//
// A tab counts only while its pane LIVES: the tabs are created with
// remain-on-exit, so a `npm run dev` that crashed leaves its session standing
// with a dead pane — visible output, but not a running dev server, and treating
// it as active would leave the project stuck on a session that serves nothing.
// A dead-pane probe that FAILS leaves every record untouched: last-known beats
// guessing, in either direction (a false "off" invites a restart that kills a
// healthy server, a false "on" hides one that is gone).
func (d *Daemon) reconcileDevTabs(ctx context.Context, aliveNames map[string]bool) bool {
	var tabs []string
	for name := range aliveNames {
		if devtab.Is(name) {
			tabs = append(tabs, name)
		}
	}
	dead := map[string]bool{}
	if len(tabs) > 0 {
		if d.deadPanes == nil {
			return false
		}
		cctx, cancel := context.WithTimeout(ctx, devExecTimeout)
		got, err := d.deadPanes(cctx)
		cancel()
		if err != nil {
			d.logf("", "observe: dev tab liveness probe failed (keeping last known): %v", err)
			return false
		}
		dead = got
	}

	changed := false
	for _, s := range d.sessions.Snapshot() {
		live := 0
		var liveTabs []string
		for _, name := range tabs {
			if devtab.Index(s.ID, name) > 0 && !dead[name] {
				live++
				liveTabs = append(liveTabs, name)
			}
		}
		if s.DevActive != (live > 0) || s.DevTabs != live {
			d.markDev(s.ID, live > 0, live)
			changed = true
		}
		if d.scanDevURLs(ctx, s, live, liveTabs) {
			changed = true
		}
	}
	return changed
}

// devURLScanLines is how far back a dev tab's scrollback is read. A server
// prints its address ONCE, at startup, and then a queue worker or a log tailer
// pushes it off the visible screen within seconds — so the visible pane alone
// answers almost never.
const devURLScanLines = 2000

// devURLAttempts bounds how many cycles are spent looking for an address in a
// pane that has none. A `tailwindcss --watch` tab never prints one, and without
// a cap it would cost a 2000-line capture every 30s forever. Reset whenever the
// tab set changes, so a restart gets a fresh budget.
const devURLAttempts = 20

// scanDevURLs keeps a session's DevURLs in step with its dev tabs, and reports
// whether it changed the record.
//
// The addresses are DERIVED, exactly like DevActive: they are dropped the moment
// the tabs stop, because a link to a server that is gone is worse than no link.
// Beyond that it is deliberately lazy — a pane is only read while there is
// nothing known yet, or right after the tab set changed (a start, a restart, a
// crash) — since the address does not move on its own.
func (d *Daemon) scanDevURLs(ctx context.Context, s session.Session, live int, liveTabs []string) bool {
	if live == 0 {
		d.forgetDevURLTries(s.ID)
		return d.setDevURLs(s.ID, nil)
	}
	restarted := s.DevTabs != live
	if restarted {
		d.forgetDevURLTries(s.ID)
	} else if len(s.DevURLs) > 0 {
		return false // known, and nothing about the tabs has changed
	}
	if d.paneTail == nil || !d.spendDevURLTry(s.ID) {
		return false
	}

	var found []string
	seen := map[string]bool{}
	for _, name := range liveTabs {
		cctx, cancel := context.WithTimeout(ctx, devExecTimeout)
		pane, err := d.paneTail(cctx, name, devURLScanLines)
		cancel()
		if err != nil {
			d.logf("", "observe: could not read %s for its dev address: %v", name, err)
			continue
		}
		// Per TAB, so a two-command project keeps its app URL ahead of its
		// bundler's even though the panes are ranked independently.
		for _, u := range devurl.URLs(pane) {
			if !seen[u] {
				seen[u] = true
				found = append(found, u)
			}
		}
	}
	if len(found) > devurl.MaxCandidates {
		found = found[:devurl.MaxCandidates]
	}
	if len(found) == 0 {
		return false
	}
	if d.setDevURLs(s.ID, found) {
		d.logf("", "dev: %s serves %s", s.ID, strings.Join(found, ", "))
		return true
	}
	return false
}

// setDevURLs writes the derived addresses onto the record, reporting whether
// anything actually changed.
func (d *Daemon) setDevURLs(sessionID string, urls []string) bool {
	_, changed := d.sessions.Update(sessionID, func(s *session.Session) bool {
		if slices.Equal(s.DevURLs, urls) {
			return false
		}
		s.DevURLs = urls
		return true
	})
	return changed
}

// spendDevURLTry consumes one of a session's pane-read attempts, reporting
// whether it had any left.
func (d *Daemon) spendDevURLTry(sessionID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.devURLTries == nil {
		d.devURLTries = map[string]int{}
	}
	if d.devURLTries[sessionID] >= devURLAttempts {
		return false
	}
	d.devURLTries[sessionID]++
	return true
}

func (d *Daemon) forgetDevURLTries(sessionID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.devURLTries, sessionID)
}

// markDev records the toggle's outcome on the session record so the UIs reflect
// it immediately instead of at the next observe cycle. It is a CACHE of the tmux
// facts, never the truth: reconcileDevTabs overwrites it every cycle.
func (d *Daemon) markDev(sessionID string, active bool, tabs int) {
	d.sessions.Update(sessionID, func(s *session.Session) bool {
		if s.DevActive == active && s.DevTabs == tabs {
			return false
		}
		s.DevActive, s.DevTabs = active, tabs
		// Any change to the tabs invalidates the addresses they printed: the
		// servers stopped, or restarted onto other ports. scanDevURLs finds them
		// again on the next cycle — until then, none is the honest answer.
		s.DevURLs = nil
		return true
	})
}

// devWorktreeDir is the directory a session's dev tabs run in: its persisted
// worktree, else the canonical <home>/worktrees/<project>/<id> (the same
// derivation the kill path uses for a record written before Worktree existed).
func devWorktreeDir(s session.Session, projectName, home string) string {
	if s.Worktree != "" {
		return s.Worktree
	}
	return filepath.Join(home, "worktrees", projectName, s.ID)
}

// devTmux is the slice of the tmux client the dev paths use, so tests can drive
// the whole toggle without a tmux server. *tmux.Client satisfies it.
type devTmux interface {
	Available() bool
	ListSessions(ctx context.Context) ([]tmux.Session, error)
	PanePIDs(ctx context.Context) ([]int, error)
	Has(ctx context.Context, name string) bool
	NewSession(ctx context.Context, name, dir, command string) error
	KillSession(ctx context.Context, name string) error
	KillSessionTree(ctx context.Context, name string) error
	KeepDeadPane(ctx context.Context, name string) error
}

// devTmuxClient resolves the tmux adapter the dev paths talk to: the injected
// fake in tests, the real lola-server client otherwise.
func (d *Daemon) devTmuxClient() devTmux {
	d.mu.Lock()
	fn := d.devTmux
	d.mu.Unlock()
	if fn != nil {
		return fn()
	}
	return d.tmuxClient()
}
