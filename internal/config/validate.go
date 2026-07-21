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

// PollByName returns the polling project with the given name. Since a poll is
// now just a project's polling config, this is an alias for ProjectByName (a
// project's name is its poll's name). Kept so poll-keyed daemon paths read
// naturally; returns nil if no such project exists.
func (c *Config) PollByName(name string) *Project { return c.ProjectByName(name) }

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

// EffectiveCap returns the project's polling concurrency cap. Project fields
// normally already hold the value resolved against [defaults]
// (ResolveInheritance); the zero check keeps this correct on a config that has
// not been resolved yet, which is how it has always behaved.
func (c *Config) EffectiveCap(p *Project) int {
	if p == nil || p.ConcurrencyCap <= 0 {
		return c.Defaults.ConcurrencyCap
	}
	return p.ConcurrencyCap
}

// AgentForProject resolves the coding-agent kind for the named project. The
// project's Agent field already carries the project → [defaults] → "claude"
// resolution; a name matching no project falls back the same way. The returned
// string is one of claude|codex|opencode (Validate rejects any other value).
func (c *Config) AgentForProject(name string) string {
	if pr := c.ProjectByName(name); pr != nil && pr.Agent != "" {
		return pr.Agent
	}
	return orString(c.Defaults.Agent, DefaultAgent)
}

// BranchPrefixForProject resolves the branch-name prefix for the named project.
// As with AgentForProject the project field is pre-resolved; a name matching no
// project falls back to [defaults] then DefaultBranchPrefix ("lola/").
func (c *Config) BranchPrefixForProject(name string) string {
	if pr := c.ProjectByName(name); pr != nil && pr.BranchPrefix != "" {
		return pr.BranchPrefix
	}
	return orString(c.Defaults.BranchPrefix, DefaultBranchPrefix)
}

// PollRepo returns the GitHub "owner/name" repo the project's PR checks run
// against: the project's own `repo`. Empty when unset (PR checks then fail
// closed). Kept as a named helper so poll-keyed daemon paths read naturally.
func (c *Config) PollRepo(p *Project) string {
	if p == nil {
		return ""
	}
	return p.Repo
}

// Validate runs every static check and returns all failures joined via
// errors.Join (nil when the config is valid). It only checks what can be
// verified offline and never execs anything: ID resolution against Linear
// and project path-exists / is-git-repo checks are the caller's (runtime
// layer's) job.
func (c *Config) Validate() error {
	var errs []error

	// A config assembled or edited in memory (the TUI and desktop mutate
	// [defaults] and [[project]] in one pass) may still hold pre-edit resolved
	// values. Re-resolve first so every check below sees effective values.
	c.ResolveInheritance()

	// Structural errors from migrating pre-merge [[poll]] / [[project.poll]]
	// tables onto their project — recorded at load time, surfaced here.
	errs = append(errs, c.migrateErrs...)

	if c.Defaults.GlobalCap <= 0 {
		errs = append(errs, errors.New("defaults.global_cap must be > 0"))
	}

	errs = append(errs, c.validateProjectDefaults()...)

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

	// Polling-config checks run only for a project that actually polls (TeamID
	// set). A non-polling project (manual worktrees / PRs only) needs none of
	// these. team_id is implied by Polls(); the rest mirror the pre-merge poll
	// validation, now scoped to the project.
	for i := range c.Projects {
		p := &c.Projects[i]
		if !p.Polls() {
			continue
		}
		id := fmt.Sprintf("project %q polling", p.Name)

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

		switch p.AssigneeMode {
		case "anyone", "me":
		case "user":
			if p.AssigneeUserID == "" {
				errs = append(errs, fmt.Errorf("%s: assignee_mode=user requires assignee_user_id", id))
			}
		default:
			errs = append(errs, fmt.Errorf("%s: assignee_mode must be one of anyone|me|user, got %q", id, p.AssigneeMode))
		}

		switch p.DedupMode {
		case "label":
			// Label-mode dedup removes ALL trigger labels on the post-spawn flip
			// (so the issue stops matching, for any match_mode) and adds
			// on_sent_set_label to mark it picked up.
			if p.OnSentSetLabel == "" {
				errs = append(errs, fmt.Errorf("%s: dedup_mode=label requires on_sent_set_label", id))
			}
			if len(p.MatchLabels) == 0 {
				errs = append(errs, fmt.Errorf("%s: dedup_mode=label requires match_labels (the removed trigger labels are the primary dedup)", id))
			}
			if p.OnSentSetLabel != "" && slices.Contains(p.MatchLabels, p.OnSentSetLabel) {
				errs = append(errs, fmt.Errorf("%s: on_sent_set_label must not be one of match_labels, otherwise the issue re-matches after the flip and respawns forever", id))
			}
		case "seen":
		case "state":
			// State-based dedup: on spawn Lola moves the issue to OnSpawnStateID,
			// which must lie OUTSIDE state_ids so it stops matching.
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

		// An unknown sort key is silently ignored by SortIssues, so a typo would
		// quietly change pickup order with no signal. Reject it instead.
		for _, k := range p.PrioritySort {
			if !slices.Contains(PrioritySortKeys, k) {
				errs = append(errs, fmt.Errorf("%s: priority_sort: unknown key %q (must be one of %v)", id, k, PrioritySortKeys))
			}
		}

		if c.EffectiveCap(p) <= 0 {
			errs = append(errs, fmt.Errorf("%s: effective concurrency_cap must be > 0 (set the project's concurrency_cap or defaults.concurrency_cap)", id))
		}
	}

	errs = append(errs, c.validateReactions()...)
	errs = append(errs, c.validateNotify()...)
	errs = append(errs, c.validateBrain()...)
	errs = append(errs, c.validateReview()...)
	errs = append(errs, c.validateReviewProviders()...)
	errs = append(errs, c.validateProjectReview()...)
	errs = append(errs, c.validateUI()...)

	return errors.Join(errs...)
}

// validateProjectDefaults checks the [defaults] keys that projects inherit.
//
// The load-bearing check is the team guard: match_labels, on_sent_set_label and
// blocked_label_id hold Linear label UUIDs, and a label UUID only exists within
// one team. A global default is therefore coherent only while every project
// that INHERITS it polls the same team — a project overriding the key with its
// own team's label is fine and is not counted. Without this, a second team's
// project would silently filter on a label that cannot match, and lola would
// look "up but idle" with nothing to point at.
func (c *Config) validateProjectDefaults() []error {
	var errs []error

	if c.Defaults.MatchMode != "" && c.Defaults.MatchMode != "any" && c.Defaults.MatchMode != "all" {
		errs = append(errs, fmt.Errorf("defaults.match_mode must be any|all (empty inherits), got %q", c.Defaults.MatchMode))
	}
	switch c.Defaults.DedupMode {
	case "", "label", "seen", "state":
	default:
		errs = append(errs, fmt.Errorf("defaults.dedup_mode must be label|seen|state (empty inherits), got %q", c.Defaults.DedupMode))
	}
	for _, k := range c.Defaults.PrioritySort {
		if !slices.Contains(PrioritySortKeys, k) {
			errs = append(errs, fmt.Errorf("defaults.priority_sort: unknown key %q (must be one of %v)", k, PrioritySortKeys))
		}
	}
	// Same shell-identifier rule as [[project]].env — these pairs reach the
	// same 0600 shell-sourced env file at spawn time. See envNameRe.
	for _, k := range slices.Sorted(maps.Keys(c.Defaults.Env)) {
		if !envNameRe.MatchString(k) {
			errs = append(errs, fmt.Errorf("defaults.env key %q is not a valid shell identifier (must match [A-Za-z_][A-Za-z0-9_]*)", k))
		}
	}

	// NOTE: there is deliberately NO cross-team check on the label keys here.
	//
	// An earlier version rejected a [defaults] label whenever polling projects
	// spanned several teams, on the grounds that a Linear label UUID is
	// team-scoped. That is only true of TEAM labels: Linear also has
	// workspace-level labels (IssueLabel.team == null) which exist across every
	// team, and those are exactly what a shared [defaults] label should be — so
	// the check rejected the correct configuration.
	//
	// Whether a given UUID is workspace- or team-scoped cannot be known offline,
	// and this package never touches the network. The distinction is enforced
	// where it CAN be: the settings UIs offer only workspace labels for the
	// [defaults] keys, and per-team labels only on a project.
	return errs
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

// validateReviewProviders checks the NEW [[review.provider]] catalog. Its rules
// mirror the PrioritySort dup/unknown pattern above and enforce the provider
// invariants (see reviewprovider.go / PLAN §1.4):
//
//   - the catalog and the legacy [review]/[coderabbit] tables are MUTUALLY
//     EXCLUSIVE — a mixed file is a hard error pointing at `lola config
//     migrate-review`;
//   - unknown provider kind; more than one provider of the same kind (guards
//     key by kind, so a duplicate is ambiguous);
//   - unknown transport token; the github transport on a coderabbit-watch (its
//     feedback is already on the PR — a review of it is a self-feedback loop);
//   - fallback on a watch (a watch cannot classify quota); a fallback entry
//     that is an unknown kind / the provider's own kind / a watch kind / not
//     present-and-enabled in the catalog / part of a cycle;
//   - timeout_seconds < 0.
//
// An empty catalog validates trivially (the legacy path is checked by
// validateReview).
func (c *Config) validateReviewProviders() []error {
	var errs []error
	if len(c.ReviewProviders) == 0 {
		return errs
	}

	// Mutually exclusive with the legacy tables. A mixed file is a hard error,
	// resolved by the one-way `lola config migrate-review`.
	if c.Review != (ReviewConfig{}) || c.CodeRabbit != (CodeRabbitConfig{}) {
		errs = append(errs, errors.New(
			"the [[review.provider]] catalog cannot coexist with the legacy [review]/[coderabbit] tables; run `lola config migrate-review` to fold the legacy tables into the catalog"))
	}

	// Index providers by kind, catching unknown kinds and duplicates.
	byKind := map[provKind]ReviewProvider{}
	seen := map[provKind]bool{}
	for _, p := range c.ReviewProviders {
		if !p.Provider.valid() {
			errs = append(errs, fmt.Errorf("review provider: unknown provider kind %q (must be coderabbit-cli|coderabbit-watch|claude-session)", p.Provider))
			continue
		}
		if seen[p.Provider] {
			errs = append(errs, fmt.Errorf("review provider %q: at most one provider per kind is allowed", p.Provider))
			continue
		}
		seen[p.Provider] = true
		byKind[p.Provider] = p
	}

	for _, p := range c.ReviewProviders {
		if !p.Provider.valid() {
			continue
		}
		if p.TimeoutSeconds < 0 {
			errs = append(errs, fmt.Errorf("review provider %q: timeout_seconds must be >= 0, got %d", p.Provider, p.TimeoutSeconds))
		}
		for _, t := range p.Transports {
			if !t.valid() {
				errs = append(errs, fmt.Errorf("review provider %q: unknown transport %q (must be lola|github|linear)", p.Provider, t))
			}
		}
		if p.Provider.isWatch() && p.Transports.Has(TransportGitHub) {
			errs = append(errs, fmt.Errorf("review provider %q: the github transport is not allowed on a watch provider (its feedback is already on the PR)", p.Provider))
		}
		if p.Provider.isWatch() && len(p.Fallback) > 0 {
			errs = append(errs, fmt.Errorf("review provider %q: fallback is not allowed on a watch provider (a watch cannot classify over-quota)", p.Provider))
		}
		for _, fb := range p.Fallback {
			switch {
			case !fb.valid():
				errs = append(errs, fmt.Errorf("review provider %q: fallback references unknown kind %q", p.Provider, fb))
			case fb == p.Provider:
				errs = append(errs, fmt.Errorf("review provider %q: fallback cannot reference its own kind", p.Provider))
			case fb.isWatch():
				errs = append(errs, fmt.Errorf("review provider %q: fallback cannot reference the watch kind %q (fallback is pass-shape only)", p.Provider, fb))
			default:
				target, ok := byKind[fb]
				if !ok {
					errs = append(errs, fmt.Errorf("review provider %q: fallback %q is not present in the catalog", p.Provider, fb))
				} else if !target.Enabled {
					errs = append(errs, fmt.Errorf("review provider %q: fallback %q must be enabled", p.Provider, fb))
				}
			}
		}
	}

	if cyc := reviewFallbackCycle(byKind); cyc != "" {
		errs = append(errs, fmt.Errorf("review provider fallback forms a cycle involving %q", cyc))
	}

	return errs
}

// reviewFallbackCycle returns a kind on a fallback cycle, or "" if the fallback
// graph (edges provider -> each fallback kind present in the catalog) is
// acyclic. Standard white/gray/black DFS; kinds are visited in sorted order for
// a deterministic message.
func reviewFallbackCycle(byKind map[provKind]ReviewProvider) provKind {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[provKind]int{}
	var dfs func(k provKind) provKind
	dfs = func(k provKind) provKind {
		color[k] = gray
		for _, fb := range byKind[k].Fallback {
			if _, ok := byKind[fb]; !ok {
				continue // unknown/absent target — reported separately
			}
			switch color[fb] {
			case gray:
				return fb
			case white:
				if c := dfs(fb); c != "" {
					return c
				}
			}
		}
		color[k] = black
		return ""
	}
	kinds := make([]provKind, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)
	for _, k := range kinds {
		if color[k] == white {
			if c := dfs(k); c != "" {
				return c
			}
		}
	}
	return ""
}

// validateProjectReview checks each project's per-project review-provider
// override (Project.Review, the inheritable `review` key). A selected kind must
// name a known provider AND be present-and-enabled in the effective catalog
// (catalog when configured, else legacy synthesis) — otherwise the project
// would ask for a review nothing can run. A nil/empty selection validates
// trivially (inherit / no per-project review). The resolved value is checked,
// so an inherited selection is validated on every project that carries it.
func (c *Config) validateProjectReview() []error {
	var errs []error

	// Enabled provider kinds available to select from.
	enabled := map[provKind]bool{}
	for _, p := range c.EffectiveReviewProviders() {
		if p.Enabled {
			enabled[p.Provider] = true
		}
	}

	for i := range c.Projects {
		pr := &c.Projects[i]
		id := fmt.Sprintf("project %q", pr.Name)
		for _, k := range pr.Review {
			switch {
			case !k.valid():
				errs = append(errs, fmt.Errorf("%s: review references unknown provider kind %q (must be coderabbit-cli|coderabbit-watch|claude-session)", id, k))
			case !enabled[k]:
				errs = append(errs, fmt.Errorf("%s: review kind %q must be an enabled provider in the catalog", id, k))
			}
		}
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
