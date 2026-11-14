package api

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

func (d *daemon) enrollNamed(org, name string) {
	d.e.t.Helper()
	r := d.do(http.MethodPost, hubproto.PathEnroll, hubproto.EnrollRequest{
		Token: d.e.token(org, time.Hour), DeviceName: name, PublicKey: hubproto.EncodePublicKey(d.pub),
	})
	if r.status != 200 {
		d.e.t.Fatalf("enroll: %d %s", r.status, r.body)
	}
}

func (d *daemon) board(query string) hubproto.BoardResponse {
	d.e.t.Helper()
	r := d.do(http.MethodGet, hubproto.PathBoard+query, nil)
	if r.status != 200 {
		d.e.t.Fatalf("board: %d %s", r.status, r.body)
	}
	var out hubproto.BoardResponse
	if err := hubproto.DecodeStrictLimit(bytes.NewReader(r.body), hubproto.MaxViewBody, &out); err != nil {
		d.e.t.Fatalf("board decode: %v", err)
	}
	if err := out.Validate(); err != nil {
		d.e.t.Fatalf("board does not validate: %v", err)
	}
	return out
}

func (d *daemon) org() hubproto.OrgView {
	d.e.t.Helper()
	r := d.do(http.MethodGet, hubproto.PathOrg, nil)
	if r.status != 200 {
		d.e.t.Fatalf("org: %d %s", r.status, r.body)
	}
	var out hubproto.OrgView
	if err := hubproto.DecodeStrictLimit(bytes.NewReader(r.body), hubproto.MaxViewBody, &out); err != nil {
		d.e.t.Fatalf("org decode: %v", err)
	}
	if err := out.Validate(); err != nil {
		d.e.t.Fatalf("org does not validate: %v", err)
	}
	return out
}

func TestBoardShowsTheOrgWithMineAndHolderName(t *testing.T) {
	e := newEnv(t)
	a, b, other := e.newDaemon(), e.newDaemon(), e.newDaemon()
	a.enrollNamed("acme", "alice laptop")
	b.enrollNamed("acme", "bob-mac")
	other.enrollNamed("beta", "eve")
	a.heartbeat([]string{"acme/web"})
	b.heartbeat([]string{"acme/web"})
	other.heartbeat([]string{"beta/x"})
	a.report(advisory("acme/web", hubproto.SeverityHigh))
	e.clk.Add(time.Second)
	a.report(redBump("acme/web", 9))
	other.report(advisory("beta/x", hubproto.SeverityHigh))

	if got := other.board(""); len(got.Items) != 1 || got.Items[0].Item.Repo() != "beta/x" {
		t.Fatalf("org isolation broken: %+v", got.Items)
	}
	all := a.board("")
	if len(all.Items) != 2 || all.Items[0].Item.Kind != hubproto.KindRedBumpPR {
		t.Fatalf("board order (newest first): %+v", all.Items)
	}
	id := all.Items[1].Item.ID
	if r := b.do(http.MethodPost, hubproto.ClaimPath(id), nil); r.status != 200 {
		t.Fatalf("claim %d", r.status)
	}
	claimed := a.board("?state=claimed")
	if len(claimed.Items) != 1 || claimed.Items[0].Lease == nil {
		t.Fatalf("claimed filter: %+v", claimed.Items)
	}
	if l := claimed.Items[0].Lease; l.Mine || l.DeviceName != "bob-mac" {
		t.Fatalf("lease view for a: %+v", l)
	}
	if l := b.board("?state=claimed").Items[0].Lease; !l.Mine {
		t.Fatalf("lease view for b: %+v", l)
	}
	if got := a.board("?limit=1"); len(got.Items) != 1 {
		t.Fatalf("limit: %d", len(got.Items))
	}

	// Complete: the outcome shows.
	r := b.do(http.MethodPost, hubproto.CompletePath(id), hubproto.CompleteRequest{
		PRURL: "https://github.com/acme/web/pull/77", Delivery: hubproto.DeliveryCIPending,
	})
	if r.status != 200 {
		t.Fatalf("complete %d %s", r.status, r.body)
	}
	done := a.board("?state=done")
	if len(done.Items) != 1 || done.Items[0].Outcome == nil || done.Items[0].Outcome.PRURL != "https://github.com/acme/web/pull/77" {
		t.Fatalf("done: %+v", done.Items)
	}
	// Nothing about devices beyond the name travels.
	raw := a.do(http.MethodGet, hubproto.PathBoard, nil).body
	if bytes.Contains(raw, []byte(b.id)) || bytes.Contains(raw, []byte(hubproto.EncodePublicKey(b.pub))) {
		t.Fatal("board leaks a device id or key")
	}
}

func TestBoardExpiresLapsedLeasesBeforeShowing(t *testing.T) {
	e := newEnv(t)
	a := e.newDaemon()
	a.enrollNamed("acme", "alice")
	a.heartbeat([]string{"acme/web"})
	a.report(advisory("acme/web", hubproto.SeverityHigh))
	id := a.offers("")[0].ID
	a.do(http.MethodPost, hubproto.ClaimPath(id), nil)
	e.clk.Add(31 * time.Minute)
	got := a.board("")
	if got.Items[0].State != hubproto.StateOpen || got.Items[0].Lease != nil {
		t.Fatalf("lapsed lease shown: %+v", got.Items[0])
	}
}

func TestBoardEscalationIdentifier(t *testing.T) {
	e := newEnv(t)
	a := e.newDaemon()
	a.enrollNamed("acme", "alice")
	a.heartbeat([]string{"acme/web"})
	a.report(advisory("acme/web", hubproto.SeverityCritical))
	id := a.offers("")[0].ID
	if v := a.board("").Items[0].Escalation; v == nil || !v.Pending || v.Identifier != "" {
		t.Fatalf("pending escalation: %+v", v)
	}
	_ = e.st.Update(func(d *store.Data) error {
		d.Items[id].Escalation.Pending = false
		d.Items[id].Escalation.LinearID = "ENG-42"
		return nil
	})
	if v := a.board("").Items[0].Escalation; v.Pending || v.Identifier != "ENG-42" {
		t.Fatalf("sent escalation: %+v", v)
	}
	// A stored identifier that is not one is dropped, not shipped.
	_ = e.st.Update(func(d *store.Data) error { d.Items[id].Escalation.LinearID = "<b>x</b>"; return nil })
	if v := a.board("").Items[0].Escalation; v.Identifier != "" {
		t.Fatalf("bad identifier shipped: %+v", v)
	}
}

func TestBoardQueryRejected(t *testing.T) {
	e := newEnv(t)
	a := e.newDaemon()
	a.enrollNamed("acme", "alice")
	for _, q := range []string{"?state=nope", "?limit=0", "?limit=501", "?limit=01", "?evil=1", "?state=open&state=done"} {
		if r := a.do(http.MethodGet, hubproto.PathBoard+q, nil); r.status != 400 {
			t.Fatalf("%s: %d", q, r.status)
		}
	}
	if r := a.do(http.MethodPost, hubproto.PathBoard, nil); r.status != 405 {
		t.Fatalf("post board %d", r.status)
	}
	if r := a.do(http.MethodGet, hubproto.PathOrg+"?x=1", nil); r.status != 400 {
		t.Fatalf("org query %d", r.status)
	}
	// Unsigned is refused.
	resp, err := http.Get(e.srv.URL + hubproto.PathBoard)
	if err != nil || resp.StatusCode != 401 {
		t.Fatalf("unsigned board %v %d", err, resp.StatusCode)
	}
	resp.Body.Close()
}

func TestOrgViewDevicesAndPolicy(t *testing.T) {
	e := newEnv(t)
	a, b, c, other := e.newDaemon(), e.newDaemon(), e.newDaemon(), e.newDaemon()
	a.enrollNamed("acme", "alice")
	b.enrollNamed("acme", "bob")
	c.enrollNamed("acme", "carol")
	other.enrollNamed("beta", "eve")
	a.heartbeat([]string{"acme/secret-repo"})
	_ = e.st.Update(func(d *store.Data) error {
		now := e.clk.Now()
		d.Devices[c.id].RevokedAt = &now
		return nil
	})
	e.clk.Add(20 * time.Minute)
	b.heartbeat(nil)

	v := a.org()
	if v.Org != "acme" || v.PolicyVersion != 0 || len(v.Devices) != 2 {
		t.Fatalf("org view %+v", v)
	}
	al, bo := v.Devices[0], v.Devices[1]
	if al.Name != "alice" || !al.Me || al.Active || al.FreeSlots != 2 || len(al.Agents) != 1 {
		t.Fatalf("alice %+v", al)
	}
	if bo.Name != "bob" || bo.Me || !bo.Active {
		t.Fatalf("bob %+v", bo)
	}
	raw := a.do(http.MethodGet, hubproto.PathOrg, nil).body
	for _, leak := range []string{"secret-repo", a.id, b.id, "eve", "carol", hubproto.EncodePublicKey(b.pub)} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("org view leaks %q", leak)
		}
	}

	apub, apriv, _ := hubproto.GenerateKey()
	p, _ := hubproto.SignPolicy(apriv, hubproto.KeyIDFor(apub), hubproto.Policy{Org: "acme", Version: 4, IssuedAt: e.clk.Now(),
		Settings: hubproto.Settings{EscalateAfter: 3}})
	_ = e.st.Update(func(d *store.Data) error { d.Orgs["acme"].Policy = &p; return nil })
	if v := a.org(); v.PolicyVersion != 4 {
		t.Fatalf("policy version %d", v.PolicyVersion)
	}
}

// A stored item can keep an owner or a claimed_at the wire refuses for its
// state (an older hub, a hand-edited store). ItemView must leave those parts
// out so the row still validates: the daemon drops the WHOLE board when one
// row is bad. Owner shows only on in_review, done, failed and escalated;
// claimed_at only on claimed and in_review.
func TestItemViewOwnerAndClaimedAtOnlyWhereTheWireAllows(t *testing.T) {
	const devID = "0123456789abcdef0123456789abcdef"
	const itemID = "fedcba9876543210fedcba9876543210"
	if !hubproto.ValidItemID(itemID) {
		t.Fatalf("test item id %q does not validate", itemID)
	}
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	d := &store.Data{Devices: map[string]*store.Device{
		devID: {ID: devID, Org: "acme", Name: "alice", Member: "Alice"},
	}}
	owner := map[hubproto.State]bool{
		hubproto.StateInReview: true, hubproto.StateDone: true,
		hubproto.StateFailed: true, hubproto.StateEscalated: true,
	}
	claimed := map[hubproto.State]bool{hubproto.StateClaimed: true, hubproto.StateInReview: true}
	for _, st := range []hubproto.State{
		hubproto.StateOpen, hubproto.StateClaimed, hubproto.StateInReview,
		hubproto.StateDone, hubproto.StateFailed, hubproto.StateEscalated,
	} {
		w := &store.WorkItem{
			ID: itemID, Org: "acme", Item: advisory("acme/web", hubproto.SeverityHigh), State: st,
			FirstSeen: now.Add(-time.Hour), LastSeen: now,
			Owner: devID, ClaimedAt: now.Add(-30 * time.Minute),
		}
		if st == hubproto.StateClaimed {
			w.Lease = &hubproto.Lease{ItemID: itemID, Device: devID, Expires: now.Add(time.Hour), ClaimedAt: w.ClaimedAt}
		}
		v := ItemView(d, w, devID)
		if err := v.Validate(); err != nil {
			t.Fatalf("%s: row does not validate: %v", st, err)
		}
		if got := v.Owner != nil; got != owner[st] {
			t.Fatalf("%s: owner shown = %v, want %v", st, got, owner[st])
		}
		if v.Owner != nil && (v.Owner.DeviceName != "alice" || v.Owner.Member != "Alice" || !v.Owner.Mine) {
			t.Fatalf("%s: owner %+v", st, v.Owner)
		}
		if got := !v.ClaimedAt.IsZero(); got != claimed[st] {
			t.Fatalf("%s: claimed_at shown = %v, want %v", st, got, claimed[st])
		}
	}
}
