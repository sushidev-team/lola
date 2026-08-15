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
	"time"

	"github.com/sushidev-team/lola/internal/devtab"
	"github.com/sushidev-team/lola/internal/lolaenv"
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

	stopped := d.takeOverProject(ctx, tm, s)

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
		for _, name := range tabs {
			if devtab.Index(s.ID, name) > 0 && !dead[name] {
				live++
			}
		}
		if s.DevActive == (live > 0) && s.DevTabs == live {
			continue
		}
		d.markDev(s.ID, live > 0, live)
		changed = true
	}
	return changed
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
