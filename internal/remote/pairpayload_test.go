package remote

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

const (
	tPin    = "vYMaGG1dwDptBdM/YlK+GhANzgrp66pRsZ/K22vrHto="
	tSecret = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"
	tRV     = "0123456789abcdef0123456789abcdef"
	tTok    = "XX4XJQ74NtekhCq2EFMdZTAkfluOKR1zYUtnfxqfecY"
)

func goodPayload() PairPayload {
	return PairPayload{V: 2, Pin: tPin, Addrs: []string{"192.168.1.20", "fd00::20"}, Port: 47821, Secret: tSecret,
		Relay: &PairRelayQR{URL: "https://hub.example.com", RV: tRV, Tok: tTok}}
}

func TestPairPayloadRoundTripAndBounds(t *testing.T) {
	p := goodPayload()
	s, err := EncodePairPayload(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s, PairPayloadPrefix) || len(s) > MaxPairPayloadBytes {
		t.Fatalf("bad encoding %q", s)
	}
	got, err := DecodePairPayload(s)
	if err != nil || !reflect.DeepEqual(got, p) {
		t.Fatalf("round trip: %v %+v", err, got)
	}

	relayOnly := goodPayload()
	relayOnly.Addrs, relayOnly.Port = nil, 0
	directOnly := goodPayload()
	directOnly.Relay = nil
	for _, ok := range []PairPayload{relayOnly, directOnly} {
		s, err := EncodePairPayload(ok)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodePairPayload(s); err != nil {
			t.Fatal(err)
		}
	}

	mut := func(f func(*PairPayload)) PairPayload { p := goodPayload(); f(&p); return p }
	bad := map[string]PairPayload{
		"version":    mut(func(p *PairPayload) { p.V = 1 }),
		"pin short":  mut(func(p *PairPayload) { p.Pin = "AAAA" }),
		"secret pad": mut(func(p *PairPayload) { p.Secret = tSecret + "=" }),
		"secret len": mut(func(p *PairPayload) { p.Secret = base64.RawURLEncoding.EncodeToString(make([]byte, 16)) }),
		"neither":    mut(func(p *PairPayload) { p.Addrs, p.Port, p.Relay = nil, 0, nil }),
		"nine addrs": mut(func(p *PairPayload) {
			p.Addrs = strings.Split(strings.Repeat("10.0.0.1,", 9)[:len(strings.Repeat("10.0.0.1,", 9))-1], ",")
		}),
		"zone":         mut(func(p *PairPayload) { p.Addrs = []string{"fe80::1%en0"} }),
		"hostname":     mut(func(p *PairPayload) { p.Addrs = []string{"mac.local"} }),
		"port zero":    mut(func(p *PairPayload) { p.Port = 0 }),
		"port big":     mut(func(p *PairPayload) { p.Port = 65536 }),
		"relay http":   mut(func(p *PairPayload) { p.Relay.URL = "http://hub.example.com" }),
		"relay rv":     mut(func(p *PairPayload) { p.Relay.RV = strings.ToUpper(tRV) }),
		"relay tok":    mut(func(p *PairPayload) { p.Relay.Tok = tTok[:42] }),
		"port no addr": mut(func(p *PairPayload) { p.Addrs = nil }),
	}
	for name, b := range bad {
		if _, err := EncodePairPayload(b); err == nil {
			t.Errorf("%s: encode accepted", name)
		} else if strings.Contains(err.Error(), tSecret) {
			t.Errorf("%s: error carries the secret", name)
		}
		raw, _ := json.Marshal(b)
		if _, err := DecodePairPayload(PairPayloadPrefix + base64.RawURLEncoding.EncodeToString(raw)); err == nil {
			t.Errorf("%s: decode accepted", name)
		}
	}

	// Oversize: an addrs list within limits cannot exceed 1024, so pad via unknown field on decode.
	big, _ := json.Marshal(map[string]any{"v": 2, "pin": tPin, "secret": tSecret,
		"relay": goodPayload().Relay, "pad": strings.Repeat("x", 1200)})
	if _, err := DecodePairPayload(PairPayloadPrefix + base64.RawURLEncoding.EncodeToString(big)); err == nil {
		t.Error("oversize decode accepted")
	}
	for _, s := range []string{"", PairPayloadPrefix, PairPayloadPrefix + "!!!", PairPayloadPrefix + "e30=", PairPayloadPrefix + "e30"} {
		if _, err := DecodePairPayload(s); err == nil {
			t.Errorf("%q accepted", s)
		}
	}

	// Unknown fields are tolerated.
	raw, _ := json.Marshal(map[string]any{"v": 2, "pin": tPin, "secret": tSecret, "relay": goodPayload().Relay, "future": 1})
	if _, err := DecodePairPayload(PairPayloadPrefix + base64.RawURLEncoding.EncodeToString(raw)); err != nil {
		t.Errorf("unknown field refused: %v", err)
	}
}

func TestPairPayloadRejectsLola1(t *testing.T) {
	for _, s := range []string{"lola1.eyJ2IjoxfQ", "lola1.", "https://example.com", "lola3.eyJ2IjozfQ"} {
		if _, err := DecodePairPayload(s); !errors.Is(err, ErrNotPairPayload) {
			t.Errorf("%q: %v", s, err)
		}
	}
}

func TestPairPayloadVectors(t *testing.T) {
	data, err := os.ReadFile("../protocol/testdata/pairing_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Payloads []struct {
			Name    string      `json:"name"`
			Encoded string      `json:"encoded"`
			Decoded PairPayload `json:"decoded"`
		} `json:"payloads"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Payloads) != 3 {
		t.Fatalf("want 3 payloads, got %d", len(f.Payloads))
	}
	for _, v := range f.Payloads {
		got, err := DecodePairPayload(v.Encoded)
		if err != nil || !reflect.DeepEqual(got, v.Decoded) {
			t.Errorf("%s decode: %v %+v", v.Name, err, got)
		}
		enc, err := EncodePairPayload(v.Decoded)
		if err != nil || enc != v.Encoded {
			t.Errorf("%s encode: %v\n got %s\nwant %s", v.Name, err, enc, v.Encoded)
		}
	}
}
