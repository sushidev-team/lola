package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/knowledge"
	"github.com/sushidev-team/lola/internal/session"
)

func TestKnowledgeCapturesUntrustedWorkerFile(t *testing.T) {
	d, _, _ := goalFixture(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".lola"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `[{"topic":"Dispatch ordering","summary":"Persist reservations before starting an external effect.","rationale":"Avoid duplicate work after a crash","evidence":["my tests pass"],"status":"active","verification":"claimed by worker"}]`
	if err := os.WriteFile(filepath.Join(dir, ".lola", "learnings.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	d.sessions.Upsert(session.Session{ID: "s", Project: "p1", Worktree: dir})
	for range 2 {
		if err := d.captureLearnings("s"); err != nil {
			t.Fatal(err)
		}
	}
	lessons := d.knowledge.List()
	if len(lessons) != 1 || lessons[0].Status != knowledge.Candidate || lessons[0].Verification != "" || lessons[0].Source.Session != "s" {
		t.Fatalf("worker claim trusted: %+v", lessons)
	}
	if got := knowledge.Select(lessons, knowledge.Query{Project: "p1"}, time.Now()); len(got.Lessons) != 0 {
		t.Fatal("unverified finding injected")
	}
}

func TestKnowledgeCaptureRefusesEscapingSymlink(t *testing.T) {
	d, _, _ := goalFixture(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".lola"), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private.json")
	if err := os.WriteFile(outside, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, ".lola", "learnings.json")); err != nil {
		t.Fatal(err)
	}
	d.sessions.Upsert(session.Session{ID: "s", Project: "p1", Worktree: dir})
	if err := d.captureLearnings("s"); err == nil {
		t.Fatal("read external symlink")
	}
	if len(d.knowledge.List()) != 0 {
		t.Fatal("symlink changed knowledge")
	}
}

func TestKnowledgeBriefRecordsVersionsAndIsolation(t *testing.T) {
	d, _, _ := goalFixture(t)
	p := knowledge.Proposal{Topic: "Shutdown ordering", Summary: "Drain outstanding writes before shutting down workers.", Evidence: []string{"explicit operator correction"}}
	l, err := d.knowledge.Capture(knowledge.Scope{Project: "p1"}, p, knowledge.Source{Kind: "correction", ID: "h"}, true)
	if err != nil {
		t.Fatal(err)
	}
	brief := string(d.knowledgeBrief("p1", "session-1", []byte("Implement shutdown")))
	if !strings.Contains(brief, p.Summary) || !strings.Contains(brief, ".lola/learnings.json") {
		t.Fatal("brief missing lesson or automatic capture instructions")
	}
	refs := d.knowledge.Delivery("session-1")
	if len(refs) != 1 || refs[0].ID != l.ID || refs[0].Version != l.Version {
		t.Fatal("missing delivery provenance")
	}
	if got := string(d.knowledgeBrief("another", "session-2", nil)); strings.Contains(got, p.Summary) {
		t.Fatal("unrelated project contaminated")
	}
	if err := d.knowledge.Dispute(l.ID, l.Version, "shutdown regression contradicts lesson"); err != nil {
		t.Fatal(err)
	}
	if got := string(d.knowledgeBrief("p1", "session-3", nil)); strings.Contains(got, p.Summary) {
		t.Fatal("disputed lesson still injected")
	}
}
