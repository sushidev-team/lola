package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGroupsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	c := &Config{
		Groups: []Group{
			{Name: "clients", Label: "Clients"},
			{Name: "internal", Collapsed: true},
		},
		Projects: []Project{
			{Name: "okane", Path: "/tmp/okane", Group: "clients"},
			{Name: "lola", Path: "/tmp/lola"},
		},
	}
	if err := c.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Groups) != 2 {
		t.Fatalf("groups = %+v", got.Groups)
	}
	if got.Groups[0].Name != "clients" || got.Groups[0].Label != "Clients" {
		t.Fatalf("group[0] = %+v", got.Groups[0])
	}
	// Order is the file's, not a sort: the array position IS the render order.
	if got.Groups[1].Name != "internal" || !got.Groups[1].Collapsed {
		t.Fatalf("group[1] = %+v", got.Groups[1])
	}
	if got.Projects[0].Group != "clients" {
		t.Fatalf("project group = %q", got.Projects[0].Group)
	}
	if got.Projects[1].Group != "" {
		t.Fatalf("ungrouped project carried group %q", got.Projects[1].Group)
	}
}

// A project at the top level must not write a `group` key at all, or an
// ungrouped project would be indistinguishable from one filed under "".
func TestUngroupedProjectWritesNoGroupKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	c := &Config{Projects: []Project{{Name: "lola", Path: "/tmp/lola"}}}
	if err := c.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "group") {
		t.Fatalf("config mentions group:\n%s", data)
	}
}

func TestDanglingGroupReferenceIsRepaired(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `[[group]]
name = "clients"

[[project]]
name = "okane"
path = "/tmp/okane"
group = "gone"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Projects[0].Group != "" {
		t.Fatalf("dangling group survived: %q", c.Projects[0].Group)
	}
	if !hasNotice(c.Notices(), "not defined") {
		t.Fatalf("notices = %v", c.Notices())
	}
}

func TestDuplicateAndNamelessGroupsAreDropped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `[[group]]
name = "clients"
label = "Clients"

[[group]]
name = "clients"
label = "Clients again"

[[group]]
name = "  "
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(c.Groups) != 1 || c.Groups[0].Label != "Clients" {
		t.Fatalf("groups = %+v", c.Groups)
	}
	if !hasNotice(c.Notices(), "duplicate") || !hasNotice(c.Notices(), "no name") {
		t.Fatalf("notices = %v", c.Notices())
	}
}

// sanitizeGroups runs from ResolveInheritance, which Load, Validate and Save all
// call. A repair must therefore be reported ONCE (by the load) and never grow a
// notice per save.
func TestGroupSanitizeIsIdempotent(t *testing.T) {
	c := &Config{
		Groups:   []Group{{Name: "clients"}, {Name: "clients"}},
		Projects: []Project{{Name: "okane", Group: "gone"}},
	}
	first := c.sanitizeGroups()
	if len(first) != 2 {
		t.Fatalf("first pass notices = %v", first)
	}
	if second := c.sanitizeGroups(); len(second) != 0 {
		t.Fatalf("second pass notices = %v", second)
	}
	if len(c.Groups) != 1 || c.Projects[0].Group != "" {
		t.Fatalf("not canonical: groups=%+v project=%+v", c.Groups, c.Projects[0])
	}
}

func TestGroupDisplayName(t *testing.T) {
	g := Group{Name: "clients"}
	if g.DisplayName() != "clients" {
		t.Fatalf("bare name = %q", g.DisplayName())
	}
	g.Label = "  Clients  "
	if g.DisplayName() != "Clients" {
		t.Fatalf("labelled = %q", g.DisplayName())
	}
	c := &Config{Groups: []Group{g}}
	if c.GroupDisplayNameFor("clients") != "Clients" {
		t.Fatalf("lookup = %q", c.GroupDisplayNameFor("clients"))
	}
	// An id nothing resolves renders as itself rather than blank.
	if c.GroupDisplayNameFor("gone") != "gone" {
		t.Fatalf("unresolved = %q", c.GroupDisplayNameFor("gone"))
	}
}

func hasNotice(notices []string, want string) bool {
	for _, n := range notices {
		if strings.Contains(n, want) {
			return true
		}
	}
	return false
}
