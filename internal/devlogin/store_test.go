package devlogin

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccountsScopedSharedAndRotated(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir())
	oldStore, oldLookup, oldRemove := store, lookup, remove
	t.Cleanup(func() { store, lookup, remove = oldStore, oldLookup, oldRemove })
	items := map[string]string{}
	store = func(k, v string) error { items[k] = v; return nil }
	reads := 0
	lookup = func(k string) (string, bool, error) { reads++; v, ok := items[k]; return v, ok, nil }
	remove = func(k string) error { delete(items, k); return nil }
	shop, _ := ForProject("shop")
	api, _ := ForProject("api")
	owner := Profile{Name: "Owner", Username: "dev@example.test"}
	reader := Profile{Name: "Reader", Username: "reader", Agent: true}
	const password = " sample password\n"
	for _, row := range []struct {
		s Store
		p Profile
	}{{shop, owner}, {shop, reader}, {api, reader}} {
		if err := row.s.Save(row.p, password, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := shop.Save(owner, "replacement", false); err == nil {
		t.Fatal("duplicate overwrote a login")
	}
	profiles, err := shop.List(true)
	if err != nil || len(profiles) != 1 || profiles[0] != reader {
		t.Fatal(profiles, err)
	}
	if _, err := shop.credentials("Owner", true); err == nil || reads != 0 {
		t.Fatal("unshared password accessed")
	}
	raw, err := os.ReadFile(shop.path("Owner"))
	if err != nil || strings.Contains(string(raw), "sample password") {
		t.Fatal("invalid metadata")
	}
	got, err := shop.Credentials("Owner")
	if err != nil || got.Password != password {
		t.Fatal("password did not round trip", err)
	}
	owner.Agent = true
	if err := shop.Save(owner, "", true); err != nil {
		t.Fatal(err)
	}
	if got, err := shop.credentials("Owner", true); err != nil || got.Password != password {
		t.Fatal("sharing lost password")
	}
	if err := shop.Save(reader, "new password", true); err != nil || len(items) != 3 {
		t.Fatal("rotation", err)
	}
	if got, _ := api.credentials(reader.Name, true); got.Password != password {
		t.Fatal("rotation changed another project")
	}
	// Existing values in the parent environment must be overridden only for
	// this command. Its stdout and command line need no password.
	t.Setenv("LOLA_TEST_PASSWORD", "old")
	check := exec.Command("sh", "-c", `test "$LOLA_TEST_USERNAME" = reader && test "${#LOLA_TEST_PASSWORD}" -eq 12`)
	if err := shop.Run(reader.Name, check); err != nil || os.Getenv("LOLA_TEST_PASSWORD") != "old" {
		t.Fatal("child environment", err)
	}
	owner.Agent = false
	if err := shop.Save(owner, "", true); err != nil {
		t.Fatal(err)
	}
	blocked := exec.Command("sh", "-c", "exit 0")
	if err := shop.Run(owner.Name, blocked); err == nil || blocked.Process != nil {
		t.Fatal("ran a child after sharing was revoked")
	}
	save := store
	store = func(string, string) error { return errors.New("keychain locked") }
	if err := shop.Save(reader, "failed", true); err == nil {
		t.Fatal("lost write failure")
	}
	store = save
	if got, _ := shop.credentials(reader.Name, true); got.Password != "new password" {
		t.Fatal("failed write changed password")
	}
	if err := shop.Delete(owner.Name); err != nil {
		t.Fatal(err)
	}
	profiles, err = shop.List(false)
	if err != nil || len(items) != 2 || len(profiles) != 1 || profiles[0] != reader {
		t.Fatal(profiles, err)
	}
}

func TestAutoLoginFollowsTheProjectHostAcrossPorts(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir())
	a, _ := ForProject("shop")
	b, _ := ForProject("api")
	oldStore, oldLookup, oldRemove := store, lookup, remove
	t.Cleanup(func() { store, lookup, remove = oldStore, oldLookup, oldRemove })
	items := map[string]string{}
	store = func(k, v string) error { items[k] = v; return nil }
	remove = func(k string) error { delete(items, k); return nil }
	reads := 0
	lookup = func(k string) (string, bool, error) { reads++; v, ok := items[k]; return v, ok, nil }
	first := AutoLogin{"Buyer", "http://localhost:3000"}
	second := AutoLogin{"Admin", "http://localhost:3000"}
	for _, s := range []Store{a, b} {
		for _, c := range []AutoLogin{first, second} {
			if err := s.Save(Profile{Name: c.Name, Username: c.Name}, "fixture", false); err != nil {
				t.Fatal(err)
			}
		}
	}
	if c, err := a.AutoCredentials(first); err != nil || c != nil {
		t.Fatal("unexpected default", err)
	}
	for _, s := range []Store{a, b} {
		if err := s.SetAutoLogin(first, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.SetAutoLogin(second, true); err != nil {
		t.Fatal(err)
	}
	if err := a.SetAutoLogin(first, false); err != nil {
		t.Fatal(err)
	}
	for _, c := range []AutoLogin{first, {second.Name, "http://other.test:3000"}, {second.Name, "https://localhost:3000"}} {
		if got, err := a.AutoCredentials(c); err != nil || got != nil {
			t.Fatal("released a different selection", err)
		}
	}
	if reads != 0 {
		t.Fatal("read an unselected password")
	}
	// Older Lola versions persisted the port. Both record formats must survive a restart.
	persisted, err := os.ReadFile(filepath.Join(a.dir, "auto-login"))
	if err != nil || strings.Contains(string(persisted), ":3000") {
		t.Fatal("new selection still contains a port", err)
	}
	if err := a.write(filepath.Join(a.dir, "auto-login"), second); err != nil {
		t.Fatal(err)
	}
	restarted := AutoLogin{second.Name, "http://LOCALHOST:3001/login"}
	if got, err := a.AutoCredentials(restarted); err != nil || got == nil || got.Username != second.Name {
		t.Fatal("missing selected account", err)
	}
	if got, err := b.AutoLogin(); err != nil || got != (AutoLogin{first.Name, "http://localhost"}) {
		t.Fatal("changed another project", err)
	}
	if err := a.SetAutoLogin(second, false); err != nil {
		t.Fatal(err)
	}
	if got, err := a.AutoLogin(); err != nil || got.Name != "" {
		t.Fatal("unchecking kept the choice", err)
	}
	if err := b.Delete(first.Name); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(Profile{Name: first.Name, Username: "new buyer"}, "new", false); err != nil {
		t.Fatal(err)
	}
	if got, err := b.AutoCredentials(first); err != nil || got != nil {
		t.Fatal("recreated login inherited auto-login", err)
	}
}
