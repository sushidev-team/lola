package config

import (
	"fmt"
	"strings"
)

// Group is one [[group]] table: a named FOLDER that projects are filed under in
// the UIs. It carries no behaviour whatsoever — nothing in the daemon, the
// runtime or the dispatch path reads it — which is deliberate: a group is a
// human's way of arranging a long project list, and giving it any effect on
// what lola DOES would make the arrangement dangerous to change.
//
// It is a real table rather than a bare `group = "…"` string on the project for
// one reason: an EMPTY group must be able to exist. The UI's "add group" flow
// creates the folder first and lets projects be dragged into it afterwards, and
// a group derived from its members could not survive the moment between the two
// (nor could it be reordered, renamed, or collapsed while empty).
//
// Name is the group's IDENTITY — the value [[project]].group references — and
// is slug-shaped for the same reason Project.Name is, minus the paths: it is
// stable across a rename of the display Label. Like a project's, the slug shape
// is a UI rule and NOT validation (see Slug): a hand-written config may spell a
// group name however it likes as long as it is unique and non-empty.
type Group struct {
	Name  string `toml:"name"`
	Label string `toml:"label,omitempty"`
	// Position is the group's index among the TOP-LEVEL rows — the one list the
	// sidebar draws, in which a folder sits beside the ungrouped projects rather
	// than in a section below them. The list is rebuilt as: the ungrouped
	// projects in [[project]] order, then each group spliced in at its Position
	// (ascending, clamped). That is why a group needs an index of its own and a
	// project does not: a project's place is its position in the [[project]]
	// array, but an EMPTY group has no member to derive a place from.
	Position  int  `toml:"position,omitempty"`
	Collapsed bool `toml:"collapsed,omitempty"`
}

// DisplayName is the group's render string: Label when set, Name otherwise —
// exactly Project.DisplayName's rule, so the two read the same way in a list
// that mixes them.
func (g *Group) DisplayName() string {
	if s := strings.TrimSpace(g.Label); s != "" {
		return s
	}
	return g.Name
}

// GroupByName returns the group with this name, or nil. The pointer aims into
// c.Groups, so a caller may edit through it.
func (c *Config) GroupByName(name string) *Group {
	for i := range c.Groups {
		if c.Groups[i].Name == name {
			return &c.Groups[i]
		}
	}
	return nil
}

// GroupDisplayNameFor resolves a group id to its display string, falling back
// to the id itself for a group that does not resolve (which sanitizeGroups
// makes unreachable through Load, but a hand-built Config can still hold).
func (c *Config) GroupDisplayNameFor(name string) string {
	if g := c.GroupByName(name); g != nil {
		return g.DisplayName()
	}
	return name
}

// sanitizeGroups canonicalizes the group table and REPAIRS what cannot be
// rendered, returning a notice per repair. It is idempotent — a second call on
// its own output changes nothing and returns no notices — because Load,
// Validate and Save all reach it through ResolveInheritance and a notice must
// describe the FILE, not how many times the config was canonicalized.
//
// Everything here is a repair rather than a hard error on purpose: a group is
// pure arrangement, so the worst case of getting one wrong is a project drawn
// at the top level. Rejecting the whole config over it would take a working
// setup down for a cosmetic key.
func (c *Config) sanitizeGroups() []string {
	var notices []string

	seen := make(map[string]bool, len(c.Groups))
	out := c.Groups[:0]
	for _, g := range c.Groups {
		g.Name = strings.TrimSpace(g.Name)
		g.Label = strings.TrimSpace(g.Label)
		switch {
		case g.Name == "":
			notices = append(notices, "dropped a [[group]] with no name")
			continue
		case seen[g.Name]:
			notices = append(notices, fmt.Sprintf("dropped a duplicate [[group]] %q", g.Name))
			continue
		}
		if g.Position < 0 {
			g.Position = 0 // a negative index is simply "first"; nothing to report
		}
		seen[g.Name] = true
		out = append(out, g)
	}
	c.Groups = out
	if len(c.Groups) == 0 {
		c.Groups = nil
	}

	// A project may only reference a group that exists. A dangling reference
	// files the project at the top level instead — visible, editable, and
	// impossible to confuse with "lola lost my project".
	for i := range c.Projects {
		ref := strings.TrimSpace(c.Projects[i].Group)
		c.Projects[i].Group = ref
		if ref == "" || seen[ref] {
			continue
		}
		notices = append(notices, fmt.Sprintf(
			"project %q references group %q which is not defined; showing it ungrouped", c.Projects[i].Name, ref))
		c.Projects[i].Group = ""
	}
	return notices
}
