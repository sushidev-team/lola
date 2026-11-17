package goalpublish

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goalwork"
)

func advanceRemoteTarget(t *testing.T, f *remote) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.repo.Source, "upstream.txt"), []byte("upstream\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, f.repo.Source, "add", "upstream.txt")
	git(t, f.repo.Source, "commit", "-m", "upstream")
	target := git(t, f.repo.Source, "rev-parse", "HEAD")
	f.repo.Start = target // fake remote's current target; the owned receipt is immutable
	return target
}

func TestTargetCaptureAndFetchAreExactIsolatedAndRecoverable(t *testing.T) {
	p, want, f := fixture(t)
	target := advanceRemoteTarget(t, f)
	ctx := context.Background()
	targets, err := p.CaptureTargets(ctx, want.Goal, want.Policy)
	if err != nil || targets["api"] != target || f.pushes != 0 || f.creates != 0 {
		t.Fatalf("capture: %v %v", targets, err)
	}
	fetches := 0
	p.Run = func(ctx context.Context, dir, bin string, args []string, stdin []byte) ([]byte, error) {
		if bin != "git" {
			return f.run(ctx, dir, bin, args, stdin)
		}
		if !slices.Contains(args, "fetch") || args[len(args)-1] != target || args[len(args)-2] != "https://github.com/owner/api.git" || dir == f.repo.GitDir || dir == f.repo.Source {
			t.Fatalf("unbounded or misplaced fetch: %s %v", dir, args)
		}
		for _, required := range []string{"--no-tags", "--no-recurse-submodules", "--no-write-fetch-head", "fetch.fsckObjects=true", "protocol.allow=never", "protocol.https.allow=always", "http.followRedirects=false", "core.hooksPath=/dev/null"} {
			if !slices.Contains(args, required) {
				t.Fatal("missing fetch constraint", required)
			}
		}
		for _, arg := range args {
			if strings.HasPrefix(arg, "--force") || arg == "push" || arg == "--all" || strings.Contains(arg, "refs/heads/") {
				t.Fatal("fetch can alter a branch", args)
			}
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "target.json")); err != nil {
			t.Fatal("fetch preceded durable ownership", err)
		}
		fetches++
		git(t, dir, "fetch", "--no-tags", "--no-write-fetch-head", f.repo.Source, target)
		return nil, errors.New("response lost after receiving exact object")
	}
	store, err := p.FetchTarget(ctx, want.Goal, "refresh", want.Policy, "api", want.Manifest["api"], target)
	if err == nil || fetches != 1 {
		t.Fatalf("lost fetch response not surfaced: %v", err)
	}
	if _, err := p.FetchTarget(ctx, want.Goal, "refresh", want.Policy, "api", want.Manifest["api"], target); err != nil || fetches != 1 {
		t.Fatalf("fetch recovery reran completed transfer: %v", err)
	}
	if got := git(t, store.GitDir, "remote"); got != "" {
		t.Fatal("fetch persisted credential route", got)
	}
	if tip, _ := p.Workspaces.Tip(ctx, want.Goal, "api"); tip != want.Manifest["api"] {
		t.Fatal("target fetch changed canonical goal")
	}
	f.denied = true
	if _, err := p.FetchTarget(ctx, want.Goal, "paused", want.Policy, "api", want.Manifest["api"], target); err == nil || fetches != 1 {
		t.Fatal("paused fetch proceeded")
	}
}

func TestTargetRefreshRejectsUnknownPlacementBeforeNetwork(t *testing.T) {
	for _, kind := range []string{"path", "repo", "member", "sha", "default"} {
		t.Run(kind, func(t *testing.T) {
			p, want, f := fixture(t)
			member, sha := "api", f.repo.Start
			switch kind {
			case "path":
				want.Policy.Members[0].Source += "-other"
			case "repo":
				want.Policy.Members[0].Repo = "owner/../private"
			case "member":
				member = "other"
			case "sha":
				sha = "main"
			case "default":
				f.defaultBranch = "goal/g1"
				if _, err := p.CaptureTargets(context.Background(), want.Goal, want.Policy); err == nil {
					t.Fatal("captured a reserved goal branch as default")
				}
				return
			}
			p.Run = func(context.Context, string, string, []string, []byte) ([]byte, error) {
				t.Fatal("invalid fetch reached external tool")
				return nil, nil
			}
			if _, err := p.FetchTarget(context.Background(), want.Goal, "refresh", want.Policy, member, want.Manifest["api"], sha); err == nil {
				t.Fatal("invalid fetch accepted")
			}
		})
	}
}

func TestPublicationRequiresFinalProofForCapturedTarget(t *testing.T) {
	p, want, f := fixture(t)
	ctx := context.Background()
	prior := f.repo.Start
	target := advanceRemoteTarget(t, f)
	if _, err := p.Publish(ctx, want); !errors.Is(err, ErrTargetChanged) || f.pushes != 0 || f.creates != 0 {
		t.Fatalf("stale goal published: %v", err)
	}
	s, err := p.Workspaces.PrepareTarget(ctx, want.Goal, "refresh", "api", want.Manifest["api"])
	if err != nil {
		t.Fatal(err)
	}
	git(t, s.GitDir, "fetch", "--no-tags", "--no-write-fetch-head", f.repo.Source, target)
	c, err := p.Workspaces.StageRefresh(ctx, s, prior, target)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := want.Policy.Digest()
	v := goalwork.Verification{ID: "refresh-proof", Goal: want.Goal, Operation: "refresh", Manifest: map[string]string{"api": c.Commit}, Targets: map[string]string{"api": target}, Policy: digest, Checks: []goalwork.Check{{ID: "unit", State: "pass"}}, At: time.Now().UTC()}
	if _, err := p.Workspaces.AdvanceManifest(ctx, want.Goal, "refresh", want.Manifest, []goalwork.Candidate{c}, v, digest, []string{"unit"}, nil); err != nil {
		t.Fatal(err)
	}
	final, err := p.Workspaces.FinalCandidateAt(ctx, want.Goal, "final-refreshed", "api", c.Commit, target)
	if err != nil {
		t.Fatal(err)
	}
	v.Operation, v.ID = final.Operation, "new-final-proof"
	if _, err := p.Workspaces.RecordFinal(ctx, []goalwork.Candidate{final}, v, digest, []string{"unit"}, nil); err != nil {
		t.Fatal(err)
	}
	want.Operation, want.Manifest, want.Targets = final.Operation, v.Manifest, v.Targets
	f.operation, f.head = final.Operation, c.Commit
	if record, err := p.Publish(ctx, want); err != nil || !record.Complete || f.pushes != 1 || f.creates != 1 {
		t.Fatalf("verified refreshed publication: %+v %v", record, err)
	}
	wrong := want
	wrong.Targets = map[string]string{"api": prior}
	if _, err := p.Publish(ctx, wrong); err == nil {
		t.Fatal("publication accepted an earlier target")
	}
	// Independently read refs can be newer than a cached PR base response.
	f.repo.Start = strings.Repeat("b", 40)
	if _, err := p.Publish(ctx, want); !errors.Is(err, ErrTargetChanged) || f.pushes != 1 || f.creates != 1 {
		t.Fatalf("stale PR base masked target drift: %v", err)
	}
}

func TestPublicationRechecksTargetObservedDuringPRCreation(t *testing.T) {
	p, want, f := fixture(t)
	p.Run = func(ctx context.Context, dir, bin string, args []string, stdin []byte) ([]byte, error) {
		out, err := f.run(ctx, dir, bin, args, stdin)
		if bin == "gh" && slices.Contains(args, "POST") && err == nil {
			f.repo.Start = strings.Repeat("b", 40)
			f.pr.Base.SHA = f.repo.Start
		}
		return out, err
	}
	r, err := p.Publish(context.Background(), want)
	if !errors.Is(err, ErrTargetChanged) || r.Complete || f.pushes != 1 || f.creates != 1 {
		t.Fatalf("target moving during PR creation was acknowledged: %+v %v", r, err)
	}
	if _, err := p.Publish(context.Background(), want); !errors.Is(err, ErrTargetChanged) || f.pushes != 1 || f.creates != 1 {
		t.Fatalf("retry duplicated effects or lost drift: %v", err)
	}
}

func TestUnchangedRefreshedMemberStillChecksRemoteTarget(t *testing.T) {
	p, want, f := fixture(t)
	ctx := context.Background()
	// Model a human fast-forward of the target to the verified goal head.
	// Nothing remains to deliver, but that observation must stay current.
	f.repo.Start = want.Manifest["api"]
	want.Operation = "already-delivered"
	want.Targets = map[string]string{"api": f.repo.Start}
	c, err := p.Workspaces.FinalCandidateAt(ctx, want.Goal, want.Operation, "api", f.head, f.head)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := want.Policy.Digest()
	v := goalwork.Verification{ID: "already-delivered-proof", Goal: want.Goal, Operation: want.Operation, Manifest: want.Manifest, Targets: want.Targets, Policy: digest, Checks: []goalwork.Check{{ID: "unit", State: "pass"}}, At: time.Now().UTC()}
	if _, err := p.Workspaces.RecordFinal(ctx, []goalwork.Candidate{c}, v, digest, []string{"unit"}, nil); err != nil {
		t.Fatal(err)
	}
	if r, err := p.Publish(ctx, want); err != nil || !r.Complete || r.Members["api"].State != "unchanged" || f.pushes != 0 || f.creates != 0 {
		t.Fatalf("unchanged refreshed member: %+v %v", r, err)
	}
	f.repo.Start = strings.Repeat("b", 40)
	if _, err := p.Publish(ctx, want); !errors.Is(err, ErrTargetChanged) || f.pushes != 0 || f.creates != 0 {
		t.Fatalf("unchanged member hid remote target drift: %v", err)
	}
}
