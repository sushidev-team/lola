package attention

import (
	"reflect"
	"testing"

	"github.com/sushidev-team/lola/internal/agent"
)

// The inputs below are real-ish tmux pane tails: box-drawing, the "❯" select
// cursor, and interleaved ANSI SGR codes, exactly what CapturePane returns.
func TestParse(t *testing.T) {
	tests := []struct {
		name         string
		in           string
		wantOK       bool
		wantFreeForm bool
		wantPrompt   string
		wantChoices  []Choice
	}{
		{
			name: "claude numbered select with cursor and ansi in a box",
			in: "\x1b[2m⏺ Done reviewing the diff.\x1b[0m\n" +
				"╭────────────────────────────────────────────────────────╮\n" +
				"│ \x1b[1mDo you want to proceed?\x1b[0m                                 │\n" +
				"│ \x1b[36m❯ 1. Yes\x1b[0m                                                │\n" +
				"│   2. Yes, and don't ask again                            │\n" +
				"│   3. No, and tell Claude what to do differently (esc)    │\n" +
				"╰────────────────────────────────────────────────────────╯\n",
			wantOK:     true,
			wantPrompt: "Do you want to proceed?",
			wantChoices: []Choice{
				{Key: "1", Label: "Yes"},
				{Key: "2", Label: "Yes, and don't ask again"},
				{Key: "3", Label: "No, and tell Claude what to do differently (esc)"},
			},
		},
		{
			name:       "yes no gate on the question line",
			in:         "Formatting build.log...\nOverwrite existing file build.log? (y/n) ",
			wantOK:     true,
			wantPrompt: "Overwrite existing file build.log?",
			wantChoices: []Choice{
				{Key: "y", Label: "Yes"},
				{Key: "n", Label: "No"},
			},
		},
		{
			name:         "ansi laden yes no with marker on its own line",
			in:           "\x1b[1;33mApply the migration to the dev database\x1b[0m\n\x1b[2m[Y/n]\x1b[0m ",
			wantOK:       true,
			wantPrompt:   "Apply the migration to the dev database",
			wantChoices:  []Choice{{Key: "y", Label: "Yes"}, {Key: "n", Label: "No"}},
			wantFreeForm: false,
		},
		{
			name:         "free form question ending in question mark with trailing prompt caret",
			in:           "I need a bit more detail before continuing.\nWhat should I name the new migration file?\n> ",
			wantOK:       true,
			wantFreeForm: true,
			wantPrompt:   "What should I name the new migration file?",
		},
		{
			name:         "free form via bare prompt indicator no question mark ansi laden",
			in:           "\x1b[2mⓘ context: 12k tokens\x1b[0m\n\n\x1b[35mEnter a commit message for the staged changes\x1b[0m\n\x1b[2m>\x1b[0m ",
			wantOK:       true,
			wantFreeForm: true,
			wantPrompt:   "Enter a commit message for the staged changes",
		},
		{
			name:   "plain output no question",
			in:     "Compiling module foo...\nok  \tgithub.com/foo/bar\t0.123s\nAll tests passed.\n",
			wantOK: false,
		},
		{
			name:   "lone shell prompt is not an answerable question",
			in:     "$ ls\nbuild.log  main.go\n$ \n> \n",
			wantOK: false,
		},
		{
			name:   "empty pane",
			in:     "\n\n   \n",
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Parse(tc.in, agent.Claude)
			if ok != tc.wantOK {
				t.Fatalf("Parse ok = %v, want %v (got %+v)", ok, tc.wantOK, got)
			}
			if !ok {
				return
			}
			if got.FreeForm != tc.wantFreeForm {
				t.Errorf("FreeForm = %v, want %v", got.FreeForm, tc.wantFreeForm)
			}
			if got.Prompt != tc.wantPrompt {
				t.Errorf("Prompt = %q, want %q", got.Prompt, tc.wantPrompt)
			}
			if !reflect.DeepEqual(got.Choices, tc.wantChoices) {
				t.Errorf("Choices = %#v, want %#v", got.Choices, tc.wantChoices)
			}
			if got.Raw == "" {
				t.Errorf("Raw is empty; want the trailing block for display")
			}
		})
	}
}

// Raw must carry the cleaned trailing block (ANSI + framing stripped) so a
// caller can show the operator what the parse was derived from.
func TestParseRawIsCleanedTrailingBlock(t *testing.T) {
	in := "\x1b[2mnoise above\x1b[0m\n\n" +
		"╭──────────────╮\n" +
		"│ \x1b[1mPick one:\x1b[0m    │\n" +
		"│ ❯ 1. Alpha   │\n" +
		"│   2. Beta    │\n" +
		"╰──────────────╯\n"
	got, ok := Parse(in, agent.Claude)
	if !ok {
		t.Fatal("Parse: want ok")
	}
	want := "Pick one:\n❯ 1. Alpha\n2. Beta"
	if got.Raw != want {
		t.Errorf("Raw = %q, want %q", got.Raw, want)
	}
}

// A numbered list needs at least two enumerated lines to count as a menu; a
// single "1. foo" degrades to free-form (it ends in "?") or is ignored.
func TestSingleEnumeratedLineIsNotAMenu(t *testing.T) {
	got, ok := Parse("Here is the one thing I found:\n1. the config is stale\n", agent.Claude)
	if !ok {
		// acceptable: no question-shaped content at all.
		return
	}
	if len(got.Choices) != 0 {
		t.Errorf("single enumerated line became a menu: %#v", got.Choices)
	}
}

// The question-parse heuristics are claude-code specific, so Parse is gated to
// k==Claude: an input that clearly parses for claude must return (zero, false)
// for codex and opencode. (An empty/legacy kind resolves to claude and DOES
// parse — pre-existing sessions keep today's behavior.)
func TestParseGatedToClaude(t *testing.T) {
	// A claude-style numbered select that parses cleanly under agent.Claude.
	const menu = "╭────────────────────────────╮\n" +
		"│ Do you want to proceed?    │\n" +
		"│ ❯ 1. Yes                   │\n" +
		"│   2. No                    │\n" +
		"╰────────────────────────────╯\n"
	if _, ok := Parse(menu, agent.Claude); !ok {
		t.Fatal("Parse(menu, claude): want ok (fixture must parse for the gate test to be meaningful)")
	}
	for _, k := range []agent.Kind{agent.Codex, agent.OpenCode} {
		if got, ok := Parse(menu, k); ok {
			t.Errorf("Parse(menu, %s) = (%+v, true), want (zero, false): question parse is claude-only", k, got)
		}
	}
	// A legacy/unknown kind resolves to claude and still parses.
	if _, ok := Parse(menu, agent.Kind("")); !ok {
		t.Error(`Parse(menu, "") : want ok (legacy kind resolves to claude)`)
	}
}
