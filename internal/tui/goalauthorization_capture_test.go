package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestCaptureGoalAuthorization(t *testing.T) {
	dir := os.Getenv("LOLA_TUI_CAPTURE_DIR")
	if dir == "" {
		t.Skip("set LOLA_TUI_CAPTURE_DIR for offline terminal fixtures")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, size := range []struct {
		name string
		w, h int
	}{{"wide", 120, 40}, {"compact", 80, 24}} {
		m, p := authorizationRoot(t)
		m.width = size.w
		m.height = size.h
		capture := func(state string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, "authorization-tui-"+size.name+"-"+state+".ansi"), []byte(strings.Join(m.goalsLines(), "\n")), 0600); err != nil {
				t.Fatal(err)
			}
		}
		capture("empty")
		authorizationFill(p)
		p.focus(authorizationKey(p.checks[0], "command"))
		m.Update(keyMsg("enter"))
		capture("editing")
		m.Update(keyMsg("esc"))
		p.focus("profile.0")
		capture("profiles")
		p.focus("review")
		capture("limits")
		p.editor.field("tokens").text = "0"
		m.Update(keyMsg("enter"))
		capture("error")
		p.editor.field("tokens").text = "500000"
		m.Update(keyMsg("enter"))
		capture("review")
		m.Update(keyMsg("end"))
		capture("review-end")
		p.uncertain = true
		p.err = "Authorization was not confirmed: socket closed. It may already be accepted. Close and refresh Goals before another action."
		capture("uncertain")
	}
}
func TestGoalAuthorizationViewportAndReviewContents(t *testing.T) {
	for _, width := range []int{80, 120} {
		m, p := authorizationRoot(t)
		m.width = width
		m.height = 24
		authorizationFill(p)
		p.options.Goal.Title = "Untrusted \x1b[2J\u202e title"
		p.focus(authorizationKey(p.checks[0], "command"))
		m.Update(keyMsg("enter"))
		view := strings.Join(m.goalsLines(), "\n")
		if !strings.Contains(view, "enter new line") || !strings.Contains(view, "Editing Command") {
			t.Fatal(view)
		}
		m.Update(keyMsg("esc"))
		authorizationReview(t, m, p)
		for _, key := range []string{"home", "end", "v"} {
			m.Update(keyMsg(key))
			lines := m.goalsLines()
			view = strings.Join(lines, "\n")
			if len(lines) != 24 || !strings.Contains(view, "a authorize and start") || strings.Contains(view, "\x1b[2J") || strings.ContainsRune(view, '\u202e') {
				t.Fatal("unsafe or missing controls", view)
			}
			for _, line := range lines {
				if lipgloss.Width(line) > width {
					t.Fatal("overflow", line)
				}
			}
		}
		p.raw = false
		review := strings.Join(p.reviewLines(), "\n")
		for _, want := range []string{"reviewed revision 7", "/fixtures/nori-api", "Paths: src, tests", "./verify invitations\nprintf 'done\\n'", "flow: interaction from app/evidence/flow.json", "Observed spend (USD): 12.5", "Total agent calls: 30", "not exact billing caps", "Do not change billing roles"} {
			if !strings.Contains(review, want) {
				t.Fatalf("missing %s\n%s", want, review)
			}
		}
	}
}
