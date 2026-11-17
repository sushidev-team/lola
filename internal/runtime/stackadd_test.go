package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/session"
)

func appNames(apps []session.App) string {
	var out []string
	for _, a := range apps {
		out = append(out, a.Name)
	}
	return strings.Join(out, ",")
}

// A human's extra members are checked out beside the label-selected ones, in
// member order; a name outside the stack checks nothing out.
func TestStackSpawnAddsTheMembersAHumanTicked(t *testing.T) {
	f := newStackFixture(t)
	is := issueENG42() // no routing label: only the always-included docs
	s, err := f.n.SpawnWith(context.Background(), f.p, is, SpawnOpts{Apps: []string{"nori-api"}})
	if err != nil {
		t.Fatalf("SpawnWith: %v", err)
	}
	if got := appNames(s.Apps); got != "nori-api,nori-docs" {
		t.Fatalf("apps = %s, want nori-api,nori-docs", got)
	}

	is.Identifier = "ENG-43"
	if _, err := f.n.SpawnWith(context.Background(), f.p, is, SpawnOpts{Apps: []string{"elsewhere"}}); err == nil || !strings.Contains(err.Error(), "not a member") {
		t.Fatalf("a non-member must be refused, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "nori", "lola-nori-eng-43")); !os.IsNotExist(err) {
		t.Errorf("a refused spawn left a folder behind: %v", err)
	}
}

func TestAddAppsChecksAMemberOutIntoARunningSession(t *testing.T) {
	f := newStackFixture(t)
	is := issueENG42()
	l := webIssue()
	is.LabelIDs, is.LabelNames, is.LabelGroups = l.ids, l.names, l.groups
	s, err := f.n.Spawn(context.Background(), f.p, is, "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	res, err := f.n.AddApps(context.Background(), s, []string{"nori-api"})
	if err != nil {
		t.Fatalf("AddApps: %v", err)
	}
	if res.Relaunched || appNames(res.Apps) != "nori-api" {
		t.Fatalf("result = %+v", res)
	}
	a := res.Apps[0]
	if a.Worktree != filepath.Join(s.Worktree, "nori-api") || a.Branch != s.Branch || a.Base != "main" || a.Repo != "acme/api" {
		t.Errorf("app record = %+v", a)
	}
	if head := realGit(t, a.Worktree, "rev-parse", "--abbrev-ref", "HEAD"); head != s.Branch {
		t.Errorf("added app on %q, want the session's branch %q", head, s.Branch)
	}
	if _, err := os.Stat(filepath.Join(a.Worktree, ".lola", "env")); err != nil {
		t.Errorf("the added app has no env of its own: %v", err)
	}
	prompt, _ := os.ReadFile(filepath.Join(s.Worktree, ".lola", "prompt.md"))
	if !strings.Contains(string(prompt), "`nori-api/` — acme/api") || strings.Contains(string(prompt), "NOT checked out") {
		t.Errorf("prompt.md not brought up to date:\n%s", prompt)
	}
	if n := strings.Count(string(prompt), "## Repositories"); n != 1 {
		t.Errorf("prompt.md has %d Repositories sections, want 1", n)
	}

	// Refusals change nothing.
	s.Apps = append(s.Apps, res.Apps...)
	for name, want := range map[string]string{"nori-api": "already checked out", "elsewhere": "not a member"} {
		if _, err := f.n.AddApps(context.Background(), s, []string{name}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("AddApps(%s) = %v, want %q", name, err, want)
		}
	}
}

// A folder of the member's name that is already there may be the agent's:
// nothing is checked out over it.
func TestAddAppsRefusesAFolderThatAlreadyExists(t *testing.T) {
	f := newStackFixture(t)
	is := issueENG42()
	l := webIssue()
	is.LabelIDs, is.LabelNames, is.LabelGroups = l.ids, l.names, l.groups
	s, err := f.n.Spawn(context.Background(), f.p, is, "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if err := os.Mkdir(filepath.Join(s.Worktree, "nori-api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := f.n.AddApps(context.Background(), s, []string{"nori-api"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("AddApps = %v, want a refusal", err)
	}
	if out := realGit(t, f.api, "branch", "--list", s.Branch); out != "" {
		t.Errorf("a refused add created the branch: %q", out)
	}
}

func TestReplaceStackBriefingKeepsWhatFollows(t *testing.T) {
	f := newStackFixture(t)
	members := f.n.Cfg.StackMembers("nori")
	one := []session.App{{Name: "nori-docs", Branch: "b"}}
	two := append([]session.App{{Name: "nori-web", Branch: "b"}}, one...)
	body := "issue text\n## Repositories\nthe issue's own heading\n" + stackBriefing(members, one) + "\n## Context\nkept\n"
	got := string(replaceStackBriefing([]byte(body), members, two))
	for _, must := range []string{"the issue's own heading", "`nori-web/`", "\n## Context\nkept\n"} {
		if !strings.Contains(got, must) {
			t.Errorf("missing %q in:\n%s", must, got)
		}
	}
	if strings.Contains(got, "`nori-web`, `nori-api`") {
		t.Errorf("the old section survived:\n%s", got)
	}
}
