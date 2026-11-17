package remote

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/devices"
	"github.com/sushidev-team/lola/internal/protocol"
)

// fakeGate is the daemon's policy: panes by member and level, share rvs.
type fakeGate struct {
	mu     sync.Mutex
	rvs    map[string]bool
	levels map[string]string // memberID+"|"+pane -> level
}

func (g *fakeGate) KnownRV(rv string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.rvs[rv]
}

func (g *fakeGate) PaneAccess(member, pane string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.levels[member+"|"+pane]
}

func (g *fakeGate) set(member, pane, level string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if level == "" {
		delete(g.levels, member+"|"+pane)
		return
	}
	g.levels[member+"|"+pane] = level
}

func (g *fakeGate) Roster(member string, att []Attachment) (protocol.ShareSessionsData, error) {
	d := protocol.ShareSessionsData{Sessions: []protocol.SharedSession{}}
	for _, a := range att {
		d.Sessions = append(d.Sessions, protocol.SharedSession{ID: "x", Pane: a.Pane, Attached: []protocol.ShareViewer{{Name: a.Name, Me: a.MemberID == member}}})
	}
	return d, nil
}

type memberRig struct {
	*relayRig
	members *devices.Registry
	gate    *fakeGate
	key     *DeviceKey
	dev     devices.Device
	bus     *fakeBus
}

const shareRV = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func newMemberRig(t *testing.T) *memberRig {
	t.Helper()
	m := &memberRig{
		members: devices.OpenMembers(t.TempDir(), nil, nil),
		gate:    &fakeGate{rvs: map[string]bool{shareRV: true}, levels: map[string]string{}},
		key:     newTestKey(t),
		bus:     newFakeBus(),
	}
	l := startTestListener(t, &stubHandler{}, m.bus, func(o *Options) {
		o.RelayOnly = true
		o.Members, o.MemberGate = m.members, m.gate
	})
	m.relayRig = &relayRig{l}
	var err error
	m.dev, err = m.members.AddMember("ada's mini", m.key.SPKISHA256(),
		devices.HubIdentity{DeviceID: "abc", PublicKey: "k", Fingerprint: "0000 1111"})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (m *memberRig) dialRV(rv string) *client {
	cp, err := m.serve(rv)
	if err != nil {
		m.t.Fatalf("serve: %v", err)
	}
	tc := tls.Client(cp, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13,
		Certificates: []tls.Certificate{m.key.Certificate}})
	return &client{t: m.t, conn: tc, fr: protocol.NewFrameReader(tc), fw: protocol.NewFrameWriter(tc)}
}

func (m *memberRig) sub(c *client, pane string) {
	m.t.Helper()
	f := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameSub, ID: "s", Pane: pane}
	_ = f.SetPayload(protocol.SubPayload{Cols: 80, Rows: 24})
	if err := c.fw.WriteFrame(&f); err != nil {
		m.t.Fatal(err)
	}
}

func (m *memberRig) write(c *client, pane, text string) {
	m.t.Helper()
	f := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FramePTY, Pane: pane}
	_ = f.SetPayload(protocol.PTYInputPayload{Action: protocol.PTYActionWrite, Data: []byte(text)})
	if err := c.fw.WriteFrame(&f); err != nil {
		m.t.Fatal(err)
	}
}

func wantErrCode(t *testing.T, c *client, code string) {
	t.Helper()
	f := c.mustRead()
	if f.Type != protocol.FrameErr {
		t.Fatalf("frame = %s, want err %s", f.Type, code)
	}
	var e protocol.ErrPayload
	_ = json.Unmarshal(f.Payload, &e)
	if e.Code != code {
		t.Fatalf("code = %q, want %q", e.Code, code)
	}
}

func TestMemberMustComeThroughAShareRendezvous(t *testing.T) {
	m := newMemberRig(t)
	// On a phone's rv, or any rv that is not a published share: not paired.
	c := m.dialRV(testRV)
	_ = c.conn.SetDeadline(time.Now().Add(testTimeout))
	if _, err := c.read(); err == nil {
		if f, _ := c.read(); f.Type == protocol.FrameReady {
			t.Fatal("member admitted on a rendezvous that is not a share's")
		}
	}
	// A member over the LAN listener never authenticates (relay only).
	l := startTestListener(t, &stubHandler{}, m.bus, func(o *Options) { o.Members, o.MemberGate = m.members, m.gate })
	lc := dialCert(t, l.srv.Addrs()[0].Addr, m.key.Certificate)
	if f, err := lc.read(); err == nil && f.Type == protocol.FrameReady {
		t.Fatal("member admitted over the LAN")
	}
}

func TestMemberAttachScopeAndLevels(t *testing.T) {
	m := newMemberRig(t)
	m.gate.set(m.dev.ID, "lola-a", LevelView)
	m.gate.set(m.dev.ID, "lola-b", LevelType)
	c := m.dialRV(shareRV)
	rd := c.wantReady()
	if rd.Via != ViaRelay || len(rd.Tiers) != 0 {
		t.Fatalf("ready = %+v: a member holds no tier", rd)
	}

	// A pane that is not shared is "not available", like one that does not exist.
	m.sub(c, "lola-other")
	wantErrCode(t, c, protocol.CodeUnknownPane)

	// view: attach works, keystrokes are denied and never reach the bus.
	m.sub(c, "lola-a")
	if f := c.mustRead(); f.Type != protocol.FrameResync || f.Pane != "lola-a" {
		t.Fatalf("view attach: %s %s", f.Type, f.Pane)
	}
	m.write(c, "lola-a", "rm -rf ~\n")
	wantErrCode(t, c, protocol.CodeDenied)

	// A viewer must not scroll the owner's pane either.
	f := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FramePTY, Pane: "lola-a"}
	_ = f.SetPayload(protocol.PTYInputPayload{Action: protocol.PTYActionScroll, Lines: 5})
	if err := c.fw.WriteFrame(&f); err != nil {
		t.Fatal(err)
	}
	wantErrCode(t, c, protocol.CodeDenied)
	if _, _, scrolls := m.bus.snapshot(); len(scrolls) != 0 {
		t.Fatalf("a view-level scroll reached the pane: %v", scrolls)
	}

	// type: keystrokes reach the pane.
	m.sub(c, "lola-b")
	_ = c.mustRead()
	m.write(c, "lola-b", "ls\n")
	deadline := time.Now().Add(testTimeout)
	for {
		_, writes, _ := m.bus.snapshot()
		if len(writes) == 1 && writes[0] != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("type-level write never reached the bus: %v", writes)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, writes, _ := m.bus.snapshot(); len(writes) != 1 {
		t.Fatalf("writes = %v: the view pane's keystrokes leaked", writes)
	}
}

func TestMemberCannotReachAnythingElse(t *testing.T) {
	m := newMemberRig(t)
	c := m.dialRV(shareRV)
	c.wantReady()
	for _, cmd := range []string{"sessions", "open", "kill", "shellCreate", "hookEvent", "stop", "diff", "openURL", protocol.CmdWatch} {
		c.req("r-"+cmd, cmd, protocol.Request{})
		wantErrCode(t, c, protocol.CodeDenied)
	}
	// The one command it has works, and answers with the roster.
	c.req("ok", protocol.CmdShareSessions, nil)
	if f := c.mustRead(); f.Type != protocol.FrameResp || f.ID != "ok" {
		t.Fatalf("roster reply = %s/%s", f.Type, f.ID)
	}
}

func TestUnsharingEndsTheSubscriptionAtOnce(t *testing.T) {
	m := newMemberRig(t)
	m.gate.set(m.dev.ID, "lola-a", LevelType)
	c := m.dialRV(shareRV)
	c.wantReady()
	m.sub(c, "lola-a")
	_ = c.mustRead()
	if got := m.srv.MemberAttachments(); len(got) != 1 || got[0].Pane != "lola-a" || got[0].MemberID != m.dev.ID {
		t.Fatalf("attachments = %+v", got)
	}
	m.gate.set(m.dev.ID, "lola-a", LevelNone)
	if n := m.srv.RecheckMembers(); n != 1 {
		t.Fatalf("RecheckMembers = %d, want 1", n)
	}
	wantErrCode(t, c, protocol.CodeUnknownPane)
	if got := m.srv.MemberAttachments(); len(got) != 0 {
		t.Fatalf("attachment survived: %+v", got)
	}
	// Even a resubscribe or a write racing the unshare is refused.
	m.write(c, "lola-a", "x")
	wantErrCode(t, c, protocol.CodeDenied)
}

func TestRevokedMemberIsDisconnected(t *testing.T) {
	m := newMemberRig(t)
	c := m.dialRV(shareRV)
	c.wantReady()
	if _, err := m.members.Revoke(m.dev.ID); err != nil {
		t.Fatal(err)
	}
	m.srv.RecheckMembers()
	c.wantClosed()
	// The pinned key no longer completes a handshake on the share rv.
	c2 := m.dialRV(shareRV)
	if f, err := c2.read(); err == nil && f.Type == protocol.FrameReady {
		t.Fatal("revoked member reconnected")
	}
}

func TestMemberRosterIsRateLimited(t *testing.T) {
	m := newMemberRig(t)
	c := m.dialRV(shareRV)
	c.wantReady()
	limited := false
	for i := 0; i < 600 && !limited; i++ {
		c.req("r", protocol.CmdShareSessions, nil)
		f := c.mustRead()
		if f.Type == protocol.FrameErr {
			var e protocol.ErrPayload
			_ = json.Unmarshal(f.Payload, &e)
			limited = e.Code == protocol.CodeRateLimited
		}
	}
	if !limited {
		t.Fatal("a member could ask for the roster without limit")
	}
}

// revokingBus withdraws the member's access while Subscribe is still running,
// after the frame was admitted and before the subscription is installed.
type revokingBus struct {
	*fakeBus
	revoke func()
}

func (b *revokingBus) Subscribe(ctx context.Context, pane string, cols, rows int) (PaneStream, error) {
	st, err := b.fakeBus.Subscribe(ctx, pane, cols, rows)
	b.revoke()
	return st, err
}

func TestAccessWithdrawnDuringSubscribeIsNotStreamed(t *testing.T) {
	m := newMemberRig(t)
	m.gate.set(m.dev.ID, "lola-a", LevelView)
	m.srv.opts.Panes = &revokingBus{fakeBus: m.bus, revoke: func() {
		m.gate.set(m.dev.ID, "lola-a", LevelNone)
		m.srv.RecheckMembers() // runs before the subscription exists
	}}
	c := m.dialRV(shareRV)
	c.wantReady()
	m.sub(c, "lola-a")
	wantErrCode(t, c, protocol.CodeUnknownPane)
	if got := m.srv.MemberAttachments(); len(got) != 0 {
		t.Fatalf("a revoked member kept a subscription: %+v", got)
	}
}
