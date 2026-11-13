package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

// Claude's come from the spike. The other two are left unpinned here on purpose: each
// turns on when a real capture of that agent goes in with it.
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

func TestOnlyClaudePastes(t *testing.T) {
	if !Caps(Claude).Paste || Caps(Codex).Paste || Caps(OpenCode).Paste {
		t.Errorf("paste: claude %v, codex %v, opencode %v", Caps(Claude).Paste, Caps(Codex).Paste, Caps(OpenCode).Paste)
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
