package tui

// sshview.go draws the workspace overlay: tier band, header, the agent's account,
// the timeline, the dock card and the line that tells the truth about the agent.
//
// Every row speaks in one of three voices (see sshstyles.go), and the rules that
// keep them honest are structural, so they live here and are pinned by tests:
//
//   - Only Lola's rows are drawn on a band or with a filled marker. A block's
//     "why" always hangs OUTSIDE its band, as a claim.
//   - No agent text ever gets a ✓ or the word "verified": a block being real does
//     not make the claim about it true.
//   - A non-zero exit reads "exit 1", never "failed": grep exits 1 on no match.

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/state"
)

func (m *rootModel) sshWidth() int {
	if m.width > 0 {
		return max(20, m.width)
	}
	return 100
}

func (m *rootModel) sshHeight() int {
	if m.height > 0 {
		return max(8, m.height)
	}
	return 24
}

// sshView is the whole frame.
func (m *rootModel) sshView() string {
	s := &m.ssh
	var lines []string
	switch {
	case s.picker != nil:
		lines = m.sshPickerView()
	case s.handback != nil:
		lines = m.sshHandbackView()
	case s.pager != nil:
		lines = m.sshPagerView()
	default:
		lines = m.sshWorkspaceLines()
	}
	return strings.Join(lines, "\n")
}

func (m *rootModel) sshWorkspaceLines() []string {
	w, H := m.sshWidth(), m.sshHeight()

	top := m.sshHeaderLines(w)
	dock := m.sshDockLines(w)
	bottom := m.sshBottomLines(w)
	brief := m.sshBriefLines(w)

	// The dock takes what it needs and the stream gets the rest. Under pressure
	// the agent's account shrinks to the one fact row before a card ever does.
	used := func() int { return len(top) + 1 + len(brief) + len(dock) + len(bottom) }
	if used() > H-3 && len(brief) > 1 {
		brief = brief[len(brief)-1:]
	}
	streamH := max(1, H-used())

	out := append([]string{}, top...)
	out = append(out, brief...)
	out = append(out, previewLine(m.sshFilterRow(w), w))
	out = append(out, m.sshStreamLines(w, streamH)...)
	out = append(out, dock...)
	out = append(out, bottom...)
	if len(out) > H { // never overdraw: the answer keys live at the bottom
		out = out[len(out)-H:]
	}
	return out
}

// sshHeaderLines reads left to right as a sentence: where, who as, what state,
// who has the wheel. The band under it carries the tier.
func (m *rootModel) sshHeaderLines(w int) []string {
	s := &m.ssh
	c := s.conn()
	if c == nil {
		return []string{previewLine(sshAccentBold.Render("SSH")+faintText.Render("  no connection"), w), sshTierRuleFaint(w)}
	}
	now := sshNow()
	parts := []string{sshTierTag(c.Tier), sshInk.Bold(true).Render(sshHostLabel(c)), faintText.Render(sshIdentity(c))}
	if !c.Sandboxed {
		parts = append(parts, warnText.Render("Unsandboxed"))
	}
	parts = append(parts, sshStateWord(c, now))
	if c.Grant != nil {
		parts = append(parts, warnText.Render("grant "+sshClock(c.Grant.ExpiresAt.Sub(now)))+faintText.Render(" V revoke"))
	}
	if !sshConnOver(c) {
		parts = append(parts, sshWheelWord(c, now))
	}
	left := strings.Join(parts, "  ")
	right := faintText.Render("✕ esc")
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	line := left
	if gap >= 2 {
		line += strings.Repeat(" ", gap) + right
	}
	out := []string{previewLine(line, w), sshTierRule(c.Tier, w)}
	if len(s.switchable()) > 1 {
		out = append(out, previewLine(s.sshHostChips(), w))
	}
	return out
}

// sshHostChips lets a multi-host session say which connection the header, the
// wheel and the terminal apply to. The stream shows all of them.
func (s *sshModel) sshHostChips() string {
	shown := s.switchable()
	chips := make([]string, 0, len(shown))
	for i := range shown {
		c := &shown[i]
		text := sshTierShort(c.Tier) + " " + sshHostLabel(c)
		if c.PendingApprovals > 0 {
			text += fmt.Sprintf(" %d↑", c.PendingApprovals)
		}
		if c.ID == s.connID {
			chips = append(chips, boxTitleHi.Render("["+text+"]"))
		} else {
			chips = append(chips, faintText.Render(" "+text+" "))
		}
	}
	return strings.Join(chips, " ") + faintText.Render("   < > switch")
}

// sshStateWord is section 12's header column.
func sshStateWord(c *protocol.SSHConnInfo, now time.Time) string {
	switch c.State {
	case protocol.SSHConnRequested:
		return statusOrange.Render("○ Waiting for your go")
	case protocol.SSHConnDenied:
		return faintText.Render("○ Denied")
	case protocol.SSHConnConnecting:
		return sshInfo.Render("● Connecting…")
	case protocol.SSHConnAuthenticating:
		word := statusOrange.Render("● Signing in")
		if !c.ConnectDeadline.IsZero() {
			word += faintText.Render("  Gives up in " + sshClock(c.ConnectDeadline.Sub(now)))
		}
		return word
	case protocol.SSHConnConnected:
		word := goodText.Render("●") + " Connected"
		if !c.ConnectedAt.IsZero() {
			word += " " + shortDuration(now.Sub(c.ConnectedAt))
		}
		if !c.TabAlive {
			word += faintText.Render("  Shell closed")
		}
		return word
	case protocol.SSHConnDisconnected:
		if sshIdleClosed(c) {
			return faintText.Render("○ Idle, connection closed")
		}
		return warnText.Render("○ Disconnected")
	case protocol.SSHConnFailed:
		return badText.Render("○ Could not connect")
	case protocol.SSHConnClosed:
		return faintText.Render("○ Closed")
	}
	return faintText.Render("○ " + sshLine(c.State))
}

// sshConnOver is a connection with nothing left to steer: no wheel, no terminal.
func sshConnOver(c *protocol.SSHConnInfo) bool {
	return c != nil && (c.State == protocol.SSHConnClosed || c.State == protocol.SSHConnDenied)
}

func sshIdleClosed(c *protocol.SSHConnInfo) bool {
	return strings.Contains(strings.ToLower(c.StateDetail), "idle")
}

func sshWheelWord(c *protocol.SSHConnInfo, now time.Time) string {
	if c.Wheel == protocol.SSHWheelHuman {
		word := sshWheelYou.Render(" wheel YOU ")
		if !c.WheelSince.IsZero() {
			word += " " + sshAccent.Render(sshClock(now.Sub(c.WheelSince)))
		}
		return word
	}
	return faintText.Render("wheel ") + sshInfo.Bold(true).Render("AGENT")
}

// --- the brief ----------------------------------------------------------------

// sshBriefLines is the glance: the agent's standing account, labelled as its own,
// then one fact. Elements updated through --ref stay at their original position
// in the stream, so this is where their current state stays visible.
func (m *rootModel) sshBriefLines(w int) []string {
	s := &m.ssh
	agentName := s.agentName()
	var summary, plan *protocol.SSHElement
	opens, confirmed, refuted := 0, 0, 0
	sev := map[string]int{}
	for i := range s.items {
		e := s.items[i].Element
		if e == nil {
			continue
		}
		switch e.Kind {
		case "summary":
			if summary == nil || !e.UpdatedAt.Before(summary.UpdatedAt) {
				summary = e
			}
		case "plan":
			plan = e
		case "hypothesis":
			switch e.State {
			case "confirmed":
				confirmed++
			case "refuted":
				refuted++
			default:
				opens++
			}
		case "finding":
			sev[e.Severity]++
		}
	}
	var claims []string
	now := sshNow()
	if summary != nil {
		age := now.Sub(summary.UpdatedAt)
		text := sshLine(summary.Title)
		ageWord := faintText.Render("  " + shortDuration(age))
		switch {
		case summary.UpdatedAt.IsZero():
			ageWord = ""
		case age > boardStaleAfter:
			// A confident stale summary is worse than none.
			text = faintText.Render(text)
			ageWord = warnText.Render("  " + shortDuration(age))
		}
		// Clipped here, with an ellipsis: previewLine would just cut it mid-word.
		if room := w - 4 - lipgloss.Width(ageWord); room > 1 && lipgloss.Width(text) > room {
			text = truncateANSI(text, room-1) + "…"
		}
		claims = append(claims, previewLine("  "+faintText.Render("○ ")+text+ageWord, w))
	}
	if plan != nil {
		done := 0
		active := ""
		for _, st := range plan.Steps {
			switch st.State {
			case "done", "skipped":
				done++
			case "active":
				if active == "" {
					active = st.Text
				}
			}
		}
		line := faintText.Render(fmt.Sprintf("○ plan %d/%d ", done, len(plan.Steps)))
		if n := len(plan.Steps); n > 0 {
			line += faintText.Render(boardMeter(done*100/n, min(n, 10)) + " ")
		}
		claims = append(claims, previewLine("  "+line+sshLine(active), w))
	}
	var tally []string
	if opens+confirmed+refuted > 0 {
		var ws []string
		if confirmed > 0 {
			ws = append(ws, fmt.Sprintf("%d confirmed", confirmed))
		}
		if opens > 0 {
			ws = append(ws, fmt.Sprintf("%d open", opens))
		}
		if refuted > 0 {
			ws = append(ws, fmt.Sprintf("%d refuted", refuted))
		}
		tally = append(tally, "theories "+strings.Join(ws, " · "))
	}
	var fs []string
	for _, name := range []string{"critical", "high", "medium", "low", "info"} {
		if n := sev[name]; n > 0 {
			fs = append(fs, fmt.Sprintf("%d %s", n, name))
		}
	}
	if len(fs) > 0 {
		tally = append(tally, "findings "+strings.Join(fs, " "))
	}
	if len(tally) > 0 {
		claims = append(claims, previewLine("  "+faintText.Render("○ "+strings.Join(tally, "     ")), w))
	}

	var out []string
	if len(claims) > 0 {
		out = append(out, previewLine(faintText.Render(agentName+"'s account  (claims)"), w))
		out = append(out, claims...)
	}
	return append(out, m.sshNowRow(w), sshTierRuleFaint(w))
}

// sshNowRow is the only fact in the brief: Lola's voice, on a band.
func (m *rootModel) sshNowRow(w int) string {
	s := &m.ssh
	agentName := s.agentName()
	now := sshNow()
	var newest *protocol.SSHBlock
	running, queued, pending := 0, 0, 0
	for i := range s.items {
		b := s.items[i].Block
		if b == nil {
			continue
		}
		switch b.Status {
		case protocol.SSHBlockRunning:
			running++
			newest = b
		case protocol.SSHBlockQueued:
			queued++
		case protocol.SSHBlockPendingApproval:
			pending++
		}
	}
	var text string
	si := m.sessions.byID(s.session)
	switch {
	case pending > 0:
		plural := "s"
		if pending == 1 {
			plural = ""
		}
		text = statusOrange.Render(fmt.Sprintf("Waiting for you to approve %d command%s", pending, plural))
	case running > 1 || (running == 1 && queued > 0):
		text = sshInfo.Render(fmt.Sprintf("Running %d, %d queued", running, queued))
	case newest != nil:
		text = sshInfo.Render("Running") + "  " + sshLine(newest.Command) + faintText.Render(" · "+sshDur(now.Sub(newest.StartedAt))+" · X stop")
	case si == nil:
		text = faintText.Render(agentName + " is starting")
	default:
		a, _ := axesOf(*si)
		switch {
		case a == state.AgentWaitingInput && si.InputReason != string(state.InputIdleNotify):
			text = statusOrange.Render(agentName+" is waiting on a prompt in its own terminal.") + faintText.Render(" A show")
		case displayOf(*si) == state.DisplayGone:
			text = warnText.Render(agentName+" has exited") + faintText.Render(" esc, then R revive")
		case a == state.AgentIdle || a == state.AgentWaitingInput:
			text = faintText.Render(agentName + " is waiting")
		default:
			text = sshInfo.Render(agentName + " is working")
		}
	}
	return highlightRow(" ▪ "+text, w, bgSGR(colSel))
}

func (m *rootModel) sshFilterRow(w int) string {
	s := &m.ssh
	var cells []string
	for _, f := range []sshFilter{sshFilterAll, sshFilterFacts, sshFilterFindings} {
		if f == s.filter {
			cells = append(cells, boxTitleHi.Render("["+f.label()+"]"))
		} else {
			cells = append(cells, faintText.Render(" "+f.label()+" "))
		}
	}
	left := strings.Join(cells, " ")
	legend := faintText.Render("▪ Lola recorded  ○ agent says  ") + sshAccent.Render("▌") + faintText.Render(" you")
	if gap := w - lipgloss.Width(left) - lipgloss.Width(legend); gap >= 2 {
		return left + strings.Repeat(" ", gap) + legend
	}
	return left
}

// --- the stream ---------------------------------------------------------------

func (m *rootModel) sshStreamLines(w, h int) []string {
	s := &m.ssh
	rows := s.rows()
	var out []string
	if len(rows) == 0 {
		out = []string{"", previewLine("  "+m.sshEmptyStream(), w)}
	} else {
		cur := s.cursor(rows)
		cache := map[int][]string{}
		render := func(i int) []string {
			if l, ok := cache[i]; ok {
				return l
			}
			l := m.sshRenderRow(&rows[i], i == cur, w)
			if i > 0 && sshRowBreak(&rows[i-1], &rows[i]) {
				l = append([]string{" " + sshRailGlyph(rows[i].rail)}, l...)
			}
			cache[i] = l
			return l
		}
		s.top = min(s.top, len(rows)-1)
		if cur < s.top {
			s.top = cur
		}
		used := 0
		for i := s.top; i <= cur; i++ {
			used += len(render(i))
		}
		for used > h && s.top < cur {
			used -= len(render(s.top))
			s.top++
		}
		for s.top > 0 && used+len(render(s.top-1)) <= h && (s.follow || cur == len(rows)-1) {
			s.top--
			used += len(render(s.top))
		}
		for i := s.top; i < len(rows) && len(out) < h; i++ {
			out = append(out, render(i)...)
		}
		if len(out) > h {
			// The selected row is what must stay whole; a row below it clips.
			out = out[:h]
		}
	}
	for len(out) < h {
		out = append(out, "")
	}
	if s.newItems > 0 && !s.follow && h > 1 {
		out[h-1] = previewLine(faintText.Render(fmt.Sprintf("   %d new · G jump to latest", s.newItems)), w)
	}
	if len(s.back) > 0 && h > 2 {
		out[h-2] = previewLine(faintText.Render("   ← b back to where you were"), w)
	}
	return out
}

func (m *rootModel) sshEmptyStream() string {
	s := &m.ssh
	agentName := s.agentName()
	switch {
	case s.predates:
		return warnText.Render("This daemon predates SSH sessions. Restart it to use them.") + faintText.Render("  esc, then ^r")
	case s.down:
		return badText.Render("The daemon is not running.")
	case s.err != "":
		return badText.Render("Could not load this timeline. Lola keeps recording it.") + faintText.Render("  It retries by itself.")
	case !s.loaded:
		return faintText.Render("Loading…")
	case s.filter == sshFilterFacts:
		return faintText.Render("No commands yet.")
	case s.filter == sshFilterFindings:
		return faintText.Render(agentName + " has not posted any findings or theories yet.")
	}
	if c := s.conn(); c != nil && c.Wheel == protocol.SSHWheelHuman {
		return faintText.Render("You have the wheel. " + agentName + " is waiting until you hand it back or ask it something.")
	}
	// Only a connected timeline may say "Connected". Every other state has already said
	// its own sentence in the header and the dock, and an empty line is the honest rest.
	c := s.conn()
	if c == nil || c.State != protocol.SSHConnConnected {
		return ""
	}
	if si := m.sessions.byID(s.session); si != nil && (si.AgentState == string(state.AgentIdle) || si.AgentState == string(state.AgentWaitingInput)) {
		return faintText.Render("Connected. " + agentName + " is waiting for you to tell it what to look at.")
	}
	return faintText.Render("Connected. " + agentName + " has not run anything yet.")
}

// sshRowBreak is where the stream takes a breath: a line of bare rail whenever the
// voice changes. Consecutive claims stay together and read as one paragraph, and a
// system event is small enough to sit tight against what it happened beside.
func sshRowBreak(a, b *sshRow) bool {
	kind := func(r *sshRow) string {
		if r.fold != nil {
			return protocol.SSHItemBlock
		}
		return r.item.Kind
	}
	ka, kb := kind(a), kind(b)
	return ka != kb && ka != protocol.SSHItemSystem && kb != protocol.SSHItemSystem
}

func sshRailGlyph(kind int) string {
	switch kind {
	case sshRailDriving:
		return sshAccent.Render("┃")
	case sshRailDashed:
		return faintText.Render("┊")
	}
	return faintText.Render("│")
}

// sshRenderRow draws one row. Col 0 is the cursor, col 1 the rail or its marker.
func (m *rootModel) sshRenderRow(r *sshRow, selected bool, w int) []string {
	cursor := " "
	if selected {
		cursor = boxTitleHi.Render("›")
	}
	rail := sshRailGlyph(r.rail)
	lead := cursor + rail + " "
	if r.fold != nil {
		return []string{m.sshFoldRow(r, selected, w)}
	}
	it := r.item
	switch {
	case it.Block != nil:
		return m.sshBlockLines(it, cursor, rail, w)
	case it.Element != nil:
		return m.sshElementLines(it.Element, r.key, lead, rail, selected, w)
	case it.Human != nil:
		return m.sshHumanLines(it, lead, rail, w)
	case it.System != nil:
		return sshSystemLines(it, lead, cursor, w)
	}
	return []string{previewLine(lead+faintText.Render(sshLine(it.Kind)), w)}
}

func (m *rootModel) sshFoldRow(r *sshRow, selected bool, w int) string {
	var total int64
	for _, it := range r.fold {
		total += it.Block.DurationMs
	}
	cursor := " "
	if selected {
		cursor = boxTitleHi.Render("›")
	}
	text := fmt.Sprintf("%d commands · all exit 0 · %s total  ▸", len(r.fold), sshDur(time.Duration(total)*time.Millisecond))
	return highlightRow(cursor+goodText.Render("✓")+" "+faintText.Render(text), w, bgSGR(colSel))
}

// sshBlockMark is the rail marker. Shapes stay distinct without any colour.
func sshBlockMark(b *protocol.SSHBlock) string {
	if b.Actor == protocol.SSHActorHuman && !protocol.SSHBlockTerminal(b.Status) {
		return sshAccent.Render("■")
	}
	switch b.Status {
	case protocol.SSHBlockPendingApproval:
		return statusOrange.Render("◆")
	case protocol.SSHBlockQueued:
		return faintText.Render("◇")
	case protocol.SSHBlockRunning:
		return sshInfo.Render(string([]rune("◐◓◑◒")[sshNow().Second()%4]))
	case protocol.SSHBlockSucceeded:
		return goodText.Render("✓")
	case protocol.SSHBlockFailed:
		return badText.Render("✗")
	case protocol.SSHBlockDenied, protocol.SSHBlockExpired, protocol.SSHBlockRefused:
		return faintText.Render("⊘")
	case protocol.SSHBlockTimedOut, protocol.SSHBlockStopped:
		return warnText.Render("✗")
	case protocol.SSHBlockLost:
		return warnText.Render("?")
	}
	// A status from a newer daemon renders as words, never as a false pass.
	return faintText.Render("?")
}

// sshBlockRight is the right side of line 1: result, duration, size, time.
func sshBlockRight(b *protocol.SSHBlock, now time.Time) string {
	dur := sshDur(time.Duration(b.DurationMs) * time.Millisecond)
	size := sshBytes(b.StdoutBytes + b.StderrBytes)
	var word string
	switch b.Status {
	case protocol.SSHBlockPendingApproval:
		word = statusOrange.Render("Waiting for you")
		if !b.CreatedAt.IsZero() {
			word += faintText.Render(" · " + sshCoarse(now.Sub(b.CreatedAt)))
		}
	case protocol.SSHBlockQueued:
		word = faintText.Render("Queued")
	case protocol.SSHBlockRunning:
		word = sshInfo.Render(sshDur(now.Sub(b.StartedAt)))
		if b.TimeoutSeconds > 0 {
			word += faintText.Render(" of " + sshDur(time.Duration(b.TimeoutSeconds)*time.Second))
		}
	case protocol.SSHBlockSucceeded:
		word = faintText.Render("exit 0 · " + dur + " · " + size)
	case protocol.SSHBlockFailed:
		code := "?"
		if b.ExitCode != nil {
			code = fmt.Sprint(*b.ExitCode)
		}
		word = badText.Render("exit "+code) + faintText.Render(" · "+dur+" · "+size)
	case protocol.SSHBlockDenied:
		word = faintText.Render("Denied")
	case protocol.SSHBlockExpired:
		word = faintText.Render("Expired")
	case protocol.SSHBlockRefused:
		word = faintText.Render("Did not run")
	case protocol.SSHBlockTimedOut:
		word = warnText.Render("Timed out · " + dur)
	case protocol.SSHBlockStopped:
		word = warnText.Render("Stopped · " + dur)
	case protocol.SSHBlockLost:
		word = warnText.Render("Result unknown · " + dur)
	default:
		word = faintText.Render(sshLine(b.Status))
	}
	if b.Actor == protocol.SSHActorHuman {
		word = sshAccent.Render("you ran this") + faintText.Render(" · ") + word
		if b.Private {
			word = faintText.Render("only you · S share · ") + word
		}
	} else if b.Approval.Mode == protocol.SSHApprovalGrant {
		word = warnText.Render("grant") + faintText.Render(" · ") + word
	}
	if b.Elevated {
		word = warnText.Render("sudo") + faintText.Render(" · ") + word
	}
	if !b.CreatedAt.IsZero() {
		word += faintText.Render(" · " + b.CreatedAt.Local().Format("15:04"))
	}
	return word
}

// sshRefusalCopy is the row text per refusal code. The daemon's own sentence wins
// when the code is one this build does not know.
func sshRefusalCopy(b *protocol.SSHBlock) string {
	switch b.RefusalCode {
	case protocol.SSHRefuseHumanControl:
		return "You had the wheel."
	case protocol.SSHRefuseNotConnected:
		return "The connection was not up."
	case protocol.SSHRefuseAwaitingGo:
		return "Still waiting for your go."
	case protocol.SSHRefuseTooManyQueued:
		return "Too many commands were already waiting."
	}
	return b.StatusDetail
}

// sshBlockDetail is line 2: why a row reads the way it does. All of it is lola's
// own words or sanitized output, so it belongs on the band.
func sshBlockDetail(b *protocol.SSHBlock, now time.Time) (string, string) {
	preview := func() string {
		lines := strings.Split(strings.TrimRight(b.Preview, "\n"), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			if t := strings.TrimSpace(lines[i]); t != "" {
				text := sshLine(lines[i])
				if b.PreviewStream == protocol.SSHStreamStderr {
					text = "stderr  " + text
				}
				return text
			}
		}
		return ""
	}
	limit := sshDur(time.Duration(b.TimeoutSeconds) * time.Second)
	switch b.Status {
	case protocol.SSHBlockPendingApproval:
		if !b.ApprovalExpiresAt.IsZero() {
			return "Expires in " + sshExpiry(b.ApprovalExpiresAt.Sub(now)), ""
		}
	case protocol.SSHBlockQueued:
		return "Approved. Waiting for a free slot.", ""
	case protocol.SSHBlockRunning, protocol.SSHBlockFailed:
		return preview(), ""
	case protocol.SSHBlockDenied:
		return "You denied this.", b.Approval.Reason
	case protocol.SSHBlockExpired:
		if !b.EndedAt.IsZero() && !b.CreatedAt.IsZero() {
			return fmt.Sprintf("Nobody answered within %d minutes. It did not run.", int(b.EndedAt.Sub(b.CreatedAt).Minutes())), ""
		}
		return "Nobody answered in time. It did not run.", ""
	case protocol.SSHBlockRefused:
		return sshLine(sshRefusalCopy(b)), ""
	case protocol.SSHBlockTimedOut:
		return "Stopped locally at the " + limit + " limit. The remote process may still be running.", ""
	case protocol.SSHBlockStopped:
		detail := sshLine(b.StatusDetail)
		if !b.StartedAt.IsZero() {
			detail = strings.TrimSpace(detail + " " + protocol.SSHRemoteOutlives)
		}
		return detail, ""
	case protocol.SSHBlockLost:
		detail := sshLine(b.StatusDetail)
		if detail == "" {
			detail = "The remote process may still be running."
		}
		return detail, ""
	}
	return "", ""
}

// sshBlockLines is a block at depths 1 and 2. A row, its detail and its facts are
// all on the band; the agent's "why" is underneath, off it, in the agent's voice.
func (m *rootModel) sshBlockLines(it *protocol.SSHTimelineItem, cursor, rail string, w int) []string {
	s := &m.ssh
	b := it.Block
	now := sshNow()
	band := bgSGR(colSel)
	cont := cursor + rail + " "
	if cursor != " " {
		cont = " " + rail + " "
	}
	mark := cursor + sshBlockMark(b) + " "

	host := ""
	if len(s.conns) > 1 {
		host = faintText.Render(sshLine(b.Host) + " ")
	}
	right := sshBlockRight(b, now)
	chev := faintText.Render(" ▸")
	expanded := s.expanded[it.ID]
	if expanded {
		chev = faintText.Render(" ▾")
	}
	room := w - lipgloss.Width(mark) - lipgloss.Width(host) - lipgloss.Width(right) - lipgloss.Width(chev) - 4
	cmd := truncPlain(sshLine(b.Command), max(8, room))
	first := mark + host + faintText.Render("$ ") + cmd
	gap := w - lipgloss.Width(first) - lipgloss.Width(right) - lipgloss.Width(chev)
	if gap < 1 {
		gap = 1
	}
	out := []string{highlightRow(first+strings.Repeat(" ", gap)+right+chev, w, band)}

	detail, reason := sshBlockDetail(b, now)
	if expanded {
		out = out[:0]
		out = append(out, highlightRow(mark+faintText.Render(sshLine(b.ID))+strings.Repeat(" ", max(1, w-lipgloss.Width(mark)-lipgloss.Width(b.ID)-lipgloss.Width(right)-3))+right+chev, w, band))
		for _, l := range sshWrapHard("$ "+b.Command, max(10, w-lipgloss.Width(cont))) {
			out = append(out, highlightRow(cont+l, w, band))
		}
		for _, f := range m.sshBlockFacts(b, now) {
			out = append(out, highlightRow(cont+f, w, band))
		}
	}
	if detail != "" {
		style := faintText
		switch b.Status {
		case protocol.SSHBlockTimedOut, protocol.SSHBlockStopped, protocol.SSHBlockLost:
			style = warnText
		case protocol.SSHBlockPendingApproval:
			style = statusOrange
		}
		out = append(out, highlightRow(cont+"  "+style.Render(truncPlain(detail, max(10, w-lipgloss.Width(cont)-2))), w, band))
	}
	if expanded {
		hint := "enter output · a ask about this"
		if b.Status == protocol.SSHBlockRunning {
			hint = "X stop · " + hint
		}
		out = append(out, highlightRow(cont+faintText.Render(hint), w, band))
		if b.Status == protocol.SSHBlockRunning {
			out = append(out, highlightRow(cont+faintText.Render(protocol.SSHRemoteOutlives), w, band))
		}
	}
	// Below this line is NOT Lola's voice, so no band.
	if reason != "" {
		out = append(out, previewLine(cont+"  "+sshAccent.Render("▌")+sshLine(reason), w))
	}
	if b.Why != "" && s.filter != sshFilterFacts {
		out = append(out, previewLine(cont+"    "+faintText.Render("○ "+s.agentName()+": "+sshLine(b.Why)), w))
	}
	return out
}

func (m *rootModel) sshBlockFacts(b *protocol.SSHBlock, now time.Time) []string {
	s := &m.ssh
	host := sshLine(b.Host)
	if c := s.connByID(b.Conn); c != nil {
		host = sshHostLabel(c) + " (" + sshIdentity(c) + ")"
	}
	cwd := faintText.Render("login directory")
	if b.Cwd != "" {
		cwd = sshLine(b.Cwd)
	}
	asked := s.agentName()
	if b.Actor == protocol.SSHActorHuman {
		asked = "You"
	}
	fact := func(k, v string) string { return faintText.Render(padTo(k, 10)) + v }
	out := []string{
		fact("Host", host),
		fact("Tier", sshTierTag(b.Tier)),
		fact("Runs in", cwd),
		fact("Asked by", asked),
	}
	if !b.StartedAt.IsZero() {
		out = append(out, fact("Started", b.StartedAt.Local().Format("15:04:05")))
	}
	if !b.EndedAt.IsZero() {
		out = append(out, fact("Ended", b.EndedAt.Local().Format("15:04:05")+" · "+sshDur(time.Duration(b.DurationMs)*time.Millisecond)))
	}
	if b.ExitCode != nil {
		out = append(out, fact("Exit", fmt.Sprint(*b.ExitCode)))
	}
	if b.Status == protocol.SSHBlockRunning && b.TimeoutSeconds > 0 {
		out = append(out, fact("Timeout", sshDur(time.Duration(b.TimeoutSeconds)*time.Second)))
	}
	switch b.Approval.Mode {
	case protocol.SSHApprovalPerCommand:
		if b.Approval.Granted {
			out = append(out, fact("Allowed", "You approved at "+b.Approval.At.Local().Format("15:04")))
		}
	case protocol.SSHApprovalGrant:
		until := "Grant"
		if c := s.connByID(b.Conn); c != nil && c.Grant != nil && c.Grant.ID == b.Approval.GrantID {
			until = "Grant, until " + c.Grant.ExpiresAt.Local().Format("15:04")
		}
		out = append(out, fact("Allowed", until))
	case protocol.SSHApprovalHuman:
		out = append(out, fact("Allowed", "You ran this"))
	}
	sizes := fmt.Sprintf("stdout %s   stderr %s", sshBytes(b.StdoutBytes), sshBytes(b.StderrBytes))
	if b.DroppedBytes > 0 {
		sizes += "   " + warnText.Render(sshBytes(b.DroppedBytes)+" not kept")
	}
	out = append(out, faintText.Render(sizes))
	if b.Binary {
		out = append(out, warnText.Render("This output contained binary data. Control characters were removed."))
	}
	return out
}

func sshSystemLines(it *protocol.SSHTimelineItem, lead, cursor string, w int) []string {
	ev := it.System
	right := ""
	if !it.At.IsZero() {
		right = faintText.Render(it.At.Local().Format("15:04"))
	}
	mark := cursor + faintText.Render("▪") + " "
	text := faintText.Render(sshLine(ev.Text))
	if ev.Kind == protocol.SSHEventHandedBack {
		text += faintText.Render(" " + protocol.SSHDrivingNote)
	}
	gap := max(1, w-lipgloss.Width(mark)-lipgloss.Width(text)-lipgloss.Width(right))
	out := []string{previewLine(mark+text+strings.Repeat(" ", gap)+right, w)}
	if ev.Note != "" {
		out = append(out, previewLine(strings.Replace(lead, "›", " ", 1)+sshAccent.Render("▌")+sshLine(ev.Note), w))
	}
	return out
}

// sshHumanLines is the human's voice: the accent bar, and what lola can honestly
// say about delivery. The word is "Delivered". It cannot know "Read".
func (m *rootModel) sshHumanLines(it *protocol.SSHTimelineItem, lead, rail string, w int) []string {
	s := &m.ssh
	h := it.Human
	bar := sshAccent.Render("▌")
	room := max(10, w-lipgloss.Width(lead)-1)
	var out []string
	for i, l := range sshWrap(h.Text, room) {
		if h.Recalled {
			l = sshStrike.Render(l)
		}
		prefix := lead
		if i > 0 {
			prefix = " " + rail + " "
		}
		out = append(out, previewLine(prefix+bar+l, w))
	}
	indent := " " + rail + "  "
	state := ""
	si := m.sessions.byID(s.session)
	switch {
	case h.Recalled:
		state = "Recalled"
	case h.Delivered && h.Via == protocol.SSHViaReply:
		state = "Delivered with its last command · " + h.DeliveredAt.Local().Format("15:04")
	case h.Delivered && h.Via == protocol.SSHViaPrompt:
		state = "Delivered at its prompt · " + h.DeliveredAt.Local().Format("15:04")
	case h.Delivered:
		state = "Delivered"
	case sshAgentGone(si):
		state = "Queued. " + s.agentName() + " has exited. R recall"
	default:
		state = "Queued · R recall"
	}
	out = append(out, previewLine(indent+faintText.Render(state), w))
	if s.sshMaybeAnsweredElsewhere(it, si) {
		out = append(out, previewLine(indent+faintText.Render(s.agentName()+" may have answered in its own terminal. A show"), w))
	}
	if h.Transcript != "" {
		key := it.ID
		if s.expanded[key] {
			out = append(out, previewLine(indent+faintText.Render("Your terminal · "+sshBytes(int64(len(h.Transcript)))+" ▾"), w))
			for _, l := range sshTextLines(h.Transcript) {
				out = append(out, previewLine(indent+"  "+faintText.Render(l), w))
			}
		} else {
			out = append(out, previewLine(indent+faintText.Render("Your terminal · "+sshBytes(int64(len(h.Transcript)))+" ▸ enter"), w))
		}
	}
	return out
}

// sshMaybeAnsweredElsewhere: delivered a while ago, the agent has gone idle, and
// nothing from it has appeared since. Its answer may be in its own pane.
func (s *sshModel) sshMaybeAnsweredElsewhere(it *protocol.SSHTimelineItem, si *protocol.SessionInfo) bool {
	h := it.Human
	if !h.Delivered || si == nil || sshNow().Sub(h.DeliveredAt) < 60*time.Second {
		return false
	}
	if a, _ := axesOf(*si); a != state.AgentIdle && !(a == state.AgentWaitingInput && si.InputReason == string(state.InputIdleNotify)) {
		return false
	}
	for i := range s.items {
		if s.items[i].Order > it.Order && s.items[i].Element != nil {
			return false
		}
	}
	return true
}

// --- elements -----------------------------------------------------------------

// sshElementLines draws one claim: a hollow ring, a quiet lead word, bare text.
// Never a band, never a filled marker, and severity only colours the ring.
func (m *rootModel) sshElementLines(e *protocol.SSHElement, key, lead, rail string, selected bool, w int) []string {
	s := &m.ssh
	ring := faintText.Render("○")
	word := ""
	title := sshLine(e.Title)
	titleStyled := title
	open := false // body shown without a key
	switch e.Kind {
	case "summary":
		ring, open = sshInk.Render("○"), true
	case "widget":
		// A widget is HTML in a sandbox, which a terminal cannot draw and must not try to.
		word = faintText.Render("Widget  ")
		titleStyled = title + faintText.Render(" · shown in the app")
	case "narration":
		open = true
	case "hypothesis":
		word = faintText.Render("Theory  ")
		switch e.State {
		case "confirmed":
			ring = sshInk.Render("◉")
			titleStyled = title + "  " + sshInk.Render("Agent says confirmed")
		case "refuted":
			titleStyled = sshStrike.Render(title) + "  " + faintText.Render("Agent says refuted")
		default:
			titleStyled = title + "  " + faintText.Render("Open")
		}
	case "finding":
		switch e.Severity {
		case "critical":
			ring, word = badText.Render("◎"), badText.Render("Critical  ")
		case "high":
			ring, word = statusOrange.Render("○"), statusOrange.Render("High  ")
		case "medium":
			ring, word = warnText.Render("○"), warnText.Render("Medium  ")
		case "low":
			ring, word = sshInfo.Render("○"), faintText.Render("Low  ")
		default:
			word = faintText.Render("Note  ")
		}
	case "plan":
		word = faintText.Render("Plan  ")
	case "metric":
		word = faintText.Render("Read  ")
	case "recommendation":
		word, open = faintText.Render("Suggests  "), true
	case "proposal":
		word = faintText.Render("Proposed  ")
	case "question":
		word = faintText.Render("Asks  ")
	}
	updated := ""
	if e.Revision > 1 {
		updated = faintText.Render("  updated " + shortDuration(sshNow().Sub(e.UpdatedAt)))
	}
	indent := " " + rail + "    "
	room := max(10, w-lipgloss.Width(indent))
	var out []string
	titleLine := lead + ring + " " + word + titleStyled + updated
	if lipgloss.Width(titleLine) <= w {
		out = append(out, previewLine(titleLine, w))
	} else {
		wrapped := sshWrap(title, max(10, w-lipgloss.Width(lead)-2-lipgloss.Width(word)))
		out = append(out, previewLine(lead+ring+" "+word+wrapped[0], w))
		for _, l := range wrapped[1:] {
			out = append(out, previewLine(indent+l, w))
		}
	}
	body := func(st lipgloss.Style) {
		for _, l := range sshWrap(e.Body, room) {
			out = append(out, previewLine(indent+st.Render(l), w))
		}
	}
	switch {
	case e.Body == "":
	case open || s.expanded[key]:
		body(sshInk)
	default:
		out = append(out, previewLine(indent+faintText.Render("▸ More"), w))
	}
	switch e.Kind {
	case "plan":
		for _, st := range e.Steps {
			out = append(out, previewLine(indent+sshStepLine(st), w))
		}
	case "metric":
		nameW := 4
		for _, mt := range e.Metrics {
			nameW = max(nameW, lipgloss.Width(sshLine(mt.Name)))
		}
		for _, mt := range e.Metrics {
			val := sshLine(mt.Value)
			if mt.Unit != "" {
				val += " " + sshLine(mt.Unit)
			}
			out = append(out, previewLine(indent+faintText.Render(padTo(sshLine(mt.Name), nameW+2))+val, w))
		}
	case "proposal":
		out = append(out, s.sshProposalBody(e, indent, selected, w)...)
	case "question":
		out = append(out, s.sshQuestionBody(e, indent, selected, w)...)
	}
	if plates := s.sshEvidencePlates(e); plates != "" {
		line := indent + plates
		if selected {
			line += faintText.Render("   e open")
		}
		out = append(out, previewLine(line, w))
	}
	return out
}

func sshStepLine(st protocol.SSHElementStep) string {
	text := sshLine(st.Text)
	switch st.State {
	case "done":
		return faintText.Render("○ ") + sshStrike.Render(text)
	case "active":
		return sshInk.Render("◌ " + text)
	case "skipped":
		return faintText.Render("○ " + text + " (skipped)")
	case "failed":
		return badText.Render("○ ") + text
	}
	return faintText.Render("○ " + text)
}

// sshEvidencePlates is the one place a claim touches something recorded, so it is
// a small piece of Lola's voice: a plate reading the block's facts. It never says
// "verified", because the block being real does not make the claim true.
func (s *sshModel) sshEvidencePlates(e *protocol.SSHElement) string {
	var out []string
	for _, ev := range e.Evidence {
		b := s.blockByID(ev.Block)
		id := sshLine(ev.Block)
		switch {
		case b != nil:
			out = append(out, sshPlateFor(b))
		case ev.Verified:
			text := id
			if ev.ExitCode != nil {
				text += fmt.Sprintf(" exit %d", *ev.ExitCode)
			} else if ev.Status != "" {
				text += " " + sshLine(ev.Status)
			}
			out = append(out, sshPlate.Render("["+text+"]"))
		default:
			out = append(out, warnText.Render(id+" not on record"))
		}
	}
	return strings.Join(out, " ")
}

func sshPlateFor(b *protocol.SSHBlock) string {
	switch b.Status {
	case protocol.SSHBlockPendingApproval, protocol.SSHBlockQueued, protocol.SSHBlockRunning:
		return sshPlate.Render("[" + b.ID + " running]")
	}
	dur := sshDur(time.Duration(b.DurationMs) * time.Millisecond)
	if b.ExitCode == nil {
		return sshPlate.Render("[" + b.ID + " " + sshLine(b.Status) + "]")
	}
	text := fmt.Sprintf("[%s exit %d · %s]", b.ID, *b.ExitCode, dur)
	if *b.ExitCode != 0 {
		return sshPlateBad.Render(text)
	}
	return sshPlate.Render(text)
}

// sshProposalBody: dashes around the command say it is not on the record yet.
func (s *sshModel) sshProposalBody(e *protocol.SSHElement, indent string, selected bool, w int) []string {
	var out []string
	cmdStyle := sshInk
	if e.Outcome != nil {
		cmdStyle = sshStrike
	}
	room := max(10, w-lipgloss.Width(indent)-4)
	for _, l := range sshWrapHard("$ "+e.Command, room) {
		out = append(out, previewLine(indent+faintText.Render("┆ ")+cmdStyle.Render(l), w))
	}
	if e.Cwd != "" {
		out = append(out, previewLine(indent+faintText.Render("┆ in "+sshLine(e.Cwd)), w))
	}
	if o := e.Outcome; o != nil {
		if o.Block != "" {
			out = append(out, previewLine(indent+sshAccent.Render("You ran this")+faintText.Render(" → "+sshLine(o.Block)), w))
		} else {
			out = append(out, previewLine(indent+sshAccent.Render("You declined this"), w))
			if o.Text != "" {
				out = append(out, previewLine(indent+sshAccent.Render("▌")+sshLine(o.Text), w))
			}
		}
		return out
	}
	if selected {
		host := ""
		if c := s.proposalConn(e); c != nil {
			host = " on " + sshHostLabel(c)
		}
		out = append(out, previewLine(indent+faintText.Render("P run"+host+"… · d decline"), w))
	}
	return out
}

func (s *sshModel) sshQuestionBody(e *protocol.SSHElement, indent string, selected bool, w int) []string {
	var out []string
	if o := e.Outcome; o != nil {
		return append(out, previewLine(indent+sshAccent.Render("▌")+sshLine(o.Text), w))
	}
	if len(e.Choices) > 0 {
		var cells []string
		for i, c := range e.Choices {
			cells = append(cells, faintText.Render(fmt.Sprintf("%d ", i+1))+sshLine(c))
		}
		out = append(out, previewLine(indent+strings.Join(cells, "   "), w))
	}
	if selected {
		out = append(out, previewLine(indent+faintText.Render("i answer in your own words"), w))
	}
	return out
}

// --- the dock -----------------------------------------------------------------

// sshDockLines is the one card waiting on the human. Every field is drawn from
// the block and connection, and the tier's rule sits on top of it.
func (m *rootModel) sshDockLines(w int) (lines []string) {
	s := &m.ssh
	c := s.card()
	if c == nil {
		if si := s.conn(); si != nil && si.State == protocol.SSHConnClosed {
			return []string{sshTierRuleFaint(w)}
		}
		return nil
	}
	tier := protocol.SSHTierDev
	if c.conn != nil {
		tier = c.conn.Tier
	} else if c.block != nil {
		tier = c.block.Tier
	}
	out := []string{sshTierRule(tier, w)}
	defer func() { lines = sshFlatten(lines) }()
	switch c.kind {
	case sshCardPrompt:
		out = append(out, m.sshPromptCard(c, w)...)
	case sshCardAuth:
		out = append(out, m.sshAuthCard(c, w)...)
	case sshCardApproval:
		out = append(out, m.sshApprovalCard(c, w)...)
	case sshCardGo:
		out = append(out, m.sshGoCard(c, w)...)
	case sshCardReconnect:
		out = append(out, m.sshReconnectCard(c, w)...)
	case sshCardFailed:
		out = append(out, m.sshFailedCard(c, w)...)
	case sshCardProposal:
		out = append(out, m.sshProposalCard(c, w)...)
	case sshCardShellClosed:
		out = append(out, m.sshShellClosedCard(c, w)...)
	}
	return out
}

// sshNote is one sentence of a card or bar, wrapped rather than clipped: the words
// here are the ones a human is trusting, so they are never cut off at the margin.
// It returns its rows joined, and sshFlatten splits them back out, so a card can
// be written as one list of lines.
func sshNote(st lipgloss.Style, text string, w int) string {
	rows := sshWrap(text, max(10, w-3))
	for i, r := range rows {
		rows[i] = previewLine("   "+st.Render(r), w)
	}
	return strings.Join(rows, "\n")
}

func sshFlatten(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, strings.Split(l, "\n")...)
	}
	return out
}

// sshYKey is the Y hint, faint until the card is armed.
func (s *sshModel) sshYKey() string {
	if s.armed() {
		return warnText.Bold(true).Render("Y")
	}
	return faintText.Render("Y")
}

func sshWhere(c *protocol.SSHConnInfo) string {
	if c == nil {
		return ""
	}
	return sshHostLabel(c) + "  " + sshTierTag(c.Tier)
}

// sshPromptCard shows a shell's question as it was asked, and who asks. Its words came from
// the other end and may be a fake, so nothing here reads as lola's own sign-in.
func (m *rootModel) sshPromptCard(c *sshCard, w int) []string {
	q := c.conn.Prompt
	head := sshHostLabel(c.conn) + " asks"
	if q.Stage == protocol.SSHPromptSignIn {
		head = "Signing in to " + sshHostLabel(c.conn)
	} else if q.Command != "" {
		head += " · " + sshLine(q.Command)
	}
	out := []string{previewLine(" "+statusOrange.Render(head), w)}
	for _, l := range q.Context {
		out = append(out, previewLine("   "+faintText.Render(sshLine(l)), w))
	}
	out = append(out, previewLine("   "+sshLine(q.Line), w))
	if q.Attempt > 0 && q.Kind == protocol.SSHPromptSecret {
		out = append(out, previewLine("   "+warnText.Render("Not accepted. Try again."), w))
	}
	keys := warnText.Render("Y") + faintText.Render(" answer  ")
	if q.Kind == protocol.SSHPromptConfirm {
		keys = warnText.Render("Y") + faintText.Render(" yes  ") + warnText.Render("N") + faintText.Render(" no  ")
	}
	keys += warnText.Render("X") + faintText.Render(" cancel  ") + warnText.Render("T") + faintText.Render(" terminal")
	return append(out, previewLine("   "+keys, w))
}

func (m *rootModel) sshAuthCard(c *sshCard, w int) []string {
	out := []string{previewLine(" "+statusOrange.Render("Signing in.")+"  "+sshWhere(c.conn)+"  "+faintText.Render("Waiting for input?"), w)}
	if !c.conn.ConnectDeadline.IsZero() {
		out = append(out, previewLine("   "+faintText.Render("Gives up in "+sshClock(c.conn.ConnectDeadline.Sub(sshNow()))+".  ")+warnText.Render("T")+faintText.Render(" terminal"), w))
	} else {
		out = append(out, previewLine("   "+warnText.Render("T")+faintText.Render(" terminal"), w))
	}
	return out
}

func (m *rootModel) sshApprovalCard(c *sshCard, w int) []string {
	s := &m.ssh
	b := c.block
	host := sshLine(b.Host)
	if c.conn != nil {
		host = sshHostLabel(c.conn)
	}
	head := " " + statusOrange.Render("⚠ "+s.agentName()+" wants to run a command on "+host) + "  " + sshTierTag(b.Tier)
	if c.total > 1 {
		count := faintText.Render(fmt.Sprintf("%d of %d", c.index, c.total))
		head += strings.Repeat(" ", max(2, w-lipgloss.Width(head)-lipgloss.Width(count))) + count
	}
	out := []string{previewLine(head, w)}

	// The command is whole, exact and never reflowed. Past the cap it says so and
	// Y stays disabled until the whole of it has been opened.
	wrapped := sshWrapHard(b.Command, max(10, w-6))
	shown := wrapped
	if len(wrapped) > sshCmdPreviewLines {
		shown = wrapped[:sshCmdPreviewLines]
	}
	for i, l := range shown {
		prefix := "   $ "
		if i > 0 {
			prefix = "   " + faintText.Render("│") + " "
		}
		out = append(out, highlightRow(prefix+l, w, bgSGR(colSel)))
	}
	needsRead := s.cmdNeedsReading(b, w-6)
	if len(wrapped) > sshCmdPreviewLines {
		more := fmt.Sprintf("… %d more lines: enter to read all", len(wrapped)-sshCmdPreviewLines)
		if !needsRead {
			more = fmt.Sprintf("… %d more lines, read", len(wrapped)-sshCmdPreviewLines)
		}
		out = append(out, highlightRow("   "+warnText.Render(more), w, bgSGR(colSel)))
	}
	cwd := "login directory"
	if b.Cwd != "" {
		cwd = sshLine(b.Cwd)
	}
	facts := "in " + cwd
	if c.conn != nil {
		who := sshIdentity(c.conn) + " (alias " + sshLine(c.conn.Alias) + ")"
		if c.conn.ResolvedUser == "" && c.conn.ResolvedHostname == "" && c.conn.User == "" {
			who = warnText.Render("Lola could not read this from your ssh config.")
		}
		facts += " · as " + who
	}
	if b.TimeoutSeconds > 0 {
		facts += " · timeout " + sshDur(time.Duration(b.TimeoutSeconds)*time.Second)
	}
	out = append(out, highlightRow("   "+facts, w, bgSGR(colSel)))
	if b.Why != "" {
		for i, l := range sshWrap(s.agentName()+" says: "+b.Why, max(10, w-6)) {
			prefix := "   ○ "
			if i > 0 {
				prefix = "     "
			}
			out = append(out, previewLine(faintText.Render(prefix)+faintText.Render(l), w))
		}
	}
	out = append(out, sshNote(faintText, protocol.SSHDataWarning, w))
	out = append(out, sshNote(faintText, protocol.SSHUserIsTheLimit, w))
	if c.conn == nil || !c.conn.Sandboxed {
		out = append(out, sshNote(warnText, protocol.SSHUnsandboxedNote, w))
	}

	// The grant control exists only on a host that allows one, and it says plainly
	// how wide it is.
	choices := sshGrantChoices(c.conn)
	grant := s.grantMinutes(c.conn)
	action := "run once on " + host
	switch {
	case len(choices) > 0 && grant > 0:
		action = fmt.Sprintf("run and allow for %d min", grant)
		out = append(out, sshNote(warnText, fmt.Sprintf("Also lets %s run anything on %s without asking for %d min.", s.agentName(), host, grant), w))
		out = append(out, sshNote(faintText, "A grant is not limited to this command. It ends if you take the wheel.", w))
	case sshTierNorm(b.Tier) == protocol.SSHTierProduction:
		out = append(out, sshNote(faintText, "Production asks for every command.", w))
	case len(choices) == 0:
		out = append(out, sshNote(faintText, "This host asks for every command.", w))
	}
	expiry := ""
	if !b.ApprovalExpiresAt.IsZero() {
		left := b.ApprovalExpiresAt.Sub(sshNow())
		word := "expires in " + sshExpiry(left)
		if left < 5*time.Minute {
			expiry = " · " + warnText.Render(word)
		} else {
			expiry = faintText.Render(" · " + word)
		}
	}
	yKey := s.sshYKey()
	yText := faintText.Render(" " + action)
	if needsRead {
		yKey, yText = faintText.Render("Y"), faintText.Render(" disabled until you read it")
	}
	keys := "   " + yKey + yText + faintText.Render(" · ") + warnText.Render("N") + faintText.Render(" deny")
	if len(choices) > 0 {
		label := "off"
		if grant > 0 {
			label = fmt.Sprintf("%d min", grant)
		}
		keys += faintText.Render(" · ") + warnText.Render("t") + faintText.Render(" grant: "+label)
	}
	return append(out, previewLine(keys+expiry, w))
}

func (m *rootModel) sshGoCard(c *sshCard, w int) []string {
	s := &m.ssh
	conn := c.conn
	head := " " + statusOrange.Render("⚠ "+s.agentName()+" asks to connect") + "  " + sshWhere(conn)
	if c.total > 1 {
		count := faintText.Render(fmt.Sprintf("%d of %d", c.index, c.total))
		head += strings.Repeat(" ", max(2, w-lipgloss.Width(head)-lipgloss.Width(count))) + count
	}
	out := []string{previewLine(head, w)}
	out = append(out, highlightRow("   as "+sshIdentity(conn)+" (alias "+sshLine(conn.Alias)+")", w, bgSGR(colSel)))
	if conn.Why != "" {
		out = append(out, sshNote(faintText, "○ "+s.agentName()+" says why: "+sshLine(conn.Why), w))
	}
	if conn.Scope != "" {
		out = append(out, sshNote(faintText, "○ "+s.agentName()+" says it will: "+sshLine(conn.Scope), w))
	}
	ask := "Each command will still ask for approval."
	if conn.GrantMaxMinutes > 0 && sshTierNorm(conn.Tier) != protocol.SSHTierProduction {
		ask = "Each command will ask, unless you open a grant."
	}
	out = append(out,
		sshNote(faintText, "Nothing connects until you press this.", w),
		sshNote(faintText, ask, w),
		sshNote(faintText, protocol.SSHDataWarning, w))
	if !conn.Sandboxed {
		out = append(out, sshNote(warnText, protocol.SSHUnsandboxedNote, w))
	}
	return append(out, previewLine("   "+s.sshYKey()+faintText.Render(" open terminal and connect · ")+warnText.Render("N")+faintText.Render(" deny"), w))
}

func (m *rootModel) sshReconnectCard(c *sshCard, w int) []string {
	s := &m.ssh
	host := sshHostLabel(c.conn)
	var out []string
	if sshIdleClosed(c.conn) {
		out = append(out, previewLine(" "+faintText.Render(fmt.Sprintf("The connection closed after %s with nothing using it.", sshMinutes(c.conn.IdleLingerSeconds))), w))
	} else {
		out = append(out, sshNote(warnText, "Connection to "+host+" dropped.", w))
		if n := s.sshLostCount(c.conn.ID); n > 0 {
			line := sshCount(n, "command") + " were running. Their results are unknown, and they may still be running on the host."
			if n == 1 {
				line = "1 command was running. Its result is unknown, and it may still be running on the host."
			}
			out = append(out, sshNote(faintText, line, w))
		}
	}
	return append(out,
		sshNote(faintText, "Reconnecting opens a terminal, and may ask for MFA again.", w),
		sshNote(faintText, protocol.SSHDataWarning, w),
		previewLine("   "+s.sshYKey()+faintText.Render(" reconnect · ")+warnText.Render("C")+faintText.Render(" close connection"), w))
}

func (s *sshModel) sshLostCount(conn string) int {
	n := 0
	for i := range s.items {
		if b := s.items[i].Block; b != nil && b.Conn == conn && b.Status == protocol.SSHBlockLost {
			n++
		}
	}
	return n
}

func (m *rootModel) sshFailedCard(c *sshCard, w int) []string {
	s := &m.ssh
	host := sshHostLabel(c.conn)
	line := "Could not connect to " + host + ". ssh's message is in the terminal."
	detail := strings.ToLower(c.conn.StateDetail)
	switch {
	case strings.Contains(detail, "cancelled"):
		line = sshLine(c.conn.StateDetail)
	case strings.Contains(detail, "not found"):
		line = "ssh was not found on this Mac. Lola needs it to connect."
	case strings.Contains(detail, "timed out") || strings.Contains(detail, "wait ran out") || strings.Contains(detail, "signing in"):
		line = fmt.Sprintf("Nobody finished signing in in time. %s", sshLine(c.conn.StateDetail))
	}
	keys := "   " + s.sshYKey() + faintText.Render(" try again · ")
	if c.conn.Tab != "" {
		keys += warnText.Render("T") + faintText.Render(" read ssh's message · ")
	}
	keys += warnText.Render("C") + faintText.Render(" close connection")
	return []string{
		sshNote(badText, line, w),
		sshNote(faintText, protocol.SSHDataWarning, w),
		previewLine(keys, w),
	}
}

func (m *rootModel) sshShellClosedCard(c *sshCard, w int) []string {
	s := &m.ssh
	out := []string{sshNote(faintText, "Your shell on "+sshHostLabel(c.conn)+" ended. The connection is still up, and "+s.agentName()+" can keep working.", w)}
	if c.conn.IdleLingerSeconds > 0 {
		out = append(out, sshNote(faintText, fmt.Sprintf("It closes by itself after %s with no client.", sshMinutes(c.conn.IdleLingerSeconds)), w))
	}
	return append(out, previewLine("   "+warnText.Render("T")+faintText.Render(" open a new shell"), w))
}

func (m *rootModel) sshProposalCard(c *sshCard, w int) []string {
	s := &m.ssh
	e := c.element
	host := "this host"
	if c.conn != nil {
		host = sshHostLabel(c.conn)
	}
	out := []string{previewLine(" "+statusOrange.Render("⚠ Run this yourself on "+host+"?")+"  "+sshTierTagOf(c.conn), w)}
	for i, l := range sshWrapHard(e.Command, max(10, w-6)) {
		prefix := "   $ "
		if i > 0 {
			prefix = "   " + faintText.Render("│") + " "
		}
		out = append(out, highlightRow(prefix+l, w, bgSGR(colSel)))
	}
	facts := "in login directory"
	if e.Cwd != "" {
		facts = "in " + sshLine(e.Cwd)
	}
	if c.conn != nil {
		facts += " · as " + sshIdentity(c.conn)
	}
	out = append(out, highlightRow("   "+facts, w, bgSGR(colSel)))
	return append(out,
		sshNote(faintText, "You are running this, not "+s.agentName()+". It is recorded as yours.", w),
		sshNote(faintText, protocol.SSHDataWarning, w),
		sshNote(faintText, protocol.SSHUserIsTheLimit, w),
		previewLine("   "+s.sshYKey()+faintText.Render(" run on "+host+" · ")+warnText.Render("N")+faintText.Render(" back"), w))
}

func sshTierTagOf(c *protocol.SSHConnInfo) string {
	if c == nil {
		return sshTierTag("")
	}
	return sshTierTag(c.Tier)
}

// --- the bottom lines ---------------------------------------------------------

// sshBottomLines: the truth about what will happen to what you say, the prompt or
// flash, then the keys. While a human drives, the driving bar takes the first.
func (m *rootModel) sshBottomLines(w int) []string {
	s := &m.ssh
	var out []string
	c := s.conn()
	switch {
	case c != nil && c.Wheel == protocol.SSHWheelHuman && sshConnLive(c):
		out = append(out, m.sshDrivingBar(c, w)...)
	case m.sshEnded():
		out = append(out, m.sshEndedBar(c, w)...)
	default:
		out = append(out, previewLine(faintText.Render(m.sshComposerState()), w))
	}
	if s.confirmClose != "" {
		out = append(out, m.sshClosePrompt(w)...)
	} else {
		out = append(out, previewLine(m.sshPromptLine(w), w))
	}
	return append(sshFlatten(out), previewLine(m.sshKeybar(w), w))
}

func (m *rootModel) sshEnded() bool {
	s := &m.ssh
	if len(s.conns) == 0 {
		return false
	}
	for i := range s.conns {
		if sshConnLive(&s.conns[i]) {
			return false
		}
	}
	return true
}

func (m *rootModel) sshDrivingBar(c *protocol.SSHConnInfo, w int) []string {
	s := &m.ssh
	out := []string{previewLine(sshAccentBold.Render("You have the wheel.")+" "+sshAccent.Render(protocol.SSHDrivingNote+" W to hand back."), w)}
	now := sshNow()
	var running []*protocol.SSHBlock
	for i := range s.items {
		if b := s.items[i].Block; b != nil && b.Conn == c.ID && b.Status == protocol.SSHBlockRunning {
			running = append(running, b)
		}
	}
	if len(running) == 0 {
		return out
	}
	head := "1 command is still running:"
	if len(running) > 1 {
		head = fmt.Sprintf("%d commands are still running:", len(running))
	}
	out = append(out, previewLine(sshInfo.Render(head), w))
	for _, b := range running {
		out = append(out, previewLine("  "+sshLine(b.Command)+faintText.Render(" · "+sshDur(now.Sub(b.StartedAt))+"  X stop"), w))
	}
	return append(out, previewLine(faintText.Render(protocol.SSHRemoteOutlives), w))
}

func (m *rootModel) sshEndedBar(c *protocol.SSHConnInfo, w int) []string {
	s := &m.ssh
	out := []string{previewLine(faintText.Render("Connection closed. The record of what ran stays here while this session exists."), w)}
	ran := false
	for i := range s.conns {
		ran = ran || s.conns[i].CommandsRun > 0
	}
	if ran {
		out = append(out, previewLine(faintText.Render("The remote process may keep running if any were still going."), w))
	}
	if c != nil && c.Entry == protocol.SSHEntryDirect {
		out = append(out, previewLine(faintText.Render("+ new session on "+sshHostLabel(c)+" · esc, then x ends this session"), w))
	} else {
		out = append(out, previewLine(faintText.Render("A back to the agent"), w))
	}
	return out
}

// sshComposerState is section 11's table, derived from facts.
func (m *rootModel) sshComposerState() string {
	s := &m.ssh
	agentName := s.agentName()
	si := m.sessions.byID(s.session)
	if si == nil {
		return agentName + " is starting."
	}
	a, _ := axesOf(*si)
	switch {
	case a == state.AgentStarting:
		if si.Location == "ssh" {
			return agentName + " is starting."
		}
		return agentName + " is working. It gets your message at its next command."
	case a == state.AgentIdle:
		return agentName + " is waiting."
	case a == state.AgentWaitingInput:
		if si.InputReason == string(state.InputIdleNotify) {
			return agentName + " is waiting."
		}
		return agentName + " is waiting on a prompt in its own terminal. It will get this after that. A show"
	case displayOf(*si) == state.DisplayGone:
		return agentName + " has exited. Your message waits until it is back. esc, then R revive"
	}
	return agentName + " is working. It gets your message at its next command."
}

func (m *rootModel) sshPromptLine(w int) string {
	s := &m.ssh
	agentName := s.agentName()
	prompt := func(label string) string {
		out := warnText.Render(label) + faintText.Render("> ") + sshLine(s.inputBuf) + "_"
		if n := len(s.inputBuf); s.input == sshInputSay && n > protocol.SSHMaxSayBytes*8/10 {
			out += "  " + warnText.Render(fmt.Sprintf("%s of %s", sshBytes(int64(n)), sshBytes(protocol.SSHMaxSayBytes)))
		}
		return out
	}
	switch s.input {
	case sshInputSay:
		if strings.HasPrefix(s.inputBuf, "!") {
			if c := s.conn(); c != nil {
				return prompt("RUN on " + sshLine(c.Host) + " as you, not shown to " + agentName + " ")
			}
		}
		label := "talk to " + agentName + " (! runs a command)"
		if c := s.conn(); c != nil && c.Wheel == protocol.SSHWheelHuman {
			label += ". It cannot run commands while you drive"
		}
		return prompt(label)
	case sshInputDeny, sshInputDenyConn:
		return prompt("Tell " + agentName + " why (optional)")
	case sshInputDecline:
		return prompt("Why not? (optional)")
	case sshInputAnswer:
		return prompt("answer")
	case sshInputSecret:
		// The one field that never draws what is in it. This pane is captured.
		line := "answer"
		if c := s.connByID(s.inputConn); c != nil && c.Prompt != nil {
			line = sshLine(c.Prompt.Line)
		}
		return warnText.Render(line) + " " + strings.Repeat("•", utf8.RuneCountInString(s.inputBuf)) + "_"
	case sshInputFind:
		count := ""
		if s.findQuery != "" {
			if len(s.findHits) == 0 {
				count = "  Nothing here matches. Output has its own find."
			} else {
				count = fmt.Sprintf("  %d of %d", s.findAt+1, len(s.findHits))
			}
		}
		return warnText.Render("find") + faintText.Render("> ") + sshLine(s.inputBuf) + "_" + faintText.Render(count)
	}
	switch {
	case s.flash != "" && s.flashOK:
		return goodText.Render(sshLine(s.flash))
	case s.flash != "":
		return warnText.Render(sshLine(s.flash))
	case m.sessions.flash != "":
		return faintText.Render(sshLine(m.sessions.flash))
	case s.down:
		return badText.Render("The daemon is not running. What is on screen stays readable.")
	}
	return ""
}

// sshClosePrompt names what may survive, before the y.
func (m *rootModel) sshClosePrompt(w int) []string {
	s := &m.ssh
	c := s.connByID(s.confirmClose)
	if c == nil {
		return nil
	}
	out := []string{
		previewLine(warnText.Render("Close the connection to "+sshHostLabel(c)+"? (y/n)"), w),
		sshNote(faintText, "This ends your shell and closes the connection. "+s.agentName()+" will not be able to run commands here.", w),
	}
	if c.RunningBlocks > 0 {
		out = append(out, sshNote(warnText, sshCount(c.RunningBlocks, "command")+" still running. "+protocol.SSHRemoteOutlives, w))
	} else if c.CommandsRun > 0 {
		out = append(out, sshNote(faintText, protocol.SSHRemoteOutlives, w))
	}
	return out
}

func (m *rootModel) sshKeybar(w int) string {
	s := &m.ssh
	if s.input != sshInputNone {
		if s.input == sshInputFind {
			return faintText.Render("↑↓ walk matches · enter keep · esc clear")
		}
		return faintText.Render("enter send · esc cancel")
	}
	if s.confirmClose != "" {
		return warnText.Render("y") + faintText.Render(" close · ") + warnText.Render("n") + faintText.Render(" keep")
	}
	over := sshConnOver(s.conn())
	keys := []string{"i talk", "j/k move", "enter open", "W wheel", "T terminal", "F filter"}
	if over {
		keys = []string{"j/k move", "enter open", "F filter"}
	}
	if e := s.selectedElement(); e != nil {
		switch {
		case e.Kind == "proposal" && e.Outcome == nil:
			keys = append([]string{"P run", "d decline"}, keys...)
		case e.Kind == "question" && e.Outcome == nil && len(e.Choices) > 0:
			keys = append([]string{fmt.Sprintf("1-%d choose", min(6, len(e.Choices)))}, keys...)
		}
		if len(s.elementTargets(e)) > 0 {
			keys = append([]string{"e evidence"}, keys...)
		}
	}
	if b := s.selectedBlock(); b != nil && b.Status == protocol.SSHBlockRunning {
		keys = append([]string{"X stop"}, keys...)
	}
	if !over {
		keys = append(keys, "C close")
	}
	// "? help" is the way to everything that did not fit, so it never falls off.
	const tail = "? help"
	line := ""
	for _, k := range keys {
		if lipgloss.Width(line+k+" · "+tail) > w {
			break
		}
		line += k + " · "
	}
	return faintText.Render(line + tail)
}

// sshHelpLines is the overlay's own key reference: the cockpit's list would not
// mention a single one of these.
func (m *rootModel) sshHelpLines() []string {
	key := func(k, d string) string { return padTo(boxTitleHi.Render(k), 10) + faintText.Render(d) }
	return []string{
		titleStyle.Render("SSH workspace"),
		key("j/k g/G", "move"),
		key("enter", "open: a block's facts, then its output"),
		key("e", "follow a claim's evidence to the command · b back"),
		key("F", "Everything / Facts / Findings"),
		key("/", "find in this timeline"),
		"",
		key("Y / N", "the band's answer: run once, go, reconnect / deny"),
		key("t", "open a grant on an approval (staging and dev only)"),
		key("V", "revoke a live grant"),
		"",
		key("W", "take the wheel at once, or hand it back"),
		key("T", "the ssh terminal (^q back)"),
		key("A", "the agent's terminal (^q back)"),
		"",
		key("i", "talk to the agent, or answer the selected question"),
		key("a", "ask about the selected command"),
		key("S", "share a command you ran with the agent"),
		key("i !", "start a line with ! to run it on the host"),
		key("R", "recall your last queued message"),
		key("P / d", "run check / decline on a proposal"),
		key("1-6", "pick a question's choice"),
		"",
		key("X", "stop a running command"),
		key("C", "close the connection (y/n)"),
		key("< >", "switch connection"),
		key("+", "new session on a host"),
		key("o", "open an address, through lola"),
		key("esc", "close · ? closes this"),
	}
}
