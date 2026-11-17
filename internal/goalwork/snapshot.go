package goalwork

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/sushidev-team/lola/internal/goalpolicy"
)

// SourceSnapshot is bounded, committed context for a proposal. It never reads
// working-tree files, follows symlinks, runs filters, or fetches from a remote.
type SourceSnapshot struct {
	Member        string       `json:"member"`
	Source        string       `json:"source"`
	Target        string       `json:"target"`
	Commit        string       `json:"commit"`
	Tree          []string     `json:"tree"`
	TreeTruncated bool         `json:"tree_truncated,omitempty"`
	Files         []SourceFile `json:"files"`
}

type SourceFile struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Content string `json:"content"`
}

// ContextPath excludes conventional secret files even when explicitly selected.
// This is a disclosure reduction, not a scanner for secrets inside source code.
func ContextPath(p string) bool {
	if p == "" || len(p) > 2000 || !utf8.ValidString(p) || path.IsAbs(p) || path.Clean(p) != p || p == "." || strings.ContainsAny(p, "\\\x00\r\n\t") || strings.HasPrefix(p, "../") {
		return false
	}
	for _, part := range strings.Split(strings.ToLower(p), "/") {
		if part == ".git" || part == ".lola" || part == ".ssh" || part == ".aws" || part == ".npmrc" || part == ".netrc" || strings.HasPrefix(part, ".env") || strings.Contains(part, "credential") || strings.Contains(part, "secret") || strings.HasPrefix(part, "id_rsa") || strings.HasPrefix(part, "id_ed25519") || strings.HasSuffix(part, ".pem") || strings.HasSuffix(part, ".key") || strings.HasSuffix(part, ".p12") || strings.HasSuffix(part, ".pfx") {
			return false
		}
	}
	return true
}

func (m *Manager) Snapshot(ctx context.Context, member, source, target string, requested []string) (SourceSnapshot, error) {
	return m.snapshot(ctx, member, source, target, requested, nil)
}

// SnapshotOwned adds a bounded selection of committed files in the task's
// ownership area to the default architectural context. Large/binary files are
// omitted just like oversized default manifests; the recorded files are exact.
func (m *Manager) SnapshotOwned(ctx context.Context, member, source, target string, scope goalpolicy.Scope) (SourceSnapshot, error) {
	if err := scope.Validate(); err != nil {
		return SourceSnapshot{}, err
	}
	return m.snapshot(ctx, member, source, target, nil, &scope)
}

func (m *Manager) snapshot(ctx context.Context, member, source, target string, requested []string, scope *goalpolicy.Scope) (SourceSnapshot, error) {
	s := SourceSnapshot{Member: member, Source: source, Target: target}
	if !segment.MatchString(member) || !filepath.IsAbs(source) || target == "" || len(requested) > 20 {
		return s, fmt.Errorf("snapshot requires a repository, target and at most 20 context files")
	}
	if _, err := m.git(ctx, source, "check-ref-format", "refs/heads/"+target); err != nil {
		return s, err
	}
	commit, err := m.resolve(ctx, source, "refs/heads/"+target)
	if err != nil {
		return s, err
	}
	s.Commit = commit
	raw, err := m.git(ctx, source, "ls-tree", "-r", "-z", commit)
	if err != nil {
		return s, err
	}
	blobs := map[string]string{}
	for _, entry := range strings.Split(raw, "\x00") {
		header, name, ok := strings.Cut(entry, "\t")
		parts := strings.Fields(header)
		if !ok || len(parts) != 3 || (parts[0] != "100644" && parts[0] != "100755") || parts[1] != "blob" || !objectID.MatchString(parts[2]) || !ContextPath(name) {
			continue
		}
		blobs[name] = parts[2]
	}
	for name := range blobs {
		s.Tree = append(s.Tree, name)
	}
	sort.Strings(s.Tree)
	if len(s.Tree) > 500 {
		s.Tree = s.Tree[:500]
		s.TreeTruncated = true
	}
	selected := append([]string(nil), requested...)
	if len(selected) == 0 {
		for _, name := range []string{"AGENTS.md", "README.md", "go.mod", "package.json", "Cargo.toml", "pyproject.toml", "composer.json", "Makefile"} {
			if blobs[name] != "" {
				selected = append(selected, name)
			}
		}
		if scope != nil {
			for _, name := range s.Tree {
				if len(selected) >= 12 {
					break
				}
				if !scope.Contains(name) {
					continue
				}
				duplicate := false
				for _, old := range selected {
					duplicate = duplicate || old == name
				}
				if !duplicate {
					selected = append(selected, name)
				}
			}
		}
	}
	seen := map[string]bool{}
	for _, name := range selected {
		if !ContextPath(name) || blobs[name] == "" || seen[name] {
			return s, fmt.Errorf("context file %q is unavailable, duplicated, or excluded", name)
		}
		seen[name] = true
		size, err := m.git(ctx, source, "cat-file", "-s", blobs[name])
		if err != nil {
			return s, err
		}
		n, err := strconv.Atoi(strings.TrimSpace(size))
		if err != nil || n < 0 || n > 16384 {
			if len(requested) == 0 {
				continue
			}
			return s, fmt.Errorf("context file %q exceeds 16 KiB", name)
		}
		body, err := m.git(ctx, source, "cat-file", "blob", blobs[name])
		if err != nil {
			return s, err
		}
		if len(body) != n || strings.ContainsRune(body, 0) || !utf8.ValidString(body) {
			if len(requested) == 0 {
				continue
			}
			return s, fmt.Errorf("context file %q is not bounded UTF-8 text", name)
		}
		s.Files = append(s.Files, SourceFile{Path: name, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(body))), Content: body})
	}
	return s, nil
}
