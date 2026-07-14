package config

import (
	"bufio"
	"os"
	"strings"
)

// AOProjects extracts the project names from an agent-orchestrator.yaml
// file without pulling in a YAML dependency.
//
// Format assumption (deliberately minimal — this is the layout AO uses): the
// file contains a top-level, unindented "projects:" mapping whose immediate
// children (keys one consistent space-indent level deeper) are the project
// names, e.g.
//
//	projects:
//	  frontend:
//	    repo: git@github.com:acme/frontend.git
//	  backend:
//	    repo: git@github.com:acme/backend.git
//
// Blank lines and "#" comments are ignored. Anchors, flow mappings
// ("projects: {a: ...}"), multi-document files, and tab indentation are NOT
// supported; keys may be single- or double-quoted.
func AOProjects(aoConfigPath string) ([]string, error) {
	path, err := expandTilde(aoConfigPath)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var (
		projects    []string
		inProjects  bool
		childIndent = -1 // indent of the first key under projects:, once seen
	)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))

		if inProjects {
			if indent == 0 || (childIndent > 0 && indent < childIndent) {
				// Dedented out of the projects block; fall through so this
				// line can still open a new top-level projects: block.
				inProjects, childIndent = false, -1
			} else {
				if childIndent == -1 {
					childIndent = indent
				}
				if indent == childIndent {
					if key, ok := yamlKey(trimmed); ok {
						projects = append(projects, key)
					}
				}
				continue
			}
		}

		if indent == 0 && isProjectsHeader(trimmed) {
			inProjects = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return projects, nil
}

// isProjectsHeader reports whether a trimmed line is exactly "projects:"
// (optionally followed by a comment).
func isProjectsHeader(trimmed string) bool {
	rest, ok := strings.CutPrefix(trimmed, "projects:")
	if !ok {
		return false
	}
	rest = strings.TrimSpace(rest)
	return rest == "" || strings.HasPrefix(rest, "#")
}

// yamlKey extracts the key from a trimmed "key:" / "key: value" line,
// stripping one level of quoting. List items and keyless lines return false.
func yamlKey(trimmed string) (string, bool) {
	if strings.HasPrefix(trimmed, "-") {
		return "", false
	}
	i := strings.Index(trimmed, ":")
	if i <= 0 {
		return "", false
	}
	key := strings.TrimSpace(trimmed[:i])
	key = strings.Trim(key, `"'`)
	if key == "" {
		return "", false
	}
	return key, true
}
