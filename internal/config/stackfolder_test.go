package config

import (
	"strings"
	"testing"
)

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
	})
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
			if _, err := c.AddStackFromFolder(tc.label, tc.apps); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if len(c.Projects) != before || c.ProjectByName("nori") != nil || c.ProjectByName("a") != nil {
				t.Fatalf("config changed on refusal: %+v", c.Projects)
			}
		})
	}
}
