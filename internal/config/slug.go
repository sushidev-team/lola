package config

import (
	"regexp"
	"strings"
)

// slugRe matches runs of characters that must not appear in a project id; each
// run collapses to a single "-".
var slugRe = regexp.MustCompile(`[^a-z0-9._-]+`)

// Slug reduces a free-text label to a project ID: a single, path-safe segment
// usable verbatim as a directory name (~/.lola/worktrees/<id>/), a filename
// (~/.lola/state/<id>.seen) and part of a tmux session name
// ("lola-<id>-<issue>").
//
// This is the ONE place that transform lives. internal/runtime has its own
// slugify for branch/PR labels, deliberately separate: that one is about
// session IDs derived from git refs, this one about the project identity a user
// types. Keep them independent — widening one must not silently widen the other.
//
// An input that reduces to nothing (or to a path-traversal segment) yields "",
// which callers must treat as "not a usable id" rather than substituting a
// placeholder: a project silently named "project" would be worse than an error.
func Slug(s string) string {
	out := slugRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
	out = strings.Trim(out, "-.")
	if out == "" || out == "." || out == ".." {
		return ""
	}
	return out
}

// SlugTyping is Slug's incremental half, for normalizing a project ID WHILE it
// is being typed: it lowercases and collapses invalid runs exactly as Slug does
// but does not trim the edges. Trimming mid-typing would eat the separator the
// moment you type it — "nori-" would snap back to "nori" and the next keystroke
// would land as "noria" — making a hyphenated id impossible to enter.
//
// A caller must still run Slug before persisting: SlugTyping can leave a
// trailing "-" or "." that is not a valid id on its own.
func SlugTyping(s string) string {
	return slugRe.ReplaceAllString(strings.ToLower(s), "-")
}

// IsSlug reports whether s is already exactly its own Slug — i.e. safe to use as
// a project ID without rewriting it. Note that pre-Label configs may hold names
// that fail this (e.g. "Okane"); those keep working untouched, so nothing here
// belongs in Validate.
func IsSlug(s string) bool { return s != "" && Slug(s) == s }

// DisplayName is the human-facing string for a project: its Label when set,
// otherwise its Name. Every UI renders this; only path/tmux/state code uses Name.
func (p Project) DisplayName() string {
	if l := strings.TrimSpace(p.Label); l != "" {
		return l
	}
	return p.Name
}

// DisplayNameFor resolves a project ID to its display string, falling back to
// the id itself when no such project is configured — so a session whose
// [[project]] was removed still renders something meaningful.
func (c *Config) DisplayNameFor(name string) string {
	if p := c.ProjectByName(name); p != nil {
		return p.DisplayName()
	}
	return name
}
