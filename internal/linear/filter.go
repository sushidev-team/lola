package linear

import "github.com/you/aop/internal/config"

// BuildIssueFilter constructs the Linear GraphQL IssueFilter for one poll.
// Exported so tests can assert filter construction without a live client.
//
// activeCycleID is the team's active cycle resolved at tick start (used only
// when cycle_mode=active); viewerID is the authenticated user's UUID (used
// only when assignee_mode=me).
func BuildIssueFilter(p config.Poll, activeCycleID, viewerID string) map[string]any {
	idEq := func(id string) map[string]any {
		return map[string]any{"id": map[string]any{"eq": id}}
	}

	f := map[string]any{"team": idEq(p.TeamID)}

	if p.ProjectID != "" {
		f["project"] = idEq(p.ProjectID)
	}

	switch p.CycleMode {
	case "active":
		f["cycle"] = idEq(activeCycleID)
	case "pinned":
		f["cycle"] = idEq(p.CycleID)
		// "none" / anything else: no cycle filter.
	}

	if len(p.StateIDs) > 0 {
		f["state"] = map[string]any{"id": map[string]any{"in": p.StateIDs}}
	}

	if len(p.MatchLabels) > 0 {
		if p.MatchMode == "all" {
			// IssueFilter ANDs sibling fields implicitly; the explicit "and"
			// array is combined with them, so per-label some-conditions here
			// require every trigger label to be present.
			conds := make([]map[string]any, 0, len(p.MatchLabels))
			for _, l := range p.MatchLabels {
				conds = append(conds, map[string]any{
					"labels": map[string]any{"some": idEq(l)},
				})
			}
			f["and"] = conds
		} else { // "any" (default)
			f["labels"] = map[string]any{
				"some": map[string]any{"id": map[string]any{"in": p.MatchLabels}},
			}
		}
	}

	switch p.AssigneeMode {
	case "me":
		f["assignee"] = idEq(viewerID)
	case "user":
		f["assignee"] = idEq(p.AssigneeUserID)
		// "anyone" / anything else: no assignee filter.
	}

	return f
}
