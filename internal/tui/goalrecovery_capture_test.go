package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/goal"
)

func TestCaptureGoalRecovery(t *testing.T) {
	dir := os.Getenv("LOLA_TUI_CAPTURE_DIR")
	if dir == "" {
		t.Skip("set LOLA_TUI_CAPTURE_DIR for offline captures")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, size := range []struct {
		name string
		w, h int
	}{{"wide", 120, 40}, {"compact", 80, 24}} {
		m, p := recoveryRoot(t)
		m.width, m.height = size.w, size.h
		capture := func(state string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, "recovery-tui-"+size.name+"-"+state+".ansi"), []byte(strings.Join(m.goalsLines(), "\n")), 0600); err != nil {
				t.Fatal(err)
			}
		}
		capture("inspect")
		p.choose("accept-brief")
		p.editor.field("outcome").lines = []string{"Owners can invite teammates and revoke pending invitations, with keyboard access and validation beside the affected field."}
		p.editor.field("reason").lines = []string{"The existing invitation task already covers keyboard interaction and validation.", "Retain its scope, checks and budgets."}
		recoveryFocus(p, "reason")
		capture("fields")
		m.Update(keyMsg("enter"))
		capture("editing")
		m.Update(keyMsg("esc"))
		m.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
		p.scroll = 8
		capture("source")
		m.Update(keyMsg("esc"))
		recoveryReview(t, m, p)
		capture("review")
		p.scroll = 1 << 20
		capture("review-end")
		p.uncertain = true
		p.err = "Action was not confirmed: fixture reply lost after saving. It may already have happened. Reload the local snapshot; do not repeat this request."
		capture("uncertain")
		s := recoverySnapshot()
		s.Goal.Revision = 8
		s.Goal.State = "paused"
		s.Goal.SyncError = ""
		s.Goal.Reason = "Brief revision accepted; review the retained task plan before resuming."
		s.PendingBrief = nil
		s.LatestBrief = &goal.BriefDecisionView{Before: s.Brief, After: p.reviewed.contract, SourceID: p.reviewed.snapshot.PendingBrief.ID, Reason: p.reviewed.reason, AcceptedAt: time.Date(2026, 10, 22, 8, 30, 0, 0, time.UTC)}
		s.Brief = p.reviewed.contract
		s.Actions = []string{"sync-brief", "dismiss-progress"}
		p.snapshot = &s
		p.reviewed = nil
		p.editor = nil
		p.action = ""
		p.uncertain = false
		p.err = ""
		p.scroll = 12
		capture("recovered")
		p.choose("dismiss-progress")
		p.editor.field("reason").lines = []string{"Inspected the parent issue in Linear. Keep this uncertain send in history and allow later updates."}
		recoveryReview(t, m, p)
		capture("progress")
		p.snapshot.Actions = []string{"delivery-evidence"}
		p.reviewed = nil
		p.editor = nil
		p.action = ""
		p.choose("delivery-evidence")
		p.editor.field("url").text = "https://artifacts.example/nori/invitations/verified-runtime-and-screenshots"
		recoveryReview(t, m, p)
		capture("evidence")
		p.snapshot.Actions = []string{"retry-verification"}
		p.reviewed = nil
		p.editor = nil
		p.action = ""
		p.choose("retry-verification")
		p.editor.field("reason").lines = []string{"The interrupted container has stopped. Retry within the existing review budget."}
		recoveryReview(t, m, p)
		capture("retry")
	}
}
