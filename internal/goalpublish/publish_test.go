package goalpublish

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/goalwork"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Test", "-c", "user.email=test@localhost", "-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture git: %v %s", err, b)
	}
	return strings.TrimSpace(string(b))
}

func fixture(t *testing.T, changes ...func(*goalpolicy.Policy)) (*Publisher, Request, *remote) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, source, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(source, "app.txt"), []byte("initial"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, source, "add", "app.txt")
	git(t, source, "commit", "-m", "initial")
	m := &goalwork.Manager{Root: filepath.Join(root, "goals")}
	repo, err := m.Initialize(ctx, "g1", "api", source, "main")
	if err != nil {
		t.Fatal(err)
	}
	w, err := m.PrepareWorker(ctx, "g1", "api", "t1", "a1", repo.Start)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Directory, "app.txt"), []byte("verified change"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, w.Directory, "add", "app.txt")
	git(t, w.Directory, "commit", "-m", "task")
	head := git(t, w.Directory, "rev-parse", "HEAD")
	git(t, w.Directory, "bundle", "create", filepath.Join(w.Directory, ".lola", "submission.bundle"), "HEAD", "^"+w.Base)
	c, err := m.Stage(ctx, w, "integrate", repo.Start, head, nil)
	if err != nil {
		t.Fatal(err)
	}
	policy := goalpolicy.Policy{Members: []goalpolicy.Member{{ID: "api", Project: "api", Source: source, Repo: "owner/api", Target: "main"}}, Checks: []goalpolicy.Check{{ID: "unit", Command: "make test", Image: "sha256:" + strings.Repeat("a", 64), TimeoutSeconds: 60}}, PublishPR: true, Limits: goalpolicy.Limits{Concurrency: 1, MaxTasks: 3, AttemptsPerTask: 2, ReviewRounds: 2, AgentRuns: 10, RuntimeMinutes: 60, AttemptMinutes: 10, Tokens: 100000}}
	for _, change := range changes {
		change(&policy)
	}
	digest, err := policy.Digest()
	if err != nil {
		t.Fatal(err)
	}
	v := goalwork.Verification{ID: "proof1", Goal: "g1", Operation: "integrate", Manifest: map[string]string{"api": c.Commit}, Policy: digest, Checks: []goalwork.Check{{ID: "unit", State: "pass"}}, At: time.Now().UTC()}
	if _, err := m.AdvanceManifest(ctx, "g1", "integrate", map[string]string{"api": repo.Start}, []goalwork.Candidate{c}, v, digest, []string{"unit"}, nil); err != nil {
		t.Fatal(err)
	}
	final, err := m.FinalCandidate(ctx, "g1", "final", "api", c.Commit)
	if err != nil {
		t.Fatal(err)
	}
	v.ID = "final-proof"
	v.Operation = "final"
	if _, err := m.RecordFinal(ctx, []goalwork.Candidate{final}, v, digest, []string{"unit"}, nil); err != nil {
		t.Fatal(err)
	}
	want := Request{Goal: "g1", Operation: "final", Policy: policy, Manifest: map[string]string{"api": c.Commit}, Title: "feat(goal): implement accepted behavior", Body: "## TL;DR\nVerified goal.\n\n## Test plan\nAcceptance passed; human review pending."}
	f := &remote{t: t, m: m, repo: repo, head: c.Commit, defaultBranch: "main"}
	p := &Publisher{Workspaces: m, Run: f.run, Guard: func(context.Context) (func(), error) {
		f.guards++
		if f.denied {
			return nil, errors.New("paused")
		}
		return func() {}, nil
	}}
	return p, want, f
}

type remote struct {
	operation                             string
	t                                     *testing.T
	m                                     *goalwork.Manager
	repo                                  goalwork.Repository
	head, branchHead, defaultBranch       string
	pr                                    *pullRequest
	pushes, creates, guards               int
	losePush, losePR, denied, unavailable bool
	advanceOnPush                         bool
}

func (f *remote) run(_ context.Context, dir, bin string, args []string, input []byte) ([]byte, error) {
	f.t.Helper()
	repoName := "owner/" + f.repo.Member
	if f.unavailable {
		return nil, errors.New("GitHub unavailable")
	}
	encode := func(v any) []byte { b, _ := json.Marshal(v); return b }
	if bin == "git" {
		if !slices.Contains(args, "push") || dir != f.repo.GitDir || args[len(args)-1] != f.head+":refs/heads/goal/g1" || args[len(args)-2] != "https://github.com/"+repoName+".git" {
			f.t.Fatalf("unexpected upstream command: %s %v", dir, args)
		}
		for _, arg := range args {
			if strings.HasPrefix(arg, "--force") || arg == "--mirror" || arg == "--all" || arg == "--delete" {
				f.t.Fatal("unsafe push option")
			}
		}
		f.intent()
		f.pushes++
		f.branchHead = f.head
		if f.advanceOnPush {
			f.repo.Start = strings.Repeat("b", 40)
		}
		if f.losePush {
			f.losePush = false
			return nil, errors.New("lost push response")
		}
		return []byte("pushed"), nil
	}
	if bin != "gh" {
		f.t.Fatalf("unexpected executable %s", bin)
	}
	if slices.Contains(args, "graphql") {
		var request struct {
			Variables map[string]string `json:"variables"`
		}
		if err := json.Unmarshal(input, &request); err != nil {
			f.t.Fatal(err)
		}
		if request.Variables["head"] != "refs/heads/goal/g1" || request.Variables["base"] != "refs/heads/main" {
			f.t.Fatal("unexpected GitHub refs")
		}
		var head any
		if f.branchHead != "" {
			head = map[string]any{"target": map[string]string{"oid": f.branchHead}}
		}
		return encode(map[string]any{"data": map[string]any{"repository": map[string]any{"nameWithOwner": repoName, "defaultBranchRef": map[string]string{"name": f.defaultBranch}, "head": head, "base": map[string]any{"target": map[string]string{"oid": f.repo.Start}}}}}), nil
	}
	if !slices.Contains(args, "repos/"+repoName+"/pulls") {
		f.t.Fatalf("unexpected GitHub endpoint: %v", args)
	}
	if slices.Contains(args, "GET") {
		if f.pr == nil {
			return []byte("[]"), nil
		}
		return encode([]pullRequest{*f.pr}), nil
	}
	if !slices.Contains(args, "POST") {
		f.t.Fatalf("forbidden GitHub mutation: %v", args)
	}
	f.intent()
	f.creates++
	var body struct {
		Title, Head, Base, Body string
		Draft                   bool
		MaintainerCanModify     bool `json:"maintainer_can_modify"`
	}
	if err := json.Unmarshal(input, &body); err != nil {
		f.t.Fatal(err)
	}
	if !body.Draft || body.MaintainerCanModify || body.Head != "goal/g1" || body.Base != "main" || !strings.HasPrefix(body.Title, "feat(goal): ") {
		f.t.Fatal("invalid PR request")
	}
	pr := &pullRequest{Number: 17, URL: "https://github.com/" + repoName + "/pull/17", Body: body.Body, State: "open", Draft: true}
	pr.Head.Ref = body.Head
	pr.Head.SHA = f.head
	pr.Head.Repo.FullName = repoName
	pr.Base.Ref = body.Base
	pr.Base.SHA = f.repo.Start
	pr.Base.Repo.FullName = repoName
	f.pr = pr
	if f.losePR {
		f.losePR = false
		return nil, errors.New("lost create response")
	}
	return encode(pr), nil
}
func (f *remote) intent() {
	f.t.Helper()
	operation := f.operation
	if operation == "" {
		operation = "final"
	}
	dir, err := f.m.DeliveryDirectory("g1", operation)
	if err != nil {
		f.t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "publication.json"))
	if err != nil {
		f.t.Fatal("external effect before durable intent")
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil || r.Request.Manifest[f.repo.Member] != f.head {
		f.t.Fatal("wrong publication reservation")
	}
	if f.guards == 0 {
		f.t.Fatal("external write without live guard")
	}
}

func TestPublicationCreatesOnlyGoalBranchAndDraftPR(t *testing.T) {
	p, want, f := fixture(t)
	ctx := context.Background()
	r, err := p.Publish(ctx, want)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Complete || r.Members["api"].State != "published" || !r.Members["api"].Draft || f.pushes != 1 || f.creates != 1 || f.guards != 3 {
		t.Fatalf("publication: %+v", r)
	}
	if _, err := p.Publish(ctx, want); err != nil {
		t.Fatal(err)
	}
	if f.pushes != 1 || f.creates != 1 {
		t.Fatal("repeat publication duplicated effects")
	}
	if got := git(t, f.repo.Source, "rev-parse", "main"); got != f.repo.Start {
		t.Fatal("source main changed")
	}
	if got := git(t, f.repo.GitDir, "rev-parse", "main"); got != f.repo.Start {
		t.Fatal("private main changed")
	}
	merged := time.Now().UTC().Format(time.RFC3339)
	f.pr.State = "closed"
	f.pr.MergedAt = &merged
	f.branchHead = ""
	r, err = p.Publish(ctx, want)
	if err != nil || r.Members["api"].State != "merged" {
		t.Fatalf("human merge observation: %+v %v", r, err)
	}
	if f.pushes != 1 || f.creates != 1 {
		t.Fatal("merge observation wrote remote state")
	}
}

func TestPublicationRecoversLostExternalResponses(t *testing.T) {
	for _, phase := range []string{"push", "PR"} {
		t.Run(phase, func(t *testing.T) {
			p, want, f := fixture(t)
			f.losePush = phase == "push"
			f.losePR = phase == "PR"
			if _, err := p.Publish(context.Background(), want); err == nil {
				t.Fatal("lost response was treated as acknowledged")
			}
			// New publisher instance simulates losing all in-memory state.
			restarted := &Publisher{Workspaces: p.Workspaces, Run: p.Run, Guard: p.Guard}
			r, err := restarted.Publish(context.Background(), want)
			if err != nil || !r.Complete {
				t.Fatalf("recovery: %+v %v", r, err)
			}
			if f.pushes != 1 || f.creates != 1 {
				t.Fatal("restart duplicated remote effect")
			}
		})
	}
}

func TestPublicationFailsClosedBeforeWrongOrUnverifiedWrites(t *testing.T) {
	for _, mode := range []string{"default-branch", "foreign-head", "advanced-base", "paused", "unavailable", "changed-source", "policy-denied"} {
		t.Run(mode, func(t *testing.T) {
			p, want, f := fixture(t)
			switch mode {
			case "default-branch":
				f.defaultBranch = "goal/g1"
			case "foreign-head":
				f.branchHead = strings.Repeat("b", 40)
			case "advanced-base":
				f.repo.Start = strings.Repeat("b", 40)
			case "paused":
				f.denied = true
			case "unavailable":
				f.unavailable = true
			case "policy-denied":
				want.Policy.PublishPR = false
			case "changed-source":
				if err := os.WriteFile(filepath.Join(p.Workspaces.Root, "g1", "candidates", "final", "api", "tree", "app.txt"), []byte("unverified"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := p.Publish(context.Background(), want); err == nil {
				t.Fatal("unsafe publication accepted")
			}
			if f.pushes != 0 || f.creates != 0 {
				t.Fatal("unsafe request reached a remote write")
			}
		})
	}
}

func TestPublicationHoldsTargetDriftBetweenPushAndPR(t *testing.T) {
	p, want, f := fixture(t)
	f.advanceOnPush = true
	if _, err := p.Publish(context.Background(), want); err == nil {
		t.Fatal("target drift reused old final evidence")
	}
	if f.pushes != 1 || f.creates != 0 {
		t.Fatal("stale target reached PR creation")
	}
	if _, err := p.Publish(context.Background(), want); err == nil {
		t.Fatal("retry ignored target drift")
	}
	if f.pushes != 1 || f.creates != 0 {
		t.Fatal("stale retry wrote remote state")
	}
}

func TestPublicationPreservesHumanEditsAndClosedPRs(t *testing.T) {
	p, want, f := fixture(t)
	if _, err := p.Publish(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	f.pr.Body += "\nHuman rollout notes"
	f.pr.State = "closed"
	r, err := p.Publish(context.Background(), want)
	if err != nil || r.Members["api"].State != "closed" {
		t.Fatal("closed PR was not retained")
	}
	if f.creates != 1 || !strings.Contains(f.pr.Body, "Human rollout notes") {
		t.Fatal("human work overwritten")
	}
	f.pr.Base.Ref = "release"
	if _, err := p.Publish(context.Background(), want); err == nil {
		t.Fatal("retargeted PR accepted")
	}
	if f.creates != 1 {
		t.Fatal("retargeted PR replaced")
	}
}

func TestPublicationRecoversPartialStackWithoutRepublishingAcknowledgedMember(t *testing.T) {
	p, want, api := fixture(t, func(p *goalpolicy.Policy) { p.Limits.TargetRefreshes = 1 })
	ctx := context.Background()
	m := p.Workspaces
	source := filepath.Join(t.TempDir(), "web")
	git(t, filepath.Dir(source), "clone", "--no-local", api.repo.Source, source)
	repo, err := m.Initialize(ctx, "g1", "web", source, "main")
	if err != nil {
		t.Fatal(err)
	}
	want.Policy.Members = append(want.Policy.Members, goalpolicy.Member{ID: "web", Project: "web", Source: source, Repo: "owner/web", Target: "main"})
	digest, err := want.Policy.Digest()
	if err != nil {
		t.Fatal(err)
	}
	w, err := m.PrepareWorker(ctx, "g1", "web", "t2", "a2", repo.Start)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Directory, "app.txt"), []byte("verified web change"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, w.Directory, "add", "app.txt")
	git(t, w.Directory, "commit", "-m", "web task")
	head := git(t, w.Directory, "rev-parse", "HEAD")
	git(t, w.Directory, "bundle", "create", filepath.Join(w.Directory, ".lola", "submission.bundle"), "HEAD", "^"+w.Base)
	c, err := m.Stage(ctx, w, "web-integrate", repo.Start, head, nil)
	if err != nil {
		t.Fatal(err)
	}
	want.Manifest["web"] = c.Commit
	v := goalwork.Verification{ID: "stack-proof", Goal: "g1", Operation: "web-integrate", Manifest: want.Manifest, Policy: digest, Checks: []goalwork.Check{{ID: "unit", State: "pass"}}, At: time.Now().UTC()}
	if _, err := m.AdvanceManifest(ctx, "g1", "web-integrate", map[string]string{"api": api.head, "web": repo.Start}, []goalwork.Candidate{c}, v, digest, []string{"unit"}, nil); err != nil {
		t.Fatal(err)
	}
	want.Operation = "stack-final"
	v.Operation = want.Operation
	var candidates []goalwork.Candidate
	for _, id := range []string{"api", "web"} {
		c, err := m.FinalCandidate(ctx, "g1", want.Operation, id, want.Manifest[id])
		if err != nil {
			t.Fatal(err)
		}
		candidates = append(candidates, c)
	}
	if _, err := m.RecordFinal(ctx, candidates, v, digest, []string{"unit"}, nil); err != nil {
		t.Fatal(err)
	}
	api.operation = want.Operation
	web := &remote{t: t, m: m, repo: repo, head: c.Commit, defaultBranch: "main", operation: want.Operation, losePR: true}
	p.Run = func(ctx context.Context, dir, bin string, args []string, stdin []byte) ([]byte, error) {
		if dir == repo.GitDir || slices.Contains(args, "repos/owner/web/pulls") || strings.Contains(string(stdin), `"name":"web"`) {
			return web.run(ctx, dir, bin, args, stdin)
		}
		return api.run(ctx, dir, bin, args, stdin)
	}
	p.Guard = func(context.Context) (func(), error) { api.guards++; web.guards++; return func() {}, nil }
	r, err := p.Publish(ctx, want)
	if err == nil || r.Complete || r.Members["api"].Number != 17 || r.Members["web"].Number != 0 {
		t.Fatalf("partial stack receipt: %+v %v", r, err)
	}
	p = &Publisher{Workspaces: m, Run: p.Run, Guard: p.Guard}
	r, err = p.Publish(ctx, want)
	if err != nil || !r.Complete {
		t.Fatalf("stack recovery: %+v %v", r, err)
	}
	if api.pushes != 1 || api.creates != 1 || web.pushes != 1 || web.creates != 1 {
		t.Fatal("stack recovery duplicated acknowledged remote work")
	}
	if r.Members["api"].URL == r.Members["web"].URL {
		t.Fatal("stack delivery lost independent PR links")
	}
	verifyRefreshedStackRecovery(t, p, want, api, web)
}
