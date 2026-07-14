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

// EffectiveCap returns the poll's concurrency cap, falling back to
// [defaults].concurrency_cap when the poll does not set one.
func (c *Config) EffectiveCap(p *Poll) int {
	if p != nil && p.ConcurrencyCap > 0 {
		return p.ConcurrencyCap
	}
	return c.Defaults.ConcurrencyCap
}

// Validate runs every static check and returns all failures joined via
// errors.Join (nil when the config is valid). It only checks what can be
// verified offline; ID resolution against Linear and ao_project existence
// are the caller's job.
func (c *Config) Validate() error {
	var errs []error

	if c.Defaults.GlobalCap <= 0 {
		errs = append(errs, errors.New("defaults.global_cap must be > 0"))
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

		// repo is optional: without it the reconciler's open-PR check is
		// unavailable and orphan reverts are skipped (fail-closed).
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

		switch p.DedupMode {
		case "label":
			if p.OnSentSetLabel == "" || p.OnSentRemoveLabel == "" {
				errs = append(errs, fmt.Errorf("%s: dedup_mode=label requires both on_sent_set_label and on_sent_remove_label", id))
			}
			// Label-mode dedup only works when the post-spawn flip makes the
			// issue stop matching; enforce that invariant statically.
			switch {
			case len(p.MatchLabels) == 0:
				errs = append(errs, fmt.Errorf("%s: dedup_mode=label requires match_labels (the flipped trigger label is the primary dedup)", id))
			case p.OnSentRemoveLabel != "" && !slices.Contains(p.MatchLabels, p.OnSentRemoveLabel):
				errs = append(errs, fmt.Errorf("%s: on_sent_remove_label must be one of match_labels, otherwise the label flip does not stop the issue from matching", id))
			case p.MatchMode == "any" && len(p.MatchLabels) > 1:
				errs = append(errs, fmt.Errorf("%s: dedup_mode=label with match_mode=any requires exactly one match label (removing on_sent_remove_label would leave other trigger labels matching)", id))
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
