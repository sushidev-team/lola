package config

import "fmt"

// The [merge_queue] table turns on lola's LOCAL merge queue: approved + green
// PRs of lola's own sessions are merged ONE AT A TIME per repository, and each
// next one is first brought up to date with the default branch (by asking its
// agent to merge it in, through the same send-keys gate every other prompt
// uses) and re-awaits CI before it lands. See internal/daemon/mergequeue.go.
//
// It is the ONE place lola ever merges a PR, so it is OFF by default and an
// absent table means exactly the old behavior: approved + green notifies and
// parks for a human.

// Merge methods, as `gh pr merge` names them.
const (
	MergeMethodSquash = "squash"
	MergeMethodMerge  = "merge"
	MergeMethodRebase = "rebase"
)

// MergeQueueConfig is the [merge_queue] table.
//
//   - Enabled gates the whole feature (default false).
//   - Method is the merge strategy handed to `gh pr merge` (default "squash").
//   - AllowNoChecks lets a PR with NO CI checks at all land. Off by default:
//     right after a push GitHub can report no checks simply because they have
//     not registered yet, so "none" is indistinguishable from "not started" and
//     merging on it would land code nothing tested. Turn it on only for a repo
//     that genuinely runs no CI.
type MergeQueueConfig struct {
	Enabled       bool   `toml:"enabled"`
	Method        string `toml:"method"`
	AllowNoChecks bool   `toml:"allow_no_checks"`
}

type fileMergeQueueConfig struct {
	Enabled       *bool   `toml:"enabled,omitempty"`
	Method        *string `toml:"method,omitempty"`
	AllowNoChecks *bool   `toml:"allow_no_checks,omitempty"`
}

// resolveMergeQueue materializes the table. Absent → the zero value
// (disabled); an empty Method reads as squash through MergeQueueMethod, so the
// zero value round-trips through Save untouched.
func resolveMergeQueue(f *fileMergeQueueConfig) MergeQueueConfig {
	var m MergeQueueConfig
	if f == nil {
		return m
	}
	if f.Enabled != nil {
		m.Enabled = *f.Enabled
	}
	if f.Method != nil && *f.Method != "" {
		m.Method = *f.Method
	}
	if f.AllowNoChecks != nil {
		m.AllowNoChecks = *f.AllowNoChecks
	}
	return m
}

// mergeQueueFile builds the on-disk mirror; the default table is omitted so a
// config that never mentioned [merge_queue] does not grow one on save.
func mergeQueueFile(m MergeQueueConfig) *fileMergeQueueConfig {
	if m == (MergeQueueConfig{}) {
		return nil
	}
	return &fileMergeQueueConfig{Enabled: &m.Enabled, Method: &m.Method, AllowNoChecks: &m.AllowNoChecks}
}

// MergeQueueMethod is the effective merge method ("" reads as squash).
func (c *Config) MergeQueueMethod() string {
	if c.MergeQueue.Method == "" {
		return MergeMethodSquash
	}
	return c.MergeQueue.Method
}

func (c *Config) validateMergeQueue() []error {
	switch c.MergeQueue.Method {
	case "", MergeMethodSquash, MergeMethodMerge, MergeMethodRebase:
		return nil
	}
	return []error{fmt.Errorf("merge_queue.method must be squash, merge or rebase, got %q", c.MergeQueue.Method)}
}
