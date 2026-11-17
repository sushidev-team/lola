package escalate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hub/internal/lease"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// linkCreator is a fakeCreator that also answers URLs and closes issues.
type linkCreator struct {
	fakeCreator
	ids      []string
	closed   map[string]string // identifier -> comment
	cerr     error
	reopened map[string]string // identifier -> comment
}

func (l *linkCreator) CreateIssueRef(ctx context.Context, team, id string, iss Issue) (Ref, error) {
	l.ids = append(l.ids, id)
	ident, err := l.CreateIssue(ctx, team, id, iss)
	return Ref{Identifier: ident, URL: "https://linear.app/acme/issue/" + ident}, err
}

func (l *linkCreator) FindIssueRef(ctx context.Context, id string) (Ref, bool, error) {
	ident, ok, err := l.FindIssue(ctx, id)
	_, closed := l.closed[ident]
	return Ref{Identifier: ident, URL: "https://linear.app/acme/issue/" + ident, Closed: closed}, ok, err
}

func (l *linkCreator) ReopenIssue(_ context.Context, ident, comment string) error {
	if l.reopened == nil {
		l.reopened = map[string]string{}
	}
	l.reopened[ident] = comment
	delete(l.closed, ident)
	return nil
}

func (l *linkCreator) CloseIssue(_ context.Context, ident, comment string) error {
	if l.cerr != nil {
		return l.cerr
	}
	if l.closed == nil {
		l.closed = map[string]string{}
	}
	l.closed[ident] = comment
	return nil
}

func advisory(pkg string, sev hubproto.Severity) hubproto.Item {
	it := critical()
	it.DepAdvisory.Package = pkg
	it.DepAdvisory.Severity = sev
	return it
}

// escalated stores an item escalated by attempts, failed under group.
func escalated(d *store.Data, id string, it hubproto.Item, group string, at time.Time) *store.WorkItem {
	w := lease.NewItem(id, "acme", it, "dev", at)
	w.State = hubproto.StateEscalated
	w.Attempts = 2
	w.FailGroup = group
	w.Escalation = &store.Escalation{Pending: true, Reason: lease.ReasonAttempts, CreatedAt: at}
	d.Items[id] = w
	return w
}

func TestIssueIDComesFromOrgAndKeyAndURLIsStored(t *testing.T) {
	s := seed(t)
	lc := &linkCreator{}
	e := &Escalator{Store: s, Creator: lc, Team: "t", Now: func() time.Time { return t0 }}
	if sent, _ := e.RunOnce(context.Background()); sent != 1 {
		t.Fatal("not sent")
	}
	var key string
	_ = s.View(func(d *store.Data) error {
		w := d.Items["i1"]
		key = w.Key
		if w.Escalation.URL != "https://linear.app/acme/issue/ENG-1" {
			t.Fatalf("url %q", w.Escalation.URL)
		}
		return nil
	})
	if want := hubproto.EscalationIssueID("acme", key); lc.ids[0] != want || want == IssueID("i1") {
		t.Fatalf("issue id %s, want %s", lc.ids[0], want)
	}
}

func TestReEscalatedFindingReusesItsIssue(t *testing.T) {
	s, _ := store.Open("")
	_ = s.Update(func(d *store.Data) error {
		d.Orgs["acme"] = &store.Org{Name: "acme", LinearTeam: "t"}
		escalated(d, "ia", advisory("lodash", hubproto.SeverityHigh), "", t0)
		return nil
	})
	fc := &fakeCreator{}
	e := &Escalator{Store: s, Creator: fc, Now: func() time.Time { return t0 }}
	e.RunOnce(context.Background())
	// The same finding as a new item (purged and reported again).
	_ = s.Update(func(d *store.Data) error {
		delete(d.Items, "ia")
		escalated(d, "ib", advisory("lodash", hubproto.SeverityHigh), "", t0)
		return nil
	})
	if sent, _ := e.RunOnce(context.Background()); sent != 1 {
		t.Fatal("second episode not recorded")
	}
	if len(fc.issues) != 1 || len(fc.calls) != 1 {
		t.Fatalf("a second issue for one finding: issues=%d calls=%d", len(fc.issues), len(fc.calls))
	}
}

func TestFailGroupFilesOneIssue(t *testing.T) {
	s, _ := store.Open("")
	_ = s.Update(func(d *store.Data) error {
		d.Orgs["acme"] = &store.Org{Name: "acme", LinearTeam: "t"}
		escalated(d, "ia", advisory("lodash", hubproto.SeverityHigh), "grp12345", t0)
		escalated(d, "ib", advisory("minimist", hubproto.SeverityHigh), "grp12345", t0)
		escalated(d, "ic", advisory("axios", hubproto.SeverityHigh), "", t0)
		return nil
	})
	lc := &linkCreator{}
	e := &Escalator{Store: s, Creator: lc, Now: func() time.Time { return t0 }}
	if sent, _ := e.RunOnce(context.Background()); sent != 2 {
		t.Fatalf("sent %d, want 2 (group + single)", sent)
	}
	var group Issue
	for _, c := range lc.calls {
		if strings.Contains(c.Title, "2 chores") {
			group = c
		}
	}
	if !strings.Contains(group.Body, "lodash") || !strings.Contains(group.Body, "minimist") || strings.Contains(group.Body, "axios") {
		t.Fatalf("group issue %+v", group)
	}
	_ = s.View(func(d *store.Data) error {
		a, b := d.Items["ia"].Escalation, d.Items["ib"].Escalation
		if a.LinearID == "" || a.LinearID != b.LinearID || a.Pending || b.Pending {
			t.Fatalf("group not shared: %+v %+v", a, b)
		}
		return nil
	})
	if lc.ids[0] != hubproto.EscalationIssueID("acme", hubproto.GroupKey("grp12345")) && lc.ids[1] != hubproto.EscalationIssueID("acme", hubproto.GroupKey("grp12345")) {
		t.Fatalf("group issue id %v", lc.ids)
	}
}

func TestSendOrderIsSeverityThenAge(t *testing.T) {
	s, _ := store.Open("")
	_ = s.Update(func(d *store.Data) error {
		d.Orgs["acme"] = &store.Org{Name: "acme", LinearTeam: "t"}
		escalated(d, "ia", advisory("old-low", hubproto.SeverityLow), "", t0)
		escalated(d, "ib", advisory("new-high", hubproto.SeverityHigh), "", t0.Add(time.Hour))
		escalated(d, "ic", advisory("old-high", hubproto.SeverityHigh), "", t0)
		return nil
	})
	fc := &fakeCreator{}
	now := t0.Add(2 * time.Hour)
	e := &Escalator{Store: s, Creator: fc, MaxPerOrgPerDay: 2, Now: func() time.Time { return now }}
	e.RunOnce(context.Background())
	if len(fc.calls) != 2 || !strings.Contains(fc.calls[0].Title, "old-high") || !strings.Contains(fc.calls[1].Title, "new-high") {
		t.Fatalf("order: %v", titles(fc.calls))
	}
}

func titles(is []Issue) []string {
	var out []string
	for _, i := range is {
		out = append(out, i.Title)
	}
	return out
}

func TestPendingEscalationOfDoneItemIsDropped(t *testing.T) {
	s := seed(t)
	_ = s.Update(func(d *store.Data) error { d.Items["i1"].State = hubproto.StateDone; return nil })
	fc := &fakeCreator{}
	e := &Escalator{Store: s, Creator: fc, Team: "t", Now: func() time.Time { return t0 }}
	if sent, failed := e.RunOnce(context.Background()); sent+failed != 0 || len(fc.calls) != 0 {
		t.Fatal("escalated a done item")
	}
	_ = s.View(func(d *store.Data) error {
		if d.Items["i1"].Escalation != nil {
			t.Fatal("pending escalation kept")
		}
		return nil
	})
}

func TestDoneItemClosesItsIssueOnce(t *testing.T) {
	s, _ := store.Open("")
	_ = s.Update(func(d *store.Data) error {
		d.Orgs["acme"] = &store.Org{Name: "acme", LinearTeam: "t"}
		escalated(d, "ia", advisory("lodash", hubproto.SeverityHigh), "grp12345", t0)
		escalated(d, "ib", advisory("minimist", hubproto.SeverityHigh), "grp12345", t0)
		return nil
	})
	lc := &linkCreator{}
	now := t0
	e := &Escalator{Store: s, Creator: lc, Now: func() time.Time { return now }}
	e.RunOnce(context.Background())
	// One of two done: the group issue stays open.
	_ = s.Update(func(d *store.Data) error {
		w := d.Items["ia"]
		w.State = hubproto.StateDone
		w.Outcome = &store.Outcome{PRURL: "https://github.com/acme/web/pull/7", Delivery: hubproto.DeliveryMerged}
		return nil
	})
	e.RunOnce(context.Background())
	if len(lc.closed) != 0 {
		t.Fatal("closed with an item still escalated")
	}
	_ = s.Update(func(d *store.Data) error { d.Items["ib"].State = hubproto.StateDone; return nil })
	lc.cerr = ErrNetwork
	e.RunOnce(context.Background())
	lc.cerr = nil
	e.RunOnce(context.Background())
	if len(lc.closed) != 0 {
		t.Fatal("a failed close retried before its backoff")
	}
	now = now.Add(CloseRetry)
	e.RunOnce(context.Background())
	e.RunOnce(context.Background())
	if len(lc.closed) != 1 || !strings.Contains(lc.closed["ENG-1"], "https://github.com/acme/web/pull/7") {
		t.Fatalf("closed %v", lc.closed)
	}
	_ = s.View(func(d *store.Data) error {
		if d.Items["ia"].Escalation.Err != ClosedMark || d.Items["ib"].Escalation.Err != ClosedMark {
			t.Fatal("close not recorded")
		}
		return nil
	})
}

func TestIssueBodyNamesLastAttemptPR(t *testing.T) {
	w := lease.NewItem("i1", "acme", critical(), "d", t0)
	w.History = []store.Attempt{
		{Action: hubproto.AttemptInReview, PRURL: "https://github.com/acme/web/pull/9", At: t0},
		{Action: hubproto.AttemptFailed, PRURL: "javascript:alert(1)", At: t0},
	}
	b := BuildIssue(w).Body
	if !strings.Contains(b, "https://github.com/acme/web/pull/9") || strings.Contains(b, "javascript") {
		t.Fatalf("%s", b)
	}
}

func TestLinearCloseAndTeamKey(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		queries = append(queries, body.Query)
		switch {
		case strings.Contains(body.Query, "commentCreate"):
			_, _ = w.Write([]byte(`{"data":{"commentCreate":{"success":true}}}`))
		case strings.Contains(body.Query, "states"):
			_, _ = w.Write([]byte(`{"data":{"issue":{"team":{"states":{"nodes":[{"id":"s2","position":2},{"id":"s1","position":1}]}}}}}`))
		case strings.Contains(body.Query, "issueUpdate"):
			_, _ = w.Write([]byte(`{"data":{"issueUpdate":{"success":true}}}`))
		case strings.Contains(body.Query, "teams"):
			_, _ = w.Write([]byte(`{"data":{"teams":{"nodes":[{"id":"team-uuid-1"}]}}}`))
		}
	}))
	defer srv.Close()
	l := &Linear{Key: "k", Endpoint: srv.URL}
	if err := l.CloseIssue(context.Background(), "ENG-1", "done"); err != nil || len(queries) != 3 {
		t.Fatalf("close: %v %d", err, len(queries))
	}
	if err := l.CloseIssue(context.Background(), "not an id", "x"); err == nil {
		t.Fatal("invalid identifier sent")
	}
	id, err := l.TeamID(context.Background(), "ENG")
	if err != nil || id != "team-uuid-1" {
		t.Fatalf("team %q %v", id, err)
	}
	if !IsTeamKey("ENG") || IsTeamKey("8a9b-uuid") {
		t.Fatal("team key shape")
	}
}

func TestReEscalationReopensTheClosedIssue(t *testing.T) {
	s, _ := store.Open("")
	_ = s.Update(func(d *store.Data) error {
		d.Orgs["acme"] = &store.Org{Name: "acme", LinearTeam: "t"}
		escalated(d, "ia", advisory("lodash", hubproto.SeverityHigh), "", t0)
		return nil
	})
	lc := &linkCreator{}
	now := t0
	e := &Escalator{Store: s, Creator: lc, Now: func() time.Time { return now }}
	e.RunOnce(context.Background())
	// A person resolves it: the hub closes ENG-1.
	_ = s.Update(func(d *store.Data) error { return lease.Resolve(d.Items["ia"], now) })
	e.RunOnce(context.Background())
	if _, ok := lc.closed["ENG-1"]; !ok {
		t.Fatalf("issue not closed: %v", lc.closed)
	}
	// Re-reported past ReopenAfter, then it fails escalate_after times again.
	now = now.Add(lease.ReopenAfter + time.Hour)
	_ = s.Update(func(d *store.Data) error {
		w := d.Items["ia"]
		if !lease.Observe(w, "dev", now) {
			t.Fatal("not reopened")
		}
		a := &store.Device{ID: "dev2", Org: "acme", Repos: []string{"acme/web"}}
		for range 2 {
			if _, err := lease.Claim(w, a, now, lease.Config{}); err != nil {
				t.Fatal(err)
			}
			if _, err := lease.Fail(w, a, hubproto.FailCI, "", now, lease.Config{}); err != nil {
				t.Fatal(err)
			}
			now = now.Add(hubproto.DeviceCooldown + time.Minute)
		}
		if w.State != hubproto.StateEscalated || w.Escalation == nil || !w.Escalation.Pending {
			t.Fatalf("re-escalation not pending: %+v", w.Escalation)
		}
		return nil
	})
	if sent, _ := e.RunOnce(context.Background()); sent != 1 {
		t.Fatal("re-escalation not sent")
	}
	if len(lc.issues) != 1 || !strings.Contains(lc.reopened["ENG-1"], "`ia`") {
		t.Fatalf("issues=%v reopened=%v", lc.issues, lc.reopened)
	}
	_ = s.View(func(d *store.Data) error {
		x := d.Items["ia"].Escalation
		if x.Pending || x.Err != "" || x.LinearID != "ENG-1" {
			t.Fatalf("escalation %+v", x)
		}
		return nil
	})
}

func TestLinearFindSeesClosedAndReopens(t *testing.T) {
	var queries []string
	stateID := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string `json:"query"`
			Variables struct {
				Input struct {
					StateID string `json:"stateId"`
				} `json:"input"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		queries = append(queries, body.Query)
		switch {
		case strings.Contains(body.Query, "issues(filter"):
			_, _ = w.Write([]byte(`{"data":{"issues":{"nodes":[{"identifier":"ENG-7","state":{"type":"completed"}}]}}}`))
		case strings.Contains(body.Query, "commentCreate"):
			_, _ = w.Write([]byte(`{"data":{"commentCreate":{"success":true}}}`))
		case strings.Contains(body.Query, "states"):
			_, _ = w.Write([]byte(`{"data":{"issue":{"team":{"states":{"nodes":[{"id":"b1","position":0,"type":"backlog"},{"id":"u2","position":2,"type":"unstarted"},{"id":"u1","position":1,"type":"unstarted"}]}}}}}`))
		case strings.Contains(body.Query, "issueUpdate"):
			stateID = body.Variables.Input.StateID
			_, _ = w.Write([]byte(`{"data":{"issueUpdate":{"success":true}}}`))
		}
	}))
	defer srv.Close()
	l := &Linear{Key: "k", Endpoint: srv.URL}
	r, found, err := l.FindIssueRef(context.Background(), "x")
	if err != nil || !found || !r.Closed {
		t.Fatalf("find: %+v %v %v", r, found, err)
	}
	if err := l.ReopenIssue(context.Background(), "ENG-7", "again"); err != nil || len(queries) != 4 || stateID != "u1" {
		t.Fatalf("reopen: %v %d state %q", err, len(queries), stateID)
	}
	if err := l.ReopenIssue(context.Background(), "not an id", "x"); err == nil {
		t.Fatal("invalid identifier sent")
	}
}
