// Project detail is the hub for one project: its status and health, a live
// strip of its sessions, and the action menu — open a PR, start a Linear
// ticket, a manual worktree, manage polls, or view sessions. It sits between
// Home (the project list) and the pickers. Actions whose backend has not landed
// yet render dimmed and flash a note when invoked, so the shape is visible while
// the pickers are built out.
package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/protocol"
)

type detailAction struct {
	key     string // mnemonic
	id      string // pr | ticket | worktree | polls | sessions | edit
	label   string
	desc    string
	enabled bool // false → dimmed + flashes "not available yet"
}

type detailModel struct {
	project string // the project name being viewed
	cursor  int
	flash   string
	flashOK bool
}

// enterDetail opens the project detail screen for the named project.
func (m *rootModel) enterDetail(name string) (tea.Model, tea.Cmd) {
	m.detail = detailModel{project: name}
	m.view = viewDetail
	return m, tea.Batch(fetchProjectsCmd, fetchSessionsCmd)
}

// detailInfo resolves the decoration (agent health, rollups) for the viewed
// project, and whether the daemon supplied it.
func (m *rootModel) detailInfo() (protocol.ProjectInfo, bool) {
	if info, ok := m.home.infoByName()[m.detail.project]; ok {
		return info, true
	}
	return protocol.ProjectInfo{}, false
}

// detailActions is the action menu for the viewed project. p/t/w are gated on
// per-project agent health (and, for PR, a configured repo) AND on whether
// their backend has shipped; until then they render disabled.
func (m *rootModel) detailActions() []detailAction {
	info, haveInfo := m.detailInfo()
	repoSet := m.projectHasRepo()
	// Spawn verbs need a healthy per-project agent; unknown (daemon down) is
	// treated as not-ready so we never advertise a launch we can't gate.
	agentReady := haveInfo && info.AgentOK

	// ticket / worktree require the feature to exist; those land in later phases
	// and flip to true then. The PR picker's enter is a DETACHED shell (git+tmux
	// only, no agent), so it gates on a configured repo, not agent health.
	const ticketShipped, worktreeShipped = false, false

	return []detailAction{
		{key: "p", id: "pr", label: "Open a PR", desc: "pick an open pull request → shell", enabled: repoSet},
		{key: "t", id: "ticket", label: "Start a ticket", desc: "pick a Linear issue → worktree + agent", enabled: ticketShipped && agentReady},
		{key: "w", id: "worktree", label: "New worktree", desc: "branch off base, agent or shell", enabled: worktreeShipped && agentReady},
		{key: "P", id: "polls", label: "Polls", desc: "add / edit / toggle this project's polls", enabled: true},
		{key: "s", id: "sessions", label: "Sessions", desc: "this project's live sessions", enabled: true},
		{key: "e", id: "edit", label: "Edit project", desc: "path / repo / agent / env", enabled: true},
	}
}

func (m *rootModel) projectHasRepo() bool {
	if p := m.cfg.ProjectByName(m.detail.project); p != nil {
		return p.Repo != ""
	}
	return false
}

// ---- update ----

func (m *rootModel) updateDetail(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	d := &m.detail
	actions := m.detailActions()

	switch k.String() {
	case "esc", "h", "left", "q":
		if k.String() == "q" {
			m.closeAllTerms()
			return m, tea.Quit
		}
		m.view = viewHome
		m.home.repin(m.cfg)
		return m, nil
	case "up", "k":
		if d.cursor > 0 {
			d.cursor--
		}
		return m, nil
	case "down", "j":
		if d.cursor < len(actions)-1 {
			d.cursor++
		}
		return m, nil
	case "enter", "l", "right":
		if d.cursor >= 0 && d.cursor < len(actions) {
			return m.runDetailAction(actions[d.cursor])
		}
		return m, nil
	case "d":
		m.doctorLoading, m.doctorScroll = true, 0
		return m, runDoctorCmd(m.cfg)
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
	// Direct mnemonics.
	for _, a := range actions {
		if k.String() == a.key {
			return m.runDetailAction(a)
		}
	}
	return m, nil
}

func (m *rootModel) runDetailAction(a detailAction) (tea.Model, tea.Cmd) {
	d := &m.detail
	if !a.enabled {
		msg := a.label + " is not available yet"
		if a.id == "pr" && !m.projectHasRepo() {
			msg = "set a GitHub repo (e) to list PRs"
		}
		d.flash, d.flashOK = msg, false
		return m, nil
	}
	switch a.id {
	case "pr":
		return m.enterPRPicker(d.project)
	case "sessions":
		return m.openProjectScope(d.project)
	case "edit":
		if f, ok := newProjectForm(m.cfgPath, m.cfg, d.project); ok {
			m.projForm = f
		} else {
			d.flash, d.flashOK = "project "+d.project+" not found", false
		}
		return m, nil
	case "polls":
		// Open a new-poll form (the form lets the user pick the project).
		f, cmd := newFormModel(m.cfg, nil)
		m.form = f
		return m, cmd
	}
	return m, nil
}
