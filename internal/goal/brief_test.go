package goal

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func proposedBrief(t *testing.T, g *Goal) BriefAcceptance {
	t.Helper()
	source, err := NewBriefSource("Goal — Better invitation flow", g.ParentDescription()+"\nHuman clarification: include keyboard interaction.", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err = g.ObserveBrief(source); err != nil {
		t.Fatal(err)
	}
	c := g.BriefContract()
	c.Outcome += " with keyboard access"
	return BriefAcceptance{ID: g.ID, Revision: g.Revision, SourceID: source.ID, Contract: c, Reason: "Existing UI task already covers keyboard access; retain its contract and all checks"}
}
func TestBriefRevisionPreservesAuthorityAndHistory(t *testing.T) {
	g := executable(t)
	g.State = "paused"
	store, _ := Open(filepath.Join(t.TempDir(), "goals.json"))
	g, err := store.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	a := proposedBrief(t, &g)
	g, err = store.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	a.Revision = g.Revision
	before := copyGoal(g)
	if err = g.AcceptBrief(a, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	g, err = store.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	if g.State != "paused" || g.PendingBrief != "" || !reflect.DeepEqual(g.Execution.Policy, before.Execution.Policy) || !reflect.DeepEqual(g.Tasks, before.Tasks) || g.Execution.Deadline != before.Execution.Deadline {
		t.Fatal("brief changed execution authority or resumed work")
	}
	title, body := g.ParentIssueText()
	if title != before.BriefSources[0].Title || body != before.BriefSources[0].Description {
		t.Fatal("human prose replaced by rendered contract")
	}
	if g.ParentDescription() == body {
		t.Fatal("raw discussion treated as structured contract")
	}
	reopened, err := Open(store.path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := reopened.Get(g.ID)
	if !reflect.DeepEqual(got, g) {
		t.Fatal("brief history lost on restart")
	}
	modified := copyGoal(g)
	modified.BriefSources[0].Description += " changed"
	if _, err = store.Save(modified); err == nil {
		t.Fatal("rewrote source history")
	}
	modified = copyGoal(g)
	modified.Outcome += " silent"
	if _, err = store.Save(modified); err == nil {
		t.Fatal("silent contract change accepted")
	}
}
func TestBriefReconciliationHoldsAndRechecks(t *testing.T) {
	for _, mode := range []string{"stale", "active", "pending", "delivery", "running", "terminal", "reason"} {
		t.Run(mode, func(t *testing.T) {
			g := executable(t)
			g.Revision = 1
			g.State = "paused"
			a := proposedBrief(t, &g)
			switch mode {
			case "stale":
				a.Revision++
			case "active":
				g.Execution.Attempts["a"] = Attempt{State: "running"}
			case "pending":
				g.Execution.PendingIntegration = "pending"
			case "delivery":
				g.Execution.Delivery = &Delivery{}
			case "running":
				g.State = "running"
			case "terminal":
				g.State = "canceled"
			case "reason":
				a.Reason = ""
			}
			before := copyGoal(g)
			if err := g.AcceptBrief(a, time.Now().UTC()); err == nil {
				t.Fatal("unsafe revision accepted")
			}
			if !reflect.DeepEqual(g, before) {
				t.Fatal("rejected acceptance mutated goal")
			}
		})
	}
	g := executable(t)
	g.Revision = 1
	_ = proposedBrief(t, &g)
	if _, err := g.ReserveAttempt(g.Tasks[0].ID, "attempt", "session", time.Now().UTC()); err == nil {
		t.Fatal("proposal did not hold dispatch")
	}
	g.State = "paused"
	if err := g.Control("resume", "", time.Now().UTC()); err == nil {
		t.Fatal("proposal resumed without acceptance")
	}
	s := g.BriefSources[0]
	s.ObservedAt = time.Now().UTC()
	if err := g.ObserveBrief(s); err != nil || len(g.BriefSources) != 1 {
		t.Fatal("repeated observation duplicated proposal", err)
	}
	title, body := g.ParentIssueText()
	baseline, _ := NewBriefSource(title, body, time.Now().UTC())
	if err := g.ObserveBrief(baseline); err != nil || g.PendingBrief != "" || len(g.BriefSources) != 1 {
		t.Fatal("revert erased history", err)
	}
}

func TestBriefRevisionArchivesFinalProofAndConsumesReviewBudget(t *testing.T) {
	g := readyRefreshGoal(t)
	g.State = "paused"
	g.Execution.DeliveryEvidenceURL = "https://example.com/old-evidence"
	store, _ := Open(filepath.Join(t.TempDir(), "goals.json"))
	g, err := store.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	old := *g.Execution.Final
	a := proposedBrief(t, &g)
	g, err = store.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	a.Revision = g.Revision
	if err = g.AcceptBrief(a, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	g, err = store.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	archived := g.BriefRevisions[0]
	if g.Execution.Final != nil || g.Execution.DeliveryEvidenceURL != "" || len(archived.ArchivedFinals) != 1 || !reflect.DeepEqual(archived.ArchivedFinals[0], old) || archived.EvidenceURL == "" {
		t.Fatal("old proof was reused or lost")
	}
	if err = g.Control("resume", "", time.Now().UTC()); err != nil || g.State != "verifying" {
		t.Fatal("revised goal did not require new final review", err)
	}
	if err = g.ReserveFinal(old.ID, time.Now().UTC()); err == nil {
		t.Fatal("reused old evidence identity")
	}
	if err = g.ReserveFinal("new-final", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	g, err = store.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	g.State = "paused"
	if err = g.RetryVerification("third-final", time.Now().UTC()); err == nil {
		t.Fatal("brief revision reset review budget")
	}
	tampered := copyGoal(g)
	tampered.BriefRevisions[0].ArchivedFinals[0].Receipt = "changed"
	if _, err = store.Save(tampered); err == nil {
		t.Fatal("archived evidence overwritten")
	}
}
