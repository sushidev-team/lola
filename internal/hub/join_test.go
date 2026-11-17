package hub

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
)

func TestJoinLeaveStatus(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.toml")
	state := filepath.Join(root, "state")
	if err := os.WriteFile(cfgPath, []byte("[[project]]\nname = \"web\"\npath = \"/tmp/web\"\nrepo = \"acme/web\"\nhub_chores = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var fh *fakeHub
	var srvURL string
	newClient := func(url string, key *Key) (*Client, error) {
		if fh == nil {
			h, srv := newFakeHub(t, key)
			fh, srvURL = h, srv.URL
			fh.routes["POST "+hubproto.PathEnroll] = func(body []byte) (int, any) {
				var req hubproto.EnrollRequest
				json.Unmarshal(body, &req)
				pub, _ := hubproto.DecodePublicKey(req.PublicKey)
				return 200, hubproto.EnrollResponse{DeviceID: hubproto.DeviceID(pub), Org: "acme"}
			}
		}
		return NewClient(srvURL, key)
	}
	// The fake server's URL is only known after the key exists; join through
	// a placeholder loopback URL and let the seam redirect to the server.
	opts := JoinOptions{URL: "http://127.0.0.1:1/", Token: "tok_0123456789abcdef", DeviceName: "mac",
		ConfigPath: cfgPath, StateDir: state, NewClient: newClient,
		Now: func() time.Time { return time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC) }}
	res, err := Join(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !res.KeyCreated || res.Identity.Org != "acme" || res.Identity.URL != "http://127.0.0.1:1" {
		t.Fatalf("Join = %+v", res)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Hub.Enabled || cfg.Hub.URL != "http://127.0.0.1:1" || cfg.Hub.DeviceName != "mac" || !cfg.Projects[0].HubChores {
		t.Fatalf("config after join: %+v", cfg.Hub)
	}

	st := LocalStatus(cfg, state)
	if !st.Joined || !st.KeyPresent || st.KeyMismatch || st.Policy != nil {
		t.Fatalf("status = %+v", st)
	}
	out := st.Render()
	for _, want := range []string{"hub:      enabled (http://127.0.0.1:1)", "org acme", "key:      present", "policy:   none"} {
		if !strings.Contains(out, want) {
			t.Errorf("render lacks %q:\n%s", want, out)
		}
	}

	// Joining another hub is refused while joined.
	other := opts
	other.URL = "https://other.example"
	if _, err := Join(context.Background(), other); err == nil || !strings.Contains(err.Error(), "already joined") {
		t.Fatalf("second hub: %v", err)
	}
	// Re-joining the same hub keeps the key.
	res2, err := Join(context.Background(), opts)
	if err != nil || res2.KeyCreated || res2.Identity.DeviceID != res.Identity.DeviceID {
		t.Fatalf("rejoin = %+v, %v", res2, err)
	}

	if err := Leave(LeaveOptions{ConfigPath: cfgPath, StateDir: state}); err != nil {
		t.Fatal(err)
	}
	cfg, _ = config.Load(cfgPath)
	if cfg.Hub.Enabled || cfg.Hub.URL == "" {
		t.Fatalf("leave must disable and keep the url: %+v", cfg.Hub)
	}
	if _, err := LoadIdentity(state); !errors.Is(err, ErrNotJoined) {
		t.Fatalf("identity after leave: %v", err)
	}
	if _, err := LoadKey(state); err != nil {
		t.Fatalf("leave must keep the key: %v", err)
	}
	if err := Leave(LeaveOptions{ConfigPath: cfgPath, StateDir: state, ForgetKey: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKey(state); !errors.Is(err, ErrNoKey) {
		t.Fatalf("--forget-key: %v", err)
	}
}

func TestJoinRefusesBadInput(t *testing.T) {
	root := t.TempDir()
	base := JoinOptions{URL: "https://hub.example", Token: "tok_0123456789abcdef", ConfigPath: filepath.Join(root, "c.toml"), StateDir: root}
	for name, mod := range map[string]func(*JoinOptions){
		"http remote": func(o *JoinOptions) { o.URL = "http://hub.example" },
		"bad name":    func(o *JoinOptions) { o.DeviceName = "\x1b[2J" },
		"no paths":    func(o *JoinOptions) { o.ConfigPath = "" },
	} {
		o := base
		mod(&o)
		if _, err := Join(context.Background(), o); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := os.Stat(filepath.Join(root, KeyFile)); err == nil {
		t.Error("a refused join must not create a key")
	}
}

func TestDefaultDeviceName(t *testing.T) {
	if n := DefaultDeviceName(); !config.ValidHubDeviceName(n) {
		t.Fatalf("DefaultDeviceName = %q is not valid", n)
	}
}

// joinFixture is a config file, a state dir and a fake hub whose enroll
// answer the test controls.
func joinFixture(t *testing.T, answer func(req hubproto.EnrollRequest) (int, any)) (JoinOptions, func() *fakeHub) {
	t.Helper()
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[[project]]\nname = \"web\"\npath = \"/tmp/web\"\nrepo = \"acme/web\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var fh *fakeHub
	var srvURL string
	newClient := func(url string, key *Key) (*Client, error) {
		if fh == nil {
			h, srv := newFakeHub(t, key)
			fh, srvURL = h, srv.URL
			fh.routes["POST "+hubproto.PathEnroll] = func(body []byte) (int, any) {
				var req hubproto.EnrollRequest
				json.Unmarshal(body, &req)
				return answer(req)
			}
		}
		return NewClient(srvURL, key)
	}
	return JoinOptions{URL: "http://127.0.0.1:1", Token: "tok_0123456789abcdef", DeviceName: "mac",
		ConfigPath: cfgPath, StateDir: filepath.Join(root, "state"), NewClient: newClient}, func() *fakeHub { return fh }
}

func enrolled(req hubproto.EnrollRequest, extra func(*hubproto.EnrollResponse)) (int, any) {
	pub, _ := hubproto.DecodePublicKey(req.PublicKey)
	r := hubproto.EnrollResponse{DeviceID: hubproto.DeviceID(pub), Org: "acme"}
	if extra != nil {
		extra(&r)
	}
	return 200, r
}

// The hub's stored (possibly suffixed) name and the member label land in the
// identity; the config edited while the hub answered is not overwritten.
func TestJoinKeepsTheHubsNameAndAConcurrentEdit(t *testing.T) {
	var o JoinOptions
	o, _ = joinFixture(t, func(req hubproto.EnrollRequest) (int, any) {
		// A person edits the config while the hub is answering.
		cfg, err := config.Load(o.ConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Defaults.GlobalCap = 7
		if err := cfg.Save(o.ConfigPath); err != nil {
			t.Fatal(err)
		}
		// The pending record exists before the token is spent.
		if _, _, ok := InterruptedJoin(o.StateDir); !ok {
			t.Error("no pending-join record while the token is in flight")
		}
		return enrolled(req, func(r *hubproto.EnrollResponse) { r.DeviceName = "mac-2"; r.Member = "Ana" })
	})
	res, err := Join(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Identity.DeviceName != "mac-2" || res.Identity.Member != "Ana" {
		t.Fatalf("identity = %+v", res.Identity)
	}
	cfg, err := config.Load(o.ConfigPath)
	if err != nil || cfg.Defaults.GlobalCap != 7 || !cfg.Hub.Enabled {
		t.Fatalf("config = %+v (cap %d), %v", cfg.Hub, cfg.Defaults.GlobalCap, err)
	}
	if _, _, ok := InterruptedJoin(o.StateDir); ok {
		t.Error("the pending record must go once the identity is saved")
	}
	if !strings.Contains(LocalStatus(cfg, o.StateDir).Render(), `member "Ana"`) {
		t.Error("status must show the member label")
	}
}

// A hub reply that does not validate (a malformed member label) is refused
// whole, and the error says the token was most likely used.
func TestJoinRefusesAMalformedReply(t *testing.T) {
	o, _ := joinFixture(t, func(req hubproto.EnrollRequest) (int, any) {
		return enrolled(req, func(r *hubproto.EnrollResponse) { r.Member = "\x1b[2Jevil" })
	})
	_, err := Join(context.Background(), o)
	if !errors.Is(err, ErrTokenUsed) {
		t.Fatalf("err = %v", err)
	}
	if _, err := LoadIdentity(o.StateDir); !errors.Is(err, ErrNotJoined) {
		t.Fatalf("identity saved from a refused reply: %v", err)
	}
	if _, _, ok := InterruptedJoin(o.StateDir); !ok {
		t.Error("the pending record must stay: the token was most likely used")
	}
}

// Enroll succeeded but the config cannot be saved: the error says the token
// was used and the identity is kept.
func TestJoinSaysTheTokenWasUsed(t *testing.T) {
	var o JoinOptions
	o, _ = joinFixture(t, func(req hubproto.EnrollRequest) (int, any) {
		// Break the config file after the pre-flight load.
		if err := os.WriteFile(o.ConfigPath, []byte("not = [toml"), 0o600); err != nil {
			t.Fatal(err)
		}
		return enrolled(req, nil)
	})
	_, err := Join(context.Background(), o)
	if !errors.Is(err, ErrTokenUsed) || !strings.Contains(err.Error(), "identity is saved") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), o.Token) {
		t.Fatal("the error echoes the token")
	}
	if _, err := LoadIdentity(o.StateDir); err != nil {
		t.Fatalf("identity must be kept: %v", err)
	}
}

// A refused enroll leaves no pending record and spends nothing locally.
func TestJoinRefusedLeavesNoPendingRecord(t *testing.T) {
	o, _ := joinFixture(t, func(hubproto.EnrollRequest) (int, any) {
		return 401, hubproto.ErrorResponse{Error: "bad token", Code: hubproto.CodeUnauthorized}
	})
	if _, err := Join(context.Background(), o); err == nil || errors.Is(err, ErrTokenUsed) {
		t.Fatalf("err = %v", err)
	}
	if _, _, ok := InterruptedJoin(o.StateDir); ok {
		t.Error("a refused join must not leave a pending record")
	}
}

// A revoked device: the identity goes, the status says revoked, a new join
// clears it, a leave records the hub it left.
func TestRevocationAndLeaveRecord(t *testing.T) {
	o, _ := joinFixture(t, func(req hubproto.EnrollRequest) (int, any) { return enrolled(req, nil) })
	if _, err := Join(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	r, err := MarkRevoked(o.StateDir, at)
	if err != nil || r.Org != "acme" || r.URL != o.URL {
		t.Fatalf("MarkRevoked = %+v, %v", r, err)
	}
	if _, err := LoadIdentity(o.StateDir); !errors.Is(err, ErrNotJoined) {
		t.Fatalf("identity after revoke: %v", err)
	}
	if _, err := LoadKey(o.StateDir); err != nil {
		t.Fatalf("the key must be kept: %v", err)
	}
	cfg, _ := config.Load(o.ConfigPath)
	st := LocalStatus(cfg, o.StateDir)
	if st.Membership() != "revoked" || !strings.Contains(st.Render(), "revoked") {
		t.Fatalf("status = %+v\n%s", st, st.Render())
	}
	if _, err := MarkRevoked(o.StateDir, at); err == nil {
		t.Error("nothing to revoke without an identity")
	}
	if _, err := Join(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if st := LocalStatus(cfg, o.StateDir); st.Membership() != "joined" || st.Revoked != nil {
		t.Fatalf("after rejoin = %+v", st)
	}
	o2 := LeaveOptions{ConfigPath: o.ConfigPath, StateDir: o.StateDir, Now: func() time.Time { return at }}
	if err := Leave(o2); err != nil {
		t.Fatal(err)
	}
	left, ok := LoadLeft(o.StateDir)
	if !ok || left.URL != o.URL || left.Org != "acme" || !left.At.Equal(at) {
		t.Fatalf("left record = %+v, %v", left, ok)
	}
	if st := LocalStatus(cfg, o.StateDir); st.Membership() != "not joined" {
		t.Fatalf("after leave = %s", st.Membership())
	}
}
