package tui

// sshmodel.go is the state of the TUI's SSH workspace (`H`): the timeline the
// daemon recorded, the dock card waiting on the human, and the wire calls. It is
// a client like the rest of the TUI: every decision is the daemon's, and what is
// held here is a cache of cmd=sshTimeline. docs/ssh-sessions-ux.md section 17.

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/protocol"
)

// sshTickEvery is the overlay's refresh and countdown cadence. It runs only
// while the overlay is open: the cockpit's glance rides on cmd=sessions.
const sshTickEvery = time.Second

// sshArmDelay is how long a card's confirm is ignored after the card appears,
// so a keystroke already on its way cannot answer a question nobody read.
const sshArmDelay = 800 * time.Millisecond

// sshCmdPreviewLines is how much of an approval's command the band shows before
// the human must open the whole of it. Approving what you have not read is the
// accident this feature exists to prevent, so past this Y stays disabled.
const sshCmdPreviewLines = 8

// sshFilter is the stream's lens.
type sshFilter int

const (
	sshFilterAll      sshFilter = iota // everything
	sshFilterFacts                     // blocks and system events: the audit view
	sshFilterFindings                  // what the agent concluded, with its evidence
)

func (f sshFilter) label() string {
	switch f {
	case sshFilterFacts:
		return "Facts"
	case sshFilterFindings:
		return "Findings"
	}
	return "Everything"
}

// sshInputKind is what the one-line prompt is collecting.
type sshInputKind int

const (
	sshInputNone     sshInputKind = iota
	sshInputSay                   // talking to the agent, or !running a command
	sshInputDeny                  // a reason for denying a command
	sshInputDenyConn              // a reason for denying a connection
	sshInputDecline               // a reason for declining a proposal
	sshInputAnswer                // a free answer to a question element
	sshInputFind                  // find in this timeline
	sshInputSecret                // an answer to a shell's question: never drawn, never kept
)

// sshModel is the workspace overlay. Everything pinned at the moment a prompt
// opens (input*) stays pinned: the dock may have advanced by the time enter is
// pressed, and an answer must land on what the human was looking at.
type sshModel struct {
	runArmed string // the !command a production enter has armed
	open     bool
	session  string
	connID   string
	gen      int // bumped on open: replies for an earlier open are dropped
	// agentKind names the session's agent in copy. Read off the session list.
	agentKind string

	conns    []protocol.SSHConnInfo
	items    []protocol.SSHTimelineItem
	lastSeq  int64
	loaded   bool
	fetching bool
	err      string
	predates bool // the daemon answered unknown cmd
	down     bool // the daemon could not be reached

	filter     sshFilter
	selKey     string // the selected row, by key: rows are rebuilt on every change
	top        int    // first row drawn
	follow     bool   // keep the newest row in view
	newItems   int    // arrived while the reader was elsewhere
	expanded   map[string]bool
	back       []string       // rows to return to after following evidence
	evidenceAt map[string]int // which evidence target "e" goes to next, per row

	input      sshInputKind
	inputBuf   string
	inputBlock string
	inputConn  string
	inputEl    string
	inputRev   int
	drafts     map[string]string
	findQuery  string
	findHits   []string
	findAt     int

	flash   string
	flashOK bool

	// The dock's arming state: which card it is timing, and since when. Grant
	// choice belongs to one card and is off again on the next.
	cardKey  string
	cardAt   time.Time
	grantIdx int
	// cmdRead holds the long commands the human opened, by block + exact text.
	cmdRead map[string]bool

	runCheck     *sshRunCheck // proposal run check, in the band
	confirmClose string       // connection awaiting the y/n of closing it

	pager    *sshPager
	handback *sshHandback
	picker   *sshPicker

	// pendingOpen is a direct session that was just started and is waiting to
	// appear in the session list so it can be selected.
	pendingOpen *protocol.SSHOpenData
	// focusAfter is the connection whose terminal to focus once its tab is known.
	focusAfter string
}

// sshRunCheck is a proposal the human asked to run, pinned to the revision drawn.
type sshRunCheck struct {
	element  string
	revision int
}

// Messages.
type (
	sshTickMsg     struct{ gen int }
	sshTimelineMsg struct {
		gen     int
		session string
		data    *protocol.SSHTimelineData
		err     error
	}
	// sshDoneMsg is the reply to any other ssh command: action says which, and the
	// remaining fields are what the caller pinned when it sent the request.
	sshDoneMsg struct {
		action string
		gen    int
		conn   string
		block  string
		raw    json.RawMessage
		err    error
	}
)

// sshCall is one human-side round trip.
func sshCall(cmd string, args any) (json.RawMessage, error) {
	var raw json.RawMessage
	if args != nil {
		b, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	resp, err := requestFn(protocol.Request{Cmd: cmd, Args: raw})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, errors.New(resp.Error)
	}
	return resp.Data, nil
}

// sshAction sends cmd and returns the reply as an sshDoneMsg.
func (s *sshModel) sshAction(action, cmd string, args any, conn, block string) tea.Cmd {
	gen := s.gen
	return func() tea.Msg {
		raw, err := sshCall(cmd, args)
		return sshDoneMsg{action: action, gen: gen, conn: conn, block: block, raw: raw, err: err}
	}
}

func (s *sshModel) fetchTimeline() tea.Cmd {
	if s.fetching || s.session == "" {
		return nil
	}
	s.fetching = true
	gen, session, after := s.gen, s.session, s.lastSeq
	return func() tea.Msg {
		raw, err := sshCall(protocol.CmdSSHTimeline, protocol.SSHTimelineArgs{Session: session, After: after})
		if err != nil {
			return sshTimelineMsg{gen: gen, session: session, err: err}
		}
		var d protocol.SSHTimelineData
		if err := json.Unmarshal(raw, &d); err != nil {
			return sshTimelineMsg{gen: gen, session: session, err: err}
		}
		return sshTimelineMsg{gen: gen, session: session, data: &d}
	}
}

func sshTickCmd(gen int) tea.Cmd {
	return tea.Tick(sshTickEvery, func(time.Time) tea.Msg { return sshTickMsg{gen: gen} })
}

// openSSH is the `H` key: the workspace when the session has a connection, the
// host picker for a new direct session otherwise.
func (m *rootModel) openSSH() (tea.Model, tea.Cmd) {
	sel := m.sessions.selected()
	if sel == nil || sel.SSH == nil || len(sel.SSH.Connections) == 0 {
		cmd := m.openSSHPicker("")
		if sel != nil && sel.Location != "ssh" {
			m.ssh.picker.session = sel.ID
		}
		return m, cmd
	}
	return m, m.openSSHWorkspace(sel.ID)
}

// openSSHWorkspace opens the overlay on a session, and starts the refresh.
func (m *rootModel) openSSHWorkspace(id string) tea.Cmd {
	s := &m.ssh
	if s.session != id {
		*s = sshModel{session: id, drafts: s.drafts, gen: s.gen}
	}
	if s.drafts == nil {
		s.drafts = map[string]string{}
	}
	if s.expanded == nil {
		s.expanded, s.cmdRead, s.evidenceAt = map[string]bool{}, map[string]bool{}, map[string]int{}
		s.follow = true
	}
	s.open, s.input, s.inputBuf, s.flash = true, sshInputNone, "", ""
	if si := m.sessions.byID(id); si != nil {
		s.agentKind = si.Agent
	}
	s.fetching = false // a reply from before this open is dropped by gen
	s.gen++
	// A session's connections arrive with its glance, so the header is right on
	// the first frame rather than one fetch later.
	if si := m.sessions.byID(id); si != nil && si.SSH != nil && len(s.conns) == 0 {
		s.conns = si.SSH.Connections
	}
	s.syncConn()
	return tea.Batch(s.fetchTimeline(), sshTickCmd(s.gen))
}

// closeSSH closes the overlay. The session keeps every bit of cached state, so
// coming back is instant and a selected row stays selected.
func (m *rootModel) closeSSH() {
	s := &m.ssh
	s.open, s.input, s.inputBuf, s.confirmClose, s.runCheck = false, sshInputNone, "", "", nil
	s.pager, s.handback = nil, nil
}

// handleSSHTick refreshes the open overlay, and is what keeps its countdowns
// moving. A running pager polls here as well.
func (m *rootModel) handleSSHTick(v sshTickMsg) tea.Cmd {
	s := &m.ssh
	if !s.open || v.gen != s.gen {
		return nil
	}
	cmds := []tea.Cmd{sshTickCmd(s.gen), s.fetchTimeline()}
	if c := s.pagerPoll(); c != nil {
		cmds = append(cmds, c)
	}
	return tea.Batch(cmds...)
}

// handleSSHTimeline folds a reply into the cache: upsert by ID, sort by Order.
func (m *rootModel) handleSSHTimeline(v sshTimelineMsg) tea.Cmd {
	s := &m.ssh
	if v.gen != s.gen || v.session != s.session {
		return nil
	}
	s.fetching = false
	if v.err != nil {
		s.noteErr(v.err)
		return nil
	}
	s.err, s.predates, s.down = "", false, false
	s.loaded = true
	if si := m.sessions.byID(s.session); si != nil {
		s.agentKind = si.Agent
	}
	d := v.data
	s.conns = d.Connections
	s.lastSeq = max(s.lastSeq, d.LastSeq)
	added := s.upsert(d.Items)
	s.syncConn()
	rows := s.rows()
	switch {
	case s.follow && len(rows) > 0:
		s.selKey = rows[len(rows)-1].key
	case !s.follow:
		s.newItems += added
	}
	s.syncRunCheck()
	s.syncCard()
	focus := s.syncFocusAfter(m)
	if d.More {
		return tea.Batch(focus, s.fetchTimeline())
	}
	return focus
}

// noteErr records why the timeline could not be read. The two facts a human can
// do something about get their own state; a failed fetch never clears what is
// already on screen, because lola keeps recording either way.
func (s *sshModel) noteErr(err error) {
	s.err = err.Error()
	s.down = errors.Is(err, errDaemonDown)
	s.predates = strings.Contains(s.err, "unknown cmd")
}

func (s *sshModel) upsert(in []protocol.SSHTimelineItem) int {
	added := 0
	// A tab trashed elsewhere: a delta only says what changed, so what it owned goes here.
	s.items = slices.DeleteFunc(s.items, func(it protocol.SSHTimelineItem) bool { return s.trashed(it) })
	for _, it := range in {
		if sshHiddenInTUI(it) {
			continue
		}
		if i := slices.IndexFunc(s.items, func(o protocol.SSHTimelineItem) bool { return o.ID == it.ID }); i >= 0 {
			s.items[i] = it
			continue
		}
		s.items = append(s.items, it)
		added++
	}
	slices.SortStableFunc(s.items, func(a, b protocol.SSHTimelineItem) int { return int(a.Order - b.Order) })
	return added
}

// sshHiddenInTUI drops the conversation's system events. The pager prints every system
// event it holds, and it is a view of the connection: a receipt or a PR row on every turn
// would bury the commands it exists to show. The app's conversation view reads them.
func sshHiddenInTUI(it protocol.SSHTimelineItem) bool {
	if it.System == nil {
		return false
	}
	switch it.System.Kind {
	case protocol.SSHEventSessionStarted, protocol.SSHEventSessionEnded, protocol.SSHEventAgentExited,
		protocol.SSHEventStopped, protocol.SSHEventCheckpoint, protocol.SSHEventPR, protocol.SSHEventCI,
		protocol.SSHEventReview, protocol.SSHEventHandoff:
		return true
	}
	return false
}

func (s *sshModel) trashed(it protocol.SSHTimelineItem) bool {
	conn := ""
	switch {
	case it.Block != nil:
		conn = it.Block.Conn
	case it.Element != nil:
		conn = it.Element.Conn
	case it.System != nil:
		conn = it.System.Conn
	case it.Human != nil:
		conn = it.Human.Conn
	}
	return conn != "" && !slices.ContainsFunc(s.conns, func(c protocol.SSHConnInfo) bool { return c.ID == conn })
}

// syncConn keeps the selected connection valid, preferring what needs the human.
// Most urgent first: authentication (a clock is running), approval, go.
func (s *sshModel) syncConn() {
	if s.conn() != nil {
		return
	}
	s.connID = ""
	rank := func(c protocol.SSHConnInfo) int {
		switch {
		case c.State == protocol.SSHConnAuthenticating:
			return 0
		case c.PendingApprovals > 0:
			return 1
		case c.State == protocol.SSHConnRequested:
			return 2
		case c.State == protocol.SSHConnClosed || c.State == protocol.SSHConnDenied:
			return 4
		}
		return 3
	}
	best := -1
	for i, c := range s.conns {
		if best < 0 || rank(c) < rank(s.conns[best]) {
			best = i
		}
	}
	if best >= 0 {
		s.connID = s.conns[best].ID
	}
}

// conn is the selected connection, nil when there is none.
func (s *sshModel) conn() *protocol.SSHConnInfo { return s.connByID(s.connID) }

// switchable is what the host chips and < > offer: the connections still in play,
// plus the one on screen. An ended connection stays in the stream, not in the
// switcher, or a session that was denied twice reads as three hosts.
func (s *sshModel) switchable() []protocol.SSHConnInfo {
	out := make([]protocol.SSHConnInfo, 0, len(s.conns))
	for _, c := range s.conns {
		if c.ID == s.connID || (c.State != protocol.SSHConnClosed && c.State != protocol.SSHConnDenied) {
			out = append(out, c)
		}
	}
	return out
}

func (s *sshModel) connByID(id string) *protocol.SSHConnInfo {
	for i := range s.conns {
		if s.conns[i].ID == id {
			return &s.conns[i]
		}
	}
	return nil
}

func (s *sshModel) setConnInfo(c protocol.SSHConnInfo) {
	if p := s.connByID(c.ID); p != nil {
		*p = c
		return
	}
	s.conns = append(s.conns, c)
}

func sshConnLive(c *protocol.SSHConnInfo) bool {
	return c != nil && c.State != protocol.SSHConnClosed && c.State != protocol.SSHConnDenied
}

func sshHostLabel(c *protocol.SSHConnInfo) string {
	if c.Label != "" {
		return sshLine(c.Label)
	}
	return sshLine(c.Host)
}

// sshIdentity is who and where the alias means: what ssh -G said, else what
// config says. It is a daemon fact.
func sshIdentity(c *protocol.SSHConnInfo) string {
	switch {
	case c.ResolvedUser != "" && c.ResolvedHostname != "":
		return sshLine(c.ResolvedUser + "@" + c.ResolvedHostname)
	case c.User != "":
		return sshLine(c.User + "@" + c.Alias)
	}
	return sshLine(c.Alias)
}

// blockByID finds a block in the timeline, nil when it is not (or no longer) there.
func (s *sshModel) blockByID(id string) *protocol.SSHBlock {
	for i := range s.items {
		if b := s.items[i].Block; b != nil && b.ID == id {
			return b
		}
	}
	return nil
}

func (s *sshModel) elementByID(id string) *protocol.SSHElement {
	for i := range s.items {
		if e := s.items[i].Element; e != nil && e.ID == id {
			return e
		}
	}
	return nil
}

// --- rows -------------------------------------------------------------------

// Rail states: what the connection was doing when a row happened. A fact drawn
// as a line.
const (
	sshRailSolid = iota
	sshRailDriving
	sshRailDashed
)

// sshRow is one selectable stream row: a timeline item or a fold of them.
type sshRow struct {
	key  string
	item *protocol.SSHTimelineItem
	fold []*protocol.SSHTimelineItem
	rail int
}

// sshFoldMin is the shortest run of clean commands worth folding.
const sshFoldMin = 3

// evidenceTargets are the blocks an element's evidence points at, so a fold can
// never hide what a jump must land on.
func (s *sshModel) evidenceBlocks() map[string]bool {
	out := map[string]bool{}
	for i := range s.items {
		e := s.items[i].Element
		if e == nil {
			continue
		}
		for _, t := range s.elementTargets(e) {
			out[t] = true
		}
	}
	return out
}

// elementTargets is every block this element points at that the stream can jump
// to: its evidence, then any block id it names in passing.
func (s *sshModel) elementTargets(e *protocol.SSHElement) []string {
	var out []string
	add := func(id string) {
		if !slices.Contains(out, id) && s.blockByID(id) != nil {
			out = append(out, id)
		}
	}
	for _, ev := range e.Evidence {
		add(ev.Block)
	}
	for _, id := range sshBareBlockRe.FindAllString(e.Title+" "+e.Body, -1) {
		add(id)
	}
	return out
}

func sshFoldable(it *protocol.SSHTimelineItem, linked map[string]bool) bool {
	b := it.Block
	return b != nil && b.Status == protocol.SSHBlockSucceeded && b.Actor != protocol.SSHActorHuman && !linked[b.ID]
}

// rows builds the stream under the current filter. Three or more consecutive
// clean commands fold into one row; a failure, a human's run or anything evidence
// points at never does, and the Facts filter folds nothing since it is the audit.
func (s *sshModel) rows() []sshRow {
	rails := s.railStates()
	linked := s.evidenceBlocks()
	var out []sshRow
	var run []*protocol.SSHTimelineItem
	flush := func() {
		if len(run) == 0 {
			return
		}
		key := "fold:" + run[0].ID
		if len(run) >= sshFoldMin && s.filter != sshFilterFacts && !s.expanded[key] {
			out = append(out, sshRow{key: key, fold: run, rail: rails[run[0].ID]})
		} else {
			for _, it := range run {
				out = append(out, sshRow{key: it.ID, item: it, rail: rails[it.ID]})
			}
		}
		run = nil
	}
	for i := range s.items {
		it := &s.items[i]
		if !s.filterKeeps(it) {
			continue
		}
		if s.filter != sshFilterFacts && sshFoldable(it, linked) {
			run = append(run, it)
			continue
		}
		flush()
		out = append(out, sshRow{key: it.ID, item: it, rail: rails[it.ID]})
	}
	flush()
	return out
}

func (s *sshModel) filterKeeps(it *protocol.SSHTimelineItem) bool {
	switch s.filter {
	case sshFilterFacts:
		return it.Kind == protocol.SSHItemBlock || it.Kind == protocol.SSHItemSystem
	case sshFilterFindings:
		if it.Element == nil {
			return false
		}
		switch it.Element.Kind {
		case "finding", "hypothesis", "evidence", "recommendation", "proposal":
			return true
		}
		return false
	}
	return true
}

// railStates walks the record, reading the rail's state off the daemon's own
// system events for the selected connection.
func (s *sshModel) railStates() map[string]int {
	c := s.conn()
	wheelEvents, firstWheel := 0, ""
	for i := range s.items {
		if ev := s.items[i].System; ev != nil && (c == nil || ev.Conn == "" || ev.Conn == c.ID) &&
			(ev.Kind == protocol.SSHEventWheelTaken || ev.Kind == protocol.SSHEventHandedBack) {
			if wheelEvents == 0 {
				firstWheel = ev.Kind
			}
			wheelEvents++
		}
	}
	// A session started with the human driving has no wheel_taken: it reads that
	// way until its first handback.
	driving := firstWheel == protocol.SSHEventHandedBack || (wheelEvents == 0 && c != nil && c.Wheel == protocol.SSHWheelHuman)
	live := false
	out := make(map[string]int, len(s.items))
	for i := range s.items {
		it := &s.items[i]
		if ev := it.System; ev != nil && (c == nil || ev.Conn == "" || ev.Conn == c.ID) {
			switch ev.Kind {
			case protocol.SSHEventConnected:
				live = true
			case protocol.SSHEventDisconnected, protocol.SSHEventFailed, protocol.SSHEventClosed, protocol.SSHEventDenied:
				live = false
			case protocol.SSHEventWheelTaken:
				driving = true
			case protocol.SSHEventHandedBack:
				driving = false
			}
		}
		switch {
		case driving:
			out[it.ID] = sshRailDriving
		case live:
			out[it.ID] = sshRailSolid
		default:
			out[it.ID] = sshRailDashed
		}
	}
	return out
}

func sshRowIndex(rows []sshRow, key string) int {
	return slices.IndexFunc(rows, func(r sshRow) bool {
		if r.key == key {
			return true
		}
		// A row folded away since it was selected: the fold now stands for it.
		return slices.ContainsFunc(r.fold, func(it *protocol.SSHTimelineItem) bool { return it.ID == key })
	})
}

// cursor is the selected row's index, the last row when the selection is gone.
func (s *sshModel) cursor(rows []sshRow) int {
	if i := sshRowIndex(rows, s.selKey); i >= 0 {
		return i
	}
	return max(0, len(rows)-1)
}

func (s *sshModel) selectedRow() *sshRow {
	rows := s.rows()
	if len(rows) == 0 {
		return nil
	}
	return &rows[s.cursor(rows)]
}

func (s *sshModel) selectedBlock() *protocol.SSHBlock {
	if r := s.selectedRow(); r != nil && r.item != nil {
		return r.item.Block
	}
	return nil
}

func (s *sshModel) selectedElement() *protocol.SSHElement {
	if r := s.selectedRow(); r != nil && r.item != nil {
		return r.item.Element
	}
	return nil
}

// move steps the selection. Reaching the last row resumes following.
func (s *sshModel) move(n int) {
	rows := s.rows()
	if len(rows) == 0 {
		return
	}
	i := min(max(0, s.cursor(rows)+n), len(rows)-1)
	s.selKey = rows[i].key
	s.follow = i == len(rows)-1
	if s.follow {
		s.newItems = 0
	}
}

func (s *sshModel) moveEdge(top bool) {
	rows := s.rows()
	if len(rows) == 0 {
		return
	}
	if top {
		s.selKey, s.follow = rows[0].key, len(rows) == 1
		return
	}
	s.selKey, s.follow, s.newItems = rows[len(rows)-1].key, true, 0
}

// --- the dock ---------------------------------------------------------------

// Dock card kinds.
const (
	sshCardAuth        = "auth"
	sshCardPrompt      = "prompt"
	sshCardApproval    = "approval"
	sshCardGo          = "go"
	sshCardReconnect   = "reconnect"
	sshCardFailed      = "failed"
	sshCardProposal    = "proposal"
	sshCardShellClosed = "shell_closed"
)

// sshCard is what the band is asking. Every field is drawn from the block and the
// connection; nothing is a summary of them.
type sshCard struct {
	kind         string
	key          string
	conn         *protocol.SSHConnInfo
	block        *protocol.SSHBlock
	element      *protocol.SSHElement
	index, total int
}

// card is the one card the dock shows: the run check the human asked for, then
// authentication, the oldest approval, a go, and last what the selected
// connection itself needs.
func (s *sshModel) card() *sshCard {
	if rc := s.runCheck; rc != nil {
		if e := s.elementByID(rc.element); e != nil {
			return &sshCard{kind: sshCardProposal, key: fmt.Sprintf("proposal:%s:%d:%s", e.ID, e.Revision, e.Command),
				conn: s.proposalConn(e), element: e}
		}
	}
	// A question comes first: something on the other end is holding for it.
	for i := range s.conns {
		if c := &s.conns[i]; c.Prompt != nil {
			return &sshCard{kind: sshCardPrompt, key: "prompt:" + c.Prompt.ID, conn: c}
		}
	}
	for i := range s.conns {
		if c := &s.conns[i]; c.State == protocol.SSHConnAuthenticating {
			return &sshCard{kind: sshCardAuth, key: "auth:" + c.ID, conn: c}
		}
	}
	var pending []*protocol.SSHBlock
	for i := range s.items {
		if b := s.items[i].Block; b != nil && b.Status == protocol.SSHBlockPendingApproval {
			pending = append(pending, b)
		}
	}
	slices.SortStableFunc(pending, func(a, b *protocol.SSHBlock) int { return a.CreatedAt.Compare(b.CreatedAt) })
	var requested []*protocol.SSHConnInfo
	for i := range s.conns {
		if s.conns[i].State == protocol.SSHConnRequested {
			requested = append(requested, &s.conns[i])
		}
	}
	total := len(pending) + len(requested)
	if len(pending) > 0 {
		b := pending[0]
		return &sshCard{kind: sshCardApproval, key: "approval:" + b.ID + ":" + b.Command, conn: s.connByID(b.Conn), block: b, index: 1, total: total}
	}
	if len(requested) > 0 {
		c := requested[0]
		return &sshCard{kind: sshCardGo, key: "go:" + c.ID, conn: c, index: 1, total: total}
	}
	if c := s.conn(); c != nil {
		switch {
		case c.State == protocol.SSHConnDisconnected:
			return &sshCard{kind: sshCardReconnect, key: "reconnect:" + c.ID, conn: c}
		case c.State == protocol.SSHConnFailed:
			return &sshCard{kind: sshCardFailed, key: "failed:" + c.ID, conn: c}
		case c.State == protocol.SSHConnConnected && !c.TabAlive:
			return &sshCard{kind: sshCardShellClosed, key: "shell:" + c.ID, conn: c}
		}
	}
	return nil
}

func (s *sshModel) proposalConn(e *protocol.SSHElement) *protocol.SSHConnInfo {
	if c := s.connByID(e.Conn); c != nil {
		return c
	}
	return s.conn()
}

// syncCard restarts the arming clock whenever the card, or the exact command it
// shows, changes. The grant choice is off again on every new card.
func (s *sshModel) syncCard() {
	key := ""
	if c := s.card(); c != nil {
		key = c.key
	}
	if key != s.cardKey {
		s.cardKey, s.cardAt, s.grantIdx = key, sshNow(), 0
	}
}

func (s *sshModel) armed() bool { return sshNow().Sub(s.cardAt) >= sshArmDelay }

// syncRunCheck closes the run check when the agent rewrites the proposal under
// it. The revision pin in sshProposal would refuse the run anyway; the human
// should be told before they press anything.
func (s *sshModel) syncRunCheck() {
	rc := s.runCheck
	if rc == nil {
		return
	}
	e := s.elementByID(rc.element)
	if e == nil || e.Revision != rc.revision || e.Outcome != nil {
		s.runCheck = nil
		s.flash, s.flashOK = s.agentName()+" changed this proposal. Read it again.", false
	}
}

// agentName is this session's agent, read off the record the list carries.
func (s *sshModel) agentName() string { return sshAgentName(s.agentKind) }

// sshCmdKey identifies a command the human opened: block and exact text, so a
// read never carries over to anything else.
func sshCmdKey(b *protocol.SSHBlock) string { return b.ID + "\x00" + b.Command }

// cmdNeedsReading reports whether a command is too long for the band and has not
// been opened whole yet.
func (s *sshModel) cmdNeedsReading(b *protocol.SSHBlock, w int) bool {
	return len(sshWrapHard(b.Command, max(1, w))) > sshCmdPreviewLines && !s.cmdRead[sshCmdKey(b)]
}

// grantChoices are the durations a card can offer: the app's list, up to what the
// host allows. Production never gets here: it has none, whatever a daemon says.
func sshGrantChoices(c *protocol.SSHConnInfo) []int {
	if c == nil || sshTierNorm(c.Tier) == protocol.SSHTierProduction || c.GrantMaxMinutes <= 0 {
		return nil
	}
	var out []int
	for _, n := range []int{5, 15, 30, 60, 120} {
		if n <= c.GrantMaxMinutes {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		out = []int{c.GrantMaxMinutes}
	}
	return out
}

// grantMinutes is the grant the card is set to, 0 for none.
func (s *sshModel) grantMinutes(c *protocol.SSHConnInfo) int {
	ch := sshGrantChoices(c)
	if s.grantIdx <= 0 || s.grantIdx > len(ch) {
		return 0
	}
	return ch[s.grantIdx-1]
}
