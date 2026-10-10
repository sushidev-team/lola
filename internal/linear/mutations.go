package linear

import (
	"context"
	"fmt"
)

// IssueTitle fetches a single issue's title by its UUID. Used to backfill the
// title of sessions spawned before the field was recorded on the session.
func (c *Client) IssueTitle(ctx context.Context, issueUUID string) (string, error) {
	const q = `query($id:String!){ issue(id:$id){ title } }`
	var r struct {
		Issue struct{ Title string }
	}
	if err := c.do(ctx, q, map[string]any{"id": issueUUID}, &r); err != nil {
		return "", err
	}
	return r.Issue.Title, nil
}

func (c *Client) IssueLabelIDs(ctx context.Context, issueUUID string) ([]string, error) {
	const q = `query($id:String!){ issue(id:$id){ labels{ nodes{ id } } } }`
	var r struct {
		Issue struct {
			Labels struct{ Nodes []struct{ ID string } }
		}
	}
	if err := c.do(ctx, q, map[string]any{"id": issueUUID}, &r); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(r.Issue.Labels.Nodes))
	for _, n := range r.Issue.Labels.Nodes {
		ids = append(ids, n.ID)
	}
	return ids, nil
}

// SetIssueLabels sends the FULL array. Linear has no add-label mutation.
func (c *Client) SetIssueLabels(ctx context.Context, issueUUID string, labelIDs []string) error {
	const m = `mutation($id:String!,$labelIds:[String!]!){
		issueUpdate(id:$id, input:{labelIds:$labelIds}){ success } }`
	var r struct{ IssueUpdate struct{ Success bool } }
	return c.do(ctx, m, map[string]any{"id": issueUUID, "labelIds": labelIDs}, &r)
}

// CreateComment posts a comment on the issue. Lola narrates agent progress
// through these as the observer crosses reaction transitions.
func (c *Client) CreateComment(ctx context.Context, issueUUID, body string) error {
	const m = `mutation($id:String!,$body:String!){ commentCreate(input:{issueId:$id, body:$body}){ success } }`
	var r struct{ CommentCreate struct{ Success bool } }
	if err := c.do(ctx, m, map[string]any{"id": issueUUID, "body": body}, &r); err != nil {
		return err
	}
	if !r.CommentCreate.Success {
		return fmt.Errorf("linear: commentCreate reported success=false for issue %s", issueUUID)
	}
	return nil
}

// SetIssueState moves the issue to a workflow state. Moving out of a poll's
// state_ids is how state-based dedup stops the issue from re-matching.
func (c *Client) SetIssueState(ctx context.Context, issueUUID, stateID string) error {
	const m = `mutation($id:String!,$stateId:String!){ issueUpdate(id:$id, input:{stateId:$stateId}){ success } }`
	var r struct{ IssueUpdate struct{ Success bool } }
	if err := c.do(ctx, m, map[string]any{"id": issueUUID, "stateId": stateID}, &r); err != nil {
		return err
	}
	if !r.IssueUpdate.Success {
		return fmt.Errorf("linear: issueUpdate reported success=false for issue %s", issueUUID)
	}
	return nil
}

// IssueDetail reads one issue for the planning pass. Linear's issue(id:)
// accepts the identifier (FE-231) as well as the UUID.
func (c *Client) IssueDetail(ctx context.Context, idOrIdentifier string) (IssueDetail, error) {
	const q = `query($id:String!){ issue(id:$id){
		id identifier title description
		team{ id } project{ id } cycle{ id } state{ id } assignee{ id }
		labels{ nodes{ id } }
		children(first:50){ nodes{ id } } } }`
	type idRef = *struct{ ID string }
	var r struct {
		Issue struct {
			ID, Identifier, Title string
			Description           *string
			Team, Project, Cycle  idRef
			State, Assignee       idRef
			Labels                struct{ Nodes []struct{ ID string } }
			Children              struct{ Nodes []struct{ ID string } }
		}
	}
	if err := c.do(ctx, q, map[string]any{"id": idOrIdentifier}, &r); err != nil {
		return IssueDetail{}, err
	}
	is := r.Issue
	ref := func(p idRef) string {
		if p == nil {
			return ""
		}
		return p.ID
	}
	out := IssueDetail{
		ID: is.ID, Identifier: is.Identifier, Title: is.Title,
		TeamID: ref(is.Team), ProjectID: ref(is.Project), CycleID: ref(is.Cycle),
		StateID: ref(is.State), AssigneeID: ref(is.Assignee),
		Children: len(is.Children.Nodes),
	}
	if is.Description != nil {
		out.Description = *is.Description
	}
	for _, l := range is.Labels.Nodes {
		out.LabelIDs = append(out.LabelIDs, l.ID)
	}
	return out, nil
}

// CreateIssue creates one issue. Empty optional fields are left out of the
// input entirely so Linear applies the team's defaults instead of rejecting an
// empty id.
func (c *Client) CreateIssue(ctx context.Context, in IssueCreate) (string, string, error) {
	const m = `mutation($input:IssueCreateInput!){
		issueCreate(input:$input){ success issue{ id identifier } } }`
	input := map[string]any{"teamId": in.TeamID, "title": in.Title}
	for k, v := range map[string]string{
		"description": in.Description, "projectId": in.ProjectID, "cycleId": in.CycleID,
		"stateId": in.StateID, "assigneeId": in.AssigneeID, "parentId": in.ParentID,
	} {
		if v != "" {
			input[k] = v
		}
	}
	if len(in.LabelIDs) > 0 {
		input["labelIds"] = in.LabelIDs
	}
	var r struct {
		IssueCreate struct {
			Success bool
			Issue   *struct{ ID, Identifier string }
		}
	}
	if err := c.do(ctx, m, map[string]any{"input": input}, &r); err != nil {
		return "", "", err
	}
	if !r.IssueCreate.Success || r.IssueCreate.Issue == nil {
		return "", "", fmt.Errorf("linear: issueCreate reported success=false for %q", in.Title)
	}
	return r.IssueCreate.Issue.ID, r.IssueCreate.Issue.Identifier, nil
}

// CreateBlocksRelation records "blocker blocks blocked" — the relation Linear
// shows as "blocked by" on the dependent issue, and the one dispatch reads back
// through inverseRelations.
func (c *Client) CreateBlocksRelation(ctx context.Context, blockerUUID, blockedUUID string) error {
	const m = `mutation($a:String!,$b:String!){
		issueRelationCreate(input:{issueId:$a, relatedIssueId:$b, type:blocks}){ success } }`
	var r struct{ IssueRelationCreate struct{ Success bool } }
	if err := c.do(ctx, m, map[string]any{"a": blockerUUID, "b": blockedUUID}, &r); err != nil {
		return err
	}
	if !r.IssueRelationCreate.Success {
		return fmt.Errorf("linear: issueRelationCreate reported success=false (%s blocks %s)", blockerUUID, blockedUUID)
	}
	return nil
}
