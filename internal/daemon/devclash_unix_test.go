//go:build unix

package daemon

// The kill half of the port-clash path (cmd=devFreePort): the ONE place lola
// signals a process outside its own worktrees, and only ever because a human
// answered a dialog. It signals real process groups, so it is unix-only — the
// same reason the sweep's tests are.

import (
	"context"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/portproc"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

// clashed puts a recorded clash on the session, as the observer's detection pass
// would have, and points the lsof seam at the same holder.
func clashed(d *Daemon, sessionID string, l portproc.Listener) {
	listeners(d, l)
	d.sessions.Update(sessionID, func(s *session.Session) bool {
		s.DevClash = &session.DevClash{
			Tab: sessionID + "-dev-1", Command: "composer dev",
			Port: l.Port, PID: l.PID, Proc: l.Command, Dir: l.Dir,
		}
		return true
	})
}

// The whole point of the feature: the port comes back and the dev tabs start,
// without a human hunting for a pid.
func TestDevFreePortKillsTheHolderAndRestartsTheTabs(t *testing.T) {
	d, tm := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	holder, gone := strayServer(t)
	clashed(d, "lola-app-eng-1", portproc.Listener{
		PID: holder, Command: "node", Port: 9245, Dir: "/Users/someone/code/app/desktop/frontend",
	})

	got, err := d.handleDevFreePort(context.Background(), protocol.DevFreePortArgs{
		Session: "lola-app-eng-1", Port: 9245, PID: holder,
	})
	if err != nil {
		t.Fatalf("handleDevFreePort: %v", err)
	}
	if !waitGone(gone) {
		t.Fatal("the process holding the port survived")
	}
	if !got.Freed || got.Port != 9245 || !got.Dev.Active {
		t.Errorf("data = %+v, want the port freed and the dev tabs running again", got)
	}
	if !strings.Contains(strings.Join(tm.calls(), " "), "start lola-app-eng-1-dev-1") {
		t.Errorf("tmux calls = %v, want the dev tab restarted", tm.calls())
	}
	// The explanation belongs to the tabs that died; the new ones have their own.
	if s, _ := d.sessions.Get("lola-app-eng-1"); s.DevClash != nil {
		t.Errorf("clash %+v survived the fix", s.DevClash)
	}
}

// A client may not name an arbitrary pid: the request has to match the clash the
// daemon itself recorded, or the dialog a human answered was about something
// else.
func TestDevFreePortRefusesARequestThatDoesNotMatchTheRecordedClash(t *testing.T) {
	d, _ := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	holder, gone := strayServer(t)
	clashed(d, "lola-app-eng-1", portproc.Listener{PID: holder, Command: "node", Port: 9245, Dir: "/elsewhere"})

	_, err := d.handleDevFreePort(context.Background(), protocol.DevFreePortArgs{
		Session: "lola-app-eng-1", Port: 9245, PID: holder + 1,
	})
	if err == nil {
		t.Fatal("want a mismatched pid refused")
	}
	if !stillAlive(gone) {
		t.Fatal("a mismatched request killed the recorded holder anyway")
	}
}

// Pids are REUSED, and the gap between detecting a clash and a human clicking is
// unbounded — so the holder is re-checked against the machine as it is now.
func TestDevFreePortRefusesWhenThePortChangedHandsSinceDetection(t *testing.T) {
	d, _ := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	holder, gone := strayServer(t)
	clashed(d, "lola-app-eng-1", portproc.Listener{PID: holder, Command: "node", Port: 9245, Dir: "/elsewhere"})
	// Somebody else holds it now.
	listeners(d, portproc.Listener{PID: holder + 1000, Command: "vite", Port: 9245, Dir: "/elsewhere"})

	_, err := d.handleDevFreePort(context.Background(), protocol.DevFreePortArgs{
		Session: "lola-app-eng-1", Port: 9245, PID: holder,
	})
	if err == nil {
		t.Fatal("want a stale holder refused")
	}
	if !stillAlive(gone) {
		t.Fatal("the process from the stale dialog was killed")
	}
	// The record is dropped rather than left pointing at a pid that moved on.
	if s, _ := d.sessions.Get("lola-app-eng-1"); s.DevClash != nil {
		t.Errorf("stale clash %+v kept", s.DevClash)
	}
}

// The port being free again is an answer, not a failure — but it must not become
// a kill of whatever inherited the pid.
func TestDevFreePortRefusesWhenThePortIsFreeAgain(t *testing.T) {
	d, _ := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	holder, gone := strayServer(t)
	clashed(d, "lola-app-eng-1", portproc.Listener{PID: holder, Command: "node", Port: 9245, Dir: "/elsewhere"})
	d.portListeners = func(context.Context) ([]portproc.Listener, error) { return nil, nil }

	_, err := d.handleDevFreePort(context.Background(), protocol.DevFreePortArgs{
		Session: "lola-app-eng-1", Port: 9245, PID: holder,
	})
	if err == nil {
		t.Fatal("want a request for a port nobody holds refused")
	}
	if !stillAlive(gone) {
		t.Fatal("something was killed for a port that is already free")
	}
}

// The same rail the sweep has: every agent's cwd IS its worktree, so a group a
// live tmux pane owns is never signalled — not even by a human's click.
func TestDevFreePortNeverKillsWhatALiveTmuxPaneOwns(t *testing.T) {
	d, tm := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	holder, gone := strayServer(t)
	tm.panePIDs = []int{holder} // the "agent" is this very process
	clashed(d, "lola-app-eng-1", portproc.Listener{PID: holder, Command: "node", Port: 9245, Dir: "/elsewhere"})

	_, err := d.handleDevFreePort(context.Background(), protocol.DevFreePortArgs{
		Session: "lola-app-eng-1", Port: 9245, PID: holder,
	})
	if err == nil {
		t.Fatal("want a live session's own process refused")
	}
	if !stillAlive(gone) {
		t.Fatal("killed a process a live tmux pane owns")
	}
}

// FAIL CLOSED: without ps or tmux the protect set cannot be built, and a kill
// without it could take down an agent mid-turn.
func TestDevFreePortRefusesWhenItCannotBuildTheProtectSet(t *testing.T) {
	d, tm := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	holder, gone := strayServer(t)
	tm.paneErr = context.DeadlineExceeded
	clashed(d, "lola-app-eng-1", portproc.Listener{PID: holder, Command: "node", Port: 9245, Dir: "/elsewhere"})

	_, err := d.handleDevFreePort(context.Background(), protocol.DevFreePortArgs{
		Session: "lola-app-eng-1", Port: 9245, PID: holder,
	})
	if err == nil {
		t.Fatal("want the kill refused when tmux cannot list its panes")
	}
	if !stillAlive(gone) {
		t.Fatal("killed a process without knowing what it belongs to")
	}
}

// A session with nothing on record has nothing to free — the dialog cannot be
// synthesized client-side.
func TestDevFreePortRefusesWithoutARecordedClash(t *testing.T) {
	d, _ := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	if _, err := d.handleDevFreePort(context.Background(), protocol.DevFreePortArgs{
		Session: "lola-app-eng-1", Port: 9245, PID: 1234,
	}); err == nil {
		t.Fatal("want a request without a recorded clash refused")
	}
}
