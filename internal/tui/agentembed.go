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
	"github.com/sushidev-team/lola/internal/devtab"
	"github.com/sushidev-team/lola/internal/lolaenv"
	"github.com/sushidev-team/lola/internal/protocol"
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
		switch {
		case strings.HasSuffix(target, "-review"):
			title = "review" // the visible review pass's pane, not a worktree shell
		case devtab.Index(sel.ID, target) > 0:
			title = "dev · " + devTabLabel(sel, target)
		}
	}
	m.agentTerm = &termView{term: t, sessionID: sel.ID, tmuxName: target, kind: kind, title: title, w: cw, h: ch}
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

// refreshShells re-reads the tmux server for this session's auxiliary sessions:
// its "<id>-dev-N" dev tabs, its "<id>-shell-N" shells, and the "<id>-review"
// pane a visible review pass runs in (see internal/daemon/reviewvisible.go) — so
// tabs reflect shells opened anywhere (the desktop app, another lola, or here)
// and both the review a pass started and the dev processes the daemon started
// show up without the TUI being told. Best-effort: on a tmux error the
// last-known list stands.
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
	review := id + "-review" // the visible review pass's pane, if one is open
	var names, devs []string
	reviewPane := ""
	for _, s := range sessions {
		if strings.HasPrefix(s.Name, prefix) {
			names = append(names, s.Name)
			continue
		}
		if devtab.Index(id, s.Name) > 0 {
			devs = append(devs, s.Name)
			continue
		}
		if s.Name == review {
			reviewPane = s.Name
		}
	}
	sort.Slice(names, func(i, j int) bool { return shellIndex(id, names[i]) < shellIndex(id, names[j]) })
	sort.Slice(devs, func(i, j int) bool { return devtab.Index(id, devs[i]) < devtab.Index(id, devs[j]) })
	// Dev tabs sort FIRST (right after the agent): they are the project's, not
	// this session's, so their position stays put while shells come and go.
	names = append(devs, names...)
	// The review pane sorts LAST so a review starting or ending never renumbers
	// the shell tabs beside it. nextShellName's max-index scan ignores both it
	// and the dev tabs (neither carries a "-shell-N" suffix), so neither can
	// claim a shell number.
	if reviewPane != "" {
		names = append(names, reviewPane)
	}
	m.shellNames[id] = names
	if m.embedTab[id] > len(names) { // active tab outlived its shell
		m.embedTab[id] = len(names)
	}
}

// devTabLabel is the tab chip for a session's dev tab: the FIRST WORD of the
// command it runs ("composer", "npm"), which is all a TUI tab has room for and
// still tells two dev processes apart. It falls back to "dev<N>" whenever the
// command is unknown — a session record from an older daemon, or a tab left over
// from a dev_commands list that has since been shortened.
func devTabLabel(sel *protocol.SessionInfo, name string) string {
	i := devtab.Index(sel.ID, name)
	if i > 0 && i <= len(sel.DevCommands) {
		if word, _, _ := strings.Cut(strings.TrimSpace(sel.DevCommands[i-1]), " "); word != "" {
			return truncPlain(word, 10)
		}
	}
	return fmt.Sprintf("dev%d", i)
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
// dir, with the worktree's .lola/env exported — same command the agent pane and
// `lola open` use. A bare login shell here would be the odd one out: the tab
// would silently lack [[project]].env, so a project command typed in it would
// behave differently from the same command in the agent pane.
func (m *rootModel) createShellSession(name, dir string) error {
	// Built from config rather than reusing the cached read-only client: this is
	// the TUI's one session-CREATING call, and the scroll defaults it applies to
	// the server come from [tmux] (see config.TmuxClient).
	c := m.cfg.TmuxClient("", "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.NewSession(ctx, name, dir, lolaenv.ShellCommand)
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

// forwardWheel scrolls the focused embed's tmux pane, through the SAME
// (*tmux.Client).ScrollPane both surfaces use — so the TUI and the app agree on
// which history moves (the inner program's own, or tmux's copy mode) instead of
// each guessing.
//
// It deliberately does NOT write an SGR sequence into the embedded tmux CLIENT
// any more. That route only worked with `mouse on` — tmux drops mouse input from
// a client otherwise — which is why the embed needed ensureTmuxMouse and why
// scrolling died the moment [tmux].mouse was honoured as written. Addressing the
// PANE has no such dependency.
//
// Bounded and best-effort: a wheel notch must never block the UI, so it runs on
// its own goroutine with a short deadline and a failure is simply a notch that
// did nothing.
func (m *rootModel) forwardWheel(mo tea.Mouse) {
	e := m.currentEmbed()
	if e == nil {
		return
	}
	lines := wheelLines
	if mo.Button == tea.MouseWheelDown {
		lines = -lines
	}
	c, name := m.cfg.TmuxClient("", ""), e.tmuxName
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), wheelScrollTimeout)
		defer cancel()
		_, _ = c.ScrollPane(ctx, name, lines)
	}()
}

const (
	// wheelLines is how far one notch scrolls, matching the three lines a
	// terminal emulator sends per notch.
	wheelLines = 3
	// wheelScrollTimeout bounds the tmux calls behind one notch.
	wheelScrollTimeout = 2 * time.Second
)

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
