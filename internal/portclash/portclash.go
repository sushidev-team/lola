// Package portclash answers one question about a dead dev tab's pane: did the
// command die because the PORT it wanted was already taken, and which port was
// it?
//
// It exists for the same reason internal/devurl does, and obeys the same rules.
// lola cannot know what port a project's dev_commands bind — the port lives
// inside `composer dev`, not in config — so when a tab exits instantly the only
// evidence is the text the command printed. Every server words that failure
// differently ("bind: address already in use", "EADDRINUSE", "Port 9245 is in
// use", "port is already allocated"), so the CUE has to be the phrase plus the
// address beside it.
//
// Pane text is UNTRUSTED (a log line, a diff, an agent's output can say
// anything), which is why the only thing that ever leaves this package is an
// integer in 1..65535. The caller's next act is asking lsof about that port and
// offering a human a kill button, so a number is the whole safe surface: no
// command name, no path, no message is carried out of here.
//
// It FAILS CLOSED. A phrase it does not know, a message that names no port
// (Python's bare "[Errno 48] Address already in use"), anything ambiguous:
// nothing is reported, and the dev tab is simply shown as dead the way it was
// before.
package portclash

import (
	"regexp"
	"strconv"
	"strings"
)

// MaxScanLines bounds how much of a pane is examined, counted from the BOTTOM.
// The message that killed the command is the last thing it printed, and a cap
// keeps a long scrollback from turning into a long scan.
const MaxScanLines = 400

// maxPort is the highest legal TCP port. A "port" outside 1..65535 is a number
// this package matched by accident, not an address.
const maxPort = 65535

// inUseRe is the PHRASE half of the cue: the line must actually be saying the
// address is taken. Deliberately a whitelist of the wordings real servers use,
// because a looser test ("in use") matches ordinary prose in an agent's pane.
var inUseRe = regexp.MustCompile(`(?i)(address (already )?in use|eaddrinuse|already in use|already allocated|already bound|in use by another)`)

// The ADDRESS half, in order of how certain each is. A port that sits behind a
// host ("127.0.0.1:9245", "[::1]:5173", "*:3000") is unmistakable; a port named
// in words ("Port 9245 is in use") is next; a bare ":9245" is the last resort,
// and is what a timestamp in the same line could also produce — hence the order,
// never the reverse.
var (
	hostPortRe = regexp.MustCompile(`(?i)(?:\d{1,3}(?:\.\d{1,3}){3}|\[[0-9a-f:]*\]|\*|localhost|:):(\d{1,5})(?:\D|$)`)
	wordPortRe = regexp.MustCompile(`(?i)\bport[:= ]+"?(\d{1,5})"?\b`)
	barePortRe = regexp.MustCompile(`:(\d{1,5})(?:\D|$)`)
)

// Port reports the port a pane says is already taken.
//
// The pane is read from the BOTTOM up and the first line carrying both halves of
// the cue wins: a tab that failed, was restarted and failed again on another
// port must report the latest failure, not the first one still sitting in the
// scrollback.
func Port(pane string) (int, bool) {
	lines := strings.Split(pane, "\n")
	if len(lines) > MaxScanLines {
		lines = lines[len(lines)-MaxScanLines:]
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if !inUseRe.MatchString(line) {
			continue
		}
		if port, ok := portIn(line); ok {
			return port, true
		}
	}
	return 0, false
}

// portIn pulls the port out of one line that has already been established to be
// an "address in use" message. Within a family the LAST match wins: a message
// that names both a socket it tried and one it fell back to ends with the one
// that failed.
func portIn(line string) (int, bool) {
	for _, re := range []*regexp.Regexp{hostPortRe, wordPortRe, barePortRe} {
		matches := re.FindAllStringSubmatch(line, -1)
		for i := len(matches) - 1; i >= 0; i-- {
			port, err := strconv.Atoi(matches[i][1])
			if err != nil || port <= 0 || port > maxPort {
				continue
			}
			return port, true
		}
	}
	return 0, false
}
