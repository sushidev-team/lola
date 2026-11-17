package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/hubproto"
)

func (d *daemon) share(title string, members ...string) hubproto.ShareEntry {
	d.e.t.Helper()
	id, _ := hubproto.NewShareID()
	rv, _ := hubproto.NewRelayID()
	return hubproto.ShareEntry{ID: id, RV: rv, Title: title, Agent: "claude", Level: hubproto.LevelView, Members: members}
}

func (d *daemon) publish(entries ...hubproto.ShareEntry) result {
	return d.do(http.MethodPost, hubproto.PathShares, hubproto.ShareSet{Shares: entries})
}

func (d *daemon) listShares() hubproto.ShareList {
	d.e.t.Helper()
	r := d.do(http.MethodGet, hubproto.PathShares, nil)
	if r.status != 200 {
		d.e.t.Fatalf("list shares: %d %s", r.status, r.body)
	}
	var l hubproto.ShareList
	r.decode(d.e.t, &l)
	return l
}

func TestSharesAudienceAndOrgIsolation(t *testing.T) {
	e := relayEnv(t)
	owner, mate, other := relayDaemon(e), relayDaemon(e), relayDaemon(e)
	stranger := e.newDaemon()
	stranger.mustEnroll("beta")

	open := owner.share("office mini")
	only := owner.share("just for mate", mate.id)
	if r := owner.publish(open, only); r.status != 200 {
		t.Fatalf("publish: %d %s", r.status, r.body)
	}
	if got := len(mate.listShares().Shares); got != 2 {
		t.Fatalf("mate sees %d shares, want 2", got)
	}
	if got := other.listShares().Shares; len(got) != 1 || got[0].ID != open.ID {
		t.Fatalf("other sees %+v, want only the open share", got)
	}
	if got := stranger.listShares().Shares; len(got) != 0 {
		t.Fatalf("another org sees %+v", got)
	}
	if got := owner.listShares().Shares; len(got) != 0 {
		t.Fatalf("owner lists its own shares: %+v", got)
	}
	// A listing never carries the rendezvous.
	r := mate.do(http.MethodGet, hubproto.PathShares, nil)
	if contains(r.body, open.RV) {
		t.Fatal("listing leaked an rv")
	}
	// Replace-all: an empty set unshares everything.
	if r := owner.publish(); r.status != 200 {
		t.Fatal(r.status)
	}
	if got := mate.listShares().Shares; len(got) != 0 {
		t.Fatalf("after unshare: %+v", got)
	}
}

func contains(b []byte, s string) bool {
	return len(s) > 0 && string(b) != "" && indexOf(string(b), s) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

func TestSharesRefuseAnotherDevicesIDs(t *testing.T) {
	e := relayEnv(t)
	a, b := relayDaemon(e), relayDaemon(e)
	s := a.share("a")
	if r := a.publish(s); r.status != 200 {
		t.Fatal(r.status)
	}
	stolen := b.share("b")
	stolen.RV = s.RV
	if r := b.publish(stolen); r.status != http.StatusConflict {
		t.Fatalf("rv of another device: %d", r.status)
	}
	clash := b.share("b")
	clash.ID = s.ID
	if r := b.publish(clash); r.status != http.StatusConflict {
		t.Fatalf("share id of another device: %d", r.status)
	}
	// A phone rendezvous of another device is taken too.
	c := b.mustCred()
	steal := a.share("a2")
	steal.RV = c.rv
	if r := a.publish(steal); r.status != http.StatusConflict {
		t.Fatalf("phone rv of another device: %d", r.status)
	}
}

func TestSharesInvalidSetIsRefused(t *testing.T) {
	e := relayEnv(t)
	a := relayDaemon(e)
	s := a.share("x")
	s.Level = "root"
	if r := a.publish(s); r.status != http.StatusBadRequest {
		t.Fatalf("bad level: %d", r.status)
	}
}

func TestShareIdentityCannotBeForged(t *testing.T) {
	e := relayEnv(t)
	a, b := relayDaemon(e), relayDaemon(e)
	const spki = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	good, _ := hubproto.SignBinding(a.priv, spki)
	if r := a.do(http.MethodPost, hubproto.PathShareIdentity, good); r.status != 200 {
		t.Fatalf("identity: %d %s", r.status, r.body)
	}
	// b cannot publish a binding signed by a's key (or any key but its own).
	if r := b.do(http.MethodPost, hubproto.PathShareIdentity, good); r.status != http.StatusBadRequest {
		t.Fatalf("binding of another device: %d", r.status)
	}
	r := b.do(http.MethodGet, hubproto.PathShareMembers, nil)
	if r.status != 200 {
		t.Fatalf("members: %d", r.status)
	}
	var l hubproto.MemberList
	r.decode(t, &l)
	if len(l.Members) != 1 || l.Members[0].DeviceID != a.id || l.Members[0].Identity.SPKI != spki {
		t.Fatalf("members = %+v", l.Members)
	}
	if err := l.Validate(); err != nil {
		t.Fatalf("hub list does not validate: %v", err)
	}
	// The asking device is excluded.
	r = a.do(http.MethodGet, hubproto.PathShareMembers, nil)
	var self hubproto.MemberList
	r.decode(t, &self)
	if len(self.Members) != 0 {
		t.Fatalf("own device listed: %+v", self.Members)
	}
}

func TestMemberDialReachesOwnerOnlyInAudience(t *testing.T) {
	e := relayEnv(t)
	owner, mate, out := relayDaemon(e), relayDaemon(e), relayDaemon(e)
	s := owner.share("mini", mate.id)
	if r := owner.publish(s); r.status != 200 {
		t.Fatal(r.status)
	}
	ctrl := owner.control()

	// Outside the audience: refused like an unknown share, no offer made.
	if _, err := out.dialWS(hubproto.PathRelayMember + "?share=" + s.ID); err == nil {
		t.Fatal("dial outside the audience succeeded")
	}
	unknown, _ := hubproto.NewShareID()
	if _, err := mate.dialWS(hubproto.PathRelayMember + "?share=" + unknown); err == nil {
		t.Fatal("dial for an unknown share succeeded")
	}

	type res struct {
		ws  *hubproto.WSConn
		err error
	}
	ch := make(chan res, 1)
	go func() { ws, err := mate.dialWS(hubproto.PathRelayMember + "?share=" + s.ID); ch <- res{ws, err} }()
	m := readMsg(t, ctrl)
	if m.T != "incoming" || m.RV != s.RV {
		t.Fatalf("incoming = %+v", m)
	}
	leg, err := owner.dialWS(hubproto.PathRelayAccept + "?conn=" + m.Conn)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	t.Cleanup(func() { _ = leg.CloseNow(hubproto.WSCloseGoingAway) })
	r := <-ch
	if r.err != nil {
		t.Fatalf("member: %v", r.err)
	}
	t.Cleanup(func() { _ = r.ws.CloseNow(hubproto.WSCloseGoingAway) })
	if _, err := r.ws.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := leg.Read(buf); err != nil || string(buf) != "hello" {
		t.Fatalf("splice: %q %v", buf, err)
	}
}

func TestMemberDialOfflineOwner(t *testing.T) {
	e := relayEnv(t)
	owner, mate := relayDaemon(e), relayDaemon(e)
	s := owner.share("mini")
	if r := owner.publish(s); r.status != 200 {
		t.Fatal(r.status)
	}
	if got := mate.listShares().Shares; len(got) != 1 || got[0].Online {
		t.Fatalf("offline owner listed online: %+v", got)
	}
	owner.control()
	if got := mate.listShares().Shares; len(got) != 1 || !got[0].Online {
		t.Fatalf("online owner listed offline: %+v", got)
	}
}
