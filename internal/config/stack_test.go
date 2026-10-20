package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const stackBody = `
[defaults]
global_cap = 3
concurrency_cap = 2

[[project]]
name = "nori"
kind = "stack"
team_id = "team-1"
cycle_mode = "none"
assignee_mode = "anyone"
match_labels = ["l-app", "l-web"]
match_mode = "any"

[[project]]
name = "nori-app"
stack = "nori"
labels = ["apps/app"]
path = "/src/nori-app"
repo = "acme/nori-app"
default_branch = "main"
team_id = "kept-while-a-member"

[[project]]
name = "nori-website"
stack = "nori"
path = "/src/nori-website"
repo = "acme/nori-website"
default_branch = "develop"
`

func TestStackRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(stackBody), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	st := c.ProjectByName("nori")
	if !st.IsStack() || !st.Polls() {
		t.Fatalf("stack not resolved: %+v", st)
	}
	members := c.StackMembers("nori")
	if len(members) != 2 || members[0].Name != "nori-app" || members[1].Name != "nori-website" {
		t.Fatalf("members = %+v", members)
	}
	app := c.ProjectByName("nori-app")
	if app.Polls() {
		t.Error("a stack member must never poll, even with a team_id left over")
	}
	if c.StackFor(app) != st {
		t.Error("StackFor did not resolve the member's stack")
	}
	if got := c.PollingProjects(); len(got) != 1 || got[0].Name != "nori" {
		t.Errorf("polling projects = %+v", got)
	}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"nori", "nori-app", "nori-website"} {
		if !reflect.DeepEqual(back.ProjectByName(name), c.ProjectByName(name)) {
			t.Errorf("round trip changed %s:\n%+v\n%+v", name, back.ProjectByName(name), c.ProjectByName(name))
		}
	}
}

func TestStackValidation(t *testing.T) {
	base := func() *Config {
		return &Config{
			Defaults: Defaults{GlobalCap: 1},
			Projects: []Project{
				{Name: "nori", Kind: ProjectKindStack},
				{Name: "nori-app", Stack: "nori", Path: "/src/app", Labels: []string{"apps/app"}},
			},
		}
	}
	cases := []struct {
		name string
		mut  func(c *Config)
		want string
	}{
		{"unknown kind", func(c *Config) { c.Projects[0].Kind = "group" }, "kind must be"},
		{"stack with path", func(c *Config) { c.Projects[0].Path = "/src/x" }, "no checkout of its own"},
		{"stack with dev", func(c *Config) { c.Projects[0].DevCommands = []string{"npm run dev"} }, "dev_commands"},
		{"nested stack", func(c *Config) { c.Projects[0].Stack = "other" }, "cannot itself be a member"},
		{"missing stack", func(c *Config) { c.Projects[1].Stack = "nope" }, `stack "nope" is not a project`},
		{"member of a repo", func(c *Config) {
			c.Projects = append(c.Projects, Project{Name: "web", Path: "/src/web"})
			c.Projects[1].Stack = "web"
		}, `stack "web" is not a project`},
		{"member without path", func(c *Config) { c.Projects[1].Path = "" }, "path is required"},
		{"member bad name", func(c *Config) { c.Projects[1].Name = ".lola" }, "becomes a folder"},
		{"empty label", func(c *Config) { c.Projects[1].Labels = []string{" "} }, "empty entry"},
		{"labels outside a stack", func(c *Config) {
			c.Projects = append(c.Projects, Project{Name: "web", Path: "/src/web", Labels: []string{"x"}})
		}, "only apply to a stack member"},
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("base config invalid: %v", err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.mut(c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestMemberWantedFor(t *testing.T) {
	ids, names, groups := []string{"u-1", "u-2"}, []string{"Website", "bug"}, []string{"apps", ""}
	for _, c := range []struct {
		labels []string
		want   bool
	}{
		{nil, true},                      // no labels: always
		{[]string{"u-1"}, true},          // UUID
		{[]string{"website"}, true},      // name, case-insensitive
		{[]string{"Apps/Website"}, true}, // group/name
		{[]string{" bug "}, true},        // trimmed
		{[]string{"apps"}, false},        // a group alone is not a label
		{[]string{"other/website"}, false},
		{[]string{"api", "u-9"}, false},
	} {
		p := Project{Labels: c.labels}
		if got := p.WantedFor(ids, names, groups); got != c.want {
			t.Errorf("labels %q: WantedFor = %v, want %v", c.labels, got, c.want)
		}
	}
}
