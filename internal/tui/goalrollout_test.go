package tui

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/protocol"
)

func TestGoalRolloutRequiresCompleteOrderAndFreezesUncertainRequest(t *testing.T) {
	m, p := authorizationRoot(t)
	authorizationFill(p)
	p.editor.field("compatibility").lines = nil
	if _, err := p.authorization(); err == nil {
		t.Fatal("missing compatibility accepted")
	}
	p.focus("compatibility")
	m.Update(keyMsg("enter"))
	m.Update(tea.PasteMsg{Content: "Keep previous clients working.\nKeep new features disabled until the API deployment."})
	m.Update(keyMsg("esc"))
	p.editor.field("merge_order").lines = []string{"api", "api"}
	if _, err := p.authorization(); err == nil {
		t.Fatal("duplicate merge member accepted")
	}
	p.editor.field("merge_order").lines = []string{"api", "foreign"}
	if _, err := p.authorization(); err == nil {
		t.Fatal("foreign merge member accepted")
	}
	p.editor.field("merge_order").lines = []string{"api", "app"}
	authorizationReview(t, m, p)
	want := &goalpolicy.Rollout{Compatibility: []string{"Keep previous clients working.", "Keep new features disabled until the API deployment."}, MergeOrder: []string{"api", "app"}}
	if !reflect.DeepEqual(p.reviewed.Policy.Rollout, want) {
		t.Fatalf("paste or review lost rollout: %+v", p.reviewed.Policy.Rollout)
	}
	m.Update(tea.PasteMsg{Content: "must not change the reviewed draft"})
	if p.editor.field("compatibility").lines[0] != want.Compatibility[0] {
		t.Fatal("review allowed a paste to alter its draft")
	}
	p.editor.field("compatibility").lines[0] = "edited after review"
	old := requestFn
	defer func() { requestFn = old }()
	calls := 0
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		calls++
		var a goal.Authorization
		if err := json.Unmarshal(req.Args, &a); err != nil {
			t.Fatal(err)
		}
		if req.Cmd != protocol.GoalStartV2 || a.Revision != 7 || !reflect.DeepEqual(a.Policy.Rollout, want) {
			t.Fatal("submitted policy differed from review")
		}
		return &protocol.Response{Error: "reply lost"}, nil
	}
	m.Update(keyMsg("space"))
	_, cmd := m.Update(keyMsg("a"))
	if cmd == nil {
		t.Fatal("missing reviewed authorization")
	}
	m.Update(cmd())
	if !p.uncertain || !reflect.DeepEqual(p.reviewed.Policy.Rollout, want) {
		t.Fatal("uncertain request lost its contract")
	}
	if _, retry := m.Update(keyMsg("a")); retry != nil || calls != 1 {
		t.Fatal("uncertain authorization retried")
	}
}

func TestGoalRolloutDisplayAndDisabledPublication(t *testing.T) {
	m, p := authorizationRoot(t)
	authorizationFill(p)
	p.editor.field("publish_pr").b = false
	a, err := p.authorization()
	if err != nil || a.Policy.Rollout != nil || len(p.editor.field("compatibility").lines) == 0 {
		t.Fatal("disabled publication retained authority or erased draft", err)
	}
	p.editor.field("publish_pr").b = true
	authorizationReview(t, m, p)
	text := strings.Join(p.reviewLines(), "\n")
	if !strings.Contains(text, "1. api\n  2. app") || !strings.Contains(text, p.reviewed.Policy.Rollout.Compatibility[0]) {
		t.Fatal("review order changed")
	}
	m.goals.authorization = nil
	g := m.goals.current()
	g.Authorized = true
	g.Members = []goal.MemberView{{ID: "api", Repo: "team/api", Target: "main"}, {ID: "app", Repo: "team/app", Target: "main"}}
	g.Rollout = p.reviewed.Policy.Rollout
	m.width, m.height = 80, 24
	m.Update(keyMsg("end"))
	view := strings.Join(m.goalsLines(), "\n")
	if !strings.Contains(view, "Human merge order") || !strings.Contains(view, "1. api") {
		t.Fatal("compact overview lost rollout", view)
	}
	g.Rollout = nil
	m.goals.scroll = 999
	view = strings.Join(m.goalsLines(), "\n")
	if !strings.Contains(view, "No reviewed rollout contract was recorded.") || !strings.Contains(view, "merge recommendation.") {
		t.Fatal("legacy overview implied a reviewed merge order", view)
	}
}
