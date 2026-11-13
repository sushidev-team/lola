package linear

import (
	"context"
	"fmt"
)

// GoalAPI extends the poller's API with recoverable create/read operations.
// All creates carry a caller-reserved UUID. A read returns nil only when an
// authoritative successful query found no resource; transport errors stay errors.
type GoalAPI interface {
	API
	LookupGoalIssue(context.Context, string) (*IssueDetail, error)
	LookupGoalMilestone(context.Context, string) (*GoalMilestone, error)
	CreateGoalMilestone(context.Context, GoalMilestone) error
	LookupGoalRelation(context.Context, string, string) (*GoalRelation, error)
	FindGoalRelation(context.Context, string, string, string) (*GoalRelation, error)
	CreateGoalRelation(context.Context, GoalRelation) error
}

type GoalMilestone struct {
	ID, ProjectID, Name, Description, ArchivedAt string
}

type GoalRelation struct {
	ID, IssueID, RelatedIssueID, Type string
}

func (c *Client) LookupGoalIssue(ctx context.Context, id string) (*IssueDetail, error) {
	const q = `query($id:ID!){ issues(filter:{id:{eq:$id}},first:1,includeArchived:true){ nodes{
		id identifier title description archivedAt team{id} project{id} parent{id}
		projectMilestone{id} state{id type} labels{nodes{id}} ` + relatedIssueFields + ` } } }`
	var r struct {
		Issues struct {
			Nodes []struct {
				GoalIncoming, GoalOutgoing                     relationPage
				ID, Identifier, Title, Description, ArchivedAt string
				Team, Project, Parent, ProjectMilestone        struct{ ID string }
				State                                          struct{ ID, Type string }
				Labels                                         struct{ Nodes []struct{ ID string } }
			}
		}
	}
	if err := c.do(ctx, q, map[string]any{"id": id}, &r); err != nil {
		return nil, err
	}
	if len(r.Issues.Nodes) == 0 {
		return nil, nil
	}
	n := r.Issues.Nodes[0]
	d := &IssueDetail{ID: n.ID, Identifier: n.Identifier, Title: n.Title, Description: n.Description,
		IssueRelations: relatedIssues(n.GoalIncoming, n.GoalOutgoing),
		TeamID:         n.Team.ID, ProjectID: n.Project.ID, ParentID: n.Parent.ID, MilestoneID: n.ProjectMilestone.ID,
		StateID: n.State.ID, StateType: n.State.Type, ArchivedAt: n.ArchivedAt}
	for _, l := range n.Labels.Nodes {
		d.LabelIDs = append(d.LabelIDs, l.ID)
	}
	return d, nil
}

func (c *Client) LookupGoalMilestone(ctx context.Context, id string) (*GoalMilestone, error) {
	const q = `query($id:ID!){ projectMilestones(filter:{id:{eq:$id}},first:1,includeArchived:true){
		nodes{id name description archivedAt project{id}} } }`
	var r struct {
		ProjectMilestones struct {
			Nodes []struct {
				ID, Name, Description, ArchivedAt string
				Project                           struct{ ID string }
			}
		}
	}
	if err := c.do(ctx, q, map[string]any{"id": id}, &r); err != nil {
		return nil, err
	}
	if len(r.ProjectMilestones.Nodes) == 0 {
		return nil, nil
	}
	n := r.ProjectMilestones.Nodes[0]
	return &GoalMilestone{ID: n.ID, Name: n.Name, Description: n.Description, ProjectID: n.Project.ID, ArchivedAt: n.ArchivedAt}, nil
}

func (c *Client) CreateGoalMilestone(ctx context.Context, in GoalMilestone) error {
	const q = `mutation($input:ProjectMilestoneCreateInput!){ projectMilestoneCreate(input:$input){success} }`
	if in.ID == "" || in.ProjectID == "" {
		return fmt.Errorf("milestone requires reserved ID and existing project")
	}
	var r struct{ ProjectMilestoneCreate struct{ Success bool } }
	input := map[string]any{"id": in.ID, "projectId": in.ProjectID, "name": in.Name, "description": in.Description}
	if err := c.do(ctx, q, map[string]any{"input": input}, &r); err != nil {
		return err
	}
	if !r.ProjectMilestoneCreate.Success {
		return fmt.Errorf("linear: projectMilestoneCreate reported success=false")
	}
	return nil
}

func (c *Client) LookupGoalRelation(ctx context.Context, issueID, id string) (*GoalRelation, error) {
	return c.findGoalRelation(ctx, issueID, func(r GoalRelation) bool { return r.ID == id })
}

func (c *Client) FindGoalRelation(ctx context.Context, issueID, relatedID, kind string) (*GoalRelation, error) {
	return c.findGoalRelation(ctx, issueID, func(r GoalRelation) bool {
		return r.IssueID == issueID && r.RelatedIssueID == relatedID && r.Type == kind
	})
}

func (c *Client) findGoalRelation(ctx context.Context, issueID string, match func(GoalRelation) bool) (*GoalRelation, error) {
	const q = `query($issue:String!,$after:String){ issue(id:$issue){ relations(first:100,after:$after){
		nodes{id type issue{id} relatedIssue{id}} pageInfo{hasNextPage endCursor} } } }`
	var after any
	for {
		var r struct {
			Issue struct {
				Relations struct {
					Nodes []struct {
						ID, Type            string
						Issue, RelatedIssue struct{ ID string }
					}
					PageInfo struct {
						HasNextPage bool
						EndCursor   string
					}
				}
			}
		}
		if err := c.do(ctx, q, map[string]any{"issue": issueID, "after": after}, &r); err != nil {
			return nil, err
		}
		for _, n := range r.Issue.Relations.Nodes {
			candidate := GoalRelation{ID: n.ID, Type: n.Type, IssueID: n.Issue.ID, RelatedIssueID: n.RelatedIssue.ID}
			if match(candidate) {
				return &candidate, nil
			}
		}
		page := r.Issue.Relations.PageInfo
		if !page.HasNextPage {
			return nil, nil
		}
		if page.EndCursor == "" || page.EndCursor == after {
			return nil, fmt.Errorf("linear: relation pagination did not advance")
		}
		after = page.EndCursor
	}
}

func (c *Client) CreateGoalRelation(ctx context.Context, in GoalRelation) error {
	const q = `mutation($input:IssueRelationCreateInput!){issueRelationCreate(input:$input){success}}`
	if in.ID == "" || in.IssueID == "" || in.RelatedIssueID == "" || (in.Type != "blocks" && in.Type != "related") {
		return fmt.Errorf("goal relation requires reserved ID, two issues and blocks or related type")
	}
	var r struct{ IssueRelationCreate struct{ Success bool } }
	input := map[string]any{"id": in.ID, "issueId": in.IssueID, "relatedIssueId": in.RelatedIssueID, "type": in.Type}
	if err := c.do(ctx, q, map[string]any{"input": input}, &r); err != nil {
		return err
	}
	if !r.IssueRelationCreate.Success {
		return fmt.Errorf("linear: issueRelationCreate reported success=false")
	}
	return nil
}

var _ GoalAPI = (*Client)(nil)
