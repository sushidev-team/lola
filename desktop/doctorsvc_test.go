package main

import (
	"os"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/doctor"
)

// A repeated check name must not give two rows the same key: the frontend
// keys its list by ID, and a duplicate key breaks the whole list.
func TestDoctorDTOGivesEveryRowAUniqueID(t *testing.T) {
	rep := doctor.Report{Results: []doctor.Result{
		{Name: "tmux", OK: true},
		{Name: "repo", Detail: "a"},
		{Name: "repo", Detail: "b"},
		{Name: "gh auth", Detail: "not logged in", Fix: doctor.FixGhLogin},
	}}
	got := doctorDTO(rep)
	ids := []string{}
	for _, r := range got.Results {
		ids = append(ids, r.ID)
	}
	if want := "tmux,repo,repo#2,gh auth"; strings.Join(ids, ",") != want {
		t.Fatalf("ids = %v, want %s", ids, want)
	}
	if got.Results[3].Fix != string(doctor.FixGhLogin) {
		t.Fatalf("fix = %q, want %q", got.Results[3].Fix, doctor.FixGhLogin)
	}
	if got.Results[0].Fix != "" {
		t.Fatalf("a passing row carries no fix, got %q", got.Results[0].Fix)
	}
	if got.Summary != rep.Summary() || got.OK != rep.OK() {
		t.Fatalf("summary/ok not carried: %+v", got)
	}
}

// Every fix id the doctor can hand out has an entry in the app's fix table, so
// a new FixID cannot reach the UI as a row with no way out.
func TestEveryDoctorFixHasAnAppAction(t *testing.T) {
	src, err := os.ReadFile("frontend/src/lib/components/SetupChecklist.svelte")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range doctor.AllFixIDs() {
		if !strings.Contains(string(src), `"`+string(id)+`":`) && !strings.Contains(string(src), string(id)+":") {
			t.Errorf("SetupChecklist.svelte FIXES has no entry for %q", id)
		}
	}
}
