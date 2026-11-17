package hub

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
)

func TestParseInvite(t *testing.T) {
	pub, _, err := hubproto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	enc := hubproto.EncodePublicKey(pub)
	const tok = "tok_0123456789abcdef"

	inv, err := ParseInvite("  " + tok + "\n")
	if err != nil || inv != (Invite{Token: tok}) {
		t.Fatalf("bare token = %+v, %v", inv, err)
	}
	inv, err = ParseInvite("lola-hub:https://hub.example.com/#" + tok)
	if err != nil || inv != (Invite{URL: "https://hub.example.com", Token: tok}) {
		t.Fatalf("invite = %+v, %v", inv, err)
	}
	inv, err = ParseInvite("lola-hub:https://hub.example.com#" + tok + "#k0123456789abcdef:" + enc)
	if err != nil || inv.KeyID != "k0123456789abcdef" || inv.PublicKey != enc || inv.Token != tok {
		t.Fatalf("invite with key = %+v, %v", inv, err)
	}
	for name, in := range map[string]string{
		"empty":        "",
		"spaces":       "a b",
		"http remote":  "lola-hub:http://hub.example.com#" + tok,
		"path":         "lola-hub:https://hub.example.com/x#" + tok,
		"no token":     "lola-hub:https://hub.example.com#",
		"no hash":      "lola-hub:https://hub.example.com",
		"too many":     "lola-hub:https://hub.example.com#a#b#c",
		"bad key id":   "lola-hub:https://hub.example.com#" + tok + "#bad id:" + enc,
		"bad key":      "lola-hub:https://hub.example.com#" + tok + "#k1:nope",
		"no key colon": "lola-hub:https://hub.example.com#" + tok + "#k1",
		"foreign":      "other:x#y",
		"huge":         strings.Repeat("a", 5000),
	} {
		_, err := ParseInvite(in)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		// No error echoes the token.
		if strings.Contains(err.Error(), tok) {
			t.Errorf("%s: error echoes the token: %v", name, err)
		}
	}
}

func TestKeyFingerprint(t *testing.T) {
	pub, _, _ := hubproto.GenerateKey()
	fp := KeyFingerprint(hubproto.EncodePublicKey(pub))
	if len(fp) != 19 || strings.Count(fp, " ") != 3 {
		t.Fatalf("fingerprint = %q", fp)
	}
	if fp != KeyFingerprint(hubproto.EncodePublicKey(pub)) {
		t.Error("fingerprint must be stable")
	}
	if KeyFingerprint("nope") != "" {
		t.Error("a malformed key has no fingerprint")
	}
}

func TestPinTrustedKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[[project]]\nname = \"web\"\npath = \"/tmp/web\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pub, _, _ := hubproto.GenerateKey()
	enc := hubproto.EncodePublicKey(pub)
	added, err := PinTrustedKey(path, "k1", enc)
	if err != nil || !added {
		t.Fatalf("pin = %v, %v", added, err)
	}
	cfg, err := config.Load(path)
	if err != nil || cfg.Hub.TrustedPolicyKeys["k1"] != enc || len(cfg.Projects) != 1 {
		t.Fatalf("config after pin = %+v, %v", cfg.Hub, err)
	}
	if added, err := PinTrustedKey(path, "k1", enc); err != nil || added {
		t.Fatalf("re-pin = %v, %v", added, err)
	}
	other, _, _ := hubproto.GenerateKey()
	if _, err := PinTrustedKey(path, "k1", hubproto.EncodePublicKey(other)); !errors.Is(err, ErrKeyIDTaken) {
		t.Fatalf("replacing a pinned key: %v", err)
	}
	if _, err := PinTrustedKey(path, "k2", "nope"); err == nil {
		t.Fatal("a malformed key must be refused")
	}
}
