package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/hub/internal/console"
	"github.com/sushidev-team/lola/hub/internal/escalate"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// runEnv runs the cli with extra environment and a Linear endpoint.
func (h *harness) runEnv(env map[string]string, endpoint string, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	c := &cli{stdout: &out, stderr: &errb, linearEndpoint: endpoint, getenv: func(k string) string {
		if k == "LOLA_HUB_DATA" {
			return h.data
		}
		return env[k]
	}}
	code := c.run(args)
	return code, out.String(), errb.String()
}

func TestInitCreatesOrgAndAdminToken(t *testing.T) {
	h := newHarness(t)
	out := h.ok("init", "--org", "acme", "--linear-team", "team-uuid-1")
	if !strings.Contains(out, "created org acme") || !strings.Contains(out, "admin token: ") {
		t.Fatalf("%s", out)
	}
	raw, err := os.ReadFile(filepath.Join(h.dir, console.TokenFile))
	if err != nil || !strings.Contains(out, strings.TrimSpace(string(raw))) {
		t.Fatalf("token file %v", err)
	}
	h.fails("init", "--org", "acme")
	if out := h.ok("init", "--org", "beta"); !strings.Contains(out, "unchanged") {
		t.Fatalf("second org re-printed the token: %s", out)
	}
	st, _ := store.Open(h.data)
	_ = st.View(func(d *store.Data) error {
		if d.Orgs["acme"].LinearTeam != "team-uuid-1" {
			t.Fatal("team not stored")
		}
		return nil
	})
}

func TestInitResolvesTeamKeyWithoutLeakingTheKey(t *testing.T) {
	const key = "lin_api_SECRET"
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":{"teams":{"nodes":[{"id":"8a9b-team"}]}}}`))
	}))
	defer srv.Close()
	h := newHarness(t)
	code, out, errs := h.runEnv(map[string]string{escalate.EnvKey: key}, srv.URL, "init", "--org", "acme", "--linear-team", "ENG")
	if code != 0 || auth != key || strings.Contains(out+errs, key) {
		t.Fatalf("exit %d auth=%q out=%s err=%s", code, auth, out, errs)
	}
	st, _ := store.Open(h.data)
	_ = st.View(func(d *store.Data) error {
		if d.Orgs["acme"].LinearTeam != "8a9b-team" {
			t.Fatalf("team %q", d.Orgs["acme"].LinearTeam)
		}
		return nil
	})
	// Without a key, a team key is stored as given, with a note.
	h2 := newHarness(t)
	_, _, errs = h2.runEnv(nil, srv.URL, "init", "--org", "acme", "--linear-team", "ENG")
	if !strings.Contains(errs, "stored as given") {
		t.Fatalf("%s", errs)
	}
}

func TestPolicyInitAndPublishRegisterKey(t *testing.T) {
	h := newHarness(t)
	h.ok("admin", "org", "create", "acme")
	dir := filepath.Join(h.dir, "pol")
	out := h.ok("policy", "init", "--org", "acme", "--out", dir)
	trusted := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "trusted:"))
	if _, err := hubproto.ParseTrustedKeys([]string{trusted}); err != nil {
		t.Fatalf("trusted line %q: %v", trusted, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, policyKeyFile)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v", err)
	}
	h.fails("policy", "init", "--org", "acme", "--out", dir) // never overwrites
	signed := filepath.Join(dir, policySignedFile)
	raw, _ := os.ReadFile(signed)
	keys, _ := hubproto.ParseTrustedKeys([]string{trusted})
	if _, err := hubproto.DecodeAndVerifyPolicy(raw, keys); err != nil {
		t.Fatalf("signed bundle does not verify: %v", err)
	}
	// The draft signs with the generated key (the edit loop works).
	h.ok("policy", "sign", "--key", filepath.Join(dir, policyKeyFile), "--out", filepath.Join(h.dir, "v1.json"), filepath.Join(dir, policyDraftFile))

	h.fails("admin", "policy", "publish", signed) // no key registered yet
	if out := h.ok("admin", "policy", "publish", "--register-key", trusted, signed); !strings.Contains(out, "published policy v1") {
		t.Fatalf("%s", out)
	}
	var p hubproto.Policy
	_ = json.Unmarshal(raw, &p)
	st, _ := store.Open(h.data)
	_ = st.View(func(d *store.Data) error {
		if d.Orgs["acme"].Policy == nil || d.Orgs["acme"].AdminKeys[p.KeyID] == "" {
			t.Fatal("publish --register-key did not store key and policy")
		}
		return nil
	})
}

func TestInviteUsesOnlyTheAdminsTrustedKey(t *testing.T) {
	h := newHarness(t)
	h.ok("admin", "org", "create", "acme")
	pub, _, _ := hubproto.GenerateKey()
	trusted := hubproto.FormatTrustedKey("k-admin", pub)
	// A key registered on the hub must never show up in an invite.
	hubPub, _, _ := hubproto.GenerateKey()
	h.ok("admin", "policy", "keys", "add", "--org", "acme", "--key-id", "k-hub", "--public-key", hubproto.EncodePublicKey(hubPub))
	out := h.ok("invite", "--url", "https://hub.example.com", "--org", "acme", "--label", "Ana Lopez", "--trusted", trusted, "--count", "2")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("%q", out)
	}
	for _, l := range lines {
		parts := strings.Split(strings.TrimPrefix(l, "lola-hub:"), "#")
		if !strings.HasPrefix(l, "lola-hub:https://hub.example.com#") || len(parts) != 3 || parts[2] != trusted || strings.Contains(l, "k-hub") {
			t.Fatalf("invite %q", l)
		}
	}
	st, _ := store.Open(h.data)
	_ = st.View(func(d *store.Data) error {
		n := 0
		for _, tk := range d.Tokens {
			if tk.Label == "Ana Lopez" {
				n++
			}
		}
		if n != 2 {
			t.Fatalf("%d labelled tokens", n)
		}
		return nil
	})
	if out := h.ok("invite", "--url", "https://hub.example.com", "--org", "acme", "--label", "Bo"); strings.Count(out, "#") != 1 {
		t.Fatalf("invite without key %q", out)
	}
	h.fails("invite", "--url", "http://hub.example.com", "--org", "acme", "--label", "Bo")
	h.fails("invite", "--url", "https://hub.example.com", "--org", "acme", "--label", "Bo", "--trusted", "nope")
	h.fails("invite", "--url", "https://hub.example.com", "--org", "acme")
}

func TestEnrollListRevokeAndItemsResolve(t *testing.T) {
	h := newHarness(t)
	h.ok("admin", "org", "create", "acme")
	tok := strings.TrimSpace(h.ok("admin", "enroll", "create", "--org", "acme", "--label", "Ana"))
	list := h.ok("admin", "enroll", "list")
	id := store.HashToken(tok)[:store.TokenIDLen]
	if !strings.Contains(list, id) || !strings.Contains(list, "Ana") || strings.Contains(list, tok) {
		t.Fatalf("%s", list)
	}
	h.ok("admin", "enroll", "revoke", id)
	if !strings.Contains(h.ok("admin", "enroll", "list", "--org", "acme"), "revoked") {
		t.Fatal("not revoked")
	}
	h.fails("admin", "enroll", "revoke", id)
	h.fails("admin", "items", "resolve", "i0123456789abcdef01234567")
	h.fails("admin", "items", "reopen")
}
