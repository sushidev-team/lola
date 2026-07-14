package linear

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// gqlRequest is the wire shape do() posts to the endpoint.
type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func decodeRequest(t *testing.T, r *http.Request) gqlRequest {
	t.Helper()
	var req gqlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return req
}

// fastClient wires New() to the test server and makes retries instant while
// recording every sleep the backoff loop requests.
func fastClient(srv *httptest.Server) (c *Client, slept *[]time.Duration) {
	c = New(srv.URL, "test-key")
	var (
		mu sync.Mutex
		ds []time.Duration
	)
	c.baseDelay = time.Millisecond
	c.sleep = func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		ds = append(ds, d)
		mu.Unlock()
		return nil
	}
	return c, &ds
}

func TestMatchingIssuesPagination(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []gqlRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		mu.Lock()
		requests = append(requests, req)
		n := len(requests)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch n {
		case 1:
			w.Write([]byte(`{"data":{"issues":{
				"nodes":[
					{"id":"uuid-1","identifier":"FE-1","title":"one","branchName":"fe-1","priority":2,"createdAt":"2026-01-01T00:00:00Z","labels":{"nodes":[{"id":"lbl-a"},{"id":"lbl-b"}]}},
					{"id":"uuid-2","identifier":"FE-2","title":"two","branchName":"fe-2","priority":1,"createdAt":"2026-01-02T00:00:00Z","labels":{"nodes":[]}}
				],
				"pageInfo":{"hasNextPage":true,"endCursor":"cursor-page-1"}}}}`))
		case 2:
			w.Write([]byte(`{"data":{"issues":{
				"nodes":[
					{"id":"uuid-3","identifier":"FE-3","title":"three","branchName":"fe-3","priority":0,"createdAt":"2026-01-03T00:00:00Z","labels":{"nodes":[{"id":"lbl-c"}]}}
				],
				"pageInfo":{"hasNextPage":false,"endCursor":"cursor-page-2"}}}}`))
		default:
			t.Errorf("unexpected request #%d after final page", n)
			w.Write([]byte(`{"data":{"issues":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}`))
		}
	}))
	defer srv.Close()

	c, _ := fastClient(srv)
	p := basePoll()
	issues, err := c.MatchingIssues(context.Background(), p, "", "")
	if err != nil {
		t.Fatalf("MatchingIssues: %v", err)
	}

	if len(issues) != 3 {
		t.Fatalf("got %d issues, want 3 across both pages", len(issues))
	}
	wantIDs := []string{"FE-1", "FE-2", "FE-3"}
	for i, want := range wantIDs {
		if issues[i].Identifier != want {
			t.Errorf("issues[%d].Identifier = %q, want %q", i, issues[i].Identifier, want)
		}
	}
	if issues[0].ID != "uuid-1" || issues[2].ID != "uuid-3" {
		t.Errorf("UUIDs not preserved: %q, %q", issues[0].ID, issues[2].ID)
	}

	// LabelIDs must be populated from labels.nodes.
	if got := issues[0].LabelIDs; len(got) != 2 || got[0] != "lbl-a" || got[1] != "lbl-b" {
		t.Errorf("issues[0].LabelIDs = %v, want [lbl-a lbl-b]", got)
	}
	if len(issues[1].LabelIDs) != 0 {
		t.Errorf("issues[1].LabelIDs = %v, want empty", issues[1].LabelIDs)
	}
	if got := issues[2].LabelIDs; len(got) != 1 || got[0] != "lbl-c" {
		t.Errorf("issues[2].LabelIDs = %v, want [lbl-c]", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("server saw %d requests, want 2", len(requests))
	}
	// First page: after is null.
	if after := requests[0].Variables["after"]; after != nil {
		t.Errorf("first request after = %v, want null", after)
	}
	// Second page must carry the endCursor from page one.
	if after := requests[1].Variables["after"]; after != "cursor-page-1" {
		t.Errorf("second request after = %v, want %q", after, "cursor-page-1")
	}
	// Both requests carry the filter as a variable (never interpolated).
	for i, req := range requests {
		f, ok := req.Variables["filter"].(map[string]any)
		if !ok {
			t.Fatalf("request %d: filter variable missing or wrong type: %#v", i, req.Variables["filter"])
		}
		team, _ := f["team"].(map[string]any)
		id, _ := team["id"].(map[string]any)
		if id["eq"] != "team-1" {
			t.Errorf("request %d: filter.team.id.eq = %v, want team-1", i, id["eq"])
		}
	}
}

func TestBackoffRetriesThenSucceeds(t *testing.T) {
	var (
		mu   sync.Mutex
		hits int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		n := hits
		mu.Unlock()
		if n <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"viewer":{"id":"u-1","name":"Viewer","email":"v@example.com"}}}`))
	}))
	defer srv.Close()

	c, slept := fastClient(srv)
	u, err := c.Viewer(context.Background())
	if err != nil {
		t.Fatalf("Viewer after two 429s: %v", err)
	}
	if u.ID != "u-1" {
		t.Errorf("viewer.ID = %q, want u-1", u.ID)
	}

	mu.Lock()
	if hits != 3 {
		t.Errorf("server hits = %d, want 3 (2 x 429 + success)", hits)
	}
	mu.Unlock()

	ds := *slept
	if len(ds) != 2 {
		t.Fatalf("sleep hook invoked %d times, want 2 (once per retry)", len(ds))
	}
	for i, d := range ds {
		if d <= 0 {
			t.Errorf("sleep[%d] = %v, want > 0", i, d)
		}
	}
	// baseDelay=1ms: attempt 1 sleeps in [0.75ms,1.25ms], attempt 2 in
	// [1.5ms,2.5ms] — exponential growth must hold even with jitter.
	if ds[1] <= ds[0] {
		t.Errorf("backoff not exponential: sleep[1]=%v <= sleep[0]=%v", ds[1], ds[0])
	}
}

func TestBackoffGivesUpAfterMaxRetries(t *testing.T) {
	var (
		mu   sync.Mutex
		hits int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c, slept := fastClient(srv)
	c.maxRetries = 2

	_, err := c.Viewer(context.Background())
	if err == nil {
		t.Fatal("expected permanent failure, got nil")
	}
	if !strings.Contains(err.Error(), "giving up after 2 retries") {
		t.Errorf("error = %q, want mention of giving up after 2 retries", err)
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error = %q, want the transient http status wrapped in it", err)
	}

	mu.Lock()
	if hits != 3 {
		t.Errorf("server hits = %d, want maxRetries+1 = 3", hits)
	}
	mu.Unlock()
	if len(*slept) != 2 {
		t.Errorf("sleep hook invoked %d times, want 2", len(*slept))
	}
}

func TestAuthFailureNotRetried(t *testing.T) {
	var (
		mu   sync.Mutex
		hits int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c, slept := fastClient(srv)
	_, err := c.Viewer(context.Background())
	if err == nil {
		t.Fatal("expected auth error, got nil")
	}
	if !strings.Contains(err.Error(), "auth failed") {
		t.Errorf("error = %q, want auth failed", err)
	}

	mu.Lock()
	if hits != 1 {
		t.Errorf("server hits = %d, want exactly 1 (401 must not be retried)", hits)
	}
	mu.Unlock()
	if len(*slept) != 0 {
		t.Errorf("sleep hook invoked %d times on 401, want 0", len(*slept))
	}
}
