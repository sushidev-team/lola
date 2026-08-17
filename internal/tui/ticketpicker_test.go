package tui

import (
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
)

// ticketPickerRoot opens the ticket picker for project "A" (which has a team)
// with a canned tickets response.
func ticketPickerRoot(t *testing.T, issues []protocol.TicketRow) *rootModel {
	t.Helper()
	return ticketPickerRootData(t, protocol.TicketsData{Team: "team-1", Issues: issues})
}

func ticketPickerRootData(t *testing.T, data protocol.TicketsData) *rootModel {
	t.Helper()
	m := detailRoot(t)
	m.detail.project = "A" // "A" is a polling project (has team_id)
	fakeRequest(t, nil, mustData(t, data), nil)
	_, cmd := m.enterTicketPicker("A")
	runCmd(t, m, cmd)
	return m
}

func TestTicketPickerRendersRows(t *testing.T) {
	m := ticketPickerRoot(t, []protocol.TicketRow{
		{Identifier: "FE-9", UUID: "u9", Title: "fix oauth flow", Priority: 1},
	})
	if m.view != viewTicketPicker {
		t.Fatalf("view = %d, want viewTicketPicker", m.view)
	}
	v := stripANSI(m.ticketPickerView())
	for _, want := range []string{"tickets", "FE-9", "fix oauth flow", "urgent"} {
		if !strings.Contains(v, want) {
			t.Errorf("picker view missing %q:\n%s", want, v)
		}
	}
}

// The row carries the facts a human picks by — workflow state and staleness —
// and the header names the TEAM rather than printing its UUID.
func TestTicketPickerShowsStateAndTeamName(t *testing.T) {
	m := ticketPickerRootData(t, protocol.TicketsData{
		Team: "ace69aca-dd39-4c63-91dc-36bbf48b62c7", TeamName: "Nori", TeamKey: "NOR",
		Issues: []protocol.TicketRow{
			{Identifier: "NOR-9", UUID: "u9", Title: "fix oauth flow", Priority: 1,
				State: "In Progress", StateType: "started", Updated: "2h05m"},
		},
	})
	v := stripANSI(m.ticketPickerView())
	for _, want := range []string{"Nori (NOR)", "In Progress", "2h05m", "STATUS"} {
		if !strings.Contains(v, want) {
			t.Errorf("picker view missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "ace69aca") {
		t.Errorf("the team UUID must not be rendered when a name resolved:\n%s", v)
	}
}

// An UNNAMEABLE team leaves the slot empty rather than falling back to the UUID:
// the crumb already names the project, and a 36-char hex string in a header reads
// as a bug (it was one, in the app's picker).
func TestTicketPickerNeverRendersTheTeamUUID(t *testing.T) {
	m := ticketPickerRootData(t, protocol.TicketsData{
		Team: "ace69aca-dd39-4c63-91dc-36bbf48b62c7",
		Issues: []protocol.TicketRow{
			{Identifier: "NOR-9", UUID: "u9", Title: "fix oauth flow", Priority: 1},
		},
	})
	v := stripANSI(m.ticketPickerView())
	if strings.Contains(v, "ace69aca") {
		t.Errorf("an unresolved team must render as nothing, not as its UUID:\n%s", v)
	}
	if !strings.Contains(v, "NOR-9") {
		t.Errorf("the rows must still render:\n%s", v)
	}
}

// `/` filters over what the row shows — state, labels and assignee included, not
// just the identifier and title.
func TestTicketPickerFilterCoversDisplayedFields(t *testing.T) {
	m := ticketPickerRoot(t, []protocol.TicketRow{
		{Identifier: "NOR-1", UUID: "u1", Title: "one", State: "In Review"},
		{Identifier: "NOR-2", UUID: "u2", Title: "two", Labels: []string{"bug"}},
		{Identifier: "NOR-3", UUID: "u3", Title: "three", Assignee: "Ada"},
	})
	for q, want := range map[string]string{"review": "NOR-1", "bug": "NOR-2", "ada": "NOR-3"} {
		m.ticket.filter = q
		rows := m.ticketRows()
		if len(rows) != 1 || rows[0].Identifier != want {
			t.Errorf("filter %q = %v, want just %s", q, rows, want)
		}
	}
}

// enter starts the selected ticket (cmd=openTicket) and scopes the cockpit.
func TestTicketPickerStartsTicket(t *testing.T) {
	m := ticketPickerRoot(t, []protocol.TicketRow{
		{Identifier: "FE-9", UUID: "u9", Title: "fix oauth", Branch: "lola/fe-9"},
	})
	var got []protocol.Request
	fakeRequest(t, &got, mustData(t, protocol.OpenData{SessionID: "s", Message: "started FE-9"}), nil)

	_, cmd := m.Update(keyMsg("enter"))
	runCmd(t, m, cmd)

	if m.view != viewCockpit || m.sessions.filter.Project != "A" {
		t.Errorf("after start, want scoped cockpit; view=%d scope=%q", m.view, m.sessions.filter.Project)
	}
	found := false
	for _, r := range got {
		if r.Cmd == "openTicket" && strings.Contains(string(r.Args), "u9") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a cmd=openTicket for u9, got %+v", got)
	}
}

// An already-live ticket is refused with a flash, no cmd=openTicket.
func TestTicketPickerRefusesAlreadyLive(t *testing.T) {
	m := ticketPickerRoot(t, []protocol.TicketRow{
		{Identifier: "FE-9", UUID: "u9", Title: "fix oauth", AlreadyLive: true},
	})
	var got []protocol.Request
	fakeRequest(t, &got, mustData(t, protocol.OpenData{}), nil)

	m.Update(keyMsg("enter"))
	if m.view != viewTicketPicker || !strings.Contains(m.ticket.flash, "already") {
		t.Errorf("already-live ticket must be refused; view=%d flash=%q", m.view, m.ticket.flash)
	}
	for _, r := range got {
		if r.Cmd == "openTicket" {
			t.Error("must not start an already-live ticket")
		}
	}
}

// esc returns to detail.
func TestTicketPickerEscReturnsToDetail(t *testing.T) {
	m := ticketPickerRoot(t, nil)
	m.Update(keyMsg("esc"))
	if m.view != viewDetail {
		t.Fatalf("view = %d, want viewDetail", m.view)
	}
}

// detail 't' on a project with no team flashes a hint instead of opening.
func TestDetailTicketNoTeamFlashes(t *testing.T) {
	m := detailRoot(t) // nori-app has no team_id
	m.Update(keyMsg("t"))
	if m.view != viewDetail {
		t.Errorf("no-team ticket action must not navigate; view=%d", m.view)
	}
	if !strings.Contains(m.detail.flash, "team_id") {
		t.Errorf("expected a team_id hint, got %q", m.detail.flash)
	}
}
