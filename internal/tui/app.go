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

// Top-level screens in the navigation model: the COCKPIT (global or
// project-scoped sessions) and the project-list HOME. Overlays (project/
// settings/poll forms, doctor) float over whichever is active. viewCockpit is
// the zero value so an unset rootModel (tests) keeps the pre-home behavior;
// Run() explicitly opens on viewHome.
const (
	viewCockpit = iota
	viewHome
	viewDetail
	viewPRPicker
	viewTicketPicker
)

type rootModel struct {
	cfgPath  string
	cfg      *config.Config
	view     int // viewCockpit | viewHome | viewDetail — the active top-level screen
	tab      int // legacy tab index; superseded by the unified cockpit (focus)
	focus    int // cockpit: which panel owns navigation/action keys (focusSessions/focusPolls)
	home     homeModel
	detail   detailModel
	prpick   prPickerModel
	ticket   ticketPickerModel
	list     listModel
	sessions sessionsModel
	form     *formModel
	settings *settingsForm        // global settings editor modal ('S'); nil otherwise
	terms    map[string]*termView // per-session persistent shells, keyed by session ID

	// The embedded terminal shown in the Detail panel for the selected session:
	// the live AGENT (a tmux attach, re-targeted as the selection moves) by
	// default, or a per-session SHELL when showShell is set. 'enter' focuses +
	// expands whichever is shown into the main column; Ctrl-q shrinks it back.
	// currentEmbed() resolves which one; embedGen guards the repaint waiter.
	agentTerm    *termView
	agentFor     string // session ID agentTerm is attached to ("" = none)
	showShell    bool   // Detail shows the session's shell instead of the agent
	embedFocused bool   // the shown embed has keyboard + is expanded
	embedSelect  bool   // select-mode (opt-in, Ctrl-g): release the mouse to the outer
	//                      terminal for native drag-select/copy and ⌘-click-to-open. OFF by
	//                      default so the wheel is captured and forwarded to the agent.
	embedGen      int  // generation, bumped on re-target so stale frame waiters are ignored
	agentDebounce int  // debounce token; only the latest selection change attaches
	spin          int  // braille spinner frame, advanced while a terminal is loading
	spinning      bool // a spinner tick loop is active
	tmuxMouseSet  bool // `mouse on` has been enabled on the lola tmux server

	width  int
	height int

	// Doctor overlay (P6.27): 'd' in the polls view runs doctor.Check via a
	// bounded tea.Cmd and shows the report in a scrollable panel; esc closes.
	doctorLoading bool
	doctorReport  *doctor.Report
	doctorScroll  int

	// daemonOp is the in-flight lifecycle transition ("starting"/"stopping"/
	// "restarting"), shown in the message line while a ^r/^x/auto-start op runs;
	// cleared when its daemonOpMsg arrives. Only set in self-managed mode.
	daemonOp string
}

// routePaste delivers pasted text to whatever currently owns keyboard input,
// in the SAME precedence as the keystroke path in Update: the focused embed,
// then the modal overlays, then the inline prompts of the active screen. A
// paste with no text field focused is dropped.
//
// This exists because bubbletea v2 emits a bracketed paste as tea.PasteMsg
// rather than as key events, so a field that only reads tea.KeyPressMsg cannot
// see it. Anything new that accepts typed input needs a case here too.
func (m *rootModel) routePaste(content string) (tea.Model, tea.Cmd) {
	if content == "" {
		return m, nil
	}
	if m.embedFocused {
		return m.handleEmbedPaste(content)
	}
	switch {
	case m.form != nil:
		m.form.paste(content)
	case m.settings != nil:
		m.settings.paste(content)
	case m.doctorLoading || m.doctorReport != nil:
		// read-only overlay
	case m.view == viewDetail && m.detail.wtMode:
		m.detail.wtBranch += pasteInline(content)
	case m.view == viewHome && m.home.adding:
		m.home.addInput += pasteInline(content)
	case m.view == viewHome && m.home.filtering:
		m.home.filter += pasteInline(content)
		m.home.repin(m.cfg)
	}
	return m, nil
}

// manageDaemon reports whether the TUI owns the daemon lifecycle (auto-start,
// ^r restart, ^x stop). Off when [defaults].manage_daemon = false (launchd owns
// it), so the TUI never fights an external supervisor.
func (m *rootModel) manageDaemon() bool {
	return m.cfg == nil || m.cfg.AutoManageDaemon()
}

// daemonDownHint is the parenthetical shown after the daemon-down banner: in
// self-managed mode it points at the restart key (auto-start already tried and
// failed); otherwise at the external supervisor.
func (m *rootModel) daemonDownHint() string {
	if m.manageDaemon() {
		return "  (^r to start)"
	}
	return "  (start with: lola run)"
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
	// The main screen is the cockpit — all sessions (unscoped) plus the poll/
	// project rail, exactly as before the project-centric restructure. The
	// project list (home) and the pickers are drill-ins reached from it (p).
	m := &rootModel{cfgPath: cfgPath, cfg: cfg, view: viewCockpit, home: newHomeModel(), list: newListModel(cfg), height: 24}
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
	cmds := []tea.Cmd{fetchStatusCmd, fetchProjectsCmd, statusTick()}
	// Self-managed lifecycle: if no daemon is answering the socket, silently
	// bring one up on open. A live (or launchd-managed) daemon is left alone.
	if m.manageDaemon() {
		m.daemonOp = "starting"
		cmds = append(cmds, ensureDaemonCmd)
	}
	return tea.Batch(cmds...)
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
	case projectsMsg:
		m.handleProjectsMsg(v)
		return m, nil
	case prsMsg:
		m.handlePrsMsg(v)
		return m, nil
	case ticketsMsg:
		m.handleTicketsMsg(v)
		return m, nil
	case statusTickMsg:
		m.sweepTerms() // reap any detached shell whose process has exited
		if m.form != nil {
			return m, statusTick()
		}
		cmds := []tea.Cmd{fetchStatusCmd, statusTick()}
		// Refresh the project decoration every tick unless the home filter/add
		// prompt is mid-edit (a reflow would fight the input).
		if !m.home.filtering && !m.home.adding {
			cmds = append(cmds, fetchProjectsCmd)
		}
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
	case reviveDoneMsg:
		// Flash the outcome (green on a successful relaunch) and refresh so the
		// revived session re-renders as working.
		m.sessions.flash, m.sessions.flashGood = v.msg, v.good
		return m, fetchSessionsCmd
	case coderabbitDoneMsg:
		// Flash the CodeRabbit poll outcome; refresh so any status the routed
		// feedback nudged (e.g. a hand-off waking the agent) re-derives.
		m.sessions.flash, m.sessions.flashGood = v.msg, v.ok
		return m, fetchSessionsCmd
	case openDoneMsg:
		// Flash the manual-open outcome (green when the branch/PR checked out) and
		// refresh so the new shell session shows up in the list.
		m.sessions.flash, m.sessions.flashGood = v.msg, v.ok
		return m, fetchSessionsCmd
	case doctorMsg:
		// A report arriving after the overlay was closed (esc during the run)
		// is dropped — doctorLoading is the "still open" signal.
		if m.doctorLoading {
			r := v.report
			m.doctorLoading, m.doctorReport, m.doctorScroll = false, &r, 0
		}
		return m, nil
	case daemonOpMsg:
		m.daemonOp = ""
		if v.err != nil {
			m.sessions.flash, m.sessions.flashGood = "daemon "+v.op+" failed: "+v.err.Error(), false
		} else if v.op != "start" {
			// Stay quiet on a successful auto-start; flash explicit stop/restart.
			m.sessions.flash, m.sessions.flashGood = daemonOpPast(v.op), true
		}
		// Re-read health and sessions now that the daemon changed state.
		return m, tea.Batch(fetchStatusCmd, fetchSessionsCmd)
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
		// bubbletea v2 delivers a bracketed paste as its OWN message, which the
		// key encoder never sees — so every text field has to be routed here
		// explicitly or pasting silently does nothing. Mirror the keystroke
		// precedence below: focused embed, then whichever overlay owns input.
		return m.routePaste(v.Content)
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

	// The global settings editor owns all input while open. It takes the whole
	// msg and returns a tea.Cmd, exactly like the project form below: its Linear
	// label pickers load asynchronously, so it needs both a way to dispatch a
	// command and a route for the result message to come back on.
	if m.settings != nil {
		cmd, ev := m.settings.update(msg)
		switch ev {
		case settingsFormCancel:
			m.settings = nil
		case settingsFormSaved:
			m.settings = nil
			m.reloadConfig()
			if m.view == viewHome {
				m.home.flash, m.home.flashGood = "settings saved", true
			} else {
				m.list.flash = "settings saved"
			}
			return m, tea.Batch(bestEffortReloadCmd, fetchStatusCmd, fetchProjectsCmd)
		}
		return m, cmd
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

	// Home (the project-list landing screen) owns input while it is the active
	// view; overlays above still take precedence (handled before this point).
	if m.view == viewHome {
		return m.updateHome(msg)
	}
	if m.view == viewDetail {
		return m.updateDetail(msg)
	}
	if m.view == viewPRPicker {
		return m.updatePRPicker(msg)
	}
	if m.view == viewTicketPicker {
		return m.updateTicketPicker(msg)
	}

	// Cockpit key routing. Global keys (focus cycle, doctor) fire unless a modal
	// gate currently owns keystrokes — a poll delete / session kill confirmation,
	// the answer card, or the filter bar (whose keys may be "tab"/"d"/digits).
	gated := m.list.confirmDelete || m.sessions.confirmKill || m.sessions.answering || m.sessions.filtering || m.sessions.opening
	if k, ok := msg.(tea.KeyPressMsg); ok && !gated {
		switch k.String() {
		case "esc":
			// The main cockpit is the root; esc only backs OUT of a project
			// scope (entered from the project detail / PR picker) to the global
			// all-sessions view.
			if m.sessions.filter.Project != "" {
				m.sessions.filter.Project = ""
				m.sessions.selID = ""
				return m, fetchSessionsCmd
			}
			return m, nil
		case "p":
			// Open the project list (the "projects pane"): add / edit projects,
			// toggle polling, and drill into a project's detail + pickers.
			m.view = viewHome
			m.home.repin(m.cfg)
			return m, fetchProjectsCmd
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
		case "S":
			m.settings = newSettingsForm(m.cfgPath, m.cfg)
			return m, nil
		case "ctrl+r":
			if m.manageDaemon() && m.daemonOp == "" {
				m.daemonOp = "restarting"
				return m, restartDaemonCmd
			}
			return m, nil
		case "ctrl+x":
			if m.manageDaemon() && m.daemonOp == "" {
				m.daemonOp = "stopping"
				return m, stopDaemonCmd
			}
			return m, nil
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
	// Paint the whole alt-screen with the cockpit canvas so the frame is one
	// opaque, deliberate surface — the palette's tints and faint text are tuned
	// for this dark background, so an unset (light or theme-dependent) terminal
	// background is what made the same frame look muddy elsewhere.
	v.BackgroundColor = canvasColor()
	// A focused embed captures the mouse by default so the wheel is forwarded to
	// the agent's own history (the cockpit itself is keyboard-driven). Select-mode
	// (opt-in, Ctrl-g) releases the mouse to the outer terminal for native
	// drag-select/copy and ⌘-click-to-open a link.
	if m.embedFocused && !m.embedSelect {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

func (m *rootModel) viewString() string {
	if m.doctorLoading || m.doctorReport != nil {
		return m.doctorModal()
	}
	if m.settings != nil {
		return m.settingsFormModal()
	}
	if m.form != nil {
		// The poll edit form floats as a modal over the cockpit. (The first-run
		// setup wizard runs standalone before the cockpit exists, so it has no
		// backdrop to float over and stays full-screen in runSetupWizard.)
		return m.formModal()
	}
	if m.view == viewHome {
		return m.homeView()
	}
	if m.view == viewDetail {
		return m.detailView()
	}
	if m.view == viewPRPicker {
		return m.prPickerView()
	}
	if m.view == viewTicketPicker {
		return m.ticketPickerView()
	}
	return m.cockpitView()
}

// backdropLines is the frame an overlay (form/doctor/settings modal) floats
// over: the active top-level screen, so an overlay opened from Home dims the
// project list rather than an unrelated cockpit.
func (m *rootModel) backdropLines() []string {
	switch m.view {
	case viewHome:
		return m.homeLines()
	case viewDetail:
		return m.detailLines()
	case viewPRPicker:
		return m.prPickerLines()
	case viewTicketPicker:
		return m.ticketPickerLines()
	default:
		return m.cockpitLines()
	}
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
		if l.cursor < len(m.railProjectPtrs())-1 {
			l.cursor++
		}
	case "n":
		// New project. The form carries every field a [[project]] needs, so it
		// creates outright — 'P' edits the selected one.
		f, cmd := newFormModel(m.cfg, nil)
		m.form = f
		return m, cmd
	case "enter", "l", "right":
		// The rail is a switcher: open the selected project's detail hub (where
		// its polls / pickers / edit live), matching the p-screen's Enter.
		if p := m.selectedRailProject(); p != nil {
			return m.enterDetail(p.Name)
		}
	case "x":
		// 'x' stops the project polling; it is a no-op on a project that never polls.
		if p := m.selectedRailProject(); p != nil && p.Polls() {
			l.confirmDelete = true
		} else if p != nil {
			l.flash = p.Name + " does not poll — nothing to stop"
		}
	case "d":
		m.doctorLoading, m.doctorScroll = true, 0
		return m, runDoctorCmd(m.cfg)
	case "space":
		return m, m.toggleSelected()
	case "r":
		if p := m.selectedRailProject(); p != nil && p.TeamID != "" {
			l.flash = "refreshing linear cache…"
			return m, loadMetaCmd(m.cfg, p.TeamID, true)
		}
	}
	return m, nil
}

// railProjectPtrs returns live pointers (into cfg.Projects) to ALL projects in
// config order. The cockpit's left rail is a project switcher, so it lists every
// project — polling or not — and callers may mutate the returned project.
func (m *rootModel) railProjectPtrs() []*config.Project {
	out := make([]*config.Project, 0, len(m.cfg.Projects))
	for i := range m.cfg.Projects {
		out = append(out, &m.cfg.Projects[i])
	}
	return out
}

// selectedRailProject returns the project the cockpit's project rail cursor is
// on. Live pointer, safe to mutate.
func (m *rootModel) selectedRailProject() *config.Project {
	ptrs := m.railProjectPtrs()
	if m.list.cursor < 0 || m.list.cursor >= len(ptrs) {
		return nil
	}
	return ptrs[m.list.cursor]
}

func (m *rootModel) deleteSelected() tea.Cmd {
	p := m.selectedRailProject()
	if p == nil {
		return nil
	}
	name := p.Name
	// Re-read config.toml first: the daemon (enable/disable) may have
	// persisted changes since our snapshot; saving the stale copy would
	// silently revert them.
	m.reloadConfig()
	pr := m.cfg.ProjectByName(name)
	if pr == nil || !pr.Polls() {
		m.list.flash = "polling already off"
		return bestEffortReloadCmd
	}
	// "Delete poll" now means stop this project polling — the project itself
	// remains (for manual worktrees / PRs). Zero the polling config.
	clearPolling(pr)
	if m.list.cursor >= len(m.railProjectPtrs()) && m.list.cursor > 0 {
		m.list.cursor--
	}
	if err := m.cfg.Save(m.cfgPath); err != nil {
		m.list.flash = "save failed: " + err.Error()
		return nil
	}
	m.list.flash = "polling removed from " + name
	return bestEffortReloadCmd
}

// clearPolling zeroes a project's Linear polling configuration, leaving the
// repository/worktree setup intact.
func clearPolling(p *config.Project) {
	p.Enabled = false
	p.TeamID = ""
	p.ProjectID = ""
	p.CycleMode = ""
	p.CycleID = ""
	p.StateIDs = nil
	p.MatchLabels = nil
	p.MatchMode = ""
	p.AssigneeMode = ""
	p.AssigneeUserID = ""
	p.ConcurrencyCap = 0
	p.PrioritySort = nil
	p.DedupMode = ""
	p.OnSentSetLabel = ""
	p.OnSpawnStateID = ""
	p.OnPRStateID = ""
	p.OnMergedStateID = ""
	p.BlockedLabelID = ""
	p.CommentOnSpawn = false
	p.CommentOnPR = false
	p.CommentOnMerged = false
	p.CommentOnBlocked = false
	p.PRRequiresChecks = false
}

// toggleSelected pauses/resumes via the socket when the daemon is up;
// otherwise it flips enabled in config directly (save + best-effort reload).
func (m *rootModel) toggleSelected() tea.Cmd {
	p := m.selectedRailProject()
	if p == nil {
		return nil
	}
	if !p.Polls() {
		m.list.flash = p.Name + " does not poll — set a team in its detail (enter)"
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
	if n := len(m.railProjectPtrs()); m.list.cursor >= n && m.list.cursor > 0 {
		m.list.cursor = n - 1
	}
}
