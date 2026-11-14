package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/hubproto"
)

func intp(v int) *int    { return &v }
func boolp(v bool) *bool { return &v }

func testHubKey(t *testing.T) string {
	t.Helper()
	pub, _, err := hubproto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return hubproto.EncodePublicKey(pub)
}

// A zero HubConfig — absent table, or a hand-built literal — must read as the
// SAFE defaults: sandbox required, 4 claims a day, 300s, every kind.
func TestHubZeroValueIsTheSafeDefault(t *testing.T) {
	var h HubConfig
	if !h.SandboxRequired() {
		t.Error("zero HubConfig must require a sandbox")
	}
	if h.ClaimsPerDay() != DefaultHubMaxClaimsPerDay {
		t.Errorf("ClaimsPerDay = %d, want %d", h.ClaimsPerDay(), DefaultHubMaxClaimsPerDay)
	}
	if h.EffectivePollSeconds() != DefaultHubPollSeconds {
		t.Errorf("EffectivePollSeconds = %d", h.EffectivePollSeconds())
	}
	for _, k := range hubproto.AllKinds() {
		if !h.AcceptsKind(k) {
			t.Errorf("empty accept_kinds should accept %q", k)
		}
	}
	if h.AcceptsKind("rm_rf") {
		t.Error("an unknown kind must never be accepted")
	}
	h.AcceptKinds = []string{string(hubproto.KindRedBumpPR)}
	if h.AcceptsKind(hubproto.KindDepAdvisory) || !h.AcceptsKind(hubproto.KindRedBumpPR) {
		t.Error("accept_kinds must narrow the accepted set")
	}
	h.MaxClaimsPerDay = intp(0)
	h.RequireSandbox = boolp(false)
	if h.ClaimsPerDay() != 0 || h.SandboxRequired() {
		t.Error("explicit 0 / false must be honoured")
	}
}

func TestHubAbsentTableIsNotWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c := &Config{Projects: []Project{validPoll()}}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "[hub]") || strings.Contains(string(data), "hub_chores") {
		t.Errorf("unconfigured hub leaked into the file:\n%s", data)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Hub, HubConfig{}) {
		t.Errorf("absent [hub] loaded as %+v", got.Hub)
	}
}

func TestHubRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	p := validPoll()
	p.Repo = "acme/web"
	p.HubChores = true
	c := &Config{Projects: []Project{p, validPoll()}}
	c.Projects[1].Name = "other"
	c.Hub = HubConfig{
		Enabled:           true,
		URL:               "https://hub.example.com",
		DeviceName:        "martins mac",
		AcceptKinds:       []string{"dep_advisory"},
		MaxClaimsPerDay:   intp(0),
		RequireSandbox:    boolp(false),
		TrustedPolicyKeys: map[string]string{"admin.1": testHubKey(t)},
		PollSeconds:       120,
	}
	if errs := c.validateHub(); len(errs) != 0 {
		t.Fatalf("validateHub: %v", errs)
	}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Hub, c.Hub) {
		t.Errorf("hub round trip:\n save %+v\n load %+v", c.Hub, got.Hub)
	}
	if !got.Projects[0].HubChores || got.Projects[1].HubChores {
		t.Errorf("hub_chores round trip: %v %v", got.Projects[0].HubChores, got.Projects[1].HubChores)
	}
	data, _ := os.ReadFile(path)
	if strings.Count(string(data), "hub_chores") != 1 {
		t.Errorf("hub_chores must be written only where set:\n%s", data)
	}
}

// Only keys that are SET are written: enabling the hub must not freeze the
// defaults (require_sandbox, max_claims_per_day, poll_seconds) into the file.
func TestHubSaveWritesOnlySetKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c := &Config{Projects: []Project{validPoll()}}
	c.Hub = HubConfig{Enabled: true, URL: "https://hub.example.com"}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, k := range []string{"require_sandbox", "max_claims_per_day", "poll_seconds", "accept_kinds", "trusted_policy_keys"} {
		if strings.Contains(string(data), k) {
			t.Errorf("unset key %s written:\n%s", k, data)
		}
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Hub.SandboxRequired() || got.Hub.ClaimsPerDay() != DefaultHubMaxClaimsPerDay {
		t.Errorf("defaults lost: %+v", got.Hub)
	}
}

func TestHubLoadFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	key := testHubKey(t)
	body := `
[hub]
enabled = true
url = "http://127.0.0.1:8080"
require_sandbox = false

[hub.trusted_policy_keys]
k1 = "` + key + `"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Hub.Enabled || c.Hub.SandboxRequired() || c.Hub.TrustedPolicyKeys["k1"] != key {
		t.Errorf("loaded %+v", c.Hub)
	}
	if got := c.Hub.TrustedKeyEntries(); len(got) != 1 || got[0] != "k1:"+key {
		t.Errorf("TrustedKeyEntries = %v", got)
	}
	if _, err := hubproto.ParseTrustedKeys(c.Hub.TrustedKeyEntries()); err != nil {
		t.Errorf("entries must parse: %v", err)
	}
}

// lola-hub prints a pinned key as hubproto.FormatTrustedKey entries in an
// array. The TOML library silently dropped that shape into an empty map, so a
// pinned key pinned nothing; it must load, and Save must rewrite it as the
// table form.
func TestHubTrustedKeysArrayForm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	key := testHubKey(t)
	body := `
[hub]
enabled = true
url = "http://127.0.0.1:8080"
trusted_policy_keys = ["k1:` + key + `"]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Hub.TrustedPolicyKeys["k1"] != key {
		t.Fatalf("array form not loaded: %+v", c.Hub.TrustedPolicyKeys)
	}
	if err := c.Validate(); err != nil && strings.Contains(err.Error(), "trusted_policy_keys") {
		t.Errorf("Validate: %v", err)
	}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[hub.trusted_policy_keys]") {
		t.Errorf("Save must write the table form:\n%s", data)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Hub.TrustedPolicyKeys["k1"] != key {
		t.Errorf("round trip lost the key: %+v", again.Hub.TrustedPolicyKeys)
	}
}

func TestHubTrustedKeysRejectsOtherShapes(t *testing.T) {
	for name, line := range map[string]string{
		"no colon":     `trusted_policy_keys = ["k1"]`,
		"empty id":     `trusted_policy_keys = [":abc"]`,
		"not a string": `trusted_policy_keys = [1]`,
		"duplicate":    `trusted_policy_keys = ["k1:a", "k1:b"]`,
		"scalar":       `trusted_policy_keys = "k1:a"`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte("[hub]\n"+line+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Errorf("%s: Load must fail", line)
			}
		})
	}
}

func TestValidateHubURL(t *testing.T) {
	ok := []string{
		"https://hub.example.com", "https://hub.example.com/", "https://hub.example.com:8443",
		"http://127.0.0.1:8080", "http://localhost:9000", "http://[::1]:80",
	}
	bad := []string{
		"", "hub.example.com", "http://hub.example.com", "http://10.0.0.1", "ftp://x",
		"https://user:pw@hub.example.com", "https://hub.example.com/api", "https://hub.example.com?x=1",
		"https://hub.example.com#f", "https://", "http://localhost.evil.com",
	}
	for _, u := range ok {
		if err := ValidateHubURL(u); err != nil {
			t.Errorf("%q: unexpected %v", u, err)
		}
	}
	for _, u := range bad {
		if err := ValidateHubURL(u); err == nil {
			t.Errorf("%q: accepted", u)
		}
	}
}

func TestValidateHub(t *testing.T) {
	key := testHubKey(t)
	cases := []struct {
		name string
		mod  func(c *Config)
		want string // substring; "" = valid
	}{
		{"absent", func(c *Config) {}, ""},
		{"enabled without url", func(c *Config) { c.Hub.Enabled = true }, "needs hub.url"},
		{"http remote", func(c *Config) { c.Hub.URL = "http://hub.example.com" }, "hub.url"},
		{"bad kind", func(c *Config) { c.Hub.AcceptKinds = []string{"shell"} }, "unknown kind"},
		{"dup kind", func(c *Config) { c.Hub.AcceptKinds = []string{"dep_advisory", "dep_advisory"} }, "twice"},
		{"claims negative", func(c *Config) { c.Hub.MaxClaimsPerDay = intp(-1) }, "max_claims_per_day"},
		{"claims huge", func(c *Config) { c.Hub.MaxClaimsPerDay = intp(1000) }, "max_claims_per_day"},
		{"claims zero ok", func(c *Config) { c.Hub.MaxClaimsPerDay = intp(0) }, ""},
		{"poll too fast", func(c *Config) { c.Hub.PollSeconds = 5 }, "poll_seconds"},
		{"poll too slow", func(c *Config) { c.Hub.PollSeconds = 901 }, "between 60 and 900 seconds"},
		{"poll ceiling ok", func(c *Config) { c.Hub.PollSeconds = 900 }, ""},
		{"bad device name", func(c *Config) { c.Hub.DeviceName = "-x\n" }, "device_name"},
		{"bad key id", func(c *Config) { c.Hub.TrustedPolicyKeys = map[string]string{"bad id": key} }, "key id"},
		{"bad key", func(c *Config) { c.Hub.TrustedPolicyKeys = map[string]string{"k1": "nope"} }, "not a base64url"},
		{"chores without repo", func(c *Config) { c.Projects[0].HubChores = true; c.Projects[0].Repo = "" }, "hub_chores needs repo"},
		{"chores bad repo", func(c *Config) { c.Projects[0].HubChores = true; c.Projects[0].Repo = "../.." }, "hub_chores needs repo"},
		{"chores ok", func(c *Config) { c.Projects[0].HubChores = true; c.Projects[0].Repo = "acme/web" }, ""},
		{"full ok", func(c *Config) {
			c.Hub = HubConfig{Enabled: true, URL: "https://h.example", AcceptKinds: []string{"red_bump_pr"},
				TrustedPolicyKeys: map[string]string{"k1": key}, PollSeconds: 60}
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{Projects: []Project{validPoll()}}
			c.Projects[0].Repo = "acme/web"
			tc.mod(c)
			errs := c.validateHub()
			if tc.want == "" {
				if len(errs) != 0 {
					t.Fatalf("unexpected: %v", errs)
				}
				return
			}
			found := false
			for _, e := range errs {
				if strings.Contains(e.Error(), tc.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("want error containing %q, got %v", tc.want, errs)
			}
		})
	}
}

// The config's device-name rule must agree with what hubproto's enroll accepts,
// or a name the config takes would be refused at enrollment.
func TestHubDeviceNameParity(t *testing.T) {
	pub := testHubKey(t)
	for _, n := range []string{"mac", "Martins MacBook Pro", "a.b_c-d", "", " lead", "x\x00", strings.Repeat("a", 64), strings.Repeat("a", 65), "ümlaut"} {
		req := hubproto.EnrollRequest{Token: strings.Repeat("t", 20), DeviceName: n, PublicKey: pub}
		if got, want := ValidHubDeviceName(n), req.Validate() == nil; got != want {
			t.Errorf("%q: config %v, hubproto %v", n, got, want)
		}
	}
}
