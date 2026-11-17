package linear

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGoalLinearCreateInputsAndLookup(t *testing.T) {
	var count int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		count++
		input, _ := req.Variables["input"].(map[string]any)
		switch count {
		case 1:
			if input["id"] != "reserved-issue" || input["projectMilestoneId"] != "milestone" || input["projectId"] != "project" {
				t.Errorf("issue identity/placement missing: %v", input)
			}
			fmt.Fprint(w, `{"data":{"issueCreate":{"success":true,"issue":{"id":"reserved-issue","identifier":"N-1"}}}}`)
		case 2:
			if !strings.Contains(req.Query, "includeArchived:true") || req.Variables["id"] != "reserved-issue" {
				t.Error("lookup must find archived bindings too")
			}
			fmt.Fprint(w, `{"data":{"issues":{"nodes":[{"id":"reserved-issue","identifier":"N-1","title":"Title","description":"marker","team":{"id":"team"},"project":{"id":"project"},"parent":{"id":"parent"},"projectMilestone":{"id":"milestone"},"state":{"id":"state","type":"canceled"},"labels":{"nodes":[{"id":"label"}]}}]}}}`)
		case 3:
			if input["id"] != "reserved-milestone" || input["projectId"] != "project" {
				t.Errorf("milestone identity missing: %v", input)
			}
			fmt.Fprint(w, `{"data":{"projectMilestoneCreate":{"success":true}}}`)
		case 4:
			fmt.Fprint(w, `{"data":{"projectMilestones":{"nodes":[{"id":"reserved-milestone","name":"Goal","description":"marker","project":{"id":"project"},"archivedAt":"2026-10-01"}]}}}`)
		case 5:
			if input["id"] != "reserved-relation" || input["issueId"] != "a" || input["relatedIssueId"] != "b" || input["type"] != "blocks" {
				t.Errorf("relation identity/direction missing: %v", input)
			}
			fmt.Fprint(w, `{"data":{"issueRelationCreate":{"success":true}}}`)
		case 6:
			fmt.Fprint(w, `{"data":{"issue":{"relations":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"next"}}}}}`)
		case 7:
			if req.Variables["after"] != "next" {
				t.Error("relation recovery must paginate")
			}
			fmt.Fprint(w, `{"data":{"issue":{"relations":{"nodes":[{"id":"reserved-relation","type":"blocks","issue":{"id":"a"},"relatedIssue":{"id":"b"}}],"pageInfo":{"hasNextPage":false}}}}}`)
		case 8:
			if req.Variables["issue"] != "a" {
				t.Error("pair lookup used wrong source")
			}
			fmt.Fprint(w, `{"data":{"issue":{"relations":{"nodes":[{"id":"unrelated","type":"related","issue":{"id":"a"},"relatedIssue":{"id":"b"}},{"id":"reserved-relation","type":"blocks","issue":{"id":"a"},"relatedIssue":{"id":"b"}}],"pageInfo":{"hasNextPage":false}}}}}`)
		default:
			t.Errorf("unexpected request %d", count)
		}
	}))
	defer srv.Close()
	c, _ := fastClient(srv)
	ctx := context.Background()
	if _, _, err := c.CreateIssue(ctx, IssueCreate{ID: "reserved-issue", TeamID: "team", ProjectID: "project", MilestoneID: "milestone", Title: "Title"}); err != nil {
		t.Fatal(err)
	}
	i, err := c.LookupGoalIssue(ctx, "reserved-issue")
	if err != nil || i == nil {
		t.Fatalf("issue lookup: %v %v", i, err)
	}
	if i.ParentID != "parent" || i.MilestoneID != "milestone" || i.StateType != "canceled" || len(i.LabelIDs) != 1 {
		t.Fatalf("missing binding facts: %+v", i)
	}
	if err := c.CreateGoalMilestone(ctx, GoalMilestone{ID: "reserved-milestone", ProjectID: "project", Name: "Goal"}); err != nil {
		t.Fatal(err)
	}
	m, err := c.LookupGoalMilestone(ctx, "reserved-milestone")
	if err != nil || m == nil || m.ProjectID != "project" || m.ArchivedAt == "" {
		t.Fatalf("milestone: %+v %v", m, err)
	}
	want := GoalRelation{ID: "reserved-relation", IssueID: "a", RelatedIssueID: "b", Type: "blocks"}
	if err := c.CreateGoalRelation(ctx, want); err != nil {
		t.Fatal(err)
	}
	rel, err := c.LookupGoalRelation(ctx, "a", "reserved-relation")
	if err != nil || rel == nil || *rel != want {
		t.Fatalf("relation: %+v %v", rel, err)
	}
	existing, err := c.FindGoalRelation(ctx, "a", "b", "blocks")
	if err != nil || existing == nil || *existing != want {
		t.Fatalf("pair lookup: %+v %v", existing, err)
	}
}

func TestGoalLinearFalseSuccessAndLookupFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		switch {
		case strings.Contains(req.Query, "projectMilestoneCreate"):
			fmt.Fprint(w, `{"data":{"projectMilestoneCreate":{"success":false}}}`)
		case strings.Contains(req.Query, "issueRelationCreate"):
			fmt.Fprint(w, `{"data":{"issueRelationCreate":{"success":false}}}`)
		default:
			fmt.Fprint(w, `{"errors":[{"message":"permission denied"}]}`)
		}
	}))
	defer srv.Close()
	c, _ := fastClient(srv)
	ctx := context.Background()
	if err := c.CreateGoalMilestone(ctx, GoalMilestone{ID: "m", ProjectID: "p", Name: "Goal"}); err == nil {
		t.Fatal("false milestone success accepted")
	}
	if err := c.CreateGoalRelation(ctx, GoalRelation{ID: "r", IssueID: "a", RelatedIssueID: "b", Type: "blocks"}); err == nil {
		t.Fatal("false dependency success accepted")
	}
	if _, err := c.LookupGoalIssue(ctx, "i"); err == nil {
		t.Fatal("failed lookup treated as absence")
	}
	if _, err := c.LookupGoalMilestone(ctx, "m"); err == nil {
		t.Fatal("failed lookup treated as absence")
	}
}
