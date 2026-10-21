package linear

import (
	"context"
	"fmt"
	"slices"
)

func (f *Fake) LookupGoalIssue(ctx context.Context, id string) (*IssueDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("LookupGoalIssue", id); err != nil {
		return nil, err
	}
	for _, d := range f.Details {
		if d.ID == id {
			if stateID, ok := f.StateByIssue[id]; ok {
				d.StateID = stateID
				d.StateType = ""
				for _, s := range f.StatesByTeam[d.TeamID] {
					if s.ID == stateID {
						d.StateType = s.Type
					}
				}
			}
			d.LabelIDs = slices.Clone(d.LabelIDs)
			d.IssueRelations = f.relatedIssuesLocked(d.ID, d.IssueRelations)
			return &d, nil
		}
	}
	return nil, nil
}

func (f *Fake) LookupGoalMilestone(ctx context.Context, id string) (*GoalMilestone, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("LookupGoalMilestone", id); err != nil {
		return nil, err
	}
	for _, m := range f.Milestones {
		if m.ID == id {
			return &m, nil
		}
	}
	return nil, nil
}

func (f *Fake) CreateGoalMilestone(ctx context.Context, m GoalMilestone) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("CreateGoalMilestone", m); err != nil {
		return err
	}
	for _, old := range f.Milestones {
		if m.ID == old.ID {
			return fmt.Errorf("duplicate milestone ID")
		}
	}
	f.Milestones = append(f.Milestones, m)
	return nil
}

func (f *Fake) LookupGoalRelation(ctx context.Context, issueID, id string) (*GoalRelation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("LookupGoalRelation", issueID, id); err != nil {
		return nil, err
	}
	for _, r := range f.GoalRelations {
		if r.ID == id && r.IssueID == issueID {
			return &r, nil
		}
	}
	return nil, nil
}

func (f *Fake) CreateGoalRelation(ctx context.Context, r GoalRelation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("CreateGoalRelation", r); err != nil {
		return err
	}
	for _, old := range f.GoalRelations {
		if r.ID == old.ID {
			return fmt.Errorf("duplicate relation ID")
		}
	}
	f.GoalRelations = append(f.GoalRelations, r)
	return nil
}

func (f *Fake) FindGoalRelation(ctx context.Context, issueID, relatedID, kind string) (*GoalRelation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("FindGoalRelation", issueID, relatedID, kind); err != nil {
		return nil, err
	}
	for _, relation := range f.GoalRelations {
		if relation.IssueID == issueID && relation.RelatedIssueID == relatedID && relation.Type == kind {
			return &relation, nil
		}
	}
	return nil, nil
}

var _ GoalAPI = (*Fake)(nil)

func (f *Fake) relatedIssuesLocked(id string, current IssueRelations) IssueRelations {
	current.RelatedIssues = slices.Clone(current.RelatedIssues)
	current.BlockingIssues = slices.Clone(current.BlockingIssues)
	for _, relation := range f.GoalRelations {
		if relation.Type == "blocks" && relation.RelatedIssueID == id {
			for _, detail := range f.Details {
				if detail.ID == relation.IssueID {
					current.BlockingIssues = append(current.BlockingIssues, Blocker{ID: detail.ID, Identifier: detail.Identifier, StateType: detail.StateType})
				}
			}
		}
		if relation.Type != "related" {
			continue
		}
		other := ""
		if relation.IssueID == id {
			other = relation.RelatedIssueID
		} else if relation.RelatedIssueID == id {
			other = relation.IssueID
		}
		for _, detail := range f.Details {
			if detail.ID == other {
				current.RelatedIssues = append(current.RelatedIssues, RelatedIssue{ID: other, Description: detail.Description})
			}
		}
	}
	return current
}
