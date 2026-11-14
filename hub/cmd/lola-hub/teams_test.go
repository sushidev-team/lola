package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/hub/internal/secret"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// runIn runs the cli with extra environment and stdin.
func (h *harness) runIn(env map[string]string, stdin string, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	c := &cli{stdout: &out, stderr: &errb, stdin: strings.NewReader(stdin), getenv: func(k string) string {
		if k == "LOLA_HUB_DATA" {
			return h.data
		}
		return env[k]
	}}
	code := c.run(args)
	return code, out.String(), errb.String()
}

func TestSecretKeyCommand(t *testing.T) {
	h := newHarness(t)
	out := h.ok("secret-key")
	if b, err := secret.New(strings.TrimSpace(out)); err != nil || b == nil {
		t.Fatalf("secret-key printed an unusable key: %v", err)
	}
}

func TestOrgSecretsFromStdin(t *testing.T) {
	h := newHarness(t)
	h.ok("admin", "org", "create", "acme")
	sk, _ := secret.NewKey()
	env := map[string]string{secret.EnvKey: sk}
	key := "lin_api_" + strings.Repeat("q", 40)
	const hook = "https://hooks.slack.com/services/T0001/B0001/HOOKSECRET"

	// Without the secret key nothing is stored.
	if code, _, errs := h.runIn(nil, key+"\n", "admin", "org", "linear-key", "acme"); code == 0 || !strings.Contains(errs, secret.EnvKey) {
		t.Fatalf("no secret key: %d %s", code, errs)
	}
	// A malformed secret key is refused outright.
	if code, _, _ := h.runIn(map[string]string{secret.EnvKey: "passphrase"}, key, "admin", "org", "linear-key", "acme"); code == 0 {
		t.Fatal("a passphrase was accepted as the secret key")
	}
	// The key is never an argument.
	if code, _, _ := h.runIn(env, "", "admin", "org", "linear-key", "acme", key); code != 2 {
		t.Fatalf("a key in argv: exit %d", code)
	}
	if code, _, _ := h.runIn(env, "", "admin", "org", "linear-key", "acme"); code != 2 {
		t.Fatalf("empty stdin: exit %d", code)
	}
	code, out, errs := h.runIn(env, key+"\n", "admin", "org", "linear-key", "acme")
	if code != 0 || strings.Contains(out+errs, key) {
		t.Fatalf("linear-key: %d %s %s", code, out, errs)
	}
	code, out, errs = h.runIn(env, hook, "admin", "org", "digest", "acme")
	if code != 0 || strings.Contains(out+errs, "HOOKSECRET") {
		t.Fatalf("digest: %d %s %s", code, out, errs)
	}
	if code, _, _ := h.runIn(env, "https://example.com/x", "admin", "org", "digest", "acme"); code == 0 {
		t.Fatal("a non-Slack webhook was accepted")
	}
	box, _ := secret.New(sk)
	st, _ := store.Open(h.data)
	_ = st.View(func(d *store.Data) error {
		o := d.Orgs["acme"]
		k, _ := box.Open("acme", secret.FieldLinearKey, o.LinearKey)
		w, _ := box.Open("acme", secret.FieldDigestWebhook, o.DigestWebhook)
		if k != key || w != hook {
			t.Fatalf("stored %q %q", k, w)
		}
		return nil
	})
	if code, _, _ := h.runIn(nil, "", "admin", "org", "linear-key", "acme", "--clear"); code != 0 {
		t.Fatal("clear failed")
	}
}

func TestCreateCodeCommand(t *testing.T) {
	h := newHarness(t)
	line := strings.TrimSpace(h.ok("admin", "create-code", "--url", "https://hub.example.com/"))
	u, code, err := hubproto.ParseCreateCode(line)
	if err != nil || u != "https://hub.example.com" {
		t.Fatalf("%q: %v", line, err)
	}
	st, _ := store.Open(h.data)
	_ = st.View(func(d *store.Data) error {
		if d.CreateCodes[store.HashToken(code)] == nil {
			t.Fatal("the printed code is not stored")
		}
		return nil
	})
	if bare := strings.TrimSpace(h.ok("admin", "create-code")); strings.Contains(bare, ":") || len(bare) != 32 {
		t.Fatalf("bare code %q", bare)
	}
	h.fails("admin", "create-code", "--url", "http://hub.example.com")
}

func TestPolicyKeysRemoveCommand(t *testing.T) {
	h := newHarness(t)
	h.ok("admin", "org", "create", "acme")
	pub, _, _ := hubproto.GenerateKey()
	h.ok("admin", "policy", "keys", "add", "--org", "acme", "--key-id", "k1", "--public-key", hubproto.EncodePublicKey(pub))
	h.ok("admin", "policy", "keys", "remove", "--org", "acme", "--key-id", "k1")
	h.fails("admin", "policy", "keys", "remove", "--org", "acme", "--key-id", "k1")
}
