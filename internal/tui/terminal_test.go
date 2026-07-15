package tui

import (
	"os/exec"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/vtterm"
)

// liveTerm registers a long-running (cat) shell for sessionID.
func liveTerm(t *testing.T, m *rootModel, sessionID string) *termView {
	t.Helper()
	term, err := vtterm.New(exec.Command("cat"), 78, 22)
	if err != nil {
		t.Fatalf("vtterm.New: %v", err)
	}
	tv := &termView{term: term, sessionID: sessionID, kind: termShell, title: "shell"}
	if m.terms == nil {
		m.terms = map[string]*termView{}
	}
	m.terms[sessionID] = tv
	return tv
}

// reapTerm closes a shell and removes it from the registry; runningShell
// reflects that.
func TestReapTerm(t *testing.T) {
	m := newTestRoot(t)
	tv := liveTerm(t, m, "s1")
	if !m.runningShell("s1") {
		t.Fatal("a fresh shell must be running")
	}
	m.reapTerm(tv, "gone")
	if m.runningShell("s1") || m.terms["s1"] != nil {
		t.Error("reaped shell must be removed from the registry")
	}
	if m.sessions.flash != "gone" {
		t.Errorf("reap flash = %q, want %q", m.sessions.flash, "gone")
	}
}

// sweepTerms reaps a registered shell whose process has exited (and which is not
// the shown embed).
func TestSweepReapsExited(t *testing.T) {
	m := newTestRoot(t)
	term, err := vtterm.New(exec.Command("sh", "-c", "exit 0"), 40, 5)
	if err != nil {
		t.Fatalf("vtterm.New: %v", err)
	}
	m.terms = map[string]*termView{"s2": {term: term, sessionID: "s2", kind: termShell}}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !term.Exited() {
		time.Sleep(20 * time.Millisecond)
	}
	if !term.Exited() {
		t.Fatal("child never exited")
	}
	m.sweepTerms() // exited + not shown → reaped
	if m.terms["s2"] != nil {
		t.Error("sweep must reap an exited shell")
	}
}

// closeAllTerms tears down every registered shell (called on quit).
func TestCloseAllTerms(t *testing.T) {
	m := newTestRoot(t)
	liveTerm(t, m, "a")
	liveTerm(t, m, "b")
	m.closeAllTerms()
	if len(m.terms) != 0 {
		t.Errorf("closeAllTerms must clear the registry (terms=%d)", len(m.terms))
	}
}
