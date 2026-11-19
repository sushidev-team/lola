package daemon

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/secrets"
)

// labelMemo remembers what each (team, reference) resolved to at the last
// successful lookup, so a transient Linear failure on a later reload keeps the
// working ids instead of swapping them back for raw names.
type labelMemo map[string][]string

func memoKey(teamID, ref string) string { return teamID + "\x00" + ref }

// resolveLabelRefs rewrites label NAMES ("agent/agent-ready", "Bug", or a glob
// like "*/agent-ready") in each project's match_labels / on_sent_set_label /
// blocked_label_id to the label UUID(s) available to that project's team (its
// own labels plus workspace labels), so one
// [defaults] entry works for every team. UUIDs pass through untouched. It runs
// on the freshly loaded config (startup and each reload), in memory only: the
// config is marked unsavable so the names stay in config.toml.
//
// Under match_mode "all" every id a glob expands to is required: each match is
// its own trigger label.
//
// Fails open per reference: no Linear key, a Linear error or an unmatched name
// leaves the reference as written (the dead-filter report then names it), or
// the id memo holds from the last good lookup, and returns a note per problem.
// A project whose resolved on_sent_set_label is also one of its resolved
// match_labels keeps its raw references (it would respawn the issue forever).
// A lin of nil builds a client from cfg's key source. The returned memo is the
// one to pass to the next call.
func resolveLabelRefs(ctx context.Context, cfg *config.Config, lin linear.API, prev labelMemo) ([]string, labelMemo) {
	need := false
	for _, p := range cfg.Projects {
		if hasLabelName(p.MatchLabels) || hasLabelName([]string{p.OnSentSetLabel, p.BlockedLabelID}) {
			need = true
			break
		}
	}
	if !need {
		return nil, nil
	}
	if lin == nil {
		key, err := secrets.LinearAPIKey(cfg.Linear.APIKeyKeychain, cfg.Linear.APIKeyEnv)
		if err != nil {
			return []string{"label names not resolved: " + err.Error()}, prev
		}
		lin = linear.New(cfg.Linear.Endpoint, key)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	var notes []string
	memo := labelMemo{}
	type catalog struct {
		labels []linear.Label
		err    error
	}
	teams := map[string]catalog{} // a failure is cached too: one request, one note, per team per call
	var workspace *catalog        // shared by every team; failures are cached too
	teamLabels := func(teamID string) ([]linear.Label, error) {
		c, ok := teams[teamID]
		if !ok {
			c.labels, c.err = lin.Labels(ctx, teamID)
			if c.err != nil {
				notes = append(notes, fmt.Sprintf("list labels of team %s: %v", teamID, c.err))
			} else {
				if workspace == nil {
					workspace = &catalog{}
					workspace.labels, workspace.err = lin.WorkspaceLabels(ctx)
					if workspace.err != nil {
						notes = append(notes, fmt.Sprintf("list workspace labels: %v", workspace.err))
					}
				}
				// A partial catalog could hide an ambiguous name or truncate a
				// glob. Keep the last good resolution until both scopes load.
				c.err = workspace.err
				seen := map[string]bool{}
				for _, l := range c.labels {
					seen[l.ID] = true
				}
				for _, l := range workspace.labels {
					if !seen[l.ID] {
						c.labels = append(c.labels, l)
						seen[l.ID] = true
					}
				}
			}
			teams[teamID] = c
		}
		return c.labels, c.err
	}
	// resolveMany expands one reference: a glob may name several labels.
	resolveMany := func(teamID, ref string) []string {
		if ref == "" || labelUUIDRe.MatchString(ref) {
			return []string{ref}
		}
		if teamID == "" {
			notes = append(notes, fmt.Sprintf("label %q has no team_id to resolve in", ref))
			return []string{ref}
		}
		labels, err := teamLabels(teamID)
		if err != nil {
			if ids, ok := prev[memoKey(teamID, ref)]; ok {
				memo[memoKey(teamID, ref)] = ids // lookup failed: keep the last good answer
				return ids
			}
			return []string{ref}
		}
		ids, err := matchLabelRefs(labels, ref)
		if err != nil {
			notes = append(notes, err.Error())
			return []string{ref}
		}
		memo[memoKey(teamID, ref)] = ids
		return ids
	}
	// resolveOne is for a setting that holds exactly one label.
	resolveOne := func(teamID, ref string) string {
		ids := resolveMany(teamID, ref)
		if len(ids) != 1 {
			notes = append(notes, fmt.Sprintf("label pattern %q matches %d labels in the project's team; this setting takes one", ref, len(ids)))
			return ref
		}
		return ids[0]
	}
	changed := false
	for i := range cfg.Projects {
		p := &cfg.Projects[i]
		raw := *p
		var ml []string
		for _, ref := range p.MatchLabels {
			for _, id := range resolveMany(p.TeamID, ref) {
				if !slices.Contains(ml, id) {
					ml = append(ml, id)
				}
			}
		}
		onSent := resolveOne(p.TeamID, p.OnSentSetLabel)
		blocked := resolveOne(p.TeamID, p.BlockedLabelID)
		if p.DedupMode == "label" && onSent != "" && slices.Contains(ml, onSent) {
			// Validate compares raw strings; names and globs can still land on
			// the same id. Leave this project unresolved (it matches nothing and
			// the dead-filter report names it) rather than respawn forever.
			notes = append(notes, fmt.Sprintf("project %s: on_sent_set_label and match_labels resolve to the same label; not resolved", p.Name))
			*p = raw
			continue
		}
		if !slices.Equal(ml, raw.MatchLabels) || onSent != raw.OnSentSetLabel || blocked != raw.BlockedLabelID {
			changed = true
		}
		p.MatchLabels, p.OnSentSetLabel, p.BlockedLabelID = ml, onSent, blocked
	}
	if changed {
		cfg.MarkLabelRefsResolved()
	}
	return notes, memo
}

func hasLabelName(refs []string) bool {
	for _, r := range refs {
		if strings.TrimSpace(r) != "" && !labelUUIDRe.MatchString(r) {
			return true
		}
	}
	return false
}
