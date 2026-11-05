package tui

// sshkeys.go routes the workspace overlay's keys and answers the daemon's replies.
// The overlay owns the keyboard while open: anything not listed is swallowed, so
// a stray "x" can never ask to kill a session from in here.
//
// The heavy answers are all SHIFTED, Y and N, and they read a card the human is
// looking at: bare "y" is not accepted, and a card is ignored for sshArmDelay
// after it appears, so a keystroke already on its way cannot approve a command.

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/sshtab"
	"github.com/sushidev-team/lola/internal/state"
)

// sshAttach (re)attaches the embed to the selection's active tab, and reports
// whether there is a terminal to give the keyboard to. A seam so tests never exec
// tmux.
var sshAttach = func(m *rootModel) (tea.Cmd, bool) {
	cmd := m.syncAgentPreview()
	return cmd, m.currentEmbed() != nil
}

func (m *rootModel) updateSSH(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := &m.ssh
	m.sessions.flash = "" // an old cockpit flash must not read as this overlay's
	if s.handback != nil {
		return m.sshHandbackKey(k)
	}
	if s.pager != nil {
		return m, s.pagerKey(k, m)
	}
	if s.input != sshInputNone {
		return m, m.sshInputKey(k)
	}
	if s.confirmClose != "" {
		conn := s.confirmClose
		s.confirmClose = ""
		if str := k.String(); str == "y" || str == "Y" {
			return m, s.sshAction("close", protocol.CmdSSHClose, protocol.SSHConnArgs{Conn: conn}, conn, "")
		}
		return m, nil
	}
	s.flash = ""
	key := k.String()
	switch key {
	case "esc":
		if s.runCheck != nil {
			s.runCheck = nil
			s.syncCard()
			return m, nil
		}
		if s.findQuery != "" {
			s.findQuery, s.findHits = "", nil
			return m, nil
		}
		m.closeSSH()
		return m, nil
	case "?":
		m.showHelp = true
		return m, nil
	case "j", "down":
		s.move(1)
	case "k", "up":
		s.move(-1)
	case "g":
		s.moveEdge(true)
	case "G":
		s.moveEdge(false)
	case "pgdown", "ctrl+d":
		s.move(10)
	case "pgup", "ctrl+u":
		s.move(-10)
	case "enter":
		return m, s.enter(m)
	case "left":
		if r := s.selectedRow(); r != nil {
			delete(s.expanded, r.key)
		}
	case "Y":
		return m, m.sshConfirm()
	case "N":
		if c := s.card(); c != nil && c.kind == sshCardPrompt {
			if c.conn.Prompt.Kind == protocol.SSHPromptConfirm && s.armed() {
				return m, s.sshPromptAnswer(c.conn, protocol.SSHPromptNo, "")
			}
			return m, nil
		}
		m.sshDeny()
	case "W":
		return m, m.sshWheel()
	case "T":
		return m, m.sshTerminalKey()
	case "A":
		return m, m.sshAgentTab()
	case "i":
		m.sshTalk()
	case "a":
		if b := s.selectedBlock(); b != nil {
			s.input, s.inputBuf = sshInputSay, s.drafts[s.session]+"About "+b.ID+": "
		} else {
			s.flash = "Select a command to ask about it."
		}
	case "S":
		if b := s.selectedBlock(); b != nil && b.Private {
			return m, s.sshAction("share", protocol.CmdSSHShareBlock, protocol.SSHShareBlockArgs{Session: s.session, Block: b.ID}, "", b.ID)
		}
		s.flash = "Select a command you ran to share it."
	case "R":
		return m, m.sshRecallLast()
	case "P":
		m.sshProposalCheck()
	case "d":
		m.sshDeclinePrompt()
	case "1", "2", "3", "4", "5", "6":
		return m, m.sshPickChoice(key)
	case "F":
		s.filter = (s.filter + 1) % 3
		s.top = 0
		s.findHits = nil
		if s.follow {
			if rows := s.rows(); len(rows) > 0 {
				s.selKey = rows[len(rows)-1].key
			}
		}
	case "/":
		s.input, s.inputBuf, s.findQuery, s.findHits, s.findAt = sshInputFind, "", "", nil, 0
	case "X":
		if c := s.card(); c != nil && c.kind == sshCardPrompt {
			return m, s.sshPromptAnswer(c.conn, protocol.SSHPromptCancel, "")
		}
		return m, m.sshStopKey()
	case "C":
		m.sshCloseKey()
	case "V":
		if c := s.conn(); c != nil && c.Grant != nil {
			return m, s.sshAction("revoke", protocol.CmdSSHRevokeGrant, protocol.SSHConnArgs{Conn: c.ID}, c.ID, "")
		}
	case "e":
		s.followEvidence()
	case "b":
		s.jumpBack()
	case "o":
		return m, s.openSelectedURL()
	case "<", ",":
		s.cycleConn(-1)
	case ">", ".":
		s.cycleConn(1)
	case "t":
		// The grant control exists only where the host allows one, and is off
		// again on every new card: an approval must never inherit a grant.
		if c := s.card(); c != nil && c.kind == sshCardApproval {
			if ch := sshGrantChoices(c.conn); len(ch) > 0 {
				s.grantIdx = (s.grantIdx + 1) % (len(ch) + 1)
			}
		}
	case "+":
		host := ""
		if c := s.conn(); c != nil {
			host = c.Host
		}
		m.closeSSH()
		return m, m.openSSHPicker(host)
	}
	return m, nil
}

func (s *sshModel) cycleConn(dir int) {
	shown := s.switchable()
	n := len(shown)
	if n < 2 {
		return
	}
	i := slices.IndexFunc(shown, func(c protocol.SSHConnInfo) bool { return c.ID == s.connID })
	s.connID = shown[((i+dir)%n+n)%n].ID
	s.syncCard()
}

// enter digs one level: a long approval's whole command first (the band says so),
// then the selected row. A block expands to its facts, then opens its output.
func (s *sshModel) enter(m *rootModel) tea.Cmd {
	if c := s.card(); c != nil && c.kind == sshCardApproval && s.cmdNeedsReading(c.block, m.sshWidth()-6) {
		s.openCommandPager(c.block)
		return nil
	}
	r := s.selectedRow()
	if r == nil {
		return nil
	}
	if r.fold != nil {
		s.expanded[r.key] = true
		s.selKey = r.fold[0].ID
		s.follow = false
		return nil
	}
	if b := r.item.Block; b != nil && s.expanded[r.key] {
		return s.openOutputPager(b)
	}
	if s.expanded[r.key] {
		delete(s.expanded, r.key)
	} else {
		s.expanded[r.key] = true
	}
	return nil
}

// sshConfirm is Y: whatever the band names.
func (m *rootModel) sshConfirm() tea.Cmd {
	s := &m.ssh
	s.syncCard()
	c := s.card()
	if c == nil || !s.armed() {
		return nil
	}
	switch c.kind {
	case sshCardApproval:
		if s.cmdNeedsReading(c.block, m.sshWidth()-6) {
			s.flash = "This command is long. Press enter to read all of it first."
			return nil
		}
		grant := s.grantMinutes(c.conn)
		s.cardKey = "" // re-arm: the next card must not be answered by a double press
		return s.sshAction("approve", protocol.CmdSSHApprove, protocol.SSHApproveArgs{
			Conn: c.block.Conn, Block: c.block.ID, Decision: protocol.SSHDecisionApprove, GrantMinutes: grant,
		}, c.block.Conn, c.block.ID)
	case sshCardPrompt:
		s.cardKey = ""
		if c.conn.Prompt.Kind == protocol.SSHPromptConfirm {
			return s.sshPromptAnswer(c.conn, protocol.SSHPromptYes, "")
		}
		s.sshOpenSecret(c.conn)
		return nil
	case sshCardGo, sshCardReconnect, sshCardFailed:
		s.cardKey = ""
		return s.sshAction("go", protocol.CmdSSHGo, protocol.SSHGoArgs{Conn: c.conn.ID}, c.conn.ID, "")
	case sshCardProposal:
		e := c.element
		rev := s.runCheck.revision
		s.runCheck = nil
		s.cardKey = ""
		return s.sshAction("proposal", protocol.CmdSSHProposal, protocol.SSHProposalArgs{
			Session: s.session, Element: e.ID, Revision: rev, Action: protocol.SSHProposalRun,
		}, "", "")
	}
	return nil
}

// sshDeny is N: it opens the reason prompt, pinned to the card on screen.
func (m *rootModel) sshDeny() {
	s := &m.ssh
	c := s.card()
	if c == nil {
		return
	}
	switch c.kind {
	case sshCardApproval:
		s.input, s.inputBuf, s.inputBlock, s.inputConn = sshInputDeny, "", c.block.ID, c.block.Conn
	case sshCardGo:
		s.input, s.inputBuf, s.inputConn = sshInputDenyConn, "", c.conn.ID
	case sshCardProposal:
		s.runCheck = nil
		s.syncCard()
	}
}

// sshRunLine runs a `!command` typed in the talk field. On production the first enter
// only arms it, so a mistyped sentence cannot execute there.
func (s *sshModel) sshRunLine(cmd, typed string) tea.Cmd {
	c := s.conn()
	switch {
	case cmd == "":
		return nil
	case c == nil || c.State != protocol.SSHConnConnected:
		s.input, s.inputBuf = sshInputSay, typed
		s.flash = "Not connected, so nothing can run."
		return nil
	case sshTierNorm(c.Tier) == protocol.SSHTierProduction && s.runArmed != cmd:
		s.input, s.inputBuf, s.runArmed = sshInputSay, typed, cmd
		s.flash = "Production: press enter again to run this on " + sshLine(c.Host) + "."
		return nil
	}
	s.runArmed = ""
	delete(s.drafts, s.session)
	s.follow = true
	return s.sshAction("run", protocol.CmdSSHHumanRun, protocol.SSHHumanRunArgs{Session: s.session, Conn: c.ID, Command: cmd}, c.ID, "")
}

// sshWheel is W: taking is instant and asks nothing, because a dialog would be a
// delay at the exact moment someone wants to stop something. Holding it already
// opens the handback form.
func (m *rootModel) sshWheel() tea.Cmd {
	s := &m.ssh
	c := s.conn()
	if !sshConnLive(c) {
		s.flash = "This connection has ended."
		return nil
	}
	if c.Wheel == protocol.SSHWheelHuman {
		return m.sshOpenHandback(c)
	}
	return s.sshAction("take", protocol.CmdSSHTakeWheel, protocol.SSHTakeWheelArgs{Conn: c.ID}, c.ID, "")
}

// sshTerminalKey is T: the host's terminal, focused. A connection whose shell has
// ended but whose master is up gets a new mux tab first, with no sign-in.
// sshOpenSecret opens the masked field for a question. What is typed stays in inputBuf,
// which no draft keeps and the prompt line never draws.
func (s *sshModel) sshOpenSecret(c *protocol.SSHConnInfo) {
	s.input, s.inputBuf, s.inputConn, s.inputEl = sshInputSecret, "", c.ID, c.Prompt.ID
}

// sshPromptAnswer answers a question without text: yes, no or cancel.
func (s *sshModel) sshPromptAnswer(c *protocol.SSHConnInfo, action, secret string) tea.Cmd {
	return s.sshAction("prompt", protocol.CmdSSHPromptAnswer, protocol.SSHPromptAnswerArgs{
		Conn: c.ID, Prompt: c.Prompt.ID, Action: action, Secret: secret,
	}, c.ID, "")
}

func (m *rootModel) sshTerminalKey() tea.Cmd {
	s := &m.ssh
	c := s.conn()
	switch {
	case c == nil:
		return nil
	case c.State == protocol.SSHConnConnected && !c.TabAlive:
		s.focusAfter = c.ID
		return s.sshAction("terminal", protocol.CmdSSHTerminal, protocol.SSHConnArgs{Conn: c.ID}, c.ID, "")
	case c.Tab == "":
		s.flash = "No connection yet. Nothing has been attempted."
		return nil
	}
	return m.sshFocusTab(c.Tab)
}

// sshAgentTab is A: the agent's own pane, for everything the timeline does not
// carry. ^q comes back.
func (m *rootModel) sshAgentTab() tea.Cmd {
	s := &m.ssh
	si := m.sessions.byID(s.session)
	if si == nil || displayOf(*si) == state.DisplayGone {
		s.flash = s.agentName() + " has exited."
		return nil
	}
	cmd := m.selectSSHSession()
	if m.embedTab == nil {
		m.embedTab = map[string]int{}
	}
	m.embedTab[s.session] = 0
	return m.sshFocusEmbed(cmd)
}

// selectSSHSession makes the overlay's session the cockpit's selection: the embed
// always follows the selection.
func (m *rootModel) selectSSHSession() tea.Cmd {
	if sel := m.sessions.selected(); sel != nil && sel.ID == m.ssh.session {
		return nil
	}
	if m.sessions.data == nil {
		return nil
	}
	return m.selectID(m.ssh.session)
}

// sshFocusTab attaches the embed to one of the session's ssh tabs and gives it
// the keyboard. The overlay stays open behind it, so ^q comes back to it.
func (m *rootModel) sshFocusTab(name string) tea.Cmd {
	s := &m.ssh
	if sshtab.Index(s.session, name) == 0 {
		return nil
	}
	cmd := m.selectSSHSession()
	if m.shellNames == nil {
		m.shellNames = map[string][]string{}
	}
	m.refreshShells(s.session)
	i := slices.Index(m.shellNames[s.session], name)
	if i < 0 {
		// The daemon named it, and tmux's list only lags: trust the record.
		m.shellNames[s.session] = append(m.shellNames[s.session], name)
		i = len(m.shellNames[s.session]) - 1
	}
	if m.embedTab == nil {
		m.embedTab = map[string]int{}
	}
	m.embedTab[s.session] = i + 1
	return m.sshFocusEmbed(cmd)
}

func (m *rootModel) sshFocusEmbed(first tea.Cmd) tea.Cmd {
	attach, ok := sshAttach(m)
	if !ok {
		m.ssh.flash = "Could not open that terminal."
		return tea.Batch(first, attach)
	}
	m.embedFocused = true
	return tea.Batch(first, attach)
}

// syncFocusAfter focuses the terminal a go or reconnect just opened, once the
// connection names it. The next thing that happens there is the human's to answer.
func (s *sshModel) syncFocusAfter(m *rootModel) tea.Cmd {
	if s.focusAfter == "" {
		return nil
	}
	c := s.connByID(s.focusAfter)
	if c == nil || c.Tab == "" {
		return nil
	}
	s.focusAfter = ""
	if m.embedFocused {
		return nil
	}
	return m.sshFocusTab(c.Tab)
}

func (m *rootModel) sshTalk() {
	s := &m.ssh
	if e := s.selectedElement(); e != nil && e.Kind == "question" && e.Outcome == nil {
		s.input, s.inputBuf, s.inputEl, s.inputRev = sshInputAnswer, "", e.ID, e.Revision
		return
	}
	s.input, s.inputBuf = sshInputSay, s.drafts[s.session]
}

// sshRecallLast is R: the newest message still waiting.
func (m *rootModel) sshRecallLast() tea.Cmd {
	s := &m.ssh
	for i := len(s.items) - 1; i >= 0; i-- {
		if h := s.items[i].Human; h != nil && !h.Delivered && !h.Recalled {
			return s.sshAction("recall", protocol.CmdSSHRecall, protocol.SSHRecallArgs{Session: s.session, Item: s.items[i].ID}, "", "")
		}
	}
	s.flash = "Nothing is waiting to be recalled."
	return nil
}

func (m *rootModel) sshProposalCheck() {
	s := &m.ssh
	e := s.selectedElement()
	if e == nil || e.Kind != "proposal" {
		return
	}
	if e.Outcome != nil {
		s.flash = "This proposal has been decided."
		return
	}
	if !sshConnLive(s.proposalConn(e)) {
		s.flash = "That connection has ended."
		return
	}
	s.runCheck = &sshRunCheck{element: e.ID, revision: e.Revision}
	s.syncCard()
}

func (m *rootModel) sshDeclinePrompt() {
	s := &m.ssh
	e := s.selectedElement()
	if e == nil || e.Kind != "proposal" || e.Outcome != nil {
		return
	}
	s.input, s.inputBuf, s.inputEl, s.inputRev = sshInputDecline, "", e.ID, e.Revision
}

func (m *rootModel) sshPickChoice(key string) tea.Cmd {
	s := &m.ssh
	e := s.selectedElement()
	if e == nil || e.Kind != "question" || e.Outcome != nil {
		return nil
	}
	n, _ := strconv.Atoi(key)
	if n < 1 || n > len(e.Choices) {
		return nil
	}
	return s.sshAction("answer", protocol.CmdSSHAnswer, protocol.SSHAnswerArgs{
		Session: s.session, Element: e.ID, Revision: e.Revision, Text: e.Choices[n-1],
	}, "", "")
}

// sshStopKey is X: the selected command, or the only one running. Stopping asks
// nothing, since it is the safe direction, and a second press changes nothing.
func (m *rootModel) sshStopKey() tea.Cmd {
	s := &m.ssh
	stoppable := func(b *protocol.SSHBlock) bool {
		return b != nil && (b.Status == protocol.SSHBlockRunning || b.Status == protocol.SSHBlockQueued)
	}
	b := s.selectedBlock()
	if !stoppable(b) {
		b = nil
		count := 0
		for i := range s.items {
			if it := s.items[i].Block; stoppable(it) {
				b = it
				count++
			}
		}
		if count > 1 {
			s.flash = "Several commands are running. Select the one to stop."
			return nil
		}
	}
	if b == nil {
		s.flash = "Nothing is running."
		return nil
	}
	return s.sshAction("stop", protocol.CmdSSHStop, protocol.SSHStopArgs{Conn: b.Conn, Block: b.ID}, b.Conn, b.ID)
}

func (m *rootModel) sshCloseKey() {
	s := &m.ssh
	if c := s.conn(); sshConnLive(c) {
		s.confirmClose = c.ID
	}
}

// followEvidence is e: from a claim to the exact command behind it, one key. It
// remembers where the reader was, because digging must not lose their place.
func (s *sshModel) followEvidence() {
	e := s.selectedElement()
	if e == nil {
		return
	}
	targets := s.elementTargets(e)
	if len(targets) == 0 {
		s.flash = "This claim links to no recorded command."
		return
	}
	i := s.evidenceAt[e.ID] % len(targets)
	s.evidenceAt[e.ID] = i + 1
	s.back = append(s.back, e.ID)
	s.jumpToBlock(targets[i])
}

func (s *sshModel) jumpToBlock(id string) {
	// The Findings lens hides commands, and an evidence link must always land.
	if s.filter == sshFilterFindings {
		s.filter = sshFilterAll
	}
	for i := range s.items {
		if b := s.items[i].Block; b != nil && b.ID == id {
			s.selKey = s.items[i].ID
			s.expanded[s.items[i].ID] = true
			s.follow = false
			return
		}
	}
}

func (s *sshModel) jumpBack() {
	if len(s.back) == 0 {
		return
	}
	s.selKey = s.back[len(s.back)-1]
	s.back = s.back[:len(s.back)-1]
	s.follow = false
}

// openSelectedURL opens the first address in the selected row, always through the
// daemon's openURL. The text is untrusted, so the http(s) guard stays over there.
func (s *sshModel) openSelectedURL() tea.Cmd {
	r := s.selectedRow()
	if r == nil || r.item == nil {
		return nil
	}
	var text string
	switch {
	case r.item.Element != nil:
		text = r.item.Element.Title + " " + r.item.Element.Body
	case r.item.Block != nil:
		text = r.item.Block.Preview
	case r.item.Human != nil:
		text = r.item.Human.Text
	}
	if u := sshURLRe.FindString(text); u != "" {
		return openURLCmd(u)
	}
	s.flash = "No address in this row."
	return nil
}

// sshInputKey drives the one-line prompt.
func (m *rootModel) sshInputKey(k tea.KeyPressMsg) tea.Cmd {
	s := &m.ssh
	kind := s.input
	switch k.String() {
	case "esc":
		if kind == sshInputSay {
			s.drafts[s.session] = s.inputBuf // leaving the field keeps the draft
		}
		if kind == sshInputFind {
			s.findQuery, s.findHits = "", nil
		}
		s.input, s.inputBuf = sshInputNone, ""
		return nil
	case "up":
		if kind == sshInputFind {
			s.walkFind(-1)
		}
		return nil
	case "down":
		if kind == sshInputFind {
			s.walkFind(1)
		}
		return nil
	case "enter":
		if kind == sshInputSecret {
			// Not trimmed: a space can be part of a passphrase.
			secret, conn, prompt := s.inputBuf, s.inputConn, s.inputEl
			s.input, s.inputBuf, s.inputEl = sshInputNone, "", ""
			if secret == "" {
				return nil
			}
			return s.sshAction("prompt", protocol.CmdSSHPromptAnswer, protocol.SSHPromptAnswerArgs{
				Conn: conn, Prompt: prompt, Action: protocol.SSHPromptSend, Secret: secret,
			}, conn, "")
		}
		text := strings.TrimSpace(s.inputBuf)
		s.input, s.inputBuf = sshInputNone, ""
		switch kind {
		case sshInputFind:
			s.findQuery = text
			return nil
		case sshInputSay:
			if text == "" {
				return nil
			}
			if cmd, ok := strings.CutPrefix(text, "!"); ok {
				return s.sshRunLine(strings.TrimSpace(cmd), text)
			}
			delete(s.drafts, s.session)
			s.follow = true
			return s.sshAction("say", protocol.CmdSSHSay, protocol.SSHSayArgs{Session: s.session, Conn: s.connID, Text: text}, "", "")
		case sshInputDeny:
			return s.sshAction("deny", protocol.CmdSSHApprove, protocol.SSHApproveArgs{
				Conn: s.inputConn, Block: s.inputBlock, Decision: protocol.SSHDecisionDeny, Reason: text,
			}, s.inputConn, s.inputBlock)
		case sshInputDenyConn:
			return s.sshAction("denyconn", protocol.CmdSSHDeny, protocol.SSHDenyArgs{Conn: s.inputConn, Reason: text}, s.inputConn, "")
		case sshInputDecline:
			return s.sshAction("decline", protocol.CmdSSHProposal, protocol.SSHProposalArgs{
				Session: s.session, Element: s.inputEl, Revision: s.inputRev, Action: protocol.SSHProposalDecline, Reason: text,
			}, "", "")
		case sshInputAnswer:
			if text == "" {
				return nil
			}
			return s.sshAction("answer", protocol.CmdSSHAnswer, protocol.SSHAnswerArgs{
				Session: s.session, Element: s.inputEl, Revision: s.inputRev, Text: text,
			}, "", "")
		}
		return nil
	case "backspace":
		if r := []rune(s.inputBuf); len(r) > 0 {
			s.inputBuf = string(r[:len(r)-1])
		}
		if kind == sshInputFind {
			s.runFind()
		}
		return nil
	}
	if k.Text != "" {
		if kind == sshInputSay && len(s.inputBuf)+len(k.Text) > protocol.SSHMaxSayBytes {
			return nil
		}
		s.inputBuf += k.Text
		if kind == sshInputFind {
			s.runFind()
		}
	}
	return nil
}

// runFind matches command, why, element text and human text in the loaded rows.
// Output has its own find, in the pager.
func (s *sshModel) runFind() {
	s.findQuery = strings.TrimSpace(s.inputBuf)
	s.findHits, s.findAt = nil, 0
	if s.findQuery == "" {
		return
	}
	q := strings.ToLower(s.findQuery)
	for _, r := range s.rows() {
		its := r.fold
		if r.item != nil {
			its = []*protocol.SSHTimelineItem{r.item}
		}
		for _, it := range its {
			if strings.Contains(strings.ToLower(sshItemText(it)), q) {
				s.findHits = append(s.findHits, r.key)
				break
			}
		}
	}
	if len(s.findHits) > 0 {
		s.selKey, s.follow = s.findHits[0], false
	}
}

func (s *sshModel) walkFind(dir int) {
	n := len(s.findHits)
	if n == 0 {
		return
	}
	s.findAt = ((s.findAt+dir)%n + n) % n
	s.selKey, s.follow = s.findHits[s.findAt], false
}

func sshItemText(it *protocol.SSHTimelineItem) string {
	var parts []string
	switch {
	case it.Block != nil:
		parts = append(parts, it.Block.Command, it.Block.Why)
	case it.Element != nil:
		e := it.Element
		parts = append(parts, e.Title, e.Body, e.Command)
		for _, st := range e.Steps {
			parts = append(parts, st.Text)
		}
		for _, mt := range e.Metrics {
			parts = append(parts, mt.Name, mt.Value)
		}
		parts = append(parts, e.Choices...)
	case it.Human != nil:
		parts = append(parts, it.Human.Text)
	case it.System != nil:
		parts = append(parts, it.System.Text, it.System.Note)
	}
	return strings.Join(parts, "\n")
}

// routeSSHPaste delivers a paste to whatever text field the overlay has open.
func (m *rootModel) routeSSHPaste(content string) {
	s := &m.ssh
	switch {
	case s.picker != nil && s.picker.step == sshPickBrief:
		s.picker.brief += pasteJoined(content)
	case s.handback != nil && s.handback.onNote:
		s.handback.note += pasteJoined(content)
	case s.pager != nil:
		if s.pager.finding {
			s.pager.find += pasteInline(content)
			s.pager.runFind()
		}
	case s.input == sshInputSecret:
		s.inputBuf += pasteInline(content) // a password manager's trailing newline is not part of it
	case s.input != sshInputNone:
		s.inputBuf += pasteJoined(content)
		if s.input == sshInputFind {
			s.runFind()
		}
	}
}

// handleSSHDone answers a reply. A refusal is shown as the daemon worded it, and
// the record is refetched either way: the timeline is what tells the truth.
func (m *rootModel) handleSSHDone(v sshDoneMsg) tea.Cmd {
	s := &m.ssh
	switch v.action {
	case "hosts":
		s.handleHosts(v)
		return nil
	case "open":
		return m.handleSSHOpened(v)
	case "connect":
		return m.handleSSHConnected(v)
	case "resolve":
		return nil
	}
	if v.gen != s.gen {
		return nil
	}
	if v.action == "preview" {
		s.handlePreview(v)
		return nil
	}
	refresh := s.fetchTimeline()
	if v.err != nil {
		s.noteActionErr(v)
		return refresh
	}
	switch v.action {
	case "prompt":
		var d protocol.SSHPromptAnswerData
		s.flash, s.flashOK = "Sent.", true
		if json.Unmarshal(v.raw, &d) == nil && d.Status == protocol.SSHPromptCancelled {
			s.flash = "Cancelled."
		}
		if c := s.connByID(v.conn); c != nil {
			c.Prompt = nil
		}
		s.syncCard()
	case "approve":
		var b protocol.SSHBlock
		if json.Unmarshal(v.raw, &b) == nil && b.Status == protocol.SSHBlockRefused {
			s.flash = "That command was already refused: " + sshLine(b.StatusDetail)
		}
		s.syncCard()
	case "deny":
		s.flash, s.flashOK = s.agentName()+" gets your reason with its next command.", true
	case "go":
		var c protocol.SSHConnInfo
		if json.Unmarshal(v.raw, &c) == nil && c.ID != "" {
			s.setConnInfo(c)
			if c.Tab != "" {
				return tea.Batch(refresh, m.sshFocusTab(c.Tab))
			}
		}
		s.focusAfter = v.conn
	case "take":
		var d protocol.SSHTakeWheelData
		if json.Unmarshal(v.raw, &d) == nil && d.Conn.ID != "" {
			s.setConnInfo(d.Conn)
			s.flash = d.Warning
			if d.Conn.Tab != "" && d.Conn.TabAlive {
				return tea.Batch(refresh, m.sshFocusTab(d.Conn.Tab))
			}
		}
	case "terminal":
		var c protocol.SSHConnInfo
		if json.Unmarshal(v.raw, &c) == nil && c.ID != "" {
			s.setConnInfo(c)
			s.focusAfter = ""
			if c.Tab != "" {
				return tea.Batch(refresh, m.sshFocusTab(c.Tab))
			}
		}
	case "handback":
		var d protocol.SSHHandbackData
		if json.Unmarshal(v.raw, &d) == nil && d.Conn.ID != "" {
			s.setConnInfo(d.Conn)
		}
		s.handback = nil
	case "stop":
		var d protocol.SSHStopData
		if json.Unmarshal(v.raw, &d) == nil {
			s.flash = d.Warning
		}
	case "close":
		var d protocol.SSHCloseData
		if json.Unmarshal(v.raw, &d) == nil {
			if d.Conn.ID != "" {
				s.setConnInfo(d.Conn)
			}
			s.flash = sshLine(strings.Join(d.Warnings, " "))
		}
	case "recall":
		var d protocol.SSHRecallData
		if json.Unmarshal(v.raw, &d) == nil && !d.Recalled {
			s.flash = "Too late: " + s.agentName() + " already has this."
		}
	}
	return refresh
}

func (s *sshModel) noteActionErr(v sshDoneMsg) {
	text := sshLine(v.err.Error())
	switch {
	case strings.Contains(strings.ToLower(text), "changed since") && (v.action == "proposal" || v.action == "decline" || v.action == "answer"):
		s.flash = s.agentName() + " changed this. Read it again."
		if v.action == "proposal" {
			s.flash = s.agentName() + " changed this proposal. Read it again."
		}
	case v.action == "approve" && strings.Contains(text, "not waiting for approval"):
		s.flash = "That command was already refused: " + text
	case v.action == "handback":
		// The form stays up: what the human wrote must survive a failed send.
		if s.handback != nil {
			s.handback.err = text
			s.handback.sending = false
		}
	default:
		s.flash = fmt.Sprintf("%s failed: %s", v.action, text)
	}
	s.flashOK = false
}
