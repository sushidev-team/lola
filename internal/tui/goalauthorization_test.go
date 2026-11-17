package tui

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/protocol"
)

func authorizationOptions() goal.StartOptions {
	return goal.StartOptions{
		Goal:          goal.Overview{ID: "g", Revision: 7, Project: "nori-app", Title: "Invite teammates", State: "planned", Outcome: "Owners can invite teammates and revoke pending invitations.", Acceptance: []string{"Invitations work across API and UI."}, NonGoals: []string{"Do not change billing roles."}, Tasks: []goal.TaskView{{ID: "t", Title: "Implement invitations", Apps: []string{"app", "api"}, Acceptance: []string{"Revoked invitations cannot be accepted."}}}},
		Authorization: goal.Authorization{ID: "g", Revision: 7, Policy: goalpolicy.Policy{Members: []goalpolicy.Member{{ID: "app", Project: "nori-app", Source: "/fixtures/nori-app", Repo: "example/nori-app", Target: "main"}, {ID: "api", Project: "nori-api", Source: "/fixtures/nori-api", Repo: "example/nori-api", Target: "main"}}, Limits: goal.SuggestedLimits(1), Profiles: []string{"backend"}}},
		Profiles:      []goal.StartProfile{{ID: "backend", Kind: "backend", Required: true}, {ID: "ux", Kind: "uiux", Artifacts: []string{"screenshot", "interaction"}}},
	}
}
func authorizationRoot(t *testing.T) (*rootModel, *goalAuthorizationModel) {
	t.Helper()
	m := goalsRoot(t)
	m.goals.rows = []goal.Overview{authorizationOptions().Goal}
	m.goals.detail = true
	m.openGoalAuthorization()
	p := m.goals.authorization
	b, _ := json.Marshal(authorizationOptions())
	m.Update(goalAuthorizationMsg{project: m.goals.project, id: p.id, gen: m.goals.gen, seq: p.seq, op: "options", data: b})
	if p.editor == nil {
		t.Fatal(p.err)
	}
	return m, p
}
func authorizationFill(p *goalAuthorizationModel) {
	p.addCheck(goalpolicy.Check{ID: "acceptance", Member: "api", Command: "./verify invitations\nprintf 'done\\n'", Image: "sha256:" + strings.Repeat("a", 64), TimeoutSeconds: 300, MemoryMiB: 4096, Artifacts: []goalpolicy.ArtifactSpec{{ID: "flow", Kind: "interaction", Path: "app/evidence/flow.json"}}})
	p.editor.field("member.0").lines = []string{"src", "tests"}
	p.editor.field("spend_usd").text = "12.50"
	p.editor.field("publish_pr").b = true
	p.editor.field("compatibility").lines = []string{"Keep the previous app compatible with the new API."}
	p.editor.field("merge_order").lines = []string{"api", "app"}
	p.editor.field("replan").b = true
}
func authorizationReview(t *testing.T, m *rootModel, p *goalAuthorizationModel) {
	t.Helper()
	p.focus("review")
	m.Update(keyMsg("enter"))
	if p.reviewed == nil {
		t.Fatal(p.err)
	}
}
func TestGoalAuthorizationReadOnlyOptionsAndFrozenStart(t *testing.T) {
	m := goalsRoot(t)
	m.goals.rows = []goal.Overview{authorizationOptions().Goal}
	m.goals.detail = true
	old := requestFn
	defer func() { requestFn = old }()
	calls := []string{}
	var submitted goal.Authorization
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		calls = append(calls, req.Cmd)
		if req.Cmd == "goalStartOptions" {
			var args map[string]string
			_ = json.Unmarshal(req.Args, &args)
			if args["id"] != "g" || len(args) != 1 {
				t.Fatal(string(req.Args))
			}
			return plannerResponse(t, authorizationOptions()), nil
		}
		if req.Cmd != protocol.GoalStartV2 {
			t.Fatal(req.Cmd)
		}
		if err := json.Unmarshal(req.Args, &submitted); err != nil {
			t.Fatal(err)
		}
		return &protocol.Response{OK: true}, nil
	}
	_, cmd := m.Update(keyMsg("a"))
	if cmd == nil {
		t.Fatal("missing options request")
	}
	m.Update(cmd())
	p := m.goals.authorization
	authorizationFill(p)
	if m.refreshGoals() != nil {
		t.Fatal("overview replaced form")
	}
	authorizationReview(t, m, p)
	if _, cmd = m.Update(keyMsg("a")); cmd != nil {
		t.Fatal("start without acknowledgment")
	}
	expected := *p.reviewed
	if expected.Policy.Checks[0].MemoryMiB != 4096 {
		t.Fatal("review dropped the accepted check memory limit")
	}
	p.editor.field("tokens").text = "1"
	p.options.Authorization.Policy.Members[0].Target = "edited"
	m.Update(keyMsg("space"))
	_, cmd = m.Update(keyMsg("a"))
	if cmd == nil || !p.busy {
		t.Fatal("no start")
	}
	p.reviewed.Policy.Limits.Tokens = 2 // mutation after dispatch must not alter submitted bytes
	for _, key := range []string{"a", "space", "esc", "ctrl+c"} {
		if _, duplicate := m.Update(keyMsg(key)); duplicate != nil {
			t.Fatal("busy action", key)
		}
	}
	if m.goals.authorization == nil {
		t.Fatal("left pending authorization")
	}
	_, refresh := m.Update(cmd())
	if refresh == nil || m.goals.authorization != nil {
		t.Fatal("success did not refresh overview")
	}
	if !reflect.DeepEqual(submitted, expected) || submitted.Revision != 7 || submitted.Policy.Members[0].Target != "main" || submitted.Policy.Limits.Tokens != 500000 {
		t.Fatalf("changed reviewed policy: %#v", submitted)
	}
	if !reflect.DeepEqual(calls, []string{"goalStartOptions", protocol.GoalStartV2}) {
		t.Fatal(calls)
	}
}
func TestGoalAuthorizationUncertainResultCannotRetry(t *testing.T) {
	m, p := authorizationRoot(t)
	authorizationFill(p)
	authorizationReview(t, m, p)
	old := requestFn
	defer func() { requestFn = old }()
	calls := 0
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		calls++
		return nil, errors.New("socket closed")
	}
	m.Update(keyMsg("space"))
	_, cmd := m.Update(keyMsg("a"))
	m.Update(cmd())
	if !p.uncertain || p.acknowledged || !strings.Contains(p.err, "may already be accepted") {
		t.Fatal(p.err)
	}
	for _, key := range []string{"a", "space", "f", "enter"} {
		if _, cmd = m.Update(keyMsg(key)); cmd != nil {
			t.Fatal("uncertain mutation retried")
		}
	}
	_, cmd = m.Update(keyMsg("esc"))
	if cmd == nil || m.goals.authorization != nil || !m.goals.loading || calls != 1 {
		t.Fatal("must close and read current goals")
	}
}
func TestGoalAuthorizationPolicyValidationAndEvidenceEditing(t *testing.T) {
	m, p := authorizationRoot(t)
	if _, err := p.authorization(); err == nil {
		t.Fatal("missing checks allowed")
	}
	p.action("add_check")
	serial := p.checks[0]
	if p.editor.cur().key != authorizationKey(serial, "id") {
		t.Fatal("new check not focused")
	}
	p.editor.field(authorizationKey(serial, "command")).lines = []string{"true"}
	p.editor.field(authorizationKey(serial, "image")).text = "image:latest"
	if _, err := p.authorization(); err == nil {
		t.Fatal("mutable image allowed")
	}
	p.editor.field(authorizationKey(serial, "image")).text = "sha256:" + strings.Repeat("a", 64)
	p.action(authorizationKey(serial, "add_evidence"))
	n := p.artifacts[serial][0]
	prefix := authorizationKey(serial, "evidence."+strconv.Itoa(n)+".")
	p.editor.field(prefix + "id").text = "screenshot"
	p.editor.field(prefix + "kind").text = "screenshot"
	p.editor.field(prefix + "path").text = "../bad.png"
	if _, err := p.authorization(); err == nil {
		t.Fatal("unsafe evidence path")
	}
	p.editor.field(prefix + "path").text = "app/screenshot.png"
	p.focus("profile.0")
	m.Update(keyMsg("space"))
	if !p.editor.field("profile.0").b {
		t.Fatal("required reviewer disabled")
	}
	p.focus("profile.1")
	m.Update(keyMsg("space"))
	a, err := p.authorization()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Policy.Profiles, []string{"backend", "ux"}) || a.Policy.Checks[0].Artifacts[0].Path != "app/screenshot.png" {
		t.Fatal(a)
	}
	for _, test := range []struct{ key, value string }{{"concurrency", "0"}, {"review_rounds", "4"}, {"attempt_minutes", "121"}, {"tokens", "9223372036854775808"}, {"spend_usd", "NaN"}, {"spend_usd", "-1"}, {"target_refreshes", "4"}, {"max_tasks", "0"}} {
		previous := p.editor.field(test.key).text
		p.editor.field(test.key).text = test.value
		if _, err = p.authorization(); err == nil {
			t.Fatal("accepted invalid limit", test)
		}
		p.editor.field(test.key).text = previous
	}
	p.action(prefix + "remove")
	if len(p.artifacts[serial]) != 0 || p.editor.cur().key != authorizationKey(serial, "add_evidence") {
		t.Fatal("evidence removal")
	}
	p.action(authorizationKey(serial, "remove"))
	if len(p.checks) != 0 || p.editor.cur().key != "add_check" {
		t.Fatal("check removal")
	}
}
func TestGoalAuthorizationDraftProtectionAndReviewReturn(t *testing.T) {
	m, p := authorizationRoot(t)
	authorizationFill(p)
	for _, editing := range []bool{false, true} {
		p.focus(authorizationKey(p.checks[0], "command"))
		if editing {
			m.Update(keyMsg("enter"))
		}
		if _, cmd := m.Update(keyMsg("ctrl+c")); cmd != nil || m.goals.authorization != p {
			t.Fatal("Ctrl+C lost policy")
		}
		if editing {
			m.Update(keyMsg("ctrl+s"))
			if p.editor.editing {
				t.Fatal("editor did not close")
			}
		}
	}
	authorizationReview(t, m, p)
	m.Update(keyMsg("space"))
	m.Update(keyMsg("esc"))
	if p.reviewed != nil || p.acknowledged || p.editor.field("spend_usd").text != "12.50" {
		t.Fatal("back lost draft or kept approval")
	}
	m.Update(keyMsg("esc"))
	if !p.discard {
		t.Fatal("no discard prompt")
	}
	m.Update(keyMsg("ctrl+c"))
	if !p.discard {
		t.Fatal("Ctrl+C bypassed discard")
	}
	m.Update(keyMsg("n"))
	if m.goals.authorization != p || p.discard {
		t.Fatal("keep editing")
	}
	m.Update(keyMsg("esc"))
	_, cmd := m.Update(keyMsg("y"))
	if cmd == nil || m.goals.authorization != nil {
		t.Fatal("discard did not close")
	}
}
func TestGoalAuthorizationRejectsStaleAndMismatchedOptions(t *testing.T) {
	for _, mutate := range []func(*goal.StartOptions){
		func(o *goal.StartOptions) { o.Goal.Project = "elsewhere" }, func(o *goal.StartOptions) { o.Authorization.Revision++ }, func(o *goal.StartOptions) { o.Goal.Authorized = true }, func(o *goal.StartOptions) { o.Goal.State = "draft" }, func(o *goal.StartOptions) { o.Authorization.ID = "other" }, func(o *goal.StartOptions) { o.Authorization.Policy.Members = nil },
	} {
		m := goalsRoot(t)
		m.goals.rows = []goal.Overview{authorizationOptions().Goal}
		m.openGoalAuthorization()
		p := m.goals.authorization
		options := authorizationOptions()
		mutate(&options)
		b, _ := json.Marshal(options)
		m.Update(goalAuthorizationMsg{project: m.goals.project, id: p.id, gen: m.goals.gen, seq: p.seq, op: "options", data: b})
		if p.editor != nil || p.err == "" {
			t.Fatal("invalid options accepted")
		}
	}
	m, p := authorizationRoot(t)
	editor := p.editor
	for _, msg := range []goalAuthorizationMsg{{project: "other", id: p.id, gen: m.goals.gen, seq: p.seq}, {project: m.goals.project, id: p.id, gen: m.goals.gen - 1, seq: p.seq}, {project: m.goals.project, id: p.id, gen: m.goals.gen, seq: p.seq - 1}} {
		m.Update(msg)
	}
	if p.editor != editor || p.err != "" {
		t.Fatal("stale reply mutated current editor")
	}
	m.goals.authorization = nil
	if _, cmd := m.Update(goalAuthorizationMsg{project: m.goals.project, id: p.id, gen: m.goals.gen, seq: p.seq, op: "start"}); cmd != nil {
		t.Fatal("late success acted after leaving")
	}
}
