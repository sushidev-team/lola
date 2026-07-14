package linear

import "context"

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
