// Package tui implements the interactive poll manager (lola / lola tui) and
// the plain socket client used by the other CLI subcommands.
package tui

import (
	"encoding/json"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Underline(true)
	selStyle   = lipgloss.NewStyle().Reverse(true)
)

type rootModel struct {
	cfgPath string
	cfg     *config.Config
	list    listModel
	form    *formModel
	width   int
	height  int
}

// Run opens the interactive TUI (poll list + cascading edit form).
func Run() error {
	cfgPath, err := config.DefaultPath()
	if err != nil {
		return err
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
		return m, tea.Batch(fetchStatusCmd, statusTick())
	case tea.KeyMsg:
		if v.String() == "ctrl+c" {
			return m, tea.Quit
		}
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
	return m.updateList(msg)
}

func (m *rootModel) View() string {
	if m.form != nil {
		return m.form.view(m.height)
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
	case "d":
		if m.selectedPoll() != nil {
			l.confirmDelete = true
		}
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

func (m *rootModel) reloadConfig() {
	if cfg, err := config.Load(m.cfgPath); err == nil {
		m.cfg = cfg
		m.list.teamNames = teamNamesFromCache(cfg)
	}
	if m.list.cursor >= len(m.cfg.Polls) && m.list.cursor > 0 {
		m.list.cursor = len(m.cfg.Polls) - 1
	}
}
