package vtterm

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// waitUntil polls cond, pumping frame signals, until it holds or the deadline
// passes. Returns whether cond held.
func waitUntil(t *testing.T, term *Term, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		select {
		case <-term.Frames():
		case <-time.After(50 * time.Millisecond):
		}
	}
	return cond()
}

func renderHas(term *Term, want string) bool {
	for _, ln := range term.Render() {
		if strings.Contains(stripSGR(ln), want) {
			return true
		}
	}
	return false
}

// stripSGR drops CSI escape sequences so assertions match on visible text.
func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && (s[i] < 0x40 || s[i] > 0x7e) {
				i++
			}
			continue // skip the final byte too
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestRunAndRender(t *testing.T) {
	term, err := New(exec.Command("sh", "-c", "printf 'hello-vt\\n'"), 30, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer term.Close()
	if !waitUntil(t, term, 2*time.Second, func() bool { return renderHas(term, "hello-vt") }) {
		t.Fatalf("render never showed the command output:\n%q", term.Render())
	}
}

func TestInputEchoesThroughPTY(t *testing.T) {
	term, err := New(exec.Command("cat"), 40, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer term.Close()
	term.Write([]byte("ping\r")) // PTY echo + cat both surface "ping"
	if !waitUntil(t, term, 2*time.Second, func() bool { return renderHas(term, "ping") }) {
		t.Fatalf("typed input never appeared:\n%q", term.Render())
	}
}

func TestResizeUpdatesSize(t *testing.T) {
	term, err := New(exec.Command("cat"), 20, 4)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer term.Close()
	term.Resize(48, 12)
	if w, h := term.Size(); w != 48 || h != 12 {
		t.Errorf("size = %dx%d, want 48x12", w, h)
	}
	term.Resize(0, -3) // floored, no panic
	if w, h := term.Size(); w != 1 || h != 1 {
		t.Errorf("floored size = %dx%d, want 1x1", w, h)
	}
}

func TestExitedAndCloseIdempotent(t *testing.T) {
	term, err := New(exec.Command("sh", "-c", "exit 0"), 10, 3)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !waitUntil(t, term, 2*time.Second, term.Exited) {
		t.Error("Exited never became true after the child exited")
	}
	if err := term.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := term.Close(); err != nil { // idempotent
		t.Errorf("second Close: %v", err)
	}
}
