package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// box must return exactly h lines, each exactly w display columns, regardless
// of body over/underflow — the repaint depends on this being pixel-exact.
func TestBoxDimensions(t *testing.T) {
	cases := []struct {
		name string
		body []string
		w, h int
	}{
		{"empty", nil, 20, 5},
		{"underfull", []string{"a", "b"}, 20, 6},
		{"overfull", []string{"1", "2", "3", "4", "5", "6"}, 20, 4},
		{"wide body clipped", []string{strings.Repeat("x", 100)}, 24, 3},
		{"tiny floored", nil, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := box("Title", tc.body, tc.w, tc.h, false)
			wantW, wantH := tc.w, tc.h
			if wantW < 4 {
				wantW = 4
			}
			if wantH < 2 {
				wantH = 2
			}
			if len(lines) != wantH {
				t.Fatalf("height = %d, want %d", len(lines), wantH)
			}
			for i, ln := range lines {
				if got := lipgloss.Width(ln); got != wantW {
					t.Errorf("line %d width = %d, want %d (%q)", i, got, wantW, stripANSI(ln))
				}
			}
		})
	}
}

// The title is cut into the top border and the corners are intact.
func TestBoxTitleCut(t *testing.T) {
	lines := box("Sessions", nil, 30, 3, false)
	top := stripANSI(lines[0])
	if !strings.HasPrefix(top, "┌─ Sessions ") {
		t.Errorf("top border missing cut title: %q", top)
	}
	if !strings.HasSuffix(top, "┐") || !strings.HasPrefix(top, "┌") {
		t.Errorf("corners malformed: %q", top)
	}
	if !strings.HasPrefix(stripANSI(lines[2]), "└") {
		t.Errorf("bottom border malformed: %q", stripANSI(lines[2]))
	}
}

// A title too wide for the box degrades to a plain rule rather than overflowing.
func TestBoxTitleTooWide(t *testing.T) {
	lines := box("A very long title that will not fit", nil, 12, 3, false)
	top := stripANSI(lines[0])
	if lipgloss.Width(lines[0]) != 12 {
		t.Errorf("width = %d, want 12", lipgloss.Width(lines[0]))
	}
	if strings.Contains(top, "very long") {
		t.Errorf("oversized title should be dropped, got %q", top)
	}
}

// Focus is purely cosmetic (border/title color): a focused box must keep the
// exact same geometry as an unfocused one. Color itself is asserted at runtime
// only — lipgloss renders without SGR under the no-TTY test profile.
func TestBoxFocusGeometry(t *testing.T) {
	foc := box("T", []string{"body"}, 10, 4, true)
	unf := box("T", []string{"body"}, 10, 4, false)
	if len(foc) != len(unf) {
		t.Fatalf("focus changed height: %d vs %d", len(foc), len(unf))
	}
	for i := range foc {
		if lipgloss.Width(foc[i]) != lipgloss.Width(unf[i]) {
			t.Errorf("focus changed width on line %d", i)
		}
	}
}

// joinCols keeps rows aligned and blank-fills shorter columns.
func TestJoinCols(t *testing.T) {
	a := []string{"aaa", "aaa", "aaa"}
	b := []string{"bb", "bb"}
	out := joinCols(1, a, b)
	if len(out) != 3 {
		t.Fatalf("height = %d, want 3 (tallest)", len(out))
	}
	if out[0] != "aaa bb" {
		t.Errorf("row 0 = %q, want %q", out[0], "aaa bb")
	}
	// The short column's missing row is blank-filled to its width (2).
	if out[2] != "aaa "+"  " {
		t.Errorf("row 2 = %q, want short column blank-filled", out[2])
	}
}

func TestFitHeight(t *testing.T) {
	in := []string{"xx", "xx"}
	if got := fitHeight(in, 4); len(got) != 4 || got[3] != "  " {
		t.Errorf("grow: %q", got)
	}
	if got := fitHeight(in, 1); len(got) != 1 {
		t.Errorf("shrink: %q", got)
	}
	if got := fitHeight(in, 2); len(got) != 2 {
		t.Errorf("exact: %q", got)
	}
}

// highlightRow pads to exactly w visible columns, re-applies its background
// after inner resets (so a pill can't punch a hole), and ends reset.
func TestHighlightRow(t *testing.T) {
	// Literal SGR so the inner reset survives the no-TTY test profile.
	row := "ENG-1  \x1b[31mci_failed\x1b[0m  #7"
	out := highlightRow(row, 30, "236")
	if got := lipgloss.Width(out); got != 30 {
		t.Errorf("width = %d, want 30 (%q)", got, stripANSI(out))
	}
	if !strings.Contains(out, "\x1b[48;5;236m") {
		t.Error("must set the selection background")
	}
	// The inner reset from badText.Render is followed by a re-applied background.
	if !strings.Contains(out, "\x1b[0m\x1b[48;5;236m") {
		t.Error("must re-apply the background after an inner reset")
	}
	if !strings.HasSuffix(out, "\x1b[0m") {
		t.Error("must end with a reset")
	}
	if !strings.Contains(stripANSI(out), "ci_failed") {
		t.Error("must preserve the row content")
	}
}

func TestStripANSI(t *testing.T) {
	styled := badText.Render("hello") + " " + goodText.Render("world")
	if got := stripANSI(styled); got != "hello world" {
		t.Errorf("stripANSI = %q, want %q", got, "hello world")
	}
}
