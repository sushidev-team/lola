package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/knowledge"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

func TestKnowledgeImprovementFromWorkerCaptureToReviewableExport(t *testing.T) {
	d, _, _ := goalFixture(t)
	body := `[{"topic":"Recovery","summary":"Persist reservations before starting work.","rationale":"Avoid duplicate work after a crash.","evidence":["Crash reproduction in worker log"]}]`
	for _, id := range []string{"one", "two"} {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, ".lola"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".lola", "learnings.json"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		d.sessions.Upsert(session.Session{ID: id, Project: "p1", Worktree: dir})
		for range 2 {
			if err := d.captureLearnings(id); err != nil {
				t.Fatal(err)
			}
		}
	}
	call := func(cmd string, a knowledgeArgs) protocol.Response {
		t.Helper()
		raw, _ := json.Marshal(a)
		return d.handle(context.Background(), protocol.Request{Cmd: cmd, Args: raw})
	}
	args := knowledgeArgs{Scope: knowledge.Scope{Project: "p1"}}
	response := call("knowledgeImprovements", args)
	if response.Error != "" {
		t.Fatal(response.Error)
	}
	var report knowledge.ImprovementReport
	if err := json.Unmarshal(response.Data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Total != 1 || report.Proposals[0].Occurrences != 2 {
		t.Fatalf("capture did not produce proposal: %+v", report)
	}
	p := report.Proposals[0]
	args.ID = p.ID
	args.Revision = "old"
	if call("knowledgeExportImprovement", args).Error == "" {
		t.Fatal("export accepted stale source revision")
	}
	args.Revision = p.Revision
	response = call("knowledgeExportImprovement", args)
	if response.Error != "" {
		t.Fatal(response.Error)
	}
	var files map[string]string
	if err := json.Unmarshal(response.Data, &files); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(files["IMPROVEMENT.md"], p.Problem) || !strings.Contains(files["IMPROVEMENT.md"], p.Sources[0].ID) {
		t.Fatal("export missing reported problem or provenance")
	}
	args.Decision = "tracked"
	args.Evidence = "Tracked as normal issue ENG-42"
	if call("knowledgeDecideImprovement", args).Error != "" {
		t.Fatal("could not record human triage")
	}
	for _, l := range d.knowledge.List() {
		if l.Status != knowledge.Candidate {
			t.Fatal("proposal authorized guidance")
		}
	}
	if len(d.goals.List()) != 0 {
		t.Fatal("proposal started goal work")
	}
}

func TestKnowledgeGoalBriefAndHistoricalEvidence(t *testing.T) {
	d, _, _ := goalFixture(t)
	p := knowledge.Proposal{Topic: "Goal recovery", Summary: "Record the exact repository combination before retrying."}
	own, err := d.knowledge.Capture(knowledge.Scope{Project: "p1", GoalID: "g"}, p, knowledge.Source{Kind: "correction", ID: "human"}, true)
	if err != nil {
		t.Fatal(err)
	}
	p.Summary = "This guidance belongs only to another experiment."
	if _, err = d.knowledge.Capture(knowledge.Scope{Project: "p1", GoalID: "other"}, p, knowledge.Source{Kind: "correction", ID: "other-human"}, true); err != nil {
		t.Fatal(err)
	}
	d.sessions.Upsert(session.Session{ID: "goal-worker", Project: "p1", Kind: session.KindGoal, GoalID: "g"})
	brief := string(d.knowledgeBrief("p1", "goal-worker", []byte("Goal contract")))
	if !strings.Contains(brief, own.Summary) || strings.Contains(brief, p.Summary) || strings.Contains(brief, "`.lola/learnings.json`") || !strings.Contains(brief, "`learnings.json`") || !strings.Contains(brief, "regression_test") {
		t.Fatal("goal learning instructions lost scope or capture schema")
	}
	if refs := d.knowledge.Delivery("goal-worker"); len(refs) != 1 || refs[0].ID != own.ID {
		t.Fatal("goal launch did not record supplied knowledge")
	}
	if err = d.knowledge.Dispute(own.ID, own.Version, "Experiment now contradicts the guidance"); err != nil {
		t.Fatal(err)
	}
	a := knowledgeArgs{Scope: own.Scope, ID: own.ID, Version: own.Version}
	raw, _ := json.Marshal(a)
	r := d.handle(context.Background(), protocol.Request{Cmd: "knowledgeVersion", Args: raw})
	if r.Error != "" {
		t.Fatal(r.Error)
	}
	var historic knowledge.Lesson
	if err = json.Unmarshal(r.Data, &historic); err != nil || historic.Status != knowledge.Active {
		t.Fatal("lost immutable source version", err)
	}
	a.Scope.GoalID = ""
	raw, _ = json.Marshal(a)
	if d.handle(context.Background(), protocol.Request{Cmd: "knowledgeVersion", Args: raw}).Error == "" {
		t.Fatal("goal evidence escaped scope")
	}
	if len(d.selectKnowledge(context.Background(), knowledge.Query{Project: "p1", GoalID: "g"}).Lessons) != 0 {
		t.Fatal("historical lookup reactivated disputed guidance")
	}
}
