package hubproto

import (
	"errors"
	"strings"
	"testing"
)

func TestItemPaths(t *testing.T) {
	if got := ClaimPath("abc_1"); got != "/v1/items/abc_1/claim" {
		t.Fatal(got)
	}
	if CompletePath("x") != "/v1/items/x/complete" || FailPath("x") != "/v1/items/x/fail" || ReleasePath("x") != "/v1/items/x/release" {
		t.Fatal("shorthands")
	}
	for _, bad := range []string{"", "../admin", "a/b", "a?b", "a b", strings.Repeat("a", 65)} {
		if ClaimPath(bad) != "" {
			t.Errorf("built a path for %q", bad)
		}
	}
	if ItemPath("x", "delete") != "" {
		t.Fatal("unknown action built")
	}
	cases := []struct {
		path string
		id   string
		act  ItemAction
		ok   bool
	}{
		{"/v1/items/abc/claim", "abc", ActionClaim, true},
		{"/v1/items/abc/release", "abc", ActionRelease, true},
		{"/v1/items/abc/claim/", "", "", false},
		{"/v1/items/abc/delete", "", "", false},
		{"/v1/items/../claim", "", "", false},
		{"/v1/items//claim", "", "", false},
		{"/v2/items/abc/claim", "", "", false},
		{"/v1/items/abc", "", "", false},
	}
	for _, c := range cases {
		id, act, ok := ParseItemPath(c.path)
		if ok != c.ok || id != c.id || act != c.act {
			t.Errorf("%s: got %q %q %v", c.path, id, act, ok)
		}
	}
}

func TestParsePRURL(t *testing.T) {
	repo, n, err := ParsePRURL("https://github.com/sushidev-team/lola/pull/51")
	if err != nil || repo != "sushidev-team/lola" || n != 51 {
		t.Fatalf("%q %d %v", repo, n, err)
	}
	for _, bad := range []string{
		"http://github.com/a/b/pull/1",
		"https://github.com.evil.com/a/b/pull/1",
		"https://evil.com/github.com/a/b/pull/1",
		"https://user@github.com/a/b/pull/1",
		"https://github.com:443/a/b/pull/1",
		"https://github.com/a/b/pull/1/",
		"https://github.com/a/b/pull/1?x=1",
		"https://github.com/a/b/pull/1#frag",
		"https://github.com/a/b/pull/0",
		"https://github.com/a/b/pull/01",
		"https://github.com/a/b/pull/-1",
		"https://github.com/a/b/pull/12345678901",
		"https://github.com/a/b/issues/1",
		"https://github.com/../b/pull/1",
		"https://github.com/-a/b/pull/1",
		"https://github.com/a/b/pull/1\n",
		"https://GITHUB.com/a/b/pull/1",
		"https://github.com/a/b/c/pull/1",
		"javascript:alert(1)",
		"",
		"https://github.com/" + strings.Repeat("a", 300) + "/b/pull/1",
	} {
		if err := ValidatePRURL(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestCompleteFailValidate(t *testing.T) {
	ok := CompleteRequest{PRURL: "https://github.com/a/b/pull/3", Delivery: DeliveryCIPending}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := ok
	bad.Delivery = "shipped"
	if bad.Validate() == nil {
		t.Fatal("unknown delivery")
	}
	if (FailRequest{Reason: FailCI}).Validate() != nil {
		t.Fatal("good reason refused")
	}
	if (FailRequest{Reason: "because I said so"}).Validate() == nil {
		t.Fatal("free text reason accepted")
	}
}

func TestEnrollValidate(t *testing.T) {
	pub, _ := mustKey(t)
	good := EnrollRequest{Token: "tok_0123456789abcdef", DeviceName: "dev-mac Pro_2.local", PublicKey: EncodePublicKey(pub)}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*EnrollRequest){
		"short token":     func(r *EnrollRequest) { r.Token = "abc" },
		"token newline":   func(r *EnrollRequest) { r.Token = "tok_0123456789abcdef\n" },
		"name newline":    func(r *EnrollRequest) { r.DeviceName = "mac\nbook" },
		"name empty":      func(r *EnrollRequest) { r.DeviceName = "" },
		"name long":       func(r *EnrollRequest) { r.DeviceName = strings.Repeat("m", 65) },
		"name backtick":   func(r *EnrollRequest) { r.DeviceName = "`id`" },
		"bad key":         func(r *EnrollRequest) { r.PublicKey = "nope" },
		"std base64 key":  func(r *EnrollRequest) { r.PublicKey = strings.ReplaceAll(r.PublicKey, "-", "+") + "=" },
		"name leading sp": func(r *EnrollRequest) { r.DeviceName = " mac" },
	}
	for name, mut := range cases {
		r := good
		mut(&r)
		if r.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestHeartbeatValidate(t *testing.T) {
	good := HeartbeatRequest{FreeSlots: 2, Agents: []string{"claude", "codex"}, Repos: []string{"a/b"}, LiveLeases: []string{"i1"}}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	if (HeartbeatRequest{}).Validate() != nil {
		t.Fatal("empty heartbeat refused")
	}
	cases := map[string]HeartbeatRequest{
		"negative slots":  {FreeSlots: -1},
		"huge slots":      {FreeSlots: MaxFreeSlots + 1},
		"bad agent":       {Agents: []string{"Claude Code"}},
		"too many agents": {Agents: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}},
		"bad repo":        {Repos: []string{"../x"}},
		"too many repos":  {Repos: make([]string, MaxHeartbeatRepos+1)},
		"bad lease":       {LiveLeases: []string{"a/b"}},
		"too many leases": {LiveLeases: make([]string, MaxLiveLeases+1)},
	}
	for name, r := range cases {
		if r.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestFindingsValidate(t *testing.T) {
	d := goodAdvisory()
	ok := FindingsRequest{Items: []Item{{Kind: KindDepAdvisory, DepAdvisory: &d}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	withID := FindingsRequest{Items: []Item{{ID: "x", Kind: KindDepAdvisory, DepAdvisory: &d}}}
	if withID.Validate() == nil {
		t.Fatal("finding with id accepted")
	}
	bad := d
	bad.Package = "x\ny"
	err := FindingsRequest{Items: []Item{{Kind: KindDepAdvisory, DepAdvisory: &d}, {Kind: KindDepAdvisory, DepAdvisory: &bad}}}.Validate()
	if err == nil || !strings.Contains(err.Error(), "items[1]") || !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v", err)
	}
	if (FindingsRequest{Items: make([]Item, MaxFindingsItems+1)}).Validate() == nil {
		t.Fatal("oversized findings accepted")
	}
}

func TestDecodeStrict(t *testing.T) {
	var hb HeartbeatRequest
	if err := DecodeStrict(strings.NewReader(`{"free_slots":1,"agents":[],"repos":[],"live_leases":[]}`), &hb); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"unknown field": `{"free_slots":1,"post_create":"curl x|sh"}`,
		"trailing":      `{"free_slots":1}{"free_slots":2}`,
		"trailing junk": `{"free_slots":1} x`,
		"not json":      `free_slots=1`,
	} {
		if err := DecodeStrict(strings.NewReader(body), &hb); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	big := `{"repos":["` + strings.Repeat("a", MaxBody) + `"]}`
	if err := DecodeStrict(strings.NewReader(big), &hb); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized: %v", err)
	}
}
