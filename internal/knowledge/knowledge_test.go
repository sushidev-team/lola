package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Store, Proposal) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "knowledge.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s, Proposal{Topic: "Atomic state writes", Summary: "Persist the snapshot before exposing updated state to callers.", Rationale: "A failed write must not release work", Evidence: []string{"disk-full regression test"}}
}

func TestCandidatesNeedIndependentAcceptance(t *testing.T) {
	s, p := fixture(t)
	scope := Scope{Project: "nori", GoalID: "goal-1"}
	source := Source{Kind: "worker", ID: "session-1", Session: "session-1"}
	l, err := s.Capture(scope, p, source, false)
	if err != nil {
		t.Fatal(err)
	}
	if l.Status != Candidate {
		t.Fatal("worker promoted its own claim")
	}
	if _, err := s.Capture(scope, p, source, true); err == nil {
		t.Fatal("worker impersonated a correction")
	}
	if b := Select(s.List(), Query{Project: "nori", GoalID: "goal-1"}, time.Now()); len(b.Lessons) != 0 {
		t.Fatal("unverified lesson leaked into briefing")
	}
	for range 3 {
		if _, err := s.Capture(scope, p, source, false); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.List()) != 1 {
		t.Fatal("repeated stop hooks duplicated a candidate")
	}
	l, err = s.Accept(l.ID, l.Version, "Human reproduced disk-full behavior in test abc123", nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if l.Status != Active || l.VerifiedAt.IsZero() {
		t.Fatal("accepted evidence missing")
	}
	for _, q := range []Query{{Project: "other", GoalID: "goal-1"}, {Project: "nori"}, {Project: "nori", GoalID: "goal-2"}} {
		if len(Select(s.List(), q, time.Now()).Lessons) != 0 {
			t.Fatalf("lesson escaped its scope: %+v", q)
		}
	}
	if len(Select(s.List(), Query{Project: "nori", GoalID: "goal-1"}, time.Now()).Lessons) != 1 {
		t.Fatal("accepted lesson unavailable in its own goal")
	}
}

func TestCorrectionConflictAndVersionHistory(t *testing.T) {
	s, p := fixture(t)
	scope := Scope{Project: "nori"}
	first, err := s.Capture(scope, p, Source{Kind: "correction", ID: "human-1"}, true)
	if err != nil || first.Status != Active {
		t.Fatalf("explicit correction: %+v %v", first, err)
	}
	refs := []Reference{{ID: first.ID, Version: first.Version}}
	if err := s.RecordDelivery("session-1", refs); err != nil {
		t.Fatal(err)
	}
	p.Summary = "Persist a write-ahead journal before publishing updated state."
	second, err := s.Capture(scope, p, Source{Kind: "worker", ID: "session-2"}, false)
	if err != nil || second.Status != Disputed || len(second.Conflicts) != 1 {
		t.Fatalf("conflict not surfaced: %+v %v", second, err)
	}
	if _, err := s.Accept(second.ID, second.Version, "verified with recovery test", nil, time.Time{}); err == nil {
		t.Fatal("silently replaced existing correction")
	}
	if _, err := s.Accept(second.ID, second.Version, "verified with recovery test", []string{first.ID}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordDelivery("session-1", []Reference{{ID: second.ID, Version: 2}}); err == nil {
		t.Fatal("silently changed a session's supplied versions")
	}
	old, ok := s.Version(refs[0])
	if !ok || old.Summary != first.Summary || old.Status != Active {
		t.Fatal("lost historical lesson content")
	}
	s, err = Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	old, ok = s.Version(s.Delivery("session-1")[0])
	if !ok || old.Summary != first.Summary {
		t.Fatal("restart lost delivery provenance")
	}
}

func TestAcceptanceRechecksNewConflicts(t *testing.T) {
	s, p := fixture(t)
	a, _ := s.Capture(Scope{Project: "p"}, p, Source{Kind: "worker", ID: "a"}, false)
	p.Summary = "Use a transaction to persist state before exposing it to callers."
	b, _ := s.Capture(Scope{Project: "p"}, p, Source{Kind: "worker", ID: "b"}, false)
	if _, err := s.Accept(a.ID, a.Version, "operator verified regression test", nil, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept(b.ID, b.Version, "operator verified regression test", nil, time.Time{}); err == nil {
		t.Fatal("stale conflict list bypassed reconciliation")
	}
}

func TestKnowledgeScopeExpiryBudgetAndPolicy(t *testing.T) {
	s, p := fixture(t)
	p.Paths = []string{"internal/daemon"}
	l, err := s.Capture(Scope{Project: "p"}, p, Source{Kind: "correction", ID: "h"}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, paths := range [][]string{nil, {"internal/runtime/x.go"}, {"internal/daemon-other/a.go"}} {
		if len(Select(s.List(), Query{Project: "p", Paths: paths}, time.Now()).Lessons) != 0 {
			t.Fatalf("path scope escaped: %v", paths)
		}
	}
	q := Query{Project: "p", Paths: []string{"internal/daemon/goals.go"}}
	if len(Select(s.List(), q, time.Now()).Lessons) != 1 {
		t.Fatal("matching path missing")
	}
	q.MaxBytes = 10
	if len(Select(s.List(), q, time.Now()).Lessons) != 0 {
		t.Fatal("bundle exceeded actual rendered byte cap")
	}
	if _, err := s.Accept(l.ID, l.Version, "environment-specific check passed", nil, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	q.MaxBytes = 8000
	if len(Select(s.List(), q, time.Now()).Lessons) != 0 {
		t.Fatal("expired lesson injected")
	}
	p.Policy = true
	l, err = s.Capture(Scope{Project: "p"}, p, Source{Kind: "correction", ID: "policy-change"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if l.Status == Active {
		t.Fatal("policy correction changed mandatory rules automatically")
	}
	if _, err := s.Accept(l.ID, l.Version, "please change mandatory policy", nil, time.Time{}); err == nil {
		t.Fatal("policy proposal activated as a lesson")
	}
}

func TestKnowledgeRejectsSecretsAndEscapingPaths(t *testing.T) {
	s, p := fixture(t)
	for _, bad := range []string{"sk-proj-abcdefghijklmnopqrstuvwxyz1234", "password=verySecret1234", "-----BEGIN RSA PRIVATE KEY-----"} {
		p.Summary = "Found a token: " + bad
		if _, err := s.Capture(Scope{Project: "p"}, p, Source{Kind: "worker", ID: "s"}, false); err == nil {
			t.Fatal("credential persisted")
		}
	}
	p.Summary = "Use repository-relative paths for lessons."
	for _, bad := range []string{"../other", "/etc", "a/../b", `C:\private`} {
		p.Paths = []string{bad}
		if _, err := s.Capture(Scope{Project: "p"}, p, Source{Kind: "worker", ID: "s"}, false); err == nil {
			t.Fatalf("invalid path accepted: %q", bad)
		}
	}
	if len(s.List()) != 0 {
		t.Fatal("invalid submissions changed state")
	}
}

func TestExportOnlyVerifiedProjectKnowledge(t *testing.T) {
	s, p := fixture(t)
	_, _ = s.Capture(Scope{Project: "p"}, p, Source{Kind: "correction", ID: "human"}, true)
	_, _ = s.Capture(Scope{Project: "p", GoalID: "experiment"}, p, Source{Kind: "correction", ID: "goal-human"}, true)
	_, _ = s.Capture(Scope{Project: "p"}, p, Source{Kind: "worker", ID: "worker"}, false)
	files := Export(s.List(), "p", time.Now())
	if len(files) != 2 || !strings.Contains(files["LEARNINGS.md"], "docs/learnings/") {
		t.Fatalf("export: %v", files)
	}
	for _, contents := range files {
		if strings.Contains(contents, "goal-human") {
			t.Fatal("experimental goal lesson escaped into portable docs")
		}
	}
	p.Evidence[0] = "mutated by caller"
	for _, l := range s.List() {
		if l.Evidence[0] == "mutated by caller" {
			t.Fatal("input slice mutated canonical store")
		}
	}
}

func TestCorruptKnowledgeFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "knowledge.json")
	for _, body := range []string{"null", "{", `{"version":1,"lessons":null}`} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(path); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}
