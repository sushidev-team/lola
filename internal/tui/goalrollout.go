package tui

import (
	"fmt"

	"github.com/sushidev-team/lola/internal/goalpolicy"
)

func memberIDs(p goalpolicy.Policy) []string {
	var ids []string
	for _, m := range p.Members {
		ids = append(ids, m.ID)
	}
	return ids
}

func goalRolloutLines(r *goalpolicy.Rollout) []string {
	if r == nil {
		return nil
	}
	out := []string{"", "Reviewed rollout contract", "Preserve compatibility between merges. Merges and deployment stay manual."}
	for _, requirement := range r.Compatibility {
		out = append(out, "  - "+goalText(requirement))
	}
	out = append(out, "Human merge order")
	for i, id := range r.MergeOrder {
		out = append(out, fmt.Sprintf("  %d. %s", i+1, goalText(id)))
	}
	return append(out, "Skip unchanged repositories; confirm intermediate compatibility before merging.")
}
