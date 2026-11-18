package attention

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/agent"
)

// codexPane loads one pane of codex-cli 0.167.2 as tmux captured it, with its cursor. Only
// the working directory was replaced.
func codexPane(t *testing.T, name string) (string, Cursor) {
	t.Helper()
	vis, err := os.ReadFile(filepath.Join("testdata", "codex", name+".vis"))
	if err != nil {
		t.Fatal(err)
	}
	cur, err := os.ReadFile(filepath.Join("testdata", "codex", name+".cur"))
	if err != nil {
		t.Fatal(err)
	}
	var c Cursor
	var flag int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(cur)), "%d %d %d", &c.X, &c.Y, &flag); err != nil {
		t.Fatalf("%s.cur: %v", name, err)
	}
	c.Visible = flag == 1
	return string(vis), c
}

// Every pane measured, and what each must read as. empty is what automation may type into;
// message is what a person's own message may reach, which adds a working agent.
func TestCodexPanes(t *testing.T) {
	cases := []struct {
		name           string
		act            Activity
		empty, message bool
	}{
		{"rest-fresh", ActivityWaiting, true, true},
		{"interrupted", ActivityWaiting, true, true},
		// One Esc at rest arms "esc again to edit previous message". A paste then went
		// through as an ordinary message, so this is still an empty composer.
		{"esc-primed", ActivityWaiting, true, true},
		{"working", ActivityWorking, false, true},

		{"draft", ActivityWaiting, false, false},
		{"draft-spaces", ActivityWaiting, false, false},
		{"draft-multiline", ActivityWaiting, false, false},
		{"draft-pasted", ActivityWaiting, false, false},
		{"after-turn-draft", ActivityWaiting, false, false},
		{"working-draft", ActivityWorking, false, false},
		{"popup-at", ActivityWaiting, false, false},
		{"slash-popup", ActivityWaiting, false, false},

		// Not the composer at all. None of these may ever be typed into.
		{"shell-mode", ActivityUnknown, false, false},
		{"transcript-browser", ActivityUnknown, false, false},
		{"update-modal", ActivityUnknown, false, false},
		{"trust-modal", ActivityUnknown, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vis, c := codexPane(t, tc.name)
			if got := Classify(vis, agent.Codex); got != tc.act {
				t.Errorf("Classify = %s, want %s", got, tc.act)
			}
			if got := ComposerEmptyAt(vis, c, agent.Codex); got != tc.empty {
				t.Errorf("ComposerEmptyAt = %v, want %v (cursor %+v)", got, tc.empty, c)
			}
			if got := MessageComposerEmptyAt(vis, c, agent.Codex); got != tc.message {
				t.Errorf("MessageComposerEmptyAt = %v, want %v", got, tc.message)
			}
		})
	}
}

// The proof rests on the cursor. The same screen with the cursor elsewhere, or hidden, is
// not an empty composer, whatever the rows say.
func TestCodexEmptyNeedsTheCursorOnTheComposer(t *testing.T) {
	vis, c := codexPane(t, "rest-fresh")
	for _, moved := range []Cursor{
		{X: c.X, Y: c.Y, Visible: false},
		{X: c.X + 1, Y: c.Y, Visible: true},
		{X: c.X, Y: c.Y - 1, Visible: true},
		{X: c.X, Y: c.Y + 2, Visible: true},
		{X: c.X, Y: 999, Visible: true},
	} {
		if ComposerEmptyAt(vis, moved, agent.Codex) {
			t.Errorf("cursor %+v read as an empty composer", moved)
		}
	}
	// A draft with the cursor moved home sits in the caret's column. Its text is not dim.
	home := strings.Replace(vis, "\x1b[2mAsk Codex to do anything\x1b[0m", "rm -rf /", 1)
	if home == vis {
		t.Fatal("the fixture has no placeholder to replace")
	}
	if ComposerEmptyAt(home, c, agent.Codex) {
		t.Error("a draft with the cursor at its start read as empty")
	}
}

// A past message keeps its caret in the transcript. With the composer in shell mode it is
// the last "›" on screen, and it is not a composer.
func TestCodexPastMessageIsNotTheComposer(t *testing.T) {
	rows := []string{"› fix the build", "", "• Done.", "", "  Worked for 5s", "", "! ls", "", "  model · /w", "  Shell mode"}
	if got := codexComposer(rows); got != -1 {
		t.Errorf("composer at row %d", got)
	}
	if got := codexComposer([]string{"• Done.", "", "› Ask Codex to do anything", "", "  model · /w", "  ? for shortcuts"}); got != 2 {
		t.Errorf("composer at row %d, want 2", got)
	}
}
