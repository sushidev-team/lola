// Package vtterm is an embedded terminal: it runs a command in a PTY, models
// the command's output with a virtual-terminal emulator (charmbracelet/x/vt),
// and exposes the current screen as styled lines a TUI panel can render. It is
// deliberately UI-framework-agnostic (no bubbletea import) — the tui layer
// converts keystrokes to input bytes and drives repaints off Frames(), the same
// exec-seam discipline the rest of lola uses for tmux/git/gh.
//
// It has a second consumer: internal/panebus runs one `tmux attach` per pane
// through it so the daemon holds a live model of that pane's screen, which is
// what lets a remote subscriber be handed a coherent RESYNC of an
// alternate-screen program instead of a blank pane. That consumer needs two
// things the tui does not — Tap, to see the same bytes the emulator models, and
// WithScreen, to read the screen without racing the reader — and both are
// additive: no existing method's behaviour or locking changed.
//
// Threading: a single reader goroutine pumps PTY output into the emulator (which
// is concurrency-safe) and coalesces a signal onto Frames(); callers Render()
// and Write() from their own goroutine. Everything is bounded and closes
// cleanly so a dead child or a wedged read can never leak a goroutine past
// Close().
package vtterm

import (
	"io"
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

	// tapMu serializes APPLYING output to the emulator with handing the same
	// bytes to the tap, so a screen read under it is exactly the prefix of the
	// stream the tap delivers from then on. Without that pairing a reader that
	// snapshots the screen between emu.Write(b) and tap(b) renders a screen
	// that already contains b and then replays b — and replaying a byte range
	// is not idempotent, so a newline scrolls twice. It is uncontended when no
	// tap is installed (the TUI's case), which is why the existing methods do
	// not take it.
	tapMu sync.Mutex
	tap   func([]byte)

	// cursorVisible mirrors DECTCEM (?25) off the emulator's own callback,
	// because SafeEmulator exposes no accessor for it: Emulator.scr is
	// unexported and isModeSet is private. The callback fires from inside
	// Emulator.Write while SafeEmulator holds its write lock, so the handler
	// may only touch this atomic — calling any emulator method from it
	// deadlocks. Seeded true to match resetModes, which sets ModeTextCursorEnable.
	cursorVisible atomic.Bool
}

// Screen is one coherent reading of the emulator: everything a remote
// subscriber needs in order to paint the pane from scratch. It exists because
// capture-pane cannot answer the two questions that matter for an agent pane —
// where the cursor is and whether it is visible — and because a resync built
// from several separate accessor calls would interleave with the reader and
// describe a screen that never existed. Take one with WithScreen.
//
// Lines is Render()'s output: ANSI SGR preserved, TRAILING BLANK ROWS TRIMMED,
// so len(Lines) may be less than H and a consumer pads the remainder rather
// than assuming a full grid. CursorX/CursorY are cells, zero-based.
type Screen struct {
	W, H          int
	Lines         []string
	CursorX       int
	CursorY       int
	CursorVisible bool
	AltScreen     bool
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
	t.cursorVisible.Store(true)
	// Installed BEFORE the reader goroutine starts, so no output can be modelled
	// while the callback set is half-written; SetCallbacks reaches the embedded
	// Emulator without SafeEmulator's lock, so this is the only safe moment.
	t.installCallbacks()
	go t.readLoop()
	go t.responseLoop()
	return t, nil
}

func (t *Term) readLoop() {
	buf := make([]byte, 32*1024)
	for {
		n, err := t.pty.Read(buf)
		if n > 0 {
			// The write and the tap are ONE step (see tapMu): a subscriber that
			// snapshots the screen in between would replay bytes it can already see.
			t.tapMu.Lock()
			_, _ = t.emu.Write(buf[:n])
			if t.tap != nil {
				t.tap(buf[:n])
			}
			t.tapMu.Unlock()
			t.notify()
		}
		if err != nil {
			t.exited.Store(true)
			t.notify()
			return
		}
	}
}

// responseLoop pumps the emulator's replies to terminal QUERIES (Primary Device
// Attributes, cursor-position reports, mode reports, …) back to the child. This
// is the other half of a real terminal: interactive programs — tmux, vim, and
// the agent — send these queries on startup and BLOCK waiting for the answer, so
// without this they hang with a blank screen. emu.Close() (in Close) unblocks
// the read so this goroutine exits cleanly.
func (t *Term) responseLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := t.emu.Read(buf)
		if n > 0 {
			_, _ = t.pty.Write(buf[:n])
		}
		if err != nil {
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
	// Under tapMu for the same reason readLoop is: Resize rewrites the grid, so
	// a screen read racing it would describe neither the old size nor the new.
	t.tapMu.Lock()
	t.emu.Resize(w, h)
	t.tapMu.Unlock()
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

// installCallbacks wires the emulator's mode callbacks to this Term. Only
// CursorVisibility is taken: its signature is a bare bool, whereas the
// EnableMode/DisableMode callbacks that would also give DECCKM (?1) and
// bracketed paste (?2004) are typed on ansi.Mode, which this module carries as
// an INDIRECT dependency — naming it in a func literal would make it a direct
// one. When a resync frame grows fields for those two modes, adding them here
// is three lines plus that import.
//
// The handler runs inside Emulator.Write while SafeEmulator holds its write
// lock, so it must touch nothing but the atomic.
func (t *Term) installCallbacks() {
	t.emu.SetCallbacks(vt.Callbacks{
		CursorVisibility: func(visible bool) { t.cursorVisible.Store(visible) },
	})
}

// Tap installs fn as the sink for raw PTY output; nil clears it, and there is
// exactly one tap. fn runs ON THE READER GOROUTINE holding tapMu, immediately
// after the same bytes have reached the emulator, which is what makes a screen
// read taken under WithScreen an exact prefix of the tapped stream.
//
// Three rules the caller owns, because breaking any of them wedges the pane:
// p is only valid for the duration of the call (copy what you keep); fn must
// not block, since the reader is stalled meanwhile and a stalled reader stops
// modelling the screen for everybody; and fn must not call back into this Term
// (tapMu is not reentrant).
func (t *Term) Tap(fn func([]byte)) {
	t.tapMu.Lock()
	t.tap = fn
	t.tapMu.Unlock()
}

// WithScreen runs fn on one coherent reading of the screen, taken while the
// reader is held off between output batches. A subscriber registered inside fn
// is guaranteed to receive exactly the bytes FOLLOWING the reading it was
// handed — which is the whole contract a resync frame rests on. fn must be
// short and must not re-enter this Term.
func (t *Term) WithScreen(fn func(Screen)) {
	if fn == nil {
		return
	}
	t.tapMu.Lock()
	defer t.tapMu.Unlock()
	fn(t.screen())
}

// screen builds the reading. Called with tapMu held.
//
// The dimensions come from the EMULATOR and not from Size(). Resize writes
// t.w/t.h under t.mu and only then takes tapMu to reshape the grid, so a
// reading taken in between reported the new size over the old grid — a torn
// snapshot in the one structure whose entire purpose is to be coherent. Under
// tapMu no Resize can be in flight, so the grid, its dimensions, the cursor and
// Render() below all describe the same instant.
func (t *Term) screen() Screen {
	p := t.emu.CursorPosition()
	return Screen{
		W:             t.emu.Width(),
		H:             t.emu.Height(),
		Lines:         t.Render(),
		CursorX:       p.X,
		CursorY:       p.Y,
		CursorVisible: t.cursorVisible.Load(),
		AltScreen:     t.emu.IsAltScreen(),
	}
}

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
	_ = t.closeEmulatorInput() // unblock responseLoop's emu.Read
	err := t.pty.Close()
	// A killed child must be reaped so it doesn't linger as a zombie.
	if t.cmd != nil {
		go func() { _ = t.cmd.Wait() }()
	}
	return err
}

// closeEmulatorInput releases responseLoop's blocked emu.Read.
//
// It closes the emulator's INPUT PIPE rather than calling emu.Close(), and that
// is a fix for a data race rather than a preference. vt.Emulator.Close writes an
// unguarded `closed` bool that vt.Emulator.Read reads on every call, and
// vt.SafeEmulator wraps neither of them — so Close racing this package's own
// responseLoop is a genuine data race inside the dependency, which -race reports
// on every Term teardown. It cannot be locked from outside: Read blocks, so a
// mutex around the pair would deadlock Close against the very read it is trying
// to release. It does not have to be. CloseWithError on the pipe is what
// actually unblocks the read (it is the only thing emu.Close does that matters
// here), io.Pipe is internally synchronized, and the flag this skips only makes
// later Write and Read calls return early — which Close already achieves by
// other means: t.closed gates Write, and closing the PTY stops readLoop.
//
// This matters more than it did when vtterm was the TUI's alone. internal/panebus
// now runs a Term per pane INSIDE the daemon and closes one on every pane
// teardown, so the unsynchronized access sits on the daemon's path, where CI
// does run -race.
func (t *Term) closeEmulatorInput() error {
	if pw, ok := t.emu.InputPipe().(interface{ CloseWithError(error) error }); ok {
		return pw.CloseWithError(io.EOF)
	}
	// A pipe shape this code does not recognize: fall back to the emulator's own
	// Close, which is correct, merely race-visible.
	return t.emu.Close()
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
