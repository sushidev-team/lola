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
	m := detailRoot(t)
	m.detail.project = "A" // "A" is a polling project (has team_id)
	resp := mustData(t, protocol.TicketsData{Team: "team-1", Issues: issues})
	fakeRequest(t, nil, resp, nil)
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
