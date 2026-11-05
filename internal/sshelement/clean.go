package sshelement

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Line sanitizes s to one display line of at most n runes: invalid UTF-8 and
// every control or format character dropped (ESC heads an ANSI sequence, a bidi
// override reorders what a human approves), whitespace runs collapsed, trimmed,
// and an over-long result ended with "…". ANSI sequences are removed whole, not
// merely their ESC, so no "[31m" debris survives.
func Line(s string, n int) string {
	s = stripSequences(s)
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			space = true
			continue
		case unsafeRune(r):
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return clipRunes(b.String(), n)
}

// Para sanitizes s to at most n runes of multi-line text: like Line, but single
// newlines and tabs survive, CR becomes a newline, and blank-line runs collapse
// to one.
func Para(s string, n int) string {
	s = stripSequences(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var b strings.Builder
	newlines := 0
	for _, r := range s {
		switch {
		case r == '\n':
			newlines++
			if newlines <= 2 {
				b.WriteByte('\n')
			}
			continue
		case r == '\t':
		case unsafeRune(r):
			continue
		}
		newlines = 0
		b.WriteRune(r)
	}
	return clipRunes(strings.TrimSpace(b.String()), n)
}

// stripSequences removes ANSI escape sequences whole and maps invalid UTF-8 to
// U+FFFD, with the same state machine the output path uses.
func stripSequences(s string) string {
	var c Cleaner
	out := c.Write([]byte(s))
	return string(append(out, c.Flush()...))
}

func unsafeRune(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r)
}

func clipRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// Cleaner is the streaming sanitizer for REMOTE OUTPUT. Remote bytes are
// untrusted and arrive in arbitrary chunks, so an escape sequence, a CRLF or a
// UTF-8 rune may straddle two reads; that state lives here.
//
// Kept: printable text, newline, tab. CRLF and lone CR become one newline (a
// progress bar reads as lines rather than overwriting itself). Invalid UTF-8
// becomes U+FFFD. Dropped: ANSI CSI/OSC/DCS/SOS/PM/APC sequences whole, other C0
// and C1 controls, and format characters such as bidi overrides. A NUL also sets
// Binary so a UI can say so instead of rendering debris.
//
// The zero value is ready. Not safe for concurrent use: one per stream.
type Cleaner struct {
	state   cleanState
	cr      bool
	partial []byte
	seqLen  int
	// Binary is set once a NUL byte has been seen in this stream.
	Binary bool
}

type cleanState uint8

const (
	stText cleanState = iota
	stEsc
	stEscIntermediate
	stCSI
	stOSC    // ended by BEL or ST
	stString // DCS, SOS, PM, APC: ended by ST only
	stStrEsc // saw ESC inside an OSC or a string: `\` ends it
)

// maxSequenceBytes bounds how much one unterminated escape sequence can swallow,
// so `printf '\033]'` followed by real output cannot make that output vanish.
const maxSequenceBytes = 4096

// Write returns the cleaned form of p. Bytes held back at a chunk boundary come
// out with the next Write or with Flush.
func (c *Cleaner) Write(p []byte) []byte {
	if len(c.partial) > 0 {
		joined := make([]byte, 0, len(c.partial)+len(p))
		joined = append(append(joined, c.partial...), p...)
		p, c.partial = joined, nil
	}
	out := make([]byte, 0, len(p))
	for i := 0; i < len(p); {
		b := p[i]
		switch c.state {
		case stEsc:
			i++
			c.seqLen = 0
			switch {
			case b == '[':
				c.state = stCSI
			case b == ']':
				c.state = stOSC
			case b == 'P' || b == 'X' || b == '^' || b == '_':
				c.state = stString
			case b >= 0x20 && b <= 0x2F:
				c.state = stEscIntermediate
			case b == 0x1b:
				// ESC ESC: still at the head of a sequence.
			default:
				c.state = stText
			}
			continue
		case stEscIntermediate:
			i++
			if b >= 0x30 && b <= 0x7E {
				c.state = stText
			}
			continue
		case stCSI:
			i++
			c.seqLen++
			if (b >= 0x40 && b <= 0x7E) || c.seqLen > maxSequenceBytes {
				c.state = stText
			}
			continue
		case stOSC, stString:
			i++
			c.seqLen++
			switch {
			case b == 0x1b:
				c.state = stStrEsc
			case b == 0x07 && c.state == stOSC:
				c.state = stText
			case c.seqLen > maxSequenceBytes:
				c.state = stText
			}
			continue
		case stStrEsc:
			if b == '\\' {
				i++
				c.state = stText
				continue
			}
			c.state = stEsc // a new sequence aborted the string; reprocess b
			continue
		}

		// stText.
		if b == '\r' {
			c.cr = true
			i++
			continue
		}
		if c.cr {
			c.cr = false
			out = append(out, '\n')
			if b == '\n' {
				i++
				continue
			}
		}
		if b < utf8.RuneSelf {
			i++
			switch {
			case b == 0x1b:
				c.state = stEsc
			case b == '\n' || b == '\t':
				out = append(out, b)
			case b == 0:
				c.Binary = true
			case b < 0x20 || b == 0x7f:
				// dropped
			default:
				out = append(out, b)
			}
			continue
		}
		r, size := utf8.DecodeRune(p[i:])
		if r == utf8.RuneError && size == 1 {
			if !utf8.FullRune(p[i:]) {
				c.partial = append([]byte(nil), p[i:]...)
				return out
			}
			out = utf8.AppendRune(out, utf8.RuneError)
			i++
			continue
		}
		i += size
		if !unsafeRune(r) {
			out = utf8.AppendRune(out, r)
		}
	}
	return out
}

// Flush ends the stream: a held-back partial rune becomes U+FFFD and a trailing
// CR becomes a newline. An unterminated escape sequence stays dropped.
func (c *Cleaner) Flush() []byte {
	var out []byte
	if c.cr {
		out = append(out, '\n')
		c.cr = false
	}
	if len(c.partial) > 0 {
		out = utf8.AppendRune(out, utf8.RuneError)
		c.partial = nil
	}
	c.state = stText
	return out
}

// ErrUndisplayable marks a command or path a human could not read faithfully.
var ErrUndisplayable = errors.New("contains characters that cannot be shown faithfully")

// ValidateCommand decides whether cmd may ever reach an approval dialog.
//
// It REFUSES rather than cleans, and that is the point: the approval screen must
// show the exact bytes that will run. Stripping an ESC or a bidi override from
// the display while running the original would let a command show one thing and
// do another; stripping it from the command too would run something nobody
// wrote. Newlines and tabs are allowed, since multi-line commands are real.
//
// This is a display-integrity check. It says nothing about whether the command
// is safe, and must never be presented as a safety check.
func ValidateCommand(cmd string) error {
	if strings.TrimSpace(cmd) == "" {
		return errors.New("command is empty")
	}
	if len(cmd) > MaxCommandBytes {
		return fmt.Errorf("command is %d bytes, the limit is %d", len(cmd), MaxCommandBytes)
	}
	return checkDisplayable("command", cmd, true)
}

// ValidateCwd decides whether a remote working directory may be used. Empty
// means none. It is one line and shown in the approval too.
func ValidateCwd(cwd string) error {
	if cwd == "" {
		return nil
	}
	if len(cwd) > MaxCwdBytes {
		return fmt.Errorf("cwd is %d bytes, the limit is %d", len(cwd), MaxCwdBytes)
	}
	return checkDisplayable("cwd", cwd, false)
}

func checkDisplayable(what, s string, multiline bool) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("%s is not valid UTF-8: %w", what, ErrUndisplayable)
	}
	for _, r := range s {
		if multiline && (r == '\n' || r == '\t') {
			continue
		}
		if unsafeRune(r) {
			return fmt.Errorf("%s has U+%04X: %w", what, r, ErrUndisplayable)
		}
	}
	return nil
}
