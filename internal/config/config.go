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

// DefaultBranchPrefix is used when [[project]].branch_prefix is unset: the
// prefix lola prepends to a session's branch (e.g. "lola/eng-42"). Resolved via
// BranchPrefixForProject.
const DefaultBranchPrefix = "lola/"

// Project is one [[project]] table: a local repository the native runtime
// can spawn worktree sessions for. Validation here is purely static —
// path-exists / is-a-git-repo checks live in the runtime layer.
type Project struct {
	Name          string `toml:"name"`
	Path          string `toml:"path"`
	Repo          string `toml:"repo"`
	DefaultBranch string `toml:"default_branch"`
	// BranchPrefix is prepended to a session's derived branch name (e.g. a
	// "feat/" prefix yields "feat/eng-42"). Empty resolves to DefaultBranchPrefix
	// "lola/" (BranchPrefixForProject).
	BranchPrefix string `toml:"branch_prefix"`
	// LinearTeamID / LinearProjID bind this project to a Linear team (and,
	// optionally, project) for the on-demand ticket picker, so a project that
	// runs no poll can still browse issues to start. Both are Linear UUIDs.
	LinearTeamID string `toml:"linear_team_id"`
	LinearProjID string `toml:"linear_project_id"`
	// Agent is the coding-agent kind this project's sessions spawn:
	// claude|codex|opencode. Empty inherits [defaults].agent (see
	// AgentForProject); the whole chain defaults to "claude". Project has no
	// file mirror — the tagged field alone round-trips.
	Agent      string            `toml:"agent"`
	PostCreate []string          `toml:"post_create"`
	Symlinks   []string          `toml:"symlinks"`
	Env        map[string]string `toml:"env"`
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
	Project        string   `toml:"project,omitempty"` // [[project]].name; implied by nesting under [[project.poll]] (only orphan/legacy top-level [[poll]] emit it)
	Repo           string   `toml:"repo"`              // GitHub "owner/name" for PR checks; empty falls back to the project's repo (PollRepo)
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

	// PRRequiresChecks gates the on_pr_* write-back (state move + comment) on the
	// PR being VALID rather than merely open: open, not a draft, and every CI /
	// CodeRabbit check green (ChecksState=="pass" — none failing or pending). With
	// it false (default) the PR write-back fires the moment a PR opens, preserving
	// the original P4 semantics. Set it true to hold "In Review" until the PR has
	// actually passed its checks.
	PRRequiresChecks bool `toml:"pr_requires_checks"`
}

// Defaults is the [defaults] table. PollInterval is a plain time.Duration in
// memory; on disk it is a string like "60s" (see fileDefaults/Duration).
type Defaults struct {
	PollInterval   time.Duration `toml:"-"`
	ConcurrencyCap int           `toml:"concurrency_cap"`
	GlobalCap      int           `toml:"global_cap"`
	// Agent is the global default coding-agent kind (claude|codex|opencode)
	// for sessions whose project sets no override. Empty resolves to "claude"
	// at read time (AgentForProject) — it is never force-written to disk.
	Agent string `toml:"agent"`
	// ManageDaemon toggles whether the TUI owns the daemon lifecycle: silent
	// auto-start when the socket is dead on open, plus restart/stop from the
	// keybar. A pointer so an unset value defaults to true (self-managed). Set
	// it false when an external supervisor (launchd KeepAlive) owns the daemon,
	// so the TUI never fights it — see AutoManageDaemon.
	ManageDaemon *bool `toml:"manage_daemon"`
}

// LinearConfig is the [linear] table. It intentionally has no api_key field:
// secrets never live in config.toml.
type LinearConfig struct {
	APIKeyKeychain string `toml:"api_key_keychain"`
	APIKeyEnv      string `toml:"api_key_env"`
	Endpoint       string `toml:"endpoint"`
}

type Config struct {
	Defaults   Defaults         `toml:"defaults"`
	Linear     LinearConfig     `toml:"linear"`
	Projects   []Project        `toml:"project"`
	Polls      []Poll           `toml:"poll"`
	Reactions  ReactionsConfig  `toml:"reactions"`
	Notify     NotifyConfig     `toml:"notify"`
	Brain      BrainConfig      `toml:"brain"`
	Review     ReviewConfig     `toml:"review"`
	CodeRabbit CodeRabbitConfig `toml:"coderabbit"`
	Tmux       TmuxConfig       `toml:"tmux"`

	// nestConflicts carries structural errors detected while flattening nested
	// [[project.poll]] tables (a nested poll naming a different project) from
	// config() to Validate, which cannot re-derive them from the flat model.
	// Unexported: never serialized, and nil in the common case.
	nestConflicts []error
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
	Defaults   fileDefaults          `toml:"defaults"`
	Linear     LinearConfig          `toml:"linear"`
	Projects   []fileProject         `toml:"project"`
	Polls      []Poll                `toml:"poll,omitempty"` // compat-only: legacy top-level polls + orphans whose project does not resolve
	Reactions  *fileReactionsConfig  `toml:"reactions,omitempty"`
	Notify     *fileNotifyConfig     `toml:"notify,omitempty"`
	Brain      *fileBrainConfig      `toml:"brain,omitempty"`
	Review     *fileReviewConfig     `toml:"review,omitempty"`
	CodeRabbit *fileCodeRabbitConfig `toml:"coderabbit,omitempty"`
	Tmux       *fileTmuxConfig       `toml:"tmux,omitempty"`
}

// fileProject mirrors Project on disk and carries the project's nested polls as
// [[project.poll]] tables. The pre-Kind schema wrote polls as top-level [[poll]]
// with a `project` back-reference; both shapes load (see config()), and a Save
// re-nests every resolvable poll (see file()), migrating the file in place.
type fileProject struct {
	Name          string            `toml:"name"`
	Path          string            `toml:"path"`
	Repo          string            `toml:"repo"`
	DefaultBranch string            `toml:"default_branch"`
	BranchPrefix  string            `toml:"branch_prefix,omitempty"`
	LinearTeamID  string            `toml:"linear_team_id,omitempty"`
	LinearProjID  string            `toml:"linear_project_id,omitempty"`
	Agent         string            `toml:"agent"`
	PostCreate    []string          `toml:"post_create"`
	Symlinks      []string          `toml:"symlinks"`
	Env           map[string]string `toml:"env"`
	Polls         []Poll            `toml:"poll,omitempty"` // -> [[project.poll]]
}

func projectFromFile(fp fileProject) Project {
	return Project{
		Name:          fp.Name,
		Path:          fp.Path,
		Repo:          fp.Repo,
		DefaultBranch: fp.DefaultBranch,
		BranchPrefix:  fp.BranchPrefix,
		LinearTeamID:  fp.LinearTeamID,
		LinearProjID:  fp.LinearProjID,
		Agent:         fp.Agent,
		PostCreate:    fp.PostCreate,
		Symlinks:      fp.Symlinks,
		Env:           fp.Env,
	}
}

func projectToFile(p Project) fileProject {
	return fileProject{
		Name:          p.Name,
		Path:          p.Path,
		Repo:          p.Repo,
		DefaultBranch: p.DefaultBranch,
		BranchPrefix:  p.BranchPrefix,
		LinearTeamID:  p.LinearTeamID,
		LinearProjID:  p.LinearProjID,
		Agent:         p.Agent,
		PostCreate:    p.PostCreate,
		Symlinks:      p.Symlinks,
		Env:           p.Env,
	}
}

type fileDefaults struct {
	PollInterval   Duration `toml:"poll_interval"`
	ConcurrencyCap int      `toml:"concurrency_cap"`
	GlobalCap      int      `toml:"global_cap"`
	Agent          string   `toml:"agent"`
	ManageDaemon   *bool    `toml:"manage_daemon"`
}

// config flattens the on-disk mirror into the in-memory Config: projects
// unwrap to the flat Projects slice, and every project's nested [[project.poll]]
// tables flatten into the single flat Polls slice with Project back-filled from
// the parent. Legacy/orphan top-level [[poll]] entries keep their explicit
// project and are appended after the nested ones. A nested poll that names a
// DIFFERENT project is a hand-edit conflict: it is dropped from the flat set and
// recorded in nestConflicts for Validate to surface (config cannot return an
// error, and Validate cannot re-derive the nesting).
func (fc *fileConfig) config() *Config {
	var projects []Project
	var polls []Poll
	var nestConflicts []error
	for _, fp := range fc.Projects {
		projects = append(projects, projectFromFile(fp))
		for _, p := range fp.Polls {
			if p.Project != "" && p.Project != fp.Name {
				nestConflicts = append(nestConflicts, fmt.Errorf(
					"poll %q nested under [[project]] %q sets project = %q; a nested poll must not name a different project",
					p.Name, fp.Name, p.Project))
				continue
			}
			p.Project = fp.Name
			polls = append(polls, p)
		}
	}
	polls = append(polls, fc.Polls...) // legacy/orphan top-level polls keep their explicit project

	return &Config{
		Defaults: Defaults{
			PollInterval:   time.Duration(fc.Defaults.PollInterval),
			ConcurrencyCap: fc.Defaults.ConcurrencyCap,
			GlobalCap:      fc.Defaults.GlobalCap,
			Agent:          fc.Defaults.Agent,
			ManageDaemon:   fc.Defaults.ManageDaemon,
		},
		Linear:        fc.Linear,
		Projects:      projects,
		Polls:         polls,
		nestConflicts: nestConflicts,
		Reactions:     resolveReactions(fc.Reactions),
		Notify:        resolveNotify(fc.Notify),
		Brain:         resolveBrain(fc.Brain),
		Review:        resolveReview(fc.Review),
		CodeRabbit:    resolveCodeRabbit(fc.CodeRabbit),
		Tmux:          resolveTmux(fc.Tmux),
	}
}

// file re-nests the flat model for serialization: each flat poll is bucketed
// under the [[project.poll]] of the project it references (its Project field is
// cleared, since nesting implies it). A poll whose project does not resolve to a
// known [[project]] is never dropped — it round-trips as a top-level [[poll]]
// orphan and keeps failing Validate until fixed. This is what migrates a
// pre-Kind (top-level [[poll]]) file to the nested schema on the first Save.
func (c *Config) file() *fileConfig {
	var fps []fileProject
	idxByName := make(map[string]int, len(c.Projects))
	for _, p := range c.Projects {
		idxByName[p.Name] = len(fps)
		fps = append(fps, projectToFile(p))
	}
	var orphans []Poll
	for _, pl := range c.Polls {
		if idx, ok := idxByName[pl.Project]; ok && pl.Project != "" {
			np := pl
			np.Project = "" // implied by nesting; omitempty drops it from [[project.poll]]
			fps[idx].Polls = append(fps[idx].Polls, np)
			continue
		}
		orphans = append(orphans, pl)
	}

	return &fileConfig{
		Defaults: fileDefaults{
			PollInterval:   Duration(c.Defaults.PollInterval),
			ConcurrencyCap: c.Defaults.ConcurrencyCap,
			GlobalCap:      c.Defaults.GlobalCap,
			Agent:          c.Defaults.Agent,
			ManageDaemon:   c.Defaults.ManageDaemon,
		},
		Linear:     c.Linear,
		Projects:   fps,
		Polls:      orphans,
		Reactions:  reactionsFile(c.Reactions),
		Notify:     notifyFile(c.Notify),
		Brain:      brainFile(c.Brain),
		Review:     reviewFile(c.Review),
		CodeRabbit: coderabbitFile(c.CodeRabbit),
		Tmux:       tmuxFile(c.Tmux),
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

// AutoManageDaemon reports whether the TUI should own the daemon lifecycle
// (auto-start on open, restart, stop). Unset defaults to true (self-managed);
// set [defaults].manage_daemon = false when launchd (KeepAlive) owns the
// daemon so the TUI never fights the supervisor.
func (c *Config) AutoManageDaemon() bool {
	return c.Defaults.ManageDaemon == nil || *c.Defaults.ManageDaemon
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
