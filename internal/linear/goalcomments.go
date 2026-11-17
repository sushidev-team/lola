package linear

import (
	"context"
	"errors"
)

// GoalCommentAPI keeps narration separate from execution bindings. UUID-backed
// creates can recover a lost response without searching or duplicating comments.
type GoalCommentAPI interface {
	LookupGoalComment(context.Context, string) (*GoalComment, error)
	CreateGoalComment(context.Context, GoalComment) error
}
type GoalComment struct{ ID, IssueID, Body, ArchivedAt string }

func (c *Client) LookupGoalComment(ctx context.Context, id string) (*GoalComment, error) {
	const q = `query($id:ID!){comments(filter:{id:{eq:$id}},first:1,includeArchived:true){nodes{id body archivedAt issue{id}}}}`
	var r struct {
		Comments struct {
			Nodes []struct {
				ID, Body, ArchivedAt string
				Issue                struct{ ID string }
			}
		}
	}
	if err := c.do(ctx, q, map[string]any{"id": id}, &r); err != nil {
		return nil, err
	}
	if len(r.Comments.Nodes) == 0 {
		return nil, nil
	}
	n := r.Comments.Nodes[0]
	return &GoalComment{ID: n.ID, IssueID: n.Issue.ID, Body: n.Body, ArchivedAt: n.ArchivedAt}, nil
}
func (c *Client) CreateGoalComment(ctx context.Context, in GoalComment) error {
	if in.ID == "" || in.IssueID == "" || in.Body == "" || len(in.Body) > 16000 {
		return errors.New("goal comment requires reserved identity, issue and bounded body")
	}
	const q = `mutation($input:CommentCreateInput!){commentCreate(input:$input){success}}`
	var r struct{ CommentCreate struct{ Success bool } }
	if err := c.do(ctx, q, map[string]any{"input": map[string]any{"id": in.ID, "issueId": in.IssueID, "body": in.Body, "doNotSubscribeToIssue": true}}, &r); err != nil {
		return err
	}
	if !r.CommentCreate.Success {
		return errors.New("linear: goal commentCreate reported success=false")
	}
	return nil
}
func (f *Fake) LookupGoalComment(ctx context.Context, id string) (*GoalComment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("LookupGoalComment", id); err != nil {
		return nil, err
	}
	for _, c := range f.GoalComments {
		if c.ID == id {
			copy := c
			return &copy, nil
		}
	}
	return nil, nil
}
func (f *Fake) CreateGoalComment(ctx context.Context, in GoalComment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("CreateGoalComment", in); err != nil {
		return err
	}
	for _, c := range f.GoalComments {
		if c.ID == in.ID {
			return errors.New("duplicate goal comment")
		}
	}
	f.GoalComments = append(f.GoalComments, in)
	return nil
}

var _ GoalCommentAPI = (*Client)(nil)
var _ GoalCommentAPI = (*Fake)(nil)
