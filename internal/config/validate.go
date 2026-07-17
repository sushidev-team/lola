package config

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"

	"github.com/sushidev-team/lola/internal/notify"
)

// repoRe matches a GitHub "owner/name" reference. Deliberately loose (GitHub's
// own rules are stricter) — it only has to catch obvious mistakes like URLs,
// missing owner, or embedded whitespace.
var repoRe = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)

// envNameRe matches a POSIX shell identifier — the only shape a [[project]].env
// key may take. This is a SECURITY check, not a cosmetic one: the native
// runtime writes each pair into a 0600 <dir>/.lola/env file that the session
// launch line `source`s under `set -a` AFTER the Linear API key is already
// exported, so a key carrying shell metacharacters (TOML permits arbitrary
// quoted bare keys, e.g. `"x; curl … $LINEAR_API_KEY #" = "y"`) would be
// parsed as shell and could exfiltrate the key. Rejecting non-identifier names
// here keeps that from ever reaching the launcher.
var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// PollByName returns a pointer to the poll with the given name (mutating it
// mutates the config), or nil if no such poll exists.
func (c *Config) PollByName(name string) *Poll {
	for i := range c.Polls {
		if c.Polls[i].Name == name {
			return &c.Polls[i]
		}
	}
	return nil
}

// ProjectByName returns a pointer to the project with the given name
// (mutating it mutates the config), or nil if no such project exists.
func (c *Config) ProjectByName(name string) *Project {
	for i := range c.Projects {
		if c.Projects[i].Name == name {
			return &c.Projects[i]
		}
	}
	return nil
}

// EffectiveCap returns the poll's concurrency cap, falling back to
// [defaults].concurrency_cap when the poll does not set one.
func (c *Config) EffectiveCap(p *Poll) int {
	if p != nil && p.ConcurrencyCap > 0 {
		return p.ConcurrencyCap
	}
	return c.Defaults.ConcurrencyCap
}

// AgentForProject resolves the coding-agent kind for the named project:
// the matching project's Agent when non-empty, else [defaults].agent when
// non-empty, else the hard "claude" fallback. A name that resolves to no
// project falls through to the defaults / claude. The returned string is one
// of claude|codex|opencode (Validate rejects any other configured value).
func (c *Config) AgentForProject(name string) string {
	if pr := c.ProjectByName(name); pr != nil && pr.Agent != "" {
		return pr.Agent
	}
	if c.Defaults.Agent != "" {
		return c.Defaults.Agent
	}
	return "claude"
}

// BranchPrefixForProject resolves the branch-name prefix for the named project:
// the project's BranchPrefix when set, else DefaultBranchPrefix ("lola/"). A
// name that resolves to no project falls back to the default.
func (c *Config) BranchPrefixForProject(name string) string {
	if pr := c.ProjectByName(name); pr != nil && pr.BranchPrefix != "" {
		return pr.BranchPrefix
	}
	return DefaultBranchPrefix
}

// PollRepo returns the GitHub "owner/name" repo the poll's PR checks run
// against: the poll's own `repo` when set, else the referenced [[project]]'s
// repo. Empty when neither is configured (PR checks then fail closed).
func (c *Config) PollRepo(p *Poll) string {
	if p == nil {
		return ""
	}
	if p.Repo != "" {
		return p.Repo
	}
	if pr := c.ProjectByName(p.Project); pr != nil {
		return pr.Repo
	}
	return ""
}

// Validate runs every static check and returns all failures joined via
// errors.Join (nil when the config is valid). It only checks what can be
// verified offline and never execs anything: ID resolution against Linear
// and project path-exists / is-git-repo checks are the caller's (runtime
// layer's) job.
func (c *Config) Validate() error {
	var errs []error

	// Structural errors from flattening nested [[project.poll]] tables (a nested
	// poll naming a different project) — recorded at load time, surfaced here.
	errs = append(errs, c.nestConflicts...)

	if c.Defaults.GlobalCap <= 0 {
		errs = append(errs, errors.New("defaults.global_cap must be > 0"))
	}

	// agent picks the coding agent a session spawns. Empty is allowed (a
	// project may inherit it, and the chain hard-defaults to claude); a set
	// value must name a known kind.
	switch c.Defaults.Agent {
	case "", "claude", "codex", "opencode":
	default:
		errs = append(errs, fmt.Errorf("defaults.agent must be one of claude|codex|opencode (empty inherits), got %q", c.Defaults.Agent))
	}

	// [[project]] registry checks run unconditionally — a broken project
	// definition is an error even before any poll references it.
	projectNames := make(map[string]bool, len(c.Projects))
	for i := range c.Projects {
		pr := &c.Projects[i]

		id := fmt.Sprintf("project %q", pr.Name)
		if pr.Name == "" {
			id = fmt.Sprintf("project[%d]", i)
			errs = append(errs, fmt.Errorf("%s: name is required", id))
		} else if projectNames[pr.Name] {
			errs = append(errs, fmt.Errorf("%s: duplicate name", id))
		}
		projectNames[pr.Name] = true

		if pr.Path == "" {
			errs = append(errs, fmt.Errorf("%s: path is required", id))
		}
		if pr.Repo != "" && !repoRe.MatchString(pr.Repo) {
			errs = append(errs, fmt.Errorf(`%s: repo must be "owner/name" (e.g. "sushidev-team/nori-app"), got %q`, id, pr.Repo))
		}
		// Per-project coding-agent override: empty inherits [defaults].agent
		// (AgentForProject), a set value must name a known kind.
		switch pr.Agent {
		case "", "claude", "codex", "opencode":
		default:
			errs = append(errs, fmt.Errorf("%s: agent must be one of claude|codex|opencode (empty inherits), got %q", id, pr.Agent))
		}
		// env keys become NAME= assignments in a shell-sourced file at spawn
		// time; only POSIX shell identifiers are allowed (see envNameRe) so a
		// crafted name can never be parsed as a command.
		for _, k := range slices.Sorted(maps.Keys(pr.Env)) {
			if !envNameRe.MatchString(k) {
				errs = append(errs, fmt.Errorf("%s: env key %q is not a valid shell identifier (must match [A-Za-z_][A-Za-z0-9_]*)", id, k))
			}
		}
	}

	names := make(map[string]bool, len(c.Polls))
	for i := range c.Polls {
		p := &c.Polls[i]

		// Label errors by name when we have one, by index otherwise.
		id := fmt.Sprintf("poll %q", p.Name)
		if p.Name == "" {
			id = fmt.Sprintf("poll[%d]", i)
			errs = append(errs, fmt.Errorf("%s: name is required", id))
		} else if names[p.Name] {
			errs = append(errs, fmt.Errorf("%s: duplicate name", id))
		}
		names[p.Name] = true

		if p.TeamID == "" {
			errs = append(errs, fmt.Errorf("%s: team_id is required", id))
		}

		switch p.CycleMode {
		case "none", "active":
		case "pinned":
			if p.CycleID == "" {
				errs = append(errs, fmt.Errorf("%s: cycle_mode=pinned requires cycle_id", id))
			}
		default:
			errs = append(errs, fmt.Errorf("%s: cycle_mode must be one of none|active|pinned, got %q", id, p.CycleMode))
		}

		switch p.MatchMode {
		case "any", "all":
		default:
			errs = append(errs, fmt.Errorf("%s: match_mode must be any|all, got %q", id, p.MatchMode))
		}

		// repo is optional: PR checks fall back to the [[project]] repo
		// (PollRepo); with neither set they are unavailable and orphan
		// reverts are skipped (fail-closed).
		if p.Repo != "" && !repoRe.MatchString(p.Repo) {
			errs = append(errs, fmt.Errorf(`%s: repo must be "owner/name" (e.g. "sushidev-team/nori-app"), got %q`, id, p.Repo))
		}

		switch p.AssigneeMode {
		case "anyone", "me":
		case "user":
			if p.AssigneeUserID == "" {
				errs = append(errs, fmt.Errorf("%s: assignee_mode=user requires assignee_user_id", id))
			}
		default:
			errs = append(errs, fmt.Errorf("%s: assignee_mode must be one of anyone|me|user, got %q", id, p.AssigneeMode))
		}

		// Every poll spawns via the native runtime, so its [[project]]
		// reference is mandatory and must resolve.
		if p.Project == "" {
			errs = append(errs, fmt.Errorf("%s: project is required (a [[project]] name)", id))
		} else if c.ProjectByName(p.Project) == nil {
			errs = append(errs, fmt.Errorf("%s: project %q is not defined as a [[project]]", id, p.Project))
		}

		switch p.DedupMode {
		case "label":
			// Label-mode dedup works by removing ALL trigger labels on the
			// post-spawn flip (so the issue stops matching, for any match_mode)
			// and adding on_sent_set_label to mark it picked up.
			if p.OnSentSetLabel == "" {
				errs = append(errs, fmt.Errorf("%s: dedup_mode=label requires on_sent_set_label", id))
			}
			if len(p.MatchLabels) == 0 {
				errs = append(errs, fmt.Errorf("%s: dedup_mode=label requires match_labels (the removed trigger labels are the primary dedup)", id))
			}
			// The sent label must not itself be a trigger label, or the issue
			// re-matches immediately after the flip and respawns forever.
			if p.OnSentSetLabel != "" && slices.Contains(p.MatchLabels, p.OnSentSetLabel) {
				errs = append(errs, fmt.Errorf("%s: on_sent_set_label must not be one of match_labels, otherwise the issue re-matches after the flip and respawns forever", id))
			}
		case "seen":
		case "state":
			// State-based dedup (P4): on spawn Lola moves the issue to
			// OnSpawnStateID, which takes it OUT of state_ids so it stops
			// matching — no seen file, no label flip. This only works when the
			// poll actually filters by state (state_ids) AND the target state
			// lies OUTSIDE that filter; otherwise the issue keeps matching after
			// the transition and respawns forever. The state IDs are checked for
			// non-emptiness only; they are never resolved against Linear here.
			if len(p.StateIDs) == 0 {
				errs = append(errs, fmt.Errorf("%s: dedup_mode=state requires state_ids (the matching set the issue leaves on spawn)", id))
			}
			if p.OnSpawnStateID == "" {
				errs = append(errs, fmt.Errorf("%s: dedup_mode=state requires on_spawn_state_id (the state the issue moves into, out of state_ids)", id))
			} else if slices.Contains(p.StateIDs, p.OnSpawnStateID) {
				errs = append(errs, fmt.Errorf("%s: on_spawn_state_id must not be one of state_ids, otherwise the issue still matches after the transition and respawns forever", id))
			}
		default:
			errs = append(errs, fmt.Errorf("%s: dedup_mode must be label|seen|state, got %q", id, p.DedupMode))
		}

		if c.EffectiveCap(p) <= 0 {
			errs = append(errs, fmt.Errorf("%s: effective concurrency_cap must be > 0 (set poll concurrency_cap or defaults.concurrency_cap)", id))
		}
	}

	errs = append(errs, c.validateReactions()...)
	errs = append(errs, c.validateNotify()...)
	errs = append(errs, c.validateBrain()...)
	errs = append(errs, c.validateReview()...)

	return errors.Join(errs...)
}

// validateReview checks the [review] table. The only rule is timeout_seconds >= 0;
// a config lacking the table resolves to the zero ReviewConfig (timeout 0) and so
// validates cleanly. Enablement, the command override, and the hand-off flags are
// unconstrained.
func (c *Config) validateReview() []error {
	var errs []error
	if c.Review.TimeoutSeconds < 0 {
		errs = append(errs, fmt.Errorf("review.timeout_seconds must be >= 0, got %d", c.Review.TimeoutSeconds))
	}
	return errs
}

// validateBrain checks the [brain] table. The only rule is timeout_seconds >= 0;
// a config lacking the table resolves to the zero BrainConfig (timeout 0) and so
// validates cleanly. Enablement, model, and the summarize flags are unconstrained.
func (c *Config) validateBrain() []error {
	var errs []error
	if c.Brain.TimeoutSeconds < 0 {
		errs = append(errs, fmt.Errorf("brain.timeout_seconds must be >= 0, got %d", c.Brain.TimeoutSeconds))
	}
	return errs
}

// validateReactions checks the [reactions] table. Auto and Message are
// free-form (no validation); only retries has a hard rule: it must be >= 0.
// A config lacking the table validates cleanly (defaults have retries >= 0).
func (c *Config) validateReactions() []error {
	var errs []error
	reactions := []struct {
		name string
		r    Reaction
	}{
		{"ci_failed", c.Reactions.CIFailed},
		{"changes_requested", c.Reactions.ChangesRequested},
		{"merge_conflict", c.Reactions.MergeConflict},
		{"approved_and_green", c.Reactions.ApprovedAndGreen},
		{"merged", c.Reactions.Merged},
	}
	for _, rc := range reactions {
		if rc.r.Retries < 0 {
			errs = append(errs, fmt.Errorf("reactions.%s: retries must be >= 0, got %d", rc.name, rc.r.Retries))
		}
	}
	return errs
}

// validateNotify checks [notify.routing]: every priority key must be one of
// urgent|action|info and every channel one of desktop|slack. A config lacking
// the table (nil routing) validates cleanly. Keys are visited in sorted order
// for deterministic error output.
func (c *Config) validateNotify() []error {
	var errs []error
	for _, prio := range slices.Sorted(maps.Keys(c.Notify.Routing)) {
		if _, ok := notifyPriorities[prio]; !ok {
			errs = append(errs, fmt.Errorf("notify.routing: unknown priority %q (must be one of urgent|action|info)", prio))
		}
		for _, ch := range c.Notify.Routing[prio] {
			switch ch {
			case notify.ChannelDesktop, notify.ChannelSlack:
			default:
				errs = append(errs, fmt.Errorf("notify.routing.%s: unknown channel %q (must be one of desktop|slack)", prio, ch))
			}
		}
	}
	return errs
}
