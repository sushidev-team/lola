// Package sshterm is what lets a human's long-running shell and lola's one-shot exec
// channels read as one place, with nothing installed on the host. It is pure text and
// a stdlib leaf:
//
//   - Bootstrap is the remote command that starts the human's bash with two hooks,
//     which print a private escape sequence before and after each command line.
//   - Parser turns a pane's raw output into command-start, output and command-end
//     events. It is what the daemon feeds from tmux pipe-pane.
//   - CwdFilter and Tracked do the small equivalent for an exec channel: report the
//     directory a one-shot command ended in.
//   - Elevated and OpensShell recognize a command that raises its rights.
//
// Everything a remote program prints is untrusted. The nonce stops ordinary output
// from reading as a marker, but a program on the host could still learn it, so an
// event is display material: a private block and a working directory, never a decision.
package sshterm

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
)

// oscCode is the escape sequence's private number. tmux swallows unknown OSC codes, so
// the markers never appear on anyone's screen.
const oscCode = "6973"

// NewNonce makes a marker secret.
func NewNonce() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

const rcEnd = "LOLA_RC_EOF"

// Bootstrap is the script for `sh -c` that starts the interactive shell. Only bash gets
// hooks. Any other login shell, or a host with no bash, gets its ordinary login shell
// and simply never reports, which the daemon reads as "not tracked".
//
// The rc arrives on a file descriptor, so nothing is written to the host. It reads the
// login files itself, because --rcfile replaces them. HISTCONTROL is set, because the
// hook finds the command line by history number: a repeated command must get one, and a
// line starting with a space must not, which is how a human keeps a command out of the
// timeline.
func Bootstrap(nonce string) string {
	rc := `exec 3<&-
[ -r /etc/profile ] && . /etc/profile
if [ -r ~/.bash_profile ]; then . ~/.bash_profile
elif [ -r ~/.bash_login ]; then . ~/.bash_login
elif [ -r ~/.profile ]; then . ~/.profile
fi
HISTCONTROL=ignorespace
__lola_hex() { printf %s "$1" | od -An -v -tx1 | tr -d ' \n'; }
__lola_ready=
__lola_hist=
__lola_precmd() {
  printf '\033]` + oscCode + `;NONCE;P;%s;%s\007' "$__lola_rc" "$(__lola_hex "$PWD")"
  __lola_ready=1
}
__lola_preexec() {
  [ -n "$__lola_ready" ] || return 0
  [ -z "${COMP_LINE:-}" ] || return 0
  __lola_ready=
  local h n
  h=$(HISTTIMEFORMAT= builtin history 1)
  h=${h#"${h%%[![:space:]]*}"}
  n=${h%%[[:space:]]*}
  [ "$n" != "$__lola_hist" ] || return 0
  __lola_hist=$n
  h=${h#"$n"}
  h=${h#"${h%%[![:space:]]*}"}
  printf '\033]` + oscCode + `;NONCE;C;%s\007' "$(__lola_hex "$h")"
}
PROMPT_COMMAND='__lola_rc=$?; '"${PROMPT_COMMAND:+$PROMPT_COMMAND; }"'__lola_precmd'
trap '__lola_preexec' DEBUG
`
	rc = strings.ReplaceAll(rc, "NONCE", nonce)
	return `case "${SHELL##*/}" in bash) ;; *) exec "${SHELL:-/bin/sh}" -l;; esac
command -v bash >/dev/null 2>&1 || exec "$SHELL" -l
exec bash --rcfile /dev/fd/3 -i 3<<'` + rcEnd + "'\n" + rc + rcEnd + "\n"
}

// Events are what a Parser reports. Every field may be nil.
type Events struct {
	Start  func(command string)
	Output func(p []byte)
	// End closes the current command, if there is one. A prompt with no command before
	// it (the first one, or a line kept out of history) arrives with command false.
	End func(exit int, cwd string, command, fullScreen bool)
}

var (
	altScreenOn  = []byte("\x1b[?1049h")
	altScreenOld = []byte("\x1b[?47h")
	maxPending   = 64 * 1024
)

// Parser is fed a pane's raw bytes in any chunking.
type Parser struct {
	ev         Events
	prefix     []byte
	pending    []byte
	inCommand  bool
	fullScreen bool
}

// NewParser recognizes only markers carrying nonce.
func NewParser(nonce string, ev Events) *Parser {
	return &Parser{ev: ev, prefix: []byte("\x1b]" + oscCode + ";" + nonce + ";")}
}

// Feed consumes p. Output outside a command (the prompt, the human's typing) is dropped.
func (p *Parser) Feed(b []byte) {
	p.pending = append(p.pending, b...)
	for {
		i := bytes.Index(p.pending, p.prefix)
		if i < 0 {
			break
		}
		end := bytes.IndexByte(p.pending[i:], 0x07)
		if end < 0 {
			if len(p.pending)-i > maxPending { // not a marker of ours: give up on it
				p.emit(p.pending[:i+len(p.prefix)])
				p.pending = append(p.pending[:0], p.pending[i+len(p.prefix):]...)
				continue
			}
			p.emit(p.pending[:i])
			p.pending = append(p.pending[:0], p.pending[i:]...)
			return
		}
		p.emit(p.pending[:i])
		p.marker(string(p.pending[i+len(p.prefix) : i+end]))
		p.pending = append(p.pending[:0], p.pending[i+end+1:]...)
	}
	// Keep back what could be the start of a marker split across two reads.
	keep := len(p.prefix) - 1
	if keep > len(p.pending) {
		keep = len(p.pending)
	}
	for ; keep > 0; keep-- {
		if bytes.HasPrefix(p.prefix, p.pending[len(p.pending)-keep:]) {
			break
		}
	}
	p.emit(p.pending[:len(p.pending)-keep])
	p.pending = append(p.pending[:0], p.pending[len(p.pending)-keep:]...)
}

func (p *Parser) emit(b []byte) {
	if !p.inCommand || len(b) == 0 {
		return
	}
	if bytes.Contains(b, altScreenOn) || bytes.Contains(b, altScreenOld) {
		p.fullScreen = true
	}
	if p.fullScreen || p.ev.Output == nil {
		return
	}
	p.ev.Output(b)
}

func (p *Parser) marker(m string) {
	kind, rest, _ := strings.Cut(m, ";")
	switch kind {
	case "C":
		raw, err := hex.DecodeString(rest)
		if err != nil {
			return
		}
		p.inCommand, p.fullScreen = true, false
		if p.ev.Start != nil {
			p.ev.Start(string(raw))
		}
	case "P":
		rcText, cwdHex, _ := strings.Cut(rest, ";")
		rc, err := strconv.Atoi(rcText)
		if err != nil {
			rc = 0
		}
		raw, err := hex.DecodeString(cwdHex)
		if err != nil {
			return
		}
		was, full := p.inCommand, p.fullScreen
		p.inCommand, p.fullScreen = false, false
		if p.ev.End != nil {
			p.ev.End(rc, string(raw), was, full)
		}
	}
}

// Tracked wraps a POSIX shell body so it reports its final directory on stderr. The
// trap is set before anything else, so a failed cd and an explicit exit both report, and
// an EXIT trap keeps the command's own exit status. A command that replaces the shell
// (exec) or sets its own EXIT trap reports nothing, and the directory just stays.
func Tracked(nonce, body string) string {
	return "trap 'printf \"" + cwdPrefix(nonce) + "%s\\n\" \"$PWD\" >&2' EXIT\n" + body
}

func cwdPrefix(nonce string) string { return "__LOLA_CWD_" + nonce + "__:" }

// CwdFilter sits in front of the stderr sink: it removes the Tracked line, wherever a
// chunk boundary falls, and keeps what it said.
type CwdFilter struct {
	w      interface{ Write([]byte) (int, error) }
	prefix []byte
	hold   []byte
	cwd    string
}

// NewCwdFilter passes everything but the marker through to w.
func NewCwdFilter(nonce string, w interface{ Write([]byte) (int, error) }) *CwdFilter {
	return &CwdFilter{w: w, prefix: []byte(cwdPrefix(nonce))}
}

// Write always reports the whole of p, because a failing sink has its own accounting.
func (f *CwdFilter) Write(p []byte) (int, error) {
	f.hold = append(f.hold, p...)
	for {
		i := bytes.Index(f.hold, f.prefix)
		if i < 0 {
			break
		}
		nl := bytes.IndexByte(f.hold[i:], '\n')
		if nl < 0 {
			f.pass(f.hold[:i])
			f.hold = append(f.hold[:0], f.hold[i:]...)
			return len(p), nil
		}
		f.pass(f.hold[:i])
		f.cwd = string(f.hold[i+len(f.prefix) : i+nl])
		f.hold = append(f.hold[:0], f.hold[i+nl+1:]...)
	}
	keep := min(len(f.prefix)-1, len(f.hold))
	for ; keep > 0; keep-- {
		if bytes.HasPrefix(f.prefix, f.hold[len(f.hold)-keep:]) {
			break
		}
	}
	f.pass(f.hold[:len(f.hold)-keep])
	f.hold = append(f.hold[:0], f.hold[len(f.hold)-keep:]...)
	return len(p), nil
}

func (f *CwdFilter) pass(b []byte) {
	if len(b) > 0 {
		_, _ = f.w.Write(b)
	}
}

// Close flushes what was held back, and returns the reported directory.
func (f *CwdFilter) Close() string {
	f.pass(f.hold)
	f.hold = nil
	return f.cwd
}

var (
	elevatedRe = regexp.MustCompile(`(^|[;&|(]\s*|\b(exec|xargs|env|nohup|time)\s+)(sudo|su|doas|pkexec)(\s|$)`)
	shellRe    = regexp.MustCompile(`^\s*(sudo(\s+-\S+)*\s+(-i|-s|su|bash|sh|zsh)(\s|$)|sudo(\s+-\S+)*\s+-[A-Za-z]*[is][A-Za-z]*(\s|$)|su(\s|$)|doas\s+-s(\s|$))`)
)

// Elevated reports a command that visibly raises its rights. It is a label for a human
// to read, so it can miss a script that elevates inside, and it is never a safety check.
func Elevated(command string) bool { return elevatedRe.MatchString(command) }

// OpensShell reports an elevated command that stays open as a shell of its own. The
// nested shell has none of lola's hooks, so everything until it exits is one entry.
func OpensShell(command string) bool { return shellRe.MatchString(command) }
