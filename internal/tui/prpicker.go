// The PR picker: a fuzzy-filterable list of a project's open pull requests
// (cmd=prs). enter opens the selected PR's branch as a detached shell (the
// existing cmd=open) so it can be run/tested; r refreshes; esc returns to the
// project detail. Agent-on-PR (tracking + push-back) is the later 'a' upgrade.
package tui

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/protocol"
)

type prPickerModel struct {
	project   string
	gen       int // request generation, for stale-drop
	data      *protocol.PrsData
	loading   bool
	err       string
	daemon    bool // daemon down
	cursor    int
	filter    string
	filtering bool
	flash     string
}

// prsMsg carries a cmd=prs result tagged with the project + generation it was
// requested for, so a stale response (from a picker the user already left) is
// dropped.
type prsMsg struct {
	project string
	gen     int
	data    *protocol.PrsData
	err     string
	down    bool
}

func fetchPrsCmd(project string, gen int, refresh bool) tea.Cmd {
	return func() tea.Msg {
		args, _ := json.Marshal(protocol.PrsArgs{Project: project, Refresh: refresh})
		resp, err := requestFn(protocol.Request{Cmd: "prs", Args: args})
		if errors.Is(err, errDaemonDown) {
			return prsMsg{project: project, gen: gen, down: true}
		}
		if err != nil {
			return prsMsg{project: project, gen: gen, err: err.Error()}
		}
		if !resp.OK {
			return prsMsg{project: project, gen: gen, err: resp.Error}
		}
		var d protocol.PrsData
		if err := json.Unmarshal(resp.Data, &d); err != nil {
			return prsMsg{project: project, gen: gen, err: err.Error()}
		}
		return prsMsg{project: project, gen: gen, data: &d}
	}
}

func (m *rootModel) enterPRPicker(project string) (tea.Model, tea.Cmd) {
	gen := m.prpick.gen + 1
	m.prpick = prPickerModel{project: project, gen: gen, loading: true}
	m.view = viewPRPicker
	return m, fetchPrsCmd(project, gen, false)
}

func (m *rootModel) handlePrsMsg(v prsMsg) {
	p := &m.prpick
	if v.project != p.project || v.gen != p.gen {
		return // stale: a newer request or a different picker superseded this
	}
	p.loading = false
	switch {
	case v.down:
		p.daemon, p.err, p.data = true, "", nil
	case v.err != "":
		p.err, p.daemon = v.err, false
	default:
		p.data, p.err, p.daemon = v.data, "", false
		if p.cursor >= len(m.prpickRows()) {
			p.cursor = 0
		}
	}
}

// prpickRows returns the PRs to display, filtered by the live filter text.
func (m *rootModel) prpickRows() []protocol.PrRow {
	p := &m.prpick
	if p.data == nil {
		return nil
	}
	if p.filter == "" {
		return p.data.PRs
	}
	q := strings.ToLower(p.filter)
	out := make([]protocol.PrRow, 0, len(p.data.PRs))
	for _, pr := range p.data.PRs {
		hay := strings.ToLower(pr.Title + " " + pr.Author + " " + pr.Branch + " " + strconv.Itoa(pr.Number))
		if strings.Contains(hay, q) {
			out = append(out, pr)
		}
	}
	return out
}

func (m *rootModel) updatePRPicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	p := &m.prpick

	if p.filtering {
		switch k.String() {
		case "esc":
			p.filtering, p.filter = false, ""
			p.cursor = 0
		case "enter":
			p.filtering = false
		case "backspace":
			if p.filter != "" {
				p.filter = p.filter[:len(p.filter)-1]
			}
			p.cursor = 0
		default:
			if k.Text != "" {
				p.filter += k.Text
				p.cursor = 0
			}
		}
		return m, nil
	}

	rows := m.prpickRows()
	switch k.String() {
	case "esc", "h", "left":
		m.view = viewDetail
		return m, nil
	case "q":
		m.closeAllTerms()
		return m, tea.Quit
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(rows)-1 {
			p.cursor++
		}
	case "g":
		p.cursor = 0
	case "G":
		if len(rows) > 0 {
			p.cursor = len(rows) - 1
		}
	case "?":
		m.showHelp = true
	case "/":
		p.filtering, p.filter = true, ""
	case "r":
		p.loading, p.flash = true, ""
		p.gen++
		return m, fetchPrsCmd(p.project, p.gen, true)
	case "o":
		if p.cursor >= 0 && p.cursor < len(rows) && rows[p.cursor].URL != "" {
			p.flash = "opening #" + strconv.Itoa(rows[p.cursor].Number) + " in browser…"
			return m, openURLCmd(rows[p.cursor].URL)
		}
	case "a":
		if p.cursor >= 0 && p.cursor < len(rows) {
			return m.openPRWithAgent(rows[p.cursor])
		}
	case "enter", "l", "right":
		if p.cursor >= 0 && p.cursor < len(rows) {
			return m.openPRDetached(rows[p.cursor])
		}
	}
	return m, nil
}

// openPRWithAgent opens the PR's branch as a tracking worktree + agent (cmd=
// openPr) and drops into the scoped cockpit. A fork PR is refused up front (the
// daemon also refuses); an already-open branch is refused.
func (m *rootModel) openPRWithAgent(pr protocol.PrRow) (tea.Model, tea.Cmd) {
	p := &m.prpick
	if pr.IsFork {
		p.flash = "fork PR — use enter for a detached run/test; push-back isn't supported"
		return m, nil
	}
	if pr.AlreadyOpen {
		p.flash = pr.Branch + " is already open — see sessions"
		return m, nil
	}
	m.sessions.filter.Project = p.project
	m.sessions.selID = ""
	m.view = viewCockpit
	m.focus = focusSessions
	return m, tea.Batch(openPrCmd(p.project, pr.Branch, pr.Number, pr.IsFork), fetchSessionsCmd)
}

// openPRDetached checks out the PR's head branch as a detached shell (the
// existing cmd=open) and drops into the project-scoped cockpit so the new
// session is visible. A branch a live session already holds is refused.
func (m *rootModel) openPRDetached(pr protocol.PrRow) (tea.Model, tea.Cmd) {
	p := &m.prpick
	if pr.AlreadyOpen {
		p.flash = pr.Branch + " is already open — see sessions"
		return m, nil
	}
	m.sessions.filter.Project = p.project
	m.sessions.selID = ""
	m.view = viewCockpit
	m.focus = focusSessions
	return m, tea.Batch(openSessionCmd(p.project, pr.Branch), fetchSessionsCmd)
}
