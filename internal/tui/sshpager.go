package tui

// sshpager.go holds the workspace's three full-screen sub-views: the raw output
// pager (depth 3 of a block), the handback form, and the host picker that starts
// a direct session.

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/state"
)

// --- output pager -------------------------------------------------------------

// sshStreamBuf is what has been loaded of one stream.
type sshStreamBuf struct {
	text    string
	next    int64
	total   int64
	dropped int64
	tail    string
	eof     bool
	loaded  bool
	loading bool
	pruned  bool
	err     string
}

// sshPager reads one block's output. Streams are separate on purpose: lola has
// no record of how they interleaved, and a false ordering would be worse than
// two tabs. With cmdOnly it reads a long approval's whole command instead.
type sshPager struct {
	session string
	block   string
	stream  string
	cmdOnly bool
	cmdText string
	bufs    map[string]*sshStreamBuf
	top     int
	follow  bool
	polling bool
	version int

	// The wrapped lines of the current stream, rebuilt when these change.
	lines  []string
	linesW int
	linesV int
	linesS string

	find    string
	finding bool
	hits    []int
	hitAt   int
}

type sshOutputMsg struct {
	session string
	block   string
	stream  string
	data    *protocol.SSHOutputData
	err     error
}

func (s *sshModel) openCommandPager(b *protocol.SSHBlock) {
	s.cmdRead[sshCmdKey(b)] = true
	s.pager = &sshPager{session: s.session, block: b.ID, cmdOnly: true, cmdText: b.Command, stream: "command"}
}

// openOutputPager opens depth 3. It lands on stderr when the block failed and
// stderr has bytes, since that is the stream that tells the story.
func (s *sshModel) openOutputPager(b *protocol.SSHBlock) tea.Cmd {
	stream := protocol.SSHStreamStdout
	if b.Status != protocol.SSHBlockSucceeded && b.StderrBytes > 0 {
		stream = protocol.SSHStreamStderr
	}
	s.pager = &sshPager{session: s.session, block: b.ID, stream: stream,
		bufs: map[string]*sshStreamBuf{}, follow: b.Status == protocol.SSHBlockRunning}
	return s.pager.load(stream)
}

func (p *sshPager) buf(stream string) *sshStreamBuf {
	b := p.bufs[stream]
	if b == nil {
		b = &sshStreamBuf{}
		p.bufs[stream] = b
	}
	return b
}

// load fetches the next piece of a stream, at most one request at a time.
func (p *sshPager) load(stream string) tea.Cmd {
	b := p.buf(stream)
	if b.loading {
		return nil
	}
	b.loading = true
	session, block, offset := p.session, p.block, b.next
	return func() tea.Msg {
		raw, err := sshCall(protocol.CmdSSHOutput, protocol.SSHOutputArgs{
			Session: session, Block: block, Stream: stream, Offset: offset, Limit: protocol.SSHMaxPollBytes,
		})
		if err != nil {
			return sshOutputMsg{session: session, block: block, stream: stream, err: err}
		}
		var d protocol.SSHOutputData
		if err := json.Unmarshal(raw, &d); err != nil {
			return sshOutputMsg{session: session, block: block, stream: stream, err: err}
		}
		return sshOutputMsg{session: session, block: block, stream: stream, data: &d}
	}
}

func (s *sshModel) handleOutput(v sshOutputMsg) {
	p := s.pager
	if p == nil || p.cmdOnly || v.session != p.session || v.block != p.block {
		return
	}
	b := p.buf(v.stream)
	b.loading = false
	if v.err != nil {
		b.err = v.err.Error()
		return
	}
	d := v.data
	b.err = ""
	if d.Offset == b.next { // a late duplicate of a piece already held changes nothing
		b.text += d.Data
		b.next = d.NextOffset
	}
	b.total, b.dropped, b.tail, b.eof, b.pruned, b.loaded = d.TotalBytes, d.DroppedBytes, d.Tail, d.EOF, d.Pruned, true
	p.version++
}

// pagerPoll follows a running block's current stream.
func (s *sshModel) pagerPoll() tea.Cmd {
	p := s.pager
	if p == nil || p.cmdOnly {
		return nil
	}
	b := s.blockByID(p.block)
	if b == nil {
		return nil
	}
	if b.Status == protocol.SSHBlockRunning {
		p.polling = true
		return p.load(p.stream)
	}
	// One last read after it ends: what arrived since the last poll, and the kept
	// tail, which a stream only has once it is finished.
	if p.polling {
		p.polling = false
		return p.load(p.stream)
	}
	return nil
}

func (s *sshModel) pagerKey(k tea.KeyPressMsg, m *rootModel) tea.Cmd {
	p := s.pager
	if p.finding {
		switch k.String() {
		case "esc":
			p.finding, p.find, p.hits = false, "", nil
		case "enter":
			p.finding = false
		case "backspace":
			if r := []rune(p.find); len(r) > 0 {
				p.find = string(r[:len(r)-1])
			}
			p.runFind()
		default:
			if k.Text != "" {
				p.find += k.Text
				p.runFind()
			}
		}
		return nil
	}
	page := max(1, m.sshPagerBodyHeight()-1)
	switch k.String() {
	case "esc", "q":
		s.pager = nil
		return nil
	case "j", "down":
		p.scroll(1, m)
	case "k", "up":
		p.scroll(-1, m)
	case "pgdown", "ctrl+d", "space":
		p.scroll(page, m)
	case "pgup", "ctrl+u":
		p.scroll(-page, m)
	case "g":
		p.top, p.follow = 0, false
	case "G":
		p.top, p.follow = 1<<30, true
	case "tab":
		if p.cmdOnly {
			return nil
		}
		p.stream = map[bool]string{true: protocol.SSHStreamStderr, false: protocol.SSHStreamStdout}[p.stream == protocol.SSHStreamStdout]
		p.top, p.hits, p.hitAt = 0, nil, 0
		if b := p.buf(p.stream); !b.loaded {
			return p.load(p.stream)
		}
	case "/":
		p.finding, p.find, p.hits, p.hitAt = true, "", nil, 0
	case "n":
		p.walk(1)
	case "N":
		p.walk(-1)
	case "L":
		if !p.cmdOnly && !p.buf(p.stream).eof {
			return p.load(p.stream)
		}
	case "o":
		if u := sshURLRe.FindString(strings.Join(p.visible(m), " ")); u != "" {
			return openURLCmd(u)
		}
		s.flash = "No address on screen."
	}
	// Reaching the end of what is loaded loads the next piece.
	if !p.cmdOnly && !p.buf(p.stream).eof && !p.buf(p.stream).loading &&
		p.top+m.sshPagerBodyHeight() >= len(p.wrapped(m.sshWidth())) {
		return p.load(p.stream)
	}
	return nil
}

func (p *sshPager) scroll(n int, m *rootModel) {
	lines := p.wrapped(m.sshWidth())
	maxTop := max(0, len(lines)-m.sshPagerBodyHeight())
	p.top = min(max(0, min(p.top, maxTop)+n), maxTop)
	p.follow = p.top == maxTop && n > 0
}

func (p *sshPager) visible(m *rootModel) []string {
	lines := p.wrapped(m.sshWidth())
	h := m.sshPagerBodyHeight()
	top := min(p.top, max(0, len(lines)-h))
	return lines[top:min(top+h, len(lines))]
}

// wrapped is the current stream as display rows, with the marker where the middle
// was dropped and the kept tail after it.
func (p *sshPager) wrapped(w int) []string {
	if p.lines != nil && p.linesW == w && p.linesV == p.version && p.linesS == p.stream {
		return p.lines
	}
	p.linesW, p.linesV, p.linesS = w, p.version, p.stream
	if p.cmdOnly {
		p.lines = sshWrapHard(p.cmdText, w)
		return p.lines
	}
	b := p.buf(p.stream)
	var out []string
	if b.text != "" {
		out = sshWrapHard(strings.TrimRight(b.text, "\n"), w)
	}
	if b.eof && b.dropped > 0 {
		out = append(out, "", warnText.Render(fmt.Sprintf("─────  %s not kept  ─────", sshBytes(b.dropped))),
			faintText.Render(fmt.Sprintf("Lola kept the first %s and the last 64 KB. The middle is gone.", sshBytes(b.total))), "")
		if b.tail != "" {
			out = append(out, sshWrapHard(strings.TrimRight(b.tail, "\n"), w)...)
		}
	}
	if out == nil {
		out = []string{}
	}
	p.lines = out
	return out
}

func (p *sshPager) runFind() {
	p.hits, p.hitAt = nil, 0
	q := strings.ToLower(p.find)
	if q == "" {
		return
	}
	for i, l := range p.lines {
		if strings.Contains(strings.ToLower(stripANSI(l)), q) {
			p.hits = append(p.hits, i)
		}
	}
	if len(p.hits) > 0 {
		p.top = p.hits[0]
		p.follow = false
	}
}

func (p *sshPager) walk(dir int) {
	n := len(p.hits)
	if n == 0 {
		return
	}
	p.hitAt = ((p.hitAt+dir)%n + n) % n
	p.top, p.follow = p.hits[p.hitAt], false
}

func (m *rootModel) sshPagerBodyHeight() int { return max(1, m.sshHeight()-5) }

func (m *rootModel) sshPagerView() []string {
	s := &m.ssh
	p := s.pager
	w, h := m.sshWidth(), m.sshPagerBodyHeight()
	block := s.blockByID(p.block)
	lines := p.wrapped(w)
	if p.follow {
		p.top = 1 << 30
	}
	p.top = min(p.top, max(0, len(lines)-h))

	var head []string
	if p.cmdOnly {
		head = append(head, previewLine(sshOrangeBold.Render("The whole command")+faintText.Render(fmt.Sprintf("  %s · %d lines · exact bytes", p.block, len(lines))), w))
	} else {
		title := faintText.Render(p.block + "  ")
		if block != nil {
			title += "$ " + sshLine(block.Command)
		}
		head = append(head, previewLine(title, w))
		var so, se int64
		if block != nil {
			so, se = block.StdoutBytes, block.StderrBytes
		}
		tab := func(name string, n int64) string {
			label := name + " " + sshBytes(n)
			if p.stream == name {
				return boxTitleHi.Render("[" + label + "]")
			}
			return faintText.Render(" " + label + " ")
		}
		head = append(head, tab(protocol.SSHStreamStdout, so)+" "+tab(protocol.SSHStreamStderr, se))
	}
	out := append(head, sshTierRuleFaint(w))

	body := p.emptyBody(block)
	if body == nil {
		hit := -1
		if len(p.hits) > 0 {
			hit = p.hits[p.hitAt]
		}
		for i := p.top; i < len(lines) && i < p.top+h; i++ {
			if i == hit {
				body = append(body, highlightRow(lines[i], w, bgSGR(colSel)))
				continue
			}
			body = append(body, previewLine(lines[i], w))
		}
	}
	for len(body) < h {
		body = append(body, "")
	}
	out = append(out, body...)

	status := ""
	switch {
	case p.finding:
		status = warnText.Render("find") + faintText.Render("> ") + sshLine(p.find) + "_" + faintText.Render("  "+p.findCount())
	case p.find != "":
		status = faintText.Render(`"` + sshLine(p.find) + `"  ` + p.findCount())
	case s.flash != "":
		status = warnText.Render(s.flash)
	case !p.follow && block != nil && block.Status == protocol.SSHBlockRunning:
		status = faintText.Render("Following paused · G resume")
	}
	keys := "j/k move · g/G ends · tab stdout/stderr · / find · o open address · esc back"
	if p.cmdOnly {
		keys = "j/k move · esc back to the card"
	}
	return append(out, previewLine(status, w), previewLine(faintText.Render(keys), w))
}

func (p *sshPager) findCount() string {
	if len(p.hits) == 0 {
		if p.find == "" {
			return ""
		}
		return "no matches"
	}
	count := fmt.Sprintf("%d of %d", p.hitAt+1, len(p.hits))
	if b := p.bufs[p.stream]; b != nil && !b.eof {
		count += fmt.Sprintf(" · Searched the first %s. L loads the rest", sshBytes(int64(len(b.text))))
	}
	return count
}

// emptyBody is the viewer's copy when there is nothing to page through, or a
// banner above text that is there. nil means draw the lines.
func (p *sshPager) emptyBody(block *protocol.SSHBlock) []string {
	if p.cmdOnly {
		return nil
	}
	b := p.buf(p.stream)
	if b.err != "" && b.text == "" {
		return []string{badText.Render("Could not load this output.") + faintText.Render("  esc, then enter to try again")}
	}
	if !b.loaded {
		return []string{faintText.Render("Loading…")}
	}
	if b.pruned {
		return []string{warnText.Render("Lola removed this output to stay within its storage limit. The command, exit code and timing stay on record.")}
	}
	if b.text != "" || b.tail != "" {
		return nil
	}
	if block != nil {
		switch {
		case block.Status == protocol.SSHBlockRunning:
			return []string{faintText.Render("No output yet.")}
		case block.StartedAt.IsZero():
			return []string{faintText.Render("This command did not run, so there is no output.")}
		case block.DroppedBytes > 0:
			return []string{warnText.Render(fmt.Sprintf("Lola could not store this output. %s were lost. The exit code is still accurate.", sshBytes(block.DroppedBytes)))}
		}
	}
	return []string{faintText.Render("No output.")}
}

func sshTierRuleFaint(w int) string { return faintText.Render(strings.Repeat("─", max(0, w))) }

// --- handback -----------------------------------------------------------------

// sshHandback is the full-screen handback form. The transcript is shown by
// default and a line can be deleted before anything leaves; nothing is written to
// disk for $EDITOR to read. Whether what the human did goes to a model provider is
// an explicit choice each time: two shifted answers, neither a default, and they
// live on the transcript rather than the note so typing "Y" in a sentence stays a
// letter.
type sshHandback struct {
	conn      string
	host      string
	note      string
	lines     []string
	cursor    int
	top       int
	onNote    bool
	loading   bool
	truncated bool
	failed    bool
	sending   bool
	err       string
}

func (m *rootModel) sshOpenHandback(c *protocol.SSHConnInfo) tea.Cmd {
	s := &m.ssh
	s.handback = &sshHandback{conn: c.ID, host: sshHostLabel(c), onNote: true, loading: true}
	return s.sshAction("preview", protocol.CmdSSHHandbackPreview, protocol.SSHConnArgs{Conn: c.ID}, c.ID, "")
}

func (s *sshModel) handlePreview(v sshDoneMsg) {
	h := s.handback
	if h == nil || h.conn != v.conn {
		return
	}
	h.loading = false
	if v.err != nil {
		h.failed = true
		return
	}
	var d protocol.SSHHandbackPreviewData
	if err := json.Unmarshal(v.raw, &d); err != nil {
		h.failed = true
		return
	}
	h.truncated = d.Truncated
	if text := strings.TrimRight(d.Transcript, "\n"); text != "" {
		h.lines = strings.Split(text, "\n")
	}
}

func (h *sshHandback) text() string { return strings.Join(h.lines, "\n") }

func (m *rootModel) sshHandbackKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := &m.ssh
	h := s.handback
	if h.sending {
		return m, nil
	}
	key := k.String()
	if key == "esc" {
		// Keep driving: nothing changes, and the keyboard goes back to the shell.
		conn := s.connByID(h.conn)
		s.handback = nil
		if conn != nil && conn.Tab != "" && conn.TabAlive {
			return m, m.sshFocusTab(conn.Tab)
		}
		return m, nil
	}
	if key == "tab" || key == "shift+tab" {
		h.onNote = !h.onNote
		return m, nil
	}
	if h.onNote {
		switch key {
		case "backspace":
			if r := []rune(h.note); len(r) > 0 {
				h.note = string(r[:len(r)-1])
			}
		case "enter":
			// Deliberately nothing: no key may choose for the human.
		default:
			if k.Text != "" {
				h.note += k.Text
			}
		}
		return m, nil
	}
	h.err = ""
	switch key {
	case "j", "down":
		h.cursor = min(h.cursor+1, max(0, len(h.lines)-1))
	case "k", "up":
		h.cursor = max(h.cursor-1, 0)
	case "g":
		h.cursor = 0
	case "G":
		h.cursor = max(0, len(h.lines)-1)
	case "d":
		if h.cursor < len(h.lines) {
			h.lines = append(h.lines[:h.cursor:h.cursor], h.lines[h.cursor+1:]...)
			h.cursor = min(h.cursor, max(0, len(h.lines)-1))
		}
	case "Y":
		if strings.TrimSpace(h.text()) == "" {
			h.err = "Nothing to send."
			return m, nil
		}
		return m, m.sshSendHandback(h.text())
	case "S":
		return m, m.sshSendHandback("")
	}
	return m, nil
}

func (m *rootModel) sshSendHandback(transcript string) tea.Cmd {
	s := &m.ssh
	h := s.handback
	h.sending = true
	return s.sshAction("handback", protocol.CmdSSHHandback, protocol.SSHHandbackArgs{
		Conn: h.conn, Note: strings.TrimSpace(h.note), Transcript: transcript,
	}, h.conn, "")
}

func (m *rootModel) sshHandbackView() []string {
	s := &m.ssh
	h := s.handback
	w, H := m.sshWidth(), m.sshHeight()
	out := []string{sshAccentBold.Render("HAND BACK TO " + strings.ToUpper(s.agentName())), ""}

	note := sshLine(h.note)
	if h.onNote {
		note += "_"
	}
	label := faintText.Render("Note: ")
	if h.onNote {
		label = boxTitleHi.Render("Note: ")
	}
	out = append(out, previewLine(label+note, w), "")

	since := ""
	if c := s.connByID(h.conn); c != nil && !c.WheelSince.IsZero() {
		since = " since " + c.WheelSince.Local().Format("15:04")
	}
	size := int64(len(h.text()))
	title := fmt.Sprintf("Your terminal%s · %s of %s", since, sshBytes(size), sshBytes(protocol.SSHMaxHandbackBytes))
	styled := faintText.Render(title)
	if !h.onNote {
		styled = boxTitleHi.Render(title) + faintText.Render("   j/k move · d delete line")
	}
	out = append(out, previewLine(styled, w))

	var notes []string
	switch {
	case h.failed:
		notes = append(notes, warnText.Render("Could not read your terminal. You can still hand back with a note."))
	case h.truncated:
		notes = append(notes, warnText.Render("Your terminal history did not go back far enough. This is what is left, and some of your session may be missing."))
	}
	if size >= protocol.SSHMaxHandbackBytes {
		notes = append(notes, warnText.Render("Only the last 8 KB can be sent."))
	}
	notes = append(notes, faintText.Render(protocol.SSHTranscriptWarning))
	if h.err != "" {
		notes = append(notes, badText.Render(h.err))
	}

	bodyH := max(1, H-len(out)-len(notes)-3)
	var body []string
	switch {
	case h.loading:
		body = append(body, faintText.Render("Reading your terminal…"))
	case len(h.lines) == 0 && !h.failed:
		body = append(body, faintText.Render("(nothing to send)"))
	default:
		if h.cursor < h.top {
			h.top = h.cursor
		}
		if h.cursor >= h.top+bodyH {
			h.top = h.cursor - bodyH + 1
		}
		for i := h.top; i < len(h.lines) && len(body) < bodyH; i++ {
			line := sshLine(h.lines[i])
			if i == h.cursor && !h.onNote {
				body = append(body, highlightRow(line, w, bgSGR(colSel)))
			} else {
				body = append(body, previewLine(line, w))
			}
		}
	}
	for len(body) < bodyH {
		body = append(body, "")
	}
	out = append(out, body...)
	for _, n := range notes {
		out = append(out, previewLine(n, w))
	}
	out = append(out, "")
	answer := faintText.Render("tab to the transcript to answer · esc keep driving")
	if !h.onNote {
		with := warnText.Render("Y") + faintText.Render(" hand back with this text")
		if strings.TrimSpace(h.text()) == "" {
			with = faintText.Render("Nothing to send")
		}
		answer = with + faintText.Render(" · ") + warnText.Render("S") + faintText.Render(" hand back without it · tab note · esc keep driving")
	}
	if h.sending {
		answer = faintText.Render("Handing back…")
	}
	return append(out, previewLine(answer, w))
}

// --- host picker --------------------------------------------------------------

const (
	sshPickHost = iota
	sshPickBrief
)

// sshPicker starts a direct session. Starting it IS the go for connecting, so the
// footer says what that does before anyone presses enter, and a refused host is
// listed rather than hidden, because its message names the way out.
type sshPicker struct {
	step      int
	hosts     []protocol.SSHHostInfo
	cursor    int
	preselect string
	loading   bool
	err       string
	predates  bool
	brief     string
	drive     bool
	sending   bool
	// session is the selected session a host can be connected to with c, "" when
	// there is none or it is itself a direct session.
	session string
}

func (m *rootModel) openSSHPicker(host string) tea.Cmd {
	s := &m.ssh
	s.picker = &sshPicker{preselect: host, loading: true}
	return s.sshAction("hosts", protocol.CmdSSHHosts, nil, "", "")
}

func (s *sshModel) handleHosts(v sshDoneMsg) {
	p := s.picker
	if p == nil {
		return
	}
	p.loading = false
	if v.err != nil {
		p.err = sshLine(v.err.Error())
		p.predates = strings.Contains(p.err, "unknown cmd")
		return
	}
	var d protocol.SSHHostsData
	if err := json.Unmarshal(v.raw, &d); err != nil {
		p.err = err.Error()
		return
	}
	p.hosts = d.Hosts
	p.cursor = 0
	first := -1
	for i, h := range p.hosts {
		if h.Name == p.preselect {
			p.cursor = i
			return
		}
		if first < 0 && h.DirectRefused == "" {
			first = i
		}
	}
	if first >= 0 {
		p.cursor = first
	}
}

func (p *sshPicker) host() *protocol.SSHHostInfo {
	if p.cursor < 0 || p.cursor >= len(p.hosts) {
		return nil
	}
	return &p.hosts[p.cursor]
}

// sshHostSession is a live direct session on this host, if any: starting another
// stays allowed, the human should just know.
func (m *rootModel) sshHostSession(name string) *protocol.SessionInfo {
	if m.sessions.data == nil {
		return nil
	}
	for i := range m.sessions.data.Sessions {
		si := &m.sessions.data.Sessions[i]
		if si.SSH == nil || si.Location != "ssh" {
			continue
		}
		for j := range si.SSH.Connections {
			if c := &si.SSH.Connections[j]; c.Host == name && sshConnLive(c) {
				return si
			}
		}
	}
	return nil
}

func (m *rootModel) sshPickerKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := &m.ssh
	p := s.picker
	if p.sending {
		return m, nil
	}
	key := k.String()
	if p.step == sshPickBrief {
		switch key {
		case "esc":
			p.step, p.err = sshPickHost, ""
		case "tab", "shift+tab":
			p.drive = !p.drive
		case "enter":
			h := p.host()
			if h == nil {
				return m, nil
			}
			p.sending, p.err = true, ""
			args := protocol.SSHOpenArgs{Host: h.Name, Brief: strings.TrimSpace(p.brief)}
			if p.drive {
				args.Wheel = protocol.SSHWheelHuman
			}
			return m, s.sshAction("open", protocol.CmdSSHOpen, args, "", "")
		case "backspace":
			if r := []rune(p.brief); len(r) > 0 {
				p.brief = string(r[:len(r)-1])
			}
		default:
			if k.Text != "" {
				p.brief += k.Text
			}
		}
		return m, nil
	}
	switch key {
	case "esc", "q":
		s.picker = nil
	case "j", "down":
		p.cursor = min(p.cursor+1, max(0, len(p.hosts)-1))
	case "k", "up":
		p.cursor = max(p.cursor-1, 0)
	case "enter":
		h := p.host()
		if h == nil {
			return m, nil
		}
		if h.DirectRefused != "" {
			return m, nil // the footer already says why, whole
		}
		p.step = sshPickBrief
	case "c":
		h := p.host()
		if h == nil || p.session == "" {
			return m, nil
		}
		p.sending, p.err = true, ""
		return m, s.sshAction("connect", protocol.CmdSSHConnect, protocol.SSHConnectArgs{Session: p.session, Host: h.Name}, "", "")
	case "o":
		if h := p.host(); h != nil {
			if si := m.sshHostSession(h.Name); si != nil {
				s.picker = nil
				return m, tea.Batch(m.selectID(si.ID), m.openSSHWorkspace(si.ID))
			}
		}
	case "S":
		if len(p.hosts) == 0 {
			s.picker = nil
			m.settings = newSettingsForm(m.cfgPath, m.cfg)
			m.settings.tab = stSSH
		}
	}
	return m, nil
}

// handleSSHConnected opens the workspace of a session a human just connected, and
// hands them the terminal: signing in is theirs to do next.
func (m *rootModel) handleSSHConnected(v sshDoneMsg) tea.Cmd {
	s := &m.ssh
	p := s.picker
	if p == nil {
		return nil
	}
	if v.err != nil {
		p.sending, p.err = false, sshLine(v.err.Error())
		return nil
	}
	var d protocol.SSHConnectData
	if err := json.Unmarshal(v.raw, &d); err != nil {
		p.sending, p.err = false, err.Error()
		return nil
	}
	id := p.session
	s.picker = nil
	cmds := []tea.Cmd{m.openSSHWorkspace(id), fetchSessionsCmd}
	s.setConnInfo(d.Conn)
	s.syncConn()
	if d.Conn.Tab != "" {
		cmds = append(cmds, m.sshFocusTab(d.Conn.Tab))
	} else {
		s.focusAfter = d.Conn.ID
	}
	return tea.Batch(cmds...)
}

// handleSSHOpened installs a started session. The list has not heard of it yet,
// so selecting it waits for the sessions fetch this triggers.
func (m *rootModel) handleSSHOpened(v sshDoneMsg) tea.Cmd {
	s := &m.ssh
	p := s.picker
	if v.err != nil {
		if p != nil {
			p.sending, p.err = false, sshLine(v.err.Error())
		}
		return nil
	}
	var d protocol.SSHOpenData
	if err := json.Unmarshal(v.raw, &d); err != nil {
		if p != nil {
			p.sending, p.err = false, err.Error()
		}
		return nil
	}
	s.picker = nil
	s.pendingOpen = &d
	return fetchSessionsCmd
}

// finishSSHOpen selects a started session once the list carries it, opens its
// workspace, and hands the human the terminal: signing in is theirs to do next.
func (m *rootModel) finishSSHOpen() tea.Cmd {
	s := &m.ssh
	d := s.pendingOpen
	if d == nil || m.sessions.byID(d.Session) == nil {
		return nil
	}
	s.pendingOpen = nil
	m.view = viewCockpit
	m.sessions.filter = Filter{}
	cmds := []tea.Cmd{m.selectID(d.Session), m.openSSHWorkspace(d.Session)}
	s.setConnInfo(d.Conn)
	s.syncConn()
	if d.Conn.Tab != "" {
		cmds = append(cmds, m.sshFocusTab(d.Conn.Tab))
	} else {
		s.focusAfter = d.Conn.ID
	}
	return tea.Batch(cmds...)
}

func (m *rootModel) sshPickerView() []string {
	p := m.ssh.picker
	w, H := m.sshWidth(), m.sshHeight()
	out := []string{sshAccentBold.Render("New SSH session"), sshTierRuleFaint(w)}

	switch {
	case p.loading:
		return append(out, faintText.Render("Reading your hosts…"))
	case p.predates:
		return append(out, warnText.Render("This daemon predates SSH sessions. Restart it to use them."), "", faintText.Render("esc close"))
	case p.err != "" && len(p.hosts) == 0:
		return append(out, badText.Render(p.err), "", faintText.Render("esc close"))
	case len(p.hosts) == 0:
		return append(out,
			sshInk.Bold(true).Render("No hosts yet."),
			faintText.Render("Add one in Settings, using an alias from your ssh config. Lola holds no address and no key."),
			"", faintText.Render("S open SSH host settings · esc close"))
	}

	nameW, idW := 4, 8
	for _, h := range p.hosts {
		nameW = max(nameW, lipgloss.Width(sshPickName(h)))
		idW = max(idW, lipgloss.Width(sshPickIdentity(h)))
	}
	nameW, idW = min(nameW, 24), min(idW, 30)
	for i, h := range p.hosts {
		cursor := "  "
		if i == p.cursor {
			cursor = boxTitleHi.Render("› ")
		}
		right := ""
		switch {
		case h.DirectRefused != "":
			right = faintText.Render("Cannot start")
		case m.sshHostSession(h.Name) != nil:
			si := m.sshHostSession(h.Name)
			right = sshInfo.Render("Open in " + sshLine(truncPlain(dash(si.Title), 24)))
		case h.DirectSandbox == config.SandboxDocker:
			right = faintText.Render("Agent runs in a container")
		case sshTierNorm(h.Tier) == protocol.SSHTierProduction:
			right = warnText.Render("Agent runs on this Mac")
		default:
			right = faintText.Render("Agent runs on this Mac")
		}
		tier := padTo(sshTierTag(h.Tier), 11)
		name := padTo(truncPlain(sshPickName(h), nameW), nameW)
		id := padTo(truncPlain(sshPickIdentity(h), idW), idW)
		if h.DirectRefused != "" {
			tier = padTo(faintText.Render(sshTierWord(h.Tier)), 11)
			name, id = faintText.Render(name), faintText.Render(id)
		} else {
			id = faintText.Render(id)
		}
		out = append(out, previewLine(cursor+tier+" "+name+"  "+id+"  "+right, w))
	}
	out = append(out, "")

	h := p.host()
	if h == nil {
		return out
	}
	if h.DirectRefused != "" {
		for _, l := range sshWrap(h.DirectRefused, max(10, w-2)) {
			out = append(out, previewLine(warnText.Render(l), w))
		}
		out = append(out, "")
	}
	agentName := sshAgentName(h.DirectAgent)
	if p.step == sshPickBrief {
		out = append(out, previewLine(boxTitleHi.Render("What should "+agentName+" look at?"), w))
		out = append(out, previewLine(sshLine(p.brief)+"_", w))
		out = append(out, previewLine(faintText.Render("Optional. Without it, "+agentName+" waits for you."), w), "")
		agentCell, youCell := boxTitleHi.Render("["+agentName+"]"), faintText.Render(" You ")
		if p.drive {
			agentCell, youCell = faintText.Render(" "+agentName+" "), sshWheelYou.Render(" You ")
		}
		out = append(out, previewLine(faintText.Render("Who drives first  ")+agentCell+" "+youCell+faintText.Render("   tab switches"), w), "")
	}
	out = append(out, previewLine(faintText.Render("Lola asks here if signing in needs anything."), w))
	switch {
	case p.step == sshPickBrief && p.drive:
		out = append(out, previewLine(faintText.Render("You start at the wheel. "+agentName+" can read what you tell it and cannot run commands until you hand back."), w))
	case sshTierNorm(h.Tier) == protocol.SSHTierProduction:
		out = append(out, previewLine(faintText.Render("Production asks you to approve every command."), w))
	default:
		out = append(out, previewLine(faintText.Render("Each command asks for your approval, unless you open a grant."), w))
	}
	out = append(out, previewLine(faintText.Render(protocol.SSHDataWarning), w))
	if h.DirectRefused == "" && h.DirectSandbox != config.SandboxDocker {
		out = append(out, previewLine(warnText.Render(protocol.SSHUnsandboxedNote), w))
	}
	out = append(out, "")
	if p.err != "" {
		out = append(out, previewLine(badText.Render(p.err), w))
	}
	var hint string
	switch {
	case p.sending:
		hint = "Connecting to " + sshLine(h.Name) + "…"
	case p.step == sshPickBrief:
		hint = "enter connect to " + sshLine(h.Name) + " · tab who drives · esc back"
	case h.DirectRefused != "":
		hint = "j/k move · esc close"
	default:
		hint = "j/k move · enter new session · esc close"
		if m.sshHostSession(h.Name) != nil {
			hint = "j/k move · enter start another · o go to that session · esc close"
		}
	}
	if p.session != "" && !p.sending && p.step == sshPickHost {
		hint = "c connect this session · " + hint
	}
	out = append(out, previewLine(faintText.Render(hint), w))
	if len(out) > H {
		out = out[len(out)-H:]
	}
	return out
}

func sshPickName(h protocol.SSHHostInfo) string {
	if h.Label != "" {
		return sshLine(h.Label)
	}
	return sshLine(h.Name)
}

func sshPickIdentity(h protocol.SSHHostInfo) string {
	if h.User != "" {
		return sshLine(h.User + "@" + h.Alias)
	}
	return sshLine(h.Alias) + " (ssh config user)"
}

// sshTierWord is the tier's word without its colour, for a greyed row.
func sshTierWord(tier string) string { return strings.ToUpper(sshTierNorm(tier)) }

// sshAgentGone reports whether the session's agent has exited.
func sshAgentGone(si *protocol.SessionInfo) bool {
	return si != nil && displayOf(*si) == state.DisplayGone
}
