package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
)

// The doctor names what the team policy locks, and reports a key the overlay
// had to ignore, without changing the config it was handed.
func TestHubPolicyResult(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	dir := filepath.Join(home, "state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	pub, priv, err := hubproto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	const keyID = "k0123456789abcdef"
	if err := hub.SaveIdentity(dir, hub.Identity{URL: "https://hub.example.com", DeviceID: hubproto.DeviceID(pub), Org: "acme"}); err != nil {
		t.Fatal(err)
	}
	p, err := hubproto.SignPolicy(priv, keyID, hubproto.Policy{Org: "acme", Version: 2, IssuedAt: time.Now().UTC(),
		Settings: hubproto.Settings{MaxConcurrency: 1, ReviewProviders: []string{"bot-watch"}}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(hub.StoredPolicy{Policy: p, VerifiedAt: time.Now().UTC()})
	if err := os.WriteFile(filepath.Join(dir, hub.PolicyFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Defaults: config.Defaults{GlobalCap: 4}, Hub: config.HubConfig{Enabled: true, URL: "https://hub.example.com",
		TrustedPolicyKeys: config.TrustedKeyMap{keyID: hubproto.EncodePublicKey(pub)}}}
	res, ok := hubPolicyResult(cfg)
	if !ok || res.OK || res.Critical {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(res.Detail, "v2 locks 2 key(s)") || !strings.Contains(res.Detail, `"bot-watch"`) {
		t.Errorf("detail = %q", res.Detail)
	}
	if cfg.Defaults.GlobalCap != 4 || cfg.TeamPolicyApplied() || len(cfg.Notices()) != 0 {
		t.Error("the doctor changed the config it read")
	}

	off := false
	cfg.Hub.ApplyPolicy = &off
	res, _ = hubPolicyResult(cfg)
	if !res.OK || !strings.Contains(res.Detail, "apply_policy = false") {
		t.Errorf("apply_policy=false result = %+v", res)
	}
}
