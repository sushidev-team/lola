package attention

import (
	"testing"

	"github.com/sushidev-team/lola/internal/agent"
)

func TestEmptyComposer(t *testing.T) {
	frame := func(body string) string {
		return "╭──────────╮\n" + body + "\n╰──────────╯\n? for shortcuts\n"
	}
	for _, kind := range []agent.Kind{agent.Claude, agent.Codex, agent.OpenCode} {
		for _, tc := range []struct {
			name, pane string
			want       bool
		}{
			{"empty", frame("│ >        │"), true},
			{"draft", frame("│ > Check PR comments │"), false},
			{"multiline", frame("│ >        │\n│ unsent second line │"), false},
			{"scrolled draft", frame("│ unsent first line │\n│ > │"), false},
			{"attachment", frame("│ > [Image #1] │"), false},
			{"pipe draft", frame("│ > | │"), false},
			{"pipe continuation", frame("│ > │\n│ || │"), false},
			{"partial frame", "│ > │\n╰──────────╯", false},
			{"missing bottom", "╭──────────╮\n│ > │", false},
			{"old empty prompt", frame("│ > │") + frame("│ > latest draft │"), false},
			{"old prompt before unknown layout", frame("│ > │") + "new editor layout\nunsent draft", false},
			{"working", "esc to interrupt\n" + frame("│ > │"), false},
			{"unknown", "done", false},
		} {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				if got := EmptyComposer(tc.pane, kind); got != tc.want {
					t.Errorf("EmptyComposer = %v, want %v", got, tc.want)
				}
			})
		}
	}
	for _, tc := range []struct {
		name, pane string
		want       bool
	}{
		{"live Claude layout", "✻ Cogitated for 5m 37s\n\x1b[37m────────────\n\x1b[39m❯\u00a0\n\x1b[37m────────────\n\x1b[39m⏵⏵ auto mode on", true},
		{"draft in live layout", "────────────\n❯ Check PR comments and resolve th\n────────────", false},
		{"dim suggestion is not proven empty", "────────────\n❯ \x1b[2mcheck CI and fix if build-test fails\x1b[0m\n────────────", false},
		{"unknown unframed layout", "❯\n", false},
		{"modal", "▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔\n────────────\n❯\n────────────", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := EmptyComposer(tc.pane, agent.Claude); got != tc.want {
				t.Errorf("EmptyComposer = %v, want %v", got, tc.want)
			}
		})
	}
}
