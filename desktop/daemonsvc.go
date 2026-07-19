package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

// DaemonService is the Wails-bound bridge between the Svelte frontend and the
// lola daemon. Every method is one socket round-trip (internal/protocol) and
// returns the daemon's typed payload, which Wails emits as a TS model. It holds
// no state beyond the resolved lola binary path used for lifecycle control.
//
// Read commands (Sessions/Projects/Status) hit the daemon's in-memory caches and
// are safe to poll frequently; the open* / pollOnce family do real work and use
// the long timeout.
type DaemonService struct{}

// --- health / lifecycle -----------------------------------------------------

// Alive reports whether the daemon socket accepts a connection right now.
func (s *DaemonService) Alive() bool { return daemonAlive() }

// Status returns runtime + Linear health and per-poll state.
func (s *DaemonService) Status() (protocol.StatusData, error) {
	var d protocol.StatusData
	err := call(protocol.Request{Cmd: "status"}, shortTimeout, &d)
	return d, err
}

// --- reads ------------------------------------------------------------------

// Sessions returns the observer's session snapshot plus the activity feed.
func (s *DaemonService) Sessions() (protocol.SessionsData, error) {
	var d protocol.SessionsData
	err := call(protocol.Request{Cmd: "sessions"}, shortTimeout, &d)
	return d, err
}

// Projects returns the configured projects with live rollups.
func (s *DaemonService) Projects() (protocol.ProjectsData, error) {
	var d protocol.ProjectsData
	err := call(protocol.Request{Cmd: "projects"}, shortTimeout, &d)
	return d, err
}

// PRs lists a project's open pull requests (short-TTL cache; refresh bypasses it).
func (s *DaemonService) PRs(project string, refresh bool) (protocol.PrsData, error) {
	args, _ := json.Marshal(protocol.PrsArgs{Project: project, Refresh: refresh})
	var d protocol.PrsData
	err := call(protocol.Request{Cmd: "prs", Args: args}, longTimeout, &d)
	return d, err
}

// Tickets browses a project's Linear issues. scope is "mine" (default) or "team".
func (s *DaemonService) Tickets(project, scope string) (protocol.TicketsData, error) {
	args, _ := json.Marshal(protocol.TicketsArgs{Project: project, Scope: scope})
	var d protocol.TicketsData
	err := call(protocol.Request{Cmd: "tickets", Args: args}, longTimeout, &d)
	return d, err
}

// Pane captures a session's tmux pane and runs the attention parser over it.
// lines bounds the trailing rows captured (0 → the daemon default).
func (s *DaemonService) Pane(session string, lines int) (protocol.PaneData, error) {
	var d protocol.PaneData
	err := call(protocol.Request{Cmd: "pane", Session: session, Lines: lines}, shortTimeout, &d)
	return d, err
}

// --- session actions --------------------------------------------------------

// Answer types a human reply into a session parked at needs_input. The daemon
// refuses it (returned as an error) unless the session is provably idle.
func (s *DaemonService) Answer(session, text string) error {
	return call(protocol.Request{Cmd: "answer", Session: session, Text: text}, shortTimeout, nil)
}

// Kill tears a session down. A dirty worktree is kept unless force is set.
func (s *DaemonService) Kill(session string, force bool) (protocol.KillData, error) {
	var d protocol.KillData
	err := call(protocol.Request{Cmd: "kill", Session: session, Force: force}, shortTimeout, &d)
	return d, err
}

// Revive relaunches a dead-pane session on its surviving worktree.
func (s *DaemonService) Revive(session string) (protocol.ReviveData, error) {
	var d protocol.ReviveData
	err := call(protocol.Request{Cmd: "revive", Session: session}, longTimeout, &d)
	return d, err
}

// Review forces a CodeRabbit QA pass for one session now.
func (s *DaemonService) Review(session string) (protocol.ReviewData, error) {
	var d protocol.ReviewData
	err := call(protocol.Request{Cmd: "review", Session: session}, longTimeout, &d)
	return d, err
}

// CodeRabbit forces the PR-comment watch for one session now.
func (s *DaemonService) CodeRabbit(session string) (protocol.CodeRabbitData, error) {
	var d protocol.CodeRabbitData
	err := call(protocol.Request{Cmd: "coderabbit", Session: session}, longTimeout, &d)
	return d, err
}

// --- launches ---------------------------------------------------------------

// Open checks out a branch or PR of a project into a throwaway shell worktree.
func (s *DaemonService) Open(project, ref string) (protocol.OpenData, error) {
	var d protocol.OpenData
	err := call(protocol.Request{Cmd: "open", Project: project, Ref: ref}, longTimeout, &d)
	return d, err
}

// OpenManual starts a new branch off a base as an agent or a plain shell.
func (s *DaemonService) OpenManual(a protocol.OpenManualArgs) (protocol.OpenData, error) {
	args, _ := json.Marshal(a)
	var d protocol.OpenData
	err := call(protocol.Request{Cmd: "openManual", Args: args}, longTimeout, &d)
	return d, err
}

// OpenPR opens a PR head branch as a tracking worktree + agent (forks refused).
func (s *DaemonService) OpenPR(a protocol.OpenPrArgs) (protocol.OpenData, error) {
	args, _ := json.Marshal(a)
	var d protocol.OpenData
	err := call(protocol.Request{Cmd: "openPr", Args: args}, longTimeout, &d)
	return d, err
}

// OpenTicket starts a Linear issue on demand (deduped like a poll dispatch).
func (s *DaemonService) OpenTicket(a protocol.OpenTicketArgs) (protocol.OpenData, error) {
	args, _ := json.Marshal(a)
	var d protocol.OpenData
	err := call(protocol.Request{Cmd: "openTicket", Args: args}, longTimeout, &d)
	return d, err
}

// OpenURL asks the daemon to open a URL in the default browser.
func (s *DaemonService) OpenURL(url string) error {
	args, _ := json.Marshal(protocol.OpenURLArgs{URL: url})
	return call(protocol.Request{Cmd: "openURL", Args: args}, shortTimeout, nil)
}

// --- config / polling -------------------------------------------------------

// Reload asks the daemon to re-read config.toml and apply it live.
func (s *DaemonService) Reload() error {
	return call(protocol.Request{Cmd: "reload"}, shortTimeout, nil)
}

// Enable turns a poll on. Disable turns it off.
func (s *DaemonService) Enable(poll string) error {
	return call(protocol.Request{Cmd: "enable", Poll: poll}, shortTimeout, nil)
}

func (s *DaemonService) Disable(poll string) error {
	return call(protocol.Request{Cmd: "disable", Poll: poll}, shortTimeout, nil)
}

// PollOnce runs one tick synchronously; dryRun performs zero side effects.
func (s *DaemonService) PollOnce(poll string, dryRun bool) (protocol.PollOnceData, error) {
	var d protocol.PollOnceData
	err := call(protocol.Request{Cmd: "pollOnce", Poll: poll, DryRun: dryRun}, longTimeout, &d)
	return d, err
}

// --- daemon process control -------------------------------------------------

// lolaBinary resolves the lola CLI used to (re)start the daemon: $LOLA_BIN, then
// the first `lola` on PATH. The desktop app is a distinct binary, so it cannot
// re-exec itself the way the TUI does.
func lolaBinary() (string, error) {
	if b := os.Getenv("LOLA_BIN"); b != "" {
		return b, nil
	}
	return exec.LookPath("lola")
}

// StartDaemon spawns a detached `lola run` if the socket is dead, mirroring
// daemonctl.spawnDaemon (own session, released, nil stdio). It waits up to 10s
// for the socket to accept. A live daemon is left untouched.
func (s *DaemonService) StartDaemon() error {
	if daemonAlive() {
		return nil
	}
	bin, err := lolaBinary()
	if err != nil {
		return errors.New("lola binary not found on PATH (set LOLA_BIN)")
	}
	cmd := exec.Command(bin, "run")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	return waitDaemon(true, 10*time.Second)
}

// StopDaemon asks a live daemon to shut down via {"cmd":"stop"} and waits for
// the socket to go quiet. A down daemon is not an error.
func (s *DaemonService) StopDaemon() error {
	if !daemonAlive() {
		return nil
	}
	if err := call(protocol.Request{Cmd: "stop"}, shortTimeout, nil); err != nil {
		return err
	}
	return waitDaemon(false, 10*time.Second)
}

// RestartDaemon stops (if up), waits for the socket to clear, then respawns.
func (s *DaemonService) RestartDaemon() error {
	if err := s.StopDaemon(); err != nil {
		return err
	}
	return s.StartDaemon()
}

// waitDaemon busy-polls until the socket reaches the wanted liveness or timeout.
func waitDaemon(wantAlive bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if daemonAlive() == wantAlive {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	if wantAlive {
		return errors.New("timed out waiting for daemon to start")
	}
	return errors.New("timed out waiting for daemon to stop")
}
