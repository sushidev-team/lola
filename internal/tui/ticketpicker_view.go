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

	crumb := faintText.Render("lola ▸ "+p.project+" ▸ ") + "tickets"
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
			header := []string{"ISSUE", "TITLE", "PRIORITY"}
			cells := make([][]string, 0, len(rows))
			for _, is := range rows {
				cells = append(cells, ticketRowCells(is))
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

func ticketRowCells(is protocol.TicketRow) []string {
	id := is.Identifier
	if is.AlreadyLive {
		id = faintText.Render(id + " ●")
	}
	return []string{id, truncPlain(is.Title, 46), ticketPriority(is.Priority)}
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
	keys := []string{"↑↓ move", "enter start (worktree + agent)", "[ ] scope", "r refresh", "/ filter", "esc back", "q quit"}
	return previewLine(faintText.Render(strings.Join(keys, " · ")), w)
}
