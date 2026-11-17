package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

// Claude's come from the spike. Each of the others turns on when a real capture of that agent
// goes in with it.
func TestCapsClaudeHasEverything(t *testing.T) {
	c := Caps(Claude)
	if !c.Feed || !c.Send || !c.Stop || !c.Cards || !c.Cursor {
		t.Errorf("Caps(Claude) = %+v, want everything on", c)
	}
	// An empty kind resolves to claude everywhere else, so it must here too.
	if Caps("") != c {
		t.Errorf(`Caps("") = %+v, want claude's`, Caps(""))
	}
}

// Codex's log and its composer were measured. Its cards were not, so none is offered, and
// every send to it is a paste because it mistakes fast typing for one.
func TestCodexCaps(t *testing.T) {
	c := Caps(Codex)
	if !c.Feed || !c.Send || !c.Stop || !c.Cursor || !c.Paste || !c.PasteOnly || c.Cards {
		t.Errorf("Caps(Codex) = %+v", c)
	}
	if Caps(Claude).PasteOnly {
		t.Error("claude types a short send")
	}
	if o := Caps(OpenCode); o.Feed || o.Send || o.Stop || o.Cards || o.Cursor || o.Paste || o.PasteOnly {
		t.Errorf("Caps(OpenCode) = %+v, want nothing: no pane of it was measured", o)
	}
}

// A word holding "@" or "$" under the cursor keeps Codex's picker open, and Enter then
// inserts a row of it instead of submitting. One space closes it.
func TestComposerTextClosesCodexPickers(t *testing.T) {
	for in, want := range map[string]string{
		"fix the build":            "fix the build",
		"look at @main.go":         "look at @main.go ",
		"look at @main.go please":  "look at @main.go please",
		"use $imagegen":            "use $imagegen ",
		"mail a@b.c":               "mail a@b.c ",
		"line one\n@two":           "line one\n@two ",
		"costs $5 and that is all": "costs $5 and that is all",
		"":                         "",
	} {
		if got := ComposerText(Codex, in); got != want {
			t.Errorf("ComposerText(codex, %q) = %q, want %q", in, got, want)
		}
		if got := ComposerText(Claude, in); got != in {
			t.Errorf("ComposerText(claude, %q) = %q, want it untouched", in, got)
		}
	}
}

// The daemon refuses what the app's list calls a screen, so a word in one and not the other is
// either a screen that gets typed or a refusal with no row to explain it.
func TestScreenCommandsMatchTheApp(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "desktop", "frontend", "src", "lib", "slash.ts"))
	if err != nil {
		t.Skip("no frontend here")
	}
	m := regexp.MustCompile(`(?s)screen: \[(.*?)\]`).FindSubmatch(raw)
	if m == nil {
		t.Fatal("slash.ts has no screen list")
	}
	words := regexp.MustCompile(`"(/[^"]+)"`).FindAllSubmatch(m[1], -1)
	var app []string
	for _, w := range words {
		app = append(app, string(w[1]))
	}
	if !slices.Equal(app, claudeScreens) {
		t.Errorf("app %q\ndaemon %q", app, claudeScreens)
	}
	if ScreenCommand(Codex, "/config") {
		t.Error("nobody measured codex")
	}
}
