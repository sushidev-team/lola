// Package vtterm is an embedded terminal: it runs a command in a PTY, models
// the command's output with a virtual-terminal emulator (charmbracelet/x/vt),
// and exposes the current screen as styled lines a TUI panel can render. It is
// deliberately UI-framework-agnostic (no bubbletea import) — the tui layer
// converts keystrokes to input bytes and drives repaints off Frames(), the same
// exec-seam discipline the rest of lola uses for tmux/git/gh.
//
// Threading: a single reader goroutine pumps PTY output into the emulator (which
// is concurrency-safe) and coalesces a signal onto Frames(); callers Render()
// and Write() from their own goroutine. Everything is bounded and closes
// cleanly so a dead child or a wedged read can never leak a goroutine past
// Close().
package vtterm

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// Term is one embedded terminal — a PTY-backed child process plus the emulator
// that models its screen.
type Term struct {
	pty    *os.File
	cmd    *exec.Cmd
	emu    *vt.SafeEmulator
	frames chan struct{} // coalesced "screen changed" signal

	mu     sync.Mutex // guards w/h
	w, h   int
	closed atomic.Bool
	exited atomic.Bool
}

// New starts cmd in a PTY sized w×h and begins pumping its output into a vt
// emulator. The child inherits cmd.Env / cmd.Dir (set those before calling —
// e.g. Dir = the worktree). Frames() fires as output arrives and once more when
// the child exits.
func New(cmd *exec.Cmd, w, h int) (*Term, error) {
	w, h = clampWH(w, h)
	f, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	_ = pty.Setsize(f, winsize(w, h))
	t := &Term{
		pty:    f,
		cmd:    cmd,
		emu:    vt.NewSafeEmulator(w, h),
		frames: make(chan struct{}, 1),
		w:      w,
		h:      h,
	}
	go t.readLoop()
	return t, nil
}

func (t *Term) readLoop() {
	buf := make([]byte, 32*1024)
	for {
		n, err := t.pty.Read(buf)
		if n > 0 {
			_, _ = t.emu.Write(buf[:n])
			t.notify()
		}
		if err != nil {
			t.exited.Store(true)
			t.notify()
			return
		}
	}
}

// notify signals a frame without blocking: a full buffer already has a pending
// frame, so the newest state will be picked up on the next Render anyway.
func (t *Term) notify() {
	select {
	case t.frames <- struct{}{}:
	default:
	}
}

// Frames is signalled (coalesced) whenever the screen may have changed; the tui
// waits on it to schedule a repaint.
func (t *Term) Frames() <-chan struct{} { return t.frames }

// Write forwards raw input bytes (already encoded keystrokes) to the PTY.
func (t *Term) Write(p []byte) {
	if t.closed.Load() || len(p) == 0 {
		return
	}
	_, _ = t.pty.Write(p)
}

// Resize sets both the PTY window size (so the child re-lays-out) and the
// emulator grid. A zero/negative dimension is floored to 1.
func (t *Term) Resize(w, h int) {
	w, h = clampWH(w, h)
	t.mu.Lock()
	if w == t.w && h == t.h {
		t.mu.Unlock()
		return
	}
	t.w, t.h = w, h
	t.mu.Unlock()
	if !t.closed.Load() {
		_ = pty.Setsize(t.pty, winsize(w, h))
	}
	t.emu.Resize(w, h)
	t.notify()
}

// Render returns the current screen as styled lines (one per row, ANSI SGR
// preserved), trailing blank rows trimmed.
func (t *Term) Render() []string {
	s := strings.TrimRight(t.emu.Render(), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// Size reports the current emulator dimensions.
func (t *Term) Size() (w, h int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.w, t.h
}

// Cursor reports the cursor's column/row within the screen.
func (t *Term) Cursor() (x, y int) {
	p := t.emu.CursorPosition()
	return p.X, p.Y
}

// AltScreen reports whether the child is in the alternate screen (a full-screen
// app like an editor or the agent); useful to suppress scrollback affordances.
func (t *Term) AltScreen() bool { return t.emu.IsAltScreen() }

// Exited reports whether the child process has ended (the PTY hit EOF).
func (t *Term) Exited() bool { return t.exited.Load() }

// Close kills the child (if still running) and releases the PTY. Idempotent.
func (t *Term) Close() error {
	if t.closed.Swap(true) {
		return nil
	}
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	err := t.pty.Close()
	// A killed child must be reaped so it doesn't linger as a zombie.
	if t.cmd != nil {
		go func() { _ = t.cmd.Wait() }()
	}
	return err
}

func clampWH(w, h int) (int, int) {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return w, h
}

func winsize(w, h int) *pty.Winsize {
	return &pty.Winsize{Rows: uint16(h), Cols: uint16(w)}
}
