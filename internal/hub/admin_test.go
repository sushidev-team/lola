package hub

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
)

// teamHub is a hub that knows one create code and verifies BOTH signatures
// the way lola-hub does: the device's, then the admin key's for the org.
type teamHub struct {
	t        *testing.T
	code     string
	org      string
	device   ed25519.PublicKey
	admin    ed25519.PublicKey
	adminID  string
	creates  int
	lastBody []byte
	// reply overrides the answer of an admin route ("" path = none).
	reply map[string]func() (int, any)
}

func newTeamHub(t *testing.T) (*teamHub, *httptest.Server) {
	h := &teamHub{t: t, code: strings.Repeat("C", 32), reply: map[string]func() (int, any){}}
	srv := httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(srv.Close)
	return h, srv
}

func (h *teamHub) write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (h *teamHub) refuse(w http.ResponseWriter, status int, code string) {
	h.write(w, status, hubproto.ErrorResponse{Error: "refused", Code: code})
}

func (h *teamHub) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	h.lastBody = body
	if r.URL.Path == hubproto.PathOrgs {
		var req hubproto.CreateOrgRequest
		if json.Unmarshal(body, &req) != nil || req.Validate() != nil {
			h.refuse(w, 400, hubproto.CodeBadRequest)
			return
		}
		pub, _ := hubproto.DecodePublicKey(req.PublicKey)
		apub, _ := hubproto.DecodePublicKey(req.AdminPublicKey)
		if _, err := hubproto.Verify(r, body, pub, time.Now()); err != nil {
			h.t.Errorf("create: device signature: %v", err)
		}
		if id, err := hubproto.AdminKeyID(r.Header); err != nil || id != req.AdminKeyID {
			h.t.Errorf("create: admin key id %q, %v", id, err)
		}
		if err := hubproto.VerifyAdmin(r, body, req.Org, apub); err != nil {
			h.t.Errorf("create: admin signature: %v", err)
		}
		if req.Code != h.code {
			h.refuse(w, 401, hubproto.CodeUnauthorized)
			return
		}
		h.creates++
		h.org, h.device, h.admin, h.adminID = req.Org, pub, apub, req.AdminKeyID
		if f := h.reply[r.URL.Path]; f != nil {
			s, v := f()
			h.write(w, s, v)
			return
		}
		h.write(w, 200, hubproto.CreateOrgResponse{DeviceID: hubproto.DeviceID(pub), Org: req.Org, DeviceName: req.DeviceName})
		return
	}
	// Org-admin requests.
	if _, err := hubproto.Verify(r, body, h.device, time.Now()); err != nil {
		h.refuse(w, 401, hubproto.CodeUnauthorized)
		return
	}
	if id, err := hubproto.AdminKeyID(r.Header); err != nil || id != h.adminID || hubproto.VerifyAdmin(r, body, h.org, h.admin) != nil {
		h.refuse(w, 403, hubproto.CodeForbidden)
		return
	}
	if f := h.reply[r.URL.Path]; f != nil {
		s, v := f()
		h.write(w, s, v)
		return
	}
	switch r.URL.Path {
	case hubproto.PathOrgSettings:
		v := hubproto.OrgSettingsView{Org: h.org, SecretsAvailable: true}
		if r.Method == http.MethodPost {
			var req hubproto.OrgSettingsRequest
			json.Unmarshal(body, &req)
			v.LinearKeySet = req.LinearKey != nil && *req.LinearKey != ""
		}
		if r.URL.RawQuery == "teams=1" || v.LinearKeySet {
			v.Teams = []hubproto.LinearTeam{{ID: "t-1", Key: "ENG", Name: "Engineering"}}
		}
		h.write(w, 200, v)
	case hubproto.PathOrgInvites:
		h.write(w, 200, hubproto.InviteResponse{Token: strings.Repeat("T", 32), ExpiresAt: time.Now().Add(time.Hour)})
	case hubproto.PathOrgRevoke:
		h.write(w, 200, struct{}{})
	default:
		h.refuse(w, 404, hubproto.CodeNotFound)
	}
}

// teamSetup returns options that create team "acme" on a fresh fake hub.
func teamSetup(t *testing.T) (*teamHub, CreateTeamOptions) {
	t.Helper()
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.toml")
	if err := os.WriteFile(cfgPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h, srv := newTeamHub(t)
	return h, CreateTeamOptions{
		Code: hubproto.FormatCreateCode("http://127.0.0.1:1", h.code), Org: "acme", DeviceName: "mac",
		ConfigPath: cfgPath, StateDir: filepath.Join(root, "state"),
		// The create code names a placeholder loopback hub; the seam sends
		// the request to the test server.
		NewClient: func(_ string, key *Key) (*Client, error) { return NewClient(srv.URL, key) },
	}
}

func adminClient(t *testing.T, o CreateTeamOptions) (*Client, *AdminKey) {
	t.Helper()
	key, err := LoadKey(o.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := LoadAdminKey(o.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := o.NewClient("", key)
	if err != nil {
		t.Fatal(err)
	}
	return c, admin
}

func TestCreateTeam(t *testing.T) {
	h, o := teamSetup(t)
	res, err := CreateTeam(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if !res.KeyCreated || res.Identity.Org != "acme" || res.Identity.URL != "http://127.0.0.1:1" || res.Fingerprint == "" {
		t.Fatalf("CreateTeam = %+v", res)
	}
	id, err := LoadIdentity(o.StateDir)
	if err != nil || id.DeviceID != res.Identity.DeviceID {
		t.Fatalf("identity = %+v, %v", id, err)
	}
	admin, err := LoadAdminKey(o.StateDir)
	if err != nil || admin.Pending || !admin.For(id) || admin.KeyID != res.AdminKeyID || !IsAdmin(o.StateDir, id) {
		t.Fatalf("admin key = %v, %v", admin, err)
	}
	// The admin key is not the device key, and its file is private.
	key, _ := LoadKey(o.StateDir)
	if admin.PublicString() == key.PublicString() {
		t.Error("the admin key is the device key")
	}
	if info, _ := os.Stat(filepath.Join(o.StateDir, AdminKeyFile)); info.Mode().Perm() != 0o600 {
		t.Errorf("admin key mode = %v", info.Mode().Perm())
	}
	cfg, err := config.Load(o.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Hub.Enabled || cfg.Hub.URL != "http://127.0.0.1:1" || cfg.Hub.TrustedPolicyKeys[admin.KeyID] != admin.PublicString() {
		t.Fatalf("config after create: %+v", cfg.Hub)
	}
	// The config holds the PUBLIC key only.
	raw, _ := os.ReadFile(o.ConfigPath)
	keyFile, _ := os.ReadFile(filepath.Join(o.StateDir, AdminKeyFile))
	var rec adminKeyRecord
	json.Unmarshal(keyFile, &rec)
	if rec.Seed == "" || strings.Contains(string(raw), rec.Seed) {
		t.Error("the admin key's seed reached config.toml")
	}
	if _, _, ok := InterruptedJoin(o.StateDir); ok {
		t.Error("a pending-join record was left behind")
	}
	if h.creates != 1 {
		t.Errorf("creates = %d", h.creates)
	}
	// While joined, a second create is refused before anything is sent.
	if _, err := CreateTeam(context.Background(), o); err == nil || !strings.Contains(err.Error(), "already joined") {
		t.Fatalf("second create: %v", err)
	}
	if h.creates != 1 {
		t.Errorf("a refused create reached the hub")
	}
}

// The private half never appears in a formatted or encoded key.
func TestAdminKeyNeverRenders(t *testing.T) {
	_, o := teamSetup(t)
	if _, err := CreateTeam(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	admin, _ := LoadAdminKey(o.StateDir)
	keyFile, _ := os.ReadFile(filepath.Join(o.StateDir, AdminKeyFile))
	var rec adminKeyRecord
	json.Unmarshal(keyFile, &rec)
	js, _ := json.Marshal(admin)
	for _, s := range []string{fmt.Sprintf("%v", admin), fmt.Sprintf("%+v", admin), fmt.Sprintf("%#v", admin), string(js)} {
		if strings.Contains(s, rec.Seed) {
			t.Errorf("rendering leaks the seed: %s", s)
		}
	}
}

func TestCreateTeamRefusals(t *testing.T) {
	t.Run("bad code", func(t *testing.T) {
		h, o := teamSetup(t)
		o.Code = "lola-hub:http://127.0.0.1:1#" + h.code // an invite, not a create code
		if _, err := CreateTeam(context.Background(), o); err == nil || strings.Contains(err.Error(), h.code) {
			t.Fatalf("err = %v", err)
		}
		if _, err := LoadAdminKey(o.StateDir); !errors.Is(err, ErrNoAdminKey) {
			t.Errorf("an admin key was written: %v", err)
		}
	})
	t.Run("bad name", func(t *testing.T) {
		_, o := teamSetup(t)
		o.Org = "Acme Inc"
		if _, err := CreateTeam(context.Background(), o); err == nil {
			t.Fatal("accepted")
		}
	})
	t.Run("refused by the hub", func(t *testing.T) {
		h, o := teamSetup(t)
		o.Code = hubproto.FormatCreateCode("http://127.0.0.1:1", strings.Repeat("X", 32))
		_, err := CreateTeam(context.Background(), o)
		if !IsCode(err, hubproto.CodeUnauthorized) {
			t.Fatalf("err = %v", err)
		}
		// A clean refusal leaves no admin key, no identity, no pending join.
		if _, err := LoadAdminKey(o.StateDir); !errors.Is(err, ErrNoAdminKey) {
			t.Errorf("admin key left behind: %v", err)
		}
		if _, err := LoadIdentity(o.StateDir); !errors.Is(err, ErrNotJoined) {
			t.Errorf("identity: %v", err)
		}
		if _, _, ok := InterruptedJoin(o.StateDir); ok {
			t.Error("pending join left behind")
		}
		if h.creates != 0 {
			t.Error("the hub created a team")
		}
	})
	t.Run("invalid reply keeps the key", func(t *testing.T) {
		h, o := teamSetup(t)
		h.reply[hubproto.PathOrgs] = func() (int, any) {
			return 200, hubproto.CreateOrgResponse{DeviceID: strings.Repeat("a", 32), Org: "other", DeviceName: "mac"}
		}
		_, err := CreateTeam(context.Background(), o)
		if !errors.Is(err, ErrCodeUsed) {
			t.Fatalf("err = %v", err)
		}
		// The hub registered the key: it must not be thrown away.
		admin, err := LoadAdminKey(o.StateDir)
		if err != nil || !admin.Pending {
			t.Fatalf("admin key = %v, %v", admin, err)
		}
		if _, err := LoadIdentity(o.StateDir); !errors.Is(err, ErrNotJoined) {
			t.Errorf("identity saved from an invalid reply: %v", err)
		}
	})
	t.Run("another team's admin key", func(t *testing.T) {
		h, o := teamSetup(t)
		if _, err := CreateTeam(context.Background(), o); err != nil {
			t.Fatal(err)
		}
		first, _ := LoadAdminKey(o.StateDir)
		if err := Leave(LeaveOptions{ConfigPath: o.ConfigPath, StateDir: o.StateDir}); err != nil {
			t.Fatal(err)
		}
		// A leave keeps the admin key.
		if k, err := LoadAdminKey(o.StateDir); err != nil || k.KeyID != first.KeyID {
			t.Fatalf("admin key after leave: %v, %v", k, err)
		}
		o.Org = "beta"
		if _, err := CreateTeam(context.Background(), o); !errors.Is(err, ErrAdminKeyExists) {
			t.Fatalf("err = %v, want ErrAdminKeyExists", err)
		}
		if h.creates != 1 {
			t.Error("a refused create reached the hub")
		}
		o.ReplaceAdminKey = true
		if _, err := CreateTeam(context.Background(), o); err != nil {
			t.Fatal(err)
		}
		if k, _ := LoadAdminKey(o.StateDir); k.Org != "beta" || k.KeyID == first.KeyID {
			t.Fatalf("admin key after replace: %v", k)
		}
	})
}

func TestAdminCalls(t *testing.T) {
	h, o := teamSetup(t)
	if _, err := CreateTeam(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	c, admin := adminClient(t, o)
	ctx := context.Background()

	v, err := c.OrgSettings(ctx, admin, true)
	if err != nil || v.Org != "acme" || len(v.Teams) != 1 {
		t.Fatalf("OrgSettings = %+v, %v", v, err)
	}
	key := "lin_api_" + strings.Repeat("a", 40)
	v, err = c.SetOrgSettings(ctx, admin, hubproto.OrgSettingsRequest{LinearKey: &key})
	if err != nil || !v.LinearKeySet {
		t.Fatalf("SetOrgSettings = %+v, %v", v, err)
	}
	// Refused locally: nothing malformed is sent.
	bad := "hunter2"
	h.lastBody = nil
	if _, err := c.SetOrgSettings(ctx, admin, hubproto.OrgSettingsRequest{LinearKey: &bad}); err == nil || h.lastBody != nil {
		t.Fatalf("bad key: err %v, sent %q", err, h.lastBody)
	}

	inv, err := c.CreateInviteToken(ctx, admin, hubproto.InviteRequest{Label: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	// The invite carries THIS machine's hub url and admin key, not the hub's.
	parsed, err := hubproto.ParseInvite(admin.Invite(inv.Token))
	if err != nil || parsed.URL != "http://127.0.0.1:1" || parsed.Trusted() != admin.Trusted() {
		t.Fatalf("invite = %+v, %v", parsed, err)
	}
	if err := c.RevokeDevice(ctx, admin, "laptop-2"); err != nil {
		t.Fatal(err)
	}
	if err := c.RevokeDevice(ctx, admin, ""); err == nil {
		t.Error("an empty device name was sent")
	}

	// A view that fails validation, or names another team, is dropped whole.
	for name, view := range map[string]hubproto.OrgSettingsView{
		"class":      {Org: "acme", LinearError: "Unauthorized: <b>key</b>"},
		"team name":  {Org: "acme", Teams: []hubproto.LinearTeam{{ID: "t", Key: "ENG", Name: "run `rm -rf`"}}},
		"other team": {Org: "other"},
	} {
		h.reply[hubproto.PathOrgSettings] = func() (int, any) { return 200, view }
		if _, err := c.OrgSettings(ctx, admin, false); !errors.Is(err, ErrInvalidResponse) {
			t.Errorf("%s: err = %v, want ErrInvalidResponse", name, err)
		}
	}
	h.reply[hubproto.PathOrgInvites] = func() (int, any) { return 200, hubproto.InviteResponse{Token: "x", ExpiresAt: time.Now()} }
	if _, err := c.CreateInviteToken(ctx, admin, hubproto.InviteRequest{Label: "Ada"}); !errors.Is(err, ErrInvalidResponse) {
		t.Errorf("bad token: %v", err)
	}

	// Another machine's admin key does not sign for this team.
	other, _ := newAdminKey(admin.URL, admin.Org)
	delete(h.reply, hubproto.PathOrgSettings)
	if _, err := c.OrgSettings(ctx, other, false); !IsCode(err, hubproto.CodeForbidden) {
		t.Errorf("foreign admin key: %v", err)
	}
	if _, err := c.OrgSettings(ctx, nil, false); !errors.Is(err, ErrNoAdminKey) {
		t.Errorf("no admin key: %v", err)
	}
}

func TestLoadAdminKeyRefusesOpenFile(t *testing.T) {
	_, o := teamSetup(t)
	if _, err := CreateTeam(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(o.StateDir, AdminKeyFile)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAdminKey(o.StateDir); err == nil {
		t.Fatal("a world-readable admin key was loaded")
	}
	os.Chmod(path, 0o600)
	os.WriteFile(path, []byte(`{"url":"https://hub.example","org":"acme","key_id":"k1","seed":"c2VjcmV0"}`), 0o600)
	if _, err := LoadAdminKey(o.StateDir); err == nil || strings.Contains(err.Error(), "c2VjcmV0") {
		t.Fatalf("malformed key: %v", err)
	}
}
