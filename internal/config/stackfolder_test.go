package config

import (
	"strings"
	"testing"
)

func TestNewStacksInheritPRCommentAutomation(t *testing.T) {
	c := &Config{Defaults: Defaults{AutoPRComments: true}}
	if _, err := c.AddStack("Empty"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddStackFromFolder("Folder", []StackApp{{Path: "/src/web"}}, nil); err != nil {
		t.Fatal(err)
	}
	c.ResolveInheritance()
	for _, p := range c.Projects {
		if !p.Inherits.AutoPRComments || !p.AutoPRComments {
			t.Fatalf("%s did not inherit PR comment automation", p.Name)
		}
	}
	c.Defaults.AutoPRComments = false
	c.ResolveInheritance()
	for _, p := range c.Projects {
		if p.AutoPRComments {
			t.Fatalf("%s froze the inherited value", p.Name)
		}
	}
}

// A stack built from a parent folder adds one member per new checkout (path,
// repo and default branch filled, [defaults] inherited) and makes an already
// configured checkout JOIN instead of duplicating it.
func TestAddStackFromFolder(t *testing.T) {
	c := &Config{Projects: []Project{
		{Name: "api", Path: "/src/other/api"},      // same id, different path: the new one is prefixed
		{Name: "nori-web", Path: "/src/nori/web/"}, // configured at a picked path: joins
	}}
	name, err := c.AddStackFromFolder("Nori", []StackApp{
		{Path: "/src/nori/api", Repo: "acme/api", DefaultBranch: "main"},
		{Path: "/src/nori/web", Repo: "acme/ignored", DefaultBranch: "ignored"},
		{Path: "/src/nori/mobile-app", Label: "Mobile App"},
	}, nil)
	if err != nil {
		t.Fatalf("AddStackFromFolder: %v", err)
	}
	if name != "nori" {
		t.Fatalf("stack id = %q", name)
	}
	st := c.ProjectByName("nori")
	if st == nil || !st.IsStack() || st.Label != "Nori" || !st.Inherits.MatchLabels {
		t.Fatalf("stack = %+v", st)
	}
	api := c.ProjectByName("nori-api")
	if api == nil || api.Stack != "nori" || api.Path != "/src/nori/api" || api.Repo != "acme/api" || api.DefaultBranch != "main" || api.Label != "Api" || !api.Inherits.Symlinks {
		t.Fatalf("api member = %+v", api)
	}
	if old := c.ProjectByName("api"); old.Stack != "" {
		t.Fatalf("an unrelated project with the same id joined: %+v", old)
	}
	web := c.ProjectByName("nori-web")
	if web.Stack != "nori" || web.Repo != "" || web.DefaultBranch != "" {
		t.Fatalf("existing project must join without its values overwritten: %+v", web)
	}
	if m := c.ProjectByName("mobile-app"); m == nil || m.Stack != "nori" || m.Label != "Mobile App" {
		t.Fatalf("mobile member = %+v", m)
	}
	if got := len(c.StackMembers("nori")); got != 3 {
		t.Fatalf("members = %d, want 3", got)
	}
	if errs := c.validateStacks(); len(errs) != 0 {
		t.Fatalf("result does not validate: %v", errs)
	}
}

// Every refusal leaves the config exactly as it was: no stack, no members.
func TestAddStackFromFolderIsAllOrNothing(t *testing.T) {
	base := func() *Config {
		return &Config{Projects: []Project{
			{Name: "other", Kind: ProjectKindStack},
			{Name: "taken", Path: "/src/taken", Stack: "other"},
			{Name: "astack", Kind: ProjectKindStack, Path: ""},
		}}
	}
	for _, tc := range []struct {
		name  string
		label string
		apps  []StackApp
		want  string
	}{
		{"no apps", "Nori", nil, "at least one app"},
		{"empty label", " ", []StackApp{{Path: "/src/a"}}, "name is required"},
		{"label clash", "Other", []StackApp{{Path: "/src/a"}}, "already exists"},
		{"relative path", "Nori", []StackApp{{Path: "/src/a"}, {Path: "rel"}}, "absolute"},
		{"duplicate path", "Nori", []StackApp{{Path: "/src/a"}, {Path: "/src/a/"}}, "twice"},
		{"member of another stack", "Nori", []StackApp{{Path: "/src/a"}, {Path: "/src/taken"}}, "already a member"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			before := len(c.Projects)
			if _, err := c.AddStackFromFolder(tc.label, tc.apps, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if len(c.Projects) != before || c.ProjectByName("nori") != nil || c.ProjectByName("a") != nil {
				t.Fatalf("config changed on refusal: %+v", c.Projects)
			}
		})
	}
}

// canon decides sameness: a project saved under a symlink and the same
// checkout picked through its real path JOIN rather than being duplicated, and
// two spellings of one checkout in a single request are refused as a repeat.
func TestAddStackFromFolderComparesCanonicalPaths(t *testing.T) {
	canon := func(p string) string { return strings.Replace(p, "/link/", "/real/", 1) }
	c := &Config{Projects: []Project{{Name: "web", Path: "/link/web"}}}
	if _, err := c.AddStackFromFolder("Nori", []StackApp{{Path: "/real/web"}}, canon); err != nil {
		t.Fatal(err)
	}
	if len(c.Projects) != 2 || c.ProjectByName("web").Stack != "nori" {
		t.Fatalf("projects = %+v", c.Projects)
	}

	c = &Config{Projects: []Project{{Name: "other", Kind: ProjectKindStack}, {Name: "web", Path: "/link/web", Stack: "other"}}}
	if _, err := c.AddStackFromFolder("Nori", []StackApp{{Path: "/real/web"}}, canon); err == nil || !strings.Contains(err.Error(), "already a member") {
		t.Fatalf("membership of another stack bypassed through a symlink: %v", err)
	}
	c = &Config{}
	if _, err := c.AddStackFromFolder("Nori", []StackApp{{Path: "/real/web"}, {Path: "/link/web"}}, canon); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("one checkout under two paths accepted: %v", err)
	}
}

func TestSetMemberLabels(t *testing.T) {
	c := &Config{Projects: []Project{
		{Name: "donso", Kind: ProjectKindStack},
		{Name: "app", Stack: "donso", Path: "/x/app"},
		{Name: "web", Stack: "donso", Path: "/x/web", Labels: []string{"old"}},
		{Name: "solo", Path: "/x/solo"},
	}}
	if err := c.SetMemberLabels("donso", map[string][]string{"app": {" apps/app ", "", "apps/app"}, "web": nil}); err != nil {
		t.Fatal(err)
	}
	if got := c.ProjectByName("app").Labels; len(got) != 1 || got[0] != "apps/app" {
		t.Fatalf("app labels = %q, want [apps/app] (trimmed, deduped, blanks dropped)", got)
	}
	if got := c.ProjectByName("web").Labels; got != nil {
		t.Fatalf("web labels = %q, want nil (every issue)", got)
	}

	for _, tc := range []struct {
		stack  string
		labels map[string][]string
	}{
		{"nope", map[string][]string{"app": {"x"}}},
		{"app", map[string][]string{"app": {"x"}}},
		{"donso", map[string][]string{"app": {"x"}, "solo": {"y"}}},
	} {
		if err := c.SetMemberLabels(tc.stack, tc.labels); err == nil {
			t.Fatalf("SetMemberLabels(%q, %v) = nil, want an error", tc.stack, tc.labels)
		}
	}
	if got := c.ProjectByName("app").Labels; len(got) != 1 || got[0] != "apps/app" {
		t.Fatalf("a refused call changed app's labels to %q", got)
	}
}
