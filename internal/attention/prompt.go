package attention

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/sushidev-team/lola/internal/agent"
)

// Cursor is where a pane's cursor is and whether the program is showing it. It is
// defined here so this package stays free of tmux.
type Cursor struct {
	X, Y    int
	Visible bool
}

// Prompt is a question read off a visible pane. The daemon maps it onto
// protocol.AgentPrompt. Kind is permission | question | question_multi | modal.
//
// Lines is what the question says, command included, and Choices is what can be pressed.
// An option's row is in Choices only, so a card that draws both does not say it twice.
// Hash covers every row of the block, options and focus marker included, so it pins
// exactly what a human read. Everything here is pane text, which a process can print:
// it decides nothing and must never be persisted.
type Prompt struct {
	Kind    string
	Hash    string
	Lines   []string
	Choices []Choice
}

// Prompt kinds.
const (
	PromptPermission    = "permission"
	PromptQuestion      = "question"
	PromptQuestionMulti = "question_multi"
	PromptModal         = "modal"
)

// composerCaretX is the column a claude-code composer's cursor rests in when nothing has
// been typed: after the caret and one space. Spaces alone move it, which is how a
// composer holding only spaces is told from an empty one.
const composerCaretX = 2

// ComposerEmptyAt is the proof that a pane's composer is resting and empty, read at the
// cursor. visible is one capture of the visible screen, rows aligned with c, read BEFORE
// the cursor so the proof never rests on a screen older than itself.
//
// For an agent whose cursor has been measured, it wants a resting pane, a shown cursor in
// the caret's column, a row holding only the caret or a dim suggestion, and a rule on
// each side. That row is the live composer whatever else is on screen, which is why this
// needs no list of known footers: a status line can print anything, and the old list
// deferred every automatic send forever for anyone who had one, or was in manual or plan
// mode, or in vim INSERT.
//
// Vim NORMAL mode cannot be told from INSERT in a pane. It reads as empty here, and text
// sent to it would run as vim commands. That limit is known and accepted: it needs a vim
// user who left the composer in NORMAL. The one case lola could cause itself, its own Esc on
// a composer in INSERT, is caught by comparing VimInsert across the Esc. Whether the Esc does
// switch modes mid-turn has not been measured.
//
// Every other agent keeps EmptyComposer, because the cursor rule was measured only on
// Claude Code.
func ComposerEmptyAt(visible string, c Cursor, k agent.Kind) bool {
	if !agent.Caps(k).Cursor {
		return EmptyComposer(visible, k)
	}
	if !c.Visible || c.X != composerCaretX || Classify(visible, k) != ActivityWaiting {
		return false
	}
	raw := strings.Split(visible, "\n")
	if c.Y < 1 || c.Y+1 >= len(raw) {
		return false
	}
	line := composerLine(stripANSI(raw[c.Y]))
	if line != "❯" && line != ">" && !suggestionOnly(raw[c.Y]) {
		return false
	}
	return composerRule(strings.TrimSpace(stripANSI(raw[c.Y-1]))) &&
		composerRule(strings.TrimSpace(stripANSI(raw[c.Y+1])))
}

// vimInsertRe is the footer a vim composer shows in INSERT. NORMAL shows nothing, which is
// why a pane alone cannot name that mode.
var vimInsertRe = regexp.MustCompile(`(?m)^\s*-- INSERT --`)

// VimInsert says whether a composer's footer shows vim's INSERT mode.
func VimInsert(visible string) bool { return vimInsertRe.MatchString(stripANSI(visible)) }

var (
	// optionRe is one row of a dialog's list: the focus marker if any, a number, a dot.
	optionRe = regexp.MustCompile(`^\s*(❯\s*)?(\d{1,2})\.\s+(\S.*)$`)
	// submitRe is the unnumbered row a multi-select answers with.
	submitRe = regexp.MustCompile(`^\s*(?:❯\s*)?Submit\s*$`)
	// dashRowRe is the dashed divider around a command in a permission dialog.
	dashRowRe = regexp.MustCompile(`^\s*╌+\s*$`)
)

// ReadPrompt reads a question off one capture of the visible screen. It answers false for
// a pane with nothing to ask, and for any agent without measured cards.
//
// A permission or question needs all of these, because a press goes to whatever is really
// on screen: a resting pane, a hidden cursor (the program is not reading a line), a footer
// of key names as the last row, options numbered 1..n with no gap and one of them focused,
// and exactly one edge above them (blockTop). Anything short of that with a hidden cursor and no empty
// composer is a modal, which offers no keys. Failing that way costs a person one trip to
// the terminal, while a wrong card could press a key into something nobody read.
func ReadPrompt(visible string, c Cursor, k agent.Kind) (Prompt, bool) {
	if !agent.Caps(k).Cards {
		return Prompt{}, false
	}
	rows := promptRows(visible)
	switch Classify(visible, k) {
	case ActivityBlocked:
		return modalPrompt(rows), true
	case ActivityWaiting:
	default:
		return Prompt{}, false
	}
	if c.Visible {
		return Prompt{}, false
	}
	if p, ok := readOptions(rows); ok {
		return p, true
	}
	return modalPrompt(rows), true
}

// promptRows is the visible screen as plain rows, with trailing blank ones gone.
func promptRows(visible string) []string {
	rows := strings.Split(stripANSI(visible), "\n")
	for i, r := range rows {
		rows[i] = strings.TrimRight(r, " \t")
	}
	end := len(rows)
	for end > 0 && rows[end-1] == "" {
		end--
	}
	return rows[:end]
}

// readOptions walks up from the footer collecting options by number until it reaches 1,
// so a numbered list in the conversation above the dialog can never join it.
func readOptions(rows []string) (Prompt, bool) {
	if len(rows) == 0 || !keyHintRe.MatchString(rows[len(rows)-1]) || !strings.Contains(strings.ToLower(rows[len(rows)-1]), "esc to") {
		return Prompt{}, false
	}
	footer := rows[len(rows)-1]
	type option struct {
		row, n int
		label  string
	}
	var found []option // bottom up
	first, focused, multi := -1, false, false
	for i := len(rows) - 2; i >= 0 && first < 0; i-- {
		if submitRe.MatchString(rows[i]) {
			multi = true
			focused = focused || strings.Contains(rows[i], "❯")
			continue
		}
		m := optionRe.FindStringSubmatch(rows[i])
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[2])
		if len(found) > 0 && n != found[len(found)-1].n-1 {
			return Prompt{}, false
		}
		found = append(found, option{row: i, n: n, label: m[3]})
		focused = focused || m[1] != ""
		if n == 1 {
			first = i
		}
	}
	if first < 0 || !focused || len(found) < 2 {
		return Prompt{}, false
	}
	top, ok := blockTop(rows, first)
	if !ok {
		return Prompt{}, false
	}

	p := Prompt{Kind: PromptQuestion, Hash: hashRows(rows[top+1:])}
	for i := top + 1; i < first; i++ {
		if !dashRowRe.MatchString(rows[i]) {
			p.Lines = append(p.Lines, strings.TrimSpace(rows[i]))
		}
	}
	p.Lines = trimBlank(p.Lines)
	var rowsOf [][]string
	for i := len(found) - 1; i >= 0; i-- {
		o := found[i]
		label := []string{o.label}
		// A label that wrapped goes on until the next option, a blank row or a rule, and
		// a card must show all of it: "always allow access to" is not the same promise as
		// the directory it goes on to name.
		for j := o.row + 1; j < len(rows)-1; j++ {
			r := strings.TrimSpace(rows[j])
			if r == "" || composerRule(r) || optionRe.MatchString(rows[j]) || submitRe.MatchString(rows[j]) {
				break
			}
			label = append(label, r)
		}
		p.Choices = append(p.Choices, Choice{Key: strconv.Itoa(o.n), Label: strings.Join(label, " ")})
		rowsOf = append(rowsOf, label)
	}
	switch {
	case multi:
		p.Kind = PromptQuestionMulti
	case strings.Contains(footer, "Enter to select"):
	case strings.Contains(footer, "Tab to amend"), askedPermission(p.Lines):
		p.Kind = PromptPermission
	}
	// A permission's second row is its label wrapping, so it stays one promise. A question's is the
	// option's description, which a card draws apart from its name. Neither touches the hash, which
	// is over the rows themselves.
	if p.Kind == PromptQuestion {
		for i, r := range rowsOf {
			p.Choices[i].Label = r[0]
			p.Choices[i].Detail = strings.Join(r[1:], " ")
		}
	}
	return p, true
}

// blockTop finds the rule a dialog hangs from, above its first option. The command or question
// inside a dialog is program output and can hold rows of rule characters, so the nearest rule
// proves nothing: taking one for the edge would leave everything above it out of the card and
// out of the hash, and a person would approve a head they never saw.
//
// claude-code draws the edge at the left margin across the whole pane, and indents what it
// shows inside (a question's text is the exception, so its rule rows can reach the margin). A
// row short of that is text. More than one such row, or dashed dividers that cannot pair up
// below the edge, leave the top in doubt, and the answer is no: a modal offers no keys, which
// costs a person one trip to the terminal. The dividers are checked because a dialog taller
// than the pane has lost its real edge, and a rule inside the command can then be the only
// candidate left, with the upper divider above it.
func blockTop(rows []string, first int) (int, bool) {
	width := 0
	for _, r := range rows {
		width = max(width, utf8.RuneCountInString(r))
	}
	top := -1
	for i := first - 1; i >= 0; i-- {
		if !composerRule(rows[i]) || utf8.RuneCountInString(rows[i]) < width {
			continue
		}
		if top >= 0 {
			return 0, false
		}
		top = i
	}
	if top < 0 {
		return 0, false
	}
	below := 0
	for i, r := range rows[:first] {
		if !dashRowRe.MatchString(r) {
			continue
		}
		if i < top {
			return 0, false
		}
		below++
	}
	return top, below%2 == 0
}

// askedPermission is the question a permission dialog ends on, in its own words.
func askedPermission(lines []string) bool {
	return len(lines) > 0 && strings.HasPrefix(lines[len(lines)-1], "Do you want to ")
}

// modalPrompt describes a screen with nothing to press: the rows below its top edge, or
// the whole trailing block when it has none.
func modalPrompt(rows []string) Prompt {
	start := 0
	for i := len(rows) - 1; i >= 0; i-- {
		r := strings.TrimSpace(rows[i])
		if modalOverlayRe.MatchString(rows[i]) || composerRule(r) {
			start = i + 1
			break
		}
	}
	body := trimBlank(rows[start:])
	if len(body) > maxBlockLines {
		body = body[:maxBlockLines]
	}
	return Prompt{Kind: PromptModal, Hash: hashRows(body), Lines: body}
}

func hashRows(rows []string) string {
	sum := sha256.Sum256([]byte(strings.Join(rows, "\n")))
	return hex.EncodeToString(sum[:])
}

func trimBlank(rows []string) []string {
	for len(rows) > 0 && strings.TrimSpace(rows[0]) == "" {
		rows = rows[1:]
	}
	for len(rows) > 0 && strings.TrimSpace(rows[len(rows)-1]) == "" {
		rows = rows[:len(rows)-1]
	}
	return rows
}
