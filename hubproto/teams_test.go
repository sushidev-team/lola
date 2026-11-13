package hubproto

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"
)

func adminRequest(t *testing.T, org string, body []byte) (*http.Request, []byte) {
	t.Helper()
	dpub, dpriv, _ := GenerateKey()
	req, err := http.NewRequest(http.MethodPost, "https://hub.example.com"+PathOrgSettings, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := Sign(req, DeviceID(dpub), dpriv, body); err != nil {
		t.Fatal(err)
	}
	return req, body
}

func TestAdminSignature(t *testing.T) {
	apub, apriv, _ := GenerateKey()
	body := []byte(`{"linear_team":"abc"}`)
	req, _ := adminRequest(t, "acme", body)
	if err := SignAdmin(req, "acme", KeyIDFor(apub), apriv, body); err != nil {
		t.Fatal(err)
	}
	if id, err := AdminKeyID(req.Header); err != nil || id != KeyIDFor(apub) {
		t.Fatalf("AdminKeyID = %q, %v", id, err)
	}
	if err := VerifyAdmin(req, body, "acme", apub); err != nil {
		t.Fatalf("VerifyAdmin: %v", err)
	}
	// Another org, another body, another key, another device: all refused.
	if VerifyAdmin(req, body, "other", apub) == nil {
		t.Error("verified for a different org")
	}
	if VerifyAdmin(req, []byte(`{"linear_team":"abd"}`), "acme", apub) == nil {
		t.Error("verified a different body")
	}
	opub, _, _ := GenerateKey()
	if VerifyAdmin(req, body, "acme", opub) == nil {
		t.Error("verified against a different key")
	}
	lifted, _ := adminRequest(t, "acme", body)
	lifted.Header.Set(HeaderAdminKey, req.Header.Get(HeaderAdminKey))
	lifted.Header.Set(HeaderAdminSignature, req.Header.Get(HeaderAdminSignature))
	if VerifyAdmin(lifted, body, "acme", apub) == nil {
		t.Error("an admin signature moved onto another device's request verified")
	}
}

func TestSignAdminNeedsDeviceSignature(t *testing.T) {
	_, apriv, _ := GenerateKey()
	req, _ := http.NewRequest(http.MethodPost, "https://hub.example.com"+PathOrgSettings, nil)
	if err := SignAdmin(req, "acme", "k1", apriv, nil); err != ErrNotSigned {
		t.Fatalf("err = %v, want ErrNotSigned", err)
	}
}

// An admin signature is not a device signature and not a policy signature:
// the signed string starts with its own domain.
func TestAdminCanonicalStringIsDomainSeparated(t *testing.T) {
	s := AdminCanonicalString("acme", strings.Repeat("a", 32), "POST", PathOrgSettings, "1", strings.Repeat("b", 32), nil)
	if !strings.HasPrefix(s, "lola-hub-admin-v1\nacme\n") {
		t.Fatalf("canonical string = %q", s)
	}
}

func TestCreateOrgRequestValidate(t *testing.T) {
	dpub, _, _ := GenerateKey()
	apub, _, _ := GenerateKey()
	ok := CreateOrgRequest{
		Code: strings.Repeat("c", 32), Org: "acme", DeviceName: "laptop",
		PublicKey: EncodePublicKey(dpub), AdminKeyID: KeyIDFor(apub), AdminPublicKey: EncodePublicKey(apub),
	}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*CreateOrgRequest){
		"code":       func(r *CreateOrgRequest) { r.Code = "short" },
		"org":        func(r *CreateOrgRequest) { r.Org = "Acme" },
		"device":     func(r *CreateOrgRequest) { r.DeviceName = "" },
		"public key": func(r *CreateOrgRequest) { r.PublicKey = "x" },
		"key id":     func(r *CreateOrgRequest) { r.AdminKeyID = "a:b" },
		"admin key":  func(r *CreateOrgRequest) { r.AdminPublicKey = "x" },
		"same key":   func(r *CreateOrgRequest) { r.AdminPublicKey = r.PublicKey },
	} {
		r := ok
		mut(&r)
		if r.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestOrgSettingsRequestValidate(t *testing.T) {
	p := func(s string) *string { return &s }
	good := []OrgSettingsRequest{
		{LinearKey: p("lin_api_" + strings.Repeat("a", 40))},
		{LinearKey: p("")},
		{LinearTeam: p("9cfb482a-81e3-4154-b5b9-2c805e70a02d")},
		{DigestWebhook: p("https://hooks.slack.com/services/T0001/B0001/abcDEF123")},
		{DigestWebhook: p(""), LinearTeam: p("")},
	}
	for i, r := range good {
		if err := r.Validate(); err != nil {
			t.Errorf("good[%d]: %v", i, err)
		}
	}
	bad := []OrgSettingsRequest{
		{},
		{LinearKey: p("hunter2")},
		{LinearKey: p("lin_api_" + strings.Repeat("a", 40) + "\n")},
		{LinearTeam: p("a b")},
		// The hub POSTs to the webhook by itself: only Slack's host, only
		// https, no port, no user info, nothing after the path.
		{DigestWebhook: p("http://hooks.slack.com/services/T0001/B0001/abc")},
		{DigestWebhook: p("https://169.254.169.254/latest/meta-data")},
		{DigestWebhook: p("https://hooks.slack.com.evil.example/services/T0001/B0001/abc")},
		{DigestWebhook: p("https://hooks.slack.com:8443/services/T0001/B0001/abc")},
		{DigestWebhook: p("https://user@hooks.slack.com/services/T0001/B0001/abc")},
		{DigestWebhook: p("https://hooks.slack.com/services/T0001/B0001/abc?x=1")},
		{DigestWebhook: p("https://hooks.slack.com/services/../../x")},
	}
	for i, r := range bad {
		if r.Validate() == nil {
			t.Errorf("bad[%d]: accepted", i)
		}
	}
}

func TestOrgSettingsViewValidate(t *testing.T) {
	v := OrgSettingsView{Org: "acme", LinearKeySet: true, LinearError: "linear: unauthorized",
		Teams: []LinearTeam{{ID: "t-1", Key: "ENG", Name: "Engineering & Ops"}, {ID: "t-2", Key: "OPS"}}}
	if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*OrgSettingsView){
		"org":       func(v *OrgSettingsView) { v.Org = "" },
		"class":     func(v *OrgSettingsView) { v.LinearError = "Unauthorized: <b>key</b>" },
		"digest":    func(v *OrgSettingsView) { v.DigestError = "x\ny" },
		"team id":   func(v *OrgSettingsView) { v.Teams = []LinearTeam{{ID: "a b", Key: "ENG"}} },
		"team key":  func(v *OrgSettingsView) { v.Teams = []LinearTeam{{ID: "t", Key: "eng"}} },
		"team name": func(v *OrgSettingsView) { v.Teams = []LinearTeam{{ID: "t", Key: "ENG", Name: "run `rm -rf`"}} },
		"too many":  func(v *OrgSettingsView) { v.Teams = make([]LinearTeam, MaxLinearTeams+1) },
	} {
		c := v
		mut(&c)
		if c.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestInviteFrames(t *testing.T) {
	if err := (InviteRequest{Label: "Ada"}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, r := range []InviteRequest{{}, {Label: "Ada", TTLSeconds: 5}, {Label: "Ada", TTLSeconds: MaxInviteTTL + 1}, {Label: "a\nb"}} {
		if r.Validate() == nil {
			t.Errorf("%+v: accepted", r)
		}
	}
	if (InviteResponse{Token: strings.Repeat("t", 32), ExpiresAt: time.Now()}).Validate() != nil {
		t.Error("good response refused")
	}
	if (InviteResponse{Token: "x", ExpiresAt: time.Now()}).Validate() == nil {
		t.Error("bad token accepted")
	}
	if (DeviceRevokeRequest{Device: "laptop-2"}).Validate() != nil || (DeviceRevokeRequest{}).Validate() == nil {
		t.Error("DeviceRevokeRequest validation")
	}
}

func TestInviteRoundTrip(t *testing.T) {
	pub, _, _ := GenerateKey()
	tok := strings.Repeat("T", 32)
	trusted := FormatTrustedKey("k1", pub)
	s := FormatInvite("https://hub.example.com/", tok, trusted)
	if s != "lola-hub:https://hub.example.com#"+tok+"#"+trusted {
		t.Fatalf("invite = %q", s)
	}
	inv, err := ParseInvite(" " + s + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if inv.URL != "https://hub.example.com" || inv.Token != tok || inv.Trusted() != trusted {
		t.Fatalf("parsed = %+v", inv)
	}
	bare, err := ParseInvite(FormatInvite("http://127.0.0.1:8099", tok, ""))
	if err != nil || bare.Trusted() != "" {
		t.Fatalf("bare invite: %+v, %v", bare, err)
	}
	for _, bad := range []string{
		"", tok, "lola-hub:https://hub.example.com", "lola-hub:http://hub.example.com#" + tok,
		"lola-hub:https://u:p@hub.example.com#" + tok, "lola-hub:https://hub.example.com#short",
		"lola-hub:https://hub.example.com#" + tok + "#k1:nope", "lola-hub:https://hub.example.com#" + tok + "#a#b",
		"lola-hub-create:https://hub.example.com#" + tok,
	} {
		if _, err := ParseInvite(bad); err == nil {
			t.Errorf("ParseInvite(%q) accepted", bad)
		} else if strings.Contains(err.Error(), tok) {
			t.Errorf("error echoes the token: %v", err)
		}
	}
	if FormatInvite("ftp://x", tok, "") != "" || FormatInvite("https://hub.example.com", "x", "") != "" || FormatInvite("https://hub.example.com", tok, "junk") != "" {
		t.Error("FormatInvite rendered a malformed part")
	}
}

func TestCreateCodeRoundTrip(t *testing.T) {
	code := strings.Repeat("C", 32)
	s := FormatCreateCode("https://hub.example.com", code)
	u, c, err := ParseCreateCode(s)
	if err != nil || u != "https://hub.example.com" || c != code {
		t.Fatalf("ParseCreateCode = %q, %q, %v", u, c, err)
	}
	for _, bad := range []string{"", code, "lola-hub:https://hub.example.com#" + code, "lola-hub-create:https://hub.example.com", "lola-hub-create:http://hub.example.com#" + code} {
		if _, _, err := ParseCreateCode(bad); err == nil {
			t.Errorf("ParseCreateCode(%q) accepted", bad)
		}
	}
}
