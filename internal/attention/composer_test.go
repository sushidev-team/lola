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
		{"dim suggestion counts as empty", "────────────\n❯ \x1b[2mcheck CI and fix if build-test fails\x1b[0m\n────────────", true},
		{"live Claude suggestion (SUSHI-674 capture)", "────────────\n\x1b[39m❯\u00a0\x1b[2mnow fix the table proportions from SUSHI-673 too\x1b[0m\n────────────\n⏵⏵ auto mode on", true},
		{"draft typed before a suggestion", "────────────\n❯ fix \x1b[2mthe rest\x1b[0m\n────────────", false},
		{"draft after a reset", "────────────\n❯ \x1b[2mghost\x1b[0m typed\n────────────", false},
		{"draft after normal intensity", "────────────\n❯ \x1b[2mghost\x1b[22mtyped\n────────────", false},
		{"truecolour 2 is not dim", "────────────\n❯ \x1b[38;2;2;2;2mtyped\x1b[0m\n────────────", false},
		{"256-colour 2 is not dim", "────────────\n❯ \x1b[38;5;2mtyped\x1b[0m\n────────────", false},
		{"dim then colour keeps dim", "────────────\n❯ \x1b[2m\x1b[37mghost\x1b[0m\n────────────", true},
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
