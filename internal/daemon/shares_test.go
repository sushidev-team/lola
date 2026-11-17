package daemon

import (
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/devices"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
	"github.com/sushidev-team/lola/internal/session"
)

// shareHub answers the share endpoints; everything else goes to the relay fake.
type shareHub struct {
	*relayHubFake
	srv *httptest.Server

	mu      sync.Mutex
	members hubproto.MemberList
	set     *hubproto.ShareSet
}

func newShareHub(t *testing.T) *shareHub {
	t.Helper()
	sh := &shareHub{relayHubFake: newRelayHubFake(t)}
	sh.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sh.mu.Lock()
		defer sh.mu.Unlock()
		switch r.URL.Path {
		case hubproto.PathShareMembers:
			_ = json.NewEncoder(w).Encode(sh.members)
		case hubproto.PathShares:
			if r.Method == http.MethodPost {
				var set hubproto.ShareSet
				if err := json.NewDecoder(r.Body).Decode(&set); err != nil || set.Validate() != nil {
					w.WriteHeader(400)
					return
				}
				sh.set = &set
				_, _ = w.Write([]byte("{}"))
				return
			}
			_ = json.NewEncoder(w).Encode(hubproto.ShareList{Shares: []hubproto.ShareView{}})
		case hubproto.PathShareIdentity:
			_, _ = w.Write([]byte("{}"))
		default:
			sh.relayHubFake.serve(w, r)
		}
	}))
	t.Cleanup(sh.srv.Close)
	return sh
}

// teammate adds a member row the hub lists.
func (sh *shareHub) teammate(t *testing.T, name string) (hubproto.MemberView, ed25519.PublicKey) {
	t.Helper()
	pub, priv, _ := hubproto.GenerateKey()
	id, err := hubproto.SignBinding(priv, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	v := hubproto.MemberView{DeviceID: hubproto.DeviceID(pub), Name: name, Member: "Ada", PublicKey: hubproto.EncodePublicKey(pub), Identity: id}
	sh.mu.Lock()
	sh.members.Members = append(sh.members.Members, v)
	sh.mu.Unlock()
	return v, pub
}

func newShareDaemon(t *testing.T) (*relayDaemon, *shareHub) {
	t.Helper()
	sh := newShareHub(t)
	rd := newRelayDaemon(t, sh.relayHubFake)
	// Phones are OFF: [remote] disabled, hub joined. Only sharing may bring a server up.
	rd.mu.Lock()
	rd.cfg.Remote = config.RemoteConfig{}
	rd.cfg.Hub = config.HubConfig{Enabled: true, URL: sh.srv.URL}
	rd.mu.Unlock()
	rd.join(t, sh.srv.URL)
	return rd, sh
}

func (rd *relayDaemon) addSession(id string, mod func(*session.Session)) {
	s := session.Session{ID: id, TmuxName: id, Agent: "claude", Title: "title " + id, Kind: session.KindLinear}
	if mod != nil {
		mod(&s)
	}
	rd.sessions.Upsert(s)
}

func (rd *relayDaemon) pin(t *testing.T, m hubproto.MemberView, pub ed25519.PublicKey) devices.Device {
	t.Helper()
	var spki [32]byte
	spki[0] = byte(len(m.Name))
	dev, err := rd.members.AddMember(m.Name, spki, devices.HubIdentity{
		DeviceID: m.DeviceID, PublicKey: m.PublicKey, Fingerprint: hubproto.Fingerprint(pub), Member: m.Member})
	if err != nil {
		t.Fatal(err)
	}
	return dev
}

func shareDo[T any](t *testing.T, d *Daemon, cmd string, args any) (T, error) {
	t.Helper()
	return hubDo[T](t, d, cmd, args)
}

func TestShareGateFollowsTheStore(t *testing.T) {
	rd, sh := newShareDaemon(t)
	m1, k1 := sh.teammate(t, "ada-mini")
	m2, k2 := sh.teammate(t, "bob-laptop")
	d1, d2 := rd.pin(t, m1, k1), rd.pin(t, m2, k2)
	rd.addSession("lola-a", nil)
	rd.addSession("lola-b", nil)
	gate := shareGate{rd.Daemon}

	if got := gate.PaneAccess(d1.ID, "lola-a"); got != remote.LevelNone {
		t.Fatalf("an unshared session is reachable: %q", got)
	}
	rd.sessions.Update("lola-a", func(s *session.Session) bool {
		s.Share = session.Share{ID: strings.Repeat("1", 32), RV: strings.Repeat("2", 32), Title: "a", Level: hubproto.LevelView}
		return true
	})
	if got := gate.PaneAccess(d1.ID, "lola-a"); got != remote.LevelView {
		t.Fatalf("shared with everyone, view: %q", got)
	}
	// Only the agent pane of the shared session: not another session, not an aux shell.
	for _, pane := range []string{"lola-b", "lola-a-shell-1", "lola-a-dev-1", ""} {
		if got := gate.PaneAccess(d1.ID, pane); got != remote.LevelNone {
			t.Errorf("pane %q reachable: %q", pane, got)
		}
	}
	// An audience narrows it to the named member.
	rd.sessions.Update("lola-a", func(s *session.Session) bool {
		s.Share.With, s.Share.Level = []string{m1.DeviceID}, hubproto.LevelType
		return true
	})
	if got := gate.PaneAccess(d1.ID, "lola-a"); got != remote.LevelType {
		t.Errorf("named member: %q", got)
	}
	if got := gate.PaneAccess(d2.ID, "lola-a"); got != remote.LevelNone {
		t.Errorf("a member outside the audience reaches it: %q", got)
	}
	// An unknown (unpinned or revoked) member id reaches nothing.
	if got := gate.PaneAccess("dnope", "lola-a"); got != remote.LevelNone {
		t.Errorf("unknown member: %q", got)
	}
	if _, err := rd.members.Revoke(d1.ID); err != nil {
		t.Fatal(err)
	}
	if got := gate.PaneAccess(d1.ID, "lola-a"); got != remote.LevelNone {
		t.Errorf("a revoked member still reaches it: %q", got)
	}
	// The rendezvous is a share's only while it is shared.
	if !gate.KnownRV(strings.Repeat("2", 32)) {
		t.Error("a shared session's rv is unknown")
	}
	rd.sessions.Update("lola-a", func(s *session.Session) bool { s.Share = session.Share{}; return true })
	if gate.KnownRV(strings.Repeat("2", 32)) {
		t.Error("an unshared session's rv is still known")
	}
}

func TestShareSessionRules(t *testing.T) {
	rd, sh := newShareDaemon(t)
	m, k := sh.teammate(t, "ada-mini")
	rd.pin(t, m, k)
	rd.addSession("lola-host", nil)
	rd.addSession("lola-box", func(s *session.Session) { s.Sandbox = "docker" })
	rd.addSession("lola-local", func(s *session.Session) { s.Kind = session.KindLocal })
	rd.addSession("lola-shell", func(s *session.Session) { s.Agentless = true })

	for _, id := range []string{"lola-local", "lola-shell"} {
		if _, err := shareDo[protocol.ShareSessionData](t, rd.Daemon, protocol.CmdShareSession, protocol.ShareSessionArgs{Session: id, On: true}); err == nil {
			t.Errorf("%s was shared", id)
		}
	}
	if _, err := shareDo[protocol.ShareSessionData](t, rd.Daemon, protocol.CmdShareSession, protocol.ShareSessionArgs{Session: "nope", On: true}); err == nil {
		t.Error("an unknown session was shared")
	}
	// Typing into a session that runs on the host needs an explicit yes.
	if _, err := shareDo[protocol.ShareSessionData](t, rd.Daemon, protocol.CmdShareSession,
		protocol.ShareSessionArgs{Session: "lola-host", On: true, Level: "type"}); err == nil {
		t.Error("type access to a host session was granted without a confirmation")
	}
	if _, err := shareDo[protocol.ShareSessionData](t, rd.Daemon, protocol.CmdShareSession,
		protocol.ShareSessionArgs{Session: "lola-host", On: true, Level: "root"}); err == nil {
		t.Error("an unknown level was accepted")
	}
	d, err := shareDo[protocol.ShareSessionData](t, rd.Daemon, protocol.CmdShareSession,
		protocol.ShareSessionArgs{Session: "lola-box", On: true, Level: "type"})
	if err != nil || !d.On || !hubproto.ValidShareID(d.ShareID) || d.Level != "type" {
		t.Fatalf("sandboxed share: %+v %v", d, err)
	}
	// Editing keeps the share id (a teammate stays attached through a retitle).
	again, err := shareDo[protocol.ShareSessionData](t, rd.Daemon, protocol.CmdShareSession,
		protocol.ShareSessionArgs{Session: "lola-box", On: true, Title: "new title", Level: "view"})
	if err != nil || again.ShareID != d.ShareID {
		t.Fatalf("edit changed the share id: %+v %v", again, err)
	}
	s, _ := rd.sessions.Get("lola-box")
	if s.Share.Title != "new title" || s.Share.Level != "view" || !hubproto.ValidRelayID(s.Share.RV) {
		t.Fatalf("share = %+v", s.Share)
	}
	// Off clears everything, including the rendezvous.
	if _, err := shareDo[protocol.ShareSessionData](t, rd.Daemon, protocol.CmdShareSession, protocol.ShareSessionArgs{Session: "lola-box"}); err != nil {
		t.Fatal(err)
	}
	if s, _ := rd.sessions.Get("lola-box"); s.Share.On() || s.Share.RV != "" {
		t.Fatalf("unshare left %+v", s.Share)
	}
}

func TestSharingNeedsAJoinedHub(t *testing.T) {
	d := newTestDaemon(t, remoteTestConfig(), nil, nil)
	d.sessions.Upsert(session.Session{ID: "lola-a", TmuxName: "lola-a", Kind: session.KindLinear})
	if _, err := shareDo[protocol.ShareSessionData](t, d, protocol.CmdShareSession, protocol.ShareSessionArgs{Session: "lola-a", On: true}); err == nil {
		t.Fatal("a daemon with no hub shared a session")
	}
}

func TestPublishedSetCarriesNoSessionIdentity(t *testing.T) {
	rd, sh := newShareDaemon(t)
	m, k := sh.teammate(t, "ada-mini")
	rd.pin(t, m, k)
	rd.addSession("lola-eng-42-secret", func(s *session.Session) { s.Sandbox = "docker" })
	if _, err := shareDo[protocol.ShareSessionData](t, rd.Daemon, protocol.CmdShareSession,
		protocol.ShareSessionArgs{Session: "lola-eng-42-secret", On: true, Title: "api work"}); err != nil {
		t.Fatal(err)
	}
	set := rd.shareSet()
	if len(set.Shares) != 1 {
		t.Fatalf("set = %+v", set)
	}
	raw, _ := json.Marshal(set)
	if strings.Contains(string(raw), "eng-42") || strings.Contains(string(raw), "title lola") {
		t.Fatalf("the published set names the session: %s", raw)
	}
	if err := set.Validate(); err != nil {
		t.Fatalf("set does not validate: %v", err)
	}
	// With nobody pinned, nothing is offered.
	if _, err := rd.members.Revoke(rd.members.List()[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := rd.shareSet(); len(got.Shares) != 0 {
		t.Fatalf("a share nobody may attach is offered: %+v", got)
	}
}

func TestSharePassPublishes(t *testing.T) {
	rd, sh := newShareDaemon(t)
	m, k := sh.teammate(t, "ada-mini")
	rd.pin(t, m, k)
	rd.addSession("lola-a", func(s *session.Session) { s.Sandbox = "docker" })
	if _, err := shareDo[protocol.ShareSessionData](t, rd.Daemon, protocol.CmdShareSession, protocol.ShareSessionArgs{Session: "lola-a", On: true}); err != nil {
		t.Fatal(err)
	}
	rd.sharePass(t.Context())
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if sh.set == nil || len(sh.set.Shares) != 1 || sh.set.Shares[0].Level != hubproto.LevelView {
		t.Fatalf("hub holds %+v", sh.set)
	}
}

func TestTrustRefusesAChangedFingerprint(t *testing.T) {
	rd, sh := newShareDaemon(t)
	m, k := sh.teammate(t, "ada-mini")
	good := hubproto.Fingerprint(k)

	if _, err := shareDo[protocol.ShareMembersData](t, rd.Daemon, protocol.CmdShareTrust,
		protocol.ShareTrustArgs{DeviceID: m.DeviceID, Fingerprint: "0000 1111 2222 3333 4444 5555 6666 7777", Allow: true}); err == nil {
		t.Fatal("pinned a key whose fingerprint the person did not compare")
	}
	if len(rd.members.List()) != 0 {
		t.Fatal("a refused pin left an entry")
	}
	if _, err := shareDo[protocol.ShareMembersData](t, rd.Daemon, protocol.CmdShareTrust,
		protocol.ShareTrustArgs{DeviceID: m.DeviceID, Fingerprint: good}); err == nil {
		t.Fatal("a pin with no direction was accepted")
	}
	out, err := shareDo[protocol.ShareMembersData](t, rd.Daemon, protocol.CmdShareTrust,
		protocol.ShareTrustArgs{DeviceID: m.DeviceID, Fingerprint: good, Allow: true})
	if err != nil {
		t.Fatal(err)
	}
	// One direction only: allowing Ada to attach to us does not trust her sessions.
	if len(out.Members) != 1 || !out.Members[0].Allowed || out.Members[0].Trusted {
		t.Fatalf("members = %+v", out.Members)
	}
	if len(rd.owners.List()) != 0 {
		t.Fatal("allowing a teammate also trusted their sessions")
	}
	// The hub swapping the key afterwards cannot ride on the earlier confirmation.
	sh.mu.Lock()
	sh.members.Members = nil
	sh.mu.Unlock()
	m2, k2 := sh.teammate(t, "ada-mini")
	if _, err := shareDo[protocol.ShareMembersData](t, rd.Daemon, protocol.CmdShareTrust,
		protocol.ShareTrustArgs{DeviceID: m2.DeviceID, Fingerprint: good, Trust: true}); err == nil {
		t.Fatalf("a new key (%s) was pinned on the old fingerprint", hubproto.Fingerprint(k2))
	}
	// Removing ends both directions.
	if _, err := shareDo[protocol.ShareMembersData](t, rd.Daemon, protocol.CmdShareUntrust, protocol.ShareUntrustArgs{DeviceID: m.DeviceID}); err != nil {
		t.Fatal(err)
	}
	if len(rd.members.List()) != 0 {
		t.Fatal("untrust left the member")
	}
}

func TestSharingAloneNeverServesPhones(t *testing.T) {
	rd, sh := newShareDaemon(t)
	m, k := sh.teammate(t, "ada-mini")
	rd.pin(t, m, k)
	// A phone that paired earlier, with [remote] now switched off.
	rd.pair(t, true)
	rd.addSession("lola-a", func(s *session.Session) { s.Sandbox = "docker" })
	if got := rd.relayEntries(time.Now()); len(got) != 0 {
		t.Fatalf("phone rendezvous registered while [remote] is off: %+v", got)
	}
	if rd.remote != nil {
		t.Fatal("a server exists with nothing shared")
	}
	if _, err := shareDo[protocol.ShareSessionData](t, rd.Daemon, protocol.CmdShareSession, protocol.ShareSessionArgs{Session: "lola-a", On: true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rd.stopRemote)
	if rd.remote == nil {
		t.Fatal("sharing a session did not bring a relay-only server up")
	}
	if len(rd.remote.Addrs()) != 0 {
		t.Fatal("sharing opened a listener")
	}
	in := rd.relayGather(time.Now())
	if !in.wanted || len(in.entries) != 0 {
		t.Fatalf("relay inputs = %+v", in)
	}
	// Un-sharing takes the server down again.
	if _, err := shareDo[protocol.ShareSessionData](t, rd.Daemon, protocol.CmdShareSession, protocol.ShareSessionArgs{Session: "lola-a"}); err != nil {
		t.Fatal(err)
	}
	if rd.remote != nil {
		t.Fatal("the server outlived the last share")
	}
}

func TestShareCommandsAreHumanOnly(t *testing.T) {
	for _, cmd := range protocol.ShareHumanCommands {
		if got := remote.TierFor(cmd); got != protocol.TierNever {
			t.Errorf("%s is tier %q for a phone, want never", cmd, got)
		}
	}
}

func TestSessionsCarryShareStateForTheDesktopOnly(t *testing.T) {
	rd, sh := newShareDaemon(t)
	m, k := sh.teammate(t, "ada-mini")
	d1 := rd.pin(t, m, k)
	rd.addSession("lola-a", func(s *session.Session) { s.Sandbox = "docker" })
	if _, err := shareDo[protocol.ShareSessionData](t, rd.Daemon, protocol.CmdShareSession,
		protocol.ShareSessionArgs{Session: "lola-a", On: true, Title: "api", With: []string{m.DeviceID}}); err != nil {
		t.Fatal(err)
	}
	find := func() protocol.SessionInfo {
		for _, si := range rd.sessionsData().Sessions {
			if si.ID == "lola-a" {
				return si
			}
		}
		t.Fatal("session missing")
		return protocol.SessionInfo{}
	}
	si := find()
	if si.Share == nil || si.Share.Title != "api" || si.Share.Level != "view" || len(si.Share.With) != 1 {
		t.Fatalf("share = %+v", si.Share)
	}
	// The phone projection never carries it.
	if remoteSessionInfo(si).Share != nil {
		t.Fatal("a phone would see who a session is shared with")
	}
	// An unshared session carries nothing.
	rd.addSession("lola-b", nil)
	for _, s := range rd.sessionsData().Sessions {
		if s.ID == "lola-b" && s.Share != nil {
			t.Fatalf("lola-b: %+v", s.Share)
		}
	}
	// Live attachments are shown from the server's list.
	att := []remote.Attachment{{MemberID: d1.ID, Name: "Ada · mini", Pane: "lola-a", Since: time.Unix(100, 0)}}
	s, _ := rd.sessions.Get("lola-a")
	got := rd.sessionShare(s, att)
	if len(got.Attached) != 1 || got.Attached[0].Name != "Ada · mini" || got.Attached[0].Typed {
		t.Fatalf("attached = %+v", got.Attached)
	}
}

func TestShareNewSessionsSetting(t *testing.T) {
	rd, sh := newShareDaemon(t)
	m, k := sh.teammate(t, "ada-mini")
	rd.pin(t, m, k)
	setting := func(on bool) {
		rd.mu.Lock()
		rd.cfg.Hub.ShareNewSessions = &on
		rd.mu.Unlock()
	}
	shared := func(id string) bool { s, _ := rd.sessions.Get(id); return s.Share.On() }

	// Off (the default): a new session is private.
	rd.addSession("lola-off", nil)
	time.Sleep(100 * time.Millisecond)
	if shared("lola-off") {
		t.Fatal("a session was shared with the setting off")
	}
	// On: a NEW session starts shared, view-only, with everyone pinned.
	setting(true)
	rd.addSession("lola-new", nil)
	waitUntil(t, func() bool { return shared("lola-new") })
	s, _ := rd.sessions.Get("lola-new")
	if s.Share.Level != hubproto.LevelView || len(s.Share.With) != 0 || !hubproto.ValidRelayID(s.Share.RV) {
		t.Fatalf("share = %+v", s.Share)
	}
	// The one that predates the setting is untouched.
	if shared("lola-off") {
		t.Fatal("turning the setting on shared an existing session")
	}
	// A person's un-share sticks: updating the session never re-shares it.
	if _, err := shareDo[protocol.ShareSessionData](t, rd.Daemon, protocol.CmdShareSession, protocol.ShareSessionArgs{Session: "lola-new"}); err != nil {
		t.Fatal(err)
	}
	rd.sessions.Update("lola-new", func(s *session.Session) bool { s.Title = "retitled"; return true })
	time.Sleep(100 * time.Millisecond)
	if shared("lola-new") {
		t.Fatal("an un-shared session was shared again")
	}
	// A session that cannot be shared is skipped quietly.
	rd.addSession("lola-local", func(s *session.Session) { s.Kind = session.KindLocal })
	time.Sleep(100 * time.Millisecond)
	if shared("lola-local") {
		t.Fatal("a borrowed-directory session was shared")
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("condition not reached")
}

func TestUntrustRemovesOnlyTheNamedDirection(t *testing.T) {
	rd, sh := newShareDaemon(t)
	m, k := sh.teammate(t, "ada-mini")
	fp := hubproto.Fingerprint(k)
	if _, err := shareDo[protocol.ShareMembersData](t, rd.Daemon, protocol.CmdShareTrust,
		protocol.ShareTrustArgs{DeviceID: m.DeviceID, Fingerprint: fp, Allow: true, Trust: true}); err != nil {
		t.Fatal(err)
	}
	if len(rd.members.List()) != 1 || len(rd.owners.List()) != 1 {
		t.Fatal("both directions should be pinned")
	}
	out, err := shareDo[protocol.ShareMembersData](t, rd.Daemon, protocol.CmdShareUntrust, protocol.ShareUntrustArgs{DeviceID: m.DeviceID, Allow: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rd.members.List()) != 0 || len(rd.owners.List()) != 1 {
		t.Fatalf("members=%d owners=%d, want only the allow direction removed", len(rd.members.List()), len(rd.owners.List()))
	}
	if len(out.Members) != 1 || out.Members[0].Allowed || !out.Members[0].Trusted {
		t.Fatalf("members = %+v", out.Members)
	}
	if _, err := shareDo[protocol.ShareMembersData](t, rd.Daemon, protocol.CmdShareUntrust, protocol.ShareUntrustArgs{DeviceID: m.DeviceID}); err != nil {
		t.Fatal(err)
	}
	if len(rd.owners.List()) != 0 {
		t.Fatal("neither direction named should remove both")
	}
}
