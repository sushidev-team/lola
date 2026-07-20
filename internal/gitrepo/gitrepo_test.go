package gitrepo

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestParseRemoteURL(t *testing.T) {
	cases := []struct{ in, want string }{
		// The forms git actually stores.
		{"git@github.com:sushidev-team/lola.git", "sushidev-team/lola"},
		{"git@github.com:sushidev-team/lola", "sushidev-team/lola"},
		{"ssh://git@github.com/sushidev-team/lola.git", "sushidev-team/lola"},
		{"ssh://git@github.com:22/sushidev-team/lola.git", "sushidev-team/lola"},
		{"https://github.com/sushidev-team/lola.git", "sushidev-team/lola"},
		{"https://github.com/sushidev-team/lola", "sushidev-team/lola"},
		{"https://user@github.com/sushidev-team/lola.git", "sushidev-team/lola"},
		{"https://user:token@github.com/sushidev-team/lola.git", "sushidev-team/lola"},
		{"git://github.com/sushidev-team/lola.git", "sushidev-team/lola"},
		{"https://github.com/sushidev-team/lola/", "sushidev-team/lola"},
		// Trailing whitespace: git remote get-url output ends in a newline.
		{"git@github.com:sushidev-team/lola.git\n", "sushidev-team/lola"},
		// A name that legitimately contains ".git".
		{"git@github.com:acme/dot.git.git", "acme/dot.git"},
		// GitHub Enterprise on a github.* host is still owner/name.
		{"git@github.acme-corp.com:acme/web.git", "acme/web"},
		{"https://GitHub.com/Acme/Web.git", "Acme/Web"},

		// FAIL CLOSED — an empty repo is safe, a wrong one is not.
		{"", ""},
		{"   ", ""},
		{"git@gitlab.com:acme/web.git", ""}, // not GitHub
		{"https://bitbucket.org/acme/web.git", ""},     // not GitHub
		{"https://git.acme-corp.com/acme/web.git", ""}, // unrecognisable GHE
		{"/srv/git/local-repo.git", ""},                // local path remote
		{"https://github.com/", ""},                    // no owner/name
		{"https://github.com/onlyowner", ""},           // owner without a name
		{"https://github.com/a/b/c", ""},               // deeper than owner/name
		{"github.com/acme/web", ""},                    // no scheme and no colon
	}
	for _, c := range cases {
		if got := ParseRemoteURL(c.in); got != c.want {
			t.Errorf("ParseRemoteURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// upstream wins over origin: in a fork, origin is your fork but upstream is the
// repository the PRs actually land in — the one PR/CI observation must watch.
func TestDetectPrefersUpstream(t *testing.T) {
	d := Detector{run: func(_ context.Context, _, _, remote string) (string, error) {
		switch remote {
		case "upstream":
			return "git@github.com:acme/web.git\n", nil
		case "origin":
			return "git@github.com:my-fork/web.git\n", nil
		}
		return "", errors.New("no such remote")
	}}
	if got := d.Detect(context.Background(), "/tmp/web"); got != "acme/web" {
		t.Errorf("Detect = %q, want acme/web (upstream)", got)
	}
}

// With no upstream it falls back to origin.
func TestDetectFallsBackToOrigin(t *testing.T) {
	d := Detector{run: func(_ context.Context, _, _, remote string) (string, error) {
		if remote == "origin" {
			return "https://github.com/acme/web.git\n", nil
		}
		return "", errors.New("no such remote")
	}}
	if got := d.Detect(context.Background(), "/tmp/web"); got != "acme/web" {
		t.Errorf("Detect = %q, want acme/web (origin)", got)
	}
}

// An upstream that is not a GitHub URL does not shadow a usable origin.
func TestDetectSkipsNonGitHubUpstream(t *testing.T) {
	d := Detector{run: func(_ context.Context, _, _, remote string) (string, error) {
		switch remote {
		case "upstream":
			return "git@gitlab.com:acme/web.git\n", nil
		case "origin":
			return "git@github.com:acme/web.git\n", nil
		}
		return "", errors.New("no such remote")
	}}
	if got := d.Detect(context.Background(), "/tmp/web"); got != "acme/web" {
		t.Errorf("Detect = %q, want the GitHub origin", got)
	}
}

// Every failure is "unknown", never a guess and never an error: not a git repo,
// no remotes, an empty dir.
func TestDetectFailsClosed(t *testing.T) {
	d := Detector{run: func(context.Context, string, string, string) (string, error) {
		return "", errors.New("fatal: not a git repository")
	}}
	if got := d.Detect(context.Background(), "/tmp/not-a-repo"); got != "" {
		t.Errorf("Detect = %q, want empty on failure", got)
	}
	if got := d.Detect(context.Background(), ""); got != "" {
		t.Errorf("Detect(\"\") = %q, want empty", got)
	}
}

// The real git path: a checkout with no remote yields "" rather than an error.
func TestDetectAgainstRealGit(t *testing.T) {
	dir := t.TempDir()
	if got := Detect(context.Background(), dir); got != "" {
		t.Errorf("Detect on a non-repo = %q, want empty", got)
	}
}

// fakeGit routes for-each-ref / symbolic-ref to canned output.
func fakeGit(refs, head string, headErr error) func(context.Context, string, string, ...string) (string, error) {
	return func(_ context.Context, _, _ string, args ...string) (string, error) {
		switch args[0] {
		case "for-each-ref":
			return refs, nil
		case "symbolic-ref":
			return head, headErr
		}
		return "", errors.New("unexpected git call")
	}
}

// Local and remote-tracking branches merge into one list: a base branch never
// checked out locally is still offerable, and the "<remote>/" prefix is gone.
func TestBranchesMergesLocalAndRemote(t *testing.T) {
	b := BranchLister{run: fakeGit(
		"main\nfeature/a\norigin/main\norigin/release-2\norigin/HEAD\n",
		"origin/main\n", nil,
	)}
	got := b.Branches(context.Background(), "/tmp/web")
	want := []string{"main", "feature/a", "release-2"}
	if !slices.Equal(got, want) {
		t.Errorf("Branches = %v, want %v", got, want)
	}
}

// origin/HEAD is a symbolic pointer, not something anyone forks from.
func TestBranchesDropsRemoteHead(t *testing.T) {
	b := BranchLister{run: fakeGit("origin/HEAD\norigin/main\n", "", errors.New("no head"))}
	if got := b.Branches(context.Background(), "/tmp/web"); !slices.Equal(got, []string{"main"}) {
		t.Errorf("Branches = %v, want [main]", got)
	}
}

// The repository's own default branch is floated to the top — it is the answer
// almost every time.
func TestBranchesPutsDefaultFirst(t *testing.T) {
	b := BranchLister{run: fakeGit("alpha\nmain\nzulu\n", "origin/main\n", nil)}
	got := b.Branches(context.Background(), "/tmp/web")
	if len(got) == 0 || got[0] != "main" {
		t.Errorf("Branches = %v, want main first", got)
	}
	if !slices.Equal(got, []string{"main", "alpha", "zulu"}) {
		t.Errorf("Branches = %v, want the rest alphabetical", got)
	}
}

// Without origin/HEAD the list is simply alphabetical rather than failing.
func TestBranchesWithoutOriginHead(t *testing.T) {
	b := BranchLister{run: fakeGit("zulu\nalpha\n", "", errors.New("not a symbolic ref"))}
	if got := b.Branches(context.Background(), "/tmp/web"); !slices.Equal(got, []string{"alpha", "zulu"}) {
		t.Errorf("Branches = %v, want [alpha zulu]", got)
	}
}

// A local branch whose name merely contains a slash keeps its full name — only
// actual remote refs are de-prefixed.
func TestBranchesKeepsSlashedLocalNames(t *testing.T) {
	b := BranchLister{run: fakeGit("feature/login\nrenovate/deps\n", "", errors.New("none"))}
	got := b.Branches(context.Background(), "/tmp/web")
	if !slices.Contains(got, "feature/login") || !slices.Contains(got, "renovate/deps") {
		t.Errorf("Branches = %v, want slashed local names intact", got)
	}
}

// Unknown is nil, never an error: git failing or a non-checkout just means the
// user types the branch instead.
func TestBranchesFailsClosed(t *testing.T) {
	b := BranchLister{run: func(context.Context, string, string, ...string) (string, error) {
		return "", errors.New("fatal: not a git repository")
	}}
	if got := b.Branches(context.Background(), "/tmp/nope"); got != nil {
		t.Errorf("Branches = %v, want nil", got)
	}
	if got := b.Branches(context.Background(), ""); got != nil {
		t.Errorf("Branches(\"\") = %v, want nil", got)
	}
}

// The real git path: an empty dir yields nothing rather than an error.
func TestBranchesAgainstRealGit(t *testing.T) {
	if got := Branches(context.Background(), t.TempDir()); got != nil {
		t.Errorf("Branches on a non-repo = %v, want nil", got)
	}
}
