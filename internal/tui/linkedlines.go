package tui

import (
	"fmt"
	"strings"

	"github.com/sushidev-team/lola/internal/config"
)

// The project form edits [[project.linked_repo]] as a list field, one repo
// per line:
//
//	name | path | owner/repo | label, label | base branch
//
// Only name and path are required; trailing fields may be left off. Labels
// (Linear label names, "group/name" or UUIDs) decide which issues get the
// repo checked out — none means every issue.

const linkedSep = " | "

// linkedLines renders a project's linked repos as editable lines.
func linkedLines(repos []config.LinkedRepo) []string {
	out := make([]string, 0, len(repos))
	for _, l := range repos {
		parts := []string{l.Name, l.Path, l.Repo, strings.Join(l.Labels, ", "), l.DefaultBranch}
		for len(parts) > 2 && parts[len(parts)-1] == "" {
			parts = parts[:len(parts)-1]
		}
		out = append(out, strings.Join(parts, linkedSep))
	}
	return out
}

// parseLinkedLines is linkedLines' inverse. Blank lines are skipped; a line
// without a name and a path is an error naming the line. Everything else
// (name shape, absolute path, repo format) is config.Validate's.
func parseLinkedLines(lines []string) ([]config.LinkedRepo, error) {
	var out []config.LinkedRepo
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "|")
		for j := range f {
			f[j] = strings.TrimSpace(f[j])
		}
		if len(f) > 5 {
			return nil, fmt.Errorf("linked repo line %d: too many fields — name | path | owner/repo | labels | base branch", i+1)
		}
		for len(f) < 5 {
			f = append(f, "")
		}
		if f[0] == "" || f[1] == "" {
			return nil, fmt.Errorf("linked repo line %d: needs at least \"name | path\"", i+1)
		}
		var labels []string
		for _, lb := range strings.Split(f[3], ",") {
			if lb = strings.TrimSpace(lb); lb != "" {
				labels = append(labels, lb)
			}
		}
		out = append(out, config.LinkedRepo{Name: f[0], Path: f[1], Repo: f[2], Labels: labels, DefaultBranch: f[4]})
	}
	return out, nil
}
