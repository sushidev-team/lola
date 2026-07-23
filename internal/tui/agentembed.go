// Live embedded agent terminal shown in the Detail panel for the SELECTED
// session. It re-targets as the selection moves (always-live): each target is a
// fresh `tmux attach` into that session's tmux, rendered in-panel. 'enter'
// focuses + expands it into the main column (the cockpit chrome stays visible,
// so it is embedded, not a full-screen takeover); Ctrl-q shrinks it back. The
// tmux session is the durable thing, so a selection change closes the attach and
// opens a new one — the agent itself keeps running regardless.
//
// The terminal is sized to the EXPANDED (focused) dimensions and kept there, so
// focusing/unfocusing never resizes the tmux session (no reflow thrash); the
// small in-panel view is just a bottom viewport of the same render.
package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/vtterm"
)

// spinnerFrames is a hand-rolled braille spinner (no bubbles dependency) for the
// "attaching…" state before the first frame arrives.
var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// embedFrameMsg repaints when the embedded agent's screen changes. gen guards
// against stale waiters after a re-target (only the current generation re-arms).
type embedFrameMsg struct{ gen int }

// spinnerTickMsg advances the loading spinner.
type spinnerTickMsg struct{}

// agentDebounceMsg fires after the selection has been still for a moment; only
// the latest token actually attaches, so fast scrolling doesn't spawn a tmux
// attach per row.
type agentDebounceMsg struct{ token int }

// agentDebounceDelay is how long the selection must settle before the live agent
// attaches.
const agentDebounceDelay = 180 * time.Millisecond

// scheduleAgentSync drops the stale embed and debounces a re-attach to the
// (soon-to-be-settled) selection's ACTIVE tab. A no-op when the right target is
// already shown.
func (m *rootModel) scheduleAgentSync() tea.Cmd {
	target, _, _ := m.activeTabTmux()
	if target == m.agentFor && (m.agentTerm != nil || target == "") {
		return nil
	}
	m.closeAgent() // clear the previous view immediately
	m.agentDebounce++
	tok := m.agentDebounce
	return tea.Tick(agentDebounceDelay, func(time.Time) tea.Msg { return agentDebounceMsg{token: tok} })
}

// activeTabTmux resolves the SELECTED session's active tab to the tmux session
// the embed should attach to: a shell name for a shell tab, else the agent's own
// tmux session. ok is false when there is nothing live to show — no selection, or
// the agent tab of a dead/terminal session.
func (m *rootModel) activeTabTmux() (name string, kind int, ok bool) {
	sel := m.sessions.selected()
	if sel == nil {
		return "", termAgent, false
	}
	names := m.shellNames[sel.ID]
	if tab := m.embedTab[sel.ID]; tab >= 1 && tab <= len(names) {
		return names[tab-1], termShell, true
	}
	if sel.TmuxName != "" && sel.Status != "dead" && sel.Status != "session_ended" {
		return sel.TmuxName, termAgent, true
	}
	return "", termAgent, false
}

func waitEmbedFrame(t *vtterm.Term, gen int) tea.Cmd {
	return func() tea.Msg {
		<-t.Frames()
		return embedFrameMsg{gen: gen}
	}
}

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

// agentLoading reports whether the embedded agent is attaching (exists, alive,
// but has not drawn its first frame yet).
func (m *rootModel) agentLoading() bool {
	return m.agentTerm != nil && !m.agentTerm.term.Exited() && len(m.agentTerm.term.Render()) == 0
}

func (m *rootModel) spinnerFrame() string {
	return string(spinnerFrames[m.spin%len(spinnerFrames)])
}

// agentSize is the FIXED terminal size: the expanded (focused) inner dimensions
// of the main column, mirroring the cockpit layout math.
func (m *rootModel) agentSize() (int, int) {
	W, H := m.width, m.height
	if W <= 0 {
		W = 100
	}
	if H <= 0 {
		H = 24
	}
	railW := 32
	if W < 104 {
		railW = 28
	}
	innerW := (W - railW - 1) - 4 // main column width minus the box border AND the one-col gutter each side
	innerH := (H - 2) - 8         // main column minus the Sessions strip, fields, borders
	if innerW < 8 {
		innerW = 8
	}
	if innerH < 6 {
		innerH = 6
	}
	return innerW, innerH
}

// syncAgentPreview makes the live embed match the selection's ACTIVE tab: the
// agent's tmux session, or a shell's — a fresh attach either way; a dead/terminal
// agent tab clears it. A no-op when already showing the right target. Returns the
// frame-wait (and, for the agent, the spinner) command.
func (m *rootModel) syncAgentPreview() tea.Cmd {
	sel := m.sessions.selected()
	target, kind, ok := m.activeTabTmux()
	if !ok {
		target = ""
	}
	if target == m.agentFor && (m.agentTerm != nil || target == "") {
		return nil
	}
	m.closeAgent()
	m.agentFor = target
	if target == "" {
		return m.armEmbed()
	}
	argv := m.sessions.tmuxClient(m.cfg.TmuxSocketName()).AttachArgs(target)
	cw, ch := m.agentSize()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "LOLA_TERMINAL=1")
	t, err := vtterm.New(cmd, cw, ch)
	if err != nil {
		m.agentFor = ""
		return m.armEmbed()
	}
	title := "agent · " + dash(sel.Issue)
	if kind == termShell {
		title = "shell"
	}
	m.agentTerm = &termView{term: t, sessionID: sel.ID, tmuxName: target, kind: kind, title: title, w: cw, h: ch}
	m.ensureTmuxMouse() // so wheel-scroll reaches the embed once focused
	cmds := []tea.Cmd{m.armEmbed()}
	if !m.spinning {
		m.spinning = true
		cmds = append(cmds, spinnerTickCmd())
	}
	return tea.Batch(cmds...)
}

// closeAgent tears down the embedded agent attach (the tmux session survives)
// and bumps the generation so any in-flight frame waiter is ignored.
func (m *rootModel) closeAgent() {
	if m.agentTerm != nil {
		_ = m.agentTerm.term.Close()
		m.agentTerm = nil
	}
	m.embedFocused = false
	m.embedGen++
}

// currentEmbed is the terminal shown in the Detail panel for the selection: the
// single live embed attach (agent or shell), as long as it belongs to the
// selected session. Its kind drives the Shell/Agent label and the tab bar.
func (m *rootModel) currentEmbed() *termView {
	sel := m.sessions.selected()
	if sel == nil {
		return nil
	}
	if m.agentTerm != nil && m.agentTerm.sessionID == sel.ID {
		return m.agentTerm
	}
	return nil
}

// armEmbed (re)starts the repaint waiter for the current embed, bumping the
// generation so any stale waiter (from a previous embed) is ignored.
func (m *rootModel) armEmbed() tea.Cmd {
	m.embedGen++
	if e := m.currentEmbed(); e != nil {
		return waitEmbedFrame(e.term, m.embedGen)
	}
	return nil
}

// resizeEmbed re-sizes the live embed attach to the current window.
func (m *rootModel) resizeEmbed() {
	if m.agentTerm == nil {
		return
	}
	w, h := m.agentSize()
	m.agentTerm.w, m.agentTerm.h = w, h
	m.agentTerm.term.Resize(w, h)
}

// focusEmbed expands + focuses whatever the Detail panel is showing (agent or
// shell) so keystrokes flow to it.
func (m *rootModel) focusEmbed() (tea.Model, tea.Cmd) {
	e := m.currentEmbed()
	if e == nil || e.term.Exited() {
		m.sessions.flash, m.sessions.flashGood = "no live terminal for this session", false
		return m, nil
	}
	m.embedFocused = true
	return m, nil
}

// newShell opens ANOTHER worktree shell for the selected session as a new tmux
// session ("<id>-shell-N"), makes it the active tab, and focuses it. There is no
// per-session limit — each press adds another. Because the shell is a tmux
// session on the shared lola server, the desktop app (which discovers the same
// sessions) shows it as a tab too, and vice versa.
func (m *rootModel) newShell() (tea.Model, tea.Cmd) {
	sel := m.sessions.selected()
	if sel == nil {
		return m, nil
	}
	if sel.Worktree == "" {
		m.sessions.flash, m.sessions.flashGood = "no worktree for this session", false
		return m, nil
	}
	name := m.nextShellName(sel.ID)
	if err := m.createShellSession(name, sel.Worktree); err != nil {
		m.sessions.flash, m.sessions.flashGood = "shell failed: "+err.Error(), false
		return m, nil
	}
	m.refreshShells(sel.ID)
	if m.embedTab == nil {
		m.embedTab = map[string]int{}
	}
	for i, n := range m.shellNames[sel.ID] {
		if n == name {
			m.embedTab[sel.ID] = i + 1
			break
		}
	}
	m.embedFocused = true
	return m, m.syncAgentPreview()
}

// cycleEmbedTab moves the selected session's active Detail tab across
// {agent, shell1, shell2, …}, wrapping. dir +1 next, -1 previous. Re-discovers
// first so shells opened elsewhere (the app) are included. A no-op with no shells.
func (m *rootModel) cycleEmbedTab(dir int) (tea.Model, tea.Cmd) {
	sel := m.sessions.selected()
	if sel == nil {
		return m, nil
	}
	m.refreshShells(sel.ID)
	n := len(m.shellNames[sel.ID])
	if n == 0 {
		return m, nil
	}
	if m.embedTab == nil {
		m.embedTab = map[string]int{}
	}
	m.embedTab[sel.ID] = cycleTabIndex(m.embedTab[sel.ID], n, dir)
	return m, m.syncAgentPreview()
}

// closeActiveShell kills the shell on the active tab (a no-op on the agent tab),
// mirroring the desktop tab's "×", and falls the tab back to its left neighbour.
func (m *rootModel) closeActiveShell() (tea.Model, tea.Cmd) {
	sel := m.sessions.selected()
	if sel == nil {
		return m, nil
	}
	tab := m.embedTab[sel.ID]
	names := m.shellNames[sel.ID]
	if tab < 1 || tab > len(names) {
		return m, nil // on the agent tab — nothing to close
	}
	name := names[tab-1]
	if m.agentTerm != nil && m.agentTerm.tmuxName == name {
		m.closeAgent() // drop our attach before killing the session
	}
	m.killShellSession(name)
	m.embedTab[sel.ID] = tab - 1 // fall back to the left tab (agent if it was first)
	m.refreshShells(sel.ID)
	m.sessions.flash, m.sessions.flashGood = "shell closed", false
	return m, m.syncAgentPreview()
}

// --- shell tmux sessions (shared with the desktop app) ----------------------

// refreshShells re-reads the tmux server for this session's "<id>-shell-N"
// sessions, so tabs reflect shells opened anywhere — the desktop app, another
// lola, or here. Best-effort: on a tmux error the last-known list stands.
func (m *rootModel) refreshShells(id string) {
	if m.shellNames == nil {
		m.shellNames = map[string][]string{}
	}
	c := m.sessions.tmuxClient(m.cfg.TmuxSocketName())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sessions, err := c.ListSessions(ctx)
	if err != nil {
		return
	}
	prefix := id + "-shell-"
	var names []string
	for _, s := range sessions {
		if strings.HasPrefix(s.Name, prefix) {
			names = append(names, s.Name)
		}
	}
	sort.Slice(names, func(i, j int) bool { return shellIndex(id, names[i]) < shellIndex(id, names[j]) })
	m.shellNames[id] = names
	if m.embedTab[id] > len(names) { // active tab outlived its shell
		m.embedTab[id] = len(names)
	}
}

// nextShellName picks the next free "<id>-shell-N" (max existing index + 1),
// discovering first so it never collides with a shell opened in the app.
func (m *rootModel) nextShellName(id string) string {
	m.refreshShells(id)
	max := 0
	for _, n := range m.shellNames[id] {
		if i := shellIndex(id, n); i > max {
			max = i
		}
	}
	return fmt.Sprintf("%s-shell-%d", id, max+1)
}

// createShellSession spawns a detached tmux session running the default shell in
// dir. Empty command → the user's login shell, mirroring internal/tmux.NewSession.
func (m *rootModel) createShellSession(name, dir string) error {
	c := m.sessions.tmuxClient(m.cfg.TmuxSocketName())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.NewSession(ctx, name, dir, "")
}

// killShellSession terminates one shell tmux session (best-effort, idempotent).
func (m *rootModel) killShellSession(name string) {
	c := m.sessions.tmuxClient(m.cfg.TmuxSocketName())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = c.KillSession(ctx, name)
}

// closeSessionShells kills every shell tmux session for one lola session — used
// on kill, where the worktree is about to go, so the shells rooted there must too.
func (m *rootModel) closeSessionShells(id string) {
	if m.agentTerm != nil && m.agentTerm.sessionID == id && m.agentTerm.kind == termShell {
		m.closeAgent()
	}
	m.refreshShells(id)
	for _, name := range m.shellNames[id] {
		m.killShellSession(name)
	}
	delete(m.shellNames, id)
	delete(m.embedTab, id)
}

// handleEmbedKey routes a keystroke while the embed is FOCUSED: Ctrl-q unfocuses
// back to the cockpit (the terminal keeps running); Ctrl-g toggles select-mode
// (release the mouse for native selection/copy and ⌘-click — off by default so
// the wheel is captured and forwarded to the agent, see View); everything else
// is forwarded to whatever is shown (agent or shell).
func (m *rootModel) handleEmbedKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+q" {
		m.embedFocused = false
		return m, nil
	}
	if k.String() == "ctrl+g" {
		m.embedSelect = !m.embedSelect
		return m, nil
	}
	if e := m.currentEmbed(); e != nil {
		if b := keyToBytes(k); len(b) > 0 {
			e.term.Write(b)
		}
	}
	return m, nil
}

// forwardWheel encodes a mouse-wheel event as an SGR mouse sequence and sends it
// to the focused embed so the inner app (Claude Code's history, tmux copy-mode)
// scrolls. Coordinates are translated to the embed's own grid. This only takes
// effect when the agent's tmux has `mouse on` (ensureTmuxMouse enables it) and
// the inner app handles the wheel.
func (m *rootModel) forwardWheel(mo tea.Mouse) {
	e := m.currentEmbed()
	if e == nil {
		return
	}
	btn := 64 // SGR wheel-up
	if mo.Button == tea.MouseWheelDown {
		btn = 65
	}
	col, row := m.embedMouseCoord(mo.X, mo.Y)
	e.term.Write([]byte(fmt.Sprintf("\x1b[<%d;%d;%dM", btn, col, row)))
}

// embedMouseCoord maps a screen mouse position to the focused embed's 1-based
// grid, clamped in-bounds (mirrors the focused mainColumn layout offsets).
func (m *rootModel) embedMouseCoord(x, y int) (int, int) {
	W := m.width
	if W <= 0 {
		W = 100
	}
	railW := 32
	if W < 104 {
		railW = 28
	}
	const panelTop = 6 // vitals(1) + Sessions strip(4) + top border(1)
	panelLeft := railW + 2
	w, h := m.agentSize()
	return clampInt(x-panelLeft+1, 1, w), clampInt(y-panelTop+1, 1, h)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ensureTmuxMouse enables `mouse on` on the lola tmux server once, best-effort,
// so wheel events forwarded to an embedded agent actually reach the inner app.
func (m *rootModel) ensureTmuxMouse() {
	if m.tmuxMouseSet {
		return
	}
	m.tmuxMouseSet = true
	bin := os.Getenv("TMUX_BIN")
	if bin == "" {
		bin = "tmux"
	}
	sock := m.cfg.TmuxSocketName()
	// mouse on: tmux processes wheel events from the client.
	_ = exec.Command(bin, "-L", sock, "set-option", "-g", "mouse", "on").Run()
	// alternate-scroll off: do NOT translate the wheel into arrow keys for
	// alt-screen apps (that recalls input history instead of scrolling) — forward
	// the real wheel so the inner app (Claude) can scroll its own history.
	_ = exec.Command(bin, "-L", sock, "set-option", "-g", "alternate-scroll", "off").Run()
}

// handleEmbedPaste forwards pasted text to the focused embed as a BRACKETED
// paste, so the child (agent / vim) treats it as one paste rather than
// keystrokes that submit on the first newline. bubbletea v2 delivers paste as a
// separate tea.PasteMsg, which the key encoder never sees — this is why pasting
// otherwise did nothing.
func (m *rootModel) handleEmbedPaste(content string) (tea.Model, tea.Cmd) {
	if content == "" {
		return m, nil
	}
	if e := m.currentEmbed(); e != nil {
		e.term.Write([]byte("\x1b[200~" + content + "\x1b[201~"))
	}
	return m, nil
}

// embedBody renders the shown embed into the Detail panel: a spinner while an
// agent is attaching, a note if it ended, otherwise the BOTTOM h rows of its
// screen (a viewport — the small panel shows the tail, the focused/expanded
// panel shows it all).
func (m *rootModel) embedBody(e *termView, w, h int) []string {
	if e.kind == termAgent && m.agentLoading() {
		return []string{"", "  " + faintText.Render(m.spinnerFrame()+" attaching to agent…")}
	}
	if e.term.Exited() {
		return []string{"", "  " + faintText.Render("terminal ended")}
	}
	lines := e.term.Render()
	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	out := make([]string, len(lines))
	for i, ln := range lines {
		out[i] = previewLine(ln, w)
	}
	return out
}
