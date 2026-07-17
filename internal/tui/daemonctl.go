// Daemon lifecycle from the TUI (self-managed mode). The TUI can silently
// start the daemon on open, restart it (re-execing THIS binary, so a rebuilt
// binary brings up the newest daemon — the dev workflow), and stop it — all
// over the same unix socket the other commands use, plus a detached spawn of
// `lola run`. Disabled when [defaults].manage_daemon is false (launchd owns it).
package tui

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	daemonStartTimeout = 10 * time.Second
	daemonStopTimeout  = 10 * time.Second
	daemonPollInterval = 150 * time.Millisecond
)

// daemonAlive reports whether a daemon is currently accepting connections on
// the unix socket. A short dial timeout keeps a wedged socket from blocking the
// UI thread's callers (all of which run inside a tea.Cmd goroutine anyway).
func daemonAlive() bool {
	sock, err := socketPath()
	if err != nil {
		return false
	}
	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// spawnDaemonFn spawns a detached `lola run`. Indirected so tests can inject a
// fake that brings the socket up (or fails) without launching a real daemon.
var spawnDaemonFn = spawnDaemon

// spawnDaemon starts a detached daemon from THIS executable. Re-execing the
// running binary's path means a rebuilt binary (make build overwrites ./lola in
// place) is what comes up on the next start — so a restart always runs the
// newest build. The child gets its own session (Setsid: no controlling
// terminal, survives TUI exit); nil stdio goes to /dev/null, losing nothing
// because the daemon writes its own ~/.lola/daemon.log.
func spawnDaemon() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "run")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// stopDaemon asks the running daemon to shut down over the socket (the same
// path as `lola stop`). A down daemon is not an error.
func stopDaemon() error {
	if !daemonAlive() {
		return nil
	}
	resp, err := requestRaw(`{"cmd":"stop"}`)
	if err != nil {
		return err
	}
	if !resp.OK {
		if resp.Error == "" {
			return errors.New("daemon refused stop")
		}
		return errors.New(resp.Error)
	}
	return nil
}

// waitDaemon blocks until daemonAlive() == want or the timeout elapses.
func waitDaemon(want bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if daemonAlive() == want {
			return nil
		}
		if time.Now().After(deadline) {
			if want {
				return errors.New("daemon did not come up in time")
			}
			return errors.New("daemon did not stop in time")
		}
		time.Sleep(daemonPollInterval)
	}
}

// daemonOpMsg reports the outcome of a lifecycle op back to the UI.
type daemonOpMsg struct {
	op  string // "start" | "stop" | "restart"
	err error
}

// ensureDaemonCmd is the silent auto-start on TUI open: a live daemon (including
// a launchd-managed one) is left untouched; otherwise a detached daemon is
// spawned and we wait briefly for it to accept. Errors surface as a flash.
func ensureDaemonCmd() tea.Msg {
	if daemonAlive() {
		return daemonOpMsg{op: "start"}
	}
	if err := spawnDaemonFn(); err != nil {
		return daemonOpMsg{op: "start", err: err}
	}
	return daemonOpMsg{op: "start", err: waitDaemon(true, daemonStartTimeout)}
}

// stopDaemonCmd stops the daemon and waits for the socket to go quiet.
func stopDaemonCmd() tea.Msg {
	if err := stopDaemon(); err != nil {
		return daemonOpMsg{op: "stop", err: err}
	}
	return daemonOpMsg{op: "stop", err: waitDaemon(false, daemonStopTimeout)}
}

// restartDaemonCmd stops any running daemon, waits for the socket to clear, then
// spawns a fresh one from the current binary — so the newest build comes up. A
// down daemon just starts.
func restartDaemonCmd() tea.Msg {
	if daemonAlive() {
		if err := stopDaemon(); err != nil {
			return daemonOpMsg{op: "restart", err: err}
		}
		if err := waitDaemon(false, daemonStopTimeout); err != nil {
			return daemonOpMsg{op: "restart", err: err}
		}
	}
	if err := spawnDaemonFn(); err != nil {
		return daemonOpMsg{op: "restart", err: err}
	}
	return daemonOpMsg{op: "restart", err: waitDaemon(true, daemonStartTimeout)}
}

// daemonOpPast renders an op name as its success flash verb.
func daemonOpPast(op string) string {
	switch op {
	case "stop":
		return "daemon stopped"
	case "restart":
		return "daemon restarted"
	default:
		return "daemon started"
	}
}
