package gitrepo

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// fakeInspect answers the four git calls Inspect makes. A missing key means the
// call fails, which is how "this repo has no origin/HEAD" is expressed.
func fakeInspect(answers map[string]string) func(context.Context, string, string, ...string) (string, error) {
	return func(_ context.Context, _, _ string, args ...string) (string, error) {
		key := args[0]
		switch args[0] {
		case "remote":
			key = "remote/" + args[2] // remote get-url <name>
		case "symbolic-ref":
			key = "symbolic-ref/" + args[len(args)-1]
		}
		if v, ok := answers[key]; ok {
			return v, nil
		}
		return "", errors.New("git: no answer for " + key)
	}
}

// One pass yields everything a project form needs: root, remote, default branch
// and the fork-from list with the default first.
func TestInspectGathersEverything(t *testing.T) {
	i := Inspector{run: fakeInspect(map[string]string{
		"rev-parse":                             "/Users/me/code/web\n",
		"remote/origin":                         "git@github.com:acme/web.git\n",
		"for-each-ref":                          "feature/a\nmain\norigin/main\norigin/release-2\norigin/HEAD\n",
		"symbolic-ref/refs/remotes/origin/HEAD": "origin/main\n",
	})}

	got := i.Inspect(context.Background(), "/Users/me/code/web/src")
	if !got.IsRepo || got.Root != "/Users/me/code/web" {
		t.Fatalf("root = %q isRepo=%v, want the checkout top level", got.Root, got.IsRepo)
	}
	if got.Repo != "acme/web" {
		t.Errorf("repo = %q, want acme/web", got.Repo)
	}
	if got.DefaultBranch != "main" {
		t.Errorf("default = %q, want main", got.DefaultBranch)
	}
	if !slices.Equal(got.Branches, []string{"main", "feature/a", "release-2"}) {
		t.Errorf("branches = %v, want the default first", got.Branches)
	}
}

// upstream wins over origin: in a fork, upstream is where the PRs land, and that
// is the repository PR/CI observation has to watch.
func TestInspectPrefersUpstream(t *testing.T) {
	i := Inspector{run: fakeInspect(map[string]string{
		"rev-parse":       "/w\n",
		"remote/upstream": "https://github.com/acme/web.git\n",
		"remote/origin":   "https://github.com/me/web.git\n",
	})}
	if got := i.Inspect(context.Background(), "/w").Repo; got != "acme/web" {
		t.Errorf("repo = %q, want the upstream", got)
	}
}

// Without origin/HEAD the default branch narrows to a CONVENTIONAL name that
// actually exists — never one that doesn't.
func TestInspectFallsBackToAnExistingConventionalBranch(t *testing.T) {
	i := Inspector{run: fakeInspect(map[string]string{
		"rev-parse":    "/w\n",
		"for-each-ref": "develop\nfeature/x\n",
	})}
	if got := i.Inspect(context.Background(), "/w").DefaultBranch; got != "develop" {
		t.Errorf("default = %q, want develop", got)
	}
}

// With neither origin/HEAD nor a conventional name, the checked-out branch is
// the last resort — in a fresh single-branch repo it is exactly right.
func TestInspectFallsBackToCheckedOutBranch(t *testing.T) {
	i := Inspector{run: fakeInspect(map[string]string{
		"rev-parse":         "/w\n",
		"for-each-ref":      "prototype\n",
		"symbolic-ref/HEAD": "prototype\n",
	})}
	got := i.Inspect(context.Background(), "/w")
	if got.DefaultBranch != "prototype" {
		t.Errorf("default = %q, want the checked-out branch", got.DefaultBranch)
	}
}

// A directory that is not a checkout is the zero value, never an error: the
// caller's response to "unknown" is always to leave the fields to the user.
func TestInspectFailsClosed(t *testing.T) {
	i := Inspector{run: func(context.Context, string, string, ...string) (string, error) {
		return "", errors.New("fatal: not a git repository")
	}}
	got := i.Inspect(context.Background(), "/tmp/plain")
	if got.IsRepo || got.Root != "" || got.Repo != "" || got.DefaultBranch != "" || got.Branches != nil {
		t.Errorf("Inspect on a non-repo = %+v, want the zero value", got)
	}
	if got := i.Inspect(context.Background(), ""); got.IsRepo {
		t.Error("Inspect(\"\") must be the zero value")
	}
}

// A non-GitHub remote yields no repo — the same fail-closed rule Detect follows,
// since a wrong owner/name would point gh at someone else's repository.
func TestInspectLeavesNonGitHubRemoteEmpty(t *testing.T) {
	i := Inspector{run: fakeInspect(map[string]string{
		"rev-parse":     "/w\n",
		"remote/origin": "git@gitlab.com:acme/web.git\n",
	})}
	got := i.Inspect(context.Background(), "/w")
	if !got.IsRepo {
		t.Fatal("it is still a checkout")
	}
	if got.Repo != "" {
		t.Errorf("repo = %q, want empty for a non-GitHub host", got.Repo)
	}
}
