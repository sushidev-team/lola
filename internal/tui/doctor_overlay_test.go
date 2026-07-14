package tui

import (
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
)

// 'd' in the polls view opens the doctor overlay; the completed report renders
// in a scrollable panel and esc closes it.
func TestDoctorOverlayOpensAndCloses(t *testing.T) {
	m := newTestRoot(t)

	_, cmd := m.Update(keyMsg("d"))
	if !m.doctorLoading {
		t.Fatal("'d' must start the doctor run (doctorLoading)")
	}
	if cmd == nil {
		t.Fatal("'d' must return the doctor command")
	}
	if !strings.Contains(m.View(), "running checks") {
		t.Errorf("loading overlay should show a running hint:\n%s", m.View())
	}

	m.Update(cmd()) // run doctor.Check and feed back the report
	if m.doctorReport == nil {
		t.Fatal("report must be stored after the command completes")
	}
	v := m.View()
	for _, want := range []string{"doctor", "tmux", "git"} {
		if !strings.Contains(v, want) {
			t.Errorf("overlay missing %q:\n%s", want, v)
		}
	}

	m.Update(keyMsg("esc"))
	if m.doctorReport != nil || m.doctorLoading {
		t.Error("esc must close the doctor overlay")
	}
	if strings.Contains(m.View(), "running checks") {
		t.Error("overlay must be gone after esc")
	}
}

// A late report arriving after esc (overlay already closed) is dropped rather
// than reopening the panel.
func TestDoctorLateReportDropped(t *testing.T) {
	m := newTestRoot(t)
	_, cmd := m.Update(keyMsg("d"))
	m.Update(keyMsg("esc")) // cancel while the command is still "in flight"
	m.Update(cmd())         // the report lands after cancellation
	if m.doctorReport != nil {
		t.Error("a report arriving after esc must not reopen the overlay")
	}
}

// Delete moved to 'x'; it still guards on a selected poll.
func TestDeleteKeyRebindToX(t *testing.T) {
	m := newTestRoot(t)
	m.Update(keyMsg("x"))
	if !m.list.confirmDelete {
		t.Error("'x' must arm the delete confirmation")
	}
}

// The polls header names the missing tool from RuntimeErr, and points at
// doctor only when RuntimeErr is empty.
func TestRuntimeHeaderNamesTool(t *testing.T) {
	m := newTestRoot(t)
	m.list.status = &protocol.StatusData{RuntimeOK: false, RuntimeErr: "missing claude"}
	if v := m.listView(); !strings.Contains(v, "missing claude") {
		t.Errorf("header must name the missing tool:\n%s", v)
	}

	m.list.status = &protocol.StatusData{RuntimeOK: false, RuntimeErr: ""}
	if v := m.listView(); !strings.Contains(v, "press d for doctor") {
		t.Errorf("empty RuntimeErr must hint at doctor:\n%s", v)
	}
}
