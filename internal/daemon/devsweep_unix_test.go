//go:build unix

package daemon

// The dev take-over's PORT SWEEP: reclaiming a listening port from a process
// that is running inside one of the project's worktrees but belongs to no tmux
// pane. It signals real process groups, so it is unix-only — the same reason
// internal/proctree's kill half is.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/portproc"
	"github.com/sushidev-team/lola/internal/tmux"
)

// strayServer starts a process that behaves like the thing this sweep exists to
// reclaim: its own process group, ignoring the hangup tmux would send, alive
// until something signals its group.
//
// Death is observed by REAPING it, never by probing the pid: an unreaped zombie
// answers a signal probe exactly like a live process, so a kill that worked
// would read as one that did nothing. Reaping in the background also keeps the
// group from lingering as a zombie member, which is what a real squatter (a
// child of pid 1, not of this test) never does.
func strayServer(t *testing.T) (int, <-chan struct{}) {
	t.Helper()
	cmd := exec.Command("sh", "-c", `trap '' HUP; sleep 30`)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start stray server: %v", err)
	}
	pid := cmd.Process.Pid
	gone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(gone)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		select {
		case <-gone:
		case <-time.After(3 * time.Second):
		}
	})
	return pid, gone
}

func waitGone(gone <-chan struct{}) bool {
	select {
	case <-gone:
		return true
	case <-time.After(3 * time.Second):
		return false
	}
}

func stillAlive(gone <-chan struct{}) bool {
	select {
	case <-gone:
		return false
	case <-time.After(200 * time.Millisecond):
		return true
	}
}

// listeners installs the lsof seam with one canned listener.
func listeners(d *Daemon, l portproc.Listener) {
	d.portListeners = func(context.Context) ([]portproc.Listener, error) {
		return []portproc.Listener{l}, nil
	}
}

// The bug: taking the dev tabs over frees the tabs' ports, but not a server the
// SESSION'S AGENT started by hand — Claude Code's Bash tool puts every command
// in its own process group, so a `php artisan serve --port=8000` it launched is
// neither a tmux session nor part of any pane's group. It outlived every
// teardown, kept :8000, and the session taking over silently served :8001 from
// the wrong checkout.
func TestHandleDevReclaimsAPortFromAStrayServerInTheProjectsWorktrees(t *testing.T) {
	d, _ := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"), devSession("lola-app-eng-2"))
	stray, gone := strayServer(t)
	listeners(d, portproc.Listener{
		PID: stray, Command: "php", Port: 8000, Addr: "127.0.0.1:8000",
		Dir: filepath.Join(d.home, "worktrees", "app", "lola-app-eng-2", "public"),
	})

	got, err := d.handleDev(context.Background(), "lola-app-eng-1", true)
	if err != nil {
		t.Fatalf("handleDev: %v", err)
	}
	if !waitGone(gone) {
		t.Fatal("the stray dev server survived the take-over — the new tab would land on :8001")
	}
	// The reclaim is reported, naming the port and the session it was serving:
	// a port that vanishes silently is indistinguishable from one that crashed.
	if !strings.Contains(got.Message, ":8000") || !strings.Contains(got.Message, "lola-app-eng-2") {
		t.Errorf("message = %q, want the reclaimed port and the session it ran in", got.Message)
	}
}

// The rail that keeps this from killing an agent: a process group that owns a
// live tmux pane is never signalled. Every agent's cwd IS its worktree, so
// without it the sweep would take the worker down mid-turn.
func TestDevSweepNeverKillsWhatALiveTmuxPaneOwns(t *testing.T) {
	d, tm := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	stray, gone := strayServer(t)
	tm.paneProcs = []tmux.PaneProc{{Session: "lola-app-eng-1", PID: stray}} // the "agent" is this very process
	listeners(d, portproc.Listener{
		PID: stray, Command: "node", Port: 5173,
		Dir: filepath.Join(d.home, "worktrees", "app", "lola-app-eng-1"),
	})

	if freed := d.sweepPortSquatters(context.Background(), tm, "app", d.home); len(freed) != 0 {
		t.Errorf("swept %v, want nothing — a live pane owns that group", freed)
	}
	if !stillAlive(gone) {
		t.Fatal("the sweep killed a process a live tmux pane owns")
	}
}

// Only ~/.lola/worktrees/<project>/ is ever swept. A dev server the user started
// by hand in the project's own checkout is theirs, not lola's to reclaim.
func TestDevSweepIgnoresListenersOutsideTheProjectsWorktrees(t *testing.T) {
	d, tm := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	stray, gone := strayServer(t)
	for _, dir := range []string{
		"/tmp/app", // the project's own checkout
		filepath.Join(d.home, "worktrees", "app-two"), // a project whose name merely shares a prefix
		filepath.Join(d.home, "worktrees"),            // the root itself
	} {
		listeners(d, portproc.Listener{PID: stray, Command: "php", Port: 8000, Dir: dir})
		if freed := d.sweepPortSquatters(context.Background(), tm, "app", d.home); len(freed) != 0 {
			t.Errorf("swept %v for a listener in %s, want nothing", freed, dir)
		}
	}
	if !stillAlive(gone) {
		t.Fatal("the sweep reached outside the project's worktrees")
	}
}

// Fail CLOSED: the protect set is what separates a stray server from an agent,
// so a tmux that cannot list its panes must cost the sweep, not the agent.
func TestDevSweepKillsNothingWithoutAProtectSet(t *testing.T) {
	d, tm := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	stray, gone := strayServer(t)
	tm.paneErr = errors.New("tmux: no server")
	listeners(d, portproc.Listener{
		PID: stray, Command: "php", Port: 8000,
		Dir: filepath.Join(d.home, "worktrees", "app", "lola-app-eng-1"),
	})

	if freed := d.sweepPortSquatters(context.Background(), tm, "app", d.home); len(freed) != 0 {
		t.Errorf("swept %v, want nothing — the pane list failed", freed)
	}
	if !stillAlive(gone) {
		t.Fatal("the sweep killed a process without being able to tell it from an agent")
	}
}

// No lsof (the nil seam) is the pre-sweep behavior: the take-over still runs.
func TestDevSweepIsSkippedWithoutTheLsofSeam(t *testing.T) {
	d, tm := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	d.portListeners = nil
	if freed := d.sweepPortSquatters(context.Background(), tm, "app", d.home); freed != nil {
		t.Errorf("swept %v, want nothing", freed)
	}
	if _, err := d.handleDev(context.Background(), "lola-app-eng-1", true); err != nil {
		t.Fatalf("handleDev without lsof: %v", err)
	}
}

// THE BUG: a process the HUMAN started inside a "-shell-N" tab lives in a
// process group OF ITS OWN — interactive-shell job control gives every
// foreground command one — so it shares no group with the tab's shell and the
// old protect set (the pane's group plus the tmux server above it) never saw
// it. Pressing Active then read the user's own long-running `opencode` TUI as
// an orphaned dev server and killed it mid-session. A shell tab's DESCENDANT
// tree is therefore protected: ps shows its foreground children below the pane,
// which is exactly the shape a real zsh and the command it runs have.
func TestDevSweepSparesWhatAShellTabPaneLeads(t *testing.T) {
	d, tm := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	stray, gone := strayServer(t)
	// The test process stands in for the tab's shell: the stray below it was
	// "started" by it, in a group of its own.
	tm.paneProcs = []tmux.PaneProc{{Session: "lola-app-eng-1-shell-1", PID: os.Getpid()}}
	listeners(d, portproc.Listener{
		PID: stray, Command: "opencode", Port: 5173,
		Dir: filepath.Join(d.home, "worktrees", "app", "lola-app-eng-1"),
	})

	if freed := d.sweepPortSquatters(context.Background(), tm, "app", d.home); len(freed) != 0 {
		t.Errorf("swept %v, want nothing — a user shell tab owns that group", freed)
	}
	if !stillAlive(gone) {
		t.Fatal("the sweep killed a process the human started in a shell tab")
	}
}

// The boundary that keeps the fix from swallowing the sweep's purpose: an AGENT
// pane gets no descendant walk. A `php artisan serve` the agent started via its
// Bash tool descends from its live pane exactly like the shell case above, and
// evicting precisely that on take-over is why the sweep exists.
func TestDevSweepStillReclaimsWhatAnAgentPaneLeads(t *testing.T) {
	d, tm := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	stray, gone := strayServer(t)
	tm.paneProcs = []tmux.PaneProc{{Session: "lola-app-eng-1", PID: os.Getpid()}}
	listeners(d, portproc.Listener{
		PID: stray, Command: "php", Port: 8000,
		Dir: filepath.Join(d.home, "worktrees", "app", "lola-app-eng-1"),
	})

	freed := d.sweepPortSquatters(context.Background(), tm, "app", d.home)
	if len(freed) == 0 {
		t.Error("swept nothing, want the agent-started server reclaimed")
	}
	if !waitGone(gone) {
		t.Fatal("an agent-started server survived the take-over")
	}
}
