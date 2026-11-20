package devices

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

func memberHub() HubIdentity {
	return HubIdentity{DeviceID: "abc", PublicKey: "key", Fingerprint: "0000 1111", Member: "Ada"}
}

func TestMembersLiveInTheirOwnFiles(t *testing.T) {
	dir := t.TempDir()
	m := OpenMembers(dir, time.Now, nil)
	var spki [32]byte
	spki[0] = 7
	d, err := m.AddMember("ada's mini", spki, memberHub())
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Tiers) != 1 || d.Tiers[0] != protocol.TierRead || d.Hub == nil || d.Hub.Fingerprint == "" {
		t.Fatalf("member entry = %+v", d)
	}
	if _, err := os.Stat(filepath.Join(dir, MembersFileName)); err != nil {
		t.Fatalf("members.json missing: %v", err)
	}
	// A phone registry over the same home must not see the teammate: an older
	// daemon would otherwise take it for a phone with the read tier.
	if _, err := os.Stat(filepath.Join(dir, FileName)); err == nil {
		t.Fatal("a member was written to devices.json")
	}
	phones := Open(dir, time.Now, nil)
	if _, ok := phones.Lookup(spki); ok {
		t.Fatal("phone registry resolves a member key")
	}
	reopened := OpenMembers(dir, time.Now, nil)
	got, ok := reopened.Lookup(spki)
	if !ok || got.Hub == nil || got.Hub.DeviceID != "abc" {
		t.Fatalf("reopened = %+v %v", got, ok)
	}
}

func TestMemberNeedsHubIdentity(t *testing.T) {
	m := OpenMembers(t.TempDir(), time.Now, nil)
	if _, err := m.AddMember("x", [32]byte{1}, HubIdentity{}); err == nil {
		t.Fatal("member without a pinned hub identity accepted")
	}
}

func TestMemberRevocationSurvivesFailedSave(t *testing.T) {
	dir := t.TempDir()
	m := OpenMembers(dir, time.Now, nil)
	spki := [32]byte{9}
	d, err := m.AddMember("ada", spki, memberHub())
	if err != nil {
		t.Fatal(err)
	}
	m.write = func(string, []byte) error { return os.ErrPermission }
	if _, err := m.Revoke(d.ID); err == nil {
		t.Log("revoke reported no error")
	}
	if _, ok := m.Lookup(spki); ok {
		t.Fatal("revoked member still resolves")
	}
	if _, ok := OpenMembers(dir, time.Now, nil).Lookup(spki); ok {
		t.Fatal("revoked member came back after a restart")
	}
}
