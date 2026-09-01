package daemon

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/sushidev-team/lola/internal/devtab"
	"github.com/sushidev-team/lola/internal/lolaenv"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

// The pane INVENTORY and shell creation, over the socket.
//
// The desktop app has had both since it was written, but as Wails services
// (desktop/termsvc.go's Shells and Shell) that drive `tmux -L lola` on the same
// machine. A phone cannot do that: it is a socket client with no tmux and no
// filesystem, so anything it wants to know about panes, or wants created, has to
// be a daemon command. These are those commands.
//
// They are deliberately NOT in internal/remote's deniedCommands. A phone that
// cannot enumerate panes cannot draw a tab strip, and one that cannot create a
// shell cannot do the thing the operator asked for. Both are reachable by any
// paired device.
//
// Say plainly what shellCreate therefore is: a remote caller can start a shell
// in a worktree, and a shell in a worktree runs as the developer with their gh
// token, SSH agent and login keychain in reach. That is arbitrary code execution
// on the machine, initiated from the phone. It is allowed because the operator
// decided phones get shell access (mobile/PLAN.md, "Settled since drafting"),
// and because M1 grants every paired device everything — there are no capability
// tiers until M2 brings per-device identities. When those land this is the first
// command that should sit behind the `shell` capability.

// maxShellTabs bounds how many shells one session can accumulate.
//
// Not a resource limit — tmux would happily hold hundreds — but a mistake limit.
// The create path is one socket call, so a client in a retry loop, or a human
// with a stuck finger on a phone, can otherwise spawn shells until something
// else falls over. Reaching it is a refusal with a reason rather than a silent
// cap, because a tab strip that stops growing for no stated reason reads as a
// broken button.
const maxShellTabs = 16

// paneKind names the ROLE of a pane, so a client can group and label a tab strip
// without re-deriving the naming convention. The strings are the wire contract;
// internal/runtime and internal/devtab own the names themselves.
const (
	paneKindAgent  = "agent"
	paneKindShell  = "shell"
	paneKindDev    = "dev"
	paneKindReview = "review"
)

const (
	shellInfix   = "-shell-"
	reviewSuffix = "-review"
)

// handlePanes lists the panes that EXIST for a session, in the order a tab strip
// should draw them: the agent first, then shells and dev tabs in index order,
// then the review pane.
//
// It reads tmux rather than the session record, and that is the whole point.
// Session.DevTabs is a cache the observer overwrites from the same facts, shell
// tabs are recorded nowhere at all, and a strip drawn from a stale cache offers
// tabs that are gone and hides ones that are there. The pane list is derived,
// like DevActive and DevURLs before it, for the same reason.
func (d *Daemon) handlePanes(ctx context.Context, sessionID string) (protocol.PanesData, error) {
	s, ok := d.sessionByID(sessionID)
	if !ok {
		return protocol.PanesData{}, fmt.Errorf("unknown session %q", sessionID)
	}
	parent := paneTarget(s)

	live, err := d.livePaneNames(ctx)
	if err != nil {
		return protocol.PanesData{}, err
	}

	out := protocol.PanesData{Session: sessionID}
	var shells, devs []protocol.PaneInfo
	for _, name := range live {
		switch {
		case name == parent:
			out.Panes = append(out.Panes, protocol.PaneInfo{
				Name: name, Kind: paneKindAgent, Label: "agent",
			})
		case strings.HasPrefix(name, parent+shellInfix):
			if n := shellIndex(parent, name); n > 0 {
				shells = append(shells, protocol.PaneInfo{
					Name: name, Kind: paneKindShell, Index: n,
					Label: "shell " + strconv.Itoa(n),
				})
			}
		case devtab.Index(parent, name) > 0:
			n := devtab.Index(parent, name)
			devs = append(devs, protocol.PaneInfo{
				Name: name, Kind: paneKindDev, Index: n,
				Label: "dev " + strconv.Itoa(n),
			})
		case name == parent+reviewSuffix:
			out.Review = protocol.PaneInfo{Name: name, Kind: paneKindReview, Label: "review"}
		}
	}

	sortByIndex(shells)
	sortByIndex(devs)
	out.Panes = append(out.Panes, shells...)
	out.Panes = append(out.Panes, devs...)
	if out.Review.Name != "" {
		out.Panes = append(out.Panes, out.Review)
	}
	// Whether another shell may be created, answered HERE rather than left for
	// the client to infer from a count it would have to know the cap for.
	out.CanCreateShell = len(shells) < maxShellTabs && s.Worktree != ""
	return out, nil
}

// handleShellCreate starts a new shell tab for a session and returns its name,
// which the caller then subscribes to like any other pane.
//
// The INDEX is allocated here, not by the caller. The desktop's TermService lets
// its frontend own the name because both run in one process on one machine; two
// phones and a desktop racing for "-shell-2" do not have that luxury, and the
// daemon is the only place that can see all of them.
func (d *Daemon) handleShellCreate(ctx context.Context, sessionID string) (protocol.ShellCreateData, error) {
	s, ok := d.sessionByID(sessionID)
	if !ok {
		return protocol.ShellCreateData{}, fmt.Errorf("unknown session %q", sessionID)
	}
	parent := paneTarget(s)

	// A shell is rooted in the worktree, so a session without one — a record
	// whose checkout was already removed — has nowhere to start it. Refuse with
	// the reason rather than creating a shell in whatever directory the daemon
	// happens to be in, which would be a shell in the operator's repository.
	if s.Worktree == "" {
		return protocol.ShellCreateData{}, fmt.Errorf("session %q has no worktree", sessionID)
	}
	if fi, err := os.Stat(s.Worktree); err != nil || !fi.IsDir() {
		return protocol.ShellCreateData{}, fmt.Errorf("worktree unavailable: %s", s.Worktree)
	}

	live, err := d.livePaneNames(ctx)
	if err != nil {
		return protocol.ShellCreateData{}, err
	}
	taken := map[int]bool{}
	for _, name := range live {
		if n := shellIndex(parent, name); n > 0 {
			taken[n] = true
		}
	}
	if len(taken) >= maxShellTabs {
		return protocol.ShellCreateData{}, fmt.Errorf(
			"session %q already has %d shells, which is the cap", sessionID, len(taken))
	}
	next := 1
	for taken[next] {
		next++
	}
	name := parent + shellInfix + strconv.Itoa(next)

	cl := d.tmuxClient()
	// lolaenv.ShellCommand rather than a bare login shell, the same line the
	// agent pane and the desktop's shell tabs use: a plain shell silently lacks
	// [[project]].env, which is the difference between a working tab and one
	// where every command is subtly misconfigured.
	//
	// NewSession applies the scroll defaults first and retries them on a cold
	// server, which matters here because a shell tab can be the session that
	// starts one — see (*tmux.Client).ConfigureServer.
	if err := cl.NewSession(ctx, name, s.Worktree, lolaenv.ShellCommand); err != nil {
		return protocol.ShellCreateData{}, fmt.Errorf("new shell %s: %w", name, err)
	}
	d.logf("", "remote: created shell tab %s", name)
	return protocol.ShellCreateData{Session: sessionID, Pane: name, Index: next}, nil
}

// livePaneNames lists every tmux session name on lola's own server.
//
// A missing server is an empty list, not an error: a daemon whose last session
// ended has no tmux running, and a tab strip asking about it should render
// nothing rather than an error banner.
func (d *Daemon) livePaneNames(ctx context.Context) ([]string, error) {
	sessions, err := d.tmuxClient().ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, s.Name)
	}
	return out, nil
}

// sessionByID reads one session out of the store snapshot.
func (d *Daemon) sessionByID(id string) (session.Session, bool) {
	if id == "" {
		return session.Session{}, false
	}
	for _, s := range d.sessions.Snapshot() {
		if s.ID == id {
			return s, true
		}
	}
	return session.Session{}, false
}

// shellIndex returns the N of "<parent>-shell-N", or 0 when name is not one.
//
// Anchored on both ends via the numeric parse, for the reason the teardown
// invariant gives: "lola-fe-42" is a prefix of "lola-fe-420-shell-1", so a
// loose prefix test would claim another session's tab as this one's.
func shellIndex(parent, name string) int {
	rest, ok := strings.CutPrefix(name, parent+shellInfix)
	if !ok || rest == "" {
		return 0
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func sortByIndex(p []protocol.PaneInfo) {
	sort.Slice(p, func(i, j int) bool { return p[i].Index < p[j].Index })
}

// maxPaneDim bounds a pin. A phone is not 10000 columns wide, and a window
// resized to something absurd is a wedged agent rather than a small one — the
// TUI redraws against whatever it is told.
const maxPaneDim = 500

// handlePaneClose closes ONE auxiliary pane of a session.
//
// The agent pane is refused, and that is the rule the whole command turns on:
// the agent pane IS the session, so closing it would end the work and leave a
// record pointing at nothing. Teardown is cmd=kill, which knows to take the
// worktree and the branch too. A tab strip offering a close on the agent tab
// would be offering a kill disguised as a tidy-up.
//
// The pane must belong to the named session. Without that check a device could
// close another session's tab by naming it, which the identity gate on the
// SUBSCRIBE path already prevents and which this path would otherwise reopen.
//
// It kills the session TREE, not the session: a shell can be holding a port
// through a process that ignores SIGHUP, and `kill-session` alone would orphan
// it onto pid 1 — the same reason teardown uses KillSessionTree everywhere.
func (d *Daemon) handlePaneClose(ctx context.Context, a protocol.PaneCloseArgs) (protocol.PaneCloseData, error) {
	s, ok := d.sessionByID(a.Session)
	if !ok {
		return protocol.PaneCloseData{}, fmt.Errorf("unknown session %q", a.Session)
	}
	parent := paneTarget(s)
	if a.Pane == "" {
		return protocol.PaneCloseData{}, fmt.Errorf("paneClose: no pane named")
	}
	if a.Pane == parent {
		return protocol.PaneCloseData{}, fmt.Errorf(
			"the agent pane cannot be closed; it is the session. Use kill to end the session")
	}
	if !d.paneBelongsTo(parent, a.Pane) {
		return protocol.PaneCloseData{}, fmt.Errorf("pane %q does not belong to session %q", a.Pane, a.Session)
	}

	if err := d.tmuxClient().KillSessionTree(ctx, a.Pane); err != nil {
		return protocol.PaneCloseData{}, fmt.Errorf("close %s: %w", a.Pane, err)
	}
	d.logf("", "remote: closed pane %s", a.Pane)
	return protocol.PaneCloseData{Session: a.Session, Pane: a.Pane, Closed: true}, nil
}

// handlePaneResize pins a pane's window to a size, or releases it.
//
// This is the deliberate opposite of panebus's ignore-size attach, and both are
// right. The fan-out must never reshape the developer's window as a side effect
// of a phone subscribing; this reshapes it because a human explicitly asked, for
// as long as they are looking at it. tmux runs window-size latest, so releasing
// hands the size straight back to whatever clients remain attached.
//
// The agent pane is ALLOWED here, unlike close: resizing it is the entire point.
func (d *Daemon) handlePaneResize(ctx context.Context, a protocol.PaneResizeArgs) (protocol.PaneResizeData, error) {
	// A release is cols <= 0, and it must stay reachable even for nonsense
	// dimensions: a client that pinned and then sent garbage should be able to
	// undo it, so the bound REJECTS rather than clamping only on the pin path.
	// Decided FIRST because the identity checks below forgive a release.
	release := a.Cols <= 0 || a.Rows <= 0

	if a.Pane == "" {
		return protocol.PaneResizeData{}, fmt.Errorf("paneResize: no pane named")
	}

	// A RELEASE OF SOMETHING THAT NO LONGER EXISTS IS SUCCESS, and that has to
	// cover the SESSION being gone, not just the pane.
	//
	// The client releases from a breadcrumb it persisted before the pin went
	// out, so it routinely names a pane after everything around it has moved
	// on: the session was killed, the daemon restarted, the phone was in a
	// pocket for a day. A hard refusal there is unfalsifiable from the client
	// side — it cannot tell "I refuse" from "the release did not land" — so it
	// keeps believing it holds the pane, keeps the breadcrumb, and warns about
	// a squashed window on every screen it opens, forever. That is not
	// theoretical: it is what a phone did after talking to a daemon too old to
	// know cmd=paneResize.
	//
	// Liveness is what makes forgiving safe. A pane name that is not a live
	// tmux session holds no size, so answering "released" states a fact. A pane
	// that IS live still gets the full identity gate — an unknown session or a
	// name outside this session's own tabs must never resize somebody else's
	// window, which is the whole point of the check.
	forgive := func() (protocol.PaneResizeData, bool) {
		if !release || d.paneIsLive(ctx, a.Pane) {
			return protocol.PaneResizeData{}, false
		}
		d.logf("", "remote: %s is gone; its pinned size went with it", a.Pane)
		return protocol.PaneResizeData{Session: a.Session, Pane: a.Pane, Pinned: false}, true
	}

	s, ok := d.sessionByID(a.Session)
	if !ok {
		if data, forgiven := forgive(); forgiven {
			return data, nil
		}
		return protocol.PaneResizeData{}, fmt.Errorf("unknown session %q", a.Session)
	}
	parent := paneTarget(s)
	if a.Pane != parent && !d.paneBelongsTo(parent, a.Pane) {
		if data, forgiven := forgive(); forgiven {
			return data, nil
		}
		return protocol.PaneResizeData{}, fmt.Errorf("pane %q does not belong to session %q", a.Pane, a.Session)
	}

	if !release && (a.Cols > maxPaneDim || a.Rows > maxPaneDim) {
		return protocol.PaneResizeData{}, fmt.Errorf(
			"paneResize: %dx%d is out of range (max %d)", a.Cols, a.Rows, maxPaneDim)
	}

	cols, rows := a.Cols, a.Rows
	if release {
		cols, rows = 0, 0
	}
	if err := d.tmuxClient().SetWindowSize(ctx, a.Pane, cols, rows); err != nil {
		// A RELEASE OF A WINDOW THAT IS GONE IS SUCCESS, not failure. The checks
		// above validate a pane by NAME, not by liveness, so releasing a pane
		// the user has just closed reaches tmux and is refused — and the client
		// cannot tell that apart from a release that genuinely did not land, so
		// it raises its stuck-pin warning. That is a false alarm in the one
		// place this feature has to be believed: nothing is squashed, because
		// the window whose size was pinned no longer exists.
		//
		// Only a RELEASE forgives, and only when the pane is really absent. A
		// failed pin still fails, and a release that failed for any other reason
		// still fails, so the warning keeps meaning what it says. Liveness is
		// checked AFTER the attempt rather than before, so a pane that dies
		// between the two is forgiven instead of racing.
		if data, forgiven := forgive(); forgiven {
			return data, nil
		}
		return protocol.PaneResizeData{}, fmt.Errorf("resize %s: %w", a.Pane, err)
	}
	if release {
		d.logf("", "remote: released the pinned size of %s", a.Pane)
		return protocol.PaneResizeData{Session: a.Session, Pane: a.Pane, Pinned: false}, nil
	}
	d.logf("", "remote: pinned %s to %dx%d for a phone", a.Pane, cols, rows)
	return protocol.PaneResizeData{
		Session: a.Session, Pane: a.Pane, Pinned: true, Cols: cols, Rows: rows,
	}, nil
}

// paneIsLive reports whether a tmux session of that exact name still exists.
//
// It exists for the release path in handlePaneResize and answers the narrow
// question "is there still a window here to have a size". A tmux that cannot be
// asked answers NO, which is the safe direction for its one caller: an
// unanswerable tmux turns a forgiven release back into a reported failure, and
// a reported failure is a warning rather than a silently squashed window.
func (d *Daemon) paneIsLive(ctx context.Context, name string) bool {
	live, err := d.livePaneNames(ctx)
	if err != nil {
		return false
	}
	for _, n := range live {
		if n == name {
			return true
		}
	}
	return false
}

// paneBelongsTo reports whether an auxiliary pane name is one of parent's.
//
// Anchored the same way shellIndex is, and for the same reason: "lola-fe-42" is
// a prefix of "lola-fe-420-shell-1", so a bare prefix test would let one session
// close or resize another's tab.
func (d *Daemon) paneBelongsTo(parent, name string) bool {
	if shellIndex(parent, name) > 0 {
		return true
	}
	if devtab.Index(parent, name) > 0 {
		return true
	}
	return name == parent+reviewSuffix
}
