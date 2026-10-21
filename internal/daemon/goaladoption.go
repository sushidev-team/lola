package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/linear"
)

// createGoal holds goalMu while capturing and saving. Ordinary dispatch takes
// the same lock for its final ownership check and in-flight claim.
func (d *Daemon) captureGoalAdoptions(ctx context.Context, api linear.GoalAPI, g *goal.Goal) error {
	for i := range g.Tasks {
		t := &g.Tasks[i]
		if t.Adoption == nil {
			continue
		}
		if d.goals.OwnsIssue(t.Issue.ID) || d.inflight.Has(t.Issue.ID) {
			return errors.New("existing issue already reserved or being worked on")
		}
		detail, err := api.LookupGoalIssue(ctx, t.Issue.ID)
		if err != nil {
			return err
		}
		if detail == nil {
			return errors.New("existing issue is unavailable; adoption never creates a replacement")
		}
		if linear.StateFinished(detail.StateType) {
			return errors.New("adopt an unfinished issue; completed/canceled work requires reconciliation")
		}
		for _, s := range d.sessions.Snapshot() {
			if (s.IssueUUID == detail.ID || s.Issue == detail.Identifier) && (nativeSessionPresent(s) || (s.PR != nil && s.PR.State == "OPEN")) {
				return errors.New("existing issue has a live session or open PR; finish it before adoption")
			}
		}
		t.Adoption.ParentID, t.Adoption.MilestoneID = detail.ParentID, detail.MilestoneID
		if err := validateAdoptedDetail(*g, *t, detail); err != nil {
			return err
		}
		t.Issue = goal.Binding{ID: detail.ID, Identifier: detail.Identifier, Confirmed: true}
	}
	// Existing prerequisite links keep their identities. Creating a second link
	// for the same pair may be rejected by Linear and would lose human ownership.
	for i, t := range g.Tasks {
		if t.Adoption == nil {
			continue
		}
		for j, dep := range t.BlockedBy {
			previous := g.Tasks[dep-1]
			if previous.Adoption == nil {
				continue
			}
			r, err := api.FindGoalRelation(ctx, previous.Issue.ID, t.Issue.ID, "blocks")
			if err != nil {
				return err
			}
			if r != nil {
				if r.ID == "" || r.IssueID != previous.Issue.ID || r.RelatedIssueID != t.Issue.ID || r.Type != "blocks" {
					return errors.New("existing dependency lookup returned different relation")
				}
				g.Tasks[i].Relations[j] = goal.Binding{ID: r.ID, Confirmed: true}
			}
		}
	}
	return nil
}

func validateAdoptedDetail(g goal.Goal, t goal.Task, detail *linear.IssueDetail) error {
	if detail == nil || t.Adoption == nil {
		return errors.New("adopted issue is missing; refusing to recreate it")
	}
	if detail.StateType == "completed" && !g.TaskSatisfied(t.ID) {
		return errors.New("adopted task marked completed before verified integration; reconcile before continuing")
	}
	labels, want := slices.Clone(detail.LabelIDs), slices.Clone(t.LabelIDs)
	slices.Sort(labels)
	slices.Sort(want)
	if detail.ID != t.Issue.ID || detail.TeamID != g.TeamID || detail.ProjectID != g.LinearProjectID || detail.Title != t.Title || detail.Description != t.Description || !slices.Equal(labels, want) || detail.ParentID != t.Adoption.ParentID || detail.MilestoneID != t.Adoption.MilestoneID || detail.ArchivedAt != "" || detail.StateType == "canceled" || goal.Marked(detail.Description) {
		return fmt.Errorf("adopted issue %s differs from its accepted text, labels or placement, or is archived/canceled; reconcile the human edit", t.Issue.ID)
	}
	if detail.RelationsTruncated {
		return errors.New("existing issue relations are incomplete; cannot establish exclusive goal ownership")
	}
	for _, link := range detail.RelatedIssues {
		if link.ID != g.Parent.ID && goal.Marked(link.Description) {
			return errors.New("existing issue is related to another goal")
		}
	}
	for _, blocker := range detail.BlockingIssues {
		if blocker.Finished() {
			continue
		}
		included := false
		for _, dep := range t.BlockedBy {
			if dep > 0 && dep <= len(g.Tasks) && g.Tasks[dep-1].Issue.ID == blocker.ID {
				included = true
			}
		}
		if !included {
			return fmt.Errorf("adopted issue %s has unfinished prerequisite %s outside its accepted dependency graph", t.Issue.ID, blocker.ID)
		}
	}
	return nil
}

func checkAdoptedIssue(ctx context.Context, api linear.GoalAPI, g goal.Goal, t goal.Task) error {
	detail, err := api.LookupGoalIssue(ctx, t.Issue.ID)
	if err != nil {
		return err
	}
	return validateAdoptedDetail(g, t, detail)
}

func adoptionRelation(g goal.Goal, t goal.Task) linear.GoalRelation {
	return linear.GoalRelation{ID: t.Adoption.Membership.ID, IssueID: g.Parent.ID, RelatedIssueID: t.Issue.ID, Type: "related"}
}

func ensureGoalRelation(ctx context.Context, api linear.GoalAPI, b goal.Binding, want linear.GoalRelation) (goal.Binding, error) {
	r, err := api.LookupGoalRelation(ctx, want.IssueID, want.ID)
	if err != nil {
		return b, err
	}
	if r == nil {
		if b.Confirmed {
			return b, errors.New("goal membership removed; refusing to recreate it")
		}
		createErr := api.CreateGoalRelation(ctx, want)
		r, err = api.LookupGoalRelation(ctx, want.IssueID, want.ID)
		if err != nil {
			return b, errors.Join(createErr, err)
		}
		if r == nil {
			return b, fmt.Errorf("goal membership creation unconfirmed: %v", createErr)
		}
	}
	if *r != want {
		return b, errors.New("goal membership changed")
	}
	b.Confirmed = true
	return b, nil
}

func (d *Daemon) claimOrdinaryIssue(id, identifier, description string, relations linear.IssueRelations) error {
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	if reason := d.goalHold(id, description, relations); reason != "" {
		return errors.New(reason)
	}
	if !d.inflight.Claim(id, identifier) {
		return fmt.Errorf("%s is already being worked on — check sessions", identifier)
	}
	return nil
}
