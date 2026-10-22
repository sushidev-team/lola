package goal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func testDraft() Draft {
	return Draft{Project: "nori", Title: "Invitations", Outcome: "Invite a colleague", Acceptance: []string{"Recipient can accept an invitation"},
		Tasks: []TaskDraft{{Title: "API", Description: "Add invitation contract", Acceptance: []string{"Expired links are refused"}},
			{Title: "UI", Acceptance: []string{"Invitation form is keyboard accessible"}, BlockedBy: []int{1}}}}
}

func TestStoreReservationRestartAndCAS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "goals.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	g, err := New(testDraft(), "team", "linear-project")
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(g); err == nil {
		t.Fatal("stale revision accepted")
	}
	first.Tasks[0].Title = "changed outside store"
	got, _ := s.Get(g.ID)
	if got.Tasks[0].Title != "API" {
		t.Fatal("caller mutated canonical record")
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{g.Parent.ID, g.Tasks[0].Issue.ID, g.Tasks[1].Issue.ID} {
		if !s.OwnsIssue(id) {
			t.Fatalf("lost reservation %s", id)
		}
	}
	got, _ = s.Get(g.ID)
	if got.Tasks[1].Relations[0].ID != g.Tasks[1].Relations[0].ID {
		t.Fatal("lost dependency operation identity")
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("permissions: %v", fi.Mode())
	}
}

func TestStoreFailedWriteDoesNotPublishMemory(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "goals.json"))
	g, _ := New(testDraft(), "team", "project")
	s.write = func(string, []byte) error { return errors.New("disk full") }
	if _, err := s.Save(g); err == nil {
		t.Fatal("write failure swallowed")
	}
	if s.OwnsIssue(g.Parent.ID) || len(s.List()) != 0 {
		t.Fatal("uncommitted record visible")
	}
}

func TestStoreFailsClosed(t *testing.T) {
	for _, body := range []string{`{`, `null`, `{"version":4,"goals":{}}`, `{"version":1,"goals":{"g":{"id":"g"}}}`} {
		path := filepath.Join(t.TempDir(), "goals.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(path); err == nil {
			t.Errorf("accepted corrupt snapshot %s", body)
		}
	}
}

func TestDraftDependencyValidation(t *testing.T) {
	for _, deps := range [][]int{{0}, {2}, {-1}, {1, 1}, {999}} {
		d := testDraft()
		d.Tasks[1].BlockedBy = deps
		if err := ValidateDraft(d); err == nil {
			t.Errorf("accepted %v", deps)
		}
	}
	d := testDraft()
	d.Tasks[0].Acceptance = nil
	if err := ValidateDraft(d); err == nil {
		t.Fatal("task without acceptance accepted")
	}
	if !Marked("copied partial " + MarkerPrefix) {
		t.Fatal("malformed marker must still hold dispatch")
	}
}
