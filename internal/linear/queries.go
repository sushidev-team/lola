package linear

import (
	"context"

	"github.com/you/aop/internal/config"
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
func (c *Client) MatchingIssues(ctx context.Context, p config.Poll, activeCycleID, viewerID string) ([]Issue, error) {
	const q = `query($filter: IssueFilter, $after: String){
		issues(filter:$filter, first:100, after:$after){
			nodes{ id identifier title branchName priority createdAt labels{ nodes{ id } } }
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
					Labels     struct{ Nodes []struct{ ID string } }
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
			}
			for _, l := range n.Labels.Nodes {
				iss.LabelIDs = append(iss.LabelIDs, l.ID)
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
