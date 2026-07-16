package attention

import (
	"testing"

	"github.com/sushidev-team/lola/internal/agent"
)

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
		{
			// THE REPORTED BUG: the agent asked a plain-text question and yielded;
			// its COMPLETED status line still shows a token counter (a weak working
			// cue) right next to the resting caret. That frozen counter must NOT keep
			// the pane reading as working — a resting prompt beats the weak cues.
			name: "waiting: completed status line with a frozen token counter beside a resting caret is waiting",
			in: "Can you unlock 1Password, then tell me to continue?\n" +
				"\n" +
				"Alternatively, say so and I'll pass --no-gpg-sign.\n" +
				"\n" +
				"✳ Cooked for 5m 59s · ↑ 12.5k tokens\n" +
				"✗ Auto-update failed · Run claude doctor\n" +
				"> \n",
			want: ActivityWaiting,
		},
		{
			// A box-free bare caret (no input box captured) is still the resting
			// prompt of a claude pane once no live cue is present.
			name: "waiting: box-free bare caret after a question is a resting prompt",
			in:   "Should I proceed with the rename?\n> \n",
			want: ActivityWaiting,
		},
		{
			// esc-to-interrupt precedence: a genuinely streaming turn wins even if a
			// caret is somehow on screen — the one unambiguous live cue.
			name: "working: live 'esc to interrupt' wins over a caret on screen",
			in:   "✳ Baking… (12s · ↑ 3.2k tokens · esc to interrupt)\n> \n",
			want: ActivityWorking,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.in, agent.Claude); got != tc.want {
				t.Errorf("Classify(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

// Codex renders a different status line — a "Working" verb followed by an
// elapsed timer ("47s", "4m 07s", or "(1s • esc to interrupt)") — and frames its
// resting composer in a bordered box like claude's. These cases exercise the
// codex-only working cue and the SHARED box/esc cues, and confirm the codex
// pane never trips the claude-only carets/question parse.
func TestClassifyCodex(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Activity
	}{
		{
			name: "working: Working verb with a bare elapsed timer",
			in:   "▌ Working 47s\n",
			want: ActivityWorking,
		},
		{
			name: "working: Working verb with a minute+second elapsed timer",
			in:   "Working 4m 07s\n",
			want: ActivityWorking,
		},
		{
			name: "working: Working verb with the parenthesised esc-to-interrupt timer",
			in:   "▌ Working (1s • esc to interrupt)\n",
			want: ActivityWorking,
		},
		{
			name: "working: esc to interrupt alone is the shared live cue",
			in:   "codex is thinking (12s • esc to interrupt)\n",
			want: ActivityWorking,
		},
		{
			name: "waiting: resting composer box with no working cue",
			in: "▌ Applied the patch to main.go.\n" +
				"╭──────────────────────────────────────────────╮\n" +
				"│ > \x1b[2mSend a message\x1b[0m                            │\n" +
				"╰──────────────────────────────────────────────╯\n",
			want: ActivityWaiting,
		},
		{
			name: "waiting: a completed 'Working 4m 07s' beside a resting composer box does not win",
			in: "Working 4m 07s\n" +
				"╭──────────────────────────────────────────────╮\n" +
				"│ >                                              │\n" +
				"╰──────────────────────────────────────────────╯\n",
			want: ActivityWaiting,
		},
		{
			name: "unknown: 'Working on it, took 5s' prose is not the codex status line",
			in:   "Working on it, took 5s to compile\nAll good.\n",
			want: ActivityUnknown,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.in, agent.Codex); got != tc.want {
				t.Errorf("Classify(%q, codex) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

// OpenCode's cues overlap claude's: the braille spinner and "esc to interrupt"
// while working, a bordered input box while waiting. It has no bespoke working
// cue, so it relies entirely on the SHARED set, plus its "▣" post-turn metadata
// line as a waiting corroborator.
func TestClassifyOpenCode(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Activity
	}{
		{
			name: "working: braille spinner status line",
			in:   "\x1b[33m⠹\x1b[0m Working on the request\n",
			want: ActivityWorking,
		},
		{
			name: "working: esc to interrupt shared cue",
			in:   "⠋ Editing files… (esc to interrupt)\n",
			want: ActivityWorking,
		},
		{
			name: "waiting: bordered input box at rest",
			in: "Made the requested edits.\n" +
				"╭──────────────────────────────────────────────╮\n" +
				"│ >                                              │\n" +
				"╰──────────────────────────────────────────────╯\n",
			want: ActivityWaiting,
		},
		{
			name: "waiting: post-turn metadata line corroborates a yielded turn",
			in:   "Done.\n▣ 1.2k tokens · $0.03 · 8s\n",
			want: ActivityWaiting,
		},
		{
			name: "unknown: plain scrolled output with no cue",
			in:   "reading src/index.ts\nreading src/app.ts\n",
			want: ActivityUnknown,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.in, agent.OpenCode); got != tc.want {
				t.Errorf("Classify(%q, opencode) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

// The claude-only cues must NOT fire for codex/opencode: a "❯" caret, a gerund
// timer, the arrow token meter and the circle/star spinner are claude-code
// rendering specifics, so a codex/opencode pane showing only one of them must
// NOT be read as working (and a "❯" caret alone must not read as waiting).
func TestClassifyClaudeCuesGatedToClaude(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		notWant Activity
	}{
		{"gerund timer is claude-only", "Deciphering… (2m 6s)\n", ActivityWorking},
		{"arrow token meter is claude-only", "Thinking\n↓ 512 tokens\n", ActivityWorking},
		{"circle spinner is claude-only", "◐ Simmering… on the request\n", ActivityWorking},
		{"bare ❯ caret alone is claude-only", "Anything else?\n❯ my draft answer\n", ActivityWaiting},
	}
	for _, k := range []agent.Kind{agent.Codex, agent.OpenCode} {
		for _, tc := range cases {
			t.Run(k.String()+": "+tc.name, func(t *testing.T) {
				if got := Classify(tc.in, k); got == tc.notWant {
					t.Errorf("Classify(%q, %s) = %s, but that cue is claude-only", tc.in, k, got)
				}
			})
		}
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
		if got := Classify(in, agent.Claude); got == ActivityWorking {
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
		if got := Classify(in, agent.Claude); got != ActivityWorking {
			t.Errorf("Classify(%q) = %s, want working", in, got)
		}
	}
}
