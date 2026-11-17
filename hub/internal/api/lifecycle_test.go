package api

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// labelledToken is an enroll token an admin labelled with a member name.
func (e *env) labelledToken(org, label string) string {
	tok := e.token(org, time.Hour)
	if err := e.st.Update(func(d *store.Data) error {
		d.Tokens[store.HashToken(tok)].Label = label
		return nil
	}); err != nil {
		e.t.Fatal(err)
	}
	return tok
}

func (d *daemon) enrollAs(tok, name string) hubproto.EnrollResponse {
	d.e.t.Helper()
	r := d.do(http.MethodPost, hubproto.PathEnroll, hubproto.EnrollRequest{
		Token: tok, DeviceName: name, PublicKey: hubproto.EncodePublicKey(d.pub),
	})
	if r.status != 200 {
		d.e.t.Fatalf("enroll: %d %s", r.status, r.body)
	}
	var out hubproto.EnrollResponse
	r.decode(d.e.t, &out)
	if err := out.Validate(); err != nil {
		d.e.t.Fatalf("enroll response: %v", err)
	}
	return out
}

func (d *daemon) stats() hubproto.StatsView {
	d.e.t.Helper()
	r := d.do(http.MethodGet, hubproto.PathStats, nil)
	if r.status != 200 {
		d.e.t.Fatalf("stats: %d %s", r.status, r.body)
	}
	var out hubproto.StatsView
	if err := hubproto.DecodeStrictLimit(bytes.NewReader(r.body), hubproto.MaxViewBody, &out); err != nil {
		e := d.e
		e.t.Fatalf("stats decode: %v", err)
	}
	if err := out.Validate(); err != nil {
		d.e.t.Fatalf("stats do not validate: %v", err)
	}
	return out
}

func (e *env) events(kind string) []store.Event {
	var out []store.Event
	_ = e.st.View(func(d *store.Data) error {
		for _, ev := range d.Events {
			if ev.Kind == kind {
				out = append(out, ev)
			}
		}
		return nil
	})
	return out
}

const pr42 = "https://github.com/acme/web/pull/42"

func progressReq(delivery hubproto.Delivery) hubproto.ProgressRequest {
	return hubproto.ProgressRequest{PRURL: pr42, Delivery: delivery}
}

// Two acme daemons that heartbeat acme/web, and one finding A reported.
func twoDaemons(t *testing.T) (*env, *daemon, *daemon, string) {
	e := newEnv(t)
	a, b := e.newDaemon(), e.newDaemon()
	a.enrollNamed("acme", "alice")
	b.enrollNamed("acme", "bob")
	a.heartbeat([]string{"acme/web"})
	b.heartbeat([]string{"acme/web"})
	a.report(advisory("acme/web", hubproto.SeverityHigh))
	return e, a, b, a.offers("")[0].ID
}

func TestProgressMovesToInReview(t *testing.T) {
	e, a, b, id := twoDaemons(t)
	if r := a.do(http.MethodPost, hubproto.ProgressPath(id), progressReq(hubproto.DeliveryCIPending)); r.status != http.StatusConflict || r.code != hubproto.CodeLeaseLost {
		t.Fatalf("progress without a lease: %d %s", r.status, r.code)
	}
	a.do(http.MethodPost, hubproto.ClaimPath(id), nil)
	if r := b.do(http.MethodPost, hubproto.ProgressPath(id), progressReq(hubproto.DeliveryCIPending)); r.status != http.StatusConflict || r.code != hubproto.CodeLeaseLost {
		t.Fatalf("foreign progress: %d %s", r.status, r.code)
	}
	wrong := hubproto.ProgressRequest{PRURL: "https://github.com/acme/api/pull/1", Delivery: hubproto.DeliveryCIPending}
	if r := a.do(http.MethodPost, hubproto.ProgressPath(id), wrong); r.status != http.StatusBadRequest {
		t.Fatalf("progress with another repo's PR: %d", r.status)
	}
	if r := a.do(http.MethodPost, hubproto.ProgressPath(id), map[string]any{"pr_url": pr42, "delivery": "ci_pending", "note": "x"}); r.status != http.StatusBadRequest {
		t.Fatalf("unknown field accepted: %d", r.status)
	}
	if r := a.do(http.MethodPost, hubproto.ProgressPath(id), progressReq(hubproto.DeliveryCIPending)); r.status != 200 {
		t.Fatalf("progress: %d %s", r.status, r.body)
	}
	if w := e.item(id); w.State != hubproto.StateInReview || w.Lease != nil || w.Owner != a.id {
		t.Fatalf("after progress: %+v", w)
	}
	// Not offered to anybody, not claimable, and a re-report does not move it.
	if len(b.offers("")) != 0 || len(a.offers("")) != 0 {
		t.Fatal("in_review item offered")
	}
	if r := b.do(http.MethodPost, hubproto.ClaimPath(id), nil); r.status != http.StatusConflict {
		t.Fatalf("claim of in_review: %d", r.status)
	}
	b.report(advisory("acme/web", hubproto.SeverityHigh))
	if w := e.item(id); w.State != hubproto.StateInReview {
		t.Fatalf("re-report moved in_review: %s", w.State)
	}
	// The heartbeat leaves an owned in_review item alone: neither renewed nor lost.
	if hb := a.heartbeat([]string{"acme/web"}, id); len(hb.Lost) != 0 || len(hb.Renewed) != 0 {
		t.Fatalf("heartbeat on in_review: %+v", hb)
	}
	// The board shows the owner (mine only for A), claimed_at and history.
	v := a.board("?state=in_review").Items
	if len(v) != 1 || v[0].Owner == nil || !v[0].Owner.Mine || v[0].Owner.DeviceName != "alice" || v[0].ClaimedAt.IsZero() || v[0].Lease != nil {
		t.Fatalf("board for A: %+v", v)
	}
	if o := b.board("?state=in_review").Items[0].Owner; o.Mine {
		t.Fatal("owner shows as mine for B")
	}
	if h := v[0].History; len(h) != 2 || h[0].Action != hubproto.AttemptClaimed || h[1].Action != hubproto.AttemptInReview || h[1].PRURL != pr42 || h[1].DeviceName != "alice" {
		t.Fatalf("history: %+v", h)
	}
	// The owner updates, then completes from in_review; B can do neither.
	if r := a.do(http.MethodPost, hubproto.ProgressPath(id), progressReq(hubproto.DeliveryApproved)); r.status != 200 {
		t.Fatalf("owner update: %d", r.status)
	}
	cr := hubproto.CompleteRequest{PRURL: pr42, Delivery: hubproto.DeliveryMerged}
	if r := b.do(http.MethodPost, hubproto.CompletePath(id), cr); r.status != http.StatusConflict {
		t.Fatalf("foreign complete: %d", r.status)
	}
	if r := a.do(http.MethodPost, hubproto.CompletePath(id), cr); r.status != 200 {
		t.Fatalf("owner complete: %d %s", r.status, r.body)
	}
	done := b.board("?state=done").Items
	if len(done) != 1 || done[0].Owner == nil || done[0].Owner.DeviceName != "alice" || !done[0].ClaimedAt.IsZero() {
		t.Fatalf("done row: %+v", done)
	}
	raw := b.do(http.MethodGet, hubproto.PathBoard, nil).body
	if bytes.Contains(raw, []byte(a.id)) {
		t.Fatal("board history leaks a device id")
	}
}

func TestInReviewIdleReopensOnSweep(t *testing.T) {
	e, a, b, id := twoDaemons(t)
	a.do(http.MethodPost, hubproto.ClaimPath(id), nil)
	a.do(http.MethodPost, hubproto.ProgressPath(id), progressReq(hubproto.DeliveryCIPending))
	e.clk.Add(hubproto.InReviewIdleReopen - time.Hour)
	b.report(advisory("acme/web", hubproto.SeverityHigh)) // keeps it fresh: no stale close
	e.hub.Sweep()
	if e.item(id).State != hubproto.StateInReview {
		t.Fatal("reopened before the idle window")
	}
	e.clk.Add(time.Hour)
	b.heartbeat([]string{"acme/web"})
	b.report(advisory("acme/web", hubproto.SeverityHigh))
	if _, err := e.hub.Sweep(); err != nil {
		t.Fatal(err)
	}
	w := e.item(id)
	if w.State != hubproto.StateOpen || w.Owner != "" || w.Reopened != 1 {
		t.Fatalf("idle in_review: %+v", w)
	}
	if len(b.offers("")) != 1 {
		t.Fatal("reopened item not offered")
	}
}

func TestPreviousHolderTakesBackInsideGrace(t *testing.T) {
	e, a, b, id := twoDaemons(t)
	a.do(http.MethodPost, hubproto.ClaimPath(id), nil)
	e.clk.Add(45 * time.Minute) // lease lapsed 15 minutes ago; grace runs 60
	if len(b.offers("")) != 0 {
		t.Fatal("held item offered to B")
	}
	if got := a.offers(""); len(got) != 1 {
		t.Fatal("held item not offered back to its holder")
	}
	if hb := a.heartbeat([]string{"acme/web"}, id); len(hb.Renewed) != 1 || hb.Renewed[0].ClaimedAt.IsZero() {
		t.Fatalf("take-back by heartbeat: %+v", hb)
	}
	if w := e.item(id); w.State != hubproto.StateClaimed || w.Attempts != 0 || len(w.History) != 1 {
		t.Fatalf("after take-back: %+v", w)
	}
}

func TestMaxLeaseAgeTimesOutOnHeartbeat(t *testing.T) {
	e, a, _, id := twoDaemons(t)
	e.withServer(func(c *Config) { c.MaxLeaseAge = time.Hour })
	a.do(http.MethodPost, hubproto.ClaimPath(id), nil)
	for i := 0; i < 3; i++ {
		e.clk.Add(20 * time.Minute)
		hb := a.heartbeat([]string{"acme/web"}, id)
		if i < 2 && len(hb.Renewed) != 1 {
			t.Fatalf("renew %d: %+v", i, hb)
		}
		if i == 2 && (len(hb.Lost) != 1 || len(hb.Renewed) != 0) {
			t.Fatalf("renew past max age: %+v", hb)
		}
	}
	w := e.item(id)
	if w.State != hubproto.StateOpen || w.Attempts != 1 || w.LastFail != hubproto.FailTimeout {
		t.Fatalf("after timeout: %+v", w)
	}
	if len(e.events("lease_timeout")) != 1 {
		t.Fatal("timeout not logged")
	}
	if len(a.offers("")) != 0 {
		t.Fatal("timed-out item offered back to its device inside the cooldown")
	}
}

func TestStaleItemClosesOnlyWithAnActiveReporter(t *testing.T) {
	e, a, b, id := twoDaemons(t)
	e.clk.Add(hubproto.DefaultStaleAfter)
	// Nobody heartbeat recently: silence proves nothing.
	e.hub.Sweep()
	if e.item(id).State != hubproto.StateOpen {
		t.Fatal("closed stale without an active reporter")
	}
	b.heartbeat([]string{"acme/web"})
	if _, err := e.hub.Sweep(); err != nil {
		t.Fatal(err)
	}
	w := e.item(id)
	if w.State != hubproto.StateFailed || w.History[len(w.History)-1].Action != hubproto.AttemptClosedStale {
		t.Fatalf("stale item: %+v", w)
	}
	if len(e.events("item_closed_stale")) != 1 {
		t.Fatal("stale close not logged")
	}
	if v := a.board("?state=failed").Items; len(v) != 1 || v[0].History[len(v[0].History)-1].Reason != hubproto.FailObsolete {
		t.Fatalf("board: %+v", v)
	}
	if s := a.stats(); len(s.Days) != 2 || s.Days[1].Failed != 1 {
		t.Fatalf("stats: %+v", s)
	}
}

func TestCooldownKeepsTheItemFromItsDevice(t *testing.T) {
	_, a, b, id := twoDaemons(t)
	a.do(http.MethodPost, hubproto.ClaimPath(id), nil)
	if r := a.do(http.MethodPost, hubproto.FailPath(id), hubproto.FailRequest{Reason: hubproto.FailRefused}); r.status != 200 {
		t.Fatalf("refuse: %d", r.status)
	}
	if len(a.offers("")) != 0 {
		t.Fatal("refused item offered straight back")
	}
	if len(b.offers("")) != 1 {
		t.Fatal("refused item not offered to another device")
	}
}

func TestFailCarriesPRAndGroup(t *testing.T) {
	e, a, _, id := twoDaemons(t)
	a.do(http.MethodPost, hubproto.ClaimPath(id), nil)
	bad := hubproto.FailRequest{Reason: hubproto.FailCI, PRURL: "https://github.com/acme/api/pull/3"}
	if r := a.do(http.MethodPost, hubproto.FailPath(id), bad); r.status != http.StatusBadRequest {
		t.Fatalf("fail with another repo's PR: %d", r.status)
	}
	if r := a.do(http.MethodPost, hubproto.FailPath(id), hubproto.FailRequest{Reason: hubproto.FailCI, Group: "BAD"}); r.status != http.StatusBadRequest {
		t.Fatalf("bad group: %d", r.status)
	}
	ok := hubproto.FailRequest{Reason: hubproto.FailCI, PRURL: pr42, Group: "abcd1234"}
	if r := a.do(http.MethodPost, hubproto.FailPath(id), ok); r.status != 200 {
		t.Fatalf("fail: %d %s", r.status, r.body)
	}
	w := e.item(id)
	if w.FailGroup != "abcd1234" || w.History[len(w.History)-1].PRURL != pr42 {
		t.Fatalf("fail not recorded: %+v", w)
	}
}

func TestCriticalExhaustingAttemptsLogsEscalation(t *testing.T) {
	e := newEnv(t)
	a := e.newDaemon()
	a.mustEnroll("acme")
	a.heartbeat([]string{"acme/web"})
	a.report(advisory("acme/web", hubproto.SeverityCritical))
	id := a.offers("")[0].ID
	if n := len(e.events("escalation_opened")); n != 1 {
		t.Fatalf("critical escalation events %d", n)
	}
	for i := 0; i < 2; i++ {
		a.do(http.MethodPost, hubproto.ClaimPath(id), nil)
		a.do(http.MethodPost, hubproto.FailPath(id), hubproto.FailRequest{Reason: hubproto.FailCI})
	}
	if e.item(id).State != hubproto.StateEscalated {
		t.Fatal("not escalated")
	}
	if ev := e.events("item_escalated"); len(ev) != 1 || ev[0].Item != id {
		t.Fatalf("leaving the offers logged nothing: %+v", ev)
	}
	if n := len(e.events("escalation_opened")); n != 1 {
		t.Fatalf("second escalation opened: %d", n)
	}
}

func TestEnrollMemberAndUniqueNames(t *testing.T) {
	e := newEnv(t)
	a, b, c, other := e.newDaemon(), e.newDaemon(), e.newDaemon(), e.newDaemon()
	ra := a.enrollAs(e.labelledToken("acme", "Ada L"), "laptop")
	if ra.Member != "Ada L" || ra.DeviceName != "laptop" {
		t.Fatalf("first enroll: %+v", ra)
	}
	if rb := b.enrollAs(e.labelledToken("acme", "Bob"), "Laptop"); rb.DeviceName != "Laptop-2" || rb.Member != "Bob" {
		t.Fatalf("second enroll: %+v", rb)
	}
	if rc := c.enrollAs(e.token("acme", time.Hour), "laptop"); rc.DeviceName != "laptop-3" || rc.Member != "" {
		t.Fatalf("third enroll: %+v", rc)
	}
	// Names are unique per org, not hub-wide.
	if ro := other.enrollAs(e.token("beta", time.Hour), "laptop"); ro.DeviceName != "laptop" {
		t.Fatalf("other org: %+v", ro)
	}
	// Member and policy version show on the org view and the board.
	a.do(http.MethodPost, hubproto.PathHeartbeat, hubproto.HeartbeatRequest{
		FreeSlots: 1, Agents: []string{"claude"}, Repos: []string{"acme/web"}, LiveLeases: []string{}, PolicyVersion: 3,
	})
	var me hubproto.DeviceView
	for _, dv := range b.org().Devices {
		if dv.Name == "laptop" {
			me = dv
		}
	}
	if me.Member != "Ada L" || me.PolicyVersion != 3 {
		t.Fatalf("org device: %+v", me)
	}
	a.report(advisory("acme/web", hubproto.SeverityHigh))
	id := a.offers("")[0].ID
	a.do(http.MethodPost, hubproto.ClaimPath(id), nil)
	if l := b.board("?state=claimed").Items[0].Lease; l.Member != "Ada L" || l.DeviceName != "laptop" {
		t.Fatalf("lease view: %+v", l)
	}
	if h := b.board("").Items[0].History; len(h) != 1 || h[0].Member != "Ada L" {
		t.Fatalf("history member: %+v", h)
	}
}

func TestStatsCountsPerOrg(t *testing.T) {
	e, a, _, id := twoDaemons(t)
	other := e.newDaemon()
	other.enrollNamed("beta", "eve")
	other.heartbeat([]string{"beta/x"})
	if r := a.do(http.MethodGet, hubproto.PathStats+"?days=3", nil); r.status != http.StatusBadRequest {
		t.Fatalf("query accepted: %d", r.status)
	}
	// Same dedup key: a re-report, not an item, but it turns critical and
	// opens the escalation.
	a.report(advisory("acme/web", hubproto.SeverityCritical))
	a.report(redBump("acme/web", 5))
	a.do(http.MethodPost, hubproto.ClaimPath(id), nil)
	for i := 0; i < 6; i++ {
		e.clk.Add(20 * time.Minute)
		a.heartbeat([]string{"acme/web"}, id)
	}
	a.do(http.MethodPost, hubproto.CompletePath(id), hubproto.CompleteRequest{PRURL: pr42, Delivery: hubproto.DeliveryMerged})
	s := a.stats()
	if len(s.Days) != 1 {
		t.Fatalf("days: %+v", s.Days)
	}
	d := s.Days[0]
	if d.Date != "2026-05-01" || d.Opened != 2 || d.Done != 1 || d.Escalated != 1 || d.FixSecondsSum != int64(2*time.Hour/time.Second) {
		t.Fatalf("day: %+v", d)
	}
	if got := other.stats(); len(got.Days) != 0 {
		t.Fatalf("beta sees acme's stats: %+v", got)
	}
	// The view keeps the last StatsDays days only.
	e.clk.Add(hubproto.StatsDays * 24 * time.Hour)
	a.report(redBump("acme/web", 6))
	if s := a.stats(); len(s.Days) != 1 || s.Days[0].Opened != 1 {
		t.Fatalf("window: %+v", s.Days)
	}
}
