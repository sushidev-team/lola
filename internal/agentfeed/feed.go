// Package agentfeed reads a coding agent's own log into conversation items. It is the
// one reader allowed to render a transcript, a deliberate exception to internal/agentlog
// and internal/claimaudit, and everything it returns is a CLAIM: the agent can write the
// file it reads, so "the human said" and "this passed" prove nothing here.
//
// The items are for display. Nothing in lola's control loop may read them, which
// internal/daemon's TestControlLoopNeverImportsAgentfeed enforces. It holds no goroutine
// and no tailer: a caller pulls with Advance, a file is opened per pass, and the log
// itself is the store, so a restart rescans and hands clients a new epoch.
//
// Paths are never taken from a hook. A segment is a base name matching a UUID directly
// under the session's slug directory, opened with no-follow, because hook payloads cross
// the sandbox bridge and a forged transcript_path must not turn lola into a reader of
// arbitrary files.
package agentfeed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/sshelement"
)

// Health is one of protocol's Feed* values.
type Health = string

// Item is the wire's item; a Source fills everything but Order and Seq, which belong to
// the Feed that retains it.
type Item = protocol.AgentFeedItem

// Bounds on what one item may carry.
const (
	maxTextRunes    = 16 << 10
	maxPreviewRunes = 2048 // Context for inline previews before the current tool's full output is fetched.
	maxSummaryRunes = 200
	maxNoticeRunes  = 2 << 10
	// maxOutputBytes bounds an expanded tool output. encoding/json may turn one byte
	// into six, and a reply is one line on the daemon socket.
	maxOutputBytes = 128 << 10
	// unreadableBytes is how much a log may grow, yielding nothing, before lola says
	// it cannot read this version's format.
	unreadableBytes = 256 << 10
)

// Counts is what a Source has seen so far. The daemon compares them with its hook
// counters to notice a reader that drops part of a format it otherwise understands.
type Counts struct {
	Bytes      int64 // log bytes consumed
	Items      int64 // items produced, of any kind
	Text       int64 // assistant text blocks
	User       int64 // user turns
	Results    int64 // tool results, including those of dropped calls
	AgentCalls int64 // subagent calls: their tool hooks fire with no result in this file
}

// Source reads one agent's log.
type Source interface {
	// Advance consumes at most about budget bytes (one line may overrun it) and answers
	// the items that are new or changed, oldest first, plus the log's health as far as
	// the file alone can say.
	Advance(ctx context.Context, budget int64) (changed []Item, h Health)
	// AtEOF is true once the log has been read to its end. A trailing partial line counts
	// as the end: it is still being written.
	AtEOF() bool
	// Generation moves when a file was replaced or shrank and the read started over.
	Generation() int64
	// Counts is what has been seen since the read last started over.
	Counts() Counts
	// TextHash is Hash of a user item's raw text, before it was sanitized or capped, so a
	// long hand-off can still match what lola recorded typing. "" when it has none.
	TextHash(id string) string
	// Output is the bounded, sanitized output of one tool item, read back from the log.
	Output(ctx context.Context, id string) (text string, truncated bool, err error)
	// Thread reads one subagent's whole conversation on demand.
	Thread(ctx context.Context, parent string) ([]Item, error)
}

var (
	pasteOpenRe  = regexp.MustCompile(`\n*<pasted_content\b[^>\n]*>\n?`)
	pasteCloseRe = regexp.MustCompile(`\n?</pasted_content\b[^>\n]*>\n*`)
)

// stripPaste removes the wrappers claude-code puts around a paste. A large send may be
// split into several fragments, so a wrapper can fall anywhere in the text.
func stripPaste(text string) string {
	if !strings.Contains(text, "pasted_content") {
		return text
	}
	return pasteCloseRe.ReplaceAllString(pasteOpenRe.ReplaceAllString(text, ""), "")
}

// pasteWrapRe finds either wrapper with the same whitespace stripPaste takes with it. Groups:
// 1 an open wrapper's attributes, 2 a close wrapper's.
var pasteWrapRe = regexp.MustCompile(`\n*<pasted_content\b([^>\n]*)>\n?|\n?</pasted_content\b([^>\n]*)>\n*`)

// pasteCutBytes is about where claude-code cuts a long send into fragments. A paste body this
// long stopped because it was cut, so what follows it is more of the same text.
const pasteCutBytes = 1000

// displayPaste is stripPaste for a row a person reads. Removing a wrapper glued typed words to the
// paste beside them, and one paste to the next, so a wrapper becomes a line break. The exception is
// two fragments of one send: the same id on both sides, or a full-size fragment running on into
// text. There the cut fell mid-word and a break would invent one. Normalize stays on stripPaste,
// because a hash must not depend on any of this.
func displayPaste(text string) string {
	if !strings.Contains(text, "pasted_content") {
		return text
	}
	var out strings.Builder
	at, bodyStart := 0, -1
	closed, sep := "", ""
	for _, m := range pasteWrapRe.FindAllStringSubmatchIndex(text, -1) {
		if chunk := text[at:m[0]]; chunk != "" {
			if out.Len() > 0 {
				out.WriteString(sep)
			}
			out.WriteString(chunk)
			closed = ""
		}
		if m[2] >= 0 { // an open wrapper
			sep = "\n"
			if id := strings.TrimSpace(text[m[2]:m[3]]); id != "" && id == closed {
				sep = ""
			}
			closed, bodyStart = "", m[1]
		} else { // a close wrapper
			closed = strings.TrimSpace(text[m[4]:m[5]])
			sep = "\n"
			if bodyStart >= 0 && m[0]-bodyStart >= pasteCutBytes {
				sep = ""
			}
			bodyStart = -1
		}
		at = m[1]
	}
	if chunk := text[at:]; chunk != "" {
		if out.Len() > 0 {
			out.WriteString(sep)
		}
		out.WriteString(chunk)
	}
	return out.String()
}

// Normalize is the form in which a typed text and its log record compare equal: paste
// wrappers gone, tabs as the 4 spaces a paste turns them into, one trailing backslash
// removed (it would not have submitted), and no surrounding space, which also covers the
// leading space that defuses a leading `!`.
func Normalize(text string) string {
	text = stripPaste(text)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\t", "    ")
	text = strings.TrimSpace(text)
	text = strings.TrimSuffix(text, `\`)
	return strings.TrimSpace(text)
}

// Hash is the identity of a sent text: hex sha256 of its Normalize form. This is what a
// SentBatch records and what a user item is matched against.
func Hash(text string) string {
	sum := sha256.Sum256([]byte(Normalize(text)))
	return hex.EncodeToString(sum[:])
}

// capText sanitizes text for display, capped at n runes, and says whether it was cut.
// Sanitizing happens first so the cap counts what a person would read.
func capText(text string, n int) (string, bool) {
	clean := sshelement.Para(text, utf8.RuneCountInString(text)+1)
	if utf8.RuneCountInString(clean) <= n {
		return clean, false
	}
	return sshelement.Para(clean, n), true
}

// preview is the short, sanitized head of a tool's output. It works on a head of the text,
// because a result can be a megabyte and the sanitizer walks whatever it is given.
func preview(text string) string {
	head, _ := clipUTF8(text, 8<<10)
	return sshelement.Para(head, maxPreviewRunes)
}

// isReportCall is a shell command whose effect the store already shows as a fact, so a
// row for it would say the same thing twice. Only the whole command counts: a chain such
// as `lola report phase x && go test ./...` did real work that must stay visible.
func isReportCall(command string) bool {
	c := strings.TrimSpace(command)
	matched := false
	for _, prefix := range []string{"lola report", "lola ssh", "lola post"} {
		if c == prefix || strings.HasPrefix(c, prefix+" ") {
			matched = true
			break
		}
	}
	return matched && !hasShellChain(c)
}

// hasShellChain says whether an unquoted separator joins a second command on. Inside
// quotes a pipe is only an argument: `lola ssh run "ps | grep x"` is one lola call.
func hasShellChain(c string) bool {
	var quote rune
	escaped := false
	for _, r := range c {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && quote != '\'':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ';' || r == '|' || r == '&' || r == '\n' || r == '`':
			return true
		}
	}
	return false
}

// toolCategory maps a tool's name onto the wire's categories. Anything unknown, an MCP
// tool included, is "other" rather than a guess.
func toolCategory(name string) string {
	switch name {
	case "Read", "Glob", "Grep", "LS", "NotebookRead", "WebFetch", "WebSearch", "ToolSearch":
		return "read"
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return "edit"
	case "Bash", "BashOutput", "KillBash", "KillShell":
		return "run"
	case "Task", "Agent":
		return "agent"
	}
	return "other"
}

// firstLine is what a multi-line command is summarized by.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// clipUTF8 cuts s to at most n bytes without splitting a rune.
func clipUTF8(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

// collector gathers what one Advance changed, keeping one entry per id, and holds the
// bookkeeping every Source shares.
type collector struct {
	changed []Item
	at      map[string]int
	counts  Counts
	hashes  map[string]string
	gen     int64
}

func (c *collector) put(it Item) {
	if c.at == nil {
		c.at = map[string]int{}
	}
	if i, ok := c.at[it.ID]; ok {
		c.changed[i] = it
		return
	}
	c.at[it.ID] = len(c.changed)
	c.changed = append(c.changed, it)
}

func (c *collector) take() []Item {
	out := c.changed
	c.changed, c.at = nil, nil
	return out
}

// restart forgets everything a read produced, because its file is gone or replaced.
func (c *collector) restart() {
	c.changed, c.at, c.hashes = nil, nil, nil
	c.counts = Counts{}
	c.gen++
}

func (c *collector) noteUser(id, raw string) {
	if c.hashes == nil {
		c.hashes = map[string]string{}
	}
	c.hashes[id] = Hash(raw)
}

// Generation implements Source.
func (c *collector) Generation() int64 { return c.gen }

// Counts implements Source.
func (c *collector) Counts() Counts { return c.counts }

// TextHash implements Source.
func (c *collector) TextHash(id string) string { return c.hashes[id] }
