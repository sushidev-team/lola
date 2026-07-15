// Package tui implements the interactive poll manager (lola / lola tui) and
// the plain socket client used by the other CLI subcommands.
package tui

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/doctor"
	"github.com/sushidev-team/lola/internal/protocol"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Underline(true)
	selStyle   = lipgloss.NewStyle().Reverse(true)
)

// Tabs of the root view (P1.8): the poll manager and the read-only
// sessions observer.
const (
	tabPolls = iota
	tabSessions
)

type rootModel struct {
	cfgPath  string
	cfg      *config.Config
	tab      int // legacy tab index; superseded by the unified cockpit (focus)
	focus    int // cockpit: which panel owns navigation/action keys (focusSessions/focusPolls)
	list     listModel
	sessions sessionsModel
	form     *formModel
	projForm *projectForm         // project editor modal ('P'); nil otherwise
	terms    map[string]*termView // per-session persistent shells, keyed by session ID

	// The embedded terminal shown in the Detail panel for the selected session:
	// the live AGENT (a tmux attach, re-targeted as the selection moves) by
	// default, or a per-session SHELL when showShell is set. 'enter' focuses +
	// expands whichever is shown into the main column; Ctrl-q shrinks it back.
	// currentEmbed() resolves which one; embedGen guards the repaint waiter.
	agentTerm     *termView
	agentFor      string // session ID agentTerm is attached to ("" = none)
	showShell     bool   // Detail shows the session's shell instead of the agent
	embedFocused  bool   // the shown embed has keyboard + is expanded
	embedGen      int    // generation, bumped on re-target so stale frame waiters are ignored
	agentDebounce int    // debounce token; only the latest selection change attaches
	spin          int    // braille spinner frame, advanced while a terminal is loading
	spinning      bool   // a spinner tick loop is active
	tmuxMouseSet  bool   // `mouse on` has been enabled on the lola tmux server

	width  int
	height int

	// attnHist is a bounded ring of recent "need you" counts (one sample per
	// sessions fetch), rendered as the Triage sparkline so the queue's trend is
	// visible at a glance.
	attnHist []int

	// Doctor overlay (P6.27): 'd' in the polls view runs doctor.Check via a
	// bounded tea.Cmd and shows the report in a scrollable panel; esc closes.
	doctorLoading bool
	doctorReport  *doctor.Report
	doctorScroll  int
}

// Run opens the interactive TUI (poll list + cascading edit form). On first
// run — when no config.toml exists yet — it enters the setup wizard first and
// only falls through into the poll list once the wizard has written a config.
func Run() error {
	cfgPath, err := config.DefaultPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(cfgPath); errors.Is(err, fs.ErrNotExist) {
		wrote, err := runSetupWizard(newSetupModel())
		if err != nil {
			return err
		}
		if !wrote {
			return nil // esc before write: nothing to open
		}
		// fall through into the normal TUI with the freshly written config
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	m := &rootModel{cfgPath: cfgPath, cfg: cfg, list: newListModel(cfg), height: 24}
	_, err = tea.NewProgram(m).Run() // alt-screen is set on the View (bubbletea v2)
	return err
}

// ---- messages / commands ----

type statusMsg struct {
	data *protocol.StatusData
	err  error
}

type statusTickMsg struct{}

type opDoneMsg struct{ err error }

// doctorMsg carries a completed doctor.Check report back to the UI.
type doctorMsg struct{ report doctor.Report }

// runDoctorCmd runs the health checks off the UI thread under a bounded
// context (doctor already bounds each subprocess; this caps the whole run).
func runDoctorCmd(cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return doctorMsg{report: doctor.Check(ctx, cfg)}
	}
}

func fetchStatusCmd() tea.Msg {
	resp, err := request(protocol.Request{Cmd: "status"})
	if err != nil {
		return statusMsg{err: err}
	}
	if !resp.OK {
		return statusMsg{err: errors.New(resp.Error)}
	}
	var d protocol.StatusData
	if err := json.Unmarshal(resp.Data, &d); err != nil {
		return statusMsg{err: err}
	}
	return statusMsg{data: &d}
}

func statusTick() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return statusTickMsg{} })
}

// bestEffortReloadCmd asks the daemon to re-read config; a down daemon is
// not an error (it will read the fresh config on next start).
func bestEffortReloadCmd() tea.Msg {
	resp, err := request(protocol.Request{Cmd: "reload"})
	if err != nil {
		return opDoneMsg{}
	}
	if !resp.OK {
		return opDoneMsg{err: errors.New("reload: " + resp.Error)}
	}
	return opDoneMsg{}
}

// ---- tea.Model ----

func (m *rootModel) Init() tea.Cmd {
	return tea.Batch(fetchStatusCmd, statusTick())
}

func (m *rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		m.resizeEmbed()
		return m, nil
	case statusMsg:
		if v.err != nil {
			m.list.status = nil
			m.list.statusErr = ""
			if !errors.Is(v.err, errDaemonDown) {
				m.list.statusErr = v.err.Error()
			}
		} else {
			m.list.status, m.list.statusErr = v.data, ""
		}
		return m, nil
	case statusTickMsg:
		m.sweepTerms() // reap any detached shell whose process has exited
		if m.form != nil {
			return m, statusTick()
		}
		cmds := []tea.Cmd{fetchStatusCmd, statusTick()}
		// Sessions are always on screen in the cockpit, so refresh them every
		// tick — EXCEPT while a kill confirmation, answer card, or filter bar is
		// open: a mid-interaction refresh could reorder/prune rows under the
		// cursor (the kill target is pinned by ID regardless, but the frozen view
		// keeps the prompt and the highlighted row in agreement).
		if !m.sessions.confirmKill && !m.sessions.answering && !m.sessions.filtering {
			cmds = append(cmds, fetchSessionsCmd)
			if c := m.paneRefreshCmd(); c != nil {
				cmds = append(cmds, c)
			}
		}
		return m, tea.Batch(cmds...)
	case sessionsMsg:
		before := m.effectiveSelID()
		cmd := m.handleSessionsMsg(v)
		m.recordAttn()
		if m.effectiveSelID() != before { // selection (re)pinned → re-target the live agent
			if c := m.scheduleAgentSync(); c != nil {
				cmd = tea.Batch(cmd, c)
			}
		}
		return m, cmd
	case paneMsg:
		m.handlePaneMsg(v)
		return m, nil
	case answerDoneMsg:
		// Surface the daemon's verdict: a green "answer sent", or the verbatim
		// refusal/dial error. Then refresh the list and pane so the resumed
		// (or still-stuck) session re-derives.
		m.sessions.flash, m.sessions.flashGood = v.msg, v.ok
		cmds := []tea.Cmd{fetchSessionsCmd}
		if c := m.paneRefreshCmd(); c != nil {
			cmds = append(cmds, c)
		}
		return m, tea.Batch(cmds...)
	case killDoneMsg:
		// Flash the outcome verbatim (success line or the daemon's dirty-kept
		// message) and refresh the list so a removed session drops out.
		m.sessions.flash = v.msg
		return m, fetchSessionsCmd
	case doctorMsg:
		// A report arriving after the overlay was closed (esc during the run)
		// is dropped — doctorLoading is the "still open" signal.
		if m.doctorLoading {
			r := v.report
			m.doctorLoading, m.doctorReport, m.doctorScroll = false, &r, 0
		}
		return m, nil
	case tea.KeyPressMsg:
		// ctrl+c quits — EXCEPT while the embed is focused, where it is forwarded
		// to the terminal (interrupt) via the embed-key routing below.
		if v.String() == "ctrl+c" && !m.embedFocused {
			m.closeAllTerms()
			return m, tea.Quit
		}
	case embedFrameMsg:
		if v.gen != m.embedGen {
			return m, nil // stale waiter from a previous embed
		}
		e := m.currentEmbed()
		if e == nil {
			return m, nil
		}
		if e.term.Exited() {
			if e.kind == termAgent {
				m.closeAgent()
			} else {
				m.reapTerm(e, "shell exited")
				m.showShell = false
			}
			return m, m.armEmbed() // fall back to the agent, if any
		}
		return m, waitEmbedFrame(e.term, m.embedGen)
	case spinnerTickMsg:
		m.spin++
		if m.agentLoading() {
			return m, spinnerTickCmd()
		}
		m.spinning = false
		return m, nil
	case agentDebounceMsg:
		if v.token != m.agentDebounce {
			return m, nil // superseded by a newer selection change
		}
		return m, m.syncAgentPreview()
	case tea.PasteMsg:
		if m.embedFocused {
			return m.handleEmbedPaste(v.Content)
		}
	case tea.MouseWheelMsg:
		if m.embedFocused {
			m.forwardWheel(v.Mouse())
		}
		return m, nil
	}

	// The focused embed owns every keystroke (Ctrl-q unfocuses).
	if m.embedFocused {
		if k, ok := msg.(tea.KeyPressMsg); ok {
			return m.handleEmbedKey(k)
		}
		return m, nil
	}

	// The project editor owns all input while open.
	if m.projForm != nil {
		if k, ok := msg.(tea.KeyPressMsg); ok {
			switch m.projForm.update(k) {
			case projFormCancel:
				m.projForm = nil
			case projFormSaved:
				m.projForm = nil
				m.reloadConfig()
				m.list.flash = "project saved"
				return m, tea.Batch(bestEffortReloadCmd, fetchStatusCmd)
			}
		}
		return m, nil
	}

	// The doctor overlay owns all input while open (loading or showing).
	if m.doctorLoading || m.doctorReport != nil {
		if k, ok := msg.(tea.KeyPressMsg); ok {
			return m.doctorKey(k)
		}
		return m, nil
	}

	if m.form != nil {
		cmd, ev := m.form.update(msg)
		switch ev {
		case formCancel:
			m.form = nil
			return m, fetchStatusCmd
		case formSaved:
			m.form = nil
			m.reloadConfig()
			m.list.flash = "poll saved"
			return m, tea.Batch(bestEffortReloadCmd, fetchStatusCmd)
		}
		return m, cmd
	}
	// Cockpit key routing. Global keys (focus cycle, doctor) fire unless a modal
	// gate currently owns keystrokes — a poll delete / session kill confirmation,
	// the answer card, or the filter bar (whose keys may be "tab"/"d"/digits).
	gated := m.list.confirmDelete || m.sessions.confirmKill || m.sessions.answering || m.sessions.filtering
	if k, ok := msg.(tea.KeyPressMsg); ok && !gated {
		switch k.String() {
		case "tab":
			if m.focus == focusSessions {
				m.focus = focusPolls
			} else {
				m.focus = focusSessions
			}
			return m, nil
		case "d":
			m.doctorLoading, m.doctorScroll = true, 0
			return m, runDoctorCmd(m.cfg)
		case "P":
			return m.openProjectForm()
		}
	}
	if m.focus == focusPolls {
		return m.updateList(msg)
	}
	// Route to the sessions view, then re-target the live agent if the selection
	// moved (arrow keys, jumps, filter changes).
	before := m.effectiveSelID()
	model, cmd := m.updateSessions(msg)
	if m.effectiveSelID() != before {
		if c := m.scheduleAgentSync(); c != nil {
			cmd = tea.Batch(cmd, c)
		}
	}
	return model, cmd
}

// tabBar is the shared header line; the active tab is highlighted.
func (m *rootModel) tabBar() string {
	polls, sessions := "1:polls", "2:sessions"
	if m.tab == tabSessions {
		return "lola  " + faintText.Render(polls) + "  " + titleStyle.Render(sessions)
	}
	return "lola  " + titleStyle.Render(polls) + "  " + faintText.Render(sessions)
}

// View wraps the rendered frame string in a tea.View (bubbletea v2) and enables
// the alt-screen there (WithAltScreen was removed as a Program option). When an
// embedded terminal is attached, the real cursor is placed at the child's
// cursor (offset by the title-bar row); otherwise it stays hidden (the cockpit
// has no text cursor).
func (m *rootModel) View() tea.View {
	v := tea.NewView(m.viewString())
	v.AltScreen = true
	// While an embed is focused, capture the mouse so wheel-scroll can be
	// forwarded to it (the cockpit itself is keyboard-driven).
	if m.embedFocused {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

func (m *rootModel) viewString() string {
	if m.doctorLoading || m.doctorReport != nil {
		return m.doctorModal()
	}
	if m.projForm != nil {
		return m.projectFormModal()
	}
	if m.form != nil {
		// The poll edit form floats as a modal over the cockpit. (The first-run
		// setup wizard runs standalone before the cockpit exists, so it has no
		// backdrop to float over and stays full-screen in runSetupWizard.)
		return m.formModal()
	}
	return m.cockpitView()
}

// ---- list behavior ----

func (m *rootModel) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case metaMsg:
		if v.err != nil {
			m.list.flash = "refresh failed: " + v.err.Error()
		} else {
			for _, t := range v.meta.Teams {
				m.list.teamNames[t.ID] = t.Key + " — " + t.Name
			}
			m.list.flash = "linear cache refreshed"
		}
		return m, nil
	case opDoneMsg:
		if v.err != nil {
			m.list.flash = v.err.Error()
		}
		// Socket ops (enable/disable) make the daemon rewrite config.toml;
		// refresh our snapshot so later saves don't clobber those changes.
		m.reloadConfig()
		return m, fetchStatusCmd
	case tea.KeyPressMsg:
		return m.listKey(v)
	}
	return m, nil
}

func (m *rootModel) listKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	l := &m.list
	if l.confirmDelete {
		l.confirmDelete = false
		if s := k.String(); s == "y" || s == "Y" {
			return m, m.deleteSelected()
		}
		return m, nil
	}
	l.flash = ""

	switch k.String() {
	case "q":
		m.closeAllTerms()
		return m, tea.Quit
	case "up", "k":
		if l.cursor > 0 {
			l.cursor--
		}
	case "down", "j":
		if l.cursor < len(m.cfg.Polls)-1 {
			l.cursor++
		}
	case "n":
		f, cmd := newFormModel(m.cfg, nil)
		m.form = f
		return m, cmd
	case "enter", "e":
		if p := m.selectedPoll(); p != nil {
			f, cmd := newFormModel(m.cfg, p)
			m.form = f
			return m, cmd
		}
	case "x":
		if m.selectedPoll() != nil {
			l.confirmDelete = true
		}
	case "d":
		m.doctorLoading, m.doctorScroll = true, 0
		return m, runDoctorCmd(m.cfg)
	case " ":
		return m, m.toggleSelected()
	case "r":
		if p := m.selectedPoll(); p != nil && p.TeamID != "" {
			l.flash = "refreshing linear cache…"
			return m, loadMetaCmd(m.cfg, p.TeamID, true)
		}
	}
	return m, nil
}

func (m *rootModel) selectedPoll() *config.Poll {
	if m.list.cursor < 0 || m.list.cursor >= len(m.cfg.Polls) {
		return nil
	}
	return &m.cfg.Polls[m.list.cursor]
}

func (m *rootModel) deleteSelected() tea.Cmd {
	p := m.selectedPoll()
	if p == nil {
		return nil
	}
	name := p.Name
	// Re-read config.toml first: the daemon (enable/disable) may have
	// persisted changes since our snapshot; saving the stale copy would
	// silently revert them.
	m.reloadConfig()
	idx := -1
	for i := range m.cfg.Polls {
		if m.cfg.Polls[i].Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		m.list.flash = "poll already gone"
		return bestEffortReloadCmd
	}
	m.cfg.Polls = append(m.cfg.Polls[:idx], m.cfg.Polls[idx+1:]...)
	if m.list.cursor >= len(m.cfg.Polls) && m.list.cursor > 0 {
		m.list.cursor--
	}
	if err := m.cfg.Save(m.cfgPath); err != nil {
		m.list.flash = "save failed: " + err.Error()
		return nil
	}
	m.list.flash = "poll deleted"
	return bestEffortReloadCmd
}

// toggleSelected pauses/resumes via the socket when the daemon is up;
// otherwise it flips enabled in config directly (save + best-effort reload).
func (m *rootModel) toggleSelected() tea.Cmd {
	p := m.selectedPoll()
	if p == nil {
		return nil
	}
	if m.list.status != nil {
		enabled := p.Enabled
		if ps := m.list.pollStatus(p.Name); ps != nil {
			enabled = ps.Enabled
		}
		verb := "enable"
		if enabled {
			verb = "disable"
		}
		name := p.Name
		return func() tea.Msg {
			resp, err := request(protocol.Request{Cmd: verb, Poll: name})
			if err != nil {
				return opDoneMsg{err: err}
			}
			if !resp.OK {
				return opDoneMsg{err: errors.New(resp.Error)}
			}
			return opDoneMsg{}
		}
	}
	// Daemon down: flip in config directly — but rebase on the on-disk
	// config first so we don't clobber changes persisted since our snapshot.
	name := p.Name
	m.reloadConfig()
	fp := m.cfg.PollByName(name)
	if fp == nil {
		m.list.flash = "poll no longer exists"
		return nil
	}
	fp.Enabled = !fp.Enabled
	if err := m.cfg.Save(m.cfgPath); err != nil {
		m.list.flash = "save failed: " + err.Error()
		return nil
	}
	return bestEffortReloadCmd
}

// ---- doctor overlay ----

// doctorKey drives the doctor panel: esc/q close it, the arrows/j/k scroll.
func (m *rootModel) doctorKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "q":
		m.doctorLoading, m.doctorReport, m.doctorScroll = false, nil, 0
	case "up", "k":
		if m.doctorScroll > 0 {
			m.doctorScroll--
		}
	case "down", "j":
		m.doctorScroll++ // clamped against the window in doctorModal
	}
	return m, nil
}

// doctorReportLines renders each Result as an aligned "<glyph> <name> <detail>"
// row using the shared TUI styles. The Linear key value never reaches a Result,
// so nothing here can leak it.
func doctorReportLines(rep doctor.Report) []string {
	nameW := 0
	for _, r := range rep.Results {
		if w := lipgloss.Width(r.Name); w > nameW {
			nameW = w
		}
	}
	lines := make([]string, 0, len(rep.Results))
	for _, r := range rep.Results {
		var glyph string
		switch {
		case r.OK:
			glyph = goodText.Render("✓")
		case r.Critical:
			glyph = badText.Render("✗")
		default:
			glyph = warnText.Render("⚠")
		}
		pad := strings.Repeat(" ", nameW-lipgloss.Width(r.Name))
		lines = append(lines, glyph+"  "+r.Name+pad+"  "+r.Detail)
	}
	return lines
}

func (m *rootModel) reloadConfig() {
	if cfg, err := config.Load(m.cfgPath); err == nil {
		m.cfg = cfg
		m.list.teamNames = teamNamesFromCache(cfg)
	}
	if m.list.cursor >= len(m.cfg.Polls) && m.list.cursor > 0 {
		m.list.cursor = len(m.cfg.Polls) - 1
	}
}
