// Package sshtab holds the ONE naming convention for a session's SSH tabs: the
// tmux sessions running real ssh beside the agent pane.
//
// It exists for the reason internal/devtab does. The daemon creates the tabs,
// internal/runtime must recognize them as auxiliary sessions (so adoption ignores
// them and teardown sweeps them), and both the TUI and the desktop app discover
// them as terminal tabs, and none of those may import the others. Stdlib only.
//
// The name is "<sessionID>-ssh-<n>", n 1-based. A number identifies a tab, not a
// host: a session on two hosts has two tabs, and a reopened terminal takes the
// next free number. The wire's SSHConnInfo.Tab says which connection owns which.
//
// The phone deliberately does NOT know this suffix. internal/daemon's
// paneAuxSuffixRe and panebus resolve remote panes, and a shell on a production
// host is not a phone job, so leave them out of this vocabulary.
package sshtab

import (
	"regexp"
	"strconv"
	"strings"
)

const infix = "-ssh-"

var nameRe = regexp.MustCompile(`-ssh-[1-9][0-9]*$`)

// Name is the tmux session name of a session's nth SSH tab (1-based).
func Name(sessionID string, n int) string { return sessionID + infix + strconv.Itoa(n) }

// Is reports whether name is the SHAPE of an SSH tab of some session. A shape is not
// proof: issue SSH-42 of project infra is the session "lola-infra-ssh-42". Whatever
// decides a session's fate must also check Parent.
func Is(name string) bool { return nameRe.MatchString(name) }

// Parent is the session an SSH tab of this shape would belong to.
func Parent(name string) (string, bool) {
	loc := nameRe.FindStringIndex(name)
	if loc == nil || loc[0] == 0 {
		return "", false
	}
	return name[:loc[0]], true
}

// Index returns the tab number of name when it is an SSH tab of exactly this
// session, else 0. It is anchored at both ends: "lola-fe-4" is a prefix of
// "lola-fe-42-ssh-1", and a loose match would let one session's teardown kill a
// sibling's terminal.
func Index(sessionID, name string) int {
	rest, ok := strings.CutPrefix(name, sessionID+infix)
	if !ok || rest == "" || rest[0] == '0' {
		return 0
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n <= 0 || strconv.Itoa(n) != rest {
		return 0
	}
	return n
}
