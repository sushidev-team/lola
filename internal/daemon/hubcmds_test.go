package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/protocol"
)

func hubReq(t *testing.T, cmd string, args any) protocol.Request {
	t.Helper()
	req := protocol.Request{Cmd: cmd}
	if args != nil {
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		req.Args = raw
	}
	return req
}

func hubDo[T any](t *testing.T, d *Daemon, cmd string, args any) (T, error) {
	t.Helper()
	var out T
	resp := d.handle(context.Background(), hubReq(t, cmd, args))
	if !resp.OK {
		return out, errors.New(resp.Error)
	}
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		t.Fatalf("%s: decode %s: %v", cmd, resp.Data, err)
	}
	return out, nil
}

func boardView(it hubproto.Item, st hubproto.State) hubproto.ItemView {
	t0 := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	return hubproto.ItemView{Item: it, State: st, FirstSeen: t0, LastSeen: t0.Add(time.Hour)}
}

// humanFixture: a sandboxed project that has NOT opted in (hub_chores off),
// on a joined hub whose board lists one open advisory.
func humanFixture(t *testing.T) (*Daemon, *fakeHub, *fakeNative) {
	t.Helper()
	d, fh, nat := hubFixture(t)
	d.cfg.Projects[0].HubChores = false
	fh.board = hubproto.BoardResponse{Items: []hubproto.ItemView{boardView(advisoryItem("it1"), hubproto.StateOpen)}}
	return d, fh, nat
}

func TestHubClaimIsAHumanOpen(t *testing.T) {
	d, fh, nat := humanFixture(t)
	d.cfg.Defaults.GlobalCap = 0 // a person's claim checks no cap
	got, err := hubDo[protocol.HubClaimData](t, d, protocol.CmdHubClaim, protocol.HubClaimArgs{ItemID: "it1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Session == "" || got.Project != "web" || got.ItemID != "it1" {
		t.Fatalf("claim = %+v", got)
	}
	if c := fh.snapshot(); len(c.claims) != 1 || c.claims[0] != "it1" {
		t.Fatalf("claims = %v", c.claims)
	}
	if len(nat.manualAgents) != 1 {
		t.Fatalf("spawns = %+v", nat.manualAgents)
	}
	ss := hubSessions(d)
	if len(ss) != 1 || ss[0].HubItem.Label != "GHSA-jf85-cpcp-j695 · lodash" {
		t.Fatalf("sessions = %+v", ss)
	}
	if n := d.hubClaimsToday(d.hub.now()); n != 0 {
		t.Errorf("a person's claim must not spend max_claims_per_day, count = %d", n)
	}
	// The wire carries it on the session row.
	var si *protocol.SessionInfo
	for _, s := range d.sessionsData().Sessions {
		if s.ID == got.Session {
			si = &s
		}
	}
	if si == nil || si.Hub == nil || si.Hub.ItemID != "it1" || si.Hub.Label != "GHSA-jf85-cpcp-j695 · lodash" {
		t.Fatalf("SessionInfo.hub = %+v", si)
	}
	// A second click is refused: the item is already worked here.
	if _, err := hubDo[protocol.HubClaimData](t, d, protocol.CmdHubClaim, protocol.HubClaimArgs{ItemID: "it1"}); err == nil {
		t.Error("a second claim of the same item must be refused")
	}
}

func TestHubClaimStillRequiresTheSandbox(t *testing.T) {
	d, fh, nat := humanFixture(t)
	d.cfg.Projects[0].Sandbox = ""
	_, err := hubDo[protocol.HubClaimData](t, d, protocol.CmdHubClaim, protocol.HubClaimArgs{ItemID: "it1"})
	if err == nil || !strings.Contains(err.Error(), hub.ReasonNeedsSandbox) {
		t.Fatalf("err = %v", err)
	}
	if c := fh.snapshot(); len(c.claims) != 0 || len(nat.manualAgents) != 0 || len(hubSessions(d)) != 0 {
		t.Fatalf("a refused claim must mutate nothing: %+v %+v", c.claims, nat.manualAgents)
	}
}

func TestHubClaimAmbiguousProject(t *testing.T) {
	d, fh, _ := humanFixture(t)
	second := d.cfg.Projects[0]
	second.Name = "web2"
	d.cfg.Projects = append(d.cfg.Projects, second)
	_, err := hubDo[protocol.HubClaimData](t, d, protocol.CmdHubClaim, protocol.HubClaimArgs{ItemID: "it1"})
	if err == nil || !strings.Contains(err.Error(), "several projects") {
		t.Fatalf("err = %v", err)
	}
	if c := fh.snapshot(); len(c.claims) != 0 {
		t.Fatalf("an ambiguous claim must claim nothing: %v", c.claims)
	}
	// The board says so too.
	b, err := hubDo[protocol.HubBoardData](t, d, protocol.CmdHubBoard, protocol.HubBoardArgs{})
	if err != nil || len(b.Items) != 1 || !b.Items[0].Local.Claimable || len(b.Items[0].Local.Projects) != 2 {
		t.Fatalf("board = %+v, %v", b, err)
	}
	if _, err := hubDo[protocol.HubClaimData](t, d, protocol.CmdHubClaim, protocol.HubClaimArgs{ItemID: "it1", Project: "nope"}); err == nil {
		t.Error("an unknown project must be refused")
	}
	got, err := hubDo[protocol.HubClaimData](t, d, protocol.CmdHubClaim, protocol.HubClaimArgs{ItemID: "it1", Project: "web2"})
	if err != nil || got.Project != "web2" {
		t.Fatalf("claim with project = %+v, %v", got, err)
	}
}

// A row the board offers as claimable in ONE project (its sibling on the same
// repo is unsandboxed while the sandbox is required) claims into that project
// with no project named, as the board promised.
func TestHubClaimPicksTheOneAcceptingProject(t *testing.T) {
	d, fh, _ := humanFixture(t)
	second := d.cfg.Projects[0]
	second.Name = "web2"
	second.Sandbox = ""
	d.cfg.Projects = append(d.cfg.Projects, second)
	b, err := hubDo[protocol.HubBoardData](t, d, protocol.CmdHubBoard, protocol.HubBoardArgs{})
	if err != nil || len(b.Items) != 1 || !b.Items[0].Local.Claimable || b.Items[0].Local.Project != "web" {
		t.Fatalf("board = %+v, %v", b, err)
	}
	got, err := hubDo[protocol.HubClaimData](t, d, protocol.CmdHubClaim, protocol.HubClaimArgs{ItemID: "it1"})
	if err != nil || got.Project != "web" {
		t.Fatalf("claim = %+v, %v", got, err)
	}
	if c := fh.snapshot(); len(c.claims) != 1 {
		t.Fatalf("claims = %v", c.claims)
	}
}

func TestHubClaimHealthGateMutatesNothing(t *testing.T) {
	d, fh, nat := humanFixture(t)
	d.runtimeHealth = func(string) error { return errors.New("docker not found") }
	_, err := hubDo[protocol.HubClaimData](t, d, protocol.CmdHubClaim, protocol.HubClaimArgs{ItemID: "it1"})
	if err == nil || !strings.Contains(err.Error(), "runtime unavailable") {
		t.Fatalf("err = %v", err)
	}
	c := fh.snapshot()
	if len(c.claims) != 0 || len(nat.manualAgents) != 0 || len(hubSessions(d)) != 0 || len(c.releases) != 0 {
		t.Fatalf("health gate must mutate nothing: %+v", c)
	}
	if _, err := os.Stat(filepath.Join(d.hubDir(), hubStateFile)); err == nil {
		t.Error("no hub state may be written for a refused claim")
	}
}

func TestHubClaimRefusesAnItemNotOpen(t *testing.T) {
	d, fh, _ := humanFixture(t)
	fh.board.Items[0].State = hubproto.StateDone
	if _, err := hubDo[protocol.HubClaimData](t, d, protocol.CmdHubClaim, protocol.HubClaimArgs{ItemID: "it1"}); err == nil {
		t.Fatal("a done item must not be claimed")
	}
	if _, err := hubDo[protocol.HubClaimData](t, d, protocol.CmdHubClaim, protocol.HubClaimArgs{ItemID: "missing"}); err == nil {
		t.Fatal("an item that is nowhere must not be claimed")
	}
	if c := fh.snapshot(); len(c.claims) != 0 {
		t.Fatalf("claims = %v", c.claims)
	}
}

func TestHubReleaseKeepsTheSession(t *testing.T) {
	d, fh, _ := humanFixture(t)
	claim, err := hubDo[protocol.HubClaimData](t, d, protocol.CmdHubClaim, protocol.HubClaimArgs{ItemID: "it1"})
	if err != nil {
		t.Fatal(err)
	}
	rel, err := hubDo[protocol.HubReleaseData](t, d, protocol.CmdHubRelease, protocol.HubReleaseArgs{ItemID: "it1"})
	if err != nil {
		t.Fatal(err)
	}
	if rel.Session != claim.Session || !rel.SessionKept || rel.Queued {
		t.Fatalf("release = %+v", rel)
	}
	if c := fh.snapshot(); len(c.releases) != 1 || c.releases[0] != "it1" {
		t.Fatalf("releases = %v", c.releases)
	}
	s, ok := d.sessions.Get(claim.Session)
	if !ok || !s.HubItem.Released || s.HubItem.Open() {
		t.Fatalf("session after release = %+v (present %v)", s.HubItem, ok)
	}
	// The loop no longer renews the lease, and the teardown reports nothing.
	d.hubCycle(context.Background())
	if c := fh.snapshot(); len(c.beats) == 0 || len(c.beats[len(c.beats)-1].LiveLeases) != 0 {
		t.Fatalf("a released lease must not be renewed: %+v", c.beats)
	}
	d.hubSessionEnded(s)
	if c := fh.snapshot(); len(c.fails) != 0 || len(c.completes) != 0 {
		t.Fatalf("a released session must report nothing: %+v", c)
	}
	if _, err := hubDo[protocol.HubReleaseData](t, d, protocol.CmdHubRelease, protocol.HubReleaseArgs{ItemID: "it1"}); err == nil {
		t.Error("a second release must be refused")
	}
}

func TestHubReleaseQueuesWhenTheHubIsDown(t *testing.T) {
	d, fh, _ := humanFixture(t)
	claim, err := hubDo[protocol.HubClaimData](t, d, protocol.CmdHubClaim, protocol.HubClaimArgs{ItemID: "it1"})
	if err != nil {
		t.Fatal(err)
	}
	fh.mu.Lock()
	fh.outErr = errors.New("connection refused")
	fh.mu.Unlock()
	rel, err := hubDo[protocol.HubReleaseData](t, d, protocol.CmdHubRelease, protocol.HubReleaseArgs{ItemID: "it1"})
	if err != nil || !rel.Queued || !rel.SessionKept {
		t.Fatalf("release = %+v, %v", rel, err)
	}
	d.hub.mu.Lock()
	n := len(d.hub.st.Outbox)
	d.hub.mu.Unlock()
	if n != 1 {
		t.Fatalf("outbox = %d", n)
	}
	if s, _ := d.sessions.Get(claim.Session); !s.HubItem.Released {
		t.Fatal("the session must stop renewing at once")
	}
	// A terminal refusal changes nothing.
	fh.mu.Lock()
	fh.outErr = &hub.APIError{Status: 409, Code: "not_holder"}
	fh.mu.Unlock()
	if _, err := hubDo[protocol.HubReleaseData](t, d, protocol.CmdHubRelease, protocol.HubReleaseArgs{ItemID: "other"}); err == nil {
		t.Fatal("a refused release must fail")
	}
}

func TestHubBoardAnnotatesAndCaches(t *testing.T) {
	d, fh, _ := humanFixture(t)
	claimed := boardView(bumpItem("it2"), hubproto.StateClaimed)
	claimed.Lease = &hubproto.LeaseView{DeviceName: "ana-mbp", Expires: time.Now().Add(time.Hour)}
	foreign := advisoryItem("it3")
	foreign.DepAdvisory.Repo = "acme/other"
	done := boardView(advisoryItem("it4"), hubproto.StateDone)
	done.Outcome = &hubproto.OutcomeView{PRURL: "https://github.com/acme/widgets/pull/7", Delivery: hubproto.DeliveryMerged}
	fh.board.Items = append(fh.board.Items, claimed, boardView(foreign, hubproto.StateOpen), done)

	b, err := hubDo[protocol.HubBoardData](t, d, protocol.CmdHubBoard, protocol.HubBoardArgs{State: "", Limit: 0})
	if err != nil || len(b.Items) != 4 {
		t.Fatalf("board = %+v, %v", b, err)
	}
	byID := map[string]protocol.HubBoardItem{}
	for _, it := range b.Items {
		byID[it.ID] = it
	}
	if l := byID["it1"].Local; !l.Claimable || l.Project != "web" {
		t.Errorf("it1 local = %+v", l)
	}
	if it := byID["it2"]; it.Local.Claimable || it.Local.Reason != "claimed by ana-mbp" || it.Label != "PR #42 · lodash" || it.Lease == nil {
		t.Errorf("it2 = %+v", it)
	}
	if l := byID["it3"].Local; l.Claimable || !strings.Contains(l.Reason, "no local project") {
		t.Errorf("it3 local = %+v", l)
	}
	if it := byID["it4"]; it.Outcome == nil || it.Outcome.PRURL == "" || it.Local.Claimable {
		t.Errorf("it4 = %+v", it)
	}
	// Cached for hubBoardTTL; fresh bypasses.
	if _, err := hubDo[protocol.HubBoardData](t, d, protocol.CmdHubBoard, protocol.HubBoardArgs{}); err != nil {
		t.Fatal(err)
	}
	fh.mu.Lock()
	n := fh.boards
	fh.mu.Unlock()
	if n != 1 {
		t.Errorf("board fetched %d times, want 1 (cached)", n)
	}
	if _, err := hubDo[protocol.HubBoardData](t, d, protocol.CmdHubBoard, protocol.HubBoardArgs{Fresh: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := hubDo[protocol.HubBoardData](t, d, protocol.CmdHubBoard, protocol.HubBoardArgs{State: "bogus"}); err == nil {
		t.Error("an unknown state must be refused")
	}
	// A hub error is an error, never a partial board.
	fh.mu.Lock()
	fh.boardErr = hub.ErrInvalidResponse
	fh.mu.Unlock()
	if _, err := hubDo[protocol.HubBoardData](t, d, protocol.CmdHubBoard, protocol.HubBoardArgs{Fresh: true}); err == nil {
		t.Error("a refused board must fail")
	}
}

func TestHubOrgAndSync(t *testing.T) {
	d, fh, _ := humanFixture(t)
	fh.org = hubproto.OrgView{Org: "acme", PolicyVersion: 3, Devices: []hubproto.DeviceView{{Name: "mbp", Me: true, FreeSlots: 2, Active: true}}}
	o, err := hubDo[protocol.HubOrgData](t, d, protocol.CmdHubOrg, nil)
	if err != nil || o.Org != "acme" || o.PolicyVersion != 3 || len(o.Devices) != 1 || o.Devices[0].Agents == nil {
		t.Fatalf("org = %+v, %v", o, err)
	}
	if _, err := hubDo[protocol.HubOrgData](t, d, protocol.CmdHubOrg, nil); err != nil {
		t.Fatal(err)
	}
	if fh.orgs != 1 {
		t.Errorf("org fetched %d times, want 1 (cached)", fh.orgs)
	}
	s, err := hubDo[protocol.HubSyncData](t, d, protocol.CmdHubSync, nil)
	if err != nil || !s.Queued {
		t.Fatalf("sync = %+v, %v", s, err)
	}
	select {
	case <-d.hub.full:
	default:
		t.Error("sync must wake the loop")
	}
}

func TestHubCommandsNeedAnEnabledHub(t *testing.T) {
	d, fh, _ := humanFixture(t)
	d.cfg.Hub.Enabled = false
	for _, cmd := range []string{protocol.CmdHubBoard, protocol.CmdHubOrg, protocol.CmdHubClaim, protocol.CmdHubRelease} {
		args := any(nil)
		if cmd == protocol.CmdHubClaim || cmd == protocol.CmdHubRelease {
			args = map[string]string{"itemId": "it1"}
		}
		if resp := d.handle(context.Background(), hubReq(t, cmd, args)); resp.OK {
			t.Errorf("%s answered with the hub off", cmd)
		}
	}
	if c := fh.snapshot(); len(c.claims) != 0 || len(c.releases) != 0 {
		t.Fatalf("%+v", c)
	}
}

func TestHubCommandsStayOffTheHookBridge(t *testing.T) {
	for _, cmd := range protocol.HubHumanCommands {
		if hookBridgeCmds[cmd] {
			t.Errorf("%s must not be on the sandbox hook bridge", cmd)
		}
	}
}

// syncBuf is a goroutine-safe log sink.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestHubJoinLeaveRoundTrip(t *testing.T) {
	const token = "tok_secret_0123456789abcdef"
	d, _, _ := hubFixture(t)
	logs := &syncBuf{}
	d.log = log.New(logs, "", 0)
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	d.cfg.Hub = config.HubConfig{}
	d.cfg.Projects[0].Sandbox = "" // the saved config must validate on reload
	if err := d.cfg.Save(path); err != nil {
		t.Fatal(err)
	}

	var gotToken string
	refuse := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req hubproto.EnrollRequest
		json.NewDecoder(r.Body).Decode(&req)
		gotToken = req.Token
		w.Header().Set("Content-Type", "application/json")
		if refuse {
			w.WriteHeader(http.StatusUnauthorized)
			// A hub that echoes the token in its error must not get it logged.
			json.NewEncoder(w).Encode(hubproto.ErrorResponse{Error: "bad token " + req.Token, Code: hubproto.CodeUnauthorized})
			return
		}
		pub, _ := hubproto.DecodePublicKey(req.PublicKey)
		json.NewEncoder(w).Encode(hubproto.EnrollResponse{DeviceID: hubproto.DeviceID(pub), Org: "acme"})
	}))
	defer srv.Close()
	d.hub.enroll = func(_ string, key *hub.Key) (*hub.Client, error) { return hub.NewClient(srv.URL, key) }

	// Refused enrollment: the token is in neither the reply nor the log.
	refuse = true
	resp := d.handle(context.Background(), hubReq(t, protocol.CmdHubJoin, protocol.HubJoinArgs{URL: srv.URL, Token: token, Name: "mac"}))
	if resp.OK || strings.Contains(resp.Error, token) || strings.Contains(string(resp.Data), token) {
		t.Fatalf("refused join leaked the token: %+v", resp)
	}

	refuse = false
	resp = d.handle(context.Background(), hubReq(t, protocol.CmdHubJoin, protocol.HubJoinArgs{URL: srv.URL, Token: token, Name: "mac"}))
	if !resp.OK {
		t.Fatalf("join: %s", resp.Error)
	}
	if strings.Contains(string(resp.Data), token) || strings.Contains(logs.String(), token) {
		t.Fatal("the token must never be echoed or logged")
	}
	if gotToken != token {
		t.Fatalf("hub got token %q", gotToken)
	}
	var jd protocol.HubJoinData
	if err := json.Unmarshal(resp.Data, &jd); err != nil || jd.Org != "acme" || jd.DeviceName != "mac" {
		t.Fatalf("join data = %+v, %v", jd, err)
	}
	cfg, err := config.Load(path)
	if err != nil || !cfg.Hub.Enabled || cfg.Hub.URL != srv.URL || len(cfg.Projects) != 1 {
		t.Fatalf("config after join = %+v, %v", cfg.Hub, err)
	}
	if !d.cfg.Hub.Enabled {
		t.Fatal("the daemon must have reloaded the joined config")
	}
	st := d.hubStatusData()
	if !st.Joined || st.Org != "acme" || st.DeviceName != "mac" {
		t.Fatalf("status after join = %+v", st)
	}

	// A different hub is refused while joined.
	resp = d.handle(context.Background(), hubReq(t, protocol.CmdHubJoin, protocol.HubJoinArgs{URL: "https://other.example.com", Token: token}))
	if resp.OK || !strings.Contains(resp.Error, "already joined") {
		t.Fatalf("second hub: %+v", resp)
	}

	ld, err := hubDo[protocol.HubLeaveData](t, d, protocol.CmdHubLeave, protocol.HubLeaveArgs{ForgetKey: true})
	if err != nil || !ld.Left || !ld.KeyDeleted {
		t.Fatalf("leave = %+v, %v", ld, err)
	}
	cfg, err = config.Load(path)
	if err != nil || cfg.Hub.Enabled || cfg.Hub.URL != srv.URL {
		t.Fatalf("config after leave = %+v, %v", cfg.Hub, err)
	}
	if _, err := hub.LoadIdentity(d.hubDir()); !errors.Is(err, hub.ErrNotJoined) {
		t.Fatalf("identity after leave: %v", err)
	}
	if _, err := hub.LoadKey(d.hubDir()); !errors.Is(err, hub.ErrNoKey) {
		t.Fatalf("key after forget: %v", err)
	}
	if st := d.hubStatusData(); st.Joined || st.Enabled {
		t.Fatalf("status after leave = %+v", st)
	}
	if strings.Contains(logs.String(), token) {
		t.Fatal("the token must never be logged")
	}
}

func TestHubStatusCarriesPolicyFindingsAndSessions(t *testing.T) {
	d, _, _ := humanFixture(t)
	d.cfg.Hub.TrustedPolicyKeys = map[string]string{"k1": "unused-by-the-fake"}
	d.cfg.Projects[0].HubChores = true // findings scan only opted-in repos
	if _, err := hubDo[protocol.HubClaimData](t, d, protocol.CmdHubClaim, protocol.HubClaimArgs{ItemID: "it1"}); err != nil {
		t.Fatal(err)
	}
	d.hubCycle(context.Background())
	st, err := hubDo[protocol.HubStatusData](t, d, protocol.CmdHubStatus, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Joined || st.Org != "acme" || st.LastHeartbeat.IsZero() {
		t.Fatalf("status = %+v", st)
	}
	if st.LastFindings == nil || st.LastFindings.Repos != 1 {
		t.Errorf("lastFindings = %+v", st.LastFindings)
	}
	if st.Policy == nil || st.Policy.Version != 3 || st.Policy.Applied || st.Policy.Keys == nil {
		t.Errorf("policy = %+v", st.Policy)
	}
	if len(st.Sessions) != 1 || st.Sessions[0].Summary != "GHSA-jf85-cpcp-j695 · lodash" || st.Sessions[0].ItemID != "it1" {
		t.Errorf("sessions = %+v", st.Sessions)
	}
}
