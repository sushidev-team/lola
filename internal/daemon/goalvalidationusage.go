package daemon

import (
	"context"
	"maps"
	"path/filepath"
	"slices"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalcheck"
	"github.com/sushidev-team/lola/internal/goalwork"
)

type goalUsageData struct {
	goal.UsageReport
	Validation goalcheck.UsageReport `json:"validation"`
}

// Validation receipts already persist ownership, input policy and timestamps.
// Read those records on an explicit usage request instead of duplicating their
// history in the goal store or querying a live container from a client action.
func (d *Daemon) goalUsageReport(ctx context.Context, g goal.Goal) goalUsageData {
	out := goalUsageData{UsageReport: g.UsageReport()}
	var refs []goalcheck.UsageRef
	if e := g.Execution; e != nil {
		for _, operation := range goalVerificationOperations(g) {
			for _, spec := range e.Policy.Checks {
				refs = append(refs, goalcheck.UsageRef{ID: verificationID(operation, "check", spec.ID), Operation: operation, Spec: spec})
			}
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	runner := goalcheck.Runner{Workspaces: &goalwork.Manager{Root: filepath.Join(d.home, "goals")}}
	policy := ""
	if g.Execution != nil {
		policy = g.Execution.PolicyDigest
	}
	out.Validation = runner.Usage(cctx, g.ID, policy, refs)
	return out
}

func goalVerificationOperations(g goal.Goal) []string {
	ids := map[string]bool{}
	add := func(id string) {
		if id != "" {
			ids[id] = true
		}
	}
	for _, r := range g.BriefRevisions {
		for _, f := range r.ArchivedFinals {
			add(f.ID)
		}
	}
	if e := g.Execution; e != nil {
		for id := range e.Integrations {
			add(id)
		}
		if e.Final != nil {
			add(e.Final.ID)
		}
		for _, f := range e.FinalHistory {
			add(f.ID)
		}
		for _, r := range e.Refreshes {
			add(r.Round.ID)
			add(r.Final.ID)
			for _, h := range r.History {
				add(h.ID)
			}
			for _, f := range r.FinalHistory {
				add(f.ID)
			}
			for _, c := range r.SupersededTargets {
				add(c.Round.ID)
				for _, h := range c.History {
					add(h.ID)
				}
			}
		}
	}
	return slices.Sorted(maps.Keys(ids))
}
