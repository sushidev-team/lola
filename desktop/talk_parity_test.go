package main

// Anti-drift pin for the conversation's first paint. A session view has to decide where to open
// before any talkState has answered, so lib/sessionview.ts carries its own list of the agents
// whose log lola can read. If that list says yes for an agent internal/agent has not verified, the
// session opens on a story that will never come. If it says no for one that has been verified, the
// new reader is never reached. Nothing else can notice the two parting ways, because they share no
// compiler.

import (
	"os"
	"regexp"
	"slices"
	"testing"

	"github.com/sushidev-team/lola/internal/agent"
)

const sessionViewTS = "frontend/src/lib/sessionview.ts"

func TestFeedAgentsMatchCaps(t *testing.T) {
	src, err := os.ReadFile(sessionViewTS)
	if err != nil {
		t.Fatalf("read %s: %v", sessionViewTS, err)
	}
	m := regexp.MustCompile(`const FEED_AGENTS = new Set\(\[(.*?)\]\)`).FindSubmatch(src)
	if m == nil {
		t.Fatalf("%s: FEED_AGENTS not found, so this test stopped guarding anything", sessionViewTS)
	}
	var got []string
	for _, q := range regexp.MustCompile(`"([a-z]*)"`).FindAllSubmatch(m[1], -1) {
		got = append(got, string(q[1]))
	}
	slices.Sort(got)

	// An empty agent is an older record, and it reads as the default agent everywhere.
	var want []string
	if agent.Caps(agent.Parse("")).Feed {
		want = append(want, "")
	}
	for _, k := range []agent.Kind{agent.Claude, agent.Codex, agent.OpenCode} {
		if agent.Caps(k).Feed {
			want = append(want, string(k))
		}
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("FEED_AGENTS is %q, and agent.Caps says %q", got, want)
	}
}

// The flip is what the whole design waited on, and the constant is one word in one file. A revert
// by accident would quietly send every session back to its terminal.
func TestConversationIsTheDefault(t *testing.T) {
	src, err := os.ReadFile(sessionViewTS)
	if err != nil {
		t.Fatalf("read %s: %v", sessionViewTS, err)
	}
	if !regexp.MustCompile(`export const CONVERSATION_FOR_ALL = true;`).Match(src) {
		t.Fatalf("%s: CONVERSATION_FOR_ALL is not true", sessionViewTS)
	}
}
