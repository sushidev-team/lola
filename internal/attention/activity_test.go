package attention

import "testing"

// The inputs are real-ish tmux pane tails: box-drawing, the "❯"/">" carets, the
// claude-code status line (spinner glyph + gerund… + elapsed timer + token
// meter + "esc to interrupt"), and interleaved ANSI SGR — exactly what
// CapturePane returns.
func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Activity
	}{
		{
			name: "working: deciphering status line with elapsed timer and token counter, box below",
			in: "\x1b[2m⏺ Reading source files…\x1b[0m\n" +
				"\x1b[36m✳ Deciphering…\x1b[0m \x1b[2m(2m 6s · ↓ 4.5k tokens · esc to interrupt)\x1b[0m\n" +
				"\n" +
				"╭──────────────────────────────────────────────╮\n" +
				"│ > \x1b[2mTry \"edit main.go\"\x1b[0m                        │\n" +
				"╰──────────────────────────────────────────────╯\n",
			want: ActivityWorking,
		},
		{
			name: "working: esc to interrupt is enough on its own",
			in:   "✻ Cerebrating… (esc to interrupt · 4s)\n",
			want: ActivityWorking,
		},
		{
			name: "working: elapsed timer next to a gerund with no other cue",
			in:   "Deciphering… (2m 6s)\n",
			want: ActivityWorking,
		},
		{
			name: "working: streaming token counter with a down arrow",
			in:   "Thinking\n↓ 512 tokens\n",
			want: ActivityWorking,
		},
		{
			name: "working: braille spinner glyph",
			in:   "\x1b[33m⠹\x1b[0m Compiling the project\n",
			want: ActivityWorking,
		},
		{
			name: "working: circle spinner status line",
			in:   "◐ Simmering… on the request\n",
			want: ActivityWorking,
		},
		{
			name: "waiting: numbered select question inside a box",
			in: "\x1b[2m⏺ Done reviewing the diff.\x1b[0m\n" +
				"╭────────────────────────────────────────────────────────╮\n" +
				"│ \x1b[1mDo you want to proceed?\x1b[0m                                 │\n" +
				"│ \x1b[36m❯ 1. Yes\x1b[0m                                                │\n" +
				"│   2. No                                                  │\n" +
				"╰────────────────────────────────────────────────────────╯\n",
			want: ActivityWaiting,
		},
		{
			name: "waiting: empty input box, no question, no spinner",
			in: "╭──────────────────────────────────────────────╮\n" +
				"│ >                                              │\n" +
				"╰──────────────────────────────────────────────╯\n" +
				"  ? for shortcuts\n",
			want: ActivityWaiting,
		},
		{
			name: "waiting: free-form question with a bare prompt caret, ansi laden",
			in:   "\x1b[35mWhat should I name the new migration file?\x1b[0m\n\x1b[2m>\x1b[0m \n",
			want: ActivityWaiting,
		},
		{
			name: "waiting: bare claude caret with editable text, no box",
			in:   "Anything else?\n❯ my draft answer\n",
			want: ActivityWaiting,
		},
		{
			name: "waiting: static context token read-out is NOT working, box makes it waiting",
			in: "\x1b[2mⓘ context left: 12k tokens\x1b[0m\n" +
				"╭────────────────╮\n" +
				"│ >              │\n" +
				"╰────────────────╯\n",
			want: ActivityWaiting,
		},
		{
			name: "unknown: plain scrolled output, no prompt and no spinner",
			in:   "Compiling module foo...\nok  \tgithub.com/foo/bar\t0.123s\nAll tests passed.\n",
			want: ActivityUnknown,
		},
		{
			name: "unknown: go test output with parenthesised fractional seconds is not a working timer",
			in:   "=== RUN   TestFoo\n--- PASS: TestFoo (1.23s)\nPASS\nok  \tgithub.com/foo/bar\t1.234s\n",
			want: ActivityUnknown,
		},
		{
			name: "unknown: lone shell prompt with no box is not a claude input prompt",
			in:   "$ ls\nbuild.log  main.go\n$ \n> \n",
			want: ActivityUnknown,
		},
		{
			name: "unknown: empty pane",
			in:   "\n\n   \n",
			want: ActivityUnknown,
		},
		{
			name: "unknown: markdown bullet list must not read as a spinner status line",
			in:   "Here is the plan:\n● first step\n○ second step\n",
			want: ActivityUnknown,
		},
		{
			// THE BUG: a spinner frame left in scrollback by an earlier command
			// (e.g. `pnpm install`) must NOT keep a resting input box reading as
			// working. The braille glyph is far above the live status cluster, so
			// the working scan (pinned to statusTailLines) never sees it.
			name: "waiting: stale braille spinner frame in scrollback does not beat the resting input box",
			in: "⠹ Installing dependencies\nadded 412 packages\ndone in 12s\n" +
				"\n\n\n\n\n\n\n" +
				"Proceed with the migration?\n" +
				"╭──────────────────────────────────────────────╮\n" +
				"│ >                                              │\n" +
				"╰──────────────────────────────────────────────╯\n" +
				"  ? for shortcuts\n",
			want: ActivityWaiting,
		},
		{
			// Same for the literal "esc to interrupt" phrase an agent renders while
			// editing this very repo (its own source/tests contain the string): a
			// stale occurrence up in scrollback is not live activity.
			name: "waiting: stale 'esc to interrupt' phrase in scrollback does not beat the resting input box",
			in: "Here is activity.go:\n  escInterruptRe matches \"esc to interrupt\"\n" +
				"\n\n\n\n\n\n\n" +
				"Want me to apply the fix?\n" +
				"╭──────────────────────────────────────────────╮\n" +
				"│ >                                              │\n" +
				"╰──────────────────────────────────────────────╯\n",
			want: ActivityWaiting,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.in); got != tc.want {
				t.Errorf("Classify(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

// The load-bearing invariant: Working is only ever returned on a positive cue.
// Any pane that lacks every working cue must classify as Waiting or Unknown,
// never Working — this is the exact bug (working shown while actually waiting)
// the classifier exists to prevent.
func TestClassifyNeverWorkingWithoutCue(t *testing.T) {
	noCue := []string{
		"",
		"\n\n   \n",
		"Compiling module foo...\nAll tests passed.\n",
		"--- PASS: TestFoo (1.23s)\nPASS\n",      // fractional-second timer, no ellipsis
		"used about 5 tokens for that request\n", // "tokens" without a streaming arrow
		"$ ls\nmain.go\n$ \n",                    // shell prompt
		"● done\n○ todo\n",                       // bullets, not a spinner status
		"╭────╮\n│ >  │\n╰────╯\n",               // idle input box
		"What should I do next?\n> \n",           // free-form question at rest
	}
	for _, in := range noCue {
		if got := Classify(in); got == ActivityWorking {
			t.Errorf("Classify(%q) = working, but there is no working cue", in)
		}
	}
}

// The braille and arrow-token cues must survive being wrapped in dense ANSI SGR,
// since real captures color every token of the status line.
func TestClassifyWorkingCuesSurviveANSI(t *testing.T) {
	ansiWorking := []string{
		"\x1b[38;5;208m\x1b[1m⠋\x1b[0m\x1b[2m Herding llamas\x1b[0m\n",
		"\x1b[2m(\x1b[0m\x1b[36m12s\x1b[0m \x1b[2m·\x1b[0m \x1b[33m↓ 2.1k tokens\x1b[0m \x1b[2m· esc to interrupt)\x1b[0m\n",
	}
	for _, in := range ansiWorking {
		if got := Classify(in); got != ActivityWorking {
			t.Errorf("Classify(%q) = %s, want working", in, got)
		}
	}
}
