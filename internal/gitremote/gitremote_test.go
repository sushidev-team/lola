package gitremote

import (
	"context"
	"errors"
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
