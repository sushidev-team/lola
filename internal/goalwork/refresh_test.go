package goalwork

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func refreshFixture(t *testing.T) (*Manager, Repository, Candidate) {
	t.Helper()
	m, r := fixture(t)
	w := worker(t, m, r, "task", "attempt", r.Start)
	c := stage(t, m, w, "integrate", r.Start, submit(t, m, w, "app.txt", "goal implementation\n"))
	advance(t, m, c)
	return m, r, c
}

func upstreamCommit(t *testing.T, m *Manager, r Repository, file, content string) string {
	t.Helper()
	put(t, filepath.Join(r.Source, file), content)
	gitOK(t, m, r.Source, "add", "--", file)
	gitOK(t, m, r.Source, "commit", "-m", "upstream change")
	return gitOK(t, m, r.Source, "rev-parse", "HEAD")
}

func fetchFixtureTarget(t *testing.T, m *Manager, r Repository, op, base, target string) TargetStore {
	t.Helper()
	s, err := m.PrepareTarget(context.Background(), r.Goal, op, r.Member, base)
	if err != nil {
		t.Fatal(err)
	}
	// Production performs an exact HTTPS fetch through goalpublish. Test only
	// uses this isolated local upstream to avoid network or user repositories.
	gitOK(t, m, s.GitDir, "fetch", "--no-tags", "--no-write-fetch-head", r.Source, target)
	return s
}

func TestRefreshPreservesHistoryAndRequiresNewFinalEvidence(t *testing.T) {
	ctx := context.Background()
	m, r, old := refreshFixture(t)
	target := upstreamCommit(t, m, r, "upstream.txt", "upstream behavior\n")
	put(t, filepath.Join(r.Source, "user-wip.txt"), "keep me\n")
	before := gitOK(t, m, r.Source, "show-ref")
	s := fetchFixtureTarget(t, m, r, "refresh", old.Commit, target)
	c, err := m.StageRefresh(ctx, s, r.Start, target)
	if err != nil {
		t.Fatal(err)
	}
	if c.Base != old.Commit || c.TargetSHA != target || c.PriorTarget != r.Start || !slices.Equal(c.Paths, []string{"upstream.txt"}) {
		t.Fatalf("wrong refresh receipt: %+v", c)
	}
	for _, ancestor := range []string{old.Commit, target} {
		gitOK(t, m, c.Directory, "merge-base", "--is-ancestor", ancestor, c.Commit)
	}
	if tip, _ := m.Tip(ctx, r.Goal, r.Member); tip != old.Commit {
		t.Fatal("staging changed the authoritative goal")
	}
	if again, err := m.StageRefresh(ctx, s, r.Start, target); err != nil || !reflect.DeepEqual(again, c) {
		t.Fatalf("refresh recovery: %+v %v", again, err)
	}
	v := proof(c)
	base := map[string]string{r.Member: old.Commit}
	if _, err := m.AdvanceManifest(ctx, r.Goal, c.Operation, base, []Candidate{c}, v, v.Policy, []string{"tests"}, []string{"security"}); err == nil {
		t.Fatal("accepted evidence with no captured target")
	}
	v.Targets = map[string]string{r.Member: target}
	if receipt, err := m.AdvanceManifest(ctx, r.Goal, c.Operation, base, []Candidate{c}, v, v.Policy, []string{"tests"}, []string{"security"}); err != nil || !receipt.Complete {
		t.Fatalf("advance refresh: %+v %v", receipt, err)
	}
	if again, err := m.StageRefresh(ctx, s, r.Start, target); err != nil || !reflect.DeepEqual(again, c) {
		t.Fatalf("post-integration candidate recovery: %+v %v", again, err)
	}
	final, err := m.FinalCandidateAt(ctx, r.Goal, "final-refreshed", r.Member, c.Commit, target)
	if err != nil {
		t.Fatal(err)
	}
	if final.Base != target || !slices.Equal(final.Paths, []string{"app.txt"}) {
		t.Fatalf("final review included upstream-only changes: %+v", final)
	}
	vf := proof(final)
	if _, err := m.RecordFinal(ctx, []Candidate{final}, vf, vf.Policy, []string{"tests"}, []string{"security"}); err == nil {
		t.Fatal("recorded refreshed final without target evidence")
	}
	vf.Targets = maps.Clone(v.Targets)
	if _, err := m.RecordFinal(ctx, []Candidate{final}, vf, vf.Policy, []string{"tests"}, []string{"security"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.VerifyFinalAt(ctx, r.Goal, final.Operation, vf.Policy, vf.Manifest, vf.Targets, []string{"tests"}, []string{"security"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.VerifyFinal(ctx, r.Goal, final.Operation, vf.Policy, vf.Manifest, []string{"tests"}, []string{"security"}); err == nil {
		t.Fatal("legacy caller bypassed captured targets")
	}
	if _, err := m.VerifyFinalAt(ctx, r.Goal, final.Operation, vf.Policy, vf.Manifest, map[string]string{r.Member: r.Start}, []string{"tests"}, []string{"security"}); err == nil {
		t.Fatal("final proof accepted a different target")
	}
	if stored, err := m.Repository(ctx, r.Goal, r.Member); err != nil || stored != r {
		t.Fatalf("original repository history changed: %+v %v", stored, err)
	}
	if got := gitOK(t, m, r.Source, "show-ref"); got != before {
		t.Fatal("refresh changed source refs")
	}
	if got := gitOK(t, m, r.Source, "status", "--porcelain"); got != "?? user-wip.txt" {
		t.Fatalf("refresh changed user checkout: %s", got)
	}
	if got := gitOK(t, m, r.GitDir, "rev-parse", "main"); got != r.Start {
		t.Fatal("refresh changed private main")
	}
}

func TestRefreshFastForwardWhenHumanTargetAlreadyContainsGoal(t *testing.T) {
	m, r, old := refreshFixture(t)
	gitOK(t, m, r.Source, "fetch", r.GitDir, old.Commit)
	gitOK(t, m, r.Source, "merge", "--ff-only", old.Commit)
	target := upstreamCommit(t, m, r, "upstream.txt", "later upstream\n")
	s := fetchFixtureTarget(t, m, r, "refresh", old.Commit, target)
	c, err := m.StageRefresh(context.Background(), s, r.Start, target)
	if err != nil || c.Commit != target {
		t.Fatalf("already delivered goal needs a true fast-forward: %+v %v", c, err)
	}
}

func TestRefreshConflictAndRewrittenHistoryHoldWithoutRefChanges(t *testing.T) {
	for _, rewrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "conflict", true: "rewritten"}[rewrite], func(t *testing.T) {
			m, r, old := refreshFixture(t)
			if rewrite {
				// A separate orphan ref in the isolated test upstream models a
				// rewritten target; no existing branch is reset or deleted.
				gitOK(t, m, r.Source, "checkout", "--orphan", "replacement")
			}
			target := upstreamCommit(t, m, r, "app.txt", "incompatible upstream\n")
			s := fetchFixtureTarget(t, m, r, "refresh", old.Commit, target)
			_, err := m.StageRefresh(context.Background(), s, r.Start, target)
			if err == nil || (!rewrite && !errors.Is(err, ErrConflict)) {
				t.Fatalf("unsafe target accepted: %v", err)
			}
			if tip, _ := m.Tip(context.Background(), r.Goal, r.Member); tip != old.Commit {
				t.Fatal("failed refresh changed goal ref")
			}
			if !rewrite {
				if _, err := os.Stat(filepath.Join(m.Root, r.Goal, "candidates", "refresh", r.Member, "tree", ".git", "MERGE_HEAD")); err != nil {
					t.Fatal("conflict evidence was not retained", err)
				}
			}
		})
	}
}

func TestRefreshRejectsChangedOwnershipAndIncompleteTargetProof(t *testing.T) {
	m, r, old := refreshFixture(t)
	target := upstreamCommit(t, m, r, "upstream.txt", "upstream\n")
	s := fetchFixtureTarget(t, m, r, "refresh", old.Commit, target)
	ctx := context.Background()
	bad := s
	bad.GitDir = r.GitDir
	if err := m.TargetPresent(ctx, bad, target); err == nil {
		t.Fatal("accepted caller-selected store")
	}
	if _, err := m.PrepareTarget(ctx, r.Goal, s.Operation, r.Member, r.Start); err == nil {
		t.Fatal("reused store identity for a different base")
	}
	c, err := m.StageRefresh(ctx, s, r.Start, target)
	if err != nil {
		t.Fatal(err)
	}
	v := proof(c)
	v.Manifest["ui"] = r.Start
	v.Targets = map[string]string{r.Member: target}
	if err := v.Validate(c, v.Policy, []string{"tests"}, nil); err == nil {
		t.Fatal("incomplete target combination passed")
	}
	v.Targets["other"] = r.Start
	if err := v.Validate(c, v.Policy, []string{"tests"}, nil); err == nil {
		t.Fatal("unrelated target combination passed")
	}
	put(t, filepath.Join(c.Directory, "upstream.txt"), "edited after staging\n")
	if _, err := m.StageRefresh(ctx, s, r.Start, target); err == nil {
		t.Fatal("resumed dirty refresh candidate")
	}
}

func TestRefreshManifestRecoversPartialStackWithoutChangingEvidence(t *testing.T) {
	ctx := context.Background()
	m, r, old := refreshFixture(t)
	ui, err := m.Initialize(ctx, r.Goal, "ui", r.Source, "main")
	if err != nil {
		t.Fatal(err)
	}
	w := worker(t, m, ui, "ui-task", "ui-attempt", ui.Start)
	u := stage(t, m, w, "ui-integrate", ui.Start, submit(t, m, w, "app.txt", "UI implementation\n"))
	advance(t, m, u)
	target := upstreamCommit(t, m, r, "upstream.txt", "shared upstream\n")
	aStore := fetchFixtureTarget(t, m, r, "refresh", old.Commit, target)
	uStore := fetchFixtureTarget(t, m, ui, "refresh", u.Commit, target)
	ac, err := m.StageRefresh(ctx, aStore, r.Start, target)
	if err != nil {
		t.Fatal(err)
	}
	uc, err := m.StageRefresh(ctx, uStore, ui.Start, target)
	if err != nil {
		t.Fatal(err)
	}
	v := proof(ac)
	v.Manifest["ui"] = uc.Commit
	v.Targets = map[string]string{"api": target, "ui": target}
	base := map[string]string{"api": old.Commit, "ui": u.Commit}
	plain := &Manager{Root: m.Root}
	m.Run = func(ctx context.Context, dir string, args []string) (string, error) {
		if dir == ui.GitDir && slices.Contains(args, "update-ref") {
			return "", errors.New("interrupted second refresh update")
		}
		return plain.git(ctx, dir, args...)
	}
	partial, err := m.AdvanceManifest(ctx, r.Goal, "refresh", base, []Candidate{ac, uc}, v, v.Policy, []string{"tests"}, []string{"security"})
	if err == nil || partial.Complete || len(partial.Integrated) != 1 {
		t.Fatalf("partial refresh released stack: %+v %v", partial, err)
	}
	if err := plain.CanRetryIntegration(ctx, r.Goal, "refresh", base); err == nil {
		t.Fatal("partial refresh journal could be abandoned")
	}
	changed := v
	changed.Targets = maps.Clone(v.Targets)
	changed.Targets["ui"] = ui.Start
	if _, err := plain.AdvanceManifest(ctx, r.Goal, "refresh", base, []Candidate{ac, uc}, changed, v.Policy, []string{"tests"}, []string{"security"}); err == nil {
		t.Fatal("recovery accepted changed target evidence")
	}
	for _, pair := range []struct {
		store     TargetStore
		candidate Candidate
	}{{aStore, ac}, {uStore, uc}} {
		c, err := plain.StageRefresh(ctx, pair.store, r.Start, target)
		if err != nil || !reflect.DeepEqual(c, pair.candidate) {
			t.Fatalf("candidate recovery after partial ref update: %+v %v", c, err)
		}
	}
	complete, err := plain.AdvanceManifest(ctx, r.Goal, "refresh", base, []Candidate{ac, uc}, v, v.Policy, []string{"tests"}, []string{"security"})
	if err != nil || !complete.Complete || !maps.Equal(complete.Manifest, v.Manifest) {
		t.Fatalf("refresh recovery: %+v %v", complete, err)
	}
}
