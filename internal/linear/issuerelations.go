package linear

// IssueRelations exposes related issue text as facts. The daemon decides whether
// a relation represents goal ownership; a truncated response cannot prove absence.
type IssueRelations struct {
	RelatedIssues      []RelatedIssue
	BlockingIssues     []Blocker
	RelationsTruncated bool
}
type RelatedIssue struct{ ID, Description string }

type relationIssue struct {
	ID, Identifier, Description string
	State                       *struct{ Type string }
}

type relationPage struct {
	Nodes []struct {
		Type                string
		Issue, RelatedIssue *relationIssue
	}
	PageInfo struct{ HasNextPage bool }
}

const relatedOutgoingFields = `
 goalOutgoing: relations(first:50){ nodes{ type relatedIssue{ id description } } pageInfo{ hasNextPage } }
`
const relatedIssueFields = `
 goalIncoming: inverseRelations(first:50){ nodes{ type issue{ id identifier description state{ type } } } pageInfo{ hasNextPage } }
` + relatedOutgoingFields

func relatedIssues(incoming, outgoing relationPage) IssueRelations {
	out := IssueRelations{RelationsTruncated: incoming.PageInfo.HasNextPage || outgoing.PageInfo.HasNextPage}
	for _, node := range incoming.Nodes {
		if node.Type != "blocks" || node.Issue == nil {
			continue
		}
		blocker := Blocker{ID: node.Issue.ID, Identifier: node.Issue.Identifier}
		if node.Issue.State != nil {
			blocker.StateType = node.Issue.State.Type
		}
		out.BlockingIssues = append(out.BlockingIssues, blocker)
	}
	for _, page := range []relationPage{incoming, outgoing} {
		for _, node := range page.Nodes {
			if node.Type != "related" {
				continue
			}
			for _, issue := range []*relationIssue{node.Issue, node.RelatedIssue} {
				if issue != nil {
					out.RelatedIssues = append(out.RelatedIssues, RelatedIssue{ID: issue.ID, Description: issue.Description})
				}
			}
		}
	}
	return out
}
