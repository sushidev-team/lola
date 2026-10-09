package tui

// diffview.go is the TUI half of the diff viewer (SUSHI-618): a full-screen,
// keyboard-driven read of what the selected session changed against its base
// branch (cmd=diff), with a way to leave line comments and a free note and send
// them to the coding agent as ONE message (cmd=feedback).
//
// It types nothing itself. The daemon renders the batch, types it only into a
// pane verifiably resting at its prompt, and otherwise queues it — so this view
// never needs the answer card's idle gate, and "s" is always safe to press.

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/gitdiff"
	"github.com/sushidev-team/lola/internal/protocol"
)

// diffRow is one rendered line of the overlay: a file header or a patch row.
type diffRow struct {
	file   string // path the row belongs to
	header bool   // the file's title row
	line   gitdiff.Line
}

// diffInput is what the bottom line is collecting, if anything.
type diffInput int

const (
	diffInputNone diffInput = iota
	diffInputComment
	diffInputNote
)

// diffModel is the overlay's state. Drafts outlive the overlay (keyed by
// session), so closing it to check on the agent never throws a review away.
type diffModel struct {
	open    bool
	session string
	loading bool
	err     string
	data    *protocol.DiffData
	rows    []diffRow
	cursor  int
	top     int

	input    diffInput
	inputBuf string
	flash    string
	flashOK  bool
	sending  bool

	drafts map[string][]protocol.FeedbackComment
	notes  map[string]string
}

type diffLoadedMsg struct {
	session string
	data    *protocol.DiffData
	err     error
}

type feedbackDoneMsg struct {
	session string
	data    protocol.FeedbackData
	err     error
}

func diffCmd(id string) tea.Cmd {
	return func() tea.Msg {
		resp, err := requestFn(protocol.Request{Cmd: "diff", Session: id})
		if err != nil {
			return diffLoadedMsg{session: id, err: err}
		}
		if !resp.OK {
			return diffLoadedMsg{session: id, err: fmt.Errorf("%s", resp.Error)}
		}
		var d protocol.DiffData
		if err := json.Unmarshal(resp.Data, &d); err != nil {
			return diffLoadedMsg{session: id, err: err}
		}
		return diffLoadedMsg{session: id, data: &d}
	}
}

func feedbackCmd(args protocol.FeedbackArgs) tea.Cmd {
	return func() tea.Msg {
		raw, err := json.Marshal(args)
		if err != nil {
			return feedbackDoneMsg{session: args.Session, err: err}
		}
		resp, err := requestFn(protocol.Request{Cmd: "feedback", Args: raw})
		if err != nil {
			return feedbackDoneMsg{session: args.Session, err: err}
		}
		if !resp.OK {
			return feedbackDoneMsg{session: args.Session, err: fmt.Errorf("%s", resp.Error)}
		}
		var d protocol.FeedbackData
		if err := json.Unmarshal(resp.Data, &d); err != nil {
			return feedbackDoneMsg{session: args.Session, err: err}
		}
		return feedbackDoneMsg{session: args.Session, data: d}
	}
}

// openDiff opens the overlay on the selected session and starts the fetch.
func (m *rootModel) openDiff() (tea.Model, tea.Cmd) {
	sel := m.sessions.selected()
	if sel == nil {
		return m, nil
	}
	d := &m.diff
	if d.drafts == nil {
		d.drafts, d.notes = map[string][]protocol.FeedbackComment{}, map[string]string{}
	}
	if d.session != sel.ID {
		d.data, d.rows, d.cursor, d.top = nil, nil, 0, 0
	}
	d.open, d.session, d.loading, d.err = true, sel.ID, true, ""
	d.input, d.inputBuf, d.flash = diffInputNone, "", ""
	return m, diffCmd(sel.ID)
}

// setData installs a fetched diff, keeping the cursor on the same file + line
// when it is still there (a refresh while reviewing must not jump).
func (d *diffModel) setData(data *protocol.DiffData) {
	var keepFile string
	var keepLine gitdiff.Line
	if d.cursor < len(d.rows) {
		keepFile, keepLine = d.rows[d.cursor].file, d.rows[d.cursor].line
	}
	d.data = data
	d.rows = d.rows[:0]
	for _, f := range data.Files {
		d.rows = append(d.rows, diffRow{file: f.Path, header: true})
		for _, l := range gitdiff.ParseLines(f.Patch) {
			d.rows = append(d.rows, diffRow{file: f.Path, line: l})
		}
	}
	d.cursor = 0
	for i, r := range d.rows {
		if r.file == keepFile && r.line == keepLine {
			d.cursor = i
			break
		}
	}
}

// anchor reports the commentable line under the cursor: the working-tree line
// for an added or context row, the base line (side "old") for a removed one.
func (d *diffModel) anchor() (path string, line int, side string, quote string, ok bool) {
	if d.cursor >= len(d.rows) {
		return "", 0, "", "", false
	}
	r := d.rows[d.cursor]
	if r.header {
		return "", 0, "", "", false
	}
	switch r.line.Kind {
	case '+', ' ':
		return r.file, r.line.NewLine, "new", r.line.Text, r.line.NewLine > 0
	case '-':
		return r.file, r.line.OldLine, "old", r.line.Text, r.line.OldLine > 0
	}
	return "", 0, "", "", false
}

func (d *diffModel) pending() int {
	n := len(d.drafts[d.session])
	if strings.TrimSpace(d.notes[d.session]) != "" {
		n++
	}
	return n
}

func (m *rootModel) updateDiff(msg tea.Msg) (tea.Model, tea.Cmd) {
	d := &m.diff
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	if d.input != diffInputNone {
		return m.updateDiffInput(k)
	}
	d.flash = ""
	page := max(1, m.diffBodyHeight()-1)
	switch k.String() {
	case "esc", "q", "f":
		d.open = false
	case "down", "j":
		d.move(1)
	case "up", "k":
		d.move(-1)
	case "ctrl+d", "pgdown", "space":
		d.move(page / 2)
	case "ctrl+u", "pgup":
		d.move(-page / 2)
	case "g":
		d.cursor = 0
	case "G":
		d.cursor = max(0, len(d.rows)-1)
	case "]", "}":
		d.jumpFile(1)
	case "[", "{":
		d.jumpFile(-1)
	case "r":
		d.loading = true
		return m, diffCmd(d.session)
	case "c":
		if _, _, _, _, ok := d.anchor(); !ok {
			d.flash, d.flashOK = "move to a code line to comment on it", false
			return m, nil
		}
		d.input, d.inputBuf = diffInputComment, ""
	case "n":
		d.input, d.inputBuf = diffInputNote, d.notes[d.session]
	case "u":
		if ds := d.drafts[d.session]; len(ds) > 0 {
			d.drafts[d.session] = ds[:len(ds)-1]
			d.flash, d.flashOK = "removed the last comment", true
		}
	case "s":
		if d.sending {
			return m, nil
		}
		if d.pending() == 0 {
			d.flash, d.flashOK = "nothing to send — c comments on a line, n adds a note", false
			return m, nil
		}
		d.sending = true
		d.flash, d.flashOK = "sending…", true
		return m, feedbackCmd(protocol.FeedbackArgs{
			Session:  d.session,
			Comments: append([]protocol.FeedbackComment(nil), d.drafts[d.session]...),
			Note:     d.notes[d.session],
		})
	}
	d.clampScroll(m.diffBodyHeight())
	return m, nil
}

func (m *rootModel) updateDiffInput(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	d := &m.diff
	switch k.String() {
	case "esc":
		d.input, d.inputBuf = diffInputNone, ""
		return m, nil
	case "enter":
		text := strings.TrimSpace(d.inputBuf)
		switch d.input {
		case diffInputComment:
			if path, line, side, quote, ok := d.anchor(); ok && text != "" {
				d.drafts[d.session] = append(d.drafts[d.session], protocol.FeedbackComment{
					Path: path, Line: line, Side: side, Quote: quote, Body: text,
				})
				d.flash, d.flashOK = fmt.Sprintf("comment queued on %s:%d — s sends", path, line), true
			}
		case diffInputNote:
			d.notes[d.session] = text
		}
		d.input, d.inputBuf = diffInputNone, ""
		return m, nil
	case "backspace":
		if r := []rune(d.inputBuf); len(r) > 0 {
			d.inputBuf = string(r[:len(r)-1])
		}
		return m, nil
	}
	if k.Text != "" {
		d.inputBuf += k.Text
	}
	return m, nil
}

// handleFeedbackDone clears the drafts the daemon ACCEPTED (delivered or queued
// on its side); a refusal keeps every one of them.
func (m *rootModel) handleFeedbackDone(v feedbackDoneMsg) tea.Cmd {
	d := &m.diff
	d.sending = false
	if v.err != nil {
		d.flash, d.flashOK = v.err.Error(), false
		return nil
	}
	delete(d.drafts, v.session)
	delete(d.notes, v.session)
	if v.data.Delivered {
		d.flash, d.flashOK = "feedback sent to the agent", true
	} else {
		d.flash, d.flashOK = "feedback queued — sent when the agent is at its prompt", true
	}
	return fetchSessionsCmd
}

func (d *diffModel) move(n int) {
	d.cursor = min(max(0, d.cursor+n), max(0, len(d.rows)-1))
}

func (d *diffModel) jumpFile(dir int) {
	for i := d.cursor + dir; i >= 0 && i < len(d.rows); i += dir {
		if d.rows[i].header {
			d.cursor = i
			return
		}
	}
}

func (d *diffModel) clampScroll(h int) {
	if h < 1 {
		h = 1
	}
	if d.cursor < d.top {
		d.top = d.cursor
	}
	if d.cursor >= d.top+h {
		d.top = d.cursor - h + 1
	}
}

// diffBodyHeight is the rows left for the diff between the title and the two
// footer lines.
func (m *rootModel) diffBodyHeight() int {
	return max(1, m.height-3)
}

func (m *rootModel) diffView() string {
	d := &m.diff
	w := max(20, m.width)
	h := m.diffBodyHeight()
	d.clampScroll(h)

	title := titleStyle.Render("diff") + " " + d.session
	switch {
	case d.loading && d.data == nil:
		title += faintText.Render("  loading…")
	case d.data != nil:
		var add, del int
		for _, f := range d.data.Files {
			add += f.Additions
			del += f.Deletions
		}
		title += faintText.Render(fmt.Sprintf("  against %s · %d files ", d.data.Base, len(d.data.Files))) +
			goodText.Render(fmt.Sprintf("+%d", add)) + " " + badText.Render(fmt.Sprintf("-%d", del))
		if d.data.Truncated {
			title += warnText.Render(" · truncated")
		}
	}
	lines := []string{truncateANSI(title, w)}

	body := make([]string, 0, h)
	switch {
	case d.err != "":
		body = append(body, badText.Render(d.err))
	case d.data != nil && len(d.data.Files) == 0:
		body = append(body, faintText.Render("no changes against "+d.data.Base))
	default:
		drafts := map[string]int{}
		for _, c := range d.drafts[d.session] {
			drafts[fmt.Sprintf("%s\x00%s\x00%d", c.Path, c.Side, c.Line)]++
		}
		for i := d.top; i < len(d.rows) && len(body) < h; i++ {
			body = append(body, d.renderRow(i, w, drafts))
		}
	}
	for len(body) < h {
		body = append(body, "")
	}
	lines = append(lines, body...)

	// Footer: the input line or the status line, then the key hints.
	status := ""
	switch d.input {
	case diffInputComment:
		path, line, _, _, _ := d.anchor()
		status = warnText.Render(fmt.Sprintf("comment %s:%d", path, line)) + faintText.Render("> ") + d.inputBuf + "_"
	case diffInputNote:
		status = warnText.Render("note") + faintText.Render("> ") + d.inputBuf + "_"
	default:
		if d.flash != "" {
			if d.flashOK {
				status = goodText.Render(d.flash)
			} else {
				status = badText.Render(d.flash)
			}
		} else if n := d.pending(); n > 0 {
			status = warnText.Render(fmt.Sprintf("%d unsent — s sends to the agent", n))
		}
		if sel := m.sessions.byID(d.session); sel != nil && sel.FeedbackPending {
			status += faintText.Render("  (earlier feedback queued for the agent)")
		}
	}
	lines = append(lines, truncateANSI(status, w))
	lines = append(lines, truncateANSI(faintText.Render(
		"j/k move · [ ] file · c comment · n note · u undo · s send · r refresh · esc close"), w))
	return strings.Join(lines, "\n")
}

func (d *diffModel) renderRow(i, w int, drafts map[string]int) string {
	r := d.rows[i]
	cursor := "  "
	if i == d.cursor {
		cursor = lipgloss.NewStyle().Foreground(lipgloss.Color(colAccent)).Render("▌ ")
	}
	if r.header {
		f := d.fileByPath(r.file)
		name := r.file
		if f != nil && f.OldPath != "" {
			name = f.OldPath + " → " + f.Path
		}
		// Built at render time rather than as a package style: it is palette
		// derived, and a package-level one would need registering in
		// rebuildStyles to follow a flavor change.
		label := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colText)).Render(name)
		if f != nil {
			switch {
			case f.Binary:
				label += faintText.Render("  (binary)")
			case f.TooLarge:
				label += faintText.Render("  (too large to show)")
			case f.Untracked:
				label += faintText.Render("  (untracked)")
			}
			label += "  " + goodText.Render(fmt.Sprintf("+%d", f.Additions)) + " " + badText.Render(fmt.Sprintf("-%d", f.Deletions))
		}
		return truncateANSI(cursor+label, w)
	}
	l := r.line
	num := func(n int) string {
		if n == 0 {
			return "    "
		}
		return fmt.Sprintf("%4d", n)
	}
	gutter := faintText.Render(num(l.OldLine) + " " + num(l.NewLine) + " ")
	var text string
	switch l.Kind {
	case '+':
		text = goodText.Render("+" + l.Text)
	case '-':
		text = badText.Render("-" + l.Text)
	case '@', '\\':
		text = faintText.Render(l.Text)
		gutter = ""
	default:
		text = " " + l.Text
	}
	mark := ""
	side, n := "new", l.NewLine
	if l.Kind == '-' {
		side, n = "old", l.OldLine
	}
	if c := drafts[fmt.Sprintf("%s\x00%s\x00%d", r.file, side, n)]; c > 0 && n > 0 {
		mark = warnText.Render(fmt.Sprintf(" ✎%d", c))
	}
	return truncateANSI(cursor+gutter+strings.ReplaceAll(text, "\t", "    ")+mark, w)
}

func (d *diffModel) fileByPath(p string) *protocol.DiffFile {
	if d.data == nil {
		return nil
	}
	for i := range d.data.Files {
		if d.data.Files[i].Path == p {
			return &d.data.Files[i]
		}
	}
	return nil
}

// byID finds a session in the last list the daemon sent, or nil.
func (s *sessionsModel) byID(id string) *protocol.SessionInfo {
	if s.data == nil {
		return nil
	}
	for i := range s.data.Sessions {
		if s.data.Sessions[i].ID == id {
			return &s.data.Sessions[i]
		}
	}
	return nil
}
