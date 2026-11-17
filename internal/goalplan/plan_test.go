package goalplan

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/goal"
)

func inputFixture(t *testing.T) Input {
	t.Helper()
	r := Request{ID: goal.NewID(), Project: "p", Title: "Invite", Outcome: "Invite a colleague", Acceptance: []string{"Recipient accepts", "Expired invitation is rejected"}, NonGoals: []string{"Billing"}}
	if err := r.Normalize(); err != nil {
		t.Fatal(err)
	}
	return Input{Request: r, TeamID: "team", LinearProjectID: "project", Members: []Member{{ID: "api"}}, Labels: []Label{{ID: "label-api", Name: "API"}}}
}

const goodOutput = `{"tasks":[{"title":"Invitations","description":"Implement API and tests","acceptance":["Acceptance and expiry tests pass"],"apps":["api"],"label_ids":["label-api"],"covers":[1,2],"paths":{"api":["src/invitations","tests/invitations"]}}],"unknowns":["Mail transport configuration requires inspection"]}`

func TestProposalPreservesContractAndRejectsUnboundedOutput(t *testing.T) {
	in := inputFixture(t)
	out, err := Parse([]byte(goodOutput), in)
	if err != nil {
		t.Fatal(err)
	}
	d := out.Draft(in.Request)
	if d.Outcome != in.Request.Outcome || !slices.Equal(d.Acceptance, in.Request.Acceptance) || !slices.Equal(d.NonGoals, in.Request.NonGoals) {
		t.Fatal("model altered user contract")
	}
	for _, body := range []string{
		"```json\n" + goodOutput + "\n```", goodOutput + "{}", strings.Replace(goodOutput, `"covers":[1,2]`, `"covers":[1]`, 1),
		strings.Replace(goodOutput, `"covers":[1,2]`, `"covers":[1,1]`, 1), strings.Replace(goodOutput, `"api"`, `"unknown"`, 1),
		strings.Replace(goodOutput, `"label-api"`, `"invented"`, 1), strings.Replace(goodOutput, `"covers":[1,2]`, `"covers":[1,2],"blocked_by":[1]`, 1),
		strings.Replace(goodOutput, `"unknowns":`, `"publish":true,"unknowns":`, 1), strings.Repeat("x", 65537),
		strings.Replace(goodOutput, `"title":"Invitations"`, `"title":"Invitations","existing_issue_id":"12ab5678-1234-4234-9234-123456789abc"`, 1),
	} {
		if _, err := Parse([]byte(body), in); err == nil {
			t.Fatalf("accepted invalid proposal %s", body[:min(len(body), 100)])
		}
	}
}

func TestProposalReservationSurvivesRestartAndLostResponse(t *testing.T) {
	file := filepath.Join(t.TempDir(), "plans.json")
	s, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	in := inputFixture(t)
	r, fresh, err := s.Reserve(in, "codex", "image")
	if err != nil || !fresh {
		t.Fatal(err)
	}
	if again, fresh, err := s.Reserve(in, "claude", "other"); err != nil || fresh || again.GoalID != r.GoalID {
		t.Fatal("duplicate reservation", err)
	}
	reopened, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	interrupted, fresh, err := reopened.Reserve(in, "codex", "image")
	if err != nil || fresh || interrupted.State != "interrupted" {
		t.Fatal("interrupted call could run again", err)
	}
	in.Request.Outcome = "different"
	if _, _, err := reopened.Reserve(in, "codex", "image"); err == nil {
		t.Fatal("identity reused for new brief")
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0600 {
		t.Fatal("proposal evidence permissions")
	}
}

func TestProposalFailureAndAcceptanceAreDurable(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "plans.json"))
	in := inputFixture(t)
	r, _, _ := s.Reserve(in, "codex", "image")
	out, _ := Parse([]byte(goodOutput), in)
	r, err := s.Finish(r.ID, &out, nil)
	if err != nil || r.State != "proposed" {
		t.Fatal(err)
	}
	if _, err := s.Accept(r.ID, r.Revision-1); err == nil {
		t.Fatal("stale acceptance")
	}
	r, err = s.Accept(r.ID, r.Revision)
	if err != nil || r.State != "accepted" {
		t.Fatal(err)
	}
	reopened, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := reopened.Get(r.ID)
	if saved.State != "accepted" || saved.Output == nil {
		t.Fatal("lost accepted proposal")
	}
	in.Request.ID = goal.NewID()
	r, _, _ = s.Reserve(in, "codex", "image")
	r, err = s.Finish(r.ID, nil, errors.New("failed call"))
	if err != nil || r.State != "failed" {
		t.Fatal(err)
	}
	if _, fresh, _ := s.Reserve(in, "codex", "image"); fresh {
		t.Fatal("failed call retried")
	}
	// A failed durable intent must not authorize the caller to launch.
	in.Request.ID = goal.NewID()
	s.write = func(string, []byte) error { return errors.New("disk full") }
	if _, fresh, err := s.Reserve(in, "codex", "image"); err == nil || fresh {
		t.Fatal("launch permitted without durable reservation")
	}
}

func TestPlannerContainerIsBoundedAndDoesNotReceiveUpstreamCredentials(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "model-secret")
	t.Setenv("GH_TOKEN", "upstream-secret")
	s, _ := Open(filepath.Join(t.TempDir(), "plans.json"))
	r, _, _ := s.Reserve(inputFixture(t), "codex", "planner-image")
	calls := 0
	runner := Runner{PassEnv: []string{"GH_TOKEN", "OPENAI_API_KEY", "OPENAI_API_KEY"}, Run: func(_ context.Context, bin string, args []string, input []byte, env []string) ([]byte, error) {
		calls++
		if bin != "docker" {
			t.Fatal("wrong tool")
		}
		if args[0] == "rm" {
			return nil, nil
		}
		joined := strings.Join(args, " ")
		for _, want := range []string{"--read-only", "--pull never", "--cap-drop ALL", "--security-opt no-new-privileges", "--pids-limit 128", "--memory 1g", "--env OPENAI_API_KEY", "timeout -s TERM -k 10 180", "--skip-git-repo-check"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("missing %s", want)
			}
		}
		for _, bad := range []string{"--mount", "--volume", "GH_TOKEN", "model-secret", "upstream-secret", "--privileged", "--network host"} {
			if strings.Contains(joined, bad) {
				t.Fatalf("unsafe arguments %s", bad)
			}
		}
		if !slices.Contains(env, "OPENAI_API_KEY=model-secret") || strings.Contains(strings.Join(env, "\n"), "upstream-secret") {
			t.Fatal("bad credential isolation")
		}
		var got Input
		if err := json.Unmarshal(input, &got); err != nil || Digest(got) != r.InputSHA {
			t.Fatal("model input differs from reservation")
		}
		return fixtureAgentEvents(args, []byte(goodOutput)), nil
	}}
	if _, err := runner.Plan(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("cleanup not executed")
	}
}

func TestPlannerRejectsUnconfirmedCleanup(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "plans.json"))
	r, _, _ := s.Reserve(inputFixture(t), "claude", "image")
	for _, stillThere := range []bool{true, false} {
		runner := Runner{Run: func(_ context.Context, _ string, args []string, _ []byte, _ []string) ([]byte, error) {
			switch args[0] {
			case "run":
				return fixtureAgentEvents(args, []byte(goodOutput)), nil
			case "rm":
				return nil, errors.New("lost response")
			case "ps":
				if stillThere {
					return []byte("container"), nil
				}
				return nil, nil
			}
			panic("unexpected")
		}}
		_, err := runner.Plan(context.Background(), r)
		if (err != nil) != stillThere {
			t.Fatalf("cleanup evidence: %v", err)
		}
	}
}

func fixtureAgentEvents(args []string, body []byte) []byte {
	kind := "codex"
	inner := args[len(args)-1]
	if strings.Contains(inner, "'claude'") {
		kind = "claude"
	} else if strings.Contains(inner, "'opencode'") {
		kind = "opencode"
	}
	var events []any
	switch kind {
	case "claude":
		events = []any{map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": string(body), "usage": map[string]int{"input_tokens": 100, "output_tokens": 20}, "total_cost_usd": 0.001}}
	case "codex":
		events = []any{map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": string(body)}}, map[string]any{"type": "turn.completed", "usage": map[string]int{"input_tokens": 100, "cached_input_tokens": 20, "output_tokens": 20}}}
	case "opencode":
		events = []any{map[string]any{"type": "text", "part": map[string]string{"type": "text", "id": "t1", "messageID": "m1", "text": string(body)}}, map[string]any{"type": "step_finish", "part": map[string]any{"type": "step-finish", "messageID": "m1", "id": "s1", "reason": "stop", "cost": 0.001, "tokens": map[string]int{"input": 100, "output": 20}}}}
	}
	var out []byte
	for _, e := range events {
		b, _ := json.Marshal(e)
		out = append(out, append(b, '\n')...)
	}
	return out
}

func TestProposalPickerScopesAndOmitsExecutionReplans(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "plans.json"))
	in := inputFixture(t)
	first, _, err := s.Reserve(in, "codex", "image")
	if err != nil {
		t.Fatal(err)
	}
	in.Request.ID = goal.NewID()
	in.Request.Project = "other"
	second, _, err := s.Reserve(in, "codex", "image")
	if err != nil {
		t.Fatal(err)
	}
	in.Request.ID = goal.NewID()
	in.Request.Project = "p"
	in.Repair = &RepairContext{GoalID: first.GoalID}
	if _, _, err = s.Reserve(in, "codex", "image"); err != nil {
		t.Fatal(err)
	}
	got := s.List("p")
	if len(got) != 1 || got[0].ID != first.ID {
		t.Fatalf("scope or execution replan leaked: %+v", got)
	}
	got[0].Title = "mutated"
	if s.List("p")[0].Title == "mutated" {
		t.Fatal("picker aliases store")
	}
	all := s.List("")
	if len(all) != 2 || all[0].ID != second.ID {
		t.Fatalf("not newest first: %+v", all)
	}
	if len(s.List("missing")) != 0 {
		t.Fatal("invented project match")
	}
}
