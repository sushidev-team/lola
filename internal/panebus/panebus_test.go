package panebus

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/tmux"
)

const pane = "lola-fe-42"

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

// slowRegistry never ticks during a test, so ordering is decided by the code
// under test rather than by a timer. Every assertion about coherence uses it.
func slowRegistry(f *Fake) *Registry {
	r := f.Registry()
	r.FlushInterval = time.Hour
	r.SizeInterval = time.Hour
	return r
}

// next reads one frame with a deadline, so a missing frame fails the test
// instead of hanging it.
func next(t *testing.T, s *Sub) Frame {
	t.Helper()
	select {
	case f, ok := <-s.Frames():
		if !ok {
			t.Fatalf("%s: stream closed while a frame was expected", s.Pane())
		}
		return f
	case <-time.After(3 * time.Second):
		t.Fatalf("%s: no frame within the deadline", s.Pane())
	}
	return Frame{}
}

// drainFor collects every frame that arrives within d.
func drainFor(s *Sub, d time.Duration) []Frame {
	var out []Frame
	deadline := time.After(d)
	for {
		select {
		case f, ok := <-s.Frames():
			if !ok {
				return out
			}
			out = append(out, f)
		case <-deadline:
			return out
		}
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// --- the two gates, both of which run before any process is spawned ----------

func TestValidNameTable(t *testing.T) {
	cases := []struct {
		name string
		want bool
		why  string
	}{
		{"lola-fe-42", true, "a plain agent session"},
		{"lola-fe-42-shell-1", true, "a shell tab"},
		{"lola-fe-42-review", true, "the review pane"},
		{"lola-fe-42-dev-2", true, "a dev tab"},
		{"lola-my.project-eng-412", true, "dots and hyphens are in the id charset"},
		{"", false, "empty"},
		{"fe-42", false, "every lola session carries the lola- prefix"},
		{"-shell-1", false, "a leading hyphen is what tmux would read as a flag"},
		{"lola-fe-42 ; rm -rf /", false, "shell metacharacters"},
		{"lola-fe-*", false, "a glob; the = target prefix stops globbing and stops nothing else"},
		// The id builders lower-case the IDENTIFIER, not the project name, and
		// config.Project.Name is not required to be a slug — CLAUDE.md pins that
		// pre-"label" configs hold names like "Okane" and must keep loading. A
		// lower-only charset made the remote terminal permanently unusable for
		// every such project, and because the refusal is deliberately
		// indistinguishable from "you may not see this pane", with no symptom.
		{"lola-Okane-eng-42", true, "runtime.SessionID interpolates the project name verbatim"},
		{"lola-Okane-eng-42-shell-1", true, "and its auxiliary sessions inherit it"},
		{"lola-fe-42:0.1", false, "a pane target, not a session name"},
		{"lola-fe$(id)", false, "command substitution"},
		{"lola-fe\n42", false, "a newline"},
		{"lola-" + strings.Repeat("x", MaxPaneNameLen), false, "over the length bound"},
	}
	for _, c := range cases {
		if got := ValidName(c.name); got != c.want {
			t.Errorf("ValidName(%q) = %v, want %v (%s)", c.name, got, c.want, c.why)
		}
	}
}

// TestBadNameIsRefusedBeforeAnyExec is the whole point of the shape gate: the
// name reaches a tmux argv, so it must be refused before a process exists.
func TestBadNameIsRefusedBeforeAnyExec(t *testing.T) {
	for _, bad := range []string{"", "-shell-1", "lola-fe-*", "lola-fe-42 ; id", "other-42"} {
		f := NewFake()
		r := slowRegistry(f)
		defer r.Close()
		if _, err := r.Subscribe(ctx(t), bad); !errors.Is(err, ErrBadPaneName) {
			t.Errorf("Subscribe(%q) error = %v, want ErrBadPaneName", bad, err)
		}
		if n := f.CallCount(); n != 0 {
			t.Errorf("Subscribe(%q) ran %d external calls (%v); a refused name must cost none", bad, n, f.Calls())
		}
	}
}

// TestUnresolvableNameSpawnsNothing pins the second gate. The shape is fine, so
// only the session store can refuse it — and it must do so before the attach,
// or a device could enumerate panes it was never shown.
func TestUnresolvableNameSpawnsNothing(t *testing.T) {
	f := NewFake()
	f.Known = map[string]bool{"lola-other-1": true}
	r := slowRegistry(f)
	defer r.Close()

	if _, err := r.Subscribe(ctx(t), pane); !errors.Is(err, ErrUnknownPane) {
		t.Fatalf("Subscribe error = %v, want ErrUnknownPane", err)
	}
	if got := f.Count("winsize") + f.Count("attach"); got != 0 {
		t.Errorf("an unresolvable name ran %d tmux calls: %v", got, f.Calls())
	}
	if got := f.Count("resolve"); got != 1 {
		t.Errorf("resolve called %d times, want exactly 1", got)
	}
}

// TestMissingResolverRefusesEverything: a missing gate must never read as an
// open one.
func TestMissingResolverRefusesEverything(t *testing.T) {
	f := NewFake()
	r := slowRegistry(f)
	r.Resolve = nil
	defer r.Close()

	if _, err := r.Subscribe(ctx(t), pane); !errors.Is(err, ErrUnknownPane) {
		t.Fatalf("Subscribe error = %v, want ErrUnknownPane", err)
	}
	if n := f.CallCount(); n != 0 {
		t.Errorf("ran %d external calls with no resolver installed: %v", n, f.Calls())
	}
}

// --- the stream ordering contract -------------------------------------------

func TestSubscribeOpensWithAResyncThenRawBytes(t *testing.T) {
	f := NewFake()
	r := f.Registry()
	r.FlushInterval = 2 * time.Millisecond
	r.SizeInterval = time.Hour
	defer r.Close()

	f.Pane(pane) // not attached yet
	s, err := r.Subscribe(ctx(t), pane)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer s.Close()

	first := next(t, s)
	if first.Kind != KindResync {
		t.Fatalf("first frame kind = %v, want resync", first.Kind)
	}
	if first.Screen == nil {
		t.Fatal("resync frame carried no screen")
	}
	if first.Screen.W != 200 || first.Screen.H != 51 {
		t.Errorf("resync screen = %dx%d, want 200x51 (window 200x50 plus one status row)",
			first.Screen.W, first.Screen.H)
	}

	f.Pane(pane).Emit([]byte("hello"))
	second := next(t, s)
	if second.Kind != KindOutput || string(second.Data) != "hello" {
		t.Fatalf("second frame = %v %q, want output %q", second.Kind, second.Data, "hello")
	}
	if second.Seq <= first.Seq {
		t.Errorf("seq did not advance: %d then %d", first.Seq, second.Seq)
	}
}

// TestASecondSubscriberIsNeverReplayedWhatItsResyncAlreadyShows is the
// coherence invariant vtterm.WithScreen exists for. If registration and the
// screen reading were not one step, the newcomer would render bytes that are
// already on its screen — and replaying a byte range is not idempotent, so a
// newline scrolls twice.
func TestASecondSubscriberIsNeverReplayedWhatItsResyncAlreadyShows(t *testing.T) {
	f := NewFake()
	r := slowRegistry(f) // the flush tick never fires; subscribe does the flushing
	defer r.Close()

	first, err := r.Subscribe(ctx(t), pane)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer first.Close()
	if k := next(t, first).Kind; k != KindResync {
		t.Fatalf("first subscriber opened with %v", k)
	}

	f.Pane(pane).Emit([]byte("already-on-screen"))

	second, err := r.Subscribe(ctx(t), pane)
	if err != nil {
		t.Fatalf("second Subscribe: %v", err)
	}
	defer second.Close()

	open := next(t, second)
	if open.Kind != KindResync {
		t.Fatalf("second subscriber opened with %v, want resync", open.Kind)
	}
	if !linesContain(open.Screen.Lines, "already-on-screen") {
		t.Fatalf("the resync did not carry the output the pane had already modelled: %q", open.Screen.Lines)
	}
	if extra := drainFor(second, 50*time.Millisecond); len(extra) != 0 {
		t.Errorf("the newcomer was replayed %d frame(s) its resync already showed: %v", len(extra), extra)
	}

	// The EXISTING subscriber does get them, flushed by the registration.
	out := next(t, first)
	if out.Kind != KindOutput || string(out.Data) != "already-on-screen" {
		t.Errorf("existing subscriber got %v %q, want the flushed output", out.Kind, out.Data)
	}
}

// TestIdlePaneCostsZeroFrames: the flush tick must produce nothing when nothing
// was buffered, or a phone pays for an agent parked at its prompt.
func TestIdlePaneCostsZeroFrames(t *testing.T) {
	f := NewFake()
	r := f.Registry() // 1ms flush and size ticks: hundreds of ticks in this window
	defer r.Close()

	s, err := r.Subscribe(ctx(t), pane)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer s.Close()
	if k := next(t, s).Kind; k != KindResync {
		t.Fatalf("opened with %v", k)
	}
	if extra := drainFor(s, 250*time.Millisecond); len(extra) != 0 {
		t.Errorf("an idle pane produced %d frame(s): %v", len(extra), extra)
	}
}

// --- fan-out ----------------------------------------------------------------

func TestOneAttachServesEverySubscriber(t *testing.T) {
	f := NewFake()
	r := f.Registry()
	r.FlushInterval = 2 * time.Millisecond
	r.SizeInterval = time.Hour
	defer r.Close()

	var subs []*Sub
	for i := 0; i < 3; i++ {
		s, err := r.Subscribe(ctx(t), pane)
		if err != nil {
			t.Fatalf("Subscribe %d: %v", i, err)
		}
		defer s.Close()
		if k := next(t, s).Kind; k != KindResync {
			t.Fatalf("subscriber %d opened with %v", i, k)
		}
		subs = append(subs, s)
	}
	if got := f.Count("attach"); got != 1 {
		t.Errorf("attach ran %d times for 3 subscribers, want 1 — two phones cost one tmux client", got)
	}

	f.Pane(pane).Emit([]byte("fanned"))
	var shared []byte
	for i, s := range subs {
		fr := next(t, s)
		if fr.Kind != KindOutput || string(fr.Data) != "fanned" {
			t.Fatalf("subscriber %d got %v %q", i, fr.Kind, fr.Data)
		}
		if i == 0 {
			shared = fr.Data
		} else if &shared[0] != &fr.Data[0] {
			t.Error("subscribers got separate copies; the reader is meant to copy the batch exactly once")
		}
	}
}

// flood emits until sub's queue overruns, then reports how long the emitting
// goroutine was ever blocked. A blocked emitter is a stalled reader goroutine,
// which is the failure this whole design exists to avoid.
func flood(t *testing.T, f *Fake, s *Sub) time.Duration {
	t.Helper()
	var worst time.Duration
	deadline := time.Now().Add(5 * time.Second)
	for !s.Desynced() {
		if time.Now().After(deadline) {
			t.Fatal("the subscriber never fell behind, so the test proved nothing")
		}
		start := time.Now()
		f.Pane(pane).Emit([]byte("xxxxxxxxxxxxxxxx"))
		if d := time.Since(start); d > worst {
			worst = d
		}
	}
	return worst
}

// TestSlowSubscriberStallsNeitherTheReaderNorItsPeers. A phone that stops
// reading must cost itself a repaint and cost nobody else anything.
func TestSlowSubscriberStallsNeitherTheReaderNorItsPeers(t *testing.T) {
	f := NewFake()
	r := f.Registry()
	r.FlushInterval = time.Millisecond
	r.SizeInterval = time.Hour
	defer r.Close()

	slow, err := r.Subscribe(ctx(t), pane)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer slow.Close()
	fast, err := r.Subscribe(ctx(t), pane)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer fast.Close()

	// fast drains continuously; slow never reads a byte.
	fastSeen := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case _, ok := <-fast.Frames():
				if !ok {
					return
				}
				select {
				case fastSeen <- struct{}{}:
				default:
				}
			}
		}
	}()
	defer close(done)

	worst := flood(t, f, slow)
	if worst > time.Second {
		t.Errorf("the emitting goroutine blocked for %v; the reader must never wait on a subscriber", worst)
	}
	if fast.Desynced() {
		t.Error("the fast subscriber was dragged into a desync by its slow peer")
	}

	// The fast one is still being served after the slow one gave up.
	f.Pane(pane).Emit([]byte("still-flowing"))
	select {
	case <-fastSeen:
	case <-time.After(3 * time.Second):
		t.Fatal("the fast subscriber stopped receiving once its peer fell behind")
	}
}

// TestDroppedSubscriberRecoversWithAResyncNotACorruptStream. A dropped byte
// range cannot be replayed — a resumed stream can begin mid-escape-sequence —
// so recovery is a fresh coherent screen and nothing else.
func TestDroppedSubscriberRecoversWithAResyncNotACorruptStream(t *testing.T) {
	f := NewFake()
	r := f.Registry()
	r.FlushInterval = time.Millisecond
	r.SizeInterval = time.Hour
	defer r.Close()

	s, err := r.Subscribe(ctx(t), pane)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer s.Close()
	flood(t, f, s)

	// Drain everything the queue holds, as a phone catching up would.
	for len(s.Frames()) > 0 {
		<-s.Frames()
	}
	waitFor(t, "the desync to be repaired", func() bool { return !s.Desynced() })

	// Whatever we drained down to, the first frame after the gap has to be a
	// resync: the client repaints rather than resuming a torn byte stream.
	var repaired bool
	deadline := time.Now().Add(3 * time.Second)
	for !repaired && time.Now().Before(deadline) {
		select {
		case fr := <-s.Frames():
			if fr.Kind == KindResync {
				repaired = true
			} else {
				t.Fatalf("first frame after the gap was %v, want a resync", fr.Kind)
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !repaired {
		t.Fatal("the dropped subscriber was never re-seeded with a resync")
	}
}

// --- window size ------------------------------------------------------------

func TestPTYIsSizedToTheWindowPlusItsStatusLines(t *testing.T) {
	cases := []struct {
		ws                 WinSize
		wantCols, wantRows int
	}{
		{WinSize{Cols: 100, Rows: 29, StatusLines: 1}, 100, 30},
		{WinSize{Cols: 100, Rows: 30, StatusLines: 0}, 100, 30},
		{WinSize{Cols: 100, Rows: 28, StatusLines: 2}, 100, 30},
	}
	for _, c := range cases {
		gotC, gotR := c.ws.PTY()
		if gotC != c.wantCols || gotR != c.wantRows {
			t.Errorf("%+v.PTY() = %dx%d, want %dx%d", c.ws, gotC, gotR, c.wantCols, c.wantRows)
		}
	}
}

func TestStatusLinesTable(t *testing.T) {
	cases := map[string]int{"off": 0, "on": 1, "2": 2, "5": 5, "": 1, "banana": 1, "-1": 1, "99": 1}
	for in, want := range cases {
		if got := statusLines(in); got != want {
			t.Errorf("statusLines(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestResizePushesAFreshResync is M1's cheap test for a stale shadow emulator:
// the desktop resizes its window mid-stream and the phone must get a correct
// frame rather than keep painting into a grid that no longer exists.
func TestResizePushesAFreshResync(t *testing.T) {
	f := NewFake()
	r := f.Registry()
	r.FlushInterval = time.Millisecond
	r.SizeInterval = time.Millisecond
	defer r.Close()

	s, err := r.Subscribe(ctx(t), pane)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer s.Close()
	open := next(t, s)
	if open.Screen.W != 200 || open.Screen.H != 51 {
		t.Fatalf("opened at %dx%d, want 200x51", open.Screen.W, open.Screen.H)
	}

	f.SetSize(WinSize{Cols: 120, Rows: 39, StatusLines: 1})

	fr := next(t, s)
	if fr.Kind != KindResync {
		t.Fatalf("frame after the resize = %v, want resync", fr.Kind)
	}
	if fr.Screen.W != 120 || fr.Screen.H != 40 {
		t.Errorf("resync after the resize = %dx%d, want 120x40", fr.Screen.W, fr.Screen.H)
	}
	if got := f.Pane(pane).Resizes(); len(got) == 0 || got[len(got)-1] != [2]int{120, 40} {
		t.Errorf("shadow emulator resizes = %v, want the last to be 120x40", got)
	}
}

// TestSizeProbeFailureRefusesTheAttach: a pane that cannot be measured is
// refused rather than attached at a guessed size, because a wrong size makes
// tmux redraw the pane for every client watching it.
func TestSizeProbeFailureRefusesTheAttach(t *testing.T) {
	f := NewFake()
	f.SizeErr = errors.New("no server")
	r := slowRegistry(f)
	defer r.Close()

	if _, err := r.Subscribe(ctx(t), pane); !errors.Is(err, ErrNoSize) {
		t.Fatalf("Subscribe error = %v, want ErrNoSize", err)
	}
	if got := f.Count("attach"); got != 0 {
		t.Errorf("attach ran %d times after an unmeasurable window", got)
	}
}

func TestZeroSizedWindowRefusesTheAttach(t *testing.T) {
	f := NewFake()
	f.Size = WinSize{Cols: 0, Rows: 0}
	r := slowRegistry(f)
	defer r.Close()

	if _, err := r.Subscribe(ctx(t), pane); !errors.Is(err, ErrNoSize) {
		t.Fatalf("Subscribe error = %v, want ErrNoSize", err)
	}
	if got := f.Count("attach"); got != 0 {
		t.Errorf("attach ran %d times for a 0x0 window", got)
	}
}

// TestMidStreamSizeProbeFailureMutatesNothing: keep the last known size, log
// once, retry next tick. Resizing on a bad read reflows the agent's TUI for
// everybody.
func TestMidStreamSizeProbeFailureMutatesNothing(t *testing.T) {
	f := NewFake()
	r := f.Registry()
	r.FlushInterval = time.Millisecond
	r.SizeInterval = time.Millisecond
	var logs int
	r.Logf = func(string, ...any) { logs++ }
	defer r.Close()

	s, err := r.Subscribe(ctx(t), pane)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer s.Close()
	next(t, s)

	f.SetSizeErr(errors.New("tmux went away"))
	if extra := drainFor(s, 150*time.Millisecond); len(extra) != 0 {
		t.Errorf("a failing size probe produced %d frame(s): %v", len(extra), extra)
	}
	if got := f.Pane(pane).Resizes(); len(got) != 0 {
		t.Errorf("a failing size probe resized the emulator to %v", got)
	}

	// The size still moves once the probe answers again.
	f.SetSizeErr(nil)
	f.SetSize(WinSize{Cols: 90, Rows: 25, StatusLines: 1})
	fr := next(t, s)
	if fr.Kind != KindResync || fr.Screen.W != 90 {
		t.Fatalf("after recovery got %v (%dx%d), want a 90-column resync", fr.Kind, fr.Screen.W, fr.Screen.H)
	}
}

// --- input ------------------------------------------------------------------

func TestWriteReachesThePTY(t *testing.T) {
	f := NewFake()
	r := slowRegistry(f)
	defer r.Close()

	s, err := r.Subscribe(ctx(t), pane)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer s.Close()

	if err := r.Write(pane, []byte("\x1b[Z")); err != nil { // Shift-Tab
		t.Fatalf("Write: %v", err)
	}
	if got := string(f.Pane(pane).Written()); got != "\x1b[Z" {
		t.Errorf("pane received %q, want the Shift-Tab sequence", got)
	}
	if n := f.Count("leavecopymode"); n != 0 {
		t.Errorf("a write with no preceding scroll cancelled copy mode %d times", n)
	}
}

func TestWriteAndScrollRefuseAnUnattachedPane(t *testing.T) {
	f := NewFake()
	r := slowRegistry(f)
	defer r.Close()

	if err := r.Write(pane, []byte("x")); !errors.Is(err, ErrNotAttached) {
		t.Errorf("Write error = %v, want ErrNotAttached", err)
	}
	if err := r.Scroll(ctx(t), pane, 5); !errors.Is(err, ErrNotAttached) {
		t.Errorf("Scroll error = %v, want ErrNotAttached", err)
	}
	if err := r.Write("lola-fe-*", []byte("x")); !errors.Is(err, ErrBadPaneName) {
		t.Errorf("Write to a malformed name = %v, want ErrBadPaneName", err)
	}
	if n := f.CallCount(); n != 0 {
		t.Errorf("input to an unattached pane ran %d external calls: %v", n, f.Calls())
	}
}

// TestScrollDelegatesTheTransportDecision: which history moves is decided
// server-side by tmux.ScrollPane and a client must never re-derive it — copy
// mode on an alternate-screen agent pane reads "[0/0]" and moves nothing.
func TestScrollDelegatesTheTransportDecision(t *testing.T) {
	f := NewFake()
	f.ScrollHow = tmux.ScrollApp
	r := slowRegistry(f)
	defer r.Close()

	s, err := r.Subscribe(ctx(t), pane)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer s.Close()

	if err := r.Scroll(ctx(t), pane, 7); err != nil {
		t.Fatalf("Scroll: %v", err)
	}
	if !hasCall(f, "scroll "+pane+" 7") {
		t.Errorf("the scroll was not delegated: %v", f.Calls())
	}
	// ScrollApp leaves no mode behind, so the next keystroke must not cancel one.
	if err := r.Write(pane, []byte("a")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n := f.Count("leavecopymode"); n != 0 {
		t.Errorf("an app-transport scroll left a copy-mode cancel behind (%d)", n)
	}

	// A zero-line scroll is a no-op and costs no exec.
	before := f.Count("scroll")
	if err := r.Scroll(ctx(t), pane, 0); err != nil {
		t.Fatalf("zero Scroll: %v", err)
	}
	if f.Count("scroll") != before {
		t.Error("a zero-line scroll still ran the tmux exec")
	}
}

// TestFirstKeystrokeAfterACopyModeScrollCancelsItOnce mirrors termsvc.Write:
// keys delivered to a scrolled-back pane are read as copy-mode COMMANDS and the
// payload is silently lost, so the first key snaps the pane back — and only the
// first, so an ordinary typing burst costs nothing.
func TestFirstKeystrokeAfterACopyModeScrollCancelsItOnce(t *testing.T) {
	f := NewFake()
	f.ScrollHow = tmux.ScrollCopyMode
	r := slowRegistry(f)
	defer r.Close()

	s, err := r.Subscribe(ctx(t), pane)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer s.Close()

	if err := r.Scroll(ctx(t), pane, 3); err != nil {
		t.Fatalf("Scroll: %v", err)
	}
	for _, k := range []string{"a", "b", "c"} {
		if err := r.Write(pane, []byte(k)); err != nil {
			t.Fatalf("Write %q: %v", k, err)
		}
	}
	if n := f.Count("leavecopymode"); n != 1 {
		t.Errorf("copy mode cancelled %d times over a 3-key burst, want exactly 1", n)
	}
	if got := string(f.Pane(pane).Written()); got != "abc" {
		t.Errorf("pane received %q, want %q", got, "abc")
	}
}

// TestScrollMuIsHeldAcrossTheWriteNotJustTheFlag is the bug this mirrors from
// desktop/termsvc.go. With the lock released between clearing the flag and
// writing, a concurrent Scroll slips copy mode in after the cancel: the pane is
// then in a mode nothing will cancel, and every later keystroke is swallowed.
func TestScrollMuIsHeldAcrossTheWriteNotJustTheFlag(t *testing.T) {
	f := NewFake()
	f.ScrollHow = tmux.ScrollCopyMode
	r := slowRegistry(f)
	defer r.Close()

	s, err := r.Subscribe(ctx(t), pane)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer s.Close()
	if err := r.Scroll(ctx(t), pane, 3); err != nil {
		t.Fatalf("Scroll: %v", err)
	}

	// Wedge the copy-mode cancel so the write is caught mid-sequence.
	gate := make(chan struct{})
	entered := make(chan struct{})
	r.LeaveCopyMode = func(context.Context, string) error {
		close(entered)
		<-gate
		return nil
	}
	scrolled := make(chan struct{})
	r.ScrollPane = func(context.Context, string, int) (tmux.ScrollTransport, error) {
		close(scrolled)
		return tmux.ScrollCopyMode, nil
	}

	wrote := make(chan error, 1)
	go func() { wrote <- r.Write(pane, []byte("k")) }()
	<-entered

	go func() { _ = r.Scroll(ctx(t), pane, 4) }()

	select {
	case <-scrolled:
		t.Fatal("a concurrent Scroll entered copy mode between the cancel and the keystroke")
	case <-time.After(100 * time.Millisecond):
	}
	if got := f.Pane(pane).Written(); len(got) != 0 {
		t.Fatalf("the keystroke landed before the cancel returned: %q", got)
	}

	close(gate)
	if err := <-wrote; err != nil {
		t.Fatalf("Write: %v", err)
	}
	waitFor(t, "the deferred scroll to run", func() bool {
		select {
		case <-scrolled:
			return true
		default:
			return false
		}
	})
	if got := string(f.Pane(pane).Written()); got != "k" {
		t.Errorf("pane received %q, want %q", got, "k")
	}
}

// --- lifecycle --------------------------------------------------------------

// TestLastUnsubscribeTearsTheAttachDownAndLeaksNoGoroutine. Every goroutine
// this package starts, plus the tmux child and its PTY, must be gone.
func TestLastUnsubscribeTearsTheAttachDownAndLeaksNoGoroutine(t *testing.T) {
	settle(t)
	before := runtime.NumGoroutine()

	f := NewFake()
	r := f.Registry() // Fake.Install sets Linger to 0, so teardown is synchronous
	s, err := r.Subscribe(ctx(t), pane)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	other, err := r.Subscribe(ctx(t), pane)
	if err != nil {
		t.Fatalf("second Subscribe: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if f.Pane(pane).Closed() {
		t.Fatal("the attach was torn down while a subscriber was still watching")
	}
	if err := other.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !f.Pane(pane).Closed() {
		t.Fatal("the last unsubscribe did not tear the attach down")
	}
	if got := r.Panes(); len(got) != 0 {
		t.Errorf("the registry still lists %v after the last unsubscribe", got)
	}
	for range s.Frames() { // drains the buffered resync, then sees the close
	}
	if s.Exited() {
		t.Error("an unsubscribe must not look like the pane dying")
	}
	_ = r.Close()

	waitFor(t, "goroutines to return to the baseline", func() bool { return runtime.NumGoroutine() <= before })
}

// TestResubscribeAfterTeardownAttachesAgain: the map entry has to go with the
// bus, or the pane is unwatchable for the rest of the daemon's life.
func TestResubscribeAfterTeardownAttachesAgain(t *testing.T) {
	f := NewFake()
	r := f.Registry()
	defer r.Close()

	s, err := r.Subscribe(ctx(t), pane)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	_ = s.Close()

	again, err := r.Subscribe(ctx(t), pane)
	if err != nil {
		t.Fatalf("re-Subscribe: %v", err)
	}
	defer again.Close()
	if k := next(t, again).Kind; k != KindResync {
		t.Fatalf("re-subscribe opened with %v", k)
	}
	if got := f.Count("attach"); got != 2 {
		t.Errorf("attach ran %d times across a teardown and a re-subscribe, want 2", got)
	}
}

// TestPaneDeathIsDistinguishableFromAnUnsubscribe. Both close the stream; only
// a death delivers KindExit and reports Exited, which is what lets a client say
// "the agent finished" instead of "you were disconnected".
func TestPaneDeathIsDistinguishableFromAnUnsubscribe(t *testing.T) {
	f := NewFake()
	r := f.Registry()
	r.FlushInterval = time.Millisecond
	r.SizeInterval = time.Hour
	defer r.Close()

	s, err := r.Subscribe(ctx(t), pane)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer s.Close()
	next(t, s)

	f.Pane(pane).Exit()

	var sawExit bool
	for fr := range s.Frames() {
		if fr.Kind == KindExit {
			sawExit = true
		}
	}
	if !sawExit {
		t.Error("a dead pane delivered no exit frame")
	}
	if !s.Exited() {
		t.Error("Exited() is false after the pane's child ended")
	}
	// The exit frame is delivered promptly and the tmux child is reaped just
	// behind it, off the loop goroutines being torn down.
	waitFor(t, "the dead pane to be reaped", f.Pane(pane).Closed)
}

// TestRegistryCloseTearsDownEveryPane, which is what the daemon's stopRemote
// relies on: an unbounded wait on a stream a phone holds open is how graceful
// shutdown hangs.
func TestRegistryCloseTearsDownEveryPane(t *testing.T) {
	settle(t)
	before := runtime.NumGoroutine()

	f := NewFake()
	f.Known = map[string]bool{pane: true, "lola-be-7": true, "lola-fe-42-shell-1": true}
	r := f.Registry()
	r.Linger = time.Hour // even a lingering attach must go down with the registry

	var subs []*Sub
	for _, n := range []string{pane, "lola-be-7", "lola-fe-42-shell-1"} {
		s, err := r.Subscribe(ctx(t), n)
		if err != nil {
			t.Fatalf("Subscribe %s: %v", n, err)
		}
		subs = append(subs, s)
	}

	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for _, s := range subs {
		for range s.Frames() { // drains to the close
		}
		if s.Exited() {
			t.Errorf("%s: a registry shutdown must not look like the pane dying", s.Pane())
		}
		if !f.Pane(s.Pane()).Closed() {
			t.Errorf("%s: attach still open after Close", s.Pane())
		}
	}
	if got := r.Panes(); len(got) != 0 {
		t.Errorf("registry still lists %v after Close", got)
	}
	if _, err := r.Subscribe(ctx(t), pane); !errors.Is(err, ErrClosed) {
		t.Errorf("Subscribe after Close = %v, want ErrClosed", err)
	}
	if err := r.Close(); err != nil { // idempotent
		t.Errorf("second Close: %v", err)
	}
	waitFor(t, "goroutines to return to the baseline", func() bool { return runtime.NumGoroutine() <= before })
}

// TestLingerKeepsTheAttachForAReconnect: a phone that drops off WiFi for a
// moment must not cost a tmux client churn.
func TestLingerKeepsTheAttachForAReconnect(t *testing.T) {
	f := NewFake()
	r := f.Registry()
	r.Linger = 2 * time.Second
	defer r.Close()

	s, err := r.Subscribe(ctx(t), pane)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	_ = s.Close()
	if f.Pane(pane).Closed() {
		t.Fatal("the attach was torn down instead of lingering")
	}

	again, err := r.Subscribe(ctx(t), pane)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	defer again.Close()
	if got := f.Count("attach"); got != 1 {
		t.Errorf("attach ran %d times across a reconnect within the linger, want 1", got)
	}
	if k := next(t, again).Kind; k != KindResync {
		t.Fatalf("the reconnect opened with %v", k)
	}
}

// --- argv -------------------------------------------------------------------

// TestAttachArgvCarriesIgnoreSize. The flag is what stops a phone shrinking the
// window a desktop is drawing into, and it has to sit ahead of the target.
func TestAttachArgvCarriesIgnoreSize(t *testing.T) {
	c := &tmux.Client{Bin: "/usr/bin/tmux", SocketName: "lola"}
	got := attachArgv(c, pane)
	want := []string{"/usr/bin/tmux", "-L", "lola", "attach-session", "-f", "ignore-size", "-t", "=" + pane}
	if len(got) != len(want) {
		t.Fatalf("attachArgv = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attachArgv = %v, want %v", got, want)
		}
	}
}

// TestTmuxArgvShapeAssumption pins what attachArgv and serverPrefix assume
// about tmux.Client.AttachArgs. That package is not this one's to edit, so a
// change over there has to fail loudly here rather than quietly produce a
// command line addressing the wrong tmux server.
func TestTmuxArgvShapeAssumption(t *testing.T) {
	c := &tmux.Client{Bin: "tmux"}
	base := c.AttachArgs("lola-x-1")
	if !argvShapeOK(base) {
		t.Fatalf("tmux.Client.AttachArgs shape changed: %v", base)
	}
	if base[len(base)-2] != "-t" || base[len(base)-1] != "=lola-x-1" {
		t.Fatalf("AttachArgs no longer ends in the target: %v", base)
	}
	pre := serverPrefix(c)
	if len(pre) != 3 || pre[1] != "-L" || pre[2] != "lola" {
		t.Fatalf("serverPrefix = %v, want [tmux -L lola]", pre)
	}
}

func TestNewRegistryLeavesResolveNilSoItFailsClosed(t *testing.T) {
	r := NewRegistry(&tmux.Client{Bin: "tmux"})
	defer r.Close()
	if r.Resolve != nil {
		t.Error("NewRegistry installed a resolver; identity is the caller's to own and a default one would be an open gate")
	}
	if r.Attach == nil || r.WinSize == nil || r.ScrollPane == nil || r.LeaveCopyMode == nil {
		t.Error("NewRegistry left a tmux seam nil")
	}
	if r.Linger != DefaultLinger {
		t.Errorf("NewRegistry Linger = %v, want %v", r.Linger, DefaultLinger)
	}
	if _, err := r.Subscribe(ctx(t), pane); !errors.Is(err, ErrUnknownPane) {
		t.Errorf("Subscribe on a fresh registry = %v, want ErrUnknownPane", err)
	}
}

// --- helpers ----------------------------------------------------------------

func linesContain(lines []string, want string) bool {
	for _, l := range lines {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

func hasCall(f *Fake, want string) bool {
	for _, c := range f.Calls() {
		if c == want {
			return true
		}
	}
	return false
}

// settle waits for goroutines left over from a previous test to finish, so a
// leak assertion measures this test rather than the one before it.
func settle(t *testing.T) {
	t.Helper()
	base := runtime.NumGoroutine()
	for i := 0; i < 100; i++ {
		time.Sleep(time.Millisecond)
		if n := runtime.NumGoroutine(); n <= base {
			base = n
		}
	}
}
