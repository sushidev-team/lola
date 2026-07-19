package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// TermService bridges lola's isolated tmux server (tmux -L lola) to the webview.
// It serves the sessions overview two ways, matching the WebGL-context ceiling
// (~16 live GPU contexts per page): read-only *snapshots* for the many grid
// tiles (cheap capture-pane, no tmux client, no resize), and a live interactive
// *PTY attach* for the one focused terminal (streamed as coalesced base64 chunks
// the frontend decodes straight into an xterm WebGL instance).
//
// Snapshots never attach a client, so they can never resize or disturb a running
// agent. Attaching does create a tmux client; we size it to the caller's xterm
// and rely on tmux's window-size=latest so a second client (e.g. the TUI embed)
// doesn't thrash the pane.
type TermService struct {
	app     *application.App
	tmuxBin string

	mu      sync.Mutex
	streams map[string]*ptyStream
}

// NewTermService resolves the tmux binary once (PATH is already augmented in
// main) and returns a ready service. A missing tmux is not fatal here — Capture
// and Attach surface it as an error so the UI can show a health hint.
func NewTermService() *TermService {
	bin, _ := exec.LookPath("tmux")
	return &TermService{tmuxBin: bin, streams: map[string]*ptyStream{}}
}

// SetApp injects the Wails emitter. Called once from main before Run.
func (t *TermService) SetApp(app *application.App) { t.app = app }

func (t *TermService) tmux() (string, error) {
	if t.tmuxBin == "" {
		if bin, err := exec.LookPath("tmux"); err == nil {
			t.tmuxBin = bin
		} else {
			return "", errors.New("tmux not found on PATH")
		}
	}
	return t.tmuxBin, nil
}

// childEnv is the environment every tmux child inherits: a real TERM so the
// remote programs render correctly into xterm.js, UTF-8 locale, plus the
// process env (which already carries the augmented PATH).
func childEnv() []string {
	env := os.Environ()
	env = append(env, "TERM=xterm-256color")
	if os.Getenv("LANG") == "" {
		env = append(env, "LANG=en_US.UTF-8")
	}
	return env
}

// --- snapshots (grid tiles) -------------------------------------------------

// Capture returns a read-only snapshot of a session's tmux pane with ANSI
// escapes preserved (capture-pane -p -e), the last `lines` rows (0 → 200). The
// tmux session name is the lola session ID (SessionInfo.TmuxName). This never
// attaches a client, so it cannot resize or disturb the agent.
func (t *TermService) Capture(name string, lines int) (string, error) {
	bin, err := t.tmux()
	if err != nil {
		return "", err
	}
	if name == "" {
		return "", errors.New("empty session name")
	}
	if lines <= 0 {
		lines = 200
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// "=name:" — exact session match, active pane (see internal/tmux.paneTarget).
	target := "=" + name + ":"
	cmd := exec.CommandContext(ctx, bin, "-L", "lola", "capture-pane", "-p", "-e",
		"-t", target, "-S", "-"+strconv.Itoa(lines))
	cmd.Env = childEnv()
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("capture %s: %w", name, err)
	}
	return string(out), nil
}

// CaptureMany snapshots several panes in one call so the grid refresh is a single
// round-trip from the frontend. A pane that fails to capture (session gone) is
// simply omitted from the map rather than failing the whole batch.
func (t *TermService) CaptureMany(names []string, lines int) map[string]string {
	out := make(map[string]string, len(names))
	for _, n := range names {
		if s, err := t.Capture(n, lines); err == nil {
			out[n] = s
		}
	}
	return out
}

// --- live PTY attach (focused terminal) -------------------------------------

type ptyStream struct {
	f      *os.File
	cmd    *exec.Cmd
	cancel context.CancelFunc

	mu      sync.Mutex
	pending []byte
	closed  bool
}

// Attach starts an interactive `tmux attach` to the named session in a PTY sized
// cols×rows and streams its output to the frontend as base64 chunks on the event
// `pty:<name>`, coalesced to one emit per ~16ms so a chatty agent can't saturate
// the webview. Re-attaching an already-live stream tears the old one down first.
// Returns the stream ID (the session name) the frontend uses for Write/Resize/
// Detach and the event subscription.
func (t *TermService) Attach(name string, cols, rows int) (string, error) {
	bin, err := t.tmux()
	if err != nil {
		return "", err
	}
	if name == "" {
		return "", errors.New("empty session name")
	}
	if t.app == nil {
		return "", errors.New("terminal service not wired to app")
	}
	_ = t.Detach(name) // idempotent re-attach

	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "-L", "lola", "attach-session", "-t", "="+name)
	cmd.Env = childEnv()
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		cancel()
		return "", fmt.Errorf("attach %s: %w", name, err)
	}

	s := &ptyStream{f: f, cmd: cmd, cancel: cancel}
	t.mu.Lock()
	t.streams[name] = s
	t.mu.Unlock()

	go t.readLoop(name, s)
	go t.flushLoop(name, s)
	return name, nil
}

// readLoop pumps PTY bytes into the stream's pending buffer. Buffering raw bytes
// (not a decoded string) keeps multibyte runes intact across read boundaries; the
// flush loop base64-encodes them so the JSON event transport can't corrupt them.
func (t *TermService) readLoop(name string, s *ptyStream) {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.f.Read(buf)
		if n > 0 {
			s.mu.Lock()
			s.pending = append(s.pending, buf[:n]...)
			s.mu.Unlock()
		}
		if err != nil {
			t.endStream(name, s)
			return
		}
	}
}

// flushLoop emits at most one event per ~16ms with everything buffered since the
// last tick — one animation frame's worth of terminal output per pane.
func (t *TermService) flushLoop(name string, s *ptyStream) {
	tick := time.NewTicker(16 * time.Millisecond)
	defer tick.Stop()
	evt := "pty:" + name
	for range tick.C {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		if len(s.pending) == 0 {
			s.mu.Unlock()
			continue
		}
		chunk := s.pending
		s.pending = nil
		s.mu.Unlock()
		t.app.Event.Emit(evt, base64.StdEncoding.EncodeToString(chunk))
	}
}

// Write forwards keystrokes (xterm onData, always valid UTF-8) to the PTY.
func (t *TermService) Write(name, data string) error {
	t.mu.Lock()
	s := t.streams[name]
	t.mu.Unlock()
	if s == nil {
		return errors.New("no such terminal stream")
	}
	_, err := s.f.Write([]byte(data))
	return err
}

// Resize propagates an xterm resize to the PTY so the remote app reflows.
func (t *TermService) Resize(name string, cols, rows int) error {
	t.mu.Lock()
	s := t.streams[name]
	t.mu.Unlock()
	if s == nil {
		return errors.New("no such terminal stream")
	}
	if cols <= 0 || rows <= 0 {
		return nil
	}
	return pty.Setsize(s.f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

// Detach closes the PTY: the tmux client detaches (the agent keeps running,
// untouched) and the stream's goroutines wind down. Idempotent.
func (t *TermService) Detach(name string) error {
	t.mu.Lock()
	s := t.streams[name]
	delete(t.streams, name)
	t.mu.Unlock()
	if s == nil {
		return nil
	}
	t.closeStream(s)
	return nil
}

// endStream is the read-loop's teardown path (PTY EOF/error): drop the registry
// entry and close, matching an explicit Detach.
func (t *TermService) endStream(name string, s *ptyStream) {
	t.mu.Lock()
	if t.streams[name] == s {
		delete(t.streams, name)
	}
	t.mu.Unlock()
	t.closeStream(s)
}

func (t *TermService) closeStream(s *ptyStream) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	_ = s.f.Close()
}
