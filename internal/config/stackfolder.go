package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// StackApp is one repository a stack is built from when it is created from a
// PARENT FOLDER: the checkouts nested in that folder, each already read with
// local git (internal/gitrepo) by the caller. config never execs, so the facts
// arrive here; this file only decides how they become [[project]] entries.
type StackApp struct {
	// Path is the checkout root. An existing [[project]] with the same path
	// JOINS the stack instead of being duplicated.
	Path string
	// Label is the display name of a NEW member ("" = derived from Path).
	Label string
	// Repo and DefaultBranch fill a NEW member's repository tab. They never
	// overwrite an existing project's values: those are decisions.
	Repo          string
	DefaultBranch string
}

// newProjectInherits is the bitmap a freshly created project starts with: every
// [defaults] key a new project would inherit, so nothing is frozen into the file.
func newProjectInherits() ProjectInherits {
	return ProjectInherits{
		Symlinks: true, PostCreate: true, Env: true,
		MatchLabels: true, MatchMode: true, OnSentSetLabel: true,
		BlockedLabelID: true, DedupMode: true, PrioritySort: true,
	}
}

// AddStack appends an empty STACK named from a free-text label and returns its
// id. A clash is refused rather than suffixed: the id names the stack's
// worktree folder and prefixes its session ids, so the human picks another.
func (c *Config) AddStack(label string) (string, error) {
	label = strings.TrimSpace(label)
	name := Slug(label)
	if name == "" {
		return "", errors.New("stack name is required")
	}
	if c.ProjectByName(name) != nil {
		return "", errors.New("a project named " + name + " already exists")
	}
	p := Project{Name: name, Kind: ProjectKindStack, CycleMode: "none", AssigneeMode: "anyone"}
	if label != name {
		p.Label = label
	}
	p.Inherits = newProjectInherits()
	c.Projects = append(c.Projects, p)
	return name, nil
}

// AddStackFromFolder creates a stack AND its members in one change: each app
// either joins (a project already configured at that path) or is added as a
// new member project with its path, repo and default branch filled. It is
// all-or-nothing — on any error c is left exactly as it was — because a
// half-built stack (some members added, the rest refused) is worse than asking
// again. Refused: no apps, an app with no absolute path, the same path twice,
// and a path already configured as a stack or as a member of another stack.
//
// canon maps a path to the form two paths are compared in (the caller
// resolves symlinks; config itself never touches the filesystem). nil compares
// cleaned paths only.
func (c *Config) AddStackFromFolder(label string, apps []StackApp, canon func(string) string) (string, error) {
	if len(apps) == 0 {
		return "", errors.New("choose at least one app for the stack")
	}
	saved := append([]Project(nil), c.Projects...)
	if canon == nil {
		canon = filepath.Clean
	}
	name, err := c.addStackFromFolder(label, apps, canon)
	if err != nil {
		c.Projects = saved
		return "", err
	}
	return name, nil
}

func (c *Config) addStackFromFolder(label string, apps []StackApp, canon func(string) string) (string, error) {
	name, err := c.AddStack(label)
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	for _, app := range apps {
		path := strings.TrimSpace(app.Path)
		if path == "" || !filepath.IsAbs(path) {
			return "", fmt.Errorf("app path must be absolute, got %q", app.Path)
		}
		path = filepath.Clean(path)
		key := canon(path)
		if seen[key] {
			return "", fmt.Errorf("%s is listed twice", path)
		}
		seen[key] = true

		if p := c.ProjectAtPath(key, canon); p != nil {
			switch {
			case p.IsStack():
				return "", fmt.Errorf("%s is a stack — stacks do not nest", p.DisplayName())
			case p.InStack() && p.Stack != name:
				return "", fmt.Errorf("%s is already a member of stack %s", p.DisplayName(), c.DisplayNameFor(p.Stack))
			}
			p.Stack = name
			continue
		}

		lbl := strings.TrimSpace(app.Label)
		if lbl == "" {
			lbl = LabelFromPath(path)
		}
		id := c.freeMemberName(name, Slug(lbl), path)
		if id == "" {
			return "", fmt.Errorf("no usable project id for %s", path)
		}
		m := Project{
			Name:          id,
			Stack:         name,
			Path:          path,
			Repo:          strings.TrimSpace(app.Repo),
			DefaultBranch: strings.TrimSpace(app.DefaultBranch),
			CycleMode:     "none",
			AssigneeMode:  "anyone",
			Inherits:      newProjectInherits(),
		}
		if lbl != id {
			m.Label = lbl
		}
		c.Projects = append(c.Projects, m)
	}
	return name, nil
}

// ProjectAtPath returns the configured project whose checkout is key, comparing
// canon(p.Path) with key (see AddStackFromFolder for canon), or nil.
func (c *Config) ProjectAtPath(key string, canon func(string) string) *Project {
	if canon == nil {
		canon = filepath.Clean
	}
	for i := range c.Projects {
		p := &c.Projects[i]
		if p.Path != "" && canon(p.Path) == key {
			return p
		}
	}
	return nil
}

// freeMemberName picks an unused id for a new member: the folder's own slug
// first (it is also the member's folder inside each session), then the slug
// prefixed with the stack's id, then numbered. "" only when the folder name
// slugs to nothing.
func (c *Config) freeMemberName(stack, slug, path string) string {
	if slug == "" {
		slug = Slug(filepath.Base(path))
	}
	if slug == "" || !appNameRe.MatchString(slug) {
		return ""
	}
	if c.ProjectByName(slug) == nil {
		return slug
	}
	base := stack + "-" + slug
	if c.ProjectByName(base) == nil {
		return base
	}
	for n := 2; ; n++ {
		if id := fmt.Sprintf("%s-%d", base, n); c.ProjectByName(id) == nil {
			return id
		}
	}
}
