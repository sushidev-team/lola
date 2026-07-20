// Home is the project-centric landing screen: a list of every [[project]] in
// config, rendered from the LOCAL config so it works even with the daemon down,
// and decorated with live status (poll health, live sessions, attention,
// last-run) from cmd=projects. From here you open a project's sessions, add /
// edit / remove a project, and toggle its polling. It is the parent screen of
// the nav model; the cockpit (global sessions) is reachable from it.
package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
)

// homeModel is the project-list screen state. Rows come from m.cfg.Projects
// (always available); data decorates them and is nil until the first fetch.
// Selection is by project NAME (selName) so a background refresh never moves the
// cursor onto a different project.
type homeModel struct {
	cursor     int
	selName    string
	data       *protocol.ProjectsData // decoration; nil until first successful fetch
	daemonDown bool
	dataErr    string

	filter    string
	filtering bool

	adding   bool // inline "new project" name prompt
	addInput string

	confirmRemove bool
	removeTarget  string

	flash     string
	flashGood bool
}

func newHomeModel() homeModel { return homeModel{} }

// projectsMsg carries a cmd=projects result (or a fetch error) back to the UI.
type projectsMsg struct {
	data *protocol.ProjectsData
	err  error
}

// fetchProjectsCmd issues cmd=projects. It goes through the injectable requestFn
// seam so model tests can supply a canned ProjectsData.
func fetchProjectsCmd() tea.Msg {
	resp, err := requestFn(protocol.Request{Cmd: "projects"})
	if err != nil {
		return projectsMsg{err: err}
	}
	if !resp.OK {
		return projectsMsg{err: errors.New(resp.Error)}
	}
	var d protocol.ProjectsData
	if err := json.Unmarshal(resp.Data, &d); err != nil {
		return projectsMsg{err: err}
	}
	return projectsMsg{data: &d}
}

// handleProjectsMsg absorbs a projectsMsg: a dial error blanks the decoration
// (rows still render from config); any other error is surfaced in the message
// line. Selection is re-pinned to selName.
func (m *rootModel) handleProjectsMsg(v projectsMsg) {
	h := &m.home
	if v.err != nil {
		h.data = nil
		if errors.Is(v.err, errDaemonDown) {
			h.daemonDown, h.dataErr = true, ""
		} else {
			h.daemonDown, h.dataErr = false, v.err.Error()
		}
	} else {
		h.data, h.daemonDown, h.dataErr = v.data, false, ""
	}
	h.repin(m.cfg)
}

// infoByName indexes the decoration data by project name.
func (h *homeModel) infoByName() map[string]protocol.ProjectInfo {
	out := map[string]protocol.ProjectInfo{}
	if h.data == nil {
		return out
	}
	for _, p := range h.data.Projects {
		out[p.Name] = p
	}
	return out
}

// rows returns the projects to display, filtered by the live filter text.
func (h *homeModel) rows(cfg *config.Config) []config.Project {
	if cfg == nil {
		return nil
	}
	if h.filter == "" {
		return cfg.Projects
	}
	q := strings.ToLower(h.filter)
	out := make([]config.Project, 0, len(cfg.Projects))
	for _, p := range cfg.Projects {
		// Match the id as well as the label: the list shows labels, but the id is
		// what appears in session names and paths, so a human searching for what
		// they saw in a terminal should still find the project.
		if strings.Contains(strings.ToLower(p.DisplayName()), q) ||
			strings.Contains(strings.ToLower(p.Name), q) ||
			strings.Contains(strings.ToLower(p.Path), q) {
			out = append(out, p)
		}
	}
	return out
}

// repin keeps cursor and selName in agreement after a data/filter/config change:
// selName is authoritative; cursor is re-derived from it, else clamped.
func (h *homeModel) repin(cfg *config.Config) {
	rows := h.rows(cfg)
	if len(rows) == 0 {
		h.cursor, h.selName = 0, ""
		return
	}
	if h.selName != "" {
		for i, p := range rows {
			if p.Name == h.selName {
				h.cursor = i
				return
			}
		}
	}
	if h.cursor >= len(rows) {
		h.cursor = len(rows) - 1
	}
	if h.cursor < 0 {
		h.cursor = 0
	}
	h.selName = rows[h.cursor].Name
}

func (h *homeModel) selectedProject(cfg *config.Config) *config.Project {
	rows := h.rows(cfg)
	if h.cursor < 0 || h.cursor >= len(rows) {
		return nil
	}
	// Return a pointer into cfg.Projects (not the filtered copy) so callers edit
	// the live config.
	name := rows[h.cursor].Name
	return cfg.ProjectByName(name)
}

// ---- update ----

func (m *rootModel) updateHome(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	h := &m.home

	if h.filtering {
		return m.updateHomeFilter(k)
	}
	if h.adding {
		return m.updateHomeAdd(k)
	}
	if h.confirmRemove {
		h.confirmRemove = false
		if s := k.String(); s == "y" || s == "Y" {
			return m, m.removeProject(h.removeTarget)
		}
		return m, nil
	}

	h.flash = ""
	rows := h.rows(m.cfg)
	switch k.String() {
	case "esc", "h", "left":
		// Back to the main cockpit (all sessions).
		m.view = viewCockpit
		return m, nil
	case "q":
		m.closeAllTerms()
		return m, tea.Quit
	case "up", "k":
		if h.cursor > 0 {
			h.cursor--
		}
		h.syncSel(rows)
	case "down", "j":
		if h.cursor < len(rows)-1 {
			h.cursor++
		}
		h.syncSel(rows)
	case "g":
		h.cursor = 0
		h.syncSel(rows)
	case "G":
		h.cursor = len(rows) - 1
		h.syncSel(rows)
	case "enter", "l", "right":
		if p := h.selectedProject(m.cfg); p != nil {
			return m.enterDetail(p.Name)
		}
	case "s":
		return m.openGlobalSessions()
	case "a":
		h.adding, h.addInput = true, ""
	case "e":
		if p := h.selectedProject(m.cfg); p != nil {
			pr := m.cfg.ProjectByName(p.Name)
			if pr == nil {
				h.flash, h.flashGood = "project "+p.Name+" not found", false
				return m, nil
			}
			f, cmd := newFormModel(m.cfg, pr)
			m.form = f
			return m, cmd
		}
	case "x":
		if p := h.selectedProject(m.cfg); p != nil {
			h.confirmRemove, h.removeTarget = true, p.Name
		}
	case "space":
		return m, m.homeTogglePoll()
	case "/":
		h.filtering, h.filter = true, ""
	case "d":
		m.doctorLoading, m.doctorScroll = true, 0
		return m, runDoctorCmd(m.cfg)
	case "S":
		m.settings = newSettingsForm(m.cfgPath, m.cfg)
	case "ctrl+r":
		if m.manageDaemon() && m.daemonOp == "" {
			m.daemonOp = "restarting"
			return m, restartDaemonCmd
		}
	case "ctrl+x":
		if m.manageDaemon() && m.daemonOp == "" {
			m.daemonOp = "stopping"
			return m, stopDaemonCmd
		}
	}
	return m, nil
}

func (h *homeModel) syncSel(rows []config.Project) {
	if h.cursor >= 0 && h.cursor < len(rows) {
		h.selName = rows[h.cursor].Name
	}
}

func (m *rootModel) updateHomeFilter(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	h := &m.home
	switch k.String() {
	case "esc":
		h.filtering, h.filter = false, ""
		h.repin(m.cfg)
	case "enter":
		h.filtering = false
		h.repin(m.cfg)
	case "backspace":
		if h.filter != "" {
			h.filter = h.filter[:len(h.filter)-1]
		}
		h.repin(m.cfg)
	default:
		if k.Text != "" { // printable runes, incl. space (bubbletea v2)
			h.filter += k.Text
			h.repin(m.cfg)
		}
	}
	return m, nil
}

func (m *rootModel) updateHomeAdd(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	h := &m.home
	switch k.String() {
	case "esc":
		h.adding, h.addInput = false, ""
	case "enter":
		name := strings.TrimSpace(h.addInput)
		h.adding, h.addInput = false, ""
		if name == "" {
			return m, nil
		}
		return m, m.addProject(name)
	case "backspace":
		if h.addInput != "" {
			h.addInput = h.addInput[:len(h.addInput)-1]
		}
	default:
		if k.Text != "" { // printable runes, incl. space (bubbletea v2)
			h.addInput += k.Text
		}
	}
	return m, nil
}

// openProjectScope enters the cockpit filtered to one project's sessions. (Once
// the project detail screen lands it will open that instead; scoping the
// sessions view is the interim so navigation works end to end.)
func (m *rootModel) openProjectScope(name string) (tea.Model, tea.Cmd) {
	m.sessions.filter.Project = name
	m.sessions.selID = ""
	m.view = viewCockpit
	m.focus = focusSessions
	return m, fetchSessionsCmd
}

// openGlobalSessions enters the cockpit showing every project's sessions.
func (m *rootModel) openGlobalSessions() (tea.Model, tea.Cmd) {
	m.sessions.filter.Project = ""
	m.view = viewCockpit
	m.focus = focusSessions
	return m, fetchSessionsCmd
}

// addProject opens the project form on a NEW entry seeded with the given name.
// Nothing is persisted until the form saves: a project needs a path to validate,
// so writing a stub first would put an invalid [[project]] on disk and leave it
// there if the user backs out.
func (m *rootModel) addProject(name string) tea.Cmd {
	m.reloadConfig()
	if m.cfg.ProjectByName(name) != nil {
		m.home.flash, m.home.flashGood = "project "+name+" already exists", false
		return nil
	}
	f, cmd := newFormModel(m.cfg, nil)
	f.poll.Name = name
	f.cursor = 1 // past the (already filled) name, onto Path
	m.form = f
	m.home.selName = name
	return cmd
}

// removeProject drops a [[project]] and its nested polls from config. A live
// session on the project is not torn down here (its worktree teardown survives
// project removal via the persisted Session.Worktree once that path lands); the
// flash notes the count so it is never silent.
func (m *rootModel) removeProject(name string) tea.Cmd {
	m.reloadConfig()
	idx := -1
	for i := range m.cfg.Projects {
		if m.cfg.Projects[i].Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		m.home.flash, m.home.flashGood = "project already gone", false
		return bestEffortReloadCmd
	}

	live := 0
	if m.home.data != nil {
		if info, ok := m.home.infoByName()[name]; ok {
			live = info.LiveCounted
		}
	}

	// Removing the project removes its polling config too (they're one struct now).
	m.cfg.Projects = append(m.cfg.Projects[:idx], m.cfg.Projects[idx+1:]...)

	if err := m.cfg.Save(m.cfgPath); err != nil {
		m.home.flash, m.home.flashGood = "save failed: "+err.Error(), false
		return nil
	}
	m.home.selName = ""
	m.home.repin(m.cfg)
	if live > 0 {
		m.home.flash, m.home.flashGood = fmt.Sprintf("removed %q (%d live session(s) still running — kill them from sessions)", name, live), true
	} else {
		m.home.flash, m.home.flashGood = "removed project "+name, true
	}
	return bestEffortReloadCmd
}

// homeTogglePoll pauses/resumes the selected project's polling (a project has
// at most one polling config — its own). A project not configured to poll
// flashes a hint to set it up in the project view. The flip goes through config
// + reload, so it works whether or not the daemon is up.
func (m *rootModel) homeTogglePoll() tea.Cmd {
	sel := m.home.selectedProject(m.cfg)
	if sel == nil {
		return nil
	}
	name := sel.Name
	m.reloadConfig()
	p := m.cfg.ProjectByName(name)
	if p == nil {
		m.home.flash, m.home.flashGood = "project no longer exists", false
		return nil
	}
	if !p.Polls() {
		m.home.flash, m.home.flashGood = "no polling on "+name+" — configure it in the project view (enter → P)", false
		return nil
	}
	p.Enabled = !p.Enabled
	if err := m.cfg.Save(m.cfgPath); err != nil {
		m.home.flash, m.home.flashGood = "save failed: "+err.Error(), false
		return nil
	}
	verb := "paused"
	if p.Enabled {
		verb = "resumed"
	}
	m.home.flash, m.home.flashGood = verb+" polling on "+name, true
	return bestEffortReloadCmd
}
