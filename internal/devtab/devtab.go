// Package devtab holds the ONE naming convention for a session's DEV tabs: the
// tmux sessions running a project's `dev_commands` (`composer dev`, `npm run
// dev`) beside the agent pane.
//
// It exists for the same reason internal/lolaenv does — four packages have to
// agree on the shape and none of them may import the others: the daemon creates
// the tabs, internal/runtime must recognize them as auxiliary sessions (so
// adoption ignores them and teardown sweeps them), and both the TUI and the
// desktop app discover them as terminal tabs. Stdlib only.
//
// The name is "<sessionID>-dev-<n>", n being the 1-BASED position of the command
// in the project's dev_commands list — so a tab's index alone says which command
// it runs, which is what lets a surface label the tab with the command text
// without asking the daemon what is in each pane.
package devtab

import (
	"regexp"
	"strconv"
	"strings"
)

// Suffix is what a dev tab's name carries beyond its parent session's ID, minus
// the index ("lola-app-eng-1" + "-dev-" + "2").
const Suffix = "-dev-"

// nameRe matches a dev-tab tmux session name. Anchored at the end and requiring
// at least one digit, so a hand-made session called "something-dev" is NOT one
// of ours and neither is a session whose ID merely contains "-dev-".
var nameRe = regexp.MustCompile(`-dev-\d+$`)

// Name is the tmux session name of the i-th (1-based) dev tab of sessionID.
func Name(sessionID string, i int) string {
	return sessionID + Suffix + strconv.Itoa(i)
}

// Is reports whether a tmux session name is SOME session's dev tab. Used where
// only the shape matters (adoption, teardown), never to bind a tab to a parent —
// see Index for that, which is prefix-anchored on both ends.
func Is(name string) bool { return nameRe.MatchString(name) }

// Index returns the 1-based command index of name when it is exactly
// sessionID's dev tab, and 0 otherwise. The prefix is matched against the FULL
// session ID rather than by a suffix test, because "lola-fe-4" is a prefix of
// "lola-fe-42-dev-1" — the same trap internal/runtime's isAuxOf guards.
func Index(sessionID, name string) int {
	rest, ok := strings.CutPrefix(name, sessionID+Suffix)
	if !ok || rest == "" {
		return 0
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}
