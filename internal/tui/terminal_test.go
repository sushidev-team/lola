package tui

import (
	"os/exec"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/vtterm"
)

// liveTerm registers a long-running (cat) embedded terminal for sessionID.
func liveTerm(t *testing.T, m *rootModel, sessionID string) *termView {
	t.Helper()
	term, err := vtterm.New(exec.Command("cat"), 78, 22)
	if err != nil {
		t.Fatalf("vtterm.New: %v", err)
	}
	tv := &termView{term: term, sessionID: sessionID, title: "shell"}
	if m.terms == nil {
		m.terms = map[string]*termView{}
	}
	m.terms[sessionID] = tv
	return tv
}

// Ctrl-q detaches without killing the shell; re-entering attaches the SAME
// terminal (no new process); reap tears it down and returns to the cockpit.
func TestTerminalDetachReenterReap(t *testing.T) {
	m := newTestRoot(t)
	tv := liveTerm(t, m, "s1")
	m.term = tv

	m.detachTerm()
	if m.term != nil {
		t.Error("detach must return to the cockpit")
	}
	if !m.runningShell("s1") {
		t.Error("a detached shell must still be running and re-enterable")
	}

	if cmd := m.attachTerm(tv); cmd == nil {
		t.Error("attach must return a frame-wait command")
	}
	if m.term != tv {
		t.Error("re-enter must attach the EXISTING terminal, not spawn a new one")
	}

	m.reapTerm(tv, "gone")
	if m.runningShell("s1") || m.terms["s1"] != nil {
		t.Error("reaped shell must be removed from the registry")
	}
	if m.term != nil {
		t.Error("reaping the attached terminal must return to the cockpit")
	}
	if m.sessions.flash != "gone" {
		t.Errorf("reap flash = %q, want %q", m.sessions.flash, "gone")
	}
}

// sweepTerms reaps a DETACHED terminal whose process has already exited, but
// never the attached one.
func TestTerminalSweepReapsExitedDetached(t *testing.T) {
	m := newTestRoot(t)
	term, err := vtterm.New(exec.Command("sh", "-c", "exit 0"), 40, 5)
	if err != nil {
		t.Fatalf("vtterm.New: %v", err)
	}
	tv := &termView{term: term, sessionID: "s2", title: "shell"}
	m.terms = map[string]*termView{"s2": tv}
	// Wait for the child to exit.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !tv.term.Exited() {
		time.Sleep(20 * time.Millisecond)
	}
	if !tv.term.Exited() {
		t.Fatal("child never exited")
	}
	m.sweepTerms() // detached + exited → reaped
	if m.terms["s2"] != nil {
		t.Error("sweep must reap a detached, exited shell")
	}
}

// closeAllTerms tears down every registered terminal (called on quit).
func TestCloseAllTerms(t *testing.T) {
	m := newTestRoot(t)
	liveTerm(t, m, "a")
	tv := liveTerm(t, m, "b")
	m.term = tv
	m.closeAllTerms()
	if len(m.terms) != 0 || m.term != nil {
		t.Errorf("closeAllTerms must clear the registry and detach (terms=%d)", len(m.terms))
	}
}

// The Ctrl-q key detaches (leaves the shell running) rather than killing it.
func TestCtrlQDetaches(t *testing.T) {
	m := newTestRoot(t)
	tv := liveTerm(t, m, "s3")
	m.term = tv
	m.handleTermKey(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if m.term != nil {
		t.Error("ctrl+q must detach")
	}
	if !m.runningShell("s3") {
		t.Error("ctrl+q must NOT kill the shell")
	}
	m.closeAllTerms()
}
