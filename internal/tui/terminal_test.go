package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// shellIndex parses the trailing N from a "<id>-shell-N" tmux name so shells sort
// and number stably; a name without a numeric suffix is index 0.
func TestShellIndex(t *testing.T) {
	cases := map[string]int{
		"NORI-1-shell-1":  1,
		"NORI-1-shell-12": 12,
		"NORI-1":          0, // not a shell name
		"NORI-1-shell-":   0, // empty number
	}
	for name, want := range cases {
		if got := shellIndex("NORI-1", name); got != want {
			t.Errorf("shellIndex(%q) = %d, want %d", name, got, want)
		}
	}
}

// cycleTabIndex walks {agent(0), shell1…shellN} and wraps at both ends.
func TestCycleTabIndex(t *testing.T) {
	// 2 shells → tabs {0,1,2}, span 3.
	if got := cycleTabIndex(0, 2, +1); got != 1 {
		t.Errorf("0 +1 = %d, want 1", got)
	}
	if got := cycleTabIndex(2, 2, +1); got != 0 {
		t.Errorf("2 +1 = %d, want 0 (wrap forward)", got)
	}
	if got := cycleTabIndex(0, 2, -1); got != 2 {
		t.Errorf("0 -1 = %d, want 2 (wrap backward)", got)
	}
	// No shells → only the agent tab; every move stays on 0.
	if got := cycleTabIndex(0, 0, +1); got != 0 {
		t.Errorf("no shells: 0 +1 = %d, want 0", got)
	}
}

// keyToBytes re-encodes a key press as the bytes the PTY child expects. The one
// case with a decision in it is Enter: a bare CR is "send this message" to a
// coding agent, so shift+enter has to become the newline byte pair (ESC CR)
// instead — the same encoding the desktop terminal writes.
func TestKeyToBytesEnter(t *testing.T) {
	plain := keyToBytes(tea.KeyPressMsg{Code: tea.KeyEnter})
	if string(plain) != "\r" {
		t.Errorf("enter = %q, want %q", plain, "\r")
	}
	shifted := keyToBytes(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	if string(shifted) != "\x1b\r" {
		t.Errorf("shift+enter = %q, want %q (line break, not submit)", shifted, "\x1b\r")
	}
	// alt+enter already meant ESC CR and must keep doing so.
	alt := keyToBytes(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	if string(alt) != "\x1b\r" {
		t.Errorf("alt+enter = %q, want %q", alt, "\x1b\r")
	}
}
