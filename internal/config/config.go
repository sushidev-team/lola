// Package config owns ~/.lola/config.toml: schema, defaults, atomic
// persistence, and static validation. All runtime paths derive from Home(),
// which honors the $LOLA_HOME override that tests rely on.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	// DefaultEndpoint is used when [linear].endpoint is unset.
	DefaultEndpoint = "https://api.linear.app/graphql"
	// DefaultPollInterval is used when [defaults].poll_interval is unset.
	DefaultPollInterval = 60 * time.Second
	// MinPollInterval is the Linear rate-limit floor; configured intervals
	// are clamped up to it, never rejected.
	MinPollInterval = 30 * time.Second
)

// DefaultBranchName is used when [[project]].default_branch is unset.
const DefaultBranchName = "main"

// Project is one [[project]] table: a local repository the native runtime
// can spawn worktree sessions for. Validation here is purely static —
// path-exists / is-a-git-repo checks live in the runtime layer.
type Project struct {
	Name          string            `toml:"name"`
	Path          string            `toml:"path"`
	Repo          string            `toml:"repo"`
	DefaultBranch string            `toml:"default_branch"`
	PostCreate    []string          `toml:"post_create"`
	Symlinks      []string          `toml:"symlinks"`
	Env           map[string]string `toml:"env"`
}

type Poll struct {
	Name           string   `toml:"name"`
	Enabled        bool     `toml:"enabled"`
	TeamID         string   `toml:"team_id"`
	ProjectID      string   `toml:"project_id"`
	CycleMode      string   `toml:"cycle_mode"` // none|active|pinned
	CycleID        string   `toml:"cycle_id"`
	StateIDs       []string `toml:"state_ids"`
	MatchLabels    []string `toml:"match_labels"`
	MatchMode      string   `toml:"match_mode"`    // any|all
	AssigneeMode   string   `toml:"assignee_mode"` // anyone|me|user
	AssigneeUserID string   `toml:"assignee_user_id"`
	Project        string   `toml:"project"` // [[project]].name; required
	Repo           string   `toml:"repo"`    // GitHub "owner/name" for PR checks; empty falls back to the project's repo (PollRepo)
	ConcurrencyCap int      `toml:"concurrency_cap"`
	PrioritySort   []string `toml:"priority_sort"`
	DedupMode      string   `toml:"dedup_mode"` // label|seen|state
	OnSentSetLabel string   `toml:"on_sent_set_label"`

	// --- Linear write-back (P4) --------------------------------------------
	// Optional lifecycle write-back: Lola advances the issue's workflow state
	// and/or posts a short comment as the agent progresses. Every field is
	// optional — "" (no transition / no label) and false (no comment) are the
	// defaults, so a poll that sets none of them behaves exactly as before.
	// The IDs are Linear UUIDs; they are validated only for non-emptiness where
	// a feature requires them (dedup_mode=state needs OnSpawnStateID) and never
	// resolved against Linear here — resolution is a runtime concern.
	OnSpawnStateID   string `toml:"on_spawn_state_id"`  // move issue to this state when a session is spawned ("" = no transition)
	OnPRStateID      string `toml:"on_pr_state_id"`     // state when the agent opens a PR ("" = no transition)
	OnMergedStateID  string `toml:"on_merged_state_id"` // state when the PR merges ("" = no transition)
	BlockedLabelID   string `toml:"blocked_label_id"`   // label added on escalation (agent-blocked); "" = none
	CommentOnSpawn   bool   `toml:"comment_on_spawn"`   // also post a short comment when the session spawns
	CommentOnPR      bool   `toml:"comment_on_pr"`      // ... when the agent opens a PR
	CommentOnMerged  bool   `toml:"comment_on_merged"`  // ... when the PR merges
	CommentOnBlocked bool   `toml:"comment_on_blocked"` // ... on escalation (agent blocked)
}

// Defaults is the [defaults] table. PollInterval is a plain time.Duration in
// memory; on disk it is a string like "60s" (see fileDefaults/Duration).
type Defaults struct {
	PollInterval   time.Duration `toml:"-"`
	ConcurrencyCap int           `toml:"concurrency_cap"`
	GlobalCap      int           `toml:"global_cap"`
}

// LinearConfig is the [linear] table. It intentionally has no api_key field:
// secrets never live in config.toml.
type LinearConfig struct {
	APIKeyKeychain string `toml:"api_key_keychain"`
	APIKeyEnv      string `toml:"api_key_env"`
	Endpoint       string `toml:"endpoint"`
}

type Config struct {
	Defaults  Defaults        `toml:"defaults"`
	Linear    LinearConfig    `toml:"linear"`
	Projects  []Project       `toml:"project"`
	Polls     []Poll          `toml:"poll"`
	Reactions ReactionsConfig `toml:"reactions"`
	Notify    NotifyConfig    `toml:"notify"`
	Brain     BrainConfig     `toml:"brain"`
	Review    ReviewConfig    `toml:"review"`
	Tmux      TmuxConfig      `toml:"tmux"`
}

// Duration is a time.Duration that TOML-round-trips as a Go duration string
// (e.g. "60s"); BurntSushi/toml has no native duration type.
type Duration time.Duration

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

func (d *Duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// fileConfig / fileDefaults mirror Config for (de)serialization only, so
// Config can expose PollInterval as a plain time.Duration.
type fileConfig struct {
	Defaults  fileDefaults         `toml:"defaults"`
	Linear    LinearConfig         `toml:"linear"`
	Projects  []Project            `toml:"project"`
	Polls     []Poll               `toml:"poll"`
	Reactions *fileReactionsConfig `toml:"reactions,omitempty"`
	Notify    *fileNotifyConfig    `toml:"notify,omitempty"`
	Brain     *fileBrainConfig     `toml:"brain,omitempty"`
	Review    *fileReviewConfig    `toml:"review,omitempty"`
	Tmux      *fileTmuxConfig      `toml:"tmux,omitempty"`
}

type fileDefaults struct {
	PollInterval   Duration `toml:"poll_interval"`
	ConcurrencyCap int      `toml:"concurrency_cap"`
	GlobalCap      int      `toml:"global_cap"`
}

func (fc *fileConfig) config() *Config {
	return &Config{
		Defaults: Defaults{
			PollInterval:   time.Duration(fc.Defaults.PollInterval),
			ConcurrencyCap: fc.Defaults.ConcurrencyCap,
			GlobalCap:      fc.Defaults.GlobalCap,
		},
		Linear:    fc.Linear,
		Projects:  fc.Projects,
		Polls:     fc.Polls,
		Reactions: resolveReactions(fc.Reactions),
		Notify:    resolveNotify(fc.Notify),
		Brain:     resolveBrain(fc.Brain),
		Review:    resolveReview(fc.Review),
		Tmux:      resolveTmux(fc.Tmux),
	}
}

func (c *Config) file() *fileConfig {
	return &fileConfig{
		Defaults: fileDefaults{
			PollInterval:   Duration(c.Defaults.PollInterval),
			ConcurrencyCap: c.Defaults.ConcurrencyCap,
			GlobalCap:      c.Defaults.GlobalCap,
		},
		Linear:    c.Linear,
		Projects:  c.Projects,
		Polls:     c.Polls,
		Reactions: reactionsFile(c.Reactions),
		Notify:    notifyFile(c.Notify),
		Brain:     brainFile(c.Brain),
		Review:    reviewFile(c.Review),
		Tmux:      tmuxFile(c.Tmux),
	}
}

// Home returns the lola runtime directory: $LOLA_HOME if set, else ~/.lola.
func Home() (string, error) {
	if h := os.Getenv("LOLA_HOME"); h != "" {
		return h, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lola"), nil
}

// DefaultPath returns Home()/config.toml.
func DefaultPath() (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "config.toml"), nil
}

// Load reads and parses the TOML config at path. A missing file is not an
// error: it yields a zero-value config with defaults applied. Leading ~ in
// [[project]].path is expanded; [linear].endpoint and
// [defaults].poll_interval get defaults (the interval clamped to
// MinPollInterval), and [[project]].default_branch defaults to
// DefaultBranchName. The optional [reactions] and [notify] tables are
// materialized to their defaults per unset field (see reactions.go); an absent
// table yields the full defaults, and existing configs load unchanged.
//
// Compatibility note: BurntSushi/toml silently ignores unknown keys, so
// configs from the AO-bridge era (an [ao] table, per-poll `runtime` /
// `ao_project` keys) still load — the AO-specific settings are simply
// dropped. Such polls need a `project` set before they validate. The
// retired per-poll `on_sent_remove_label` key is likewise dropped: Lola now
// removes all of a poll's `match_labels` on the post-spawn flip.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		// Route the empty config through config() so the [reactions]/[notify]
		// defaults (materialized there from all-nil file mirrors) apply exactly
		// as they would for a file that omits those tables.
		c := (&fileConfig{}).config()
		c.applyDefaults()
		return c, nil
	}
	if err != nil {
		return nil, err
	}

	var fc fileConfig
	if err := toml.Unmarshal(data, &fc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	c := fc.config()

	for i := range c.Projects {
		if c.Projects[i].Path, err = expandTilde(c.Projects[i].Path); err != nil {
			return nil, fmt.Errorf("expand project %q path: %w", c.Projects[i].Name, err)
		}
	}

	c.applyDefaults()
	return c, nil
}

func (c *Config) applyDefaults() {
	if c.Linear.Endpoint == "" {
		c.Linear.Endpoint = DefaultEndpoint
	}
	if c.Defaults.PollInterval == 0 {
		c.Defaults.PollInterval = DefaultPollInterval
	}
	if c.Defaults.PollInterval < MinPollInterval {
		c.Defaults.PollInterval = MinPollInterval
	}
	for i := range c.Projects {
		if c.Projects[i].DefaultBranch == "" {
			c.Projects[i].DefaultBranch = DefaultBranchName
		}
	}
}

// Save writes the config atomically: parents are created 0700, the TOML is
// written to a temp file in the destination directory (so the rename cannot
// cross filesystems), then renamed into place with final mode 0600.
func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return err
	}
	defer func() {
		if tmp != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if err := toml.NewEncoder(tmp).Encode(c.file()); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	name := tmp.Name()
	tmp = nil // written and closed; disarm the cleanup deferral
	return os.Rename(name, path)
}

// expandTilde expands a leading "~" or "~/" to the current user's home
// directory. "~user" forms are not supported and pass through unchanged.
func expandTilde(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if p == "~" {
		return home, nil
	}
	return filepath.Join(home, p[2:]), nil
}
