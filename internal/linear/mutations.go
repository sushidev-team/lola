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
