package hubproto

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func relayHash(c byte) string { return strings.Repeat(string(c), 64) }

func TestRendezvousValidate(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	rv1, rv2 := strings.Repeat("a", 32), strings.Repeat("b", 32)
	ok := RelayRendezvous{RV: rv1, BearerSHA256: relayHash('1')}
	cases := []struct {
		name string
		req  RendezvousRequest
		bad  bool
	}{
		{"empty", RendezvousRequest{}, false},
		{"plain", RendezvousRequest{Entries: []RelayRendezvous{ok}}, false},
		{"bad rv", RendezvousRequest{Entries: []RelayRendezvous{{RV: "xyz", BearerSHA256: relayHash('1')}}}, true},
		{"upper rv", RendezvousRequest{Entries: []RelayRendezvous{{RV: strings.Repeat("A", 32), BearerSHA256: relayHash('1')}}}, true},
		{"bad hash", RendezvousRequest{Entries: []RelayRendezvous{{RV: rv1, BearerSHA256: "abc"}}}, true},
		{"dup rv", RendezvousRequest{Entries: []RelayRendezvous{ok, ok}}, true},
		{"two rv", RendezvousRequest{Entries: []RelayRendezvous{ok, {RV: rv2, BearerSHA256: relayHash('2')}}}, false},
		{"prev without until", RendezvousRequest{Entries: []RelayRendezvous{{RV: rv1, BearerSHA256: relayHash('1'), PrevSHA256: relayHash('2')}}}, true},
		{"until without prev", RendezvousRequest{Entries: []RelayRendezvous{{RV: rv1, BearerSHA256: relayHash('1'), PrevUntil: now.Add(time.Hour)}}}, true},
		{"prev equals bearer", RendezvousRequest{Entries: []RelayRendezvous{{RV: rv1, BearerSHA256: relayHash('1'), PrevSHA256: relayHash('1'), PrevUntil: now.Add(time.Hour)}}}, true},
		{"prev bad shape", RendezvousRequest{Entries: []RelayRendezvous{{RV: rv1, BearerSHA256: relayHash('1'), PrevSHA256: "zz", PrevUntil: now.Add(time.Hour)}}}, true},
		{"prev at max+skew", RendezvousRequest{Entries: []RelayRendezvous{{RV: rv1, BearerSHA256: relayHash('1'), PrevSHA256: relayHash('2'), PrevUntil: now.Add(RelayPrevBearerGraceMax + MaxSkew)}}}, false},
		{"prev past max+skew", RendezvousRequest{Entries: []RelayRendezvous{{RV: rv1, BearerSHA256: relayHash('1'), PrevSHA256: relayHash('2'), PrevUntil: now.Add(RelayPrevBearerGraceMax + MaxSkew + time.Second)}}}, true},
		{"prev already past", RendezvousRequest{Entries: []RelayRendezvous{{RV: rv1, BearerSHA256: relayHash('1'), PrevSHA256: relayHash('2'), PrevUntil: now.Add(-24 * time.Hour)}}}, false},
		{"window at max+skew", RendezvousRequest{Entries: []RelayRendezvous{{RV: rv1, BearerSHA256: relayHash('1'), ExpiresAt: now.Add(RelayPairingTTLMax + MaxSkew)}}}, false},
		{"window past max+skew", RendezvousRequest{Entries: []RelayRendezvous{{RV: rv1, BearerSHA256: relayHash('1'), ExpiresAt: now.Add(RelayPairingTTLMax + MaxSkew + time.Second)}}}, true},
		{"window already past", RendezvousRequest{Entries: []RelayRendezvous{{RV: rv1, BearerSHA256: relayHash('1'), ExpiresAt: now.Add(-time.Hour)}}}, false},
	}
	for _, c := range cases {
		err := c.req.Validate(now)
		if (err != nil) != c.bad {
			t.Errorf("%s: err=%v, want bad=%v", c.name, err, c.bad)
		}
	}

	// Entry-count bound: max accepted, max+1 refused.
	var many []RelayRendezvous
	for i := 0; i < RelayMaxRendezvousPerDevice; i++ {
		many = append(many, RelayRendezvous{RV: fmt.Sprintf("%032x", i+1), BearerSHA256: relayHash('1')})
	}
	if err := (RendezvousRequest{Entries: many}).Validate(now); err != nil {
		t.Errorf("max entries refused: %v", err)
	}
	many = append(many, RelayRendezvous{RV: fmt.Sprintf("%032x", 99), BearerSHA256: relayHash('1')})
	if err := (RendezvousRequest{Entries: many}).Validate(now); err == nil {
		t.Error("max+1 entries accepted")
	}

	// Errors name the field, never a value.
	secret := "SECRETVALUE"
	err := RendezvousRequest{Entries: []RelayRendezvous{{RV: secret, BearerSHA256: relayHash('1')}}}.Validate(now)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Errorf("error leaks value: %v", err)
	}
}

func TestRelayIDShapes(t *testing.T) {
	good := strings.Repeat("0123456789abcdef", 2)
	for s, want := range map[string]bool{
		good:                    true,
		good[:31]:               false,
		good + "0":              false,
		strings.ToUpper(good):   false,
		strings.Repeat("g", 32): false,
		"":                      false,
	} {
		if ValidRelayID(s) != want {
			t.Errorf("ValidRelayID(%q) != %v", s, want)
		}
	}
	for s, want := range map[string]bool{
		strings.Repeat("ab", 32):       true,
		strings.Repeat("ab", 31):       false,
		strings.Repeat("AB", 32):       false,
		strings.Repeat("ab", 32) + "0": false,
	} {
		if ValidRelayHash(s) != want {
			t.Errorf("ValidRelayHash(%q) != %v", s, want)
		}
	}
	b43 := strings.Repeat("aZ0-_", 8) + "abc"
	for s, want := range map[string]bool{
		b43:            true,
		b43[:42]:       false,
		b43 + "a":      false,
		b43[:42] + "=": false,
		b43[:42] + "+": false,
		b43[:42] + "/": false,
	} {
		if ValidRelayBearer(s) != want {
			t.Errorf("ValidRelayBearer(%q) != %v", s, want)
		}
	}
	for i := 0; i < 20; i++ {
		id, err := NewRelayID()
		if err != nil || !ValidRelayID(id) {
			t.Fatalf("NewRelayID = %q, %v", id, err)
		}
		b, h, err := NewRelayBearer()
		if err != nil || !ValidRelayBearer(b) || !ValidRelayHash(h) || h != RelayBearerHash(b) {
			t.Fatalf("NewRelayBearer = %q, %q, %v", b, h, err)
		}
	}
	a, _ := NewRelayID()
	b, _ := NewRelayID()
	if a == b {
		t.Error("two ids equal")
	}
}

func TestRelayBearerHash(t *testing.T) {
	// sha256("abc") and sha256("") are well-known vectors.
	if got := RelayBearerHash("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("hash(abc) = %s", got)
	}
	if got := RelayBearerHash(""); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("hash(empty) = %s", got)
	}
}

func TestRelayMsgLenient(t *testing.T) {
	conn, rv := strings.Repeat("c", 32), strings.Repeat("d", 32)
	decode := func(s string) RelayMsg {
		t.Helper()
		var m RelayMsg
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	// Unknown t: decodes, validates, receiver ignores it.
	if m := decode(`{"t":"future","x":1}`); m.T != "future" || m.Validate() != nil {
		t.Errorf("unknown t: %+v %v", m, m.Validate())
	}
	// Unknown field on a known t is ignored.
	if m := decode(`{"t":"incoming","conn":"` + conn + `","rv":"` + rv + `","extra":{"a":1}}`); m.Validate() != nil {
		t.Errorf("incoming with extra field: %v", m.Validate())
	}
	if m := decode(`{"t":"hello","v":1,"ping_s":99,"max_conns":7,"new":true}`); m.Validate() != nil {
		t.Errorf("hello: %v", m.Validate())
	}
	// Bad known fields are protocol errors.
	for _, s := range []string{
		`{"t":"hello","v":2}`,
		`{"t":"hello"}`,
		`{"t":"incoming","conn":"nope","rv":"` + rv + `"}`,
		`{"t":"incoming","conn":"` + conn + `"}`,
		`{"t":"cancel","conn":"` + strings.ToUpper(conn) + `"}`,
		`{"t":"reject","conn":"` + conn + `","reason":"because"}`,
		`{"t":"reject","conn":"` + conn + `"}`,
	} {
		if err := decode(s).Validate(); err == nil {
			t.Errorf("accepted %s", s)
		}
	}
	for _, s := range []string{
		`{"t":"cancel","conn":"` + conn + `"}`,
		`{"t":"reject","conn":"` + conn + `","reason":"busy"}`,
		`{"t":"reject","conn":"` + conn + `","reason":"unknown"}`,
		`{"t":"reject","conn":"` + conn + `","reason":"off"}`,
		`{"t":"bye","reason":"whatever"}`, // bye: unknown reason is treated as shutdown, not an error
	} {
		if err := decode(s).Validate(); err != nil {
			t.Errorf("refused %s: %v", s, err)
		}
	}
}

func TestRelayConstantsOrdering(t *testing.T) {
	if !(RelayPhoneAttemptTimeout > RelayAcceptTimeout) {
		t.Error("phone attempt must exceed accept timeout")
	}
	if !(RelayDeadAfter > 2*RelayPingInterval) {
		t.Error("dead-after must exceed two pings")
	}
	if RelayFreshCredentialGrace < 30*time.Second {
		t.Error("fresh credential grace too short")
	}
	if RelayPrevBearerGrace+MaxSkew > RelayPrevBearerGraceMax {
		t.Error("prev grace plus skew exceeds max")
	}
	if RelayMaxConnsPerRV > RelayMaxConnsPerDevice {
		t.Error("per-rv conns exceed per-device")
	}
	if RelayDailyBytesPerRV > RelayDailyBytesPerDevice {
		t.Error("per-rv quota exceeds per-device")
	}
	if RelayCloseReplaced != 4000 || RelayClosePeerGone != 4004 {
		t.Error("close codes")
	}
}
