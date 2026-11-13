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
