package hub

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOrCreateKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if _, err := LoadKey(dir); !errors.Is(err, ErrNoKey) {
		t.Fatalf("LoadKey on empty dir = %v, want ErrNoKey", err)
	}
	k1, err := LoadOrCreateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, KeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("key mode = %v, want 0600", info.Mode().Perm())
	}
	k2, err := LoadOrCreateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if k1.DeviceID() != k2.DeviceID() {
		t.Error("a second LoadOrCreateKey must return the stored key")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("leftover temp files: %d entries", len(entries))
	}
}

func TestKeyNeverRendersPrivateHalf(t *testing.T) {
	dir := t.TempDir()
	k, err := LoadOrCreateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	seed := strings.TrimSpace(string(must(os.ReadFile(filepath.Join(dir, KeyFile)))))
	js, _ := json.Marshal(k)
	for _, s := range []string{fmt.Sprint(k), fmt.Sprintf("%v %+v %#v", k, k, k), string(js)} {
		if strings.Contains(s, seed) {
			t.Fatalf("private key leaked into %q", s)
		}
	}
}

func TestLoadKeyRefusesLoosePerms(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOrCreateKey(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, KeyFile), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKey(dir); !errors.Is(err, ErrKeyPerms) {
		t.Fatalf("LoadKey = %v, want ErrKeyPerms", err)
	}
}

func TestLoadKeyMalformedDoesNotEcho(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, KeyFile), []byte("SECRETSECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadKey(dir)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("LoadKey = %v", err)
	}
}

func TestIdentityRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadIdentity(dir); !errors.Is(err, ErrNotJoined) {
		t.Fatalf("LoadIdentity = %v, want ErrNotJoined", err)
	}
	k, _ := LoadOrCreateKey(dir)
	id := Identity{URL: "https://hub.example.com", DeviceID: k.DeviceID(), Org: "acme", DeviceName: "mac"}
	if err := SaveIdentity(dir, id); err != nil {
		t.Fatal(err)
	}
	got, err := LoadIdentity(dir)
	if err != nil || got != id {
		t.Fatalf("LoadIdentity = %+v, %v", got, err)
	}
	if err := SaveIdentity(dir, Identity{URL: "http://evil.example", DeviceID: k.DeviceID(), Org: "acme"}); err == nil {
		t.Error("SaveIdentity must refuse a non-https url")
	}
	if err := os.WriteFile(filepath.Join(dir, IdentityFile), []byte(`{"url":"https://h.example","device_id":"x","org":"acme"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIdentity(dir); err == nil {
		t.Error("LoadIdentity must validate what it reads")
	}
	if err := RemoveIdentity(dir); err != nil {
		t.Fatal(err)
	}
	if err := RemoveIdentity(dir); err != nil {
		t.Fatalf("second RemoveIdentity: %v", err)
	}
	if _, err := LoadKey(dir); err != nil {
		t.Errorf("RemoveIdentity must keep the key: %v", err)
	}
	if err := RemoveKey(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKey(dir); !errors.Is(err, ErrNoKey) {
		t.Errorf("after RemoveKey: %v", err)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
