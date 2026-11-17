package hubproto

import (
	"strings"
	"testing"
)

func goodShare(t *testing.T) ShareEntry {
	t.Helper()
	id, _ := NewShareID()
	rv, _ := NewRelayID()
	return ShareEntry{ID: id, RV: rv, Title: "office mini: api work", Agent: "claude", Level: LevelView}
}

func TestShareSetValidate(t *testing.T) {
	ok := goodShare(t)
	if err := (ShareSet{Shares: []ShareEntry{ok}}).Validate(); err != nil {
		t.Fatalf("valid set refused: %v", err)
	}
	if err := (ShareSet{}).Validate(); err != nil {
		t.Fatalf("empty set refused: %v", err)
	}
	mut := map[string]func(e *ShareEntry){
		"id":      func(e *ShareEntry) { e.ID = "ENG-123" },
		"rv":      func(e *ShareEntry) { e.RV = "x" },
		"title":   func(e *ShareEntry) { e.Title = "a\x1b[31mb" },
		"empty":   func(e *ShareEntry) { e.Title = "" },
		"long":    func(e *ShareEntry) { e.Title = strings.Repeat("a", MaxShareTitleRunes+1) },
		"agent":   func(e *ShareEntry) { e.Agent = "Rm -rf" },
		"level":   func(e *ShareEntry) { e.Level = "admin" },
		"member":  func(e *ShareEntry) { e.Members = []string{"nope"} },
		"dupmem":  func(e *ShareEntry) { d := strings.Repeat("a", 32); e.Members = []string{d, d} },
		"toomany": func(e *ShareEntry) { e.Members = make([]string, MaxShareAudience+1) },
	}
	for name, f := range mut {
		e := goodShare(t)
		f(&e)
		if (ShareSet{Shares: []ShareEntry{e}}).Validate() == nil {
			t.Errorf("%s: invalid share accepted", name)
		}
	}
	dup := goodShare(t)
	if (ShareSet{Shares: []ShareEntry{dup, dup}}).Validate() == nil {
		t.Error("duplicate id accepted")
	}
	if (ShareSet{Shares: make([]ShareEntry, MaxSharesPerDevice+1)}).Validate() == nil {
		t.Error("too many shares accepted")
	}
}

func TestShareErrorsNameNoValue(t *testing.T) {
	e := goodShare(t)
	e.Title = "SECRET\x00"
	err := (ShareSet{Shares: []ShareEntry{e}}).Validate()
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("error must name the field only: %v", err)
	}
}

func TestMemberIdentityBinding(t *testing.T) {
	pub, priv, _ := GenerateKey()
	spki := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	id, err := SignBinding(priv, spki)
	if err != nil {
		t.Fatal(err)
	}
	if err := id.Verify(pub); err != nil {
		t.Fatalf("good binding refused: %v", err)
	}
	// The hub cannot swap the TLS key: the signature covers it.
	forged := id
	forged.SPKI = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB="
	if forged.Verify(pub) == nil {
		t.Error("binding for another spki verified")
	}
	// Nor another device's key.
	other, _, _ := GenerateKey()
	if id.Verify(other) == nil {
		t.Error("binding verified under another key")
	}
	if _, err := SignBinding(priv, "short"); err == nil {
		t.Error("malformed spki signed")
	}
}

func TestMemberListRefusedWholeOnBadBinding(t *testing.T) {
	pub, priv, _ := GenerateKey()
	id, _ := SignBinding(priv, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	row := MemberView{DeviceID: DeviceID(pub), Name: "mini", PublicKey: EncodePublicKey(pub), Identity: id}
	if err := (MemberList{Members: []MemberView{row}}).Validate(); err != nil {
		t.Fatalf("good list refused: %v", err)
	}
	bad := row
	bad.Identity.SPKI = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB="
	if (MemberList{Members: []MemberView{row, bad}}).Validate() == nil {
		t.Error("list with a forged binding accepted")
	}
	wrongID := row
	wrongID.DeviceID = strings.Repeat("0", 32)
	if (MemberList{Members: []MemberView{wrongID}}).Validate() == nil {
		t.Error("device id not derived from key accepted")
	}
}

func TestFingerprintShape(t *testing.T) {
	pub, _, _ := GenerateKey()
	fp := Fingerprint(pub)
	if len(strings.Fields(fp)) != 8 || len(fp) != 39 {
		t.Fatalf("fingerprint shape: %q", fp)
	}
}
