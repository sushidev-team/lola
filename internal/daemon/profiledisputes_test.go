package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/reviewprofile"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

func disputeResult() reviewprofile.Result {
	return reviewprofile.Result{Complete: true, Evidence: []reviewprofile.Evidence{{Criterion: "Atomic saves", Observation: "A concurrent update is lost"}}, Findings: []reviewprofile.Finding{{Requirement: "Preserve every committed update", Location: "store.go:10", Reproduction: "Run concurrent saves", Evidence: "Lost update in concurrent reproduction", Severity: "major", Confidence: "verified", Fix: "Keep the transaction lock across the read and write"}}}
}

func disputeFixture(t *testing.T) (*Daemon, session.Session, reviewprofile.Record, reviewprofile.FindingReport) {
	t.Helper()
	d, s := profileFixture(t)
	s.Worktree = t.TempDir()
	s.TmuxName = s.ID
	s.AtPrompt = true
	s.AtPromptVerified = true
	d.sessions.Upsert(s)
	d.cfg.ReviewProviders[0].SendToAgent = true
	d.profileRun = func(context.Context, string, reviewprofile.Profile, reviewprofile.Inputs) (reviewprofile.Result, error) {
		return disputeResult(), nil
	}
	r, err := d.runProfile(context.Background(), profileArgs{Session: s.ID, Profile: "backend", Contract: "No lost writes"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range d.profileReviews.Findings() {
		if f.ReviewKey == r.Key {
			return d, s, r, f
		}
	}
	t.Fatal("finding missing")
	return nil, session.Session{}, reviewprofile.Record{}, reviewprofile.FindingReport{}
}

func writeDispute(t *testing.T, s session.Session, f reviewprofile.FindingReport) {
	t.Helper()
	name := filepath.Join(s.Worktree, "review-disputes.json")
	if !s.GoalManaged() {
		if err := os.MkdirAll(filepath.Join(s.Worktree, ".lola"), 0700); err != nil {
			t.Fatal(err)
		}
		name = filepath.Join(s.Worktree, ".lola", "review-disputes.json")
	}
	body, _ := json.Marshal([]reviewprofile.WorkerDispute{{ID: f.ID, Revision: f.Revision, Evidence: "The reproduction bypasses the public transaction wrapper; see concurrent API regression test."}})
	if err := os.WriteFile(name, body, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSpecialistHandoffDefersAndDeliversCurrentFindingRevisionOnce(t *testing.T) {
	d, s, r, f := disputeFixture(t)
	seams := &fakeReactSeams{}
	seams.install(d)
	key := specialistHandoffKey(r)
	saved, _ := d.sessions.Get(s.ID)
	if !strings.Contains(saved.PendingHandoffs[key], f.ID) {
		t.Fatal("finding id not queued")
	}
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.AtPrompt = false
		cur.SetAgentState(state.AgentWorking, "", time.Now())
		return true
	})
	d.flushReviewHandoffs(context.Background(), s.ID)
	if len(seams.sendCalls()) != 0 {
		t.Fatal("sent specialist findings mid-turn")
	}
	accepted, err := d.profileReviews.Decide(reviewprofile.FindingDecision{ID: f.ID, Revision: f.Revision, State: "accepted", Evidence: "Operator reproduced the concurrency failure"})
	if err != nil {
		t.Fatal(err)
	}
	idleSession(t, d, s.ID)
	d.flushReviewHandoffs(context.Background(), s.ID)
	calls := seams.sendCalls()
	if len(calls) != 1 || !strings.Contains(calls[0].text, f.ID) || !strings.Contains(calls[0].text, `"revision":2`) || !strings.Contains(calls[0].text, ".lola/review-disputes.json") {
		t.Fatalf("missing current dispute contract: %+v", calls)
	}
	writeDispute(t, s, accepted)
	if err = d.captureProfileDisputes(s.ID); err != nil {
		t.Fatal(err)
	}
	if !d.sessionDisputeHold(s) || d.profileGatesOpen(s) {
		t.Fatal("worker dispute opened delivery gate")
	}
	d.queueSpecialistHandoff(s, r)
	idleSession(t, d, s.ID)
	d.flushReviewHandoffs(context.Background(), s.ID)
	if len(seams.sendCalls()) != 1 {
		t.Fatal("same review started another worker turn")
	}
}

func TestSpecialistHandoffDropsStaleHeadsAndRespectsDisabledSink(t *testing.T) {
	d, s, r, _ := disputeFixture(t)
	seams := &fakeReactSeams{}
	seams.install(d)
	d.cfg.ReviewProviders[0].SendToAgent = false
	d.flushReviewHandoffs(context.Background(), s.ID)
	if len(seams.sendCalls()) != 0 {
		t.Fatal("disabled sink sent findings")
	}
	d.cfg.ReviewProviders[0].SendToAgent = true
	d.sessions.Update(s.ID, func(cur *session.Session) bool { cur.PR.HeadSHA = headC; return true })
	d.flushReviewHandoffs(context.Background(), s.ID)
	current, _ := d.sessions.Get(s.ID)
	if len(seams.sendCalls()) != 0 || current.PendingHandoffs[specialistHandoffKey(r)] != "" {
		t.Fatal("stale review delivered or retained")
	}
}

func TestSpecialistHandoffBoundsFeedbackWithoutLosingFindingReferences(t *testing.T) {
	d, s := profileFixture(t)
	d.profileRun = func(context.Context, string, reviewprofile.Profile, reviewprofile.Inputs) (reviewprofile.Result, error) {
		result := disputeResult()
		f := result.Findings[0]
		f.Requirement, f.Location, f.Reproduction, f.Evidence, f.Fix = strings.Repeat("e", 200), strings.Repeat("l", 200), strings.Repeat("r", 200), strings.Repeat("o", 200), strings.Repeat("f", 200)
		result.Findings = nil
		for range 20 {
			result.Findings = append(result.Findings, f)
		}
		return result, nil
	}
	r, err := d.runProfile(context.Background(), profileArgs{Session: s.ID, Profile: "backend", Contract: "No lost writes"})
	if err != nil {
		t.Fatal(err)
	}
	feedback := d.specialistFeedback(r, true)
	if len(feedback) > 16000 || !strings.Contains(feedback, `"truncated":true`) {
		t.Fatalf("feedback not bounded and explicitly truncated: %d bytes", len(feedback))
	}
	_, body, _ := strings.Cut(feedback, "\n")
	var payload struct {
		Findings []reviewprofile.WorkerDispute `json:"findings"`
	}
	if err = json.Unmarshal([]byte(body), &payload); err != nil || len(payload.Findings) != 20 {
		t.Fatalf("truncation lost structured references: %v", err)
	}
	for _, f := range d.profileReviews.Findings() {
		if !strings.Contains(body, f.ID) {
			t.Fatalf("finding %s dropped from feedback", f.ID)
		}
	}
}

func TestWorkerDisputeStackCaptureKeepsLaneOwnership(t *testing.T) {
	d, s := profileFixture(t)
	s.Stack, s.Worktree = true, t.TempDir()
	s.Apps = []session.App{
		{Name: "api", Repo: s.Repo, Branch: s.Branch, Worktree: t.TempDir(), PR: s.PR},
		{Name: "web", Repo: "owner/web", Branch: s.Branch, Worktree: t.TempDir()},
	}
	d.sessions.Upsert(s)
	d.cfg.ReviewProviders[0].SendToAgent = true
	d.profileRun = func(context.Context, string, reviewprofile.Profile, reviewprofile.Inputs) (reviewprofile.Result, error) {
		return disputeResult(), nil
	}
	api, _ := d.sessions.Get(session.LaneID(s.ID, "api"))
	web, _ := d.sessions.Get(session.LaneID(s.ID, "web"))
	r, err := d.runProfile(context.Background(), profileArgs{Session: api.ID, Profile: "backend", Contract: "No lost writes"})
	if err != nil {
		t.Fatal(err)
	}
	f := d.profileReviews.Findings()[0]
	writeDispute(t, web, f)
	if err = d.captureProfileDisputes(s.ID); err == nil || d.sessionDisputeHold(api) {
		t.Fatal("another lane claimed this finding")
	}
	if err = os.Remove(filepath.Join(web.Worktree, ".lola", "review-disputes.json")); err != nil {
		t.Fatal(err)
	}
	writeDispute(t, api, f)
	if err = d.captureProfileDisputes(s.ID); err != nil || !d.sessionDisputeHold(api) || d.sessionDisputeHold(web) {
		t.Fatalf("parent capture failed to scope the dispute to its lane: %v", err)
	}
	api, _ = d.sessions.Get(api.ID)
	web, _ = d.sessions.Get(web.ID)
	if api.PendingHandoffs[specialistHandoffKey(r)] == "" || len(web.PendingHandoffs) != 0 {
		t.Fatal("specialist handoff was not kept on the reviewed lane")
	}
}

func TestWorkerDisputeFileRejectsAuthoritySpoofingAndUnsafeFiles(t *testing.T) {
	d, s, _, f := disputeFixture(t)
	writeDispute(t, s, f)
	path := filepath.Join(s.Worktree, ".lola", "review-disputes.json")
	original, _ := os.ReadFile(path)
	forged := strings.Replace(string(original), `"evidence":`, `"state":"verified","evidence":`, 1)
	if err := os.WriteFile(path, []byte(forged), 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.captureProfileDisputes(s.ID); err == nil {
		t.Fatal("worker selected an operator decision")
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if err := d.captureProfileDisputes(s.ID); err == nil {
		t.Fatal("followed an escaping dispute symlink")
	}
	if d.sessionDisputeHold(s) {
		t.Fatal("invalid file changed finding state")
	}
}

func TestGoalWorkerDisputeHoldsProgressUntilHumanResolution(t *testing.T) {
	d, _, g, policy, launched := goalExecutionFixture(t)
	var err error
	g, err = d.authorizeGoal(context.Background(), goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	g, _ = d.goals.Get(g.ID)
	var worker session.Session
	for _, a := range g.Execution.Attempts {
		worker, _ = d.sessions.Get(a.SessionID)
		break
	}
	p := reviewprofile.Profile{ID: "backend", Kind: "backend", Provider: "codex-session", Version: "1", Enabled: true}
	in := reviewprofile.Inputs{Owner: &reviewprofile.ReviewOwner{Project: g.Project, Goal: g.ID, Task: worker.TaskID, Session: "previous-attempt"}, Repo: "owner/repo", Head: headA, Base: headB, Contract: "No lost writes"}
	r, err := d.profileReviews.Begin(p, in, false)
	if err != nil {
		t.Fatal(err)
	}
	r.State, r.Result = "findings", disputeResult()
	if err = d.profileReviews.Finish(r); err != nil {
		t.Fatal(err)
	}
	f := d.profileReviews.Findings()[0]
	writeDispute(t, worker, f)
	// The controller must collect reports even if a worker's Stop callback is
	// delayed or lost, before allowing more work or integration.
	if err = d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	blocked, _ := d.goals.Get(g.ID)
	if blocked.State != "blocked" || blocked.Execution.StopWorkersAt.IsZero() || !strings.Contains(blocked.Execution.Reason, f.ID) {
		t.Fatal("blocking disagreement did not reach human steering")
	}
	before := *launched
	if _, err = d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: blocked.Revision, Action: "resume"}); err == nil {
		t.Fatal("resume ignored unresolved dispute")
	}
	if err = d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	if *launched != before {
		t.Fatal("dispute launched more workers")
	}
	reports := d.profileReviews.Findings()
	var disputed reviewprofile.FindingReport
	for _, item := range reports {
		if item.ID == f.ID {
			disputed = item
		}
	}
	if _, err = d.profileReviews.Decide(reviewprofile.FindingDecision{ID: f.ID, Revision: disputed.Revision, State: "dismissed", Evidence: "Human reproduced the exact API path and confirmed the counterexample"}); err != nil {
		t.Fatal(err)
	}
	blocked, _ = d.goals.Get(g.ID)
	if err = d.goalDisputeHold(blocked); err != nil {
		t.Fatal("resolved dispute stayed blocking", err)
	}
	if _, err = d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: blocked.Revision, Action: "resume"}); err != nil {
		t.Fatal("human resolution did not permit explicit resume", err)
	}
	// Human resolution changes only the annotation. The independent review is
	// still a findings result and must be replaced by clean verification.
	stored, _ := d.profileReviews.Get(r.Key)
	if stored.State != "findings" {
		t.Fatal("human dismissal bypassed independent verification")
	}
	// A later operator disagreement must enforce the same durable hold as a
	// worker report, including when the goal has explicitly resumed.
	body, _ := json.Marshal(reviewprofile.FindingDecision{ID: f.ID, Revision: disputed.Revision + 1, State: "disputed", Evidence: "New human reproduction contradicts the previous resolution"})
	response := d.handleProfile(context.Background(), protocol.Request{Cmd: "profileDecideFinding", Args: body})
	blocked, _ = d.goals.Get(g.ID)
	if response.Error != "" || blocked.State != "blocked" || blocked.Execution.StopWorkersAt.IsZero() {
		t.Fatalf("operator dispute did not restore the goal hold: %s", response.Error)
	}
}
