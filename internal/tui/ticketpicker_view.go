package tui

import (
	"fmt"
	"strings"

	"github.com/sushidev-team/lola/internal/protocol"
)

func (m *rootModel) ticketPickerView() string {
	return strings.Join(m.ticketPickerLines(), "\n")
}

func (m *rootModel) ticketPickerLines() []string {
	W, H := m.width, m.height
	if W <= 0 {
		W = 100
	}
	if H <= 0 {
		H = 24
	}
	p := &m.ticket

	out := make([]string, 0, H)
	out = append(out, m.vitalsBar(W))

	crumb := faintText.Render("lola ▸ "+m.projLabel(p.project)+" ▸ ") + "tickets"
	if team := ticketTeamLabel(p.data); team != "" {
		crumb += faintText.Render("  ·  " + team)
	}
	right := "scope " + scopeLabel(p.scope)
	if p.data != nil {
		right += faintText.Render(fmt.Sprintf("  ·  %d", len(p.data.Issues)))
	}
	out = append(out, prpickHeaderLine(crumb, faintText.Render(right), W))
	if p.filtering {
		out = append(out, previewLine(faintText.Render("/")+p.filter+"_", W))
	}

	panelH := H - len(out) - 2
	if panelH < 3 {
		panelH = 3
	}
	out = append(out, m.ticketPanel(W, panelH)...)
	out = append(out, m.ticketMessage(W))
	out = append(out, m.ticketKeybar(W))
	return fitHeight(out, H)
}

// ticketTeamLabel names the team a human recognizes — "Frontend (FE)" — or
// NOTHING when the daemon could not resolve one (its lookup fails open).
// `d.Team` is deliberately not a fallback: it is the UUID config keys by, and a
// 36-character hex string where a name belongs reads as a bug rather than as
// information. The crumb beside it already names the project.
func ticketTeamLabel(d *protocol.TicketsData) string {
	if d == nil {
		return ""
	}
	switch {
	case d.TeamName != "" && d.TeamKey != "":
		return d.TeamName + " (" + d.TeamKey + ")"
	case d.TeamName != "":
		return d.TeamName
	default:
		return d.TeamKey
	}
}

func scopeLabel(scope string) string {
	if scope == "team" {
		return goodText.Render("‹ team ›") + faintText.Render(" mine")
	}
	return goodText.Render("‹ mine ›") + faintText.Render(" team")
}

func (m *rootModel) ticketPanel(w, panelH int) []string {
	p := &m.ticket
	var body []string

	switch {
	case p.loading && p.data == nil:
		body = append(body, faintText.Render("  Loading issues…"))
	case p.daemon:
		body = append(body, badText.Render("  daemon not running")+faintText.Render(" — ^r to start"))
	case p.err != "":
		body = append(body, badText.Render("  couldn't list issues: ")+p.err, faintText.Render("  r to retry"))
	default:
		rows := m.ticketRows()
		if len(rows) == 0 {
			body = append(body, faintText.Render("  No issues in this scope — [ ] switch scope, r refresh"))
		} else {
			team := p.scope == "team"
			header := []string{"ISSUE", "TITLE", "STATUS", "PRIORITY", "UPD"}
			if team {
				header = append(header, "ASSIGNEE")
			}
			cells := make([][]string, 0, len(rows))
			for _, is := range rows {
				cells = append(cells, ticketRowCells(is, team))
			}
			widths := colWidths(header, cells)
			body = append(body, tblHeader.Render(padCells(header, widths)))
			innerH := panelH - 3
			if innerH < 1 {
				innerH = 1
			}
			start := viewportStart(p.cursor, len(cells), innerH)
			for i := start; i < len(cells) && i < start+innerH; i++ {
				line := padCells(cells[i], widths)
				if i == p.cursor {
					line = highlightRow(line, w-4, bgSGR(colSel))
				}
				body = append(body, line)
			}
		}
	}
	return box(paneTitle("Tickets", ""), body, w, panelH, true)
}

func ticketRowCells(is protocol.TicketRow, teamScope bool) []string {
	id := is.Identifier
	if is.AlreadyLive {
		id = faintText.Render(id + " ●")
	}
	upd := is.Updated
	if upd == "" {
		upd = "—"
	}
	cells := []string{
		id,
		truncPlain(is.Title, 40),
		ticketState(is.State, is.StateType),
		ticketPriority(is.Priority),
		faintText.Render(upd),
	}
	if teamScope {
		who := is.Assignee
		if who == "" {
			who = "—"
		}
		cells = append(cells, faintText.Render(truncPlain(who, 14)))
	}
	return cells
}

// ticketState renders the team's own state NAME, coloured by the stable state
// TYPE — the names are per-team free text ("Ready for QA", "Doing"), so the type
// is the only thing worth branching on.
func ticketState(name, stateType string) string {
	if name == "" {
		return faintText.Render("—")
	}
	name = truncPlain(name, 14)
	switch stateType {
	case "started":
		return goodText.Render(name)
	case "triage":
		return warnText.Render(name)
	case "unstarted":
		return name
	case "completed", "canceled":
		return faintText.Render(name)
	default:
		return faintText.Render(name)
	}
}

func ticketPriority(pri float64) string {
	switch pri {
	case 1:
		return badText.Render("urgent")
	case 2:
		return warnText.Render("high")
	case 3:
		return "medium"
	case 4:
		return faintText.Render("low")
	default:
		return faintText.Render("—")
	}
}

func (m *rootModel) ticketMessage(w int) string {
	p := &m.ticket
	if p.flash != "" {
		return previewLine(warnText.Render(p.flash), w)
	}
	if p.loading && p.data != nil {
		return previewLine(faintText.Render("loading…"), w)
	}
	return ""
}

func (m *rootModel) ticketKeybar(w int) string {
	p := &m.ticket
	if p.filtering {
		return previewLine(faintText.Render("type to filter · enter apply · esc clear"), w)
	}
	keys := []string{"↑↓ move", "enter start (worktree + agent)", "[ ] scope", "r refresh", "/ filter", "esc back", "? help", "q quit"}
	return previewLine(faintText.Render(strings.Join(keys, " · ")), w)
}
