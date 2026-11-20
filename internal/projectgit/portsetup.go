// Port setup only samples development entry points. The same evidence bounds
// the lines that the reviewed plan may change in the human's checkout.
package projectgit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

type PortLine struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
}

type PortSource struct {
	Path  string     `json:"path"`
	Lines []PortLine `json:"lines,omitempty"`
	// Local env files expose only simple port assignments and loopback origins.
	// They can be shared by multiple worktrees and are never an edit target.
	ReadOnly bool `json:"readOnly,omitempty"`
}

var (
	portPath        = regexp.MustCompile(`^[a-zA-Z0-9_./@+ -]+$`)
	portCue         = regexp.MustCompile(`(?i)(\bports?\b|[_-]port\b|localhost|127\.0\.0\.1|\blisten\b|--port|\b(vite|next|nuxt|astro|webpack|artisan|uvicorn|gunicorn|flask|rails|react-scripts|concurrently)\b|"(dev|dev:[^"]+|start|serve|scripts|packagemanager)"\s*:|\b(dev|serve|start):|^\s*["'-]?[0-9]{2,5}:[0-9]{2,5})`)
	privateCue      = regexp.MustCompile(`(?i)(password|secret|token|api[_-]?key|authorization|credential|://[^/\s]+@)`)
	localAssignment = regexp.MustCompile(`^(?:export\s+)?[a-zA-Z_][a-zA-Z0-9_]*\s*=\s*["']?(?:[0-9]{1,5}|https?://(?:127\.0\.0\.1|localhost|\[::1\]):[0-9]{1,5}/?)["']?\s*$`)
)

// PortContextSafe screens user-authored commands and configuration excerpts
// before they become AI input. Local dotenv files use a separate allowlist.
func PortContextSafe(line string) bool {
	return len(line) <= 600 && !privateCue.MatchString(line)
}

func portFileKind(name string) string {
	switch name {
	case "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb", "artisan", "manage.py", "Gemfile", "go.mod", "Cargo.toml":
		return "marker"
	case "package.json", "composer.json", "Makefile", "Procfile", "Procfile.dev", "compose.yml", "compose.yaml", "docker-compose.yml", "docker-compose.yaml", "pyproject.toml", "angular.json", "server.js", "server.ts", "server.py", "main.go", "app.py", "main.py", "index.js", "index.ts", "app.js", "app.ts", "puma.rb", "launchSettings.json", "application.yml", "application.properties":
		return "source"
	case ".env", ".env.local", ".env.development", ".env.example", ".env.sample":
		return "env"
	}
	for _, prefix := range []string{"vite.config.", "next.config.", "nuxt.config.", "astro.config.", "webpack.config."} {
		if strings.HasPrefix(name, prefix) {
			return "source"
		}
	}
	return ""
}

func portPathOK(name string) bool {
	if !validPath(name) || !portPath.MatchString(name) {
		return false
	}
	for _, part := range strings.Split(path.Dir(name), "/") {
		if part != "." && (strings.HasPrefix(part, ".") || part == "node_modules" || part == "vendor" || part == "dist") {
			return false
		}
	}
	return true
}

func readPortFile(root *os.Root, name string) ([]byte, bool) {
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 128<<10 {
		return nil, false
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(f, 128<<10+1))
	return data, err == nil && len(data) <= 128<<10 && utf8.Valid(data) && !slices.Contains(data, byte(0))
}

func (c Client) PortSources(ctx context.Context, dir string) ([]PortSource, error) {
	raw, err := c.git(ctx, dir, "", "ls-files", "-z", "--cached")
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	paths := map[string]string{}
	dirs := map[string]bool{".": true}
	for name := range strings.SplitSeq(string(raw), "\x00") {
		kind := portFileKind(path.Base(name))
		if kind == "" || !portPathOK(name) {
			continue
		}
		paths[name] = kind
		dirs[path.Dir(name)] = true
	}
	for dir := range dirs {
		for _, env := range []string{".env", ".env.local", ".env.development"} {
			paths[path.Join(dir, env)] = "env"
		}
	}
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	slices.SortFunc(names, func(a, b string) int {
		if depth := strings.Count(a, "/") - strings.Count(b, "/"); depth != 0 {
			return depth
		}
		return strings.Compare(a, b)
	})
	var out []PortSource
	budget := 0
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(out) >= 120 || budget > 60<<10 {
			break
		}
		kind := paths[name]
		source := PortSource{Path: name, ReadOnly: kind != "source"}
		if kind != "marker" {
			data, ok := readPortFile(root, name)
			if !ok {
				continue
			}
			for i, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSuffix(line, "\r")
				if !PortContextSafe(line) || !portCue.MatchString(line) {
					continue
				}
				if kind == "env" && !localAssignment.MatchString(strings.TrimSpace(line)) {
					continue
				}
				source.Lines = append(source.Lines, PortLine{Number: i + 1, Text: line})
				budget += len(line) + 48
				if len(source.Lines) >= 40 || budget > 60<<10 {
					break
				}
			}
			if kind == "env" && len(source.Lines) == 0 {
				continue
			}
		}
		out = append(out, source)
	}
	return out, nil
}

type PortEdit struct {
	Path          string
	Line          int
	Before, After string
}

// ApplyPortEdits constructs the patch itself. Filenames and preimages must
// match sampled, tracked entry points. Git checks the entire batch before
// updating working files and leaves the user's index alone.
func (c Client) ApplyPortEdits(ctx context.Context, dir string, sources []PortSource, edits []PortEdit) error {
	if len(edits) == 0 {
		return nil
	}
	if err := c.requireIdle(ctx, dir); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	var patch strings.Builder
	for _, src := range sources {
		var group []PortEdit
		for _, e := range edits {
			if e.Path == src.Path {
				group = append(group, e)
			}
		}
		if len(group) == 0 {
			continue
		}
		if src.ReadOnly || !portPathOK(src.Path) || portFileKind(path.Base(src.Path)) != "source" {
			return errors.New("choose a tracked development file")
		}
		data, ok := readPortFile(root, src.Path)
		if !ok {
			return errors.New("development file changed; review ports again")
		}
		lines := strings.Split(string(data), "\n")
		slices.SortFunc(group, func(a, b PortEdit) int { return a.Line - b.Line })
		fmt.Fprintf(&patch, "diff --git a/%s b/%s\n--- a/%s\t\n+++ b/%s\t\n", src.Path, src.Path, src.Path, src.Path)
		for i, e := range group {
			if !slices.Contains(src.Lines, PortLine{e.Line, e.Before}) || e.Line < 1 || e.Line > len(lines) || strings.TrimSuffix(lines[e.Line-1], "\r") != e.Before || (i > 0 && e.Line == group[i-1].Line) {
				return errors.New("development line changed; review ports again")
			}
			if strings.ContainsAny(e.After, "\r\n\x00") {
				return errors.New("use one replacement line per change")
			}
			old := lines[e.Line-1]
			end := ""
			if strings.HasSuffix(old, "\r") {
				end = "\r"
			}
			marker := ""
			if e.Line == len(lines) {
				marker = "\\ No newline at end of file\n"
			}
			fmt.Fprintf(&patch, "@@ -%d +%d @@\n-%s\n%s+%s%s\n%s", e.Line, e.Line, old, marker, e.After, end, marker)
		}
	}
	for _, e := range edits {
		if !slices.ContainsFunc(sources, func(src PortSource) bool { return src.Path == e.Path }) {
			return errors.New("unknown development file")
		}
	}
	_, err = c.git(ctx, dir, patch.String(), "-c", "apply.ignoreWhitespace=false", "apply", "--unidiff-zero", "--whitespace=nowarn", "-")
	return err
}
