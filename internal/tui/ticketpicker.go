// The ticket picker: a fuzzy-filterable list of a project's Linear issues
// (cmd=tickets). enter starts the selected issue — a worktree + agent, deduped
// like a poll dispatch (cmd=openTicket). [ ] toggles the scope (mine/team); r
// refreshes; esc returns to the project detail.
package tui

import (
	"encoding/json"
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/protocol"
)

type ticketPickerModel struct {
	project   string
	scope     string // mine | team
	gen       int
	data      *protocol.TicketsData
	loading   bool
	err       string
	daemon    bool
	cursor    int
	filter    string
	filtering bool
	flash     string
}

type ticketsMsg struct {
	project string
	gen     int
	data    *protocol.TicketsData
	err     string
	down    bool
}

func fetchTicketsCmd(project, scope string, gen int) tea.Cmd {
	return func() tea.Msg {
		args, _ := json.Marshal(protocol.TicketsArgs{Project: project, Scope: scope})
		resp, err := requestFn(protocol.Request{Cmd: "tickets", Args: args})
		if errors.Is(err, errDaemonDown) {
			return ticketsMsg{project: project, gen: gen, down: true}
		}
		if err != nil {
			return ticketsMsg{project: project, gen: gen, err: err.Error()}
		}
		if !resp.OK {
			return ticketsMsg{project: project, gen: gen, err: resp.Error}
		}
		var d protocol.TicketsData
		if err := json.Unmarshal(resp.Data, &d); err != nil {
			return ticketsMsg{project: project, gen: gen, err: err.Error()}
		}
		return ticketsMsg{project: project, gen: gen, data: &d}
	}
}

func (m *rootModel) enterTicketPicker(project string) (tea.Model, tea.Cmd) {
	gen := m.ticket.gen + 1
	m.ticket = ticketPickerModel{project: project, scope: "mine", gen: gen, loading: true}
	m.view = viewTicketPicker
	return m, fetchTicketsCmd(project, "mine", gen)
}

func (m *rootModel) handleTicketsMsg(v ticketsMsg) {
	p := &m.ticket
	if v.project != p.project || v.gen != p.gen {
		return
	}
	p.loading = false
	switch {
	case v.down:
		p.daemon, p.err, p.data = true, "", nil
	case v.err != "":
		p.err, p.daemon = v.err, false
	default:
		p.data, p.err, p.daemon = v.data, "", false
		if p.cursor >= len(m.ticketRows()) {
			p.cursor = 0
		}
	}
}

func (m *rootModel) ticketRows() []protocol.TicketRow {
	p := &m.ticket
	if p.data == nil {
		return nil
	}
	if p.filter == "" {
		return p.data.Issues
	}
	q := strings.ToLower(p.filter)
	out := make([]protocol.TicketRow, 0, len(p.data.Issues))
	for _, is := range p.data.Issues {
		if strings.Contains(strings.ToLower(is.Identifier+" "+is.Title), q) {
			out = append(out, is)
		}
	}
	return out
}

func (m *rootModel) updateTicketPicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	p := &m.ticket

	if p.filtering {
		switch k.String() {
		case "esc":
			p.filtering, p.filter, p.cursor = false, "", 0
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

	rows := m.ticketRows()
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
	case "[", "]":
		if p.scope == "mine" {
			p.scope = "team"
		} else {
			p.scope = "mine"
		}
		p.loading, p.gen = true, p.gen+1
		return m, fetchTicketsCmd(p.project, p.scope, p.gen)
	case "r":
		p.loading, p.flash, p.gen = true, "", p.gen+1
		return m, fetchTicketsCmd(p.project, p.scope, p.gen)
	case "/":
		p.filtering, p.filter = true, ""
	case "enter", "l", "right":
		if p.cursor >= 0 && p.cursor < len(rows) {
			return m.startTicket(rows[p.cursor])
		}
	}
	return m, nil
}

// startTicket starts the selected issue (cmd=openTicket) and drops into the
// project-scoped cockpit. An issue a session already holds is refused.
func (m *rootModel) startTicket(is protocol.TicketRow) (tea.Model, tea.Cmd) {
	p := &m.ticket
	if is.AlreadyLive {
		p.flash = is.Identifier + " already has a session — see sessions"
		return m, nil
	}
	m.sessions.filter.Project = p.project
	m.sessions.selID = ""
	m.view = viewCockpit
	m.focus = focusSessions
	return m, tea.Batch(openTicketCmd(p.project, is), fetchSessionsCmd)
}
