// Package config owns ~/.lola/config.toml: schema, defaults, atomic
// persistence, and static validation. All runtime paths derive from Home(),
// which honors the $LOLA_HOME override that tests rely on.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
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

// DefaultBranchPrefix is used when neither [[project]].branch_prefix nor
// [defaults].branch_prefix is set: the prefix lola prepends to a session's
// branch (e.g. "lola/eng-42"). Resolved via BranchPrefixForProject.
const DefaultBranchPrefix = "lola/"

// Hard fallbacks for the inheritable polling enums: used when neither the
// project nor [defaults] sets one. They are the values the forms have always
// seeded, so resolution never yields a value Validate would reject.
const (
	DefaultMatchMode = "any"
	DefaultDedupMode = "seen"
	DefaultAgent     = "claude"
)

// DefaultPrioritySort is the issue ordering used when neither the project nor
// [defaults] sets one.
var DefaultPrioritySort = []string{"priority", "createdAt"}

// PrioritySortKeys are the ONLY keys daemon.SortIssues understands. This is not
// a Linear concept and there is nothing to fetch: it is lola's own tie-break
// chain for ranking the issues a tick matched, applied in the configured order
// (e.g. ["priority", "createdAt"] = highest priority first, oldest first within
// a priority). Unknown keys used to be silently ignored by the sorter, so
// Validate now rejects them — a typo'd key that quietly does nothing is worse
// than a startup error.
//
// Keep in lockstep with the switch in daemon.SortIssues.
var PrioritySortKeys = []string{"priority", "createdAt"}

// ProjectInherits records which [defaults]-inheritable keys a project does NOT
// set itself. It is derived from key ABSENCE in config.toml on load and decides
// what Save writes back, so an inherited key never gets frozen into the file.
//
// The polarity is deliberate: the ZERO VALUE means "this project sets every key
// explicitly", which is how a hand-built config.Project literal behaves. That
// keeps every in-memory construction site (tests, the TUI and desktop forms)
// working unchanged — nothing silently inherits just because it forgot to opt
// out. Only Load, which can actually observe key absence, turns bits on.
//
// The corresponding Project fields always hold the RESOLVED (effective) value —
// that is what every consumer outside this package and the config UIs reads.
// Consult this struct only to render the inherited-vs-overridden distinction or
// to promote/revert an override.
type ProjectInherits struct {
	PostCreate     bool
	Symlinks       bool
	Env            bool
	MatchLabels    bool
	MatchMode      bool
	OnSentSetLabel bool
	BlockedLabelID bool
	DedupMode      bool
	PrioritySort   bool
}

// Project is one [[project]] table: a local repository the native runtime can
// spawn worktree sessions for, with an OPTIONAL Linear polling configuration
// merged in. There is no separate "poll" concept — a project IS the unit, and
// it polls Linear iff TeamID is set (and Enabled). Validation here is purely
// static — path-exists / is-a-git-repo checks live in the runtime layer.
type Project struct {
	// --- Repository / worktree setup ---------------------------------------
	// Name is the project's IDENTITY, not its display string: it is a path
	// segment (~/.lola/worktrees/<name>/, ~/.lola/state/<name>.seen) and part of
	// every session ID (and therefore tmux session name), so it must stay
	// slug-shaped and is expensive to change — see Slug and DisplayName. Label
	// is the free-text name shown in the UIs; empty falls back to Name, which is
	// what every pre-Label config does.
	Name          string `toml:"name"`
	Label         string `toml:"label,omitempty"`
	Path          string `toml:"path"`
	Repo          string `toml:"repo"`
	DefaultBranch string `toml:"default_branch"`
	// BranchPrefix is prepended to a session's derived branch name (e.g. a
	// "feat/" prefix yields "feat/eng-42"). Empty resolves to DefaultBranchPrefix
	// "lola/" (BranchPrefixForProject).
	BranchPrefix string `toml:"branch_prefix,omitempty"`
	// Agent is the coding-agent kind this project's sessions spawn:
	// claude|codex|opencode. Empty inherits [defaults].agent (see
	// AgentForProject); the whole chain defaults to "claude".
	Agent      string            `toml:"agent,omitempty"`
	PostCreate []string          `toml:"post_create,omitempty"`
	Symlinks   []string          `toml:"symlinks,omitempty"`
	Env        map[string]string `toml:"env,omitempty"`

	// --- Linear polling (optional) -----------------------------------------
	// The project polls Linear only when TeamID is set; Enabled toggles it
	// on/off (pause). TeamID also binds the on-demand ticket picker, so a
	// non-polling project may set it to browse issues. All Linear IDs are UUIDs,
	// validated only for non-emptiness where a feature requires them and never
	// resolved against Linear here (resolution is a runtime concern).
	Enabled        bool     `toml:"enabled,omitempty"`
	TeamID         string   `toml:"team_id,omitempty"`
	ProjectID      string   `toml:"project_id,omitempty"` // optional Linear project filter
	CycleMode      string   `toml:"cycle_mode,omitempty"` // none|active|pinned
	CycleID        string   `toml:"cycle_id,omitempty"`
	StateIDs       []string `toml:"state_ids,omitempty"`
	MatchLabels    []string `toml:"match_labels,omitempty"`
	MatchMode      string   `toml:"match_mode,omitempty"`    // any|all
	AssigneeMode   string   `toml:"assignee_mode,omitempty"` // anyone|me|user
	AssigneeUserID string   `toml:"assignee_user_id,omitempty"`
	ConcurrencyCap int      `toml:"concurrency_cap,omitempty"`
	PrioritySort   []string `toml:"priority_sort,omitempty"`
	DedupMode      string   `toml:"dedup_mode,omitempty"` // label|seen|state
	OnSentSetLabel string   `toml:"on_sent_set_label,omitempty"`

	// --- Linear write-back (optional) --------------------------------------
	// Lola advances the issue's workflow state and/or posts a short comment as
	// the agent progresses. Every field is optional — "" / false leave it off.
	OnSpawnStateID   string `toml:"on_spawn_state_id,omitempty"`
	OnPRStateID      string `toml:"on_pr_state_id,omitempty"`
	OnMergedStateID  string `toml:"on_merged_state_id,omitempty"`
	BlockedLabelID   string `toml:"blocked_label_id,omitempty"`
	CommentOnSpawn   bool   `toml:"comment_on_spawn,omitempty"`
	CommentOnPR      bool   `toml:"comment_on_pr,omitempty"`
	CommentOnMerged  bool   `toml:"comment_on_merged,omitempty"`
	CommentOnBlocked bool   `toml:"comment_on_blocked,omitempty"`
	// PRRequiresChecks gates the on_pr_* write-back on the PR being VALID (open,
	// not draft, checks green) rather than merely open.
	PRRequiresChecks bool `toml:"pr_requires_checks,omitempty"`

	// Inherits marks which of the [defaults]-inheritable fields above this
	// project leaves to [defaults]; the fields themselves always hold the
	// resolved value. Never serialized — Load derives it from key absence and
	// Save re-derives the file shape from it. See ProjectInherits.
	Inherits ProjectInherits `toml:"-"`
}

// Polls reports whether this project is configured to poll Linear: it needs a
// team to filter by. Enabled is the separate on/off toggle checked at dispatch.
func (p *Project) Polls() bool { return p.TeamID != "" }

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

	// --- Project defaults --------------------------------------------------
	// Every key below is the fallback for the same-named [[project]] field: a
	// project that omits it inherits this value (see resolveInheritance). They
	// exist so shared setup — which Linear trigger label to match, which label
	// to flip on spawn, the worktree bootstrap — is written once instead of
	// repeated per project.
	//
	// The label/ID keys hold Linear UUIDs, which are TEAM-SCOPED: a global
	// default is only meaningful while every polling project targets the same
	// team. Validate enforces exactly that.
	BranchPrefix   string            `toml:"branch_prefix"`
	PostCreate     []string          `toml:"post_create"`
	Symlinks       []string          `toml:"symlinks"`
	Env            map[string]string `toml:"env"`
	MatchLabels    []string          `toml:"match_labels"`
	MatchMode      string            `toml:"match_mode"`
	OnSentSetLabel string            `toml:"on_sent_set_label"`
	BlockedLabelID string            `toml:"blocked_label_id"`
	DedupMode      string            `toml:"dedup_mode"`
	PrioritySort   []string          `toml:"priority_sort"`
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
	Reactions  ReactionsConfig  `toml:"reactions"`
	Notify     NotifyConfig     `toml:"notify"`
	Brain      BrainConfig      `toml:"brain"`
	Review     ReviewConfig     `toml:"review"`
	CodeRabbit CodeRabbitConfig `toml:"coderabbit"`
	Tmux       TmuxConfig       `toml:"tmux"`
	UI         UIConfig         `toml:"ui"`

	// notices are NON-FATAL repairs Load made to the file: things that were
	// already inert but would otherwise be rejected, so a config nobody could
	// have meant is fixed rather than turned into a hard block. Surfaced by
	// `lola doctor` and the settings editor; never serialized.
	notices []string

	// migrateErrs carries structural errors detected while migrating legacy
	// [[poll]] / [[project.poll]] tables onto their project (an unresolvable
	// project reference, or more than one poll for a project) from config() to
	// Validate. Unexported: never serialized, nil in the common case.
	migrateErrs []error
}

// Notices returns the non-fatal repairs Load made to the on-disk config, in
// file order. Empty for a clean config.
func (c *Config) Notices() []string { return slices.Clone(c.notices) }

// sanitizePrioritySort drops sort keys daemon.SortIssues does not understand,
// recording a notice for each. Those keys were ALREADY inert — the sorter's
// switch ignores anything it does not match — so dropping them cannot break a
// working setup, while rejecting them outright would hard-block a daemon on a
// value that never did anything. Validate still rejects an unknown key set in
// memory (a UI writing one now), which is where the check earns its keep.
//
// Note the effective order does change: a chain of only-unknown keys used to
// fall through to the issue identifier, and an empty chain sorts by the
// DefaultPrioritySort instead. The notice says so.
func (c *Config) sanitizePrioritySort() {
	clean := func(in []string, where string) []string {
		var out, dropped []string
		for _, k := range in {
			if slices.Contains(PrioritySortKeys, k) {
				out = append(out, k)
				continue
			}
			dropped = append(dropped, k)
		}
		if len(dropped) > 0 {
			eff := out
			if len(eff) == 0 {
				eff = DefaultPrioritySort
			}
			c.notices = append(c.notices, fmt.Sprintf(
				"%s.priority_sort: dropped unknown key(s) %v — only %v are understood (they are lola sort keys, not Linear priorities); now ordering by %v",
				where, dropped, PrioritySortKeys, eff))
		}
		return out
	}
	c.Defaults.PrioritySort = clean(c.Defaults.PrioritySort, "defaults")
	for i := range c.Projects {
		p := &c.Projects[i]
		p.PrioritySort = clean(p.PrioritySort, fmt.Sprintf("project %q", p.Name))
	}
}

// PollingProjects returns the projects configured to poll Linear (TeamID set),
// in config order. Enabled is not filtered here — dispatch checks it — so a
// paused project is still returned (the TUI shows it).
func (c *Config) PollingProjects() []Project {
	var out []Project
	for _, p := range c.Projects {
		if p.Polls() {
			out = append(out, p)
		}
	}
	return out
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
	Polls      []legacyPoll          `toml:"poll,omitempty"` // COMPAT-ONLY: pre-merge top-level polls, folded onto their project on load
	Reactions  *fileReactionsConfig  `toml:"reactions,omitempty"`
	Notify     *fileNotifyConfig     `toml:"notify,omitempty"`
	Brain      *fileBrainConfig      `toml:"brain,omitempty"`
	Review     *fileReviewConfig     `toml:"review,omitempty"`
	CodeRabbit *fileCodeRabbitConfig `toml:"coderabbit,omitempty"`
	Tmux       *fileTmuxConfig       `toml:"tmux,omitempty"`
	UI         *fileUIConfig         `toml:"ui,omitempty"`
}

// fileProject mirrors Project on disk. Its polling fields are inline; the
// LegacyPolls slice reads any pre-merge [[project.poll]] tables so they can be
// folded onto the project (migration) — a Save then drops them.
//
// Every [defaults]-inheritable field is a POINTER: nil means the key is absent
// from the file, i.e. "inherit", which is distinct from a present-but-empty
// value ("override to nothing"). Project carries the resolved value plus an
// Overrides bitmap; see projectFromFile / projectToFile for the translation.
type fileProject struct {
	Name          string             `toml:"name"`
	Label         string             `toml:"label,omitempty"`
	Path          string             `toml:"path"`
	Repo          string             `toml:"repo"`
	DefaultBranch string             `toml:"default_branch"`
	BranchPrefix  string             `toml:"branch_prefix,omitempty"`
	Agent         string             `toml:"agent,omitempty"`
	PostCreate    *[]string          `toml:"post_create,omitempty"`
	Symlinks      *[]string          `toml:"symlinks,omitempty"`
	Env           *map[string]string `toml:"env,omitempty"`

	Enabled        bool      `toml:"enabled,omitempty"`
	TeamID         string    `toml:"team_id,omitempty"`
	ProjectID      string    `toml:"project_id,omitempty"`
	CycleMode      string    `toml:"cycle_mode,omitempty"`
	CycleID        string    `toml:"cycle_id,omitempty"`
	StateIDs       []string  `toml:"state_ids,omitempty"`
	MatchLabels    *[]string `toml:"match_labels,omitempty"`
	MatchMode      *string   `toml:"match_mode,omitempty"`
	AssigneeMode   string    `toml:"assignee_mode,omitempty"`
	AssigneeUserID string    `toml:"assignee_user_id,omitempty"`
	ConcurrencyCap int       `toml:"concurrency_cap,omitempty"`
	PrioritySort   *[]string `toml:"priority_sort,omitempty"`
	DedupMode      *string   `toml:"dedup_mode,omitempty"`
	OnSentSetLabel *string   `toml:"on_sent_set_label,omitempty"`

	OnSpawnStateID   string  `toml:"on_spawn_state_id,omitempty"`
	OnPRStateID      string  `toml:"on_pr_state_id,omitempty"`
	OnMergedStateID  string  `toml:"on_merged_state_id,omitempty"`
	BlockedLabelID   *string `toml:"blocked_label_id,omitempty"`
	CommentOnSpawn   bool    `toml:"comment_on_spawn,omitempty"`
	CommentOnPR      bool    `toml:"comment_on_pr,omitempty"`
	CommentOnMerged  bool    `toml:"comment_on_merged,omitempty"`
	CommentOnBlocked bool    `toml:"comment_on_blocked,omitempty"`
	PRRequiresChecks bool    `toml:"pr_requires_checks,omitempty"`

	LegacyPolls []legacyPoll `toml:"poll,omitempty"` // pre-merge [[project.poll]]; folded onto the project on load, dropped on save
}

// legacyPoll is the pre-merge [[poll]] / [[project.poll]] shape, kept only to
// read older configs and fold their fields onto the owning project.
type legacyPoll struct {
	Name           string   `toml:"name"`
	Enabled        bool     `toml:"enabled"`
	TeamID         string   `toml:"team_id"`
	ProjectID      string   `toml:"project_id"`
	CycleMode      string   `toml:"cycle_mode"`
	CycleID        string   `toml:"cycle_id"`
	StateIDs       []string `toml:"state_ids"`
	MatchLabels    []string `toml:"match_labels"`
	MatchMode      string   `toml:"match_mode"`
	AssigneeMode   string   `toml:"assignee_mode"`
	AssigneeUserID string   `toml:"assignee_user_id"`
	Project        string   `toml:"project"`
	Repo           string   `toml:"repo"`
	ConcurrencyCap int      `toml:"concurrency_cap"`
	PrioritySort   []string `toml:"priority_sort"`
	DedupMode      string   `toml:"dedup_mode"`
	OnSentSetLabel string   `toml:"on_sent_set_label"`

	OnSpawnStateID   string `toml:"on_spawn_state_id"`
	OnPRStateID      string `toml:"on_pr_state_id"`
	OnMergedStateID  string `toml:"on_merged_state_id"`
	BlockedLabelID   string `toml:"blocked_label_id"`
	CommentOnSpawn   bool   `toml:"comment_on_spawn"`
	CommentOnPR      bool   `toml:"comment_on_pr"`
	CommentOnMerged  bool   `toml:"comment_on_merged"`
	CommentOnBlocked bool   `toml:"comment_on_blocked"`
	PRRequiresChecks bool   `toml:"pr_requires_checks"`
}

// foldOnto copies a legacy poll's filter/dedup/write-back fields onto p (its
// repo falls back to the project's own). Used only during migration.
//
// The legacy shape has no notion of inheritance, so every non-zero inheritable
// value it carries is recorded as an explicit project override — that preserves
// the config's behavior exactly across the migration, at the cost of a project
// that could have inherited instead. Zero values stay unset and inherit.
func (lp legacyPoll) foldOnto(p *Project) {
	p.Inherits.MatchLabels = len(lp.MatchLabels) == 0
	p.Inherits.MatchMode = lp.MatchMode == ""
	p.Inherits.PrioritySort = len(lp.PrioritySort) == 0
	p.Inherits.DedupMode = lp.DedupMode == ""
	p.Inherits.OnSentSetLabel = lp.OnSentSetLabel == ""
	p.Inherits.BlockedLabelID = lp.BlockedLabelID == ""

	p.Enabled = lp.Enabled
	p.TeamID = lp.TeamID
	p.ProjectID = lp.ProjectID
	p.CycleMode = lp.CycleMode
	p.CycleID = lp.CycleID
	p.StateIDs = lp.StateIDs
	p.MatchLabels = lp.MatchLabels
	p.MatchMode = lp.MatchMode
	p.AssigneeMode = lp.AssigneeMode
	p.AssigneeUserID = lp.AssigneeUserID
	if lp.Repo != "" && p.Repo == "" {
		p.Repo = lp.Repo
	}
	p.ConcurrencyCap = lp.ConcurrencyCap
	p.PrioritySort = lp.PrioritySort
	p.DedupMode = lp.DedupMode
	p.OnSentSetLabel = lp.OnSentSetLabel
	p.OnSpawnStateID = lp.OnSpawnStateID
	p.OnPRStateID = lp.OnPRStateID
	p.OnMergedStateID = lp.OnMergedStateID
	p.BlockedLabelID = lp.BlockedLabelID
	p.CommentOnSpawn = lp.CommentOnSpawn
	p.CommentOnPR = lp.CommentOnPR
	p.CommentOnMerged = lp.CommentOnMerged
	p.CommentOnBlocked = lp.CommentOnBlocked
	p.PRRequiresChecks = lp.PRRequiresChecks
}

// deref returns *p and true when the key was present, or the zero value and
// false when it was absent (inherit).
func deref[T any](p *T) (T, bool) {
	var zero T
	if p == nil {
		return zero, false
	}
	return *p, true
}

// ptr returns &v when set is true (the project overrides the key and the value
// must be written), or nil when it inherits and the key must be omitted.
func ptr[T any](v T, set bool) *T {
	if !set {
		return nil
	}
	return &v
}

// projectFromFile lifts the on-disk shape into a Project. Inheritable keys land
// as (value, present) pairs: the value goes into the field, the INVERSE of the
// presence bit into Inherits. The fields still need ResolveInheritance to fill
// the inherited ones from [defaults] — Load does that via applyDefaults.
func projectFromFile(fp fileProject) Project {
	postCreate, hasPostCreate := deref(fp.PostCreate)
	symlinks, hasSymlinks := deref(fp.Symlinks)
	env, hasEnv := deref(fp.Env)
	matchLabels, hasMatchLabels := deref(fp.MatchLabels)
	matchMode, hasMatchMode := deref(fp.MatchMode)
	prioritySort, hasPrioritySort := deref(fp.PrioritySort)
	dedupMode, hasDedupMode := deref(fp.DedupMode)
	onSentSetLabel, hasOnSentSetLabel := deref(fp.OnSentSetLabel)
	blockedLabelID, hasBlockedLabelID := deref(fp.BlockedLabelID)

	return Project{
		Name:           fp.Name,
		Label:          fp.Label,
		Path:           fp.Path,
		Repo:           fp.Repo,
		DefaultBranch:  fp.DefaultBranch,
		BranchPrefix:   fp.BranchPrefix,
		Agent:          fp.Agent,
		PostCreate:     postCreate,
		Symlinks:       symlinks,
		Env:            env,
		Enabled:        fp.Enabled,
		TeamID:         fp.TeamID,
		ProjectID:      fp.ProjectID,
		CycleMode:      fp.CycleMode,
		CycleID:        fp.CycleID,
		StateIDs:       fp.StateIDs,
		MatchLabels:    matchLabels,
		MatchMode:      matchMode,
		AssigneeMode:   fp.AssigneeMode,
		AssigneeUserID: fp.AssigneeUserID,
		ConcurrencyCap: fp.ConcurrencyCap,
		PrioritySort:   prioritySort,
		DedupMode:      dedupMode,
		OnSentSetLabel: onSentSetLabel,

		Inherits: ProjectInherits{
			PostCreate:     !hasPostCreate,
			Symlinks:       !hasSymlinks,
			Env:            !hasEnv,
			MatchLabels:    !hasMatchLabels,
			MatchMode:      !hasMatchMode,
			OnSentSetLabel: !hasOnSentSetLabel,
			BlockedLabelID: !hasBlockedLabelID,
			DedupMode:      !hasDedupMode,
			PrioritySort:   !hasPrioritySort,
		},

		OnSpawnStateID:   fp.OnSpawnStateID,
		OnPRStateID:      fp.OnPRStateID,
		OnMergedStateID:  fp.OnMergedStateID,
		BlockedLabelID:   blockedLabelID,
		CommentOnSpawn:   fp.CommentOnSpawn,
		CommentOnPR:      fp.CommentOnPR,
		CommentOnMerged:  fp.CommentOnMerged,
		CommentOnBlocked: fp.CommentOnBlocked,
		PRRequiresChecks: fp.PRRequiresChecks,
	}
}

// projectToFile lowers a Project back to the on-disk shape. An inheritable key
// is OMITTED when Inherits says the project leaves it to [defaults] — such a
// field holds the resolved [defaults] value in memory and must not be frozen
// into the file, or a later change to [defaults] would stop reaching it.
func projectToFile(p Project) fileProject {
	set := func(inherits bool) bool { return !inherits }
	o := p.Inherits
	return fileProject{
		Name:           p.Name,
		Label:          p.Label,
		Path:           p.Path,
		Repo:           p.Repo,
		DefaultBranch:  p.DefaultBranch,
		BranchPrefix:   p.BranchPrefix,
		Agent:          p.Agent,
		PostCreate:     ptr(p.PostCreate, set(o.PostCreate)),
		Symlinks:       ptr(p.Symlinks, set(o.Symlinks)),
		Env:            ptr(p.Env, set(o.Env)),
		Enabled:        p.Enabled,
		TeamID:         p.TeamID,
		ProjectID:      p.ProjectID,
		CycleMode:      p.CycleMode,
		CycleID:        p.CycleID,
		StateIDs:       p.StateIDs,
		MatchLabels:    ptr(p.MatchLabels, set(o.MatchLabels)),
		MatchMode:      ptr(p.MatchMode, set(o.MatchMode)),
		AssigneeMode:   p.AssigneeMode,
		AssigneeUserID: p.AssigneeUserID,
		ConcurrencyCap: p.ConcurrencyCap,
		PrioritySort:   ptr(p.PrioritySort, set(o.PrioritySort)),
		DedupMode:      ptr(p.DedupMode, set(o.DedupMode)),
		OnSentSetLabel: ptr(p.OnSentSetLabel, set(o.OnSentSetLabel)),

		OnSpawnStateID:   p.OnSpawnStateID,
		OnPRStateID:      p.OnPRStateID,
		OnMergedStateID:  p.OnMergedStateID,
		BlockedLabelID:   ptr(p.BlockedLabelID, set(o.BlockedLabelID)),
		CommentOnSpawn:   p.CommentOnSpawn,
		CommentOnPR:      p.CommentOnPR,
		CommentOnMerged:  p.CommentOnMerged,
		CommentOnBlocked: p.CommentOnBlocked,
		PRRequiresChecks: p.PRRequiresChecks,
	}
}

type fileDefaults struct {
	PollInterval   Duration `toml:"poll_interval"`
	ConcurrencyCap int      `toml:"concurrency_cap"`
	GlobalCap      int      `toml:"global_cap"`
	Agent          string   `toml:"agent"`
	ManageDaemon   *bool    `toml:"manage_daemon"`

	// Project defaults. Plain values, not pointers: an absent key is a zero
	// value, which already means "no default here" and falls through to the
	// hard fallback (Default*).
	BranchPrefix   string            `toml:"branch_prefix,omitempty"`
	PostCreate     []string          `toml:"post_create,omitempty"`
	Symlinks       []string          `toml:"symlinks,omitempty"`
	Env            map[string]string `toml:"env,omitempty"`
	MatchLabels    []string          `toml:"match_labels,omitempty"`
	MatchMode      string            `toml:"match_mode,omitempty"`
	OnSentSetLabel string            `toml:"on_sent_set_label,omitempty"`
	BlockedLabelID string            `toml:"blocked_label_id,omitempty"`
	DedupMode      string            `toml:"dedup_mode,omitempty"`
	PrioritySort   []string          `toml:"priority_sort,omitempty"`
}

// config flattens the on-disk mirror into the in-memory Config and MIGRATES the
// pre-merge poll shape: each project's inline polling fields load directly, and
// any legacy [[project.poll]] / top-level [[poll]] table is folded onto its
// project. A project already carrying inline polling config keeps it (legacy
// only fills an unconfigured project). More than one legacy poll for a project,
// or a top-level poll whose project does not resolve, is recorded in
// migrateErrs for Validate to surface. On the next Save the legacy tables are
// dropped (the file is migrated in place).
func (fc *fileConfig) config() *Config {
	var projects []Project
	byName := map[string]int{}
	var migrateErrs []error
	for _, fp := range fc.Projects {
		p := projectFromFile(fp)
		if len(fp.LegacyPolls) > 0 {
			if !p.Polls() { // no inline polling: fold the first legacy poll
				fp.LegacyPolls[0].foldOnto(&p)
			}
			if len(fp.LegacyPolls) > 1 {
				migrateErrs = append(migrateErrs, fmt.Errorf(
					"project %q defines %d polls; a project may have at most one polling config", fp.Name, len(fp.LegacyPolls)))
			}
		}
		byName[p.Name] = len(projects)
		projects = append(projects, p)
	}
	// Top-level legacy [[poll]] tables fold onto their referenced project.
	for _, lp := range fc.Polls {
		idx, ok := byName[lp.Project]
		if !ok || lp.Project == "" {
			migrateErrs = append(migrateErrs, fmt.Errorf(
				"top-level poll %q references project %q which is not defined", lp.Name, lp.Project))
			continue
		}
		if projects[idx].Polls() {
			migrateErrs = append(migrateErrs, fmt.Errorf(
				"project %q has both inline polling and a top-level poll %q; keep one", lp.Project, lp.Name))
			continue
		}
		lp.foldOnto(&projects[idx])
	}

	return &Config{
		Defaults: Defaults{
			PollInterval:   time.Duration(fc.Defaults.PollInterval),
			ConcurrencyCap: fc.Defaults.ConcurrencyCap,
			GlobalCap:      fc.Defaults.GlobalCap,
			Agent:          fc.Defaults.Agent,
			ManageDaemon:   fc.Defaults.ManageDaemon,
			BranchPrefix:   fc.Defaults.BranchPrefix,
			PostCreate:     fc.Defaults.PostCreate,
			Symlinks:       fc.Defaults.Symlinks,
			Env:            fc.Defaults.Env,
			MatchLabels:    fc.Defaults.MatchLabels,
			MatchMode:      fc.Defaults.MatchMode,
			OnSentSetLabel: fc.Defaults.OnSentSetLabel,
			BlockedLabelID: fc.Defaults.BlockedLabelID,
			DedupMode:      fc.Defaults.DedupMode,
			PrioritySort:   fc.Defaults.PrioritySort,
		},
		Linear:      fc.Linear,
		Projects:    projects,
		migrateErrs: migrateErrs,
		Reactions:   resolveReactions(fc.Reactions),
		Notify:      resolveNotify(fc.Notify),
		Brain:       resolveBrain(fc.Brain),
		Review:      resolveReview(fc.Review),
		CodeRabbit:  resolveCodeRabbit(fc.CodeRabbit),
		Tmux:        resolveTmux(fc.Tmux),
		UI:          resolveUI(fc.UI),
	}
}

// file serializes the flat model: each project's polling fields are written
// inline; no [[poll]] / [[project.poll]] tables are emitted (the migrated
// schema). This is what rewrites a pre-merge file to the new shape on Save.
func (c *Config) file() *fileConfig {
	fps := make([]fileProject, 0, len(c.Projects))
	for _, p := range c.Projects {
		fps = append(fps, projectToFile(p))
	}
	return &fileConfig{
		Defaults: fileDefaults{
			PollInterval:   Duration(c.Defaults.PollInterval),
			ConcurrencyCap: c.Defaults.ConcurrencyCap,
			GlobalCap:      c.Defaults.GlobalCap,
			Agent:          c.Defaults.Agent,
			ManageDaemon:   c.Defaults.ManageDaemon,
			BranchPrefix:   c.Defaults.BranchPrefix,
			PostCreate:     c.Defaults.PostCreate,
			Symlinks:       c.Defaults.Symlinks,
			Env:            c.Defaults.Env,
			MatchLabels:    c.Defaults.MatchLabels,
			MatchMode:      c.Defaults.MatchMode,
			OnSentSetLabel: c.Defaults.OnSentSetLabel,
			BlockedLabelID: c.Defaults.BlockedLabelID,
			DedupMode:      c.Defaults.DedupMode,
			PrioritySort:   c.Defaults.PrioritySort,
		},
		Linear:     c.Linear,
		Projects:   fps,
		Reactions:  reactionsFile(c.Reactions),
		Notify:     notifyFile(c.Notify),
		Brain:      brainFile(c.Brain),
		Review:     reviewFile(c.Review),
		CodeRabbit: coderabbitFile(c.CodeRabbit),
		Tmux:       tmuxFile(c.Tmux),
		UI:         uiFile(c.UI),
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
// DefaultBranchName.
//
// Compatibility: BurntSushi/toml silently ignores unknown keys, so AO-bridge
// era keys still load and are dropped. Pre-merge configs (a separate [[poll]] /
// [[project.poll]] table) load too — their fields are folded onto the owning
// project and the tables are dropped on the next Save.
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
	// Repair before resolving, so an inherited chain never carries a key the
	// sorter would ignore.
	c.sanitizePrioritySort()
	c.ResolveInheritance()
}

// ResolveInheritance fills every project field the project does NOT override
// (per its Overrides bitmap) from [defaults], falling back to the package
// Default* constants where [defaults] is silent too. After it runs, each
// Project field holds its effective value — which is what the daemon, runtime,
// linear filter and every read-only view consume.
//
// It is idempotent (the override bitmap, not the stored value, is the source of
// truth) and cheap. Load calls it via applyDefaults, and Validate calls it up
// front so a config mutated in memory — the UIs edit [defaults] and projects in
// the same pass — is never validated against stale resolved values.
func (c *Config) ResolveInheritance() {
	d := c.Defaults
	for i := range c.Projects {
		p := &c.Projects[i]
		in := p.Inherits

		// Normalize first, so the bitmap is canonical afterwards and a
		// save/load round trip is an identity. For the slices and the map a NIL
		// value means "never set" (inherit) while an empty non-nil value is a
		// deliberate "override to nothing" — exactly the distinction TOML draws
		// between an absent key and `key = []`.
		//
		// branch_prefix / agent / concurrency_cap are deliberately NOT part of
		// this bitmap: a zero value has always meant "fall back" for them and
		// BranchPrefixForProject / AgentForProject / EffectiveCap already
		// resolve project -> [defaults] -> hard default at read time.
		in.PostCreate = in.PostCreate || p.PostCreate == nil
		in.Symlinks = in.Symlinks || p.Symlinks == nil
		in.Env = in.Env || p.Env == nil
		in.MatchLabels = in.MatchLabels || p.MatchLabels == nil
		in.PrioritySort = in.PrioritySort || p.PrioritySort == nil
		p.Inherits = in

		// The rest resolve on the Inherits bit alone: an empty value there is a
		// deliberate override ("match no labels", "no blocked label"), and for
		// the enums an empty value is a config error Validate must still catch
		// rather than have papered over here.
		if in.MatchMode {
			p.MatchMode = orString(d.MatchMode, DefaultMatchMode)
		}
		if in.DedupMode {
			p.DedupMode = orString(d.DedupMode, DefaultDedupMode)
		}
		if in.OnSentSetLabel {
			p.OnSentSetLabel = d.OnSentSetLabel
		}
		if in.BlockedLabelID {
			p.BlockedLabelID = d.BlockedLabelID
		}
		// Slices/maps are cloned so a project can never alias — and thereby
		// mutate — the shared [defaults] value.
		if in.PostCreate {
			p.PostCreate = slices.Clone(d.PostCreate)
		}
		if in.Symlinks {
			p.Symlinks = slices.Clone(d.Symlinks)
		}
		if in.Env {
			p.Env = maps.Clone(d.Env)
		}
		if in.MatchLabels {
			p.MatchLabels = slices.Clone(d.MatchLabels)
		}
		if in.PrioritySort {
			if len(d.PrioritySort) > 0 {
				p.PrioritySort = slices.Clone(d.PrioritySort)
			} else {
				p.PrioritySort = slices.Clone(DefaultPrioritySort)
			}
		}
	}
}

// orString returns v when non-empty, else the fallback.
func orString(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

// Save writes the config atomically: parents are created 0700, the TOML is
// written to a temp file in the destination directory (so the rename cannot
// cross filesystems), then renamed into place with final mode 0600.
//
// It first canonicalizes the receiver via ResolveInheritance, so what stays in
// memory is exactly what a Load of the written file would produce — a caller
// that mutates [defaults] and saves does not keep stale resolved project
// values, and save/load is an identity.
func (c *Config) Save(path string) error {
	c.ResolveInheritance()

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
