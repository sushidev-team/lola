package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/tmux"
)

// viewerSession is the name of the aggregate "tab per agent" viewer session.
// Deliberately NOT under the "lola-" agent prefix so the daemon's Adopt scan
// never mistakes it for an orphaned agent session (see tmux.BuildViewer).
const viewerSession = "lola_viewer"

// viewerBuildTimeout bounds the handful of tmux calls that assemble the viewer,
// so a wedged tmux can never hang the CLI before the (unbounded, interactive)
// attach takes over.
const viewerBuildTimeout = 10 * time.Second

// RunAttach hands the terminal to tmux so the user can watch/drive agents live,
// on lola's isolated tmux server. With a session id it attaches straight to that
// one session; with no id it (re)builds a viewer session with one tab per live
// agent and attaches to that — the "attach once, tab through every agent"
// surface. It talks to tmux directly (no daemon needed) so it works even when
// the daemon is down.
func RunAttach(session string) error {
	dir := ""
	if home, err := config.Home(); err == nil {
		dir = home
	}
	// The viewer below CREATES a session, so this client carries the [tmux]
	// scroll defaults too — an unreadable config just leaves lola's own.
	cfg := &config.Config{}
	if path, err := config.DefaultPath(); err == nil {
		if loaded, err := config.Load(path); err == nil {
			cfg = loaded
		}
	}
	c := cfg.TmuxClient("tmux", dir)
	if !c.Available() {
		return fmt.Errorf("tmux is not on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), viewerBuildTimeout)
	defer cancel()

	// Direct single-session attach.
	if session != "" {
		if !c.Has(ctx, session) {
			return fmt.Errorf("no live session %q on the lola tmux server", session)
		}
		return execAttach(c.AttachArgs(session))
	}

	// Aggregate viewer: one tab per live agent session.
	sessions, err := c.ListSessions(ctx)
	if err != nil {
		return err
	}
	var tabs []tmux.ViewerTab
	for _, s := range sessions {
		if s.Name == viewerSession || !strings.HasPrefix(s.Name, tmux.OrphanSessionPrefix) {
			continue // skip the viewer itself and any non-agent session
		}
		tabs = append(tabs, tmux.ViewerTab{Session: s.Name, Name: viewerTabName(s.Name)})
	}
	if len(tabs) == 0 {
		fmt.Println("no live lola sessions to attach to")
		return nil
	}
	if err := c.BuildViewer(ctx, viewerSession, dir, tabs); err != nil {
		return err
	}
	return execAttach(c.AttachArgs(viewerSession))
}

// viewerTabName is the short label for a session's tab: the session ID minus the
// "lola-" prefix (e.g. "lola-nori-app-nor-325" -> "nori-app-nor-325"), which is
// unambiguous without parsing the project/issue split out of the ID.
func viewerTabName(id string) string {
	return strings.TrimPrefix(id, tmux.OrphanSessionPrefix)
}

// execAttach runs `tmux attach` with the terminal fully handed over, blocking
// until the user detaches. Uses the CLI process's own stdio so tmux drives the
// real tty.
func execAttach(argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
