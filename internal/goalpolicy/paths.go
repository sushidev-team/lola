package goalpolicy

import (
	"errors"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Scope makes unrestricted and read-only explicit. An empty Paths slice with
// All=false is read-only; legacy policy Paths=[] is resolved to All=true.
type Scope struct {
	All   bool     `json:"all,omitempty"`
	Paths []string `json:"paths,omitempty"`
}

func ValidatePaths(paths []string) error {
	if len(paths) > 100 {
		return errors.New("at most 100 owned path prefixes per repository")
	}
	seen := map[string]bool{}
	for _, p := range paths {
		if p == "" || len(p) > 2000 || !utf8.ValidString(p) || path.IsAbs(p) || path.Clean(p) != p || p == "." || p == ".." || strings.HasPrefix(p, "../") || strings.Contains(p, "\\") || strings.ContainsFunc(p, unicode.IsControl) || seen[p] {
			return errors.New("owned paths must be distinct normalized repository-relative prefixes")
		}
		for _, part := range strings.Split(p, "/") {
			if strings.EqualFold(part, ".lola") || strings.EqualFold(part, ".git") {
				return errors.New("owned paths must exclude Git and Lola control directories")
			}
		}
		seen[p] = true
	}
	return nil
}

func (s Scope) Validate() error {
	if s.All && len(s.Paths) > 0 {
		return errors.New("unrestricted scope cannot also declare path restrictions")
	}
	return ValidatePaths(s.Paths)
}
func (s Scope) Contains(p string) bool {
	if s.All {
		return true
	}
	for _, prefix := range s.Paths {
		if within(p, prefix) {
			return true
		}
	}
	return false
}
func (s Scope) Within(parent Scope) bool {
	if s.All {
		return parent.All
	}
	for _, p := range s.Paths {
		if !parent.Contains(p) {
			return false
		}
	}
	return true
}
func (s Scope) ReadOnly() bool { return !s.All && len(s.Paths) == 0 }

// Overlaps conservatively folds case because authoritative clones may live on
// a case-insensitive host filesystem even when the worker image uses Linux.
func (s Scope) Overlaps(other Scope) bool {
	if s.ReadOnly() || other.ReadOnly() {
		return false
	}
	if s.All || other.All {
		return true
	}
	for _, a := range s.Paths {
		for _, b := range other.Paths {
			a, b = strings.ToLower(a), strings.ToLower(b)
			if within(a, b) || within(b, a) {
				return true
			}
		}
	}
	return false
}
func within(p, prefix string) bool { return p == prefix || strings.HasPrefix(p, prefix+"/") }
