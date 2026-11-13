package panebus

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/tmux"
	"github.com/sushidev-team/lola/internal/vtterm"
)

// This file is the package's test double, shipped beside the code rather than
// in a _test.go file for the same reason internal/linear/fake.go is: the layer
// above (the remote listener, the daemon wiring) has to drive a Registry in its
// own tests, and it must not have to re-invent a tmux stand-in — or worse, run
// against the real one.

// FakePane is an in-memory Pane. It models the one property of a real pane the
// bus depends on: output arrives on a single goroutine, and the tap is called
// with the same bytes the emulator has already seen, under a lock a screen read
// also takes. Emit is that goroutine.
type FakePane struct {
	mu     sync.Mutex
	tapMu  sync.Mutex // stands in for vtterm's, so lock-order bugs still show up
	tap    func([]byte)
	screen vtterm.Screen

	written []byte
	resizes [][2]int
	closed  bool
	exited  bool

	frames chan struct{}
}

// NewFakePane returns a pane whose shadow screen is cols x rows.
func NewFakePane(cols, rows int) *FakePane {
	return &FakePane{
		screen: vtterm.Screen{W: cols, H: rows, CursorVisible: true},
		frames: make(chan struct{}, 1),
	}
}

// Emit delivers output as if the PTY had produced it: the screen is updated
// first, then the tap is called, both under the same lock a screen read takes.
// Applying an EmitFunc lets a test move the modelled screen with the bytes so
// resync coherence can actually be asserted.
func (f *FakePane) Emit(p []byte) {
	f.tapMu.Lock()
	f.mu.Lock()
	f.screen.Lines = append(f.screen.Lines, string(p))
	tap := f.tap
	f.mu.Unlock()
	if tap != nil {
		tap(p)
	}
	f.tapMu.Unlock()
	f.notify()
}

// SetLines replaces what the shadow screen renders, without producing any
// output — a test uses it to make a resync distinguishable from the byte
// stream.
func (f *FakePane) SetLines(lines ...string) {
	f.tapMu.Lock()
	f.mu.Lock()
	f.screen.Lines = append([]string(nil), lines...)
	f.mu.Unlock()
	f.tapMu.Unlock()
}

// SetCursor moves the modelled cursor.
func (f *FakePane) SetCursor(x, y int) {
	f.tapMu.Lock()
	f.mu.Lock()
	f.screen.CursorX, f.screen.CursorY = x, y
	f.mu.Unlock()
	f.tapMu.Unlock()
}

// Exit marks the child as ended and wakes the frame signal, exactly as
// vtterm.readLoop does on EOF.
func (f *FakePane) Exit() {
	f.mu.Lock()
	f.exited = true
	f.mu.Unlock()
	f.notify()
}

// Written is everything Registry.Write has put into this pane.
func (f *FakePane) Written() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.written...)
}

// Resizes is every (cols, rows) the bus has applied, in order.
func (f *FakePane) Resizes() [][2]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][2]int(nil), f.resizes...)
}

// Closed reports whether the bus has torn this pane down.
func (f *FakePane) Closed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *FakePane) notify() {
	select {
	case f.frames <- struct{}{}:
	default:
	}
}

// --- Pane ---

func (f *FakePane) Tap(fn func([]byte)) {
	f.tapMu.Lock()
	f.mu.Lock()
	f.tap = fn
	f.mu.Unlock()
	f.tapMu.Unlock()
}

func (f *FakePane) WithScreen(fn func(vtterm.Screen)) {
	if fn == nil {
		return
	}
	f.tapMu.Lock()
	defer f.tapMu.Unlock()
	f.mu.Lock()
	scr := f.screen
	scr.Lines = append([]string(nil), f.screen.Lines...)
	f.mu.Unlock()
	fn(scr)
}

func (f *FakePane) Write(p []byte) {
	f.mu.Lock()
	f.written = append(f.written, p...)
	f.mu.Unlock()
}

func (f *FakePane) Resize(w, h int) {
	f.tapMu.Lock()
	f.mu.Lock()
	f.resizes = append(f.resizes, [2]int{w, h})
	f.screen.W, f.screen.H = w, h
	f.mu.Unlock()
	f.tapMu.Unlock()
	f.notify()
}

func (f *FakePane) Frames() <-chan struct{} { return f.frames }

func (f *FakePane) Exited() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exited
}

func (f *FakePane) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

// Fake supplies every external seam a Registry has and RECORDS each call, so a
// test can assert not only what happened but that nothing happened — "an
// invalid name is refused before any exec" is only meaningful if the exec count
// is observable.
type Fake struct {
	mu    sync.Mutex
	calls []string
	panes map[string]*FakePane

	// Size is what WinSize reports for every pane; SizeErr overrides it with a
	// failure. Both are read under the lock, so a test can move them mid-stream.
	Size    WinSize
	SizeErr error

	// AttachErr makes the attach fail.
	AttachErr error

	// Names refused by Resolve. A name absent from Known is unresolvable, which
	// is the fail-closed default; an empty Known resolves everything, for tests
	// whose subject is not the gate.
	Known map[string]bool

	// ScrollHow and ScrollErr are what the scroll seam reports.
	ScrollHow tmux.ScrollTransport
	ScrollErr error

	// CopyModeErr makes the copy-mode cancel fail.
	CopyModeErr error
}

// NewFake returns a Fake reporting a 200x50 window with one status row, which
// is a realistic desktop-sized agent pane.
func NewFake() *Fake {
	return &Fake{
		panes: map[string]*FakePane{},
		Size:  WinSize{Cols: 200, Rows: 50, StatusLines: 1},
	}
}

// Install wires every seam on r, including Resolve, and gives r a zero Linger
// so teardown is synchronous and therefore assertable.
func (f *Fake) Install(r *Registry) {
	r.Resolve = f.resolve
	r.Attach = f.attach
	r.WinSize = f.winSize
	r.ScrollPane = f.scroll
	r.LeaveCopyMode = f.leaveCopyMode
	r.Linger = 0
}

// Registry returns a Registry wired to this Fake with a fast flush and size
// cadence, which is what a test wants in every case.
func (f *Fake) Registry() *Registry {
	r := &Registry{
		buses:         map[string]*bus{},
		FlushInterval: time.Millisecond,
		SizeInterval:  time.Millisecond,
		ExecTimeout:   time.Second,
	}
	f.Install(r)
	return r
}

// Pane returns the fake pane attached for name, or nil if nothing attached it.
func (f *Fake) Pane(name string) *FakePane {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.panes[name]
}

// Calls is the ordered list of seam invocations, e.g. "resolve lola-fe-42".
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// CallCount is len(Calls), for the assertions that only care that nothing ran.
func (f *Fake) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// Count reports how many recorded calls were of the given seam.
func (f *Fake) Count(seam string) int {
	n := 0
	for _, c := range f.Calls() {
		if c == seam || (len(c) > len(seam) && c[:len(seam)+1] == seam+" ") {
			n++
		}
	}
	return n
}

func (f *Fake) record(format string, args ...any) {
	f.mu.Lock()
	f.calls = append(f.calls, fmt.Sprintf(format, args...))
	f.mu.Unlock()
}

func (f *Fake) resolve(_ context.Context, name string) error {
	f.record("resolve %s", name)
	f.mu.Lock()
	known := f.Known
	f.mu.Unlock()
	if len(known) == 0 || known[name] {
		return nil
	}
	return fmt.Errorf("no such session")
}

func (f *Fake) attach(_ context.Context, name string, cols, rows int) (Pane, error) {
	f.record("attach %s %dx%d", name, cols, rows)
	f.mu.Lock()
	err := f.AttachErr
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	p := NewFakePane(cols, rows)
	f.mu.Lock()
	f.panes[name] = p
	f.mu.Unlock()
	return p, nil
}

func (f *Fake) winSize(_ context.Context, name string) (WinSize, error) {
	f.record("winsize %s", name)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.SizeErr != nil {
		return WinSize{}, f.SizeErr
	}
	return f.Size, nil
}

// SetSize changes what the window probe reports, which is how a test drives the
// resize path.
func (f *Fake) SetSize(ws WinSize) {
	f.mu.Lock()
	f.Size = ws
	f.mu.Unlock()
}

// SetSizeErr makes the window probe fail from now on.
func (f *Fake) SetSizeErr(err error) {
	f.mu.Lock()
	f.SizeErr = err
	f.mu.Unlock()
}

func (f *Fake) scroll(_ context.Context, name string, lines int) (tmux.ScrollTransport, error) {
	f.record("scroll %s %d", name, lines)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ScrollHow, f.ScrollErr
}

func (f *Fake) leaveCopyMode(_ context.Context, name string) error {
	f.record("leavecopymode %s", name)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.CopyModeErr
}
