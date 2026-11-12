package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
)

type hubCallRec struct {
	cmd     string
	args    json.RawMessage
	timeout time.Duration
}

// withHubCall replaces the round-trip with a recorder answering reply.
func withHubCall(t *testing.T, reply any, err error) *[]hubCallRec {
	t.Helper()
	var calls []hubCallRec
	prev := hubCall
	hubCall = func(cmd string, args, out any, timeout time.Duration) error {
		raw, _ := json.Marshal(args)
		calls = append(calls, hubCallRec{cmd, raw, timeout})
		if reply != nil && out != nil {
			b, _ := json.Marshal(reply)
			_ = json.Unmarshal(b, out)
		}
		return err
	}
	t.Cleanup(func() { hubCall = prev })
	return &calls
}

func TestHubJoinTrimsAndForwards(t *testing.T) {
	calls := withHubCall(t, protocol.HubJoinData{Org: "acme", DeviceName: "mac"}, nil)
	d, err := (&HubService{}).Join(protocol.HubJoinArgs{URL: " https://hub.example.com/ ", Token: " tok_0123456789abcdef\n", Name: " mac "})
	if err != nil || d.Org != "acme" {
		t.Fatalf("Join = %+v, %v", d, err)
	}
	c := (*calls)[0]
	if c.cmd != protocol.CmdHubJoin || c.timeout != hubJoinTimeout {
		t.Fatalf("call = %+v", c)
	}
	var a protocol.HubJoinArgs
	_ = json.Unmarshal(c.args, &a)
	if a.URL != "https://hub.example.com" || a.Token != "tok_0123456789abcdef" || a.Name != "mac" {
		t.Errorf("args = %+v", a)
	}
}

func TestHubCommandsRouteToTheirSocketCmd(t *testing.T) {
	calls := withHubCall(t, nil, nil)
	s := &HubService{}
	_, _ = s.Status()
	_, _ = s.Leave(true)
	_, _ = s.Board(protocol.HubBoardArgs{State: "open", Limit: 50, Fresh: true})
	_, _ = s.Org()
	_, _ = s.Claim(protocol.HubClaimArgs{ItemID: "it_1", Project: "web"})
	_, _ = s.Release("it_1")
	_, _ = s.Sync()
	var got []string
	for _, c := range *calls {
		got = append(got, c.cmd)
	}
	want := []string{protocol.CmdHubStatus, protocol.CmdHubLeave, protocol.CmdHubBoard, protocol.CmdHubOrg, protocol.CmdHubClaim, protocol.CmdHubRelease, protocol.CmdHubSync}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("cmds = %v, want %v", got, want)
	}
	if !strings.Contains(string((*calls)[1].args), `"forgetKey":true`) || !strings.Contains(string((*calls)[4].args), `"project":"web"`) ||
		!strings.Contains(string((*calls)[5].args), `"itemId":"it_1"`) || !strings.Contains(string((*calls)[2].args), `"fresh":true`) {
		t.Errorf("args lost: %+v", *calls)
	}
}

// Lists the frontend iterates are never null, even on an error.
func TestHubListsNeverNull(t *testing.T) {
	withHubCall(t, nil, os.ErrDeadlineExceeded)
	s := &HubService{}
	if b, _ := s.Board(protocol.HubBoardArgs{}); b.Items == nil {
		t.Error("Board.Items nil")
	}
	if o, _ := s.Org(); o.Devices == nil {
		t.Error("Org.Devices nil")
	}
	withHubCall(t, protocol.HubStatusData{Joined: true, Policy: &protocol.HubPolicyInfo{Version: 1}}, nil)
	st, _ := s.Status()
	if st.Sessions == nil || st.Policy.Keys == nil || st.Policy.AppliedKeys == nil || st.Policy.Summary == nil {
		t.Errorf("Status = %+v", st)
	}
}

func TestHubKindsCoverEveryKind(t *testing.T) {
	got := (&HubService{}).Kinds()
	if len(got) != len(hubproto.AllKinds()) {
		t.Fatalf("Kinds = %+v", got)
	}
	for _, k := range got {
		if k.Label == "" || k.Label == k.ID {
			t.Errorf("kind %q has no Sentence-case label", k.ID)
		}
	}
}

func testPublicKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(pub)
}

// Defaults come back effective and a save that changes nothing writes no
// [hub] table: a default is never frozen into the file.
func TestHubSettingsDefaultsStayImplicit(t *testing.T) {
	path := writeTestConfig(t, minimalConfig)
	s := &ConfigService{}
	dto, err := s.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if *dto.HubMaxClaimsPerDay != 4 || !*dto.HubRequireSandbox || *dto.HubPollSeconds != 300 || !*dto.HubApplyPolicy ||
		dto.HubAcceptKinds == nil || dto.HubTrustedKeys == nil {
		t.Fatalf("defaults = %+v", dto)
	}
	if err := s.SaveSettings(dto); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "[hub") {
		t.Errorf("default save wrote a hub table:\n%s", raw)
	}
}

func TestHubSettingsRoundTrip(t *testing.T) {
	writeTestConfig(t, minimalConfig)
	s := &ConfigService{}
	dto, _ := s.GetSettings()
	pub := testPublicKey(t)
	claims, sandbox, poll, apply := 2, false, 600, false
	dto.HubAcceptKinds = []string{"dep_advisory"}
	dto.HubMaxClaimsPerDay, dto.HubRequireSandbox, dto.HubPollSeconds, dto.HubApplyPolicy = &claims, &sandbox, &poll, &apply
	dto.HubTrustedKeys = []HubTrustedKeyDTO{{KeyID: "kadmin1", PublicKey: " " + pub + " "}, {}}
	if dto.HubGroupChores == nil || !*dto.HubGroupChores {
		t.Fatal("grouped chores are not the default")
	}
	ungroup := false
	dto.HubGroupChores = &ungroup
	if err := s.SaveSettings(dto); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	h := cfg.Hub
	if h.ClaimsPerDay() != 2 || h.SandboxRequired() || h.PollSeconds != 600 || h.PolicyApplies() || h.GroupsChores() ||
		len(h.AcceptKinds) != 1 || h.TrustedPolicyKeys["kadmin1"] != pub {
		t.Fatalf("hub = %+v", h)
	}
	back, _ := s.GetSettings()
	if len(back.HubTrustedKeys) != 1 || back.HubTrustedKeys[0].KeyID != "kadmin1" || *back.HubRequireSandbox {
		t.Errorf("back = %+v", back.HubTrustedKeys)
	}
}

// An older client sends none of the hub fields: nothing in [hub] changes, and
// require_sandbox above all is not flipped to false.
func TestHubSettingsNilPreserves(t *testing.T) {
	writeTestConfig(t, minimalConfig+"[hub]\nrequire_sandbox = true\nmax_claims_per_day = 7\naccept_kinds = [\"red_bump_pr\"]\n")
	s := &ConfigService{}
	dto, _ := s.GetSettings()
	dto.HubAcceptKinds, dto.HubMaxClaimsPerDay, dto.HubRequireSandbox, dto.HubPollSeconds, dto.HubApplyPolicy, dto.HubTrustedKeys = nil, nil, nil, nil, nil, nil
	if err := s.SaveSettings(dto); err != nil {
		t.Fatal(err)
	}
	cfg, _, _ := loadConfig()
	if !cfg.Hub.SandboxRequired() || cfg.Hub.ClaimsPerDay() != 7 || len(cfg.Hub.AcceptKinds) != 1 {
		t.Errorf("hub = %+v", cfg.Hub)
	}
}

func TestHubSettingsRejectsHalfAKey(t *testing.T) {
	writeTestConfig(t, minimalConfig)
	s := &ConfigService{}
	dto, _ := s.GetSettings()
	dto.HubTrustedKeys = []HubTrustedKeyDTO{{KeyID: "kadmin1"}}
	if err := s.SaveSettings(dto); err == nil || !strings.Contains(err.Error(), "key id and a public key") {
		t.Fatalf("err = %v", err)
	}
	dto.HubTrustedKeys = []HubTrustedKeyDTO{{KeyID: "kadmin1", PublicKey: "not-a-key"}}
	if err := s.SaveSettings(dto); err == nil {
		t.Fatal("a malformed public key saved")
	}
}

func TestProjectHubChoresRoundTrip(t *testing.T) {
	writeTestConfig(t, minimalConfig+"[[project]]\nname = \"web\"\npath = \"/tmp\"\nrepo = \"acme/web\"\nteam_id = \"t\"\ncycle_mode = \"none\"\nassignee_mode = \"anyone\"\n")
	s := &ConfigService{}
	p, err := s.GetProject("web")
	if err != nil {
		t.Fatal(err)
	}
	if p.HubChores == nil || *p.HubChores {
		t.Fatalf("HubChores = %v", p.HubChores)
	}
	on := true
	p.HubChores = &on
	if err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	cfg, _, _ := loadConfig()
	if !cfg.ProjectByName("web").HubChores {
		t.Fatal("hub_chores not saved")
	}
	p.HubChores = nil // an older client
	if err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	cfg, _, _ = loadConfig()
	if !cfg.ProjectByName("web").HubChores {
		t.Fatal("nil HubChores cleared the opt-in")
	}
	_ = config.DefaultHubPollSeconds
}
