package vtterm

import (
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// The tap and WithScreen exist for one consumer (internal/panebus) and rest on
// one property: a screen read taken under WithScreen is EXACTLY the prefix of
// the byte stream the tap delivers afterwards. These tests are about that
// property, not about the plumbing.

func TestTapSeesOutputTheEmulatorHasAlreadyModelled(t *testing.T) {
	// New starts the reader immediately. Hold the child until the tap is
	// installed: output consumed before registration is intentionally not replayed.
	term, err := New(exec.Command("sh", "-c", "read -r _; printf 'tapped-line\\n'"), 40, 6)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer term.Close()

	var mu sync.Mutex
	var got []byte
	term.Tap(func(p []byte) {
		mu.Lock()
		got = append(got, p...)
		mu.Unlock()
	})
	term.Write([]byte("\r")) // release the child only after registration

	ok := waitUntil(t, term, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(string(got), "tapped-line")
	})
	if !ok {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("tap never saw the output; got %q", got)
	}
	// The emulator saw the same bytes: the tap is a mirror of the stream, not a
	// diversion of it.
	if !renderHas(term, "tapped-line") {
		t.Errorf("emulator did not render what the tap saw:\n%q", term.Render())
	}
}

func TestTapNilStopsDelivery(t *testing.T) {
	term, err := New(exec.Command("cat"), 40, 6)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer term.Close()

	var mu sync.Mutex
	var n int
	term.Tap(func(p []byte) { mu.Lock(); n += len(p); mu.Unlock() })
	term.Write([]byte("first\r"))
	if !waitUntil(t, term, 2*time.Second, func() bool { mu.Lock(); defer mu.Unlock(); return n > 0 }) {
		t.Fatal("tap never fired")
	}

	term.Tap(nil)
	mu.Lock()
	before := n
	mu.Unlock()
	term.Write([]byte("second\r"))
	// Wait for the emulator to have taken the second write, then check the tap
	// stayed silent through it.
	if !waitUntil(t, term, 2*time.Second, func() bool { return renderHas(term, "second") }) {
		t.Fatal("second write never reached the emulator")
	}
	mu.Lock()
	defer mu.Unlock()
	if n != before {
		t.Errorf("cleared tap still received %d bytes", n-before)
	}
}

// TestWithScreenHoldsTheReaderOff is the coherence guarantee in its observable
// form: no byte may be tapped while a screen reading is in progress, because a
// subscriber registered inside the callback would otherwise miss it — the byte
// would be neither in the screen it was handed nor in the stream that followed.
func TestWithScreenHoldsTheReaderOff(t *testing.T) {
	term, err := New(exec.Command("cat"), 40, 6)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer term.Close()

	var mu sync.Mutex
	var tapped int
	var inScreen bool
	var violated bool
	term.Tap(func(p []byte) {
		mu.Lock()
		if inScreen {
			violated = true
		}
		tapped += len(p)
		mu.Unlock()
	})

	// A steady stream of echoes for the reader to pump while the screen is read.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			term.Write([]byte("noise\r"))
			time.Sleep(time.Millisecond)
		}
	}()

	for i := 0; i < 20; i++ {
		term.WithScreen(func(Screen) {
			mu.Lock()
			inScreen = true
			mu.Unlock()
			time.Sleep(2 * time.Millisecond)
			mu.Lock()
			inScreen = false
			mu.Unlock()
		})
	}
	close(stop)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if violated {
		t.Error("the tap fired while a screen reading was in progress; a subscriber registered there would lose those bytes")
	}
	if tapped == 0 {
		t.Error("the test never actually produced output, so it proved nothing")
	}
}

func TestScreenReportsGeometryCursorAndAltScreen(t *testing.T) {
	term, err := New(exec.Command("cat"), 30, 8)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer term.Close()

	term.Write([]byte("abc"))
	if !waitUntil(t, term, 2*time.Second, func() bool { return renderHas(term, "abc") }) {
		t.Fatal("input never echoed")
	}

	var scr Screen
	term.WithScreen(func(s Screen) { scr = s })
	if scr.W != 30 || scr.H != 8 {
		t.Errorf("screen size = %dx%d, want 30x8", scr.W, scr.H)
	}
	wantX, wantY := term.Cursor()
	if scr.CursorX != wantX || scr.CursorY != wantY {
		t.Errorf("screen cursor = %d,%d; Cursor() says %d,%d", scr.CursorX, scr.CursorY, wantX, wantY)
	}
	if scr.AltScreen != term.AltScreen() {
		t.Errorf("screen altScreen = %v, AltScreen() = %v", scr.AltScreen, term.AltScreen())
	}
	if !scr.CursorVisible {
		t.Error("cursor should start visible, matching the emulator's mode defaults")
	}
	if len(scr.Lines) == 0 {
		t.Error("screen carried no lines")
	}
}

// TestScreenTracksCursorVisibility pins the one mode this package mirrors off
// the emulator's callback. SafeEmulator exposes no accessor for DECTCEM, so if
// the callback is not wired the resync frame would always claim the cursor is
// visible — and the cursor is the field capture-pane cannot give and the reason
// the shadow emulator is here at all.
func TestScreenTracksCursorVisibility(t *testing.T) {
	// The sequences have to come from the CHILD's stdout: bytes written to the
	// PTY master are the child's INPUT, and the line discipline echoes an ESC
	// back as "^[" rather than as an escape the emulator would parse.
	script := `printf '\033[?25l'; read -r _; printf '\033[?25h'; sleep 30`
	term, err := New(exec.Command("sh", "-c", script), 30, 8)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer term.Close()

	visible := func() bool {
		var v bool
		term.WithScreen(func(s Screen) { v = s.CursorVisible })
		return v
	}
	if !waitUntil(t, term, 2*time.Second, func() bool { return !visible() }) {
		t.Fatal("DECTCEM reset never reached the screen reading")
	}
	term.Write([]byte("\r")) // release the read, so the child emits the set
	if !waitUntil(t, term, 2*time.Second, visible) {
		t.Fatal("DECTCEM set never reached the screen reading")
	}
}

func TestResizeIsVisibleInTheNextScreenReading(t *testing.T) {
	term, err := New(exec.Command("cat"), 20, 4)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer term.Close()
	term.Resize(64, 20)
	var scr Screen
	term.WithScreen(func(s Screen) { scr = s })
	if scr.W != 64 || scr.H != 20 {
		t.Errorf("screen size after resize = %dx%d, want 64x20", scr.W, scr.H)
	}
}

func TestWithScreenNilIsANoOp(t *testing.T) {
	term, err := New(exec.Command("cat"), 10, 3)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer term.Close()
	term.WithScreen(nil) // must not panic, and must not wedge tapMu
	term.WithScreen(func(Screen) {})
}
