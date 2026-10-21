package linear

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGoalOwnershipRelationsReachEveryIssueRead(t *testing.T) {
	for _, method := range []string{"matching", "detail", "goal"} {
		t.Run(method, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				req := decodeRequest(t, r)
				if !strings.Contains(req.Query, "inverseRelations(first:50)") || !strings.Contains(req.Query, "relatedIssue{ id description }") || !strings.Contains(req.Query, "pageInfo{ hasNextPage }") {
					t.Error("ownership query omitted relation text or truncation")
				}
				incoming := map[string]any{"nodes": []any{
					map[string]any{"type": "related", "issue": map[string]string{"id": "goal-parent", "description": "<!-- lola-goal:external:parent -->"}},
					map[string]any{"type": "blocks", "issue": map[string]string{"id": "blocker", "description": "not membership"}},
				}, "pageInfo": map[string]bool{"hasNextPage": false}}
				outgoing := map[string]any{"nodes": []any{map[string]any{"type": "related", "relatedIssue": map[string]string{"id": "related", "description": "Other related work"}}}, "pageInfo": map[string]bool{"hasNextPage": true}}
				node := map[string]any{"id": "issue", "identifier": "N-42", "goalIncoming": incoming, "inverseRelations": incoming, "goalOutgoing": outgoing}
				var data any = map[string]any{"issue": node}
				if method != "detail" {
					data = map[string]any{"issues": map[string]any{"nodes": []any{node}, "pageInfo": map[string]bool{"hasNextPage": false}}}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			}))
			defer srv.Close()
			c, _ := fastClient(srv)
			var got IssueRelations
			switch method {
			case "matching":
				issues, err := c.MatchingIssues(context.Background(), basePoll(), "", "")
				if err != nil {
					t.Fatal(err)
				}
				if len(issues) != 1 || len(issues[0].BlockedBy) != 1 {
					t.Fatal("lost issue/blocker facts")
				}
				got = issues[0].IssueRelations
			case "detail":
				i, err := c.IssueDetail(context.Background(), "N-42")
				if err != nil {
					t.Fatal(err)
				}
				got = i.IssueRelations
			case "goal":
				i, err := c.LookupGoalIssue(context.Background(), "issue")
				if err != nil || i == nil {
					t.Fatal(err)
				}
				got = i.IssueRelations
			}
			if !got.RelationsTruncated || len(got.RelatedIssues) != 2 || got.RelatedIssues[0].ID != "goal-parent" || !strings.Contains(got.RelatedIssues[0].Description, "lola-goal:") || got.RelatedIssues[1].ID != "related" {
				t.Fatalf("lost ownership facts: %+v", got)
			}
		})
	}
}

func TestGoalAdoptionCreatesOnlyReservedRelatedLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		in, _ := req.Variables["input"].(map[string]any)
		if !strings.Contains(req.Query, "issueRelationCreate") || len(in) != 4 || in["id"] != "reserved" || in["issueId"] != "goal-parent" || in["relatedIssueId"] != "existing-task" || in["type"] != "related" {
			t.Error("wrong adoption mutation", req)
		}
		_, _ = w.Write([]byte(`{"data":{"issueRelationCreate":{"success":true}}}`))
	}))
	defer srv.Close()
	c, _ := fastClient(srv)
	if err := c.CreateGoalRelation(context.Background(), GoalRelation{ID: "reserved", IssueID: "goal-parent", RelatedIssueID: "existing-task", Type: "related"}); err != nil {
		t.Fatal(err)
	}
}
