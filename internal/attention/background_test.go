package attention

import (
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/agent"
)

func backgroundCursor(visible string) Cursor {
	for i, row := range strings.Split(stripANSI(visible), "\n") {
		if strings.TrimSpace(row) == "❯" {
			return Cursor{X: 2, Y: i, Visible: true}
		}
	}
	return Cursor{}
}

func TestBackgroundFromCapturedRunningCommand(t *testing.T) {
	var hash string
	for _, name := range []string{"fresh-5", "fresh-6", "fresh-7"} {
		vis := ansiCapture(t, name)
		p, ok := ReadPrompt(vis, backgroundCursor(vis), agent.Claude)
		if !ok || p.Kind != PromptBackground || len(p.Choices) != 1 || p.Choices[0].Key != "background" {
			t.Fatalf("%s: %+v, %v", name, p, ok)
		}
		if strings.Join(p.Lines, "\n") != "Bash(sleep 8)" {
			t.Fatalf("wrong command: %q", p.Lines)
		}
		if hash != "" && p.Hash != hash {
			t.Fatal("elapsed time or activity glyph changed the action hash")
		}
		hash = p.Hash
	}
}

func TestBackgroundRejectsStaleAndUnsafePanes(t *testing.T) {
	vis := stripANSI(ansiCapture(t, "fresh-6"))
	c := backgroundCursor(vis)
	for name, pane := range map[string]string{
		"completed":    strings.ReplaceAll(vis, "Running… (5s)", "Done"),
		"missing hint": strings.ReplaceAll(vis, "(ctrl+b to run in background)", ""),
		"draft":        strings.ReplaceAll(vis, "❯ ", "❯ my draft"),
		"later output": strings.ReplaceAll(vis, "(ctrl+b to run in background)", "(ctrl+b to run in background)\n⏺ Another tool result"),
		"modal":        vis + "\n▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔\nSettings\n",
		"duplicate":    "(ctrl+b to run in background)\n" + vis,
	} {
		t.Run(name, func(t *testing.T) {
			p, ok := ReadPrompt(pane, backgroundCursor(pane), agent.Claude)
			if ok && p.Kind == PromptBackground {
				t.Fatalf("unsafe background action: %+v", p)
			}
		})
	}
	for _, k := range []agent.Kind{agent.Codex, agent.OpenCode} {
		if _, ok := ReadPrompt(vis, c, k); ok {
			t.Fatalf("offered Claude control for %s", k)
		}
	}
	for _, bad := range []Cursor{{X: 2, Y: c.Y}, {X: 3, Y: c.Y, Visible: true}, {X: 2, Y: 0, Visible: true}} {
		if p, ok := ReadPrompt(vis, bad, agent.Claude); ok && p.Kind == PromptBackground {
			t.Fatalf("accepted cursor %+v", bad)
		}
	}
	p, _ := ReadPrompt(vis, c, agent.Claude)
	changed, _ := ReadPrompt(strings.ReplaceAll(vis, "Bash(sleep 8)", "Bash(sleep 9)"), c, agent.Claude)
	if p.Hash == changed.Hash {
		t.Fatal("changed command kept the old hash")
	}
}
