package sshterm

import (
	"regexp"
	"strings"
	"unicode"
)

// Prompt kinds. A secret takes a masked field and a confirm takes two buttons.
const (
	PromptSecret  = "secret"
	PromptConfirm = "confirm"
)

// Prompt is a question a program seems to be asking a person. Everything in it is text a
// remote program printed, so it may be a fake: it is shown to a human, word for word, and
// never decides anything.
type Prompt struct {
	Kind string
	// Line is the question, cleaned of escape sequences and invisible characters.
	Line string
	// Word is what answers yes to a confirm: "yes" or "y". The no is the same length.
	Word string
	// Context is the text just above a question that needs it to be answered, such as a
	// host key's fingerprint. A plain password question has none.
	Context []string
}

const (
	maxPromptRunes  = 240
	maxContextLines = 5
	maxContextRunes = 200
)

var (
	termSeqRe = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[0-9;:?<=>]*[ -/]*[@-~]|\x1b[()][0-9A-Za-z]|\x1b[@-_a-z=>]`)

	confirmRe     = regexp.MustCompile(`(?i)[(\[]\s*(yes\s*/\s*no|y\s*/\s*n)(\s*/\s*\[?fingerprint\]?)?\s*[)\]]\s*[?:]?\s*$`)
	typeYesRe     = regexp.MustCompile(`(?i)\btype\s+['"]?yes['"]?.*[:?]\s*$`)
	passwordRe    = regexp.MustCompile(`(?i)(^|[^a-z])(password|passwd|passphrase|passcode)\b[^:?>]*[:?>]\s*$`)
	pinRe         = regexp.MustCompile(`(?i)(^|[^a-z])pin\b[^:?>]*[:?>]\s*$`)
	codeRe        = regexp.MustCompile(`(?i)(^|[^a-z])((verification|security|authentication|authenticator|authorization|one[- ]time|two[- ]factor|2fa|mfa|sms)\s+(code|token|pin|password)|otp|totp|2fa|mfa)\b[^:?>]*[:?>]\s*$`)
	bareCodeRe    = regexp.MustCompile(`(?i)^\s*(please\s+)?(enter\s+)?(your\s+)?(code|token)\b[^:?>]*[:?>]\s*$`)
	passwordishRe = regexp.MustCompile(`(?i)password|passwd|passphrase`)
)

// DetectPrompt reads text whose last line is where the cursor rests, and reports whether
// that line is a question for a person. A last line ended by a newline is no question, so
// this is safe to call with any output tail. Missing one costs nothing but a card: the
// human still has the terminal. It knows only the common wordings on purpose, because a
// false card next to a command that is merely printing is worse.
func DetectPrompt(raw string) (Prompt, bool) {
	lines := screenLines(raw)
	line := lines[len(lines)-1]
	if line == "" || len([]rune(line)) > maxPromptRunes {
		return Prompt{}, false
	}
	p := Prompt{Line: line}
	switch {
	case confirmRe.MatchString(line):
		p.Kind, p.Word = PromptConfirm, "y"
		if m := confirmRe.FindStringSubmatch(line); strings.Contains(strings.ToLower(m[1]), "yes") {
			p.Word = "yes"
		}
	case typeYesRe.MatchString(line):
		p.Kind, p.Word = PromptConfirm, "yes"
	case passwordRe.MatchString(line), pinRe.MatchString(line), codeRe.MatchString(line), bareCodeRe.MatchString(line):
		p.Kind = PromptSecret
	default:
		return Prompt{}, false
	}
	if p.Kind == PromptConfirm || !passwordishRe.MatchString(line) {
		p.Context = contextAbove(lines)
	}
	return p, true
}

// OpenLine reports text ending in the middle of a line, which is what a program waiting
// on a question nobody recognized looks like.
func OpenLine(raw string) bool {
	lines := screenLines(raw)
	return lines[len(lines)-1] != ""
}

// CleanLine is the text of one line as a person would read it.
func CleanLine(raw string) string {
	lines := screenLines(raw)
	return lines[len(lines)-1]
}

// screenLines removes everything that is not text. A carriage return goes back over the
// line, as it does on screen, so a progress bar cannot hide a question and a question
// cannot be disguised by what it printed before overwriting itself. Invisible characters
// go too, or a bidi override could make the shown line read as something else.
func screenLines(raw string) []string {
	raw = termSeqRe.ReplaceAllString(raw, "")
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	for i, l := range lines {
		if j := strings.LastIndexByte(l, '\r'); j >= 0 {
			l = l[j+1:]
		}
		lines[i] = strings.TrimRight(strings.Map(func(r rune) rune {
			switch {
			case r == '\t':
				return ' '
			case unicode.IsControl(r), unicode.Is(unicode.Cf, r), r == unicode.ReplacementChar:
				return -1
			}
			return r
		}, l), " ")
	}
	return lines
}

// contextAbove is the unbroken run of text right above the last line, oldest first.
func contextAbove(lines []string) []string {
	var out []string
	for i := len(lines) - 2; i >= 0 && len(out) < maxContextLines; i-- {
		if lines[i] == "" {
			break
		}
		out = append([]string{clipRunes(lines[i], maxContextRunes)}, out...)
	}
	return out
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
