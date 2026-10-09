package daemon

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
)

// Stale filter references.
//
// A poll filters on Linear UUIDs (match labels, workflow states). When one of
// them stops existing — a label deleted, or a workspace label replaced by
// per-team copies that carry NEW ids — Linear does not reject the query: it
// answers with zero issues. The tick then logs "matched=0", the status reads
// healthy, and an issue that looks perfectly ready in Linear is never picked
// up, with nothing anywhere saying why. That silence is the bug this file
// closes: each tick resolves the poll's references (cached, one query per
// poll per refTTL) and a reference that cannot match is reported as the poll's
// LastError, which every surface (status CLI, TUI, app project panel) already
// renders.
//
// It FAILS OPEN: a lookup that errors reports nothing and caches nothing. The
// point is to name a broken config with certainty; a Linear blip must never
// paint a working poll red. It never changes what the tick dispatches either —
// the filter is still sent as configured, so a false report costs a message,
// not a spawn.

// refTTL bounds how often a poll's references are re-resolved. Labels and
// states change rarely, and a config edit changes the cache key at once.
const refTTL = 10 * time.Minute

type refCacheEntry struct {
	key      string
	at       time.Time
	problems []string
}

type refCache struct {
	mu sync.Mutex
	m  map[string]refCacheEntry
}

// refKey is everything the verdict depends on: the team and the IDs checked.
func refKey(p config.Project) string {
	return p.TeamID + "|" + p.MatchMode + "|" + strings.Join(p.MatchLabels, ",") + "|" + strings.Join(p.StateIDs, ",") + "|" + p.OnSentSetLabel
}

// filterRefProblems returns the poll's unmatchable references, newest verdict
// from cache when the config and TTL allow.
func (d *Daemon) filterRefProblems(ctx context.Context, api linear.API, name string, p config.Project, now time.Time) []string {
	if len(p.MatchLabels) == 0 && len(p.StateIDs) == 0 {
		return nil
	}
	key := refKey(p)
	d.refs.mu.Lock()
	if e, ok := d.refs.m[name]; ok && e.key == key && now.Sub(e.at) < refTTL {
		d.refs.mu.Unlock()
		return e.problems
	}
	d.refs.mu.Unlock()

	ids := slices.Clone(p.MatchLabels)
	if p.OnSentSetLabel != "" && !slices.Contains(ids, p.OnSentSetLabel) {
		ids = append(ids, p.OnSentSetLabel)
	}
	labels, states, err := api.FilterRefs(ctx, ids, p.StateIDs)
	if err != nil {
		d.logf(name, "filter reference check skipped: %v", err)
		return nil
	}
	problems := refProblems(p, labels, states)

	d.refs.mu.Lock()
	if d.refs.m == nil {
		d.refs.m = map[string]refCacheEntry{}
	}
	d.refs.m[name] = refCacheEntry{key: key, at: now, problems: problems}
	d.refs.mu.Unlock()
	return problems
}

// refProblems is the pure verdict. A label is unusable when Linear does not
// know it or it belongs to ANOTHER team (a team label never appears on another
// team's issues); a state, likewise. It only reports what makes the filter
// unable to match — with match_mode=any one dead label among live ones still
// matches, so that case is not an error.
func refProblems(p config.Project, labels, states []linear.Ref) []string {
	byID := func(refs []linear.Ref) map[string]linear.Ref {
		m := make(map[string]linear.Ref, len(refs))
		for _, r := range refs {
			m[r.ID] = r
		}
		return m
	}
	usable := func(r linear.Ref, ok bool) bool {
		return ok && (r.TeamID == "" || r.TeamID == p.TeamID)
	}
	why := func(kind, id string, r linear.Ref, ok bool) string {
		if !ok {
			return fmt.Sprintf("%s %s no longer exists in Linear", kind, shortID(id))
		}
		return fmt.Sprintf("%s %q belongs to another team", kind, r.Name)
	}

	var problems []string
	lm := byID(labels)
	var badLabels []string
	for _, id := range p.MatchLabels {
		if r, ok := lm[id]; !usable(r, ok) {
			badLabels = append(badLabels, why("match label", id, r, ok))
		}
	}
	if len(badLabels) > 0 && (p.MatchMode == "all" || len(badLabels) == len(p.MatchLabels)) {
		problems = append(problems, badLabels...)
	}
	sm := byID(states)
	var badStates []string
	for _, id := range p.StateIDs {
		if r, ok := sm[id]; !usable(r, ok) {
			badStates = append(badStates, why("state", id, r, ok))
		}
	}
	if len(badStates) > 0 && len(badStates) == len(p.StateIDs) {
		problems = append(problems, badStates...)
	}
	// Not a filter clause, but the label-mode dedup: a spawn flips the issue
	// onto this label, and a flip naming a dead label fails AFTER the agent is
	// already running. Reported here so it is fixed in the same edit as the
	// match labels it almost always moved with.
	if id := p.OnSentSetLabel; id != "" {
		if r, ok := lm[id]; !usable(r, ok) {
			problems = append(problems, why("on-sent label", id, r, ok))
		}
	}
	return slices.Clip(problems)
}

// refError renders the problems as the poll's LastError, naming the fix.
func refError(problems []string) string {
	if len(problems) == 0 {
		return ""
	}
	return "filter can never match: " + strings.Join(problems, "; ") +
		" — re-pick it in the project's filter settings"
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8] + "…"
	}
	return id
}
