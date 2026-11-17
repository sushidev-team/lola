package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/secrets"
)

// resolveLabelRefs rewrites label NAMES ("apps/website", "Bug") in each
// project's match_labels / on_sent_set_label / blocked_label_id to the label
// UUID of that project's own team, so one [defaults] entry works for every
// team. UUIDs pass through untouched. It runs on the freshly loaded config
// (startup and each reload), in memory only: the config is marked unsavable
// so the names stay in config.toml.
//
// Fails open: no Linear key, a Linear error or an unmatched name leaves the
// reference as written (the dead-filter report then names it) and returns a
// note per problem. A client of nil builds one from cfg's key source.
func resolveLabelRefs(ctx context.Context, cfg *config.Config, lin linear.API) []string {
	need := false
	for _, p := range cfg.Projects {
		if hasLabelName(p.MatchLabels) || hasLabelName([]string{p.OnSentSetLabel, p.BlockedLabelID}) {
			need = true
			break
		}
	}
	if !need {
		return nil
	}
	if lin == nil {
		key, err := secrets.LinearAPIKey(cfg.Linear.APIKeyKeychain, cfg.Linear.APIKeyEnv)
		if err != nil {
			return []string{"label names not resolved: " + err.Error()}
		}
		lin = linear.New(cfg.Linear.Endpoint, key)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	var notes []string
	teams := map[string][]linear.Label{}
	resolve := func(teamID, ref string) string {
		if ref == "" || labelUUIDRe.MatchString(ref) {
			return ref
		}
		if teamID == "" {
			notes = append(notes, fmt.Sprintf("label %q has no team_id to resolve in", ref))
			return ref
		}
		labels, ok := teams[teamID]
		if !ok {
			var err error
			if labels, err = lin.Labels(ctx, teamID); err != nil {
				notes = append(notes, fmt.Sprintf("list labels of team %s: %v", teamID, err))
				return ref
			}
			teams[teamID] = labels
		}
		id, err := matchLabelRef(labels, ref)
		if err != nil {
			notes = append(notes, err.Error())
			return ref
		}
		return id
	}
	changed := false
	for i := range cfg.Projects {
		p := &cfg.Projects[i]
		for j, ref := range p.MatchLabels {
			if id := resolve(p.TeamID, ref); id != ref {
				p.MatchLabels[j] = id
				changed = true
			}
		}
		if id := resolve(p.TeamID, p.OnSentSetLabel); id != p.OnSentSetLabel {
			p.OnSentSetLabel, changed = id, true
		}
		if id := resolve(p.TeamID, p.BlockedLabelID); id != p.BlockedLabelID {
			p.BlockedLabelID, changed = id, true
		}
	}
	if changed {
		cfg.MarkLabelRefsResolved()
	}
	return notes
}

func hasLabelName(refs []string) bool {
	for _, r := range refs {
		if strings.TrimSpace(r) != "" && !labelUUIDRe.MatchString(r) {
			return true
		}
	}
	return false
}
