package goalpublish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type evidenceComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	URL  string `json:"html_url"`
}

func revisionMarker(goal, member, operation string) string {
	return "<!-- lola-goal-evidence:" + goal + ":" + member + ":" + operation + " -->"
}

// revisionEvidence appends the newly verified combination without editing a
// human's PR description. The operation marker and exact body reconcile a lost
// POST response. Deleted/edited acknowledged evidence holds, never overwrites.
func (p *Publisher) revisionEvidence(ctx context.Context, want Request, id string, member Member, pr pullRequest) (int64, error) {
	marker := revisionMarker(want.Goal, id, want.Operation)
	if member.EvidenceComment == 0 && strings.Contains(pr.Body, marker) {
		return 0, nil
	} // created with this revision's description
	body := "Refreshed goal verification. This evidence supersedes the previous repository combination; the original PR description and human edits are retained.\n\n" + want.Body + "\n\n" + marker
	endpoint := fmt.Sprintf("repos/%s/issues/%d/comments", member.Repo, member.Number)
	read := func() (int64, error) {
		var found int64
		seen := map[int64]bool{}
		for page := 1; page <= 10; page++ {
			b, err := p.gh(ctx, []string{"api", "--hostname", "github.com", "--method", "GET", endpoint, "-f", "per_page=100", "-f", fmt.Sprintf("page=%d", page)}, nil)
			if err != nil {
				return 0, err
			}
			var comments []evidenceComment
			if err := json.Unmarshal(b, &comments); err != nil {
				return 0, err
			}
			if len(comments) > 100 {
				return 0, errors.New("invalid goal evidence comment page")
			}
			for _, c := range comments {
				if c.ID < 1 || seen[c.ID] {
					return 0, errors.New("goal discussion pagination changed; retry before posting evidence")
				}
				seen[c.ID] = true
				if (member.EvidenceComment != 0 && c.ID == member.EvidenceComment) || strings.Contains(c.Body, marker) {
					if c.ID < 1 || found != 0 || c.Body != body || c.URL != fmt.Sprintf("%s#issuecomment-%d", pr.URL, c.ID) || (member.EvidenceComment != 0 && c.ID != member.EvidenceComment) {
						return 0, errors.New("goal evidence comment changed or duplicated; preserving human edits")
					}
					found = c.ID
				}
			}
			if len(comments) < 100 {
				break
			}
			if page == 10 {
				return 0, errors.New("goal evidence comments are truncated; preserving remote discussion")
			}
		}
		if found == 0 && member.EvidenceComment != 0 {
			return 0, errors.New("acknowledged goal evidence comment disappeared; refusing to recreate it")
		}
		return found, nil
	}
	found, err := read()
	if err != nil || found != 0 {
		return found, err
	}
	if err := p.effect(ctx, func() error {
		b, _ := json.Marshal(map[string]string{"body": body})
		_, err := p.gh(ctx, []string{"api", "--hostname", "github.com", "--method", "POST", endpoint, "--input", "-"}, b)
		return err
	}); err != nil {
		return 0, err
	}
	found, err = read()
	if err == nil && found == 0 {
		err = errors.New("goal evidence comment not yet observable; retaining publication intent")
	}
	return found, err
}
