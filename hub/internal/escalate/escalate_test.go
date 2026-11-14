package escalate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hub/internal/lease"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// fakeCreator is an in-memory Linear: created issues are kept by id, so a
// FindIssue sees what an earlier CreateIssue made.
type fakeCreator struct {
	calls   []Issue
	teams   []string
	finds   int
	err     error // CreateIssue fails without creating
	lostErr error // CreateIssue creates, then reports this (a lost answer)
	issues  map[string]string
}

func (f *fakeCreator) CreateIssue(_ context.Context, team, id string, iss Issue) (string, error) {
	f.calls = append(f.calls, iss)
	f.teams = append(f.teams, team)
	if f.err != nil {
		return "", f.err
	}
	if f.issues == nil {
		f.issues = map[string]string{}
	}
	if _, dup := f.issues[id]; dup {
		return "", ErrGraphQL // Linear refuses a second issue with one id
	}
	ident := "ENG-" + string(rune('0'+len(f.issues)+1))
	f.issues[id] = ident
	if f.lostErr != nil {
		return "", f.lostErr
	}
	return ident, nil
}

func (f *fakeCreator) FindIssue(_ context.Context, id string) (string, bool, error) {
	f.finds++
	ident, ok := f.issues[id]
	return ident, ok, nil
}

// flakyStore fails the Update calls whose 1-based number is in fail.
type flakyStore struct {
	store.Store
	n    int
	fail map[int]bool
}

func (f *flakyStore) Update(fn func(d *store.Data) error) error {
	f.n++
	if f.fail[f.n] {
		return errors.New("disk full")
	}
	return f.Store.Update(fn)
}

var t0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func critical() hubproto.Item {
	return hubproto.Item{Kind: hubproto.KindDepAdvisory, DepAdvisory: &hubproto.DepAdvisory{
		Repo: "acme/web", Ecosystem: "npm", Package: "lodash", Vulnerable: "< 4.17.21",
		Fixed: "4.17.21", Advisory: "GHSA-jf85-cpcp-j695", Severity: hubproto.SeverityCritical,
	}}
}

func seed(t *testing.T) *store.File {
	s, _ := store.Open("")
	if err := s.Update(func(d *store.Data) error {
		d.Orgs["acme"] = &store.Org{Name: "acme"}
		d.Items["i1"] = lease.NewItem("i1", "acme", critical(), "dev", t0)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRunOnceSendsOnceAndRecords(t *testing.T) {
	s := seed(t)
	fc := &fakeCreator{}
	e := &Escalator{Store: s, Creator: fc, Team: "team-1", Now: func() time.Time { return t0 }}
	if sent, failed := e.RunOnce(context.Background()); sent != 1 || failed != 0 {
		t.Fatalf("sent=%d failed=%d", sent, failed)
	}
	if sent, _ := e.RunOnce(context.Background()); sent != 0 || len(fc.calls) != 1 {
		t.Fatal("escalated twice")
	}
	if fc.teams[0] != "team-1" || !strings.Contains(fc.calls[0].Title, "GHSA-jf85-cpcp-j695") || !strings.Contains(fc.calls[0].Body, "critical") {
		t.Fatalf("issue %+v", fc.calls[0])
	}
	_ = s.View(func(d *store.Data) error {
		x := d.Items["i1"].Escalation
		if x.Pending || x.LinearID != "ENG-1" || x.SentAt.IsZero() {
			t.Fatalf("escalation %+v", x)
		}
		return nil
	})
}

func TestRunOnceFailsOpenWithBackoff(t *testing.T) {
	s := seed(t)
	fc := &fakeCreator{err: ErrAuth}
	now := t0
	e := &Escalator{Store: s, Creator: fc, Team: "t", Now: func() time.Time { return now }}
	if _, failed := e.RunOnce(context.Background()); failed != 1 {
		t.Fatal("expected failure")
	}
	_ = s.View(func(d *store.Data) error {
		x := d.Items["i1"].Escalation
		if !x.Pending || x.Err != ErrAuth.Error() || !x.NextTry.Equal(t0.Add(MinBackoff)) {
			t.Fatalf("%+v", x)
		}
		return nil
	})
	// Not due yet: no call.
	e.RunOnce(context.Background())
	if len(fc.calls) != 1 {
		t.Fatal("retried before backoff")
	}
	now = t0.Add(2 * time.Minute)
	fc.err = nil
	if sent, _ := e.RunOnce(context.Background()); sent != 1 {
		t.Fatal("retry not sent")
	}
}

func TestDisabledSendsNothing(t *testing.T) {
	s := seed(t)
	e := &Escalator{Store: s, Creator: &fakeCreator{}}
	if !e.Enabled() {
		t.Fatal("a key alone enables sending")
	}
	if sent, failed := e.RunOnce(context.Background()); sent+failed != 0 {
		t.Fatal("sent with no team")
	}
	if (&Escalator{Store: s, Team: "t"}).Enabled() {
		t.Fatal("enabled without a key")
	}
}

func TestBuildIssueInvalidItemIsGeneric(t *testing.T) {
	w := lease.NewItem("i1", "acme", critical(), "d", t0)
	w.Item.DepAdvisory.Package = "evil`\n## injected"
	iss := BuildIssue(w)
	if strings.Contains(iss.Body, "injected") || strings.Contains(iss.Title, "injected") {
		t.Fatalf("unvalidated field leaked: %+v", iss)
	}
}

func TestBuildIssueRedBump(t *testing.T) {
	w := lease.NewItem("i2", "acme", hubproto.Item{Kind: hubproto.KindRedBumpPR, RedBumpPR: &hubproto.RedBumpPR{
		Repo: "acme/web", PR: 12, Branch: "renovate/lodash", Package: "lodash", Author: hubproto.AuthorRenovate,
	}}, "d", t0)
	w.Attempts = 2
	iss := BuildIssue(w)
	if !strings.Contains(iss.Title, "acme/web#12") || !strings.Contains(iss.Body, "https://github.com/acme/web/pull/12") || !strings.Contains(iss.Body, "2 automated attempts") {
		t.Fatalf("%+v", iss)
	}
}

func TestLinearClient(t *testing.T) {
	const key = "lin_api_SECRET"
	var gotAuth string
	var gotBody map[string]any
	status := http.StatusOK
	resp := `{"data":{"issueCreate":{"success":true,"issue":{"identifier":"ENG-42"}}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(resp))
	}))
	defer srv.Close()
	l := &Linear{Key: key, Endpoint: srv.URL}
	id, err := l.CreateIssue(context.Background(), "team", IssueID("i1"), Issue{Title: "T", Body: "B"})
	if err != nil || id != "ENG-42" || gotAuth != key {
		t.Fatalf("id=%q err=%v auth=%q", id, err, gotAuth)
	}
	in := gotBody["variables"].(map[string]any)["input"].(map[string]any)
	if in["teamId"] != "team" || in["title"] != "T" || in["description"] != "B" || in["id"] != IssueID("i1") {
		t.Fatalf("input %+v", in)
	}

	status = http.StatusUnauthorized
	if _, err := l.CreateIssue(context.Background(), "team", IssueID("i1"), Issue{}); !errors.Is(err, ErrAuth) || strings.Contains(err.Error(), key) {
		t.Fatalf("401: %v", err)
	}
	status, resp = http.StatusOK, `{"errors":[{"message":"`+key+`"}]}`
	if _, err := l.CreateIssue(context.Background(), "team", IssueID("i1"), Issue{}); !errors.Is(err, ErrGraphQL) || strings.Contains(err.Error(), key) {
		t.Fatalf("graphql: %v", err)
	}
}

func TestRunOnceRecordsAttemptBeforeCalling(t *testing.T) {
	s := seed(t)
	fc := &fakeCreator{}
	fs := &flakyStore{Store: s, fail: map[int]bool{1: true}}
	e := &Escalator{Store: fs, Creator: fc, Team: "t", Now: func() time.Time { return t0 }}
	if sent, _ := e.RunOnce(context.Background()); sent != 0 || len(fc.calls) != 0 {
		t.Fatalf("called Linear without a recorded attempt: sent=%d calls=%d", sent, len(fc.calls))
	}
}

func TestRetryAfterUnrecordedCreateFindsTheIssue(t *testing.T) {
	cases := map[string]func(fc *fakeCreator) *flakyStore{
		// The create succeeded but the result could not be written.
		"result write fails": func(fc *fakeCreator) *flakyStore {
			return &flakyStore{fail: map[int]bool{2: true}}
		},
		// The create succeeded but the answer was lost (canceled ctx).
		"answer lost": func(fc *fakeCreator) *flakyStore {
			fc.lostErr = context.Canceled
			return &flakyStore{}
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			s := seed(t)
			fc := &fakeCreator{}
			fs := mk(fc)
			fs.Store = s
			now := t0
			e := &Escalator{Store: fs, Creator: fc, Team: "t", Now: func() time.Time { return now }}
			e.RunOnce(context.Background())
			if len(fc.issues) != 1 {
				t.Fatalf("issues %d", len(fc.issues))
			}
			// Same tick again: the attempt was recorded, nothing is due.
			e.RunOnce(context.Background())
			if len(fc.calls) != 1 {
				t.Fatalf("retried before backoff: %d calls", len(fc.calls))
			}
			fc.lostErr = nil
			now = t0.Add(2 * time.Minute)
			if sent, _ := e.RunOnce(context.Background()); sent != 1 {
				t.Fatal("retry did not resolve")
			}
			// One lookup per attempt by the stable id: the retry finds it.
			if len(fc.calls) != 1 || len(fc.issues) != 1 || fc.finds < 2 {
				t.Fatalf("second issue: calls=%d issues=%d finds=%d", len(fc.calls), len(fc.issues), fc.finds)
			}
			_ = s.View(func(d *store.Data) error {
				if x := d.Items["i1"].Escalation; x.Pending || x.LinearID != "ENG-1" {
					t.Fatalf("%+v", x)
				}
				return nil
			})
		})
	}
}

func TestEscalationsStayInTheirOrg(t *testing.T) {
	s, _ := store.Open("")
	_ = s.Update(func(d *store.Data) error {
		d.Orgs["acme"] = &store.Org{Name: "acme", LinearTeam: "team-acme"}
		d.Orgs["beta"] = &store.Org{Name: "beta"}
		d.Items["i1"] = lease.NewItem("i1", "acme", critical(), "dev", t0)
		d.Items["i2"] = lease.NewItem("i2", "beta", critical(), "dev", t0)
		return nil
	})
	fc := &fakeCreator{}
	e := &Escalator{Store: s, Creator: fc, Team: "hub-team", Now: func() time.Time { return t0 }}
	if sent, _ := e.RunOnce(context.Background()); sent != 1 {
		t.Fatalf("sent %d", sent)
	}
	if len(fc.teams) != 1 || fc.teams[0] != "team-acme" {
		t.Fatalf("teams %v", fc.teams)
	}
	_ = s.View(func(d *store.Data) error {
		if !d.Items["i2"].Escalation.Pending || d.Items["i2"].Escalation.Tries != 0 {
			t.Fatal("beta escalated without its own team")
		}
		return nil
	})
	// A single-org hub uses the hub-wide team.
	_ = s.Update(func(d *store.Data) error { delete(d.Orgs, "acme"); return nil })
	if sent, _ := e.RunOnce(context.Background()); sent != 1 || fc.teams[1] != "hub-team" {
		t.Fatalf("single org fallback: sent=%d teams=%v", sent, fc.teams)
	}
}

func TestDailyCapPerOrg(t *testing.T) {
	s, _ := store.Open("")
	_ = s.Update(func(d *store.Data) error {
		d.Orgs["acme"] = &store.Org{Name: "acme", LinearTeam: "t"}
		for i := 0; i < 5; i++ {
			id := "i" + string(rune('a'+i))
			it := critical()
			it.DepAdvisory.Package = "pkg" + string(rune('a'+i))
			d.Items[id] = lease.NewItem(id, "acme", it, "dev", t0)
		}
		return nil
	})
	fc := &fakeCreator{}
	now := t0
	e := &Escalator{Store: s, Creator: fc, MaxPerOrgPerDay: 2, Now: func() time.Time { return now }}
	e.RunOnce(context.Background())
	e.RunOnce(context.Background())
	if len(fc.calls) != 2 {
		t.Fatalf("cap ignored: %d", len(fc.calls))
	}
	now = t0.Add(25 * time.Hour)
	e.RunOnce(context.Background())
	if len(fc.calls) != 4 {
		t.Fatalf("cap did not roll: %d", len(fc.calls))
	}
}

func TestLegacyIssueIDIsStableUUIDv5(t *testing.T) {
	a, b := IssueID("i0123"), IssueID("i0123")
	if a != b || a == IssueID("i0124") || len(a) != 36 || a[14] != '5' {
		t.Fatalf("%s %s", a, b)
	}
}

func TestLinearFindIssue(t *testing.T) {
	resp := `{"data":{"issues":{"nodes":[{"identifier":"ENG-7"}]}}}`
	var gotVars map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotVars, _ = body["variables"].(map[string]any)
		_, _ = w.Write([]byte(resp))
	}))
	defer srv.Close()
	l := &Linear{Key: "k", Endpoint: srv.URL}
	id, found, err := l.FindIssue(context.Background(), IssueID("i1"))
	if err != nil || !found || id != "ENG-7" || gotVars["id"] != IssueID("i1") {
		t.Fatalf("%q %v %v %v", id, found, err, gotVars)
	}
	resp = `{"data":{"issues":{"nodes":[]}}}`
	if _, found, err := l.FindIssue(context.Background(), "x"); err != nil || found {
		t.Fatalf("not found: %v %v", found, err)
	}
	resp = `{"errors":[{"message":"x"}]}`
	if _, _, err := l.FindIssue(context.Background(), "x"); !errors.Is(err, ErrGraphQL) {
		t.Fatalf("error: %v", err)
	}
}
