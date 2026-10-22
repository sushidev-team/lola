package goalplan

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/usage"
)

func editFixture(t *testing.T) (*Store, Record, EditRequest) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "plans.json"))
	if err != nil {
		t.Fatal(err)
	}
	in := inputFixture(t)
	r, _, err := s.Reserve(in, "codex", "image")
	if err != nil {
		t.Fatal(err)
	}
	out, err := Parse([]byte(goodOutput), in)
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.Finish(r.ID, &out, nil)
	if err != nil {
		t.Fatal(err)
	}
	changed := clone(r)
	changed.Output.Tasks[0].Title = "Invitation API and regression tests"
	return s, r, EditRequest{ID: r.ID, Project: in.Request.Project, Revision: r.Revision, EditID: goal.NewID(), Reason: "Clarify the deliverable before accepting the plan", Output: *changed.Output}
}
func TestProposalEditPreservesOriginalAndSurvivesRestart(t *testing.T) {
	s, original, a := editFixture(t)
	before, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	var old disk
	_ = json.Unmarshal(before, &old)
	if old.Version != 1 {
		t.Fatal("unedited proposals migrated unnecessarily")
	}
	reopened, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, _ := os.ReadFile(s.path)
	if string(before) != string(unchanged) {
		t.Fatal("opening legacy store rewrote it")
	}
	edited, err := reopened.Edit(a)
	if err != nil {
		t.Fatal(err)
	}
	if edited.Revision != original.Revision+1 || edited.InputSHA != original.InputSHA || edited.GoalID != original.GoalID || !reflect.DeepEqual(edited.Input, original.Input) || !reflect.DeepEqual(edited.ModelOutput, original.Output) || len(edited.Edits) != 1 || edited.Output.Tasks[0].Title != a.Output.Tasks[0].Title {
		t.Fatal("edit lost provenance", edited)
	}
	b, _ := os.ReadFile(s.path)
	var current disk
	_ = json.Unmarshal(b, &current)
	if current.Version != 2 {
		t.Fatal("old binary would ignore history")
	}
	reopened, err = Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := reopened.Edit(a)
	if err != nil || replay.Revision != edited.Revision {
		t.Fatal("replay rewrote edit", err)
	}
	next := a
	next.EditID = goal.NewID()
	next.Revision = edited.Revision
	next.Reason = "Resolve the mail transport question using the repository configuration"
	next.Output.Unknowns = []string{}
	second, err := reopened.Edit(next)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err = Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err = reopened.Edit(next); err != nil || replay.Revision != second.Revision {
		t.Fatal("empty optional fields broke retry", err)
	}
	if replay, err = reopened.Edit(a); err != nil || replay.Revision != second.Revision {
		t.Fatal("old replay rewound history", err)
	}
	accepted, err := reopened.Accept(second.ID, second.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Open(s.path); err != nil {
		t.Fatal(err)
	}
	if replay, err = reopened.Edit(a); err != nil || replay.State != "accepted" || replay.Revision != accepted.Revision {
		t.Fatal("accepted replay changed record", err)
	}
	next.EditID = goal.NewID()
	next.Revision = accepted.Revision
	if _, err = reopened.Edit(next); err == nil {
		t.Fatal("accepted plan edited")
	}
}
func TestProposalEditRejectsChangedIdentityStaleScopeAndInvalidGraphs(t *testing.T) {
	s, original, a := editFixture(t)
	edited, err := s.Edit(a)
	if err != nil {
		t.Fatal(err)
	}
	changed := a
	changed.Reason = "Different reason with retained edit identity"
	if _, err = s.Edit(changed); err == nil {
		t.Fatal("identity rewritten")
	}
	for _, mutate := range []func(*EditRequest){func(a *EditRequest) { a.Project = "other" }, func(a *EditRequest) { a.EditID = goal.NewID() }, func(a *EditRequest) { a.EditID = "invalid" }, func(a *EditRequest) { a.Reason = "" }} {
		bad := a
		mutate(&bad)
		if _, err = s.Edit(bad); err == nil {
			t.Fatal("invalid edit accepted", bad)
		}
	}
	if _, err = s.Accept(a.ID, original.Revision); err == nil {
		t.Fatal("stale review accepted changed graph")
	}
	for _, body := range []string{strings.Replace(goodOutput, `"covers":[1,2]`, `"covers":[1]`, 1), strings.Replace(goodOutput, `"covers":[1,2]`, `"covers":[1,2],"blocked_by":[1]`, 1), strings.Replace(goodOutput, `"paths":{"api":["src/invitations","tests/invitations"]}`, `"paths":{"api":[]}`, 1), strings.Replace(goodOutput, `"label-api"`, `"unknown-label"`, 1)} {
		bad := a
		bad.EditID = goal.NewID()
		bad.Revision = edited.Revision
		_ = json.Unmarshal([]byte(body), &bad.Output)
		if _, err = s.Edit(bad); err == nil {
			t.Fatal("invalid graph accepted", body)
		}
	}
	after, _ := s.Get(a.ID)
	if !reflect.DeepEqual(after, edited) {
		t.Fatal("rejected operation mutated record")
	}
}
func TestProposalEditAtomicFailureAndConcurrentDecisions(t *testing.T) {
	s, r, a := editFixture(t)
	s.write = func(string, []byte) error { return errors.New("disk full") }
	if _, err := s.Edit(a); err == nil {
		t.Fatal("failed persistence accepted")
	}
	saved, _ := s.Get(r.ID)
	if !reflect.DeepEqual(saved, r) {
		t.Fatal("failed write changed in-memory record")
	}
	s.write = atomicWrite
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := a
			copy.EditID = goal.NewID()
			if _, err := s.Edit(copy); err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if success != 1 {
		t.Fatal("concurrent stale edit accepted", success)
	}
	if _, err := Open(s.path); err != nil {
		t.Fatal(err)
	}
}
func TestProposalEditStoreRejectsLostOrTamperedHistory(t *testing.T) {
	s, _, a := editFixture(t)
	r, err := s.Edit(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*disk){func(d *disk) { d.Version = 1 }, func(d *disk) {
		v := d.Records[r.ID]
		v.Output.Tasks[0].Title = "Unattributed change"
		d.Records[r.ID] = v
	}, func(d *disk) { v := d.Records[r.ID]; v.Edits = nil; d.Records[r.ID] = v }, func(d *disk) { v := d.Records[r.ID]; v.Edits[0].Request.Project = "other"; d.Records[r.ID] = v }, func(d *disk) { v := d.Records[r.ID]; v.Edits[0].Request.Revision--; d.Records[r.ID] = v }} {
		b, _ := json.Marshal(s.data)
		var d disk
		_ = json.Unmarshal(b, &d)
		change(&d)
		b, _ = json.Marshal(d)
		file := filepath.Join(t.TempDir(), "bad.json")
		_ = os.WriteFile(file, b, 0600)
		if _, err := Open(file); err == nil {
			t.Fatal("accepted damaged history")
		}
		after, _ := os.ReadFile(file)
		if string(after) != string(b) {
			t.Fatal("failed open rewrote evidence")
		}
	}
}
func TestDecodeProposalEditIsStrictAndBounded(t *testing.T) {
	_, _, a := editFixture(t)
	b, _ := json.Marshal(a)
	for _, body := range [][]byte{append(b, []byte(`{}`)...), []byte(strings.Replace(string(b), `"reason":`, `"authorize":true,"reason":`, 1)), []byte(strings.Repeat("x", 96<<10+1))} {
		if _, err := DecodeEdit(body); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
	if _, err := DecodeEdit(b); err != nil {
		t.Fatal(err)
	}
}

func TestProposalEditPreservesPaidUsageAcrossLaterWrites(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "plans.json"))
	if err != nil {
		t.Fatal(err)
	}
	in := inputFixture(t)
	r, _, err := s.Reserve(in, "codex", "image")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	observation := usage.Observation{Agent: "codex", Status: "partial", CostStatus: "estimate", At: now, Days: map[string]usage.Totals{now.Local().Format(usage.DayFormat): {Input: 100, CostUSD: 0.25}}}
	if err = s.ObserveUsage(r.ID, observation); err != nil {
		t.Fatal(err)
	}
	out, _ := Parse([]byte(goodOutput), in)
	r, err = s.Finish(r.ID, &out, nil)
	if err != nil {
		t.Fatal(err)
	}
	out.Tasks[0].Title = "Clarified invitation work"
	a := EditRequest{ID: r.ID, Project: in.Request.Project, Revision: r.Revision, EditID: goal.NewID(), Reason: "Clarify the graph while retaining the model call cost", Output: out}
	edited, err := s.Edit(a)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(edited.Usage, r.Usage) {
		t.Fatal("edit changed observed usage")
	}
	in.Request.ID = goal.NewID()
	if _, _, err = s.Reserve(in, "codex", "image"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := reopened.Get(r.ID)
	if saved.Usage == nil || usage.Sum(saved.Usage.Days).CostUSD != 0.25 || saved.Revision != edited.Revision || len(saved.Edits) != 1 || reopened.data.Version != 2 {
		t.Fatal("later writes or recovery lost history/usage")
	}
}
