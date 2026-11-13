package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/goalpublish"
	"github.com/sushidev-team/lola/internal/goalwork"
)

func readyPublicationFixture(t *testing.T, changes ...func(*goalpolicy.Policy)) (*Daemon, goal.Goal) {
	t.Helper()
	ctx := context.Background()
	d, g, _, _ := submittedGoalFixture(t, func(p *goalpolicy.Policy) {
		p.PublishPR = true
		for _, change := range changes {
			change(p)
		}
	})
	if err := d.verifyGoalIntegration(ctx, g.ID, g.Execution.PendingIntegration); err != nil {
		t.Fatal(err)
	}
	if err := d.runGoal(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	g = submitRunningGoalTask(t, d, g.ID)
	if err := d.verifyGoalIntegration(ctx, g.ID, g.Execution.PendingIntegration); err != nil {
		t.Fatal(err)
	}
	if err := d.runGoal(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	g, _ = d.goals.Get(g.ID)
	if err := d.verifyGoalFinal(ctx, g.ID, g.Execution.Final.ID); err != nil {
		t.Fatal(err)
	}
	g, _ = d.goals.Get(g.ID)
	return d, g
}

func TestGoalPublicationIsObservedSeparatelyFromHumanMerge(t *testing.T) {
	d, g := readyPublicationFixture(t)
	ctx := context.Background()
	branch := "goal/" + g.ID
	head := g.Execution.Manifest["p1"]
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	repository, err := m.Repository(ctx, g.ID, "p1")
	if err != nil {
		t.Fatal(err)
	}
	base := repository.Start
	remoteHead := ""
	var pr map[string]any
	pushes, creates := 0, 0
	d.goalPublishRun = func(_ context.Context, _ string, bin string, args []string, input []byte) ([]byte, error) {
		encode := func(v any) []byte { b, _ := json.Marshal(v); return b }
		if bin == "git" {
			if args[len(args)-1] != head+":refs/heads/"+branch {
				t.Fatal("wrong push target")
			}
			pushes++
			remoteHead = head
			return nil, nil
		}
		if slices.Contains(args, "graphql") {
			var ref any
			if remoteHead != "" {
				ref = map[string]any{"target": map[string]string{"oid": remoteHead}}
			}
			return encode(map[string]any{"data": map[string]any{"repository": map[string]any{"nameWithOwner": "acme/widgets", "defaultBranchRef": map[string]string{"name": "main"}, "head": ref, "base": map[string]any{"target": map[string]string{"oid": base}}}}}), nil
		}
		if slices.Contains(args, "GET") {
			if pr == nil {
				return []byte("[]"), nil
			}
			return encode([]any{pr}), nil
		}
		if !slices.Contains(args, "POST") || !slices.Contains(args, "repos/acme/widgets/pulls") {
			t.Fatalf("forbidden publication command %v", args)
		}
		var body map[string]any
		if err := json.Unmarshal(input, &body); err != nil {
			t.Fatal(err)
		}
		if body["draft"] != true || body["base"] != "main" || body["head"] != branch {
			t.Fatal("publication was not an isolated draft")
		}
		creates++
		pr = map[string]any{"number": 7, "html_url": "https://github.com/acme/widgets/pull/7", "body": body["body"], "state": "open", "draft": true, "head": map[string]any{"ref": branch, "sha": head, "repo": map[string]string{"full_name": "acme/widgets"}}, "base": map[string]any{"ref": "main", "sha": base, "repo": map[string]string{"full_name": "acme/widgets"}}}
		return encode(pr), nil
	}
	if err := d.runGoal(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	g, _ = d.goals.Get(g.ID)
	if g.State != "ready" || g.Execution.Delivery == nil || g.Execution.Delivery.State != "published" || pushes != 1 || creates != 1 {
		t.Fatalf("draft publication mistaken for completion: %+v", g.Execution.Delivery)
	}
	d.goals, _ = goal.Open(filepath.Join(d.home, "state", "goals.json"))
	r, err := d.publishVerifiedGoal(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.recordGoalPublication(g.ID, r, nil)
	if pushes != 1 || creates != 1 {
		t.Fatal("restart duplicated upstream writes")
	}
	pr["state"] = "closed"
	pr["merged_at"] = time.Now().UTC().Format(time.RFC3339)
	remoteHead = ""
	r, err = d.publishVerifiedGoal(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.recordGoalPublication(g.ID, r, nil)
	g, _ = d.goals.Get(g.ID)
	if g.State != "accepted" || g.Execution.Delivery.State != "merged" {
		t.Fatal("exact human delivery did not complete goal")
	}
	if pushes != 1 || creates != 1 {
		t.Fatal("observing a merge performed a write")
	}
}

func TestGoalPublicationRechecksRubricsAndSteering(t *testing.T) {
	d, g := readyPublicationFixture(t)
	d.goalPublishRun = func(context.Context, string, string, []string, []byte) ([]byte, error) {
		t.Fatal("unverified goal reached GitHub")
		return nil, nil
	}
	d.cfg.ReviewProfiles[0].Version = "changed"
	if _, err := d.publishVerifiedGoal(context.Background(), g.ID); err == nil {
		t.Fatal("changed final rubric accepted")
	}
	d.cfg.ReviewProfiles[0].Version = "1"
	g, err := d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "pause"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.publishVerifiedGoal(context.Background(), g.ID); err == nil {
		t.Fatal("paused goal published")
	}
	// A late async error must not erase the human's paused state.
	d.recordGoalPublication(g.ID, goalpublish.Record{}, errors.New("canceled publication"))
	g, _ = d.goals.Get(g.ID)
	if g.State != "paused" {
		t.Fatal("publication overwrote steering")
	}
	g, err = d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "resume"})
	if err != nil || g.State != "ready" {
		t.Fatalf("resume lost verified artifact: %s %v", g.State, err)
	}
}

func TestGoalSharedEvidenceIsRevisionCheckedAndDoesNotLeakCommands(t *testing.T) {
	d, g := readyPublicationFixture(t)
	if _, err := d.setGoalDeliveryEvidence(goal.DeliveryEvidenceRequest{ID: g.ID, Revision: g.Revision - 1, URL: "https://artifacts.example/goal"}); err == nil {
		t.Fatal("stale evidence edit accepted")
	}
	for _, url := range []string{"file:///private/report.html", "javascript:alert(1)", "https://secret@example.com/report"} {
		if _, err := d.setGoalDeliveryEvidence(goal.DeliveryEvidenceRequest{ID: g.ID, Revision: g.Revision, URL: url}); err == nil {
			t.Fatal("unsafe evidence link accepted")
		}
	}
	g, err := d.setGoalDeliveryEvidence(goal.DeliveryEvidenceRequest{ID: g.ID, Revision: g.Revision, URL: "https://artifacts.example/goal"})
	if err != nil {
		t.Fatal(err)
	}
	// PR prose lists check identities, never raw commands/configured secrets.
	g.Execution.Policy.Checks[0].Command = "TOKEN=do-not-publish ./test"
	body := goalPRBody(g, goalwork.Verification{})
	if strings.Contains(body, "do-not-publish") || !strings.Contains(body, "https://artifacts.example/goal") || !strings.Contains(body, "## Risk assessment") {
		t.Fatal("PR description lost evidence or exposed private configuration")
	}
}

func TestGoalPauseCancelsBlockedPublicationBeforeWaitingForEffectLock(t *testing.T) {
	d, g := readyPublicationFixture(t)
	started := make(chan struct{})
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	repository, err := m.Repository(context.Background(), g.ID, "p1")
	if err != nil {
		t.Fatal(err)
	}
	d.goalPublishRun = func(ctx context.Context, _ string, bin string, args []string, _ []byte) ([]byte, error) {
		if bin == "git" {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		if slices.Contains(args, "graphql") {
			return []byte(fmt.Sprintf(`{"data":{"repository":{"nameWithOwner":"acme/widgets","defaultBranchRef":{"name":"main"},"head":null,"base":{"target":{"oid":%q}}}}}`, repository.Start)), nil
		}
		if slices.Contains(args, "GET") {
			return []byte("[]"), nil
		}
		t.Error("paused goal reached PR creation")
		return nil, errors.New("unexpected mutation")
	}
	if err := d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	<-started
	done := make(chan error, 1)
	go func() {
		_, err := d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "pause"})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pause waited for the publication network timeout")
	}
	d.connWg.Wait()
	current, _ := d.goals.Get(g.ID)
	if current.State != "paused" {
		t.Fatal("late publication completion overwrote pause")
	}
}

func TestGoalPRDescriptionBoundsLargeContractsWithoutDroppingRepositoryLinks(t *testing.T) {
	g := goal.Goal{ID: "12345678-1234-1234-1234-123456789abc", Outcome: strings.Repeat("界", 5000), Execution: &goal.Execution{Manifest: map[string]string{}}}
	v := goalwork.Verification{Targets: map[string]string{}}
	g.Execution.DeliveryEvidenceURL = "https://example.com/" + strings.Repeat("a", 2000)
	g.Execution.Policy.Rollout = &goalpolicy.Rollout{}
	for i := 0; i < 20; i++ {
		g.Execution.Policy.Rollout.Compatibility = append(g.Execution.Policy.Rollout.Compatibility, fmt.Sprintf("%02d", i)+strings.Repeat("x", 498))
	}
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("member-%02d-%s", i, strings.Repeat("m", 110))
		g.Execution.Policy.Rollout.MergeOrder = append(g.Execution.Policy.Rollout.MergeOrder, id)
		g.Tasks = append(g.Tasks, goal.Task{TaskDraft: goal.TaskDraft{Title: strings.Repeat("界", 80), Acceptance: []string{strings.Repeat("界", 600), strings.Repeat("界", 600), strings.Repeat("界", 600), "full remaining criterion"}}})
		g.Execution.Policy.Members = append(g.Execution.Policy.Members, goalpolicy.Member{ID: id, Repo: "owner/" + strings.Repeat("r", 240), Target: strings.Repeat("branch/", 100)})
		g.Execution.Policy.Checks = append(g.Execution.Policy.Checks, goalpolicy.Check{ID: strings.Repeat("c", 120)})
		g.Execution.Manifest[id] = strings.Repeat("a", 64)
		v.Targets[id] = strings.Repeat("b", 64)
		v.Reviews = append(v.Reviews, goalwork.Check{ID: strings.Repeat("p", 64)})
	}
	body := goalPRBody(g, v)
	if len(body) > 60000 || !utf8.ValidString(body) {
		t.Fatalf("invalid bounded PR prose: %d bytes", len(body))
	}
	for _, m := range g.Execution.Policy.Members {
		if !strings.Contains(body, "["+m.ID+"]") {
			t.Fatal("large description omitted repository link")
		}
	}
	if !strings.Contains(body, "complete acceptance contract") {
		t.Fatal("abbreviated checklist presented as exhaustive")
	}
	for _, requirement := range g.Execution.Policy.Rollout.Compatibility {
		if !strings.Contains(body, requirement) {
			t.Fatal("large description truncated reviewed compatibility")
		}
	}
	g.Execution.Policy.Rollout = nil
	if legacy := goalPRBody(g, v); len(legacy) > 60000 || !strings.Contains(legacy, "not a merge recommendation") {
		t.Fatal("legacy stack description exceeded bounds or invented a merge order")
	}
}
