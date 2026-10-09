package linear

import (
	"context"

	"github.com/sushidev-team/lola/internal/config"
)

func (c *Client) Viewer(ctx context.Context) (User, error) {
	var r struct{ Viewer User }
	err := c.do(ctx, `{ viewer { id name email } }`, nil, &r)
	return r.Viewer, err
}

func (c *Client) Teams(ctx context.Context) ([]Team, error) {
	var r struct{ Teams struct{ Nodes []Team } }
	err := c.do(ctx, `{ teams { nodes { id key name } } }`, nil, &r)
	return r.Teams.Nodes, err
}

func (c *Client) Projects(ctx context.Context, teamID string) ([]Project, error) {
	const q = `query($t:String!){ team(id:$t){ projects{ nodes{ id name state } } } }`
	var r struct {
		Team struct{ Projects struct{ Nodes []Project } }
	}
	err := c.do(ctx, q, map[string]any{"t": teamID}, &r)
	return r.Team.Projects.Nodes, err
}

func (c *Client) Cycles(ctx context.Context, teamID string) (*Cycle, []Cycle, error) {
	const q = `query($t:String!){ team(id:$t){
		activeCycle{ id number name }
		cycles(first:20){ nodes{ id number name } } } }`
	var r struct {
		Team struct {
			ActiveCycle *Cycle
			Cycles      struct{ Nodes []Cycle }
		}
	}
	err := c.do(ctx, q, map[string]any{"t": teamID}, &r)
	return r.Team.ActiveCycle, r.Team.Cycles.Nodes, err
}

func (c *Client) States(ctx context.Context, teamID string) ([]State, error) {
	const q = `query($t:String!){ team(id:$t){ states{ nodes{ id name type position } } } }`
	var r struct {
		Team struct{ States struct{ Nodes []State } }
	}
	err := c.do(ctx, q, map[string]any{"t": teamID}, &r)
	return r.Team.States.Nodes, err
}

func (c *Client) Labels(ctx context.Context, teamID string) ([]Label, error) {
	const q = `query($t:String!){ team(id:$t){ labels{ nodes{ id name color parent{ id name } } } } }`
	var r struct {
		Team struct{ Labels struct{ Nodes []Label } }
	}
	err := c.do(ctx, q, map[string]any{"t": teamID}, &r)
	return r.Team.Labels.Nodes, err
}

// WorkspaceLabels returns the ORGANISATION-level labels: those with no team,
// which therefore exist across every team in the workspace. These are the
// labels a shared [defaults] setting should use — a team-scoped label cannot
// match issues in another team.
//
// Linear models this as IssueLabel.team being null, so the filter asks for
// exactly that rather than fetching everything and filtering client-side.
func (c *Client) WorkspaceLabels(ctx context.Context) ([]Label, error) {
	const q = `query($after:String){
		issueLabels(filter:{team:{null:true}}, first:250, after:$after){
			nodes{ id name color parent{ id name } }
			pageInfo{ hasNextPage endCursor } } }`

	var (
		out   []Label
		after any // nil on first page -> GraphQL null
	)
	for {
		var r struct {
			IssueLabels struct {
				Nodes    []Label
				PageInfo struct {
					HasNextPage bool
					EndCursor   string
				}
			}
		}
		if err := c.do(ctx, q, map[string]any{"after": after}, &r); err != nil {
			return nil, err
		}
		out = append(out, r.IssueLabels.Nodes...)
		if !r.IssueLabels.PageInfo.HasNextPage {
			return out, nil
		}
		after = r.IssueLabels.PageInfo.EndCursor
	}
}

// FilterRefs looks up the given label and state IDs in ONE query. Archived
// labels are excluded on purpose: an archived label no longer appears on the
// issue picker, so a filter naming one is as stale as one naming a deleted
// label. Empty inputs skip their half of the query.
func (c *Client) FilterRefs(ctx context.Context, labelIDs, stateIDs []string) (labels, states []Ref, err error) {
	if len(labelIDs) == 0 && len(stateIDs) == 0 {
		return nil, nil, nil
	}
	const q = `query($l:[ID!], $s:[ID!]){
		issueLabels(filter:{id:{in:$l}}, first:250){ nodes{ id name team{ id } } }
		workflowStates(filter:{id:{in:$s}}, first:250){ nodes{ id name team{ id } } } }`
	type node struct {
		ID, Name string
		Team     *struct{ ID string }
	}
	var r struct {
		IssueLabels    struct{ Nodes []node }
		WorkflowStates struct{ Nodes []node }
	}
	// An empty `in` list matches nothing, which is exactly "nothing to check".
	l, s := labelIDs, stateIDs
	if l == nil {
		l = []string{}
	}
	if s == nil {
		s = []string{}
	}
	if err := c.do(ctx, q, map[string]any{"l": l, "s": s}, &r); err != nil {
		return nil, nil, err
	}
	conv := func(ns []node) []Ref {
		out := make([]Ref, 0, len(ns))
		for _, n := range ns {
			ref := Ref{ID: n.ID, Name: n.Name}
			if n.Team != nil {
				ref.TeamID = n.Team.ID
			}
			out = append(out, ref)
		}
		return out
	}
	return conv(r.IssueLabels.Nodes), conv(r.WorkflowStates.Nodes), nil
}

func (c *Client) Members(ctx context.Context, teamID string) ([]User, error) {
	const q = `query($t:String!){ team(id:$t){ members{ nodes{ id name email active } } } }`
	var r struct {
		Team struct{ Members struct{ Nodes []User } }
	}
	err := c.do(ctx, q, map[string]any{"t": teamID}, &r)
	return r.Team.Members.Nodes, err
}

// MatchingIssues runs the per-tick issues query for one poll, paginating
// until pageInfo.hasNextPage is false. The filter is built via
// BuildIssueFilter and passed as a variable — never string-interpolated.
func (c *Client) MatchingIssues(ctx context.Context, p config.Project, activeCycleID, viewerID string) ([]Issue, error) {
	// The state/assignee/estimate/updatedAt fields are for the pickers (a human
	// choosing an issue); dispatch ignores them. They ride the SAME query because
	// they are plain scalars on a node already being fetched — a second round trip
	// per tick would cost far more than they do.
	const q = `query($filter: IssueFilter, $after: String){
		issues(filter:$filter, first:100, after:$after){
			nodes{ id identifier title branchName priority createdAt updatedAt estimate
				state{ name type } assignee{ displayName name }
				labels{ nodes{ id name } } }
			pageInfo{ hasNextPage endCursor } } }`

	filter := BuildIssueFilter(p, activeCycleID, viewerID)

	var (
		out   []Issue
		after any // nil on first page -> GraphQL null
	)
	for {
		var r struct {
			Issues struct {
				Nodes []struct {
					ID         string
					Identifier string
					Title      string
					BranchName string
					Priority   float64
					CreatedAt  string
					UpdatedAt  string
					Estimate   *float64
					State      *struct{ Name, Type string }
					Assignee   *struct{ DisplayName, Name string }
					Labels     struct{ Nodes []struct{ ID, Name string } }
				}
				PageInfo struct {
					HasNextPage bool
					EndCursor   string
				}
			}
		}
		vars := map[string]any{"filter": filter, "after": after}
		if err := c.do(ctx, q, vars, &r); err != nil {
			return nil, err
		}
		for _, n := range r.Issues.Nodes {
			iss := Issue{
				ID:         n.ID,
				Identifier: n.Identifier,
				Title:      n.Title,
				BranchName: n.BranchName,
				Priority:   n.Priority,
				CreatedAt:  n.CreatedAt,
				UpdatedAt:  n.UpdatedAt,
			}
			if n.State != nil {
				iss.StateName, iss.StateType = n.State.Name, n.State.Type
			}
			if n.Assignee != nil {
				if iss.Assignee = n.Assignee.DisplayName; iss.Assignee == "" {
					iss.Assignee = n.Assignee.Name
				}
			}
			if n.Estimate != nil {
				iss.Estimate = *n.Estimate
			}
			for _, l := range n.Labels.Nodes {
				iss.LabelIDs = append(iss.LabelIDs, l.ID)
				iss.LabelNames = append(iss.LabelNames, l.Name)
			}
			out = append(out, iss)
		}
		if !r.Issues.PageInfo.HasNextPage {
			return out, nil
		}
		after = r.Issues.PageInfo.EndCursor
	}
}

// Compile-time check that the real client satisfies the testing seam.
var _ API = (*Client)(nil)
