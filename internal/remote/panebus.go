package remote

import "context"

// PaneBus is the pane-attach seam: one tmux attach per pane name, fanned out to
// every subscriber. internal/panebus is the production implementation, and the
// interface is declared here rather than imported from there for two reasons.
//
// The narrow one is that this package must not depend on the pane layer's
// concrete types to be testable — the tests below drive a fake and never touch
// tmux or a process. The load-bearing one is that Go channel types are
// INVARIANT: a Frames() <-chan panebus.Frame can never satisfy a Frames()
// <-chan PaneFrame however similar the two structs are, so a shared interface
// would force one package to import the other's frame type. The daemon already
// owns pane-name resolution (only it can check a name against the session store
// and the requesting device's visible set), so the daemon is where the adapter
// belongs, and this seam is what it adapts to.
//
// Write and Scroll match internal/panebus method for method on purpose; only
// Subscribe differs, and only in its return type.
//
// Every method may exec tmux, so every caller here wraps its call in a bounded
// context. A method that cannot answer must return an error rather than a
// zero value: an unresolvable pane is refused, never guessed at.
type PaneBus interface {
	// Subscribe attaches to pane and returns a stream of that pane's frames.
	// cols and rows are the subscriber's own viewport and are ADVISORY — the
	// bus attaches once per pane at the tmux WINDOW size and fans the full
	// untruncated stream out, so a phone pans client-side rather than shrinking
	// the developer's window. They are passed through so the implementation can
	// record them.
	//
	// The returned stream MUST deliver a PaneResync frame first: an agent runs
	// on the alternate screen, where a fresh tmux attach replays nothing, so a
	// subscriber with no prelude stares at a blank pane until the agent next
	// repaints.
	Subscribe(ctx context.Context, pane string, cols, rows int) (PaneStream, error)

	// Write sends raw bytes to the pane's PTY master, cancelling copy mode
	// first when the pane is scrolled back. It takes no context because it is
	// a write to an already-open file descriptor on the hot path of a
	// keystroke; the copy-mode cancel carries its own deadline inside.
	Write(pane string, p []byte) error

	// Scroll moves the pane's own transcript by lines (negative scrolls back).
	// The implementation decides between the program's scrollback and tmux copy
	// mode; a client must never synthesize wheel bytes itself, because getting
	// that choice wrong is not a degraded scroll but no scroll at all.
	Scroll(ctx context.Context, pane string, lines int) error
}

// PaneStream is one subscriber's view of one pane. Frames is closed when the
// stream ends, whether because the pane died, because Close was called, or
// because the bus tore the pane down.
type PaneStream interface {
	// Frames yields the pane's frames in order. The first is always a
	// PaneResync. A closed channel is the stream's terminal state.
	Frames() <-chan PaneFrame

	// Close releases the subscription. It is idempotent and must not block on
	// a reader: the connection layer calls it from its teardown path, which
	// runs before any Wait.
	Close() error
}

// PaneKind discriminates a PaneFrame. The zero value is PaneResync, matching
// internal/panebus, because a subscription always opens with one — and because
// a mis-mapped zero should degrade into a redundant repaint rather than into
// raw bytes rendered as a screen.
type PaneKind int

const (
	// PaneResync carries a coherent rendering of the whole screen in Screen.
	// It is sent first on every subscription and again after any resize or
	// drop, and it is what makes a reconnect paintable: a resumed byte stream
	// can begin mid-escape-sequence, whereas a rendered frame is parseable from
	// its first byte.
	PaneResync PaneKind = iota

	// PaneOutput carries raw PTY bytes in Data, already coalesced by the bus.
	// These bytes are never logged at any level, and Data is immutable and
	// shared with every other subscriber of the pane — never written into.
	PaneOutput

	// PaneExit is terminal: the pane's child has ended. Screen may carry the
	// final rendering. Nothing follows it, and the stream closes after it. It
	// is a distinct kind rather than a closed channel because a DEATH and an
	// unsubscribe must stay distinguishable at the client.
	PaneExit
)

// PaneFrame is one thing that happened on a pane.
//
// Seq is the bus's own per-PANE counter, and it is forwarded to the wire
// VERBATIM when it is set. That matters more than it looks: the bus drops
// frames for a subscriber that fell behind, so renumbering them here would
// renumber the stream and hide exactly the gap the counter exists to expose.
// A bus that leaves Seq zero gets the connection's own counter instead, which
// is correct only because such a bus never drops.
type PaneFrame struct {
	Kind   PaneKind
	Data   []byte      // PaneOutput only
	Screen *PaneScreen // PaneResync, and optionally PaneExit
	Seq    uint64      // the bus's per-pane sequence; 0 means "not numbered"
}

// PaneScreen is a coherent reading of the pane's emulated screen, in the shape
// protocol.ResyncPayload puts on the wire.
//
// Lines are screen rows with ANSI SGR preserved and TRAILING BLANK ROWS
// TRIMMED, so len(Lines) may be less than Rows and a client pads the remainder
// rather than assuming a full grid. Cursor is in cells, zero-based, and is
// precisely the field capture-pane cannot give — it is what a human needs in
// order to answer a prompt.
type PaneScreen struct {
	Cols    int
	Rows    int
	Lines   []string
	CursorX int
	CursorY int

	// CursorVisible is stated positively here and inverted once on the way to
	// the wire (see resyncPayload), because this side mirrors the emulator,
	// which models DECTCEM as "enabled", while the wire mirrors what is cheap
	// and safe to omit.
	CursorVisible bool

	AltScreen bool
}
