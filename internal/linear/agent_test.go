package linear

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// gqlServer answers every GraphQL POST with data and records the requests.
func gqlServer(t *testing.T, data string, seen *[]map[string]any, auth *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if seen != nil {
			*seen = append(*seen, body)
		}
		if auth != nil {
			*auth = r.Header.Get("Authorization")
		}
		io.WriteString(w, `{"data":`+data+`}`)
	}))
}

func TestAgentClientUsesBearerAuth(t *testing.T) {
	var auth string
	srv := gqlServer(t, `{"viewer":{"id":"u1","name":"lola"}}`, nil, &auth)
	defer srv.Close()
	if _, err := NewBearer(srv.URL, "tok").AgentViewer(context.Background()); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer tok" {
		t.Fatalf("Authorization = %q", auth)
	}
	if _, err := New(srv.URL, "key").Viewer(context.Background()); err != nil {
		t.Fatal(err)
	}
	if auth != "key" {
		t.Fatalf("a personal key must stay raw, got %q", auth)
	}
}

func TestAgentSessionsParsesIssueRouting(t *testing.T) {
	srv := gqlServer(t, `{"agentSessions":{"nodes":[
		{"id":"s1","status":"pending","createdAt":"2026-01-01T00:00:00Z","url":"https://linear.app/s1",
		 "issue":{"id":"i1","identifier":"ENG-1","title":"t","branchName":"b","priority":2,"createdAt":"x","team":{"id":"team-1"},"project":{"id":"lp"}}},
		{"id":"s2","status":"active","createdAt":"2026-01-01T00:00:00Z","url":null,"issue":null}]}}`, nil, nil)
	defer srv.Close()
	got, err := NewBearer(srv.URL, "t").AgentSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Issue == nil || got[0].Issue.TeamID != "team-1" || got[0].Issue.ProjectID != "lp" || got[0].URL == "" {
		t.Fatalf("sessions = %+v", got)
	}
	if got[1].Issue != nil {
		t.Fatal("a session without an issue must parse with Issue=nil")
	}
}

func TestAgentPromptsFilterAndOrder(t *testing.T) {
	var seen []map[string]any
	srv := gqlServer(t, `{"agentActivities":{"nodes":[
		{"id":"b","createdAt":"2026-01-01T00:00:02Z","signal":null,"user":{"id":"h"},"content":{"body":"second"}},
		{"id":"a","createdAt":"2026-01-01T00:00:01Z","signal":"stop","user":{"id":"h"},"content":{"body":"first"}}]}}`, &seen, nil)
	defer srv.Close()
	got, err := NewBearer(srv.URL, "t").AgentPrompts(context.Background(), "s1", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Body != "first" || got[0].Signal != "stop" || got[1].Body != "second" {
		t.Fatalf("prompts = %+v", got)
	}
	f := seen[0]["variables"].(map[string]any)["f"].(map[string]any)
	if f["type"].(map[string]any)["eq"] != "prompt" || f["createdAt"].(map[string]any)["gt"] != "2026-01-01T00:00:00Z" {
		t.Fatalf("filter = %+v", f)
	}
}

func TestElicitationCarriesSelectOptions(t *testing.T) {
	a := Elicitation("approve?", "Approve", "Request changes")
	if a.Signal != "select" {
		t.Fatalf("signal = %q", a.Signal)
	}
	opts := a.SignalMetadata["options"].([]map[string]any)
	if len(opts) != 2 || opts[0]["value"] != "Approve" {
		t.Fatalf("options = %+v", opts)
	}
	if Elicitation("plain").Signal != "" {
		t.Fatal("an elicitation without options carries no signal")
	}
}

func TestAgentAuthorizeURL(t *testing.T) {
	u, err := url.Parse(AgentAuthorizeURL("cid", "http://localhost:8790/callback", "st"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("actor") != "app" || q.Get("client_id") != "cid" || q.Get("state") != "st" ||
		!strings.Contains(q.Get("scope"), "app:assignable") || !strings.Contains(q.Get("scope"), "app:mentionable") {
		t.Fatalf("authorize URL = %s", u)
	}
}

// The OAuth exchange keeps every credential in the BODY and never echoes a
// response body into an error.
func TestOAuthExchangeAndErrorHygiene(t *testing.T) {
	var form url.Values
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("no credential may travel in the query string: %q", r.URL.RawQuery)
		}
		_ = r.ParseForm()
		form = r.PostForm
		io.WriteString(w, `{"access_token":"A","refresh_token":"R","expires_in":3600}`)
	}))
	defer ok.Close()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	o := OAuthClient{TokenURL: ok.URL, ClientID: "cid", ClientSecret: "sec", Now: func() time.Time { return now }}
	tok, err := o.Exchange(context.Background(), "code", "http://localhost/cb")
	if err != nil || tok.AccessToken != "A" || tok.RefreshToken != "R" || !tok.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("tok=%+v err=%v", tok, err)
	}
	if form.Get("client_secret") != "sec" || form.Get("grant_type") != "authorization_code" || form.Get("code") != "code" {
		t.Fatalf("form = %v", form)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":"invalid_grant","error_description":"token sec-LEAK expired","access_token":"LEAK"}`)
	}))
	defer bad.Close()
	o.TokenURL = bad.URL
	_, err = o.Refresh(context.Background(), "R")
	if err == nil || strings.Contains(err.Error(), "LEAK") || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("error must name the OAuth code only: %v", err)
	}
	weird := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, `{"error":"Bearer abc.def LEAK"}`)
	}))
	defer weird.Close()
	o.TokenURL = weird.URL
	if _, err = o.Refresh(context.Background(), "R"); err == nil || strings.Contains(err.Error(), "LEAK") {
		t.Fatalf("a non-vocabulary error field must not be echoed: %v", err)
	}
	if _, err = o.Refresh(context.Background(), ""); err == nil {
		t.Fatal("refresh without a refresh token must fail")
	}
}

func TestOAuthTokenExpired(t *testing.T) {
	now := time.Now()
	if (OAuthToken{}).Expired(now, time.Minute) {
		t.Fatal("a token with no expiry never expires client-side")
	}
	if !(OAuthToken{ExpiresAt: now.Add(30 * time.Second)}).Expired(now, time.Minute) {
		t.Fatal("within skew counts as expired")
	}
}

// Both agent queries follow the connection cursor instead of stopping at the
// first page.
func TestAgentQueriesPaginate(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls++
		after, _ := body["variables"].(map[string]any)["after"].(string)
		q := body["query"].(string)
		switch {
		case strings.Contains(q, "agentSessions") && after == "":
			io.WriteString(w, `{"data":{"agentSessions":{"nodes":[{"id":"s1","status":"active","createdAt":"x"}],"pageInfo":{"hasNextPage":true,"endCursor":"c1"}}}}`)
		case strings.Contains(q, "agentSessions"):
			io.WriteString(w, `{"data":{"agentSessions":{"nodes":[{"id":"s2","status":"pending","createdAt":"x"}],"pageInfo":{"hasNextPage":false,"endCursor":"c2"}}}}`)
		case after == "":
			io.WriteString(w, `{"data":{"agentActivities":{"nodes":[{"id":"a","createdAt":"2","content":{"body":"b"}}],"pageInfo":{"hasNextPage":true,"endCursor":"p1"}}}}`)
		default:
			io.WriteString(w, `{"data":{"agentActivities":{"nodes":[{"id":"b","createdAt":"1","content":{"body":"a"}}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}`)
		}
	}))
	defer srv.Close()
	c := NewBearer(srv.URL, "t")
	ss, err := c.AgentSessions(context.Background())
	if err != nil || len(ss) != 2 || ss[1].ID != "s2" {
		t.Fatalf("sessions = %+v, %v", ss, err)
	}
	ps, err := c.AgentPrompts(context.Background(), "s1", "")
	if err != nil || len(ps) != 2 || ps[0].Body != "a" {
		t.Fatalf("prompts = %+v, %v", ps, err)
	}
	if calls != 4 {
		t.Fatalf("calls = %d, want 4", calls)
	}
}
