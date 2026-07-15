package linear

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateCommentSendsQueryAndVariables(t *testing.T) {
	var got gqlRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"commentCreate":{"success":true}}}`))
	}))
	defer srv.Close()

	c, _ := fastClient(srv)
	if err := c.CreateComment(context.Background(), "uuid-1", "agent is working"); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}

	if !strings.Contains(got.Query, "commentCreate") {
		t.Errorf("query does not target commentCreate: %q", got.Query)
	}
	// Values must travel as variables, never interpolated into the query.
	if strings.Contains(got.Query, "uuid-1") || strings.Contains(got.Query, "agent is working") {
		t.Errorf("values interpolated into query: %q", got.Query)
	}
	if got.Variables["id"] != "uuid-1" {
		t.Errorf("variables.id = %v, want uuid-1", got.Variables["id"])
	}
	if got.Variables["body"] != "agent is working" {
		t.Errorf("variables.body = %v, want %q", got.Variables["body"], "agent is working")
	}
}

func TestCreateCommentSuccessFalseIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"commentCreate":{"success":false}}}`))
	}))
	defer srv.Close()

	c, _ := fastClient(srv)
	err := c.CreateComment(context.Background(), "uuid-1", "body")
	if err == nil {
		t.Fatal("expected error on success=false, got nil")
	}
	if !strings.Contains(err.Error(), "success=false") {
		t.Errorf("error = %q, want mention of success=false", err)
	}
}

func TestSetIssueStateSendsQueryAndVariables(t *testing.T) {
	var got gqlRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = decodeRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"issueUpdate":{"success":true}}}`))
	}))
	defer srv.Close()

	c, _ := fastClient(srv)
	if err := c.SetIssueState(context.Background(), "uuid-1", "state-done"); err != nil {
		t.Fatalf("SetIssueState: %v", err)
	}

	if !strings.Contains(got.Query, "issueUpdate") || !strings.Contains(got.Query, "stateId") {
		t.Errorf("query does not target issueUpdate with stateId: %q", got.Query)
	}
	if strings.Contains(got.Query, "uuid-1") || strings.Contains(got.Query, "state-done") {
		t.Errorf("values interpolated into query: %q", got.Query)
	}
	if got.Variables["id"] != "uuid-1" {
		t.Errorf("variables.id = %v, want uuid-1", got.Variables["id"])
	}
	if got.Variables["stateId"] != "state-done" {
		t.Errorf("variables.stateId = %v, want state-done", got.Variables["stateId"])
	}
}

func TestSetIssueStateSuccessFalseIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"issueUpdate":{"success":false}}}`))
	}))
	defer srv.Close()

	c, _ := fastClient(srv)
	err := c.SetIssueState(context.Background(), "uuid-1", "state-done")
	if err == nil {
		t.Fatal("expected error on success=false, got nil")
	}
	if !strings.Contains(err.Error(), "success=false") {
		t.Errorf("error = %q, want mention of success=false", err)
	}
}
