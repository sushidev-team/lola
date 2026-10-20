package tui

import "github.com/sushidev-team/lola/internal/config"

// stackOrder returns the indices of projects in the order the project lists
// draw them: config order, except that a stack's MEMBERS are pulled up to sit
// directly under their stack (in member config order), wherever they appear
// in the file. A member whose stack does not exist (Validate reports it) stays
// where it is, so no project ever drops out of a list.
func stackOrder(projects []config.Project) []int {
	stacks := map[string]bool{}
	for _, p := range projects {
		if p.IsStack() {
			stacks[p.Name] = true
		}
	}
	out := make([]int, 0, len(projects))
	for i, p := range projects {
		if p.InStack() && stacks[p.Stack] {
			continue // drawn under its stack
		}
		out = append(out, i)
		if !p.IsStack() {
			continue
		}
		for j, m := range projects {
			if m.Stack == p.Name {
				out = append(out, j)
			}
		}
	}
	return out
}

// stackMarker prefixes a project's name in a list: a glyph on a stack, an
// indent + branch on one of its members, nothing on a plain project.
func stackMarker(cfg *config.Config, p *config.Project) string {
	switch {
	case p.IsStack():
		return "▤ "
	case cfg != nil && cfg.StackFor(p) != nil:
		return "  └ "
	}
	return ""
}
