package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// STACKS: one Linear issue stream for several repositories that ship
// together (an app, its website, its API). A stack is a [[project]] with
// kind = "stack" and NO checkout of its own; its MEMBERS ("apps") are ordinary
// [[project]] entries with stack = "<name>":
//
//	[[project]]                 # the stack: owns the Linear side
//	name = "nori"
//	kind = "stack"
//	team_id = "…"
//	match_labels = ["…", "…"]
//
//	[[project]]                 # a member: an ordinary repository
//	name = "nori-website"
//	stack = "nori"
//	labels = ["apps/website"]   # which issues need it ([] = every issue)
//	path = "/src/nori-website"
//	dev_commands = ["npm run dev"]
//
// The split follows what ONE session can have only once versus what each
// repository has for itself:
//
//   - The STACK owns everything about the issue and the agent: the Linear
//     filter and dedup, write-back, concurrency, the agent and its fallback
//     chain, the branch prefix, the budget, the sandbox, review overrides and
//     pr_requires_checks (a write-back gate). One session = one issue, one
//     agent, one container, one branch name.
//   - A MEMBER owns everything about its repository: path, repo, base branch,
//     symlinks, post_create, env, dev_commands, ports, pre_remove. Its own copies of the stack-owned keys are kept in
//     the file but IGNORED while it is a member (a project dragged out of a
//     stack gets them back), so a member never polls (Project.Polls).
//
// A session of a stack is a FOLDER, ~/.lola/worktrees/<stack>/<session>/, the
// agent's working directory, holding one worktree per selected member at
// <folder>/<member name>/ (internal/runtime/stack.go). Which members an issue
// gets is decided at spawn by Labels (WantedFor).

// ProjectKindStack is the [[project]].kind of a stack.
const ProjectKindStack = "stack"

// IsStack reports whether p is a stack (no checkout of its own).
func (p *Project) IsStack() bool { return p.Kind == ProjectKindStack }

// InStack reports whether p is a member of a stack.
func (p *Project) InStack() bool { return p.Stack != "" }

// StackMembers returns the members of the named stack in config order — the
// order a session's apps are listed and checked out in.
func (c *Config) StackMembers(stack string) []Project {
	if stack == "" {
		return nil
	}
	var out []Project
	for _, p := range c.Projects {
		if p.Stack == stack {
			out = append(out, p)
		}
	}
	return out
}

// StackFor returns the stack a member belongs to, or nil.
func (c *Config) StackFor(p *Project) *Project {
	if p == nil || p.Stack == "" {
		return nil
	}
	if s := c.ProjectByName(p.Stack); s != nil && s.IsStack() {
		return s
	}
	return nil
}

// WantedFor reports whether an issue whose labels are given (ids, names and
// group names, parallel slices; a label outside a group has group "") needs
// this member checked out. No Labels = always.
func (p Project) WantedFor(ids, names, groups []string) bool {
	if len(p.Labels) == 0 {
		return true
	}
	for _, want := range p.Labels {
		want = strings.TrimSpace(want)
		for i, id := range ids {
			if want == id {
				return true
			}
			name := at(names, i)
			if name == "" {
				continue
			}
			if strings.EqualFold(want, name) {
				return true
			}
			if g := at(groups, i); g != "" && strings.EqualFold(want, g+"/"+name) {
				return true
			}
		}
	}
	return false
}

// SelectApps returns the members an issue with the given labels needs (see
// WantedFor), in member order — the ONE selection rule, shared by dispatch
// (skip an issue that selects nothing) and the spawn (check out exactly these).
func SelectApps(members []Project, ids, names, groups []string) []Project {
	var out []Project
	for _, m := range members {
		if m.WantedFor(ids, names, groups) {
			out = append(out, m)
		}
	}
	return out
}

func at(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return ""
}

// appNameRe keeps a member's name one safe path segment: it becomes
// <session folder>/<name>/, beside the session's .lola/.
var appNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// validateStacks checks the stack keys: a stack has no checkout and no
// repository-only keys, a member names an existing stack, stacks do not nest,
// and member labels are non-empty. Static like the rest of Validate.
func (c *Config) validateStacks() []error {
	var errs []error
	for i := range c.Projects {
		p := &c.Projects[i]
		id := fmt.Sprintf("project %q", p.Name)
		switch p.Kind {
		case "", ProjectKindStack:
		default:
			errs = append(errs, fmt.Errorf("%s: kind must be %q or empty, got %q", id, ProjectKindStack, p.Kind))
		}
		if p.IsStack() {
			var set []string
			if p.Path != "" {
				set = append(set, "path")
			}
			if p.Repo != "" {
				set = append(set, "repo")
			}
			if len(p.DevCommands) > 0 {
				set = append(set, "dev_commands")
			}
			if p.PortRange != "" {
				set = append(set, "port_range")
			}
			if len(p.PreRemove) > 0 {
				set = append(set, "pre_remove")
			}
			if len(set) > 0 {
				errs = append(errs, fmt.Errorf("%s: a stack has no checkout of its own — move %s to its member projects", id, strings.Join(set, ", ")))
			}
			if p.Stack != "" {
				errs = append(errs, fmt.Errorf("%s: a stack cannot itself be a member of a stack", id))
			}
		}
		if p.Stack == "" {
			if len(p.Labels) > 0 {
				errs = append(errs, fmt.Errorf("%s: labels only apply to a stack member (set stack = \"<name>\")", id))
			}
			continue
		}
		if s := c.ProjectByName(p.Stack); s == nil || !s.IsStack() {
			errs = append(errs, fmt.Errorf("%s: stack %q is not a project with kind = %q", id, p.Stack, ProjectKindStack))
		}
		if !appNameRe.MatchString(p.Name) {
			errs = append(errs, fmt.Errorf("%s: a stack member's name becomes a folder in each session and must be [A-Za-z0-9._-], starting with a letter or digit", id))
		}
		if p.Path != "" && !filepath.IsAbs(p.Path) {
			errs = append(errs, fmt.Errorf("%s: a stack member's path must be absolute, got %q", id, p.Path))
		}
		for _, lb := range p.Labels {
			if strings.TrimSpace(lb) == "" {
				errs = append(errs, fmt.Errorf("%s: labels must not contain an empty entry", id))
				break
			}
		}
	}
	return errs
}
