package config

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
)

// repoRe matches a GitHub "owner/name" reference. Deliberately loose (GitHub's
// own rules are stricter) — it only has to catch obvious mistakes like URLs,
// missing owner, or embedded whitespace.
var repoRe = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)

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

	if c.Defaults.GlobalCap <= 0 {
		errs = append(errs, errors.New("defaults.global_cap must be > 0"))
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
		default:
			errs = append(errs, fmt.Errorf("%s: dedup_mode must be label|seen, got %q", id, p.DedupMode))
		}

		if c.EffectiveCap(p) <= 0 {
			errs = append(errs, fmt.Errorf("%s: effective concurrency_cap must be > 0 (set poll concurrency_cap or defaults.concurrency_cap)", id))
		}
	}

	return errors.Join(errs...)
}
