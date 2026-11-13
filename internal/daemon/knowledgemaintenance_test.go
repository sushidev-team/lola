package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitrepo"
	"github.com/sushidev-team/lola/internal/knowledge"
	"github.com/sushidev-team/lola/internal/protocol"
)

func watchedLesson(t *testing.T) (*Daemon, knowledge.Lesson, *gitrepo.CodeIdentity) {
	t.Helper()
	d, _, _ := goalFixture(t)
	d.cfg.ProjectByName("p1").Path = t.TempDir()
	code := &gitrepo.CodeIdentity{Commit: strings.Repeat("a", 40), Digest: strings.Repeat("b", 64), Exists: true}
	d.knowledgeCodeRead = func(context.Context, string, string, string) (gitrepo.CodeIdentity, error) { return *code, nil }
	l, err := d.knowledge.Capture(knowledge.Scope{Project: "p1"}, knowledge.Proposal{Topic: "Storage", Summary: "Persist reservations before dispatching work."}, knowledge.Source{Kind: "worker", ID: "s"}, false)
	if err != nil {
		t.Fatal(err)
	}
	l, err = d.acceptKnowledge(context.Background(), knowledgeArgs{ID: l.ID, Version: l.Version, Evidence: "Verified recovery test", CodeTargets: []knowledge.CodeTarget{{Member: "p1", Path: "internal"}}})
	if err != nil {
		t.Fatal(err)
	}
	return d, l, code
}

func TestKnowledgeMaintenanceAndReacceptance(t *testing.T) {
	d, l, code := watchedLesson(t)
	if got := d.selectKnowledge(context.Background(), knowledge.Query{Project: "p1"}); len(got.Lessons) != 1 {
		t.Fatal("current lesson missing")
	}
	code.Commit = strings.Repeat("c", 40)
	if got := d.selectKnowledge(context.Background(), knowledge.Query{Project: "p1"}); len(got.Lessons) != 1 {
		t.Fatal("unrelated commit suppressed lesson")
	}
	code.Digest = strings.Repeat("d", 64)
	if got := string(d.knowledgeBrief("p1", "new-session", nil)); strings.Contains(got, l.Summary) {
		t.Fatal("stale lesson injected")
	}
	changed := d.knowledge.List()[0]
	if changed.Status != knowledge.Candidate || changed.ReviewReason != "referenced code changed" {
		t.Fatalf("not demoted: %+v", changed)
	}
	// Missing --watch retains and refreshes previous targets instead of dropping them.
	accepted, err := d.acceptKnowledge(context.Background(), knowledgeArgs{ID: changed.ID, Version: changed.Version, Evidence: "Reproduced with changed code"})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Code[0].Digest != code.Digest || accepted.Code[0].Path != "internal" {
		t.Fatal("did not reacquire anchors")
	}
	d.knowledge, err = knowledge.Open(filepath.Join(d.home, "state", "knowledge.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.selectKnowledge(context.Background(), knowledge.Query{Project: "p1"}).Lessons) != 1 {
		t.Fatal("restart lost fresh acceptance")
	}
	code.Exists = false
	if len(d.selectKnowledge(context.Background(), knowledge.Query{Project: "p1"}).Lessons) != 0 {
		t.Fatal("deleted reference still active")
	}
}

func TestKnowledgeUnavailableExcludesLookupExportAndBrief(t *testing.T) {
	d, l, code := watchedLesson(t)
	d.knowledgeCodeRead = func(context.Context, string, string, string) (gitrepo.CodeIdentity, error) {
		return gitrepo.CodeIdentity{}, errors.New("offline")
	}
	for _, cmd := range []string{"knowledgeLookup", "knowledgeExport", "knowledgeMaintain"} {
		args, _ := json.Marshal(knowledgeArgs{Scope: knowledge.Scope{Project: "p1"}, Query: knowledge.Query{Project: "p1"}})
		r := d.handleKnowledge(context.Background(), protocol.Request{Cmd: cmd, Args: args})
		body, _ := json.Marshal(r)
		if r.Error != "" || strings.Contains(string(body), l.Summary) {
			t.Fatalf("%s leaked unavailable lesson: %s", cmd, body)
		}
		if cmd == "knowledgeMaintain" && !strings.Contains(string(body), "unavailable") {
			t.Fatal("missing diagnostic")
		}
	}
	if strings.Contains(string(d.knowledgeBrief("p1", "offline-session", nil)), l.Summary) {
		t.Fatal("unavailable guidance injected")
	}
	if got := d.knowledge.List()[0]; got.Status != knowledge.Active || got.Version != l.Version {
		t.Fatal("transient failure destroyed proof")
	}
	d.knowledgeCodeRead = func(context.Context, string, string, string) (gitrepo.CodeIdentity, error) { return *code, nil }
	if len(d.selectKnowledge(context.Background(), knowledge.Query{Project: "p1"}).Lessons) != 1 {
		t.Fatal("successful retry did not restore availability")
	}
}

func TestKnowledgeMaintenanceExpiryScopeAndConcurrentCuration(t *testing.T) {
	d, l, code := watchedLesson(t)
	d.cfg.Projects = append(d.cfg.Projects, config.Project{Name: "other", Path: t.TempDir()})
	if _, err := d.acceptKnowledge(context.Background(), knowledgeArgs{ID: l.ID, Version: l.Version, Evidence: "Evidence for other repo", CodeTargets: []knowledge.CodeTarget{{Member: "other", Path: "."}}}); err == nil {
		t.Fatal("cross-project anchor accepted")
	}
	d.knowledgeCodeRead = func(context.Context, string, string, string) (gitrepo.CodeIdentity, error) {
		if err := d.knowledge.Dispute(l.ID, l.Version, "Operator found a contradiction"); err != nil {
			t.Fatal(err)
		}
		return *code, nil
	}
	if len(d.selectKnowledge(context.Background(), knowledge.Query{Project: "p1"}).Lessons) != 0 {
		t.Fatal("concurrent dispute ignored")
	}
	// Expired lessons are durably returned to candidates by explicit maintenance.
	d, l, _ = watchedLesson(t)
	l, err := d.knowledge.AcceptWithCode(l.ID, l.Version, "Verified but time limited", nil, time.Now().Add(-time.Hour), l.Code)
	if err != nil {
		t.Fatal(err)
	}
	_, reports := d.maintainKnowledge(context.Background(), d.knowledge.List())
	if reports[0].State != "expired" || d.knowledge.List()[0].Status != knowledge.Candidate {
		t.Fatal("expiry not recorded")
	}
}

func TestKnowledgeScopeChangesAndBoundedReads(t *testing.T) {
	for _, change := range []string{"branch", "checkout", "removed"} {
		t.Run(change, func(t *testing.T) {
			d, _, _ := watchedLesson(t)
			switch change {
			case "branch":
				d.cfg.ProjectByName("p1").DefaultBranch = "release"
			case "checkout":
				d.cfg.ProjectByName("p1").Path = t.TempDir()
			case "removed":
				d.cfg.Projects = nil
			}
			valid, reports := d.maintainKnowledge(context.Background(), d.knowledge.List())
			expected := "changed"
			if change == "removed" {
				expected = "unavailable"
			}
			if len(valid) != 0 || reports[0].State != expected {
				t.Fatalf("unexpected report: %+v", reports)
			}
		})
	}
	d, l, _ := watchedLesson(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.knowledgeCodeRead = func(context.Context, string, string, string) (gitrepo.CodeIdentity, error) {
		t.Fatal("read after deadline")
		return gitrepo.CodeIdentity{}, nil
	}
	if got := d.selectKnowledge(ctx, knowledge.Query{Project: "p1"}); len(got.Lessons) != 0 {
		t.Fatal("deadline ignored")
	}
	if got := d.knowledge.List()[0]; got.Version != l.Version || got.Status != knowledge.Active {
		t.Fatal("deadline demoted proof")
	}
	// Stack membership is checked before Git reads, including reassignment.
	d.cfg.Projects = append(d.cfg.Projects, config.Project{Name: "stack", Kind: config.ProjectKindStack})
	d.cfg.ProjectByName("p1").Stack = "stack"
	if _, _, err := d.knowledgeRepository("stack", "p1"); err != nil {
		t.Fatal(err)
	}
	d.cfg.ProjectByName("p1").Stack = "another-stack"
	if _, _, err := d.knowledgeRepository("stack", "p1"); err == nil {
		t.Fatal("reassigned member still in scope")
	}
}
