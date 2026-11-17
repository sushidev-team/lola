package daemon

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/protocol"
)

// adminHub is a hub for the team commands: one create code, and org-admin
// requests verified with BOTH signatures like lola-hub does.
type adminHub struct {
	t            *testing.T
	code         string
	org          string
	device       ed25519.PublicKey
	admin        ed25519.PublicKey
	settings     hubproto.OrgSettingsRequest
	revoked      string
	refuseLinear bool
}

func (h *adminHub) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "application/json")
	fail := func(status int, code string) {
		w.WriteHeader(status)
		// A hub that echoes the request must not get a secret logged.
		json.NewEncoder(w).Encode(hubproto.ErrorResponse{Error: "refused " + string(body), Code: code})
	}
	if r.URL.Path == hubproto.PathOrgs {
		var req hubproto.CreateOrgRequest
		json.Unmarshal(body, &req)
		pub, _ := hubproto.DecodePublicKey(req.PublicKey)
		apub, _ := hubproto.DecodePublicKey(req.AdminPublicKey)
		if _, err := hubproto.Verify(r, body, pub, time.Now()); err != nil {
			h.t.Errorf("create: device signature: %v", err)
		}
		if err := hubproto.VerifyAdmin(r, body, req.Org, apub); err != nil {
			h.t.Errorf("create: admin signature: %v", err)
		}
		if req.Code != h.code {
			fail(401, hubproto.CodeUnauthorized)
			return
		}
		h.org, h.device, h.admin = req.Org, pub, apub
		json.NewEncoder(w).Encode(hubproto.CreateOrgResponse{DeviceID: hubproto.DeviceID(pub), Org: req.Org, DeviceName: req.DeviceName})
		return
	}
	if _, err := hubproto.Verify(r, body, h.device, time.Now()); err != nil {
		fail(401, hubproto.CodeUnauthorized)
		return
	}
	if hubproto.VerifyAdmin(r, body, h.org, h.admin) != nil {
		fail(403, hubproto.CodeForbidden)
		return
	}
	switch r.URL.Path {
	case hubproto.PathOrgSettings:
		if r.Method == http.MethodPost {
			var req hubproto.OrgSettingsRequest
			json.Unmarshal(body, &req)
			if h.refuseLinear && req.LinearKey != nil {
				fail(422, hubproto.CodeInvalidSecret)
				return
			}
			if req.LinearKey != nil {
				h.settings.LinearKey = req.LinearKey
			}
			if req.LinearTeam != nil {
				h.settings.LinearTeam = req.LinearTeam
			}
			if req.DigestWebhook != nil {
				h.settings.DigestWebhook = req.DigestWebhook
			}
		}
		v := hubproto.OrgSettingsView{Org: h.org, SecretsAvailable: true,
			LinearKeySet: h.settings.LinearKey != nil && *h.settings.LinearKey != "",
			DigestSet:    h.settings.DigestWebhook != nil && *h.settings.DigestWebhook != ""}
		if h.settings.LinearTeam != nil {
			v.LinearTeam = *h.settings.LinearTeam
		}
		if v.LinearKeySet {
			v.Teams = []hubproto.LinearTeam{{ID: "t-1", Key: "ENG", Name: "Engineering"}}
		}
		json.NewEncoder(w).Encode(v)
	case hubproto.PathOrgInvites:
		json.NewEncoder(w).Encode(hubproto.InviteResponse{Token: strings.Repeat("T", 32), ExpiresAt: time.Now().Add(time.Hour)})
	case hubproto.PathOrgRevoke:
		var req hubproto.DeviceRevokeRequest
		json.Unmarshal(body, &req)
		h.revoked = req.Device
		w.Write([]byte("{}"))
	default:
		fail(404, hubproto.CodeNotFound)
	}
}

func TestHubCreateTeamAndAdminCommands(t *testing.T) {
	d, _, _ := hubFixture(t)
	logs := &syncBuf{}
	d.log = log.New(logs, "", 0)
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	d.cfg.Hub = config.HubConfig{}
	d.cfg.Projects[0].Sandbox = ""
	if err := d.cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	// hubFixture starts joined; a team is created from a machine that is not.
	if _, err := hubDo[protocol.HubLeaveData](t, d, protocol.CmdHubLeave, protocol.HubLeaveArgs{ForgetKey: true}); err != nil {
		t.Fatal(err)
	}

	h := &adminHub{t: t, code: strings.Repeat("C", 32)}
	srv := httptest.NewServer(http.HandlerFunc(h.serve))
	defer srv.Close()
	d.hub.enroll = func(_ string, key *hub.Key) (*hub.Client, error) { return hub.NewClient(srv.URL, key) }
	ctx := context.Background()

	// Admin commands without a team are refused before anything is sent.
	if _, err := hubDo[protocol.HubInviteData](t, d, protocol.CmdHubInvite, protocol.HubInviteArgs{Label: "Ada"}); err == nil {
		t.Fatal("an invite was minted without a team")
	}

	// A refused create leaks the code nowhere.
	wrong := hubproto.FormatCreateCode(srv.URL, strings.Repeat("X", 32))
	resp := d.handle(ctx, hubReq(t, protocol.CmdHubCreateTeam, protocol.HubCreateTeamArgs{Code: wrong, Team: "acme", Name: "mac"}))
	if resp.OK || strings.Contains(resp.Error, strings.Repeat("X", 32)) || strings.Contains(logs.String(), strings.Repeat("X", 32)) {
		t.Fatalf("refused create leaked the code: %+v", resp)
	}
	if st := d.hubStatusData(); st.Joined || st.Admin {
		t.Fatalf("status after a refused create = %+v", st)
	}

	code := hubproto.FormatCreateCode(srv.URL, h.code)
	cd, err := hubDo[protocol.HubCreateTeamData](t, d, protocol.CmdHubCreateTeam, protocol.HubCreateTeamArgs{Code: code, Team: "acme", Name: "mac"})
	if err != nil {
		t.Fatal(err)
	}
	if cd.Org != "acme" || cd.DeviceName != "mac" || cd.URL != srv.URL || cd.AdminKeyID == "" || cd.Fingerprint == "" {
		t.Fatalf("create data = %+v", cd)
	}
	if strings.Contains(logs.String(), h.code) {
		t.Fatal("the create code was logged")
	}
	cfg, err := config.Load(path)
	if err != nil || !cfg.Hub.Enabled || cfg.Hub.URL != srv.URL || cfg.Hub.TrustedPolicyKeys[cd.AdminKeyID] == "" {
		t.Fatalf("config after create = %+v, %v", cfg.Hub, err)
	}
	if st := d.hubStatusData(); !st.Joined || st.Org != "acme" || !st.Admin {
		t.Fatalf("status after create = %+v", st)
	}
	// Nothing the daemon answered or logged holds the admin key's seed.
	keyFile, _ := os.ReadFile(filepath.Join(d.hubDir(), hub.AdminKeyFile))
	var rec struct{ Seed string }
	json.Unmarshal(keyFile, &rec)
	if rec.Seed == "" || strings.Contains(logs.String(), rec.Seed) {
		t.Fatal("the admin key seed is missing or was logged")
	}

	// Invite: built here, with this machine's hub url and admin key.
	inv, err := hubDo[protocol.HubInviteData](t, d, protocol.CmdHubInvite, protocol.HubInviteArgs{Label: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := hubproto.ParseInvite(inv.Invite)
	if err != nil || parsed.URL != srv.URL || parsed.KeyID != cd.AdminKeyID || inv.Fingerprint != cd.Fingerprint {
		t.Fatalf("invite = %+v, %v", parsed, err)
	}
	if strings.Contains(logs.String(), parsed.Token) {
		t.Fatal("the invite token was logged")
	}
	if _, err := hubDo[protocol.HubInviteData](t, d, protocol.CmdHubInvite, protocol.HubInviteArgs{Label: "a\nb"}); err == nil {
		t.Error("a malformed label was accepted")
	}

	// Settings: secrets go in, only status comes out.
	p := func(s string) *string { return &s }
	linear := "lin_api_" + strings.Repeat("a", 40)
	hook := "https://hooks.slack.com/services/T0001/B0001/abcDEF123"
	resp = d.handle(ctx, hubReq(t, protocol.CmdHubTeamSettings, protocol.HubTeamSettingsArgs{LinearKey: p(linear), DigestWebhook: p(hook)}))
	if !resp.OK {
		t.Fatalf("settings: %s", resp.Error)
	}
	var sd protocol.HubTeamSettingsData
	json.Unmarshal(resp.Data, &sd)
	if !sd.LinearKeySet || !sd.DigestSet || len(sd.Teams) != 1 || sd.Teams[0].Key != "ENG" {
		t.Fatalf("settings data = %+v", sd)
	}
	for _, secret := range []string{linear, hook} {
		if strings.Contains(string(resp.Data), secret) || strings.Contains(logs.String(), secret) {
			t.Fatalf("a secret was echoed or logged")
		}
	}
	if h.settings.LinearKey == nil || *h.settings.LinearKey != linear {
		t.Fatal("the hub did not get the key")
	}
	sd, err = hubDo[protocol.HubTeamSettingsData](t, d, protocol.CmdHubTeamSettings, protocol.HubTeamSettingsArgs{LinearTeam: p("t-1")})
	if err != nil || sd.LinearTeam != "t-1" {
		t.Fatalf("team = %+v, %v", sd, err)
	}
	// A read changes nothing.
	sd, err = hubDo[protocol.HubTeamSettingsData](t, d, protocol.CmdHubTeamSettings, protocol.HubTeamSettingsArgs{})
	if err != nil || !sd.LinearKeySet || sd.LinearTeam != "t-1" || sd.Teams == nil {
		t.Fatalf("read = %+v, %v", sd, err)
	}
	// Refused locally, in fixed words, without quoting the value.
	for _, a := range []protocol.HubTeamSettingsArgs{
		{LinearKey: p("hunter2-not-a-key")},
		{DigestWebhook: p("https://169.254.169.254/latest/meta-data")},
		{LinearTeam: p("a b")},
	} {
		resp = d.handle(ctx, hubReq(t, protocol.CmdHubTeamSettings, a))
		if resp.OK || strings.Contains(resp.Error, "hunter2") || strings.Contains(resp.Error, "169.254") {
			t.Errorf("bad setting: %+v", resp)
		}
	}
	// A key Linear refuses: a fixed sentence, and the hub's echo is dropped.
	h.refuseLinear = true
	other := "lin_api_" + strings.Repeat("b", 40)
	resp = d.handle(ctx, hubReq(t, protocol.CmdHubTeamSettings, protocol.HubTeamSettingsArgs{LinearKey: p(other)}))
	if resp.OK || !strings.Contains(resp.Error, "Linear refused") || strings.Contains(resp.Error, other) {
		t.Fatalf("refused key: %+v", resp)
	}

	rd, err := hubDo[protocol.HubRevokeData](t, d, protocol.CmdHubRevoke, protocol.HubRevokeArgs{Device: "laptop-2"})
	if err != nil || !rd.Revoked || h.revoked != "laptop-2" {
		t.Fatalf("revoke = %+v, %v (hub got %q)", rd, err, h.revoked)
	}

	// Without the admin key this machine is a plain member again.
	if err := hub.RemoveAdminKey(d.hubDir()); err != nil {
		t.Fatal(err)
	}
	if st := d.hubStatusData(); st.Admin {
		t.Error("status says admin without an admin key")
	}
	h.revoked = ""
	if _, err := hubDo[protocol.HubRevokeData](t, d, protocol.CmdHubRevoke, protocol.HubRevokeArgs{Device: "laptop-2"}); err == nil || h.revoked != "" {
		t.Fatalf("revoke without the admin key: %v (hub got %q)", err, h.revoked)
	}
}
