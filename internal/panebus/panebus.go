// Package panebus multiplexes ONE tmux attach per pane across N subscribers.
//
// A remote client wants to watch a pane, and so does the next one, and neither
// may cost the pane a tmux client of its own: tmux sizes a WINDOW rather than a
// client, so under tmux's default "window-size latest" the most recently active
// client wins and a phone attaching normally would collapse a 200-column agent
// pane to roughly 40 and mangle the agent's TUI on every screen at once. So
// there is exactly one attach per pane name —
//
//	tmux -L lola attach-session -t "=<name>" -f ignore-size
//
// run through internal/vtterm so the daemon also holds a live model of the
// screen, and every subscriber is fanned the same untruncated byte stream and
// pans over it client-side. Two phones cost one tmux client.
//
// What a subscriber receives, in order and always in this order:
//
//  1. ONE resync frame built from the shadow emulator. This is what the
//     emulator is worth here: an agent runs on the ALTERNATE screen, where a
//     fresh tmux attach replays nothing at all, so a subscriber with no prelude
//     stares at a blank pane until the agent next repaints. capture-pane is
//     strictly weaker — it carries no cursor position, and the cursor is
//     precisely what a human needs in order to answer a prompt. A raw byte ring
//     is weaker still: a resumed ring can begin mid-escape-sequence, whereas a
//     rendered frame is parseable from its first byte.
//  2. Raw PTY bytes, coalesced on a short tick so an IDLE PANE COSTS ZERO
//     FRAMES, mirroring desktop/termsvc.go's flushLoop.
//
// and a fresh resync whenever the tmux window changes size or the subscriber
// fell far enough behind that bytes had to be dropped.
//
// Fail closed, everywhere: an unresolvable or malformed pane name is refused
// BEFORE any exec, a window whose size cannot be probed is refused rather than
// guessed at, a later probe that will not answer keeps the last known size and
// changes nothing, and an unattached pane accepts neither a write nor a scroll.
//
// Every external call is a seam (the func fields on Registry), so tests never
// touch tmux, a PTY or a child process; see Fake and FakePane.
package panebus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/tmux"
	"github.com/sushidev-team/lola/internal/vtterm"
)

// Screen is one coherent reading of a pane's shadow emulator. Aliased rather
// than redeclared so a consumer building a wire frame out of it never has to
// import vtterm.
type Screen = vtterm.Screen

// FrameKind discriminates what a Frame carries. The zero value is the resync,
// because a subscription always opens with one.
type FrameKind int

const (
	// KindResync carries Screen: repaint everything from it and discard whatever
	// was on screen before. Sent on subscribe, on a window resize, and after a
	// drop, and it is the only frame that can be rendered without having seen
	// any earlier one.
	KindResync FrameKind = iota
	// KindOutput carries Data: raw pane bytes to feed straight to an emulator.
	KindOutput
	// KindExit is terminal — the pane's child has ended. It is the last frame on
	// a stream and is what distinguishes a pane DEATH from an unsubscribe, which
	// closes the channel with no exit frame at all.
	KindExit
)

func (k FrameKind) String() string {
	switch k {
	case KindResync:
		return "resync"
	case KindOutput:
		return "output"
	case KindExit:
		return "exit"
	}
	return "unknown"
}

// Frame is one unit of a pane stream.
//
// Data is IMMUTABLE and SHARED by every subscriber of the pane — the bus copies
// the reader's buffer exactly once per flush and hands the same slice out N
// times, which is what keeps the reader goroutine from allocating per
// subscriber. A consumer must never write into it.
//
// Seq is assigned per PANE, not per subscriber, and increments across every
// frame the bus produces. That is deliberate: a subscriber that fell behind has
// frames dropped, so a per-subscriber counter would renumber the stream and
// hide exactly the gap the counter exists to expose. A gap is always followed
// by a resync.
type Frame struct {
	Kind   FrameKind
	Data   []byte
	Screen *Screen
	Seq    uint64
}

// WinSize is one reading of the tmux WINDOW behind a pane, and it is the whole
// input to the attach PTY's size.
//
// The status line is a load-bearing off-by-one. tmux reports window_height
// WITHOUT its status line, so a 100x30 PTY yields window 100x29 with status on,
// 100x30 with status off and 100x28 with status=2. Sizing the PTY to
// window_height would therefore make tmux redraw into H-1 rows and put the
// status bar on the emulator's bottom row — visible as a screen that is one row
// short and one row wrong. Hence PTY() adds the status lines back.
//
// Bigger mirrors #{window_bigger}: non-zero means the window is larger than
// this client's terminal, so the stream is a panned viewport rather than the
// grid. It is never acted on — it is logged, because it is the corruption mode.
type WinSize struct {
	Cols        int
	Rows        int
	StatusLines int
	Bigger      bool
}

// PTY is the size the attach PTY must have for tmux to draw the whole window
// into it.
func (w WinSize) PTY() (cols, rows int) { return w.Cols, w.Rows + w.StatusLines }

// Valid reports whether the reading is usable at all. A zero dimension means
// the probe answered but said nothing, which is refused rather than floored:
// attaching at 1x1 would reflow the agent's TUI for every client.
func (w WinSize) Valid() bool { return w.Cols > 0 && w.Rows > 0 }

// Pane is the shadow terminal behind one attach: an emulator fed by a PTY
// master. *vtterm.Term satisfies it; FakePane is the test double, which is why
// this is an interface rather than a *vtterm.Term.
type Pane interface {
	Tap(func([]byte))
	WithScreen(func(vtterm.Screen))
	Write([]byte)
	Resize(w, h int)
	Frames() <-chan struct{}
	Exited() bool
	Close() error
}

// Errors a caller can branch on. Everything else is wrapped context.
var (
	// ErrBadPaneName means the name failed the anchored shape gate. No exec ran.
	ErrBadPaneName = errors.New("panebus: pane name refused")
	// ErrUnknownPane means the identity gate (Resolve) refused the name. No exec
	// ran either — Resolve is asked before anything is spawned.
	ErrUnknownPane = errors.New("panebus: unknown pane")
	// ErrNotAttached means a write or a scroll named a pane nobody is streaming.
	// It is not an error worth retrying: input reaches a pane through its own
	// subscription or not at all.
	ErrNotAttached = errors.New("panebus: pane not attached")
	// ErrClosed means the registry has been shut down.
	ErrClosed = errors.New("panebus: registry closed")
	// ErrNoSize means the window size could not be read, so the attach was
	// refused rather than sized by guess.
	ErrNoSize = errors.New("panebus: window size unavailable")
)

// MaxPaneNameLen bounds a name before it reaches a tmux argv. The longest name
// lola itself builds is nowhere near it; the bound exists because the string
// arrives from a remote peer.
const MaxPaneNameLen = 128

// paneNameRe is the SHAPE gate: what a lola tmux session name may look like,
// anchored at both ends.
//
// It is defence in depth rather than the authorization — Registry.Resolve is
// the identity gate and the session store is the authority — but it is the half
// that has to be here, because this package owns the argv. It refuses a name
// beginning with "-" that tmux would read as a flag, refuses glob characters
// (the "=" exact-match target prefix stops globbing and stops nothing else),
// and refuses everything outside the charset lola's own id builders can
// produce: sessionPrefix "lola-", a project name, an identifier, and
// runtime.manualSlugRe collapsing anything outside [a-z0-9._-].
//
// The charset includes UPPER CASE because a session id is only partly
// lower-cased. runtime.SessionID lowercases the IDENTIFIER and interpolates
// config.Project.Name verbatim, and a project name is not required to be a
// slug: CLAUDE.md pins the opposite, that pre-"label" configs hold names like
// "Okane" and must keep loading. A lower-only charset therefore refused
// "lola-Okane-eng-42" outright, and because a refused subscribe deliberately
// answers the same CodeUnknownPane as a pane the device may not see — so a
// refusal cannot enumerate sessions — the operator got a terminal that never
// worked and no symptom to debug. Nothing about argv safety rests on case: the
// mandatory "lola-" prefix still makes a leading "-" unreachable, the glob
// metacharacters are still excluded, and Registry.Resolve is still the gate
// that decides which of these names actually exists.
//
// The optional suffix group is redundant for matching (the charset already
// covers it) and is written out anyway, because it is the vocabulary of
// auxiliary sessions — shell tabs, the review pane, dev tabs — and a reader
// changing this pattern needs to see it.
var paneNameRe = regexp.MustCompile(`^lola-[A-Za-z0-9._-]+(?:-shell-\d+|-review|-dev-\d+)?$`)

// ValidName reports whether name passes the shape gate. Exported because the
// layer above refuses a bad name with its own error code before it ever gets
// here, and duplicating the pattern there is how the two drift apart.
func ValidName(name string) bool {
	if name == "" || len(name) > MaxPaneNameLen {
		return false
	}
	return paneNameRe.MatchString(name)
}

// clipName bounds an untrusted name before it is interpolated into an error.
//
// It exists because the length check lives INSIDE ValidName, so the very names
// this package rejects are the ones with no bound on them: a pane name arrives
// from a remote peer capped only by the frame size (a megabyte), and the caller
// above logs the returned error. The daemon's log is append-only, unrotated and
// mirrored to stderr, so an authenticated peer could otherwise drive a megabyte
// per frame into a file that grows without bound. Only the ErrBadPaneName paths
// need this — everything past ValidName is already under MaxPaneNameLen.
func clipName(name string) string {
	if len(name) <= MaxPaneNameLen {
		return name
	}
	return name[:MaxPaneNameLen] + "..."
}

// Tunables. They are fields on Registry rather than constants so a test can
// make the timing deterministic without sleeping through it.
const (
	// DefaultFlushInterval is one animation frame: the coalescing window for
	// pane output, matching desktop/termsvc.go's flushLoop. A tick with nothing
	// buffered produces NO frame, so an idle pane costs nothing.
	DefaultFlushInterval = 16 * time.Millisecond
	// DefaultSizeInterval is how often the tmux window is re-measured while at
	// least one subscriber is attached, and never otherwise. Polling is the only
	// mechanism available: panebus owns the PTY MASTER and the tmux client is
	// the slave, so size flows master to slave only and no SIGWINCH ever arrives.
	DefaultSizeInterval = time.Second
	// DefaultLinger is how long a pane's attach outlives its last subscriber, so
	// a phone reconnecting after a network blip does not cost a tmux client
	// churn. Zero tears down synchronously.
	DefaultLinger = 5 * time.Second
	// DefaultExecTimeout bounds every tmux exec, so a wedged external process
	// can never hang a shutdown.
	DefaultExecTimeout = 2 * time.Second
	// subBuffer is one subscriber's queue depth. It is a queue and not a ring:
	// a ring of raw bytes hands a waking phone a stream beginning mid-escape-
	// sequence, which is the failure the resync frame exists to remove. On
	// overflow the frame is dropped, the subscriber is marked desynced, and the
	// next flush repairs it with a fresh resync.
	subBuffer = 256
)

// Registry owns one bus per pane name. Everything external is a func field;
// NewRegistry fills them with the real tmux and vtterm calls, and a test
// installs Fake instead.
type Registry struct {
	// Resolve is the IDENTITY gate and it is asked BEFORE anything is spawned.
	// This package deliberately cannot answer it: the session store is the
	// authority on which names exist and which of them a caller may see, and
	// that store lives in the daemon. A nil Resolve refuses every name, because
	// a missing gate must not read as an open one.
	Resolve func(ctx context.Context, name string) error

	// Attach opens the shadow terminal for a pane at the given PTY size.
	//
	// ctx bounds the OPENING and nothing else. An implementation must never make
	// it the child's lifetime — exec.CommandContext kills the process when the
	// context is done, so a per-exec deadline around the attach kills the tmux
	// client the instant the deadline is cancelled and the pane reads as having
	// exited a few milliseconds after it was opened. The child's lifetime
	// belongs to Pane.Close.
	Attach func(ctx context.Context, name string, cols, rows int) (Pane, error)

	// WinSize measures the tmux window behind a pane. Called once before the
	// attach and once per size tick after it.
	WinSize func(ctx context.Context, name string) (WinSize, error)

	// ScrollPane delegates to tmux.ScrollPane and reports WHICH history moved. The
	// decision between the program's own transcript and tmux copy mode is
	// server-side by design and a remote client must never re-derive it: copy
	// mode on an alternate-screen agent pane reads "[0/0]" and moves nothing.
	ScrollPane func(ctx context.Context, name string, lines int) (tmux.ScrollTransport, error)

	// LeaveCopyMode cancels any mode on the pane. A no-op on a pane that has
	// none, so callers do not have to ask first.
	LeaveCopyMode func(ctx context.Context, name string) error

	// Logf receives one line per notable event. It never receives pane bytes or
	// a resync frame, at any level.
	Logf func(format string, args ...any)

	// FlushInterval is the output coalescing window (DefaultFlushInterval when
	// zero), SizeInterval the window-size poll period (DefaultSizeInterval when
	// zero) and ExecTimeout the per-exec deadline (DefaultExecTimeout when
	// zero). They are fields rather than constants so a test can be
	// deterministic without sleeping through the real cadence.
	FlushInterval time.Duration
	SizeInterval  time.Duration
	ExecTimeout   time.Duration

	// Linger is how long a pane's attach outlives its last subscriber.
	// NewRegistry sets DefaultLinger; a ZERO value tears the attach down
	// synchronously inside the last Unsubscribe, which is what a hand-built
	// Registry (a test) wants — a lingering attach makes teardown a race.
	Linger time.Duration

	mu     sync.Mutex
	buses  map[string]*bus
	closed bool
}

// NewRegistry returns a Registry whose seams run the real tmux and vtterm.
// Resolve is deliberately left nil: the caller owns identity and must install
// it, and until it does every Subscribe is refused.
func NewRegistry(c *tmux.Client) *Registry {
	r := &Registry{buses: map[string]*bus{}, Linger: DefaultLinger}
	r.Attach = func(ctx context.Context, name string, cols, rows int) (Pane, error) {
		argv := attachArgv(c, name)
		// exec.Command, deliberately NOT exec.CommandContext: ctx is the caller's
		// per-exec deadline, and binding a long-lived tmux client to it kills the
		// attach as soon as that deadline is cancelled. vtterm.Term.Close owns the
		// child, kills it and reaps it. pty.Start does not block, so there is
		// nothing here for a deadline to rescue anyway.
		_ = ctx
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Env = attachEnv()
		return vtterm.New(cmd, cols, rows)
	}
	r.WinSize = func(ctx context.Context, name string) (WinSize, error) { return probeWinSize(ctx, c, name) }
	r.ScrollPane = func(ctx context.Context, name string, lines int) (tmux.ScrollTransport, error) {
		return c.ScrollPane(ctx, name, lines)
	}
	r.LeaveCopyMode = func(ctx context.Context, name string) error { return c.LeaveCopyMode(ctx, name) }
	return r
}

func (r *Registry) flushInterval() time.Duration {
	if r.FlushInterval > 0 {
		return r.FlushInterval
	}
	return DefaultFlushInterval
}

func (r *Registry) sizeInterval() time.Duration {
	if r.SizeInterval > 0 {
		return r.SizeInterval
	}
	return DefaultSizeInterval
}

func (r *Registry) execTimeout() time.Duration {
	if r.ExecTimeout > 0 {
		return r.ExecTimeout
	}
	return DefaultExecTimeout
}

func (r *Registry) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

// Subscribe opens a stream on a pane, attaching it if nobody else is watching.
// The two gates run first and in this order — shape, then identity — and BOTH
// run before any process is spawned, so a refused name costs zero execs.
//
// The returned Sub's first frame is always a resync taken at the instant of
// registration, and every byte after it is the stream that followed that
// reading.
func (r *Registry) Subscribe(ctx context.Context, name string) (*Sub, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("%w: %q", ErrBadPaneName, clipName(name))
	}
	if r.Resolve == nil {
		return nil, fmt.Errorf("%w: %q (no resolver installed)", ErrUnknownPane, name)
	}
	if err := r.Resolve(ctx, name); err != nil {
		return nil, fmt.Errorf("%w: %q: %w", ErrUnknownPane, name, err)
	}
	// Two attempts, because a bus found in the map may be torn down by its
	// linger timer between the lookup and the registration.
	//
	// The retry only works because it EVICTS the closed bus itself. A bus
	// removes itself from the registry at the very end of its teardown, after
	// waiting for its own goroutines — which can include an in-flight size
	// probe bounded by ExecTimeout — so for up to a couple of seconds after
	// every linger expiry the map still holds a closed bus whose attach flag is
	// still set. Without the drop below, both attempts found that same bus,
	// ensureAttached did nothing, and subscribe returned ErrClosed twice: a
	// window in which a pane was simply unsubscribable, which is exactly where
	// a phone reconnecting around the linger boundary lands.
	for attempt := 0; attempt < 2; attempt++ {
		b, err := r.bus(ctx, name)
		if err != nil {
			return nil, err
		}
		s, err := b.subscribe()
		if err == nil {
			return s, nil
		}
		if !errors.Is(err, ErrClosed) {
			return nil, err
		}
		// Identity-checked, so this can never evict a successor another
		// subscribe has already registered under the same name.
		r.drop(name, b)
	}
	return nil, fmt.Errorf("%w: %q", ErrClosed, name)
}

// bus returns the pane's bus, creating and attaching it if needed. The attach
// exec happens OUTSIDE r.mu, so a slow or wedged tmux on one pane cannot block
// a subscribe on another.
func (r *Registry) bus(ctx context.Context, name string) (*bus, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, ErrClosed
	}
	b := r.buses[name]
	if b == nil {
		b = newBus(r, name)
		r.buses[name] = b
	}
	r.mu.Unlock()

	if err := b.ensureAttached(ctx); err != nil {
		r.drop(name, b)
		return nil, err
	}
	return b, nil
}

// drop removes a bus from the map, but only if it is still the one registered:
// a failed attach must never evict the successor a concurrent subscribe made.
func (r *Registry) drop(name string, b *bus) {
	r.mu.Lock()
	if r.buses[name] == b {
		delete(r.buses, name)
	}
	r.mu.Unlock()
}

// Write sends raw bytes to a pane's PTY master. It is the human-in-the-loop
// path and it deliberately bypasses lola's AtPrompt idle gate, which exists to
// stop lola's own automation typing into an agent mid-turn — not a person.
//
// The first keystroke after a scroll cancels copy mode first, and scrollMu is
// held ACROSS that cancel and the write rather than just across the flag. That
// is the bug this mirrors from desktop/termsvc.go: with the lock released in
// between, a concurrent Scroll can slip copy mode in after the cancel, leaving
// the pane in a mode nothing cancels and swallowing every later keystroke.
//
// It takes no context on purpose: the write itself goes to an already-open file
// descriptor on the hot path of a keystroke, and the one exec it may make — the
// copy-mode cancel — carries ExecTimeout of its own.
func (r *Registry) Write(name string, p []byte) error {
	b, err := r.attached(name)
	if err != nil {
		return err
	}
	if len(p) == 0 {
		return nil
	}
	b.scrollMu.Lock()
	defer b.scrollMu.Unlock()
	if b.scrolled {
		b.scrolled = false
		r.leaveCopyMode(name)
	}
	term := b.pane()
	if term == nil {
		return fmt.Errorf("%w: %q", ErrNotAttached, name)
	}
	term.Write(p)
	return nil
}

// Scroll moves a pane's view through its history: lines > 0 scrolls BACK,
// lines < 0 forward, zero is a no-op. All it records is which history moved,
// because only copy mode leaves a mode behind for the next keystroke to cancel.
func (r *Registry) Scroll(ctx context.Context, name string, lines int) error {
	b, err := r.attached(name)
	if err != nil {
		return err
	}
	if lines == 0 {
		return nil
	}
	if r.ScrollPane == nil {
		return fmt.Errorf("panebus: scroll %q: no scroll seam", name)
	}
	// Held across the exec, so a keystroke can never consume the flag before the
	// pane is actually in copy mode.
	b.scrollMu.Lock()
	defer b.scrollMu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, r.execTimeout())
	defer cancel()
	how, err := r.ScrollPane(cctx, name, lines)
	b.scrolled = how == tmux.ScrollCopyMode
	if err != nil {
		return fmt.Errorf("panebus: scroll %q: %w", name, err)
	}
	return nil
}

// attached resolves a pane that is already being streamed. It re-checks the
// shape gate — cheap, and the name reached this call from the same untrusted
// place a subscribe's did.
func (r *Registry) attached(name string) (*bus, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("%w: %q", ErrBadPaneName, clipName(name))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, ErrClosed
	}
	b := r.buses[name]
	if b == nil || !b.ready() {
		return nil, fmt.Errorf("%w: %q", ErrNotAttached, name)
	}
	return b, nil
}

func (r *Registry) leaveCopyMode(name string) {
	if r.LeaveCopyMode == nil {
		return
	}
	cctx, cancel := context.WithTimeout(context.Background(), r.execTimeout())
	defer cancel()
	// Best effort: a failure here costs a keystroke's worth of confusion, never
	// the keystroke itself, so the caller writes regardless.
	if err := r.LeaveCopyMode(cctx, name); err != nil {
		r.logf("panebus: leave copy mode %s: %v", name, err)
	}
}

// Close tears down every pane: each bus closes its subscribers, kills its tmux
// attach child and releases its PTY, and every goroutine this package started
// has returned by the time Close does. Idempotent.
func (r *Registry) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	buses := make([]*bus, 0, len(r.buses))
	for _, b := range r.buses {
		buses = append(buses, b)
	}
	r.buses = map[string]*bus{}
	r.mu.Unlock()
	for _, b := range buses {
		b.shutdown("registry closed", false)
	}
	// Waited AFTER every shutdown has been started, so N panes tear down
	// concurrently rather than one after the other. When Close returns, every
	// goroutine this package started has exited, every tmux attach child has
	// been killed and reaped, and every PTY fd is closed.
	for _, b := range buses {
		b.wait()
	}
	return nil
}

// Panes lists the pane names currently attached. For diagnostics only.
func (r *Registry) Panes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.buses))
	for n := range r.buses {
		out = append(out, n)
	}
	return out
}

// --- the real tmux seams ----------------------------------------------------

// attachArgv builds the attach command line. It STARTS from
// tmux.Client.AttachArgs so the binary and the "-L <socket>" server selection
// can never drift from every other tmux call lola makes, and splices
// "-f ignore-size" in ahead of the target.
//
// The flag is the belt: its man page says of such a client that "the client
// does not affect the size of other clients", so a phone cannot shrink the
// window a desktop is drawing into. It is only half the protection — measured
// on tmux 3.7c, an ignore-size client that is the ONLY client still sizes the
// window, because there are then no other clients to not-affect. Sizing the PTY
// to the window (see WinSize) is the braces, and it is the half that holds in
// every case.
//
// The splice assumes AttachArgs ends in "-t", "=<name>"; argvShapeOK pins that.
func attachArgv(c *tmux.Client, name string) []string {
	base := c.AttachArgs(name)
	if !argvShapeOK(base) {
		// Shape changed under us: fall back to the un-spliced argv rather than
		// building a corrupt command line. The window is then no longer protected
		// by the flag, only by the PTY sizing, which is the stronger half anyway.
		return base
	}
	out := make([]string, 0, len(base)+2)
	out = append(out, base[:len(base)-2]...)
	out = append(out, "-f", "ignore-size")
	out = append(out, base[len(base)-2:]...)
	return out
}

// argvShapeOK is the assumption attachArgv and serverPrefix make about
// tmux.Client.AttachArgs, written down once: [bin, "-L", socket,
// "attach-session", "-t", "=<name>"]. A test pins it against the real client so
// a change over there fails here loudly instead of producing a subtly wrong
// command line.
func argvShapeOK(argv []string) bool {
	return len(argv) == 6 && argv[1] == "-L" && argv[3] == "attach-session" && argv[4] == "-t"
}

// serverPrefix is [bin, "-L", socket] — the server selection every other tmux
// exec in this file inherits, taken from the client rather than re-derived, so
// a probe can never address a different tmux server than the attach.
func serverPrefix(c *tmux.Client) []string {
	base := c.AttachArgs("probe")
	if !argvShapeOK(base) {
		return base[:min(3, len(base))]
	}
	return base[:3]
}

// attachEnv gives the tmux client a real TERM and a UTF-8 locale, so the
// program in the pane renders into the shadow emulator the same way it renders
// for the desktop.
func attachEnv() []string {
	env := os.Environ()
	env = append(env, "TERM=xterm-256color")
	if os.Getenv("LANG") == "" {
		env = append(env, "LANG=en_US.UTF-8")
	}
	return env
}

// probeWinSize asks tmux for the window geometry and the status-line height in
// two bounded execs. Both are needed and neither is guessable: window_height
// excludes the status line (see WinSize), and "status" is a global option whose
// value may be off, on, or a row count.
func probeWinSize(ctx context.Context, c *tmux.Client, name string) (WinSize, error) {
	pre := serverPrefix(c)
	out, err := runTmux(ctx, pre, "display-message", "-p", "-t", "="+name+":",
		"#{window_width} #{window_height} #{window_bigger}")
	if err != nil {
		return WinSize{}, fmt.Errorf("panebus: window size %q: %w", name, err)
	}
	// Two fields, not three: tmux renders #{window_bigger} as the EMPTY string
	// when the window is not bigger than the client, rather than as "0"
	// (verified on tmux 3.7c), so the flag simply vanishes from the reply. Only
	// the geometry is required; an absent flag means false.
	f := strings.Fields(strings.TrimSpace(out))
	if len(f) < 2 {
		return WinSize{}, fmt.Errorf("panebus: window size %q: unparseable reply", name)
	}
	w, _ := strconv.Atoi(f[0])
	h, _ := strconv.Atoi(f[1])
	bigger := false
	if len(f) > 2 {
		n, err := strconv.Atoi(f[2])
		bigger = err == nil && n != 0
	}
	ws := WinSize{Cols: w, Rows: h, Bigger: bigger}
	if !ws.Valid() {
		return WinSize{}, fmt.Errorf("panebus: window size %q: reported %dx%d", name, w, h)
	}
	st, err := runTmux(ctx, pre, "show-options", "-gv", "status")
	if err != nil {
		// The geometry is the load-bearing half and it answered. tmux's own
		// default is one status row, so assume that rather than losing the read.
		ws.StatusLines = 1
		return ws, nil
	}
	ws.StatusLines = statusLines(strings.TrimSpace(st))
	return ws, nil
}

// statusLines maps tmux's "status" option to the number of rows it occupies:
// "off" is none, "on" is one, and 2..5 are themselves. An unrecognized value
// falls back to tmux's own default of one row rather than to zero, because
// under-sizing the PTY is the damaging direction — tmux then redraws into H-1
// rows and puts the status bar on the emulator's bottom row.
func statusLines(v string) int {
	switch v {
	case "off":
		return 0
	case "on":
		return 1
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 5 {
		return n
	}
	return 1
}

func runTmux(ctx context.Context, prefix []string, args ...string) (string, error) {
	argv := append(append([]string{}, prefix...), args...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.Output()
	return string(out), err
}
