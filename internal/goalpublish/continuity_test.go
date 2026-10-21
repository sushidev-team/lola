package goalpublish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/goalwork"
)

type revisionRemote struct {
	*remote
	comments     []evidenceComment
	commentPosts int
	loseComment  bool
}

func (f *revisionRemote) run(ctx context.Context, dir, bin string, args []string, input []byte) ([]byte, error) {
	endpoint := "repos/owner/" + f.repo.Member + "/issues/17/comments"
	if bin == "gh" && slices.Contains(args, endpoint) {
		encode := func(v any) []byte { b, _ := json.Marshal(v); return b }
		if slices.Contains(args, "GET") {
			page := 1
			for _, arg := range args {
				if value, ok := strings.CutPrefix(arg, "page="); ok {
					page, _ = strconv.Atoi(value)
				}
			}
			start := min(len(f.comments), max(0, page-1)*100)
			return encode(f.comments[start:min(len(f.comments), start+100)]), nil
		}
		if !slices.Contains(args, "POST") {
			f.t.Fatal("attempted to edit remote discussion", args)
		}
		f.intent()
		var body struct {
			Body string `json:"body"`
		}
		if err := json.Unmarshal(input, &body); err != nil {
			f.t.Fatal(err)
		}
		f.commentPosts++
		id := int64(10000 + f.commentPosts)
		c := evidenceComment{ID: id, Body: body.Body, URL: fmt.Sprintf("https://github.com/owner/%s/pull/17#issuecomment-%d", f.repo.Member, id)}
		f.comments = append(f.comments, c)
		if f.loseComment {
			f.loseComment = false
			return nil, errors.New("lost evidence comment response")
		}
		return encode(c), nil
	}
	out, err := f.remote.run(ctx, dir, bin, args, input)
	if bin == "git" && slices.Contains(args, "push") && f.pr != nil {
		f.pr.Head.SHA = f.head
	}
	return out, err
}

func continuityFixture(t *testing.T) (*Publisher, Request, *revisionRemote) {
	t.Helper()
	p, want, base := fixture(t, func(p *goalpolicy.Policy) { p.Limits.TargetRefreshes = 3 })
	f := &revisionRemote{remote: base}
	p.Run = f.run
	if _, err := p.Publish(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	return p, want, f
}

func nextRevision(t *testing.T, p *Publisher, want Request, f *revisionRemote, operation string) Request {
	t.Helper()
	ctx := context.Background()
	r, err := p.Workspaces.Repository(ctx, want.Goal, "api")
	if err != nil {
		t.Fatal(err)
	}
	prior := r.Start
	if len(want.Targets) > 0 {
		prior = want.Targets["api"]
	}
	file := operation + ".txt"
	if err := os.WriteFile(filepath.Join(r.Source, file), []byte("upstream "+operation+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, r.Source, "add", file)
	git(t, r.Source, "commit", "-m", "upstream "+operation)
	target := git(t, r.Source, "rev-parse", "HEAD")
	op := "refresh-" + operation
	s, err := p.Workspaces.PrepareTarget(ctx, want.Goal, op, "api", want.Manifest["api"])
	if err != nil {
		t.Fatal(err)
	}
	git(t, s.GitDir, "fetch", "--no-tags", r.Source, target)
	c, err := p.Workspaces.StageRefresh(ctx, s, prior, target)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := want.Policy.Digest()
	v := goalwork.Verification{ID: op + "-proof", Goal: want.Goal, Operation: op, Manifest: map[string]string{"api": c.Commit}, Targets: map[string]string{"api": target}, Policy: digest, Checks: []goalwork.Check{{ID: "unit", State: "pass"}}, At: time.Now().UTC()}
	if _, err := p.Workspaces.AdvanceManifest(ctx, want.Goal, op, want.Manifest, []goalwork.Candidate{c}, v, digest, []string{"unit"}, nil); err != nil {
		t.Fatal(err)
	}
	final, err := p.Workspaces.FinalCandidateAt(ctx, want.Goal, operation, "api", c.Commit, target)
	if err != nil {
		t.Fatal(err)
	}
	v.ID, v.Operation = operation+"-proof", operation
	if _, err := p.Workspaces.RecordFinal(ctx, []goalwork.Candidate{final}, v, digest, []string{"unit"}, nil); err != nil {
		t.Fatal(err)
	}
	want.PreviousOperation, want.Operation = want.Operation, operation
	want.Manifest, want.Targets = v.Manifest, v.Targets
	want.Body = "## TL;DR\n\nNew verified revision " + operation + ".\n\n## Test plan\n\nChecks passed."
	f.repo.Start, f.head, f.operation = target, c.Commit, operation
	if f.pr != nil {
		f.pr.Base.SHA = target
	}
	return want
}

func TestRefreshedPublicationPreservesPRAndHumanDescription(t *testing.T) {
	p, want, f := continuityFixture(t)
	f.pr.Body += "\n\nHuman rollout notes: keep these."
	f.pr.Draft = false // a human has already requested review
	original := f.pr.Body
	for _, op := range []string{"final2", "final3"} {
		want = nextRevision(t, p, want, f, op)
		r, err := p.Publish(context.Background(), want)
		if err != nil {
			t.Fatal(err)
		}
		if !r.Complete || r.Members["api"].Number != 17 || r.Members["api"].MarkerOperation != "final" || r.Members["api"].EvidenceComment < 1 || f.creates != 1 || f.pr.Body != original || r.Members["api"].Draft {
			t.Fatalf("refresh replaced PR or human content: %+v", r)
		}
		if !strings.Contains(f.comments[len(f.comments)-1].Body, want.Body) {
			t.Fatal("latest proof was not shared")
		}
		pushes, posts := f.pushes, f.commentPosts
		// A new publisher process recovers the same PR and exact comment.
		p = &Publisher{Workspaces: p.Workspaces, Run: f.run, Guard: p.Guard}
		if _, err := p.Publish(context.Background(), want); err != nil {
			t.Fatal(err)
		}
		if f.pushes != pushes || f.commentPosts != posts || f.creates != 1 {
			t.Fatal("recovery duplicated remote effects")
		}
	}
	if f.pushes != 3 || f.commentPosts != 2 {
		t.Fatal("wrong number of distinct verified revisions")
	}
}

func TestRefreshedPublicationReconcilesLostPushAndEvidenceResponses(t *testing.T) {
	for _, mode := range []string{"push", "comment"} {
		t.Run(mode, func(t *testing.T) {
			p, want, f := continuityFixture(t)
			want = nextRevision(t, p, want, f, "final2")
			f.losePush, f.loseComment = mode == "push", mode == "comment"
			if _, err := p.Publish(context.Background(), want); err == nil {
				t.Fatal("lost response not surfaced")
			}
			p = &Publisher{Workspaces: p.Workspaces, Run: f.run, Guard: p.Guard}
			if r, err := p.Publish(context.Background(), want); err != nil || !r.Complete {
				t.Fatalf("recovery: %+v %v", r, err)
			}
			if f.pushes != 2 || f.creates != 1 || f.commentPosts != 1 {
				t.Fatalf("duplicated effects: pushes=%d creates=%d comments=%d", f.pushes, f.creates, f.commentPosts)
			}
		})
	}
}

func TestRefreshedPublicationHoldsRemoteOwnershipChanges(t *testing.T) {
	for _, mode := range []string{"foreign-head", "deleted-branch", "deleted-pr", "retargeted", "closed", "marker", "missing-predecessor", "predecessor-marker"} {
		t.Run(mode, func(t *testing.T) {
			p, want, f := continuityFixture(t)
			want = nextRevision(t, p, want, f, "final2")
			switch mode {
			case "foreign-head":
				f.branchHead = strings.Repeat("b", 40)
				f.pr.Head.SHA = f.branchHead
			case "deleted-branch":
				f.branchHead = ""
			case "deleted-pr":
				f.pr = nil
			case "retargeted":
				f.pr.Base.Ref = "release"
			case "closed":
				f.pr.State = "closed"
			case "marker":
				f.pr.Body = "human removed ownership marker"
			case "missing-predecessor":
				dir, _ := p.Workspaces.DeliveryDirectory(want.Goal, want.PreviousOperation)
				if err := os.Rename(filepath.Join(dir, "publication.json"), filepath.Join(dir, "retained-publication.json")); err != nil {
					t.Fatal(err)
				}
			case "predecessor-marker":
				dir, _ := p.Workspaces.DeliveryDirectory(want.Goal, want.PreviousOperation)
				path := filepath.Join(dir, "publication.json")
				b, _ := os.ReadFile(path)
				var r Record
				_ = json.Unmarshal(b, &r)
				m := r.Members["api"]
				m.MarkerOperation = "unrelated"
				r.Members["api"] = m
				if err := save(path, &r); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := p.Publish(context.Background(), want); err == nil {
				t.Fatal("remote or journal ownership drift was accepted")
			}
			if f.pushes != 1 || f.creates != 1 || f.commentPosts != 0 {
				t.Fatal("wrote upstream despite ownership drift")
			}
		})
	}
}

func TestRefreshedEvidencePreservesEditedOrDeletedComments(t *testing.T) {
	for _, mode := range []string{"edited", "deleted", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			p, want, f := continuityFixture(t)
			want = nextRevision(t, p, want, f, "final2")
			if _, err := p.Publish(context.Background(), want); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "edited":
				f.comments[0].Body = "human corrected evidence"
			case "deleted":
				f.comments = nil
			case "duplicate":
				c := f.comments[0]
				c.ID++
				f.comments = append(f.comments, c)
			}
			if _, err := p.Publish(context.Background(), want); err == nil {
				t.Fatal("edited evidence was silently replaced or accepted")
			}
			if f.pushes != 2 || f.creates != 1 || f.commentPosts != 1 {
				t.Fatal("recreated human-edited evidence")
			}
		})
	}
}

func TestRefreshedEvidenceFindsLostResponseBeyondFirstPage(t *testing.T) {
	p, want, f := continuityFixture(t)
	want = nextRevision(t, p, want, f, "final2")
	for i := range 120 {
		f.comments = append(f.comments, evidenceComment{ID: int64(1000 + i), Body: "human discussion"})
	}
	f.loseComment = true
	if _, err := p.Publish(context.Background(), want); err == nil {
		t.Fatal("lost response not surfaced")
	}
	if r, err := p.Publish(context.Background(), want); err != nil || !r.Complete {
		t.Fatalf("paginated evidence recovery: %+v %v", r, err)
	}
	if f.commentPosts != 1 || len(f.comments) != 121 || f.pushes != 2 {
		t.Fatal("paginated recovery duplicated or replaced discussion")
	}
}

func TestRefreshedEvidenceHoldAfterPushResumesWithoutAnotherPush(t *testing.T) {
	p, want, f := continuityFixture(t)
	want = nextRevision(t, p, want, f, "final2")
	guard := p.Guard
	p.Guard = func(ctx context.Context) (func(), error) {
		if f.pushes == 2 {
			return nil, errors.New("goal paused before evidence comment")
		}
		return guard(ctx)
	}
	if _, err := p.Publish(context.Background(), want); err == nil {
		t.Fatal("evidence ignored changed authority")
	}
	if f.pushes != 2 || f.commentPosts != 0 {
		t.Fatal("unexpected effects before pause")
	}
	p.Guard = guard
	if _, err := p.Publish(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if f.pushes != 2 || f.creates != 1 || f.commentPosts != 1 {
		t.Fatal("resume repeated push or lost evidence")
	}
}

func TestRefreshedPublicationDoesNotReopenAlreadyDeliveredMember(t *testing.T) {
	for _, mode := range []string{"fast-forward", "squash"} {
		t.Run(mode, func(t *testing.T) {
			p, want, f := continuityFixture(t)
			originalHead := want.Manifest["api"]
			git(t, f.repo.Source, "fetch", f.repo.GitDir, want.Manifest["api"])
			if mode == "squash" {
				git(t, f.repo.Source, "merge", "--squash", originalHead)
				git(t, f.repo.Source, "commit", "-m", "Human squash merge")
			} else {
				git(t, f.repo.Source, "merge", "--ff-only", originalHead)
			}
			merged := time.Now().UTC().Format(time.RFC3339)
			f.pr.State, f.pr.MergedAt = "closed", &merged
			want = nextRevision(t, p, want, f, "final2")
			if mode == "squash" {
				if want.Manifest["api"] == want.Targets["api"] {
					t.Fatal("fixture did not preserve distinct squash ancestry")
				}
				if err := p.Workspaces.IsAncestor(context.Background(), want.Goal, "api", originalHead, want.Targets["api"]); err == nil {
					t.Fatal("fixture target is not a squash")
				}
			}
			r, err := p.Publish(context.Background(), want)
			if err != nil || !r.Complete || r.Members["api"].State != "unchanged" {
				t.Fatalf("already delivered refresh: %+v %v", r, err)
			}
			if f.pushes != 1 || f.creates != 1 || f.commentPosts != 0 {
				t.Fatal("reopened human-delivered member")
			}
		})
	}
}

func verifyRefreshedStackRecovery(t *testing.T, p *Publisher, want Request, api, web *remote) {
	t.Helper()
	ctx := context.Background()
	m := p.Workspaces
	base := want.Manifest
	heads, targets := map[string]string{}, map[string]string{}
	var candidates []goalwork.Candidate
	for _, f := range []*remote{api, web} {
		r, err := m.Repository(ctx, want.Goal, f.repo.Member)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(r.Source, "upstream.txt"), []byte("upstream "+r.Member+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		git(t, r.Source, "add", "upstream.txt")
		git(t, r.Source, "commit", "-m", "upstream")
		target := git(t, r.Source, "rev-parse", "HEAD")
		s, err := m.PrepareTarget(ctx, want.Goal, "stack-refresh", r.Member, base[r.Member])
		if err != nil {
			t.Fatal(err)
		}
		git(t, s.GitDir, "fetch", "--no-tags", r.Source, target)
		c, err := m.StageRefresh(ctx, s, r.Start, target)
		if err != nil {
			t.Fatal(err)
		}
		candidates = append(candidates, c)
		heads[r.Member], targets[r.Member] = c.Commit, target
		f.repo.Start, f.head, f.operation = target, c.Commit, "stack-refreshed-final"
		f.pr.Base.SHA = target
	}
	digest, _ := want.Policy.Digest()
	v := goalwork.Verification{ID: "stack-refresh-proof", Goal: want.Goal, Operation: "stack-refresh", Manifest: heads, Targets: targets, Policy: digest, Checks: []goalwork.Check{{ID: "unit", State: "pass"}}, At: time.Now().UTC()}
	if _, err := m.AdvanceManifest(ctx, want.Goal, v.Operation, base, candidates, v, digest, []string{"unit"}, nil); err != nil {
		t.Fatal(err)
	}
	want.PreviousOperation, want.Operation = want.Operation, "stack-refreshed-final"
	want.Manifest, want.Targets = heads, targets
	v.Operation, v.ID = want.Operation, "stack-refreshed-proof"
	candidates = nil
	for _, member := range want.Policy.Members {
		c, err := m.FinalCandidateAt(ctx, want.Goal, want.Operation, member.ID, heads[member.ID], targets[member.ID])
		if err != nil {
			t.Fatal(err)
		}
		candidates = append(candidates, c)
	}
	if _, err := m.RecordFinal(ctx, candidates, v, digest, []string{"unit"}, nil); err != nil {
		t.Fatal(err)
	}
	a, u := &revisionRemote{remote: api}, &revisionRemote{remote: web, loseComment: true}
	p.Run = func(ctx context.Context, dir, bin string, args []string, stdin []byte) ([]byte, error) {
		if dir == web.repo.GitDir || slices.Contains(args, "repos/owner/web/pulls") || slices.Contains(args, "repos/owner/web/issues/17/comments") || strings.Contains(string(stdin), `"name":"web"`) {
			return u.run(ctx, dir, bin, args, stdin)
		}
		return a.run(ctx, dir, bin, args, stdin)
	}
	r, err := p.Publish(ctx, want)
	if err == nil || r.Complete || r.Members["api"].EvidenceComment == 0 || r.Members["web"].EvidenceComment != 0 {
		t.Fatalf("partial refreshed stack: %+v %v", r, err)
	}
	p = &Publisher{Workspaces: m, Run: p.Run, Guard: p.Guard}
	r, err = p.Publish(ctx, want)
	if err != nil || !r.Complete {
		t.Fatalf("refreshed stack recovery: %+v %v", r, err)
	}
	if api.pushes != 2 || web.pushes != 2 || api.creates != 1 || web.creates != 1 || a.commentPosts != 1 || u.commentPosts != 1 || r.Members["api"].URL == r.Members["web"].URL {
		t.Fatal("refreshed stack duplicated writes or mixed repository identity")
	}
}
