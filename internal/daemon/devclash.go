package daemon

// devclash.go explains ONE dev-tab failure and offers to undo it: the command
// exited because something else already holds the port it wanted.
//
// It exists because that failure is the only one a human cannot read off the
// pane. A dev server that crashes on a syntax error leaves the error on screen;
// one that loses a port race prints a single line and exits, and a `wails3 dev`
// or `vite` clears the screen on its way out, so the tab reads as "dead, no
// reason given". The cause is also somewhere else entirely — another session's
// leftover server, or a `npm run dev` the user started in their own checkout an
// hour ago — which is exactly what makes it worth naming.
//
// The sweep in dev.go already reclaims ports automatically, but ONLY inside
// ~/.lola/worktrees/<project>/: a server in the user's own checkout is theirs,
// not lola's to kill. That rule is not relaxed here. This path turns the case
// the sweep refuses into a QUESTION — the daemon detects, describes and waits;
// a human answers; only then is anything signalled.
//
// Detection is post-mortem and cheap: a dead dev tab is read once (a short
// bounded capture), and lsof is asked only when the pane actually said a port
// was taken. A healthy project costs nothing at all.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/sushidev-team/lola/internal/devtab"
	"github.com/sushidev-team/lola/internal/portclash"
	"github.com/sushidev-team/lola/internal/portproc"
	"github.com/sushidev-team/lola/internal/proctree"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

// devClashScanLines is how far back a DEAD dev tab is read. Unlike the address
// scan (which hunts a line that scrolled away hours ago) the failure is the last
// thing the command printed, so a short tail is enough — and a dead pane is
// frozen, so re-reading more would never find more.
const devClashScanLines = 300

// scanDevClash examines a session's DEAD dev tabs for a port clash and records
// what it finds, reporting whether the session record changed.
//
// It is deliberately one-shot per tab (devClashChecked): the pane of a dead tab
// never changes again, so a second read would cost the same and learn the same.
// Everything about it fails closed — no pane reader, no lsof, an unparsable
// pane, a port nobody holds any more — because the only thing a finding buys is
// a dialog offering to kill a process, and a wrong one is worse than none.
func (d *Daemon) scanDevClash(ctx context.Context, sessionID string, deadTabs []string, projectName, home string, commands []string) bool {
	if len(deadTabs) == 0 {
		return d.setDevClash(sessionID, nil)
	}
	// The FRESH record, not the observer's snapshot: markDev may have cleared a
	// clash between the two, and re-explaining a tab is the whole point of that.
	s, ok := d.sessions.Get(sessionID)
	if !ok || s.DevClash != nil {
		return false // already explained; the pane cannot change under a dead tab
	}
	d.mu.Lock()
	tail, listeners := d.paneTail, d.portListeners
	d.mu.Unlock()
	if tail == nil || listeners == nil {
		return false
	}

	for _, tab := range deadTabs {
		if !d.spendDevClashCheck(tab) {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, devExecTimeout)
		pane, err := tail(cctx, tab, devClashScanLines)
		cancel()
		if err != nil {
			d.logf("", "observe: could not read %s to explain why it died: %v", tab, err)
			continue
		}
		port, ok := portclash.Port(pane)
		if !ok {
			continue // it died of something a human can read on the pane
		}
		holder, ok := d.portHolder(ctx, port)
		if !ok {
			// The port is free again: whatever won the race has since exited, so
			// there is nothing to offer and nothing to explain.
			continue
		}
		clash := &session.DevClash{
			Tab:     tab,
			Command: devTabCommand(s.ID, tab, commands),
			Port:    port,
			PID:     holder.PID,
			Proc:    holder.Command,
			Dir:     holder.Dir,
			Ours:    underDir(filepath.Join(home, "worktrees", projectName), holder.Dir),
			At:      time.Now(),
		}
		d.logf("", "dev: %s died on :%d, which %s (pid %d) holds in %s",
			tab, port, holder.Command, holder.PID, holder.Dir)
		return d.setDevClash(s.ID, clash)
	}
	return false
}

// portHolder names the process listening on one port. It reports nothing rather
// than a guess whenever lsof cannot answer — the caller would otherwise offer to
// kill a pid it did not verify.
func (d *Daemon) portHolder(ctx context.Context, port int) (portproc.Listener, bool) {
	d.mu.Lock()
	listeners := d.portListeners
	d.mu.Unlock()
	if listeners == nil || port <= 0 {
		return portproc.Listener{}, false
	}
	lctx, cancel := context.WithTimeout(ctx, devExecTimeout)
	found, err := listeners(lctx)
	cancel()
	if err != nil {
		d.logf("", "dev: could not check who holds :%d: %v", port, err)
		return portproc.Listener{}, false
	}
	for _, l := range found {
		if l.Port == port {
			return l, true
		}
	}
	return portproc.Listener{}, false
}

// devTabCommand labels a tab with the dev_commands entry it was running. The
// label comes from CONFIG, never from the pane: it is shown to a human beside a
// kill button, so it must not be attacker-influenceable text.
func devTabCommand(sessionID, tab string, commands []string) string {
	i := devtab.Index(sessionID, tab)
	if i <= 0 || i > len(commands) {
		return ""
	}
	return commands[i-1]
}

// setDevClash writes (or clears) a session's clash, reporting whether the record
// changed.
func (d *Daemon) setDevClash(sessionID string, clash *session.DevClash) bool {
	_, changed := d.sessions.Update(sessionID, func(s *session.Session) bool {
		switch {
		case clash == nil && s.DevClash == nil:
			return false
		case clash != nil && s.DevClash != nil &&
			s.DevClash.Tab == clash.Tab && s.DevClash.Port == clash.Port && s.DevClash.PID == clash.PID:
			return false
		}
		s.DevClash = clash
		return true
	})
	return changed
}

// spendDevClashCheck reports whether this tab's death still has to be examined,
// consuming the one check it gets.
func (d *Daemon) spendDevClashCheck(tab string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.devClashChecked == nil {
		d.devClashChecked = map[string]bool{}
	}
	if d.devClashChecked[tab] {
		return false
	}
	d.devClashChecked[tab] = true
	return true
}

// forgetDevClashChecks re-arms the examination of the named tabs, so a restarted
// tab that dies the same way is explained a second time.
func (d *Daemon) forgetDevClashChecks(tabs ...string) {
	if len(tabs) == 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, tab := range tabs {
		delete(d.devClashChecked, tab)
	}
}

// reapDevClashChecks drops the bookkeeping for every tab that is no longer dead
// — it either lives again or is gone — which is what makes the one-shot check
// per DEATH rather than per tab name for the daemon's lifetime.
func (d *Daemon) reapDevClashChecks(dead map[string]bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for tab := range d.devClashChecked {
		if !dead[tab] {
			delete(d.devClashChecked, tab)
		}
	}
}

// handleDevFreePort answers a human's "yes, kill it" (cmd=devFreePort): it takes
// down the process holding the port a dev tab died on, then starts the session's
// dev tabs again.
//
// This is the ONE path in lola that signals a process outside its own worktrees,
// so every step is a gate:
//
//   - The request must MATCH the clash on record (session + port + pid). A
//     client cannot name an arbitrary pid, and a dialog left open while things
//     moved on is refused rather than applied to whatever holds the port now.
//   - The pid must STILL hold that port, re-checked with a fresh lsof at this
//     moment. Pids are reused, and the gap between detection and a human's click
//     is unbounded — it is the whole point of asking.
//   - A process group that owns a live tmux pane is refused outright, the same
//     rail the sweep has: every agent's cwd is its worktree, and a session's own
//     pane must never be killable through this.
//   - Anything it cannot verify (no lsof, no ps, no tmux) is an error and
//     nothing is signalled.
func (d *Daemon) handleDevFreePort(ctx context.Context, a protocol.DevFreePortArgs) (protocol.DevFreePortData, error) {
	if a.Session == "" {
		return protocol.DevFreePortData{}, errors.New("session id required")
	}
	s, ok := d.sessions.Get(a.Session)
	if !ok {
		return protocol.DevFreePortData{}, fmt.Errorf("unknown session %s", a.Session)
	}
	clash := s.DevClash
	if clash == nil {
		return protocol.DevFreePortData{}, fmt.Errorf("session %s has no port clash on record", s.ID)
	}
	if a.Port != clash.Port || a.PID != clash.PID {
		return protocol.DevFreePortData{}, fmt.Errorf(
			"port clash has changed (now :%d held by pid %d) — check again", clash.Port, clash.PID)
	}

	tm := d.devTmuxClient()
	if tm == nil || !tm.Available() {
		return protocol.DevFreePortData{}, errors.New("tmux is not available")
	}

	// Re-verify against the machine as it is NOW, not as it was when the clash
	// was detected: a holder that exited in the meantime must not cost an
	// unrelated process its life through a recycled pid.
	holder, ok := d.portHolder(ctx, clash.Port)
	if !ok {
		d.setDevClash(s.ID, nil)
		return protocol.DevFreePortData{}, fmt.Errorf(":%d is free again — nothing to kill", clash.Port)
	}
	if holder.PID != clash.PID {
		d.setDevClash(s.ID, nil)
		return protocol.DevFreePortData{}, fmt.Errorf(
			":%d is now held by %s (pid %d), not the process you were shown — check again",
			clash.Port, holder.Command, holder.PID)
	}

	groups, err := d.killableGroups(ctx, tm, holder)
	if err != nil {
		return protocol.DevFreePortData{}, err
	}
	if err := proctree.KillGroups(groups, squatterGrace); err != nil {
		return protocol.DevFreePortData{}, fmt.Errorf("could not stop %s (pid %d): %w", holder.Command, holder.PID, err)
	}
	d.setDevClash(s.ID, nil)
	d.forgetDevClashChecks(clash.Tab)
	d.logf("", "dev: freed :%d — killed %s (pid %d) in %s at %s's request",
		clash.Port, holder.Command, holder.PID, holder.Dir, s.ID)

	// Restarting goes through the ordinary toggle, so the take-over, the
	// worktree sweep and the address watch all behave exactly as they do for a
	// click on Active. A restart that fails is reported, not swallowed: the port
	// is free either way, which is already half the answer.
	dev, derr := d.handleDev(ctx, s.ID, true)
	msg := fmt.Sprintf("freed :%d (%s, pid %d)", clash.Port, holder.Command, holder.PID)
	if derr != nil {
		return protocol.DevFreePortData{Freed: true, Port: clash.Port, Message: msg},
			fmt.Errorf("%s, but the dev processes did not restart: %w", msg, derr)
	}
	return protocol.DevFreePortData{
		Freed:   true,
		Port:    clash.Port,
		Dev:     dev,
		Message: msg + "; " + dev.Message,
	}, nil
}

// killableGroups resolves the process groups that have to go down to free a
// holder's port, refusing whenever the answer cannot be trusted.
//
// The tree matters as much as the group: Claude Code's Bash tool puts every
// command it runs in its OWN process group, so the process actually holding the
// port is regularly not in the group of the one lsof named (the same reason
// tmux.KillSessionTree exists).
func (d *Daemon) killableGroups(ctx context.Context, tm devTmux, holder portproc.Listener) ([]int, error) {
	tctx, tcancel := context.WithTimeout(ctx, devExecTimeout)
	table, terr := proctree.Read(tctx)
	tcancel()
	pctx, pcancel := context.WithTimeout(ctx, devExecTimeout)
	panePIDs, perr := tm.PanePIDs(pctx)
	pcancel()
	if terr != nil || perr != nil {
		return nil, fmt.Errorf("cannot tell pid %d from a live session's own processes (ps: %v, tmux: %v)",
			holder.PID, terr, perr)
	}

	// Everything a live pane leads is off limits, and so is the tmux server
	// above it — those groups hold the agents, the shell tabs and the dev tabs.
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

	var wanted []int
	if pgid := table.Group(holder.PID); pgid > 1 {
		wanted = append(wanted, pgid)
	}
	wanted = append(wanted, table.TreeGroups(holder.PID)...)
	if len(wanted) == 0 {
		return nil, fmt.Errorf("pid %d is already gone", holder.PID)
	}
	unique := map[int]bool{}
	for _, pgid := range wanted {
		if protected[pgid] {
			return nil, fmt.Errorf("%s (pid %d) belongs to a live lola session — it will not be killed",
				holder.Command, holder.PID)
		}
		unique[pgid] = true
	}
	groups := make([]int, 0, len(unique))
	for pgid := range unique {
		groups = append(groups, pgid)
	}
	sort.Ints(groups)
	return groups, nil
}
