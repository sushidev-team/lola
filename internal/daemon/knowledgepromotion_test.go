package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/gitrepo"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalwork"
	"github.com/sushidev-team/lola/internal/knowledge"
	"github.com/sushidev-team/lola/internal/reviewprofile"
	"github.com/sushidev-team/lola/internal/session"
)

func verifiedLessonFixture(t *testing.T) (*Daemon, string, string, reviewprofile.FindingReport) {
	t.Helper()
	d, s := profileFixture(t)
	dir := t.TempDir()
	goalFixtureGit(t, dir, "init", "-b", "main")
	file := filepath.Join(dir, "store.go")
	if err := os.WriteFile(file, []byte("unprotected write\n"), 0600); err != nil {
		t.Fatal(err)
	}
	goalFixtureGit(t, dir, "add", "store.go")
	goalFixtureGit(t, dir, "commit", "-m", "initial")
	initial := goalFixtureGit(t, dir, "rev-parse", "HEAD")
	goalFixtureGit(t, dir, "checkout", "-b", "fix")
	if err := os.WriteFile(file, []byte("serialized write\n"), 0600); err != nil {
		t.Fatal(err)
	}
	goalFixtureGit(t, dir, "commit", "-am", "fix")
	fix := goalFixtureGit(t, dir, "rev-parse", "HEAD")
	goalFixtureGit(t, dir, "checkout", "main")
	p := d.cfg.ProjectByName(s.Project)
	p.Path = dir
	p.DefaultBranch = "main"
	p.Repo = s.Repo
	profile := d.cfg.ReviewProfiles[0]
	in := reviewprofile.Inputs{Owner: &reviewprofile.ReviewOwner{Project: s.Project, Session: s.ID}, Invocation: profileInvocation(profile, d.cfg.ReviewProviders[0], false), Repo: s.Repo, Head: initial, Base: initial, Contract: "No lost writes", Paths: []string{"store.go"}}
	r, err := d.profileReviews.Begin(profile, in, false)
	if err != nil {
		t.Fatal(err)
	}
	r.State, r.Result = "findings", disputeResult()
	if err = d.profileReviews.Finish(r); err != nil {
		t.Fatal(err)
	}
	d.captureProfileFindings(s, r)
	f := d.profileReviews.Findings()[0]
	decide := func(state string) {
		t.Helper()
		a := reviewprofile.FindingDecision{ID: f.ID, Revision: f.Revision, State: state, Evidence: "Independently reproduced and checked the concurrent update regression"}
		if state == "fixed" {
			a.FixHead = fix
		}
		f, err = d.profileReviews.Decide(a)
		if err != nil {
			t.Fatal(err)
		}
	}
	decide("accepted")
	decide("fixed")
	in.Head = fix
	verified, err := d.profileReviews.Begin(profile, in, false)
	if err != nil {
		t.Fatal(err)
	}
	verified.State = "pass"
	verified.Result = reviewprofile.Result{Complete: true, Evidence: []reviewprofile.Evidence{{Criterion: "Atomicity", Observation: "The public transaction path serializes every write"}}}
	if err = d.profileReviews.Finish(verified); err != nil {
		t.Fatal(err)
	}
	f, err = d.profileReviews.Decide(reviewprofile.FindingDecision{ID: f.ID, Revision: f.Revision, State: "verified", FixHead: fix, VerificationKey: verified.Key, Evidence: "Independent clean review of the exact fix commit confirms the requirement"})
	if err != nil {
		t.Fatal(err)
	}
	return d, dir, fix, f
}

func TestReviewKnowledgePromotionWaitsForAcceptedCodeAndRetractsOnDispute(t *testing.T) {
	d, dir, fix, f := verifiedLessonFixture(t)
	if reports := d.promoteReviewKnowledge(context.Background(), "p1"); len(reports) != 1 || reports[0].State != "waiting" {
		t.Fatalf("unmerged code promoted: %+v", reports)
	}
	goalFixtureGit(t, dir, "merge", "--ff-only", fix)
	reports := d.promoteReviewKnowledge(context.Background(), "p1")
	if len(reports) != 1 || reports[0].State != "promoted" || reports[0].LessonID == "" {
		t.Fatalf("promotion failed: %+v", reports)
	}
	q := knowledge.Query{Project: "p1", Paths: []string{"store.go"}}
	bundle := d.selectKnowledge(context.Background(), q)
	if len(bundle.Lessons) != 1 || bundle.Lessons[0].ReviewProof == nil || len(bundle.Lessons[0].Code) != 1 {
		t.Fatalf("verified guidance not delivered: %+v", bundle)
	}
	if got := d.selectKnowledge(context.Background(), knowledge.Query{Project: "p1", Paths: []string{"other.go"}}); len(got.Lessons) != 0 {
		t.Fatal("promotion escaped its inspected paths")
	}
	if _, err := d.profileReviews.Decide(reviewprofile.FindingDecision{ID: f.ID, Revision: f.Revision, State: "disputed", Evidence: "New independent evidence contradicts the verified fix"}); err != nil {
		t.Fatal(err)
	}
	if got := d.selectKnowledge(context.Background(), q); len(got.Lessons) != 0 {
		t.Fatal("withdrawn proof remained in guidance")
	}
	for _, l := range d.knowledge.List() {
		if l.ID == reports[0].LessonID && (l.Status != knowledge.Candidate || l.ReviewReason != "review verification changed") {
			t.Fatal("proof invalidation was not durable")
		}
	}
	if got := d.promoteReviewKnowledge(context.Background(), "p1"); len(got) != 0 {
		t.Fatal("maintenance overrode retraction")
	}
}

func TestReviewKnowledgePromotionRechecksCodeBeforeEveryUse(t *testing.T) {
	d, dir, fix, _ := verifiedLessonFixture(t)
	goalFixtureGit(t, dir, "merge", "--ff-only", fix)
	if reports := d.promoteReviewKnowledge(context.Background(), "p1"); len(reports) != 1 || reports[0].State != "promoted" {
		t.Fatal(reports)
	}
	if err := os.WriteFile(filepath.Join(dir, "store.go"), []byte("changed again\n"), 0600); err != nil {
		t.Fatal(err)
	}
	goalFixtureGit(t, dir, "commit", "-am", "changed implementation")
	if got := d.selectKnowledge(context.Background(), knowledge.Query{Project: "p1", Paths: []string{"store.go"}}); len(got.Lessons) != 0 {
		t.Fatal("changed code reused old verification")
	}
	if got := d.promoteReviewKnowledge(context.Background(), "p1"); len(got) != 0 {
		t.Fatal("maintenance resurrected stale guidance")
	}
}

func TestReviewKnowledgePromotionCurationWinsDuringCodeRead(t *testing.T) {
	d, dir, fix, f := verifiedLessonFixture(t)
	goalFixtureGit(t, dir, "merge", "--ff-only", fix)
	reports := d.promoteReviewKnowledge(context.Background(), "p1")
	if len(reports) != 1 || reports[0].State != "promoted" {
		t.Fatal(reports)
	}
	changed := false
	d.knowledgeCodeRead = func(ctx context.Context, source, branch, path string) (gitrepo.CodeIdentity, error) {
		if !changed {
			changed = true
			if _, err := d.profileReviews.Decide(reviewprofile.FindingDecision{ID: f.ID, Revision: f.Revision, State: "disputed", Evidence: "Concurrent human review withdraws this verification"}); err != nil {
				t.Fatal(err)
			}
		}
		return (gitrepo.CodeReader{}).Read(ctx, source, branch, path)
	}
	q := knowledge.Query{Project: "p1", Paths: []string{"store.go"}}
	if got := d.selectKnowledge(context.Background(), q); len(got.Lessons) != 0 {
		t.Fatal("concurrent dispute was ignored after git read")
	}
	// A subsequent explicit acceptance can replace the automatic proof, with
	// that provenance retained as history and fresh code references still required.
	var derived knowledge.Lesson
	for _, l := range d.knowledge.List() {
		if l.ID == reports[0].LessonID {
			derived = l
		}
	}
	accepted, err := d.acceptKnowledge(context.Background(), knowledgeArgs{ID: derived.ID, Version: derived.Version, Evidence: "Human independently validated applicability after the new counterexample"})
	if err != nil || accepted.ReviewProof == nil || !accepted.ReviewProof.ReplacedByHuman {
		t.Fatal("explicit acceptance lost or misrepresented verification history", err)
	}
	if got := d.selectKnowledge(context.Background(), q); len(got.Lessons) != 1 {
		t.Fatal("explicit human acceptance remained tied to withdrawn automatic proof")
	}
}

func TestReviewKnowledgePromotionRejectsInvalidatedVerification(t *testing.T) {
	d, dir, fix, f := verifiedLessonFixture(t)
	goalFixtureGit(t, dir, "merge", "--ff-only", fix)
	if reports := d.promoteReviewKnowledge(context.Background(), "p1"); len(reports) != 1 || reports[0].State != "promoted" {
		t.Fatal(reports)
	}
	key := f.Events[len(f.Events)-1].Verification.Key
	if err := d.profileReviews.Invalidate(key, "acceptance contract changed"); err != nil {
		t.Fatal(err)
	}
	if got := d.selectKnowledge(context.Background(), knowledge.Query{Project: "p1", Paths: []string{"store.go"}}); len(got.Lessons) != 0 {
		t.Fatal("invalidated verification remained active guidance")
	}
}

func TestGoalReviewKnowledgePromotesOnlyAfterObservedDelivery(t *testing.T) {
	d, g := readyPublicationFixture(t)
	ctx := context.Background()
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	repo, err := m.Repository(ctx, g.ID, "p1")
	if err != nil {
		t.Fatal(err)
	}
	fix := g.Execution.Manifest["p1"]
	p := d.cfg.ReviewProfiles[0]
	in := reviewprofile.Inputs{Owner: &reviewprofile.ReviewOwner{Project: g.Project, Goal: g.ID, Session: "proof-worker", Task: g.Tasks[0].ID}, Invocation: profileInvocation(p, d.cfg.ReviewProviders[0], true), Repo: g.Execution.Policy.Members[0].Repo, Head: repo.Start, Base: repo.Start, Contract: "Preserve writes", ContextID: "lesson-proof"}
	combination, _ := json.Marshal(map[string]string{"p1": repo.Start})
	in.Contract += "\nVerified repository combination: " + string(combination)
	r, err := d.profileReviews.Begin(p, in, false)
	if err != nil {
		t.Fatal(err)
	}
	r.State, r.Result = "findings", disputeResult()
	if err = d.profileReviews.Finish(r); err != nil {
		t.Fatal(err)
	}
	d.captureProfileFindings(session.Session{ID: "proof-worker", Project: g.Project, GoalID: g.ID}, r)
	var f reviewprofile.FindingReport
	for _, item := range d.profileReviews.Findings() {
		if item.ReviewKey == r.Key {
			f = item
		}
	}
	in.Head = fix
	combination, _ = json.Marshal(g.Execution.Manifest)
	in.Contract = "Preserve writes\nVerified repository combination: " + string(combination)
	check, err := d.profileReviews.Begin(p, in, false)
	if err != nil {
		t.Fatal(err)
	}
	check.State = "pass"
	check.Result = reviewprofile.Result{Complete: true, Evidence: []reviewprofile.Evidence{{Criterion: "Preserve writes", Observation: "Independent transaction regression passes"}}}
	if err = d.profileReviews.Finish(check); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"accepted", "fixed", "verified"} {
		a := reviewprofile.FindingDecision{ID: f.ID, Revision: f.Revision, State: state, Evidence: "Independent reproduction and regression proof inspected"}
		if state == "fixed" || state == "verified" {
			a.FixHead = fix
		}
		if state == "verified" {
			a.VerificationKey = check.Key
		}
		f, err = d.profileReviews.Decide(a)
		if err != nil {
			t.Fatal(err)
		}
	}
	if reports := d.promoteReviewKnowledge(ctx, g.Project); len(reports) != 1 || reports[0].State != "waiting" {
		t.Fatalf("ready goal escaped scope: %+v", reports)
	}
	delivery := goal.Delivery{Operation: g.Execution.Final.ID, State: "merged", CheckedAt: time.Now(), Members: map[string]goal.DeliveryMember{"p1": {Repo: in.Repo, Target: repo.Target, Head: fix, State: "merged", Number: 1, URL: "https://github.com/acme/widgets/pull/1"}}}
	if err = g.ObserveDelivery(delivery); err != nil {
		t.Fatal(err)
	}
	if _, err = d.goals.Save(g); err != nil {
		t.Fatal(err)
	}
	if reports := d.promoteReviewKnowledge(ctx, g.Project); len(reports) != 1 || reports[0].State != "waiting" {
		t.Fatalf("missing local target evidence accepted: %+v", reports)
	}
	goalFixtureGit(t, repo.Source, "fetch", repo.GitDir, fix)
	goalFixtureGit(t, repo.Source, "merge", "--ff-only", fix)
	if reports := d.promoteReviewKnowledge(ctx, g.Project); len(reports) != 1 || reports[0].State != "promoted" {
		t.Fatalf("delivered goal did not promote: %+v", reports)
	}
	if got := d.selectKnowledge(ctx, knowledge.Query{Project: g.Project}); len(got.Lessons) != 1 || got.Lessons[0].Scope.GoalID != "" {
		t.Fatal("verified project guidance unavailable")
	}
}
