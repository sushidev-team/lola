package projectgit

import (
	"context"
	"testing"
)

func TestMergePreview(t *testing.T) {
	dir := actionRepo(t)
	target := tip(t, dir, "HEAD")
	testGit(t, dir, "switch", "-c", "feature")
	testGit(t, dir, "commit", "--allow-empty", "-m", "feature")
	source := tip(t, dir, "HEAD")
	testGit(t, dir, "switch", "main")
	c := Client{}
	r := BranchRequest{Action: "merge", Source: "refs/heads/feature", SourceSHA: source, Target: "refs/heads/main", TargetSHA: target}
	check := func(want bool) {
		t.Helper()
		got, err := c.CanFastForward(context.Background(), dir, r)
		if err != nil || got != want {
			t.Fatalf("CanFastForward = %v, %v; want %v", got, err, want)
		}
	}
	check(true)
	for _, setting := range []string{"false", "true", "only", "yes", "0"} {
		testGit(t, dir, "config", "merge.ff", setting)
		check(setting != "false" && setting != "0")
	}
	testGit(t, dir, "config", "merge.ff", "true")
	testGit(t, dir, "config", "branch.main.mergeOptions", "--no-ff")
	check(false)
	testGit(t, dir, "config", "--unset", "branch.main.mergeOptions")
	if head := tip(t, dir, "HEAD"); head != target {
		t.Fatal("preview changed HEAD")
	}
	r.SourceSHA = target
	if _, err := c.CanFastForward(context.Background(), dir, r); err == nil {
		t.Fatal("accepted stale source")
	}
	r.SourceSHA = source
	testGit(t, dir, "commit", "--allow-empty", "-m", "diverged")
	r.TargetSHA = tip(t, dir, "HEAD")
	check(false)
	r.Source, r.SourceSHA = r.Target, r.TargetSHA
	check(false)
}
