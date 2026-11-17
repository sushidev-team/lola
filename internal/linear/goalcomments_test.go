package linear

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGoalCommentStableIdentity(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		calls++
		if calls == 1 {
			input, _ := req.Variables["input"].(map[string]any)
			if input["id"] != "reserved" || input["issueId"] != "parent" || input["body"] != "summary" || input["doNotSubscribeToIssue"] != true {
				t.Errorf("wrong comment input: %v", input)
			}
			fmt.Fprint(w, `{"data":{"commentCreate":{"success":true}}}`)
			return
		}
		if req.Variables["id"] != "reserved" || !strings.Contains(req.Query, "includeArchived:true") {
			t.Errorf("lookup lost exact identity: %+v", req)
		}
		fmt.Fprint(w, `{"data":{"comments":{"nodes":[{"id":"reserved","body":"summary","issue":{"id":"parent"},"archivedAt":"2026-10-21"}]}}}`)
	}))
	defer srv.Close()
	c, _ := fastClient(srv)
	if err := c.CreateGoalComment(context.Background(), GoalComment{ID: "reserved", IssueID: "parent", Body: "summary"}); err != nil {
		t.Fatal(err)
	}
	got, err := c.LookupGoalComment(context.Background(), "reserved")
	if err != nil || got == nil || got.ArchivedAt == "" || got.IssueID != "parent" {
		t.Fatal(got, err)
	}
}
