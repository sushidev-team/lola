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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	tab      int
	list     listModel
	sessions sessionsModel
	form     *formModel
	width    int
	height   int

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
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
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
		if m.form != nil {
			return m, statusTick()
		}
		cmds := []tea.Cmd{fetchStatusCmd, statusTick()}
		// Freeze the sessions list while a kill confirmation is pending: a
		// mid-confirm refresh could reorder/prune rows under the cursor (the
		// kill target is pinned by ID regardless, but the frozen view keeps the
		// prompt and the highlighted row in agreement).
		if m.tab == tabSessions && !m.sessions.confirmKill && !m.sessions.answering && !m.sessions.filtering {
			cmds = append(cmds, fetchSessionsCmd)
			if c := m.paneRefreshCmd(); c != nil {
				cmds = append(cmds, c)
			}
		}
		return m, tea.Batch(cmds...)
	case sessionsMsg:
		return m, m.handleSessionsMsg(v)
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
	case attachDoneMsg:
		if v.err != nil {
			m.sessions.flash = "attach failed: " + v.err.Error()
		}
		return m, fetchSessionsCmd
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
	case tea.KeyMsg:
		if v.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}

	// The doctor overlay owns all input while open (loading or showing).
	if m.doctorLoading || m.doctorReport != nil {
		if k, ok := msg.(tea.KeyMsg); ok {
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
	// Tab switching — but never while a delete/kill confirmation awaits y/n, nor
	// while an answer card is open (its choices may be keyed "1"/"2", which would
	// otherwise be swallowed as tab switches).
	if k, ok := msg.(tea.KeyMsg); ok &&
		!(m.tab == tabPolls && m.list.confirmDelete) &&
		!(m.tab == tabSessions && m.sessions.confirmKill) &&
		!(m.tab == tabSessions && m.sessions.answering) &&
		!(m.tab == tabSessions && m.sessions.filtering) {
		switch k.String() {
		case "tab":
			return m.switchTab(1 - m.tab)
		case "1":
			return m.switchTab(tabPolls)
		case "2":
			return m.switchTab(tabSessions)
		}
	}
	if m.tab == tabSessions {
		return m.updateSessions(msg)
	}
	return m.updateList(msg)
}

// switchTab activates a tab; entering the sessions tab triggers an immediate
// fetch (the 5s tick keeps it fresh afterwards).
func (m *rootModel) switchTab(tab int) (tea.Model, tea.Cmd) {
	if tab == m.tab {
		return m, nil
	}
	m.tab = tab
	if tab == tabSessions {
		return m, fetchSessionsCmd
	}
	return m, fetchStatusCmd
}

// tabBar is the shared header line; the active tab is highlighted.
func (m *rootModel) tabBar() string {
	polls, sessions := "1:polls", "2:sessions"
	if m.tab == tabSessions {
		return "lola  " + faintText.Render(polls) + "  " + titleStyle.Render(sessions)
	}
	return "lola  " + titleStyle.Render(polls) + "  " + faintText.Render(sessions)
}

func (m *rootModel) View() string {
	if m.doctorLoading || m.doctorReport != nil {
		return m.doctorView()
	}
	if m.form != nil {
		return m.form.view(m.height)
	}
	if m.tab == tabSessions {
		return m.sessionsView()
	}
	return m.listView()
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
	case tea.KeyMsg:
		return m.listKey(v)
	}
	return m, nil
}

func (m *rootModel) listKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
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
func (m *rootModel) doctorKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "q":
		m.doctorLoading, m.doctorReport, m.doctorScroll = false, nil, 0
	case "up", "k":
		if m.doctorScroll > 0 {
			m.doctorScroll--
		}
	case "down", "j":
		m.doctorScroll++ // clamped against the window in doctorView
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

func (m *rootModel) doctorView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("doctor") + "\n\n")
	if m.doctorReport == nil {
		b.WriteString(faintText.Render("running checks…") + "\n")
		b.WriteString("\n" + faintText.Render("esc close") + "\n")
		return b.String()
	}

	rep := *m.doctorReport
	lines := doctorReportLines(rep)

	// Scroll window sized to the terminal, mirroring the picker overlay.
	win := m.height - 7
	if win < 5 {
		win = 5
	}
	if maxScroll := len(lines) - win; m.doctorScroll > maxScroll {
		if maxScroll < 0 {
			maxScroll = 0
		}
		m.doctorScroll = maxScroll
	}
	start := m.doctorScroll
	end := start + win
	if end > len(lines) {
		end = len(lines)
	}
	if start > 0 {
		b.WriteString(faintText.Render("  ↑ more") + "\n")
	}
	for i := start; i < end; i++ {
		b.WriteString(lines[i] + "\n")
	}
	if end < len(lines) {
		b.WriteString(faintText.Render("  ↓ more") + "\n")
	}

	b.WriteString("\n" + rep.Summary() + "\n")
	b.WriteString(faintText.Render("↑/↓ scroll · esc close") + "\n")
	return b.String()
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
