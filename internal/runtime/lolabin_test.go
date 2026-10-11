package runtime

import (
	"errors"
	"strings"
	"testing"
)

func TestResolveLolaBin(t *testing.T) {
	const wt = "/home/u/.lola/worktrees"
	const inWT = wt + "/lola/lola-lola-sushi-620/lola"
	const goBin = "/home/u/go/bin/lola"
	onPath := func(p string) func(string) (string, error) {
		return func(string) (string, error) {
			if p == "" {
				return "", errors.New("not found")
			}
			return p, nil
		}
	}
	files := func(present ...string) func(string) bool {
		return func(p string) bool {
			for _, f := range present {
				if f == p {
					return true
				}
			}
			return false
		}
	}

	cases := []struct {
		name, exe, path string
		present         []string
		want            string
	}{
		{"stable exe wins", goBin, "/usr/local/bin/lola", []string{goBin, "/usr/local/bin/lola"}, goBin},
		// The NOR/SUSHI-623 case: started from a torn-down worktree's build.
		{"deleted worktree exe falls back to PATH", inWT, goBin, []string{goBin}, goBin},
		{"live worktree exe still loses to PATH", inWT, goBin, []string{inWT, goBin}, goBin},
		{"deleted non-worktree exe falls back to PATH", "/tmp/old/lola", goBin, []string{goBin}, goBin},
		{"PATH copy inside a worktree is not stable either", inWT, inWT, []string{inWT}, inWT},
		{"live worktree exe beats nothing", inWT, "", []string{inWT}, inWT},
		{"nothing resolves: bare name", inWT, "", nil, "lola"},
		{"PATH hit that does not exist is skipped", inWT, goBin, nil, "lola"},
		{"worktrees root itself is not inside", wt, "", []string{wt}, wt},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ResolveLolaBin(c.exe, wt, onPath(c.path), files(c.present...)); got != c.want {
				t.Fatalf("ResolveLolaBin(%q) = %q, want %q", c.exe, got, c.want)
			}
		})
	}
	// A sibling directory sharing the prefix is not "inside".
	if got := ResolveLolaBin(wt+"-old/lola", wt, onPath(goBin), files(wt+"-old/lola", goBin)); got != wt+"-old/lola" {
		t.Fatalf("prefix sibling treated as worktree: %q", got)
	}
}

// Every artifact a spawn writes goes through the resolver, not the startup path.
func TestNativeWiresResolvedLolaBin(t *testing.T) {
	var logged []string
	n := &Native{
		LolaBin:        "/gone/lola",
		ResolveLolaBin: func() string { return "/home/u/go/bin/lola" },
		Logf:           func(f string, a ...any) { logged = append(logged, f) },
	}
	got := string(n.withReportBriefing([]byte("# task")))
	if !strings.Contains(got, "/home/u/go/bin/lola report todo set") || strings.Contains(got, "/gone/lola") {
		t.Fatalf("briefing must name the resolved binary:\n%s", got)
	}
	if len(logged) == 0 {
		t.Fatal("a fallback must be logged")
	}
	if got := string((&Native{LolaBin: "/x/lola", ResolveLolaBin: func() string { return "lola" }}).withReportBriefing(nil)); !strings.Contains(got, "`lola report`") {
		t.Fatalf("a bare-name fallback must not be quoted:\n%s", got)
	}
}
